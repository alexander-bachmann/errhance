package errhance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

type projectSnapshot struct {
	data     []byte
	info     os.FileInfo
	metadata *fileMetadata
}

type projectLoad struct {
	dir      string
	patterns []string
	packages []*packages.Package
	config   *packages.Config
	valid    map[string]bool
}

// safeFile deliberately rejects hard links as well as symlinks: replacing one
// name of a shared inode would silently leave its other names unmodified.
func safeFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("unsafe non-regular file %s", path)
	}
	if err := checkFileLinks(path, info); err != nil {
		return nil, err
	}
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		parent, err := os.Lstat(dir)
		if err != nil {
			return nil, err
		}
		if !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsafe ancestor %s", dir)
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return info, nil
}

func verifySnapshot(path string, original projectSnapshot) error {
	info, err := safeFile(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !os.SameFile(info, original.info) || info.Mode() != original.info.Mode() || !info.ModTime().Equal(original.info.ModTime()) || !bytes.Equal(data, original.data) {
		return fmt.Errorf("file changed during analysis: %s", path)
	}
	if original.metadata != nil {
		metadata, err := readFileMetadata(path, original.info)
		if err != nil {
			return err
		}
		if !sameFileMetadata(*original.metadata, metadata) {
			return fmt.Errorf("file metadata changed during analysis: %s", path)
		}
	}
	return nil
}

// atomicReplace is atomic for one file, not for a whole project. Checks reduce
// accidental races; callers must not run against a hostile, mutating filesystem.
// The boolean reports a committed rename even if cancellation follows it.
func atomicReplace(ctx context.Context, path string, original projectSnapshot, data []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := verifySnapshot(path, original); err != nil {
		return false, err
	}
	if original.metadata == nil {
		metadata, err := readFileMetadata(path, original.info)
		if err != nil {
			return false, err
		}
		original.metadata = &metadata
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".errhance-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return false, err
	}
	if err := applyFileMetadata(file, *original.metadata, original.info.Mode()); err != nil {
		file.Close()
		return false, err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	if err := verifySnapshot(path, original); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return false, err
	}
	// Cancellation cannot undo a committed rename. The caller must count it.
	return true, ctx.Err()
}

func rewriteProject(ctx context.Context, cfg SourceConfig, paths []string, explicit map[string]bool) (DirectoryReport, map[string]SourceReport, error) {
	var report DirectoryReport
	reports := make(map[string]SourceReport)
	validated := false
	defer func() {
		if !validated {
			for path, sr := range reports {
				sr.After, sr.TotalErrorsEnhanced = sr.Before, 0
				reports[path] = sr
			}
		}
	}()
	snapshots := make(map[string]projectSnapshot)
	groups := make(map[string]*projectLoad)
	selected := make(map[string]bool)
	unavailable := make(map[string]string)
	sort.Strings(paths)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return report, reports, err
		}
		if filepath.Ext(path) != ".go" {
			return report, reports, fmt.Errorf("not a Go source file: %s", path)
		}
		info, err := safeFile(path)
		if err != nil {
			return report, reports, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return report, reports, err
		}
		snapshots[path] = projectSnapshot{data: data, info: info}
		reports[path] = SourceReport{Before: string(data), After: string(data)}
		selected[path] = true
		dir := filepath.Dir(path)
		// Separate nested modules; load all selected directories in each module
		// together so package variants and imports share type identities.
		root := dir
		for parent := dir; ; parent = filepath.Dir(parent) {
			if _, err := os.Stat(filepath.Join(parent, "go.mod")); err == nil {
				root = parent
				break
			}
			if filepath.Dir(parent) == parent {
				break
			}
		}
		group := groups[root]
		if group == nil {
			group = &projectLoad{dir: root}
			groups[root] = group
		}
		pattern := "."
		if rel, _ := filepath.Rel(root, dir); rel != "." {
			pattern = "./" + filepath.ToSlash(rel)
		}
		found := false
		for _, existing := range group.patterns {
			found = found || existing == pattern
		}
		if !found {
			group.patterns = append(group.patterns, pattern)
		}
	}
	if len(paths) == 0 {
		return report, reports, ctx.Err()
	}
	// On-save no-ops need no package driver or dependency type checking. This
	// deliberately overestimates candidates; any possible edit takes the typed path.
	if len(paths) == 1 && explicit[paths[0]] {
		path := paths[0]
		candidate, err := hasRewriteCandidate(reports[path].Before, cfg)
		if err != nil {
			return report, reports, err
		}
		if !candidate {
			file, _ := parser.ParseFile(token.NewFileSet(), path, snapshots[path].data, parser.PackageClauseOnly|parser.ParseComments)
			sr := reports[path]
			sr.Skipped = !cfg.generated && file != nil && ast.IsGenerated(file)
			reports[path] = sr
			return report, reports, ctx.Err()
		}
	}
	// Query Go once per module, then share one load across actual workspace
	// members. Never silently opt unlisted modules out of an active workspace.
	workspaces := make(map[string]map[string]bool)
	consolidated := make(map[string]*projectLoad)
	var moduleRoots []string
	for root := range groups {
		moduleRoots = append(moduleRoots, root)
	}
	sort.Strings(moduleRoots)
	for _, root := range moduleRoots {
		group := groups[root]
		loadCfg, err := projectLoadConfig(ctx, root)
		if err != nil {
			return report, reports, err
		}
		group.config = loadCfg
		key := root
		for _, env := range loadCfg.Env {
			if !strings.HasPrefix(env, "GOWORK=") {
				continue
			}
			work := strings.TrimPrefix(env, "GOWORK=")
			if work == "" || work == "off" {
				continue
			}
			members, ok := workspaces[work]
			if !ok {
				data, err := os.ReadFile(work)
				if err != nil {
					return report, reports, err
				}
				parsed, err := modfile.ParseWork(work, data, nil)
				if err != nil {
					return report, reports, err
				}
				members = make(map[string]bool)
				for _, use := range parsed.Use {
					dir := use.Path
					if !filepath.IsAbs(dir) {
						dir = filepath.Join(filepath.Dir(work), dir)
					}
					members[filepath.Clean(dir)] = true
				}
				workspaces[work] = members
			}
			if !members[root] {
				for _, pattern := range group.patterns {
					dir := filepath.Join(root, filepath.FromSlash(pattern))
					for _, path := range paths {
						if filepath.Dir(path) == dir {
							unavailable[path] = fmt.Sprintf("module %s is not listed in active workspace %s; file left unchanged", root, work)
						}
					}
				}
				key = ""
			} else {
				key = work
			}
			break
		}
		if key == "" {
			continue
		}
		target := consolidated[key]
		if target == nil {
			target = &projectLoad{dir: root, config: loadCfg}
			consolidated[key] = target
		}
		for _, pattern := range group.patterns {
			target.patterns = append(target.patterns, filepath.Join(root, filepath.FromSlash(pattern)))
		}
	}
	groups = consolidated
	var roots []string
	for root := range groups {
		roots = append(roots, root)
		sort.Strings(groups[root].patterns)
	}
	sort.Strings(roots)
	var mu sync.Mutex
	parse := func(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
		mu.Lock()
		defer mu.Unlock()
		if snapshot, ok := snapshots[filename]; ok {
			src = snapshot.data
		} else {
			info, err := safeFile(filename)
			if err != nil {
				return nil, err
			}
			if src == nil {
				src, err = os.ReadFile(filename)
				if err != nil {
					return nil, err
				}
			}
			snapshots[filename] = projectSnapshot{data: append([]byte(nil), src...), info: info}
		}
		return parser.ParseFile(fset, filename, src, parser.ParseComments|parser.AllErrors|parser.SkipObjectResolution)
	}
	broken := len(unavailable) != 0
	active := make(map[string]bool)
	listed := make(map[string]bool)
	for _, root := range roots {
		group := groups[root]
		parseCfg := *group.config
		parseCfg.ParseFile = parse
		pkgs, err := packages.Load(&parseCfg, group.patterns...)
		if ctx.Err() != nil {
			return report, reports, ctx.Err()
		}
		if err != nil {
			broken = true
			for _, path := range paths {
				for _, pattern := range group.patterns {
					if filepath.Dir(path) == pattern {
						unavailable[path] = fmt.Sprintf("package load failed: %v; file left unchanged", err)
					}
				}
			}
			continue
		}
		group.packages = pkgs
		// Prefer augmented test packages so alias selection sees test-only names.
		sort.SliceStable(group.packages, func(i, j int) bool {
			return len(group.packages[i].Syntax) > len(group.packages[j].Syntax)
		})
		if err := projectErrors(pkgs, &report); err != nil {
			broken = true
		}
		group.valid = make(map[string]bool)
		for _, pkg := range pkgs {
			valid := projectPackageValid(pkg)
			if valid {
				group.valid[pkg.ID] = true
			}
			for _, path := range pkg.GoFiles {
				listed[path] = true
				if !valid {
					broken = true
					unavailable[path] = fmt.Sprintf("package %s or its dependencies failed typechecking; file left unchanged", pkg.ID)
				}
			}
			for _, path := range pkg.CompiledGoFiles {
				if !valid {
					unavailable[path] = fmt.Sprintf("package %s or its dependencies failed typechecking; file left unchanged", pkg.ID)
				}
			}
			for _, file := range pkg.Syntax {
				active[pkg.Fset.PositionFor(file.Pos(), false).Filename] = true
			}
		}
	}
	var excluded bool
	usable := 0
	for _, path := range paths {
		if !active[path] || unavailable[path] != "" {
			message := "file excluded from the active build configuration"
			if listed[path] {
				message = "active source mapping is unsupported; file left unchanged"
				file, _ := parser.ParseFile(token.NewFileSet(), path, snapshots[path].data, parser.ImportsOnly)
				if file != nil {
					for _, imp := range file.Imports {
						if imp.Path.Value == `"C"` || imp.Path.Value == "`C`" {
							message = "active cgo source mapping is unsupported; file left unchanged"
						}
					}
				}
			} else if broken {
				message += " or unavailable after package errors"
			}
			if unavailable[path] != "" {
				message = unavailable[path]
			}
			d := Diagnostic{Position: token.Position{Filename: path}, Message: message}
			report.Diagnostics = append(report.Diagnostics, d)
			sr := reports[path]
			sr.Skipped, sr.Diagnostics = true, []Diagnostic{d}
			reports[path] = sr
			excluded = excluded || explicit[path]
		} else {
			usable++
		}
	}
	if excluded {
		return report, reports, fmt.Errorf("explicitly selected file unavailable in the active build configuration or failed typechecking")
	}
	if usable == 0 && broken {
		return report, reports, fmt.Errorf("no selected files in loadable, type-correct packages")
	}
	var loaded []*packages.Package
	for _, root := range roots {
		for _, pkg := range groups[root].packages {
			if groups[root].valid[pkg.ID] {
				loaded = append(loaded, pkg)
			}
		}
	}
	blocked, contractDiagnostics := projectContracts(loaded)
	cfg.blockedFunctions = blocked
	report.Diagnostics = append(report.Diagnostics, contractDiagnostics...)
	for _, d := range contractDiagnostics {
		if sr, ok := reports[d.Position.Filename]; ok {
			sr.Diagnostics = append(sr.Diagnostics, d)
			reports[d.Position.Filename] = sr
		}
	}
	matched := make(map[string]bool)
	overlay := make(map[string][]byte)
	for _, root := range roots {
		for _, pkg := range groups[root].packages {
			if !groups[root].valid[pkg.ID] {
				continue
			}
			for _, file := range pkg.Syntax {
				path := pkg.Fset.PositionFor(file.Pos(), false).Filename
				if !selected[path] || matched[path] || unavailable[path] != "" {
					continue
				}
				matched[path] = true
				sr := reports[path]
				sr.Skipped = ast.IsGenerated(file) && !cfg.generated
				after, diagnostics, count, err := rewriteTyped(pkg.Fset, file, pkg.TypesInfo, sr.Before, cfg)
				sr.Diagnostics = append(sr.Diagnostics, diagnostics...)
				report.Diagnostics = append(report.Diagnostics, diagnostics...)
				reports[path] = sr
				if err != nil {
					return report, reports, err
				}
				if after == sr.Before {
					continue
				}
				// rewriteTyped inserts the required import with its actual binding.
				// Do not let goimports guess module package names from our process cwd.
				formatted, err := imports.Process(path, []byte(after), &imports.Options{Comments: true, TabIndent: true, TabWidth: 8, FormatOnly: true})
				if err != nil {
					return report, reports, fmt.Errorf("imports %s: %w", path, err)
				}
				sr.After, sr.TotalErrorsEnhanced = string(formatted), count
				reports[path] = sr
				overlay[path] = formatted
			}
		}
	}
	// Validation reuses original imports and ASTs, but needs no expression/use maps.
	// Drop those large maps before checking output bodies.
	loaded = nil
	for _, group := range groups {
		packages.Visit(group.packages, func(pkg *packages.Package) bool {
			pkg.TypesInfo = nil
			return true
		}, nil)
	}
	if len(overlay) == 0 {
		return report, reports, ctx.Err()
	}
	// Preview checks source validity, not replacement permissions or metadata.
	if !cfg.dryRun {
		for path := range overlay {
			original := snapshots[path]
			metadata, err := readFileMetadata(path, original.info)
			if err != nil {
				return report, reports, err
			}
			original.metadata = &metadata
			snapshots[path] = original
		}
	}
	// Recheck changed bodies in memory. Unchanged declarations ensure that original
	// dependency types remain valid; unusual source mappings use loader validation.
	for _, root := range roots {
		group := groups[root]
		if len(group.valid) == 0 {
			continue
		}
		groupOverlay := make(map[string][]byte)
		for _, pkg := range group.packages {
			for _, path := range pkg.GoFiles {
				if data, ok := overlay[path]; ok {
					groupOverlay[path] = data
				}
			}
		}
		var validationReport DirectoryReport
		err := validateRewrites(ctx, group.packages, group.valid, groupOverlay, &validationReport)
		group.packages = nil
		if !errors.Is(err, errRewriteValidationUnsupported) {
			report.Diagnostics = append(report.Diagnostics, validationReport.Diagnostics...)
			if err != nil {
				return report, reports, fmt.Errorf("transformed packages: %w", err)
			}
			continue
		}
		loadCfg := *group.config
		loadCfg.Overlay = overlay
		pkgs, err := packages.Load(&loadCfg, group.patterns...)
		if ctx.Err() != nil {
			return report, reports, ctx.Err()
		}
		if err != nil {
			return report, reports, err
		}
		if err := validateProjectRoots(pkgs, group.valid, &report); err != nil {
			return report, reports, fmt.Errorf("transformed packages: %w", err)
		}
	}
	// Preflight every source read before the first write, then recheck each target
	// immediately before replacement. No-op files are never reformatted or touched.
	for path, original := range snapshots {
		if err := verifySnapshot(path, original); err != nil {
			return report, reports, err
		}
	}
	if err := ctx.Err(); err != nil {
		return report, reports, err
	}
	if cfg.dryRun {
		validated = true
		for _, path := range paths {
			if _, changed := overlay[path]; !changed {
				continue
			}
			sr := reports[path]
			report.Changes = append(report.Changes, FileChange{Path: path, Before: sr.Before, After: sr.After})
			report.ProposedFilesModified++
			report.ProposedErrorsEnhanced += sr.TotalErrorsEnhanced
		}
		return report, reports, nil
	}
	validated = true
	for _, path := range paths {
		data, changed := overlay[path]
		if !changed {
			continue
		}
		if err := ctx.Err(); err != nil {
			return report, reports, err
		}
		committed, err := atomicReplace(ctx, path, snapshots[path], data)
		if committed {
			delete(overlay, path)
			report.TotalFilesModified++
			report.TotalErrorsEnhanced += reports[path].TotalErrorsEnhanced
		}
		if err != nil {
			return report, reports, fmt.Errorf("write %s: %w", path, err)
		}
	}
	return report, reports, ctx.Err()
}

