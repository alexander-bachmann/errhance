package errhance

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

var ErrNoChanges = errors.New("no changes")

// SourceReport describes the proposed transformation. On a disk-entrypoint
// error, After and TotalErrorsEnhanced do not imply that a write succeeded.
type SourceReport struct {
	Skipped             bool
	Before              string
	After               string
	TotalErrorsEnhanced int
	Diagnostics         []Diagnostic
}

// LocalFile checks the containing package, including tests, but edits only path.
// Dependencies must already be available; no dependency installation is performed.
func LocalFile(cfg SourceConfig, path string) (SourceReport, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return SourceReport{}, err
	}
	directory, reports, err := rewriteProject(context.Background(), cfg, []string{path}, map[string]bool{path: true})
	report := reports[path]
	if err != nil && len(report.Diagnostics) == 0 {
		report.Diagnostics = directory.Diagnostics
	}
	if err == nil && report.Before == report.After {
		err = ErrNoChanges
	}
	return report, err
}

// Source rewrites a standalone, type-correct file. importer.Default resolves
// standard-library imports; use disk entrypoints for module-aware checking.
func Source(cfg SourceConfig, src string) (SourceReport, error) {
	report := SourceReport{Before: src, After: src}
	fset, file, info, err := checkSource(src)
	if err != nil {
		return report, err
	}
	report.Skipped = ast.IsGenerated(file) && !cfg.generated
	blocked, contractDiagnostics := projectContracts([]*packages.Package{{Fset: fset, Syntax: []*ast.File{file}, TypesInfo: info}})
	cfg.blockedFunctions = blocked
	after, diagnostics, count, err := rewriteTyped(fset, file, info, src, cfg)
	report.Diagnostics = append(contractDiagnostics, diagnostics...)
	if err != nil {
		return report, err
	}
	if after == src {
		return report, ErrNoChanges
	}
	formatted, err := imports.Process("source.go", []byte(after), &imports.Options{Comments: true, TabIndent: true, TabWidth: 8, FormatOnly: true})
	if err != nil {
		return report, fmt.Errorf("imports: %w", err)
	}
	if _, _, _, err := checkSource(string(formatted)); err != nil {
		return report, fmt.Errorf("transformed source: %w", err)
	}
	report.After, report.TotalErrorsEnhanced = string(formatted), count
	return report, nil
}

func checkSource(src string) (*token.FileSet, *ast.File, *types.Info, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "source.go", src, parser.ParseComments|parser.AllErrors)
	if err != nil {
		return nil, nil, nil, err
	}
	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue), Defs: make(map[*ast.Ident]types.Object),
		Uses: make(map[*ast.Ident]types.Object), Implicits: make(map[ast.Node]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection), Scopes: make(map[ast.Node]*types.Scope),
	}
	cfg := types.Config{Importer: importer.Default()}
	_, err = cfg.Check(file.Name.Name, fset, []*ast.File{file}, info)
	return fset, file, info, err
}
