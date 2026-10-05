package errhance

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// errRewriteValidationUnsupported permits the caller to use loader validation.
// Invariant violations and type errors do not wrap this sentinel.
var errRewriteValidationUnsupported = errors.New("in-memory rewrite validation unsupported")

type rewriteImporter func(string) (*types.Package, error)

func (f rewriteImporter) Import(path string) (*types.Package, error) { return f(path) }

// validateRewrites validates body-only rewrites without rebuilding dependency
// exports. Call with one initial load's graph and only that load's overlay files.
// Original ASTs and types are never modified. Parsing appends validation-only
// token files to pkg.Fset; original positions and offsets remain valid.
func validateRewrites(ctx context.Context, pkgs []*packages.Package, baseline map[string]bool, overlay map[string][]byte, report *DirectoryReport) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	present := make(map[string]*packages.Package)
	var graph []*packages.Package
	var fmtTypes *types.Package
	packages.Visit(pkgs, func(pkg *packages.Package) bool {
		present[pkg.ID] = pkg
		graph = append(graph, pkg)
		if pkg.PkgPath == "fmt" && pkg.Types != nil && pkg.Types.Complete() && !pkg.IllTyped {
			fmtTypes = pkg.Types
		}
		return true
	}, nil)
	for id, valid := range baseline {
		if valid && (present[id] == nil || present[id].Types == nil || !present[id].Types.Complete() || present[id].IllTyped || len(present[id].Errors) != 0) {
			return fmt.Errorf("previously valid package missing or invalid: %s", id)
		}
	}
	type check struct {
		pkg   *packages.Package
		files []*ast.File
	}
	var checks []check
	seen := make(map[string]bool)
	sort.Slice(graph, func(i, j int) bool { return graph[i].ID < graph[j].ID })
	// Establish the invariant everywhere before using any original exports.
	for _, pkg := range graph {
		if err := ctx.Err(); err != nil {
			return err
		}
		changed := false
		for _, path := range append(append([]string(nil), pkg.GoFiles...), pkg.CompiledGoFiles...) {
			if _, ok := overlay[path]; ok {
				changed = true
			}
		}
		if !changed {
			continue
		}
		unsupported := func(reason string) error {
			return fmt.Errorf("%w: %s: %s", errRewriteValidationUnsupported, pkg.ID, reason)
		}
		if pkg.Fset == nil || pkg.Types == nil || !pkg.Types.Complete() || pkg.IllTyped || len(pkg.Errors) != 0 || pkg.TypesSizes == nil || len(pkg.Syntax) != len(pkg.CompiledGoFiles) {
			return unsupported("incomplete initial syntax/types/sizes")
		}
		paths := make(map[string]bool)
		for _, path := range pkg.GoFiles {
			paths[path] = true
		}
		files := append([]*ast.File(nil), pkg.Syntax...)
		for i, file := range files {
			path := pkg.Fset.PositionFor(file.Pos(), false).Filename
			if !paths[path] || path != pkg.CompiledGoFiles[i] {
				return unsupported("generated or cgo source mapping")
			}
			delete(paths, path)
			for _, imp := range file.Imports {
				name, _ := strconv.Unquote(imp.Path.Value)
				if name == "C" {
					return unsupported("cgo import")
				}
				if name != "unsafe" {
					dep := pkg.Imports[name]
					if dep == nil || dep.Types == nil || !dep.Types.Complete() || dep.IllTyped {
						return unsupported("missing original import mapping: " + name)
					}
				}
			}
			data, ok := overlay[path]
			if !ok {
				continue
			}
			seen[path] = true
			next, err := parser.ParseFile(pkg.Fset, path, data, parser.ParseComments|parser.AllErrors|parser.SkipObjectResolution)
			if err != nil {
				return fmt.Errorf("transformed source %s: %w", path, err)
			}
			if err := rewriteInvariant(file, next); err != nil {
				return fmt.Errorf("transformed source %s: %w", path, err)
			}
			files[i] = next
		}
		if len(paths) != 0 {
			return unsupported("unmapped original source")
		}
		checks = append(checks, check{pkg, files})
	}
	for path := range overlay {
		if !seen[path] {
			return fmt.Errorf("%w: overlay source not mapped: %s", errRewriteValidationUnsupported, path)
		}
	}
	for _, item := range checks {
		if err := ctx.Err(); err != nil {
			return err
		}
		pkg := item.pkg
		version := pkg.Types.GoVersion()
		if version == "" && pkg.Module != nil && pkg.Module.GoVersion != "" {
			version = "go" + strings.TrimPrefix(pkg.Module.GoVersion, "go")
		}
		if version == "" {
			return fmt.Errorf("%w: %s: unknown Go version", errRewriteValidationUnsupported, pkg.ID)
		}
		cfg := types.Config{GoVersion: version, Sizes: pkg.TypesSizes}
		cfg.Importer = rewriteImporter(func(path string) (*types.Package, error) {
			if dep := pkg.Imports[path]; dep != nil {
				if dep.Types != nil && dep.Types.Complete() && !dep.IllTyped {
					return dep.Types, nil
				}
				return nil, fmt.Errorf("%w: incomplete original import %q", errRewriteValidationUnsupported, path)
			}
			if path == "unsafe" {
				return types.Unsafe, nil
			}
			if path == "fmt" {
				if fmtTypes == nil {
					// Only newly added fmt.Errorf is allowed: its signature uses
					// builtin types, so no foreign named type identity can escape
					// this separate importer graph into the original graph.
					var err error
					fmtTypes, err = importer.Default().Import("fmt")
					if err != nil {
						return nil, fmt.Errorf("%w: import fmt: %v", errRewriteValidationUnsupported, err)
					}
				}
				return fmtTypes, nil
			}
			return nil, fmt.Errorf("%w: missing original import %q", errRewriteValidationUnsupported, path)
		})
		var importFailure error
		originalImporter := cfg.Importer
		cfg.Importer = rewriteImporter(func(path string) (*types.Package, error) {
			dep, err := originalImporter.Import(path)
			if err != nil {
				importFailure = err
			}
			return dep, err
		})
		cfg.Error = func(err error) {
			if report == nil {
				return
			}
			d := Diagnostic{Message: err.Error()}
			if e, ok := err.(types.Error); ok {
				d.Position, d.Message = pkg.Fset.PositionFor(e.Pos, false), e.Msg
			}
			report.Diagnostics = append(report.Diagnostics, d)
		}
		// No Info maps: this pass checks full bodies but retains no use/def data.
		_, err := cfg.Check(pkg.PkgPath, pkg.Fset, item.files, nil)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if importFailure != nil {
			return importFailure
		}
		if err != nil {
			return fmt.Errorf("transformed package %s: %w", pkg.ID, err)
		}
	}
	return ctx.Err()
}