func projectConfig(ctx context.Context, dir string) *packages.Config {
	// Prevent implicit downloads, toolchain installation, and module-file edits.
	env := append(os.Environ(), "GOPROXY=off", "GONOPROXY=none", "GOSUMDB=off", "GOTOOLCHAIN=local")
	return &packages.Config{Context: ctx, Dir: dir, Tests: true,
		Mode: packages.LoadSyntax | packages.NeedModule, Env: env, BuildFlags: []string{"-mod=readonly"}}
}

func projectLoadConfig(ctx context.Context, dir string) (*packages.Config, error) {
	cfg := projectConfig(ctx, dir)
	cmd := exec.CommandContext(ctx, "go", "env", "-json", "GOWORK", "GOFLAGS")
	cmd.Dir, cmd.Env = dir, cfg.Env
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("read Go build environment: %w", err)
	}
	var env struct{ GOWORK, GOFLAGS string }
	if err := json.Unmarshal(output, &env); err != nil {
		return nil, err
	}
	// Pin the effective workspace for both initial and overlay loads. Remove
	// inherited entries so grouping sees exactly the value returned by Go.
	var buildEnv []string
	for _, value := range cfg.Env {
		if !strings.HasPrefix(value, "GOWORK=") {
			buildEnv = append(buildEnv, value)
		}
	}
	if env.GOWORK == "" {
		env.GOWORK = "off"
	}
	cfg.Env = append(buildEnv, "GOWORK="+env.GOWORK)
	mode := ""
	// GOFLAGS uses Go's quoted-word syntax, not shell escaping. Quotes may
	// surround a whole flag, including values containing whitespace.
	for flags := strings.TrimSpace(env.GOFLAGS); flags != ""; flags = strings.TrimSpace(flags) {
		var flag string
		if flags[0] == '\'' || flags[0] == '"' {
			end := strings.IndexByte(flags[1:], flags[0])
			if end < 0 {
				return nil, fmt.Errorf("unterminated GOFLAGS quote")
			}
			flag, flags = flags[1:end+1], flags[end+2:]
		} else {
			end := strings.IndexAny(flags, " \t\r\n")
			if end < 0 {
				end = len(flags)
			}
			flag, flags = flags[:end], flags[end:]
		}
		if strings.HasPrefix(flag, "-mod=") || flag == "-mod" {
			if flag != "-mod=vendor" && flag != "-mod=readonly" {
				return nil, fmt.Errorf("unsafe GOFLAGS module mode %q; use vendor or readonly", flag)
			}
			mode = flag
		}
	}
	if mode == "" {
		vendorRoot := dir
		if env.GOWORK != "" && env.GOWORK != "off" {
			vendorRoot = filepath.Dir(env.GOWORK)
		}
		mode = "-mod=readonly"
		if _, err := os.Stat(filepath.Join(vendorRoot, "vendor", "modules.txt")); err == nil {
			mode = "-mod=vendor"
		}
	}
	cfg.BuildFlags = []string{mode}
	return cfg, nil
}

