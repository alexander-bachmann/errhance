package errhance

import (
	"bytes"
	"context"
	"encoding/json"
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
	snapshots := make(map[string]projectSnapshot)
	groups := make(map[string]*projectLoad)
	selected := make(map[string]bool)
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
	var roots []string
	for root := range groups {
		roots = append(roots, root)
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
		return parser.ParseFile(fset, filename, src, parser.ParseComments|parser.AllErrors)
	}
	var preflightErr error
	active := make(map[string]bool)
	listed := make(map[string]bool)
	for _, root := range roots {
		group := groups[root]
		loadCfg, err := projectLoadConfig(ctx, group.dir)
		if err != nil {
			return report, reports, err
		}
		group.config = loadCfg
		parseCfg := *loadCfg
		parseCfg.ParseFile = parse
		pkgs, err := packages.Load(&parseCfg, group.patterns...)
		if ctx.Err() != nil {
			return report, reports, ctx.Err()
		}
		if err != nil {
			return report, reports, err
		}
		group.packages = pkgs
		// Prefer augmented test packages so alias selection sees test-only names.
		sort.SliceStable(group.packages, func(i, j int) bool {
			return len(group.packages[i].Syntax) > len(group.packages[j].Syntax)
		})
		if err := projectErrors(pkgs, &report); err != nil {
			preflightErr = err
		}
		for _, pkg := range pkgs {
			for _, path := range pkg.GoFiles {
				listed[path] = true
			}
			for _, file := range pkg.Syntax {
				active[pkg.Fset.PositionFor(file.Pos(), false).Filename] = true
			}
		}
	}
	var excluded bool
	for _, path := range paths {
		if !active[path] {
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
			} else if preflightErr != nil {
				message += " or unavailable after package errors"
			}
			d := Diagnostic{Position: token.Position{Filename: path}, Message: message}
			report.Diagnostics = append(report.Diagnostics, d)
			sr := reports[path]
			sr.Skipped, sr.Diagnostics = true, []Diagnostic{d}
			reports[path] = sr
			excluded = excluded || (explicit[path] && !listed[path])
		}
	}
	if preflightErr != nil {
		return report, reports, preflightErr
	}
	if excluded {
		return report, reports, fmt.Errorf("explicitly selected file excluded from the active build configuration")
	}
	var loaded []*packages.Package
	for _, root := range roots {
		loaded = append(loaded, groups[root].packages...)
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
			for _, file := range pkg.Syntax {
				path := pkg.Fset.PositionFor(file.Pos(), false).Filename
				if !selected[path] || matched[path] {
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
	if len(overlay) == 0 {
		return report, reports, ctx.Err()
	}
	// Check metadata support for every edit before the first replacement.
	for path := range overlay {
		original := snapshots[path]
		metadata, err := readFileMetadata(path, original.info)
		if err != nil {
			return report, reports, err
		}
		original.metadata = &metadata
		snapshots[path] = original
	}
	// Validate the entire loaded set, including unedited files and test variants.
	for _, root := range roots {
		group := groups[root]
		loadCfg := *group.config
		loadCfg.Overlay = overlay
		pkgs, err := packages.Load(&loadCfg, group.patterns...)
		if ctx.Err() != nil {
			return report, reports, ctx.Err()
		}
		if err != nil {
			return report, reports, err
		}
		if err := projectErrors(pkgs, &report); err != nil {
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