func rewriteInvariant(before, after *ast.File) error {
	if rewriteFingerprint(before) != rewriteFingerprint(after) {
		return errors.New("rewrite changes declarations, initializers, or build directives")
	}
	imports := func(file *ast.File) map[string]int {
		result := make(map[string]int)
		for _, imp := range file.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			alias := ""
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			result[path+"\x00"+alias]++
		}
		return result
	}
	old, next := imports(before), imports(after)
	for key, count := range old {
		if next[key] < count {
			return errors.New("rewrite removes or renames an original import")
		}
		next[key] -= count
	}
	added := ""
	for key, count := range next {
		if count == 0 {
			continue
		}
		parts := strings.Split(key, "\x00")
		if parts[0] != "fmt" || count != 1 || added != "" || parts[1] == "." || parts[1] == "_" {
			return errors.New("rewrite may only add a named fmt import")
		}
		added = parts[1]
		if added == "" {
			added = "fmt"
		}
	}
	if added != "" {
		// New bindings must not change the meaning of an unchanged declaration.
		if strings.Contains(rewriteFingerprint(before), strconv.Quote(added)) {
			return errors.New("new fmt binding occurs in original declarations")
		}
		allowed := make(map[*ast.Ident]bool)
		ast.Inspect(after, func(n ast.Node) bool {
			if imp, ok := n.(*ast.ImportSpec); ok {
				allowed[imp.Name] = true
			}
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Errorf" {
					if id, ok := sel.X.(*ast.Ident); ok {
						allowed[id] = true
					}
				}
			}
			return true
		})
		valid := true
		ast.Inspect(after, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == added && !allowed[id] {
				valid = false
			}
			return true
		})
		if !valid {
			return errors.New("new fmt import may only be used for Errorf calls")
		}
	}
	return nil
}