func projectPackageValid(pkg *packages.Package) bool {
	return !pkg.IllTyped && len(pkg.Errors) == 0 && pkg.Types != nil && pkg.Types.Complete() &&
		pkg.TypesInfo != nil && pkg.TypesInfo.Types != nil && pkg.TypesInfo.Defs != nil && pkg.TypesInfo.Uses != nil
}

// Baseline failures are tolerated, but every previously valid root (including
// unedited dependents and test variants) must survive overlay validation.
func validateProjectRoots(pkgs []*packages.Package, baseline map[string]bool, report *DirectoryReport) error {
	present := make(map[string]*packages.Package)
	for _, pkg := range pkgs {
		present[pkg.ID] = pkg
	}
	var failed []string
	for id := range baseline {
		pkg := present[id]
		if pkg == nil || !projectPackageValid(pkg) {
			failed = append(failed, id)
		}
	}
	sort.Strings(failed)
	for _, id := range failed {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{Message: "previously valid package missing or failed typechecking: " + id})
		if pkg := present[id]; pkg != nil {
			_ = projectErrors([]*packages.Package{pkg}, report)
		}
	}
	if len(failed) != 0 {
		return fmt.Errorf("previously valid packages failed validation: %s", strings.Join(failed, ", "))
	}
	return nil
}

func projectErrors(pkgs []*packages.Package, report *DirectoryReport) error {
	var messages []string
	roots := make(map[*packages.Package]bool)
	for _, pkg := range pkgs {
		roots[pkg] = true
	}
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		for _, err := range pkg.Errors {
			// A walked directory can contain only build-excluded files. Report
			// those per file, not as broken packages; dependency errors still fail.
			if roots[pkg] && len(pkg.GoFiles) == 0 && err.Kind == packages.ListError &&
				(strings.Contains(err.Msg, "build constraints exclude all Go files") || strings.Contains(err.Msg, "no Go files in")) {
				continue
			}
			messages = append(messages, err.Error())
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Message: err.Error()})
		}
	})
	if len(messages) != 0 {
		return fmt.Errorf("package typecheck failed: %s", strings.Join(messages, "\n"))
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("no active packages loaded")
	}
	return nil
}