// Fingerprint all declarations, not just exported ones: private declarations can
// determine exported inferred types. Function literal signatures remain included
// even in var initializers. Reflection avoids cloning or mutating the source AST.
func rewriteFingerprint(file *ast.File) string {
	var out strings.Builder
	var visit func(reflect.Value)
	visit = func(v reflect.Value) {
		if !v.IsValid() || v.Type() == reflect.TypeOf(token.Pos(0)) || v.Type() == reflect.TypeOf((*ast.CommentGroup)(nil)) || v.Type() == reflect.TypeOf((*ast.Object)(nil)) {
			return
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if !v.IsNil() {
				visit(v.Elem())
			}
		case reflect.Struct:
			if v.Type() == reflect.TypeOf(ast.BasicLit{}) {
				literal := v.Interface().(ast.BasicLit)
				// gofmt normalizes forms such as 1E3 and 0XFF without changing
				// their types or values. Do not reject an ordinary on-save edit.
				out.WriteString("BasicLit{" + strconv.Itoa(int(literal.Kind)) + ";")
				out.WriteString(strconv.Quote(constant.MakeFromLiteral(literal.Value, literal.Kind, 0).ExactString()))
				out.WriteByte('}')
				return
			}
			out.WriteString(v.Type().Name() + "{")
			for i := 0; i < v.NumField(); i++ {
				// Assign is a position, but its presence distinguishes an alias
				// from a defined type and therefore changes type identity.
				if v.Type() == reflect.TypeOf(ast.TypeSpec{}) && v.Type().Field(i).Name == "Assign" {
					out.WriteString(strconv.FormatBool(v.Field(i).Int() != 0))
					continue
				}
				if v.Type() == reflect.TypeOf(ast.CallExpr{}) && v.Type().Field(i).Name == "Ellipsis" {
					out.WriteString(strconv.FormatBool(v.Field(i).Int() != 0))
					continue
				}
				if v.Type().Field(i).Name == "Body" && (v.Type() == reflect.TypeOf(ast.FuncDecl{}) || v.Type() == reflect.TypeOf(ast.FuncLit{})) {
					continue
				}
				visit(v.Field(i))
				out.WriteByte(';')
			}
			out.WriteByte('}')
		case reflect.Slice:
			out.WriteByte('[')
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
				out.WriteByte(',')
			}
			out.WriteByte(']')
		case reflect.String:
			out.WriteString(strconv.Quote(v.String()))
		case reflect.Int:
			out.WriteString(strconv.FormatInt(v.Int(), 10))
		case reflect.Bool:
			out.WriteString(strconv.FormatBool(v.Bool()))
		}
	}
	visit(reflect.ValueOf(file.Name))
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			continue
		}
		visit(reflect.ValueOf(decl))
	}
	var modern, legacy constraint.Expr
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if comment.Pos() < file.Package && (constraint.IsGoBuild(comment.Text) || constraint.IsPlusBuild(comment.Text)) {
				isModern := constraint.IsGoBuild(comment.Text)
				// Legacy tags attached to package documentation have no separating
				// blank line and are not effective build constraints.
				if !isModern && group == file.Doc {
					continue
				}
				expr, err := constraint.Parse(comment.Text)
				if err == nil {
					if isModern && modern == nil {
						modern = expr
					} else if !isModern {
						if legacy == nil {
							legacy = expr
						} else {
							legacy = &constraint.AndExpr{X: legacy, Y: expr}
						}
					} else {
						out.WriteString(strconv.Quote(comment.Text)) // Invalid duplicate.
					}
					continue
				}
			}
			if strings.HasPrefix(comment.Text, "//go:") || strings.HasPrefix(comment.Text, "// +build") {
				out.WriteString(strconv.Quote(comment.Text))
			}
		}
	}
	if modern == nil {
		modern = legacy
	}
	if modern != nil {
		out.WriteString("build:" + strconv.Quote(modern.String()))
	}
	// Formatting must not silently change a file's language-version override.
	out.WriteString("version:" + strconv.Quote(file.GoVersion))
	return out.String()
}
