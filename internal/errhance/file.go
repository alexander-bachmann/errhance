package errhance

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrNoChanges = errors.New("no changes")
)

// may return ErrNoChanges
func LocalFile(cfg SourceConfig, path string) (SourceReport, error) {
	before, err := os.ReadFile(path)
	if err != nil {
		return SourceReport{}, fmt.Errorf("os.ReadFile: %w", err)
	}
	report, err := Source(cfg, path, string(before))
	if err != nil {
		return report, fmt.Errorf("Source: %w", err)
	}
	// open the file for writing and truncate
	// its content without modifying permissions
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return report, fmt.Errorf("os.OpenFile: %w", err)
	}
	defer file.Close()
	if _, err := file.Write([]byte(report.After)); err != nil {
		return report, fmt.Errorf("file.Write: %w", err)
	}
	if err := file.Sync(); err != nil {
		return report, fmt.Errorf("file.Sync: %w", err)
	}
	err = runGoImportsOnFile(path)
	if err != nil {
		return report, fmt.Errorf("runGoImportsOnFile: %w", err)
	}
	return report, nil
}

type SourceReport struct {
	Skipped             bool
	Before              string
	After               string
	TotalErrorsEnhanced int
}

// keep the core errhance logic as a pure function; any side effects should be put in LocalFile()
func Source(cfg SourceConfig, path, src string) (SourceReport, error) {
	report := SourceReport{
		Before:              src,
		After:               src,
		TotalErrorsEnhanced: 0,
		Skipped:             false,
	}
	if !cfg.generated && isGeneratedFile(report.Before) {
		report.Skipped = true
		return report, ErrNoChanges
	}
	if cfg.includeRelativePath {
		wd, err := os.Getwd()
		if err != nil {
			return report, fmt.Errorf("os.Getwd: %w", err)
		}
		path, err = filepath.Rel(wd, path)
		if err != nil {
			return report, fmt.Errorf("filepath.Rel: %w", err)
		}
	} else {
		path = ""
	}
	for {
		var ok bool
		var err error
		src, ok, err = enhanceNextErr(cfg, path, src)
		if err != nil {
			return report, fmt.Errorf("enhanceNextErr: %w", err)
		}
		if ok {
			report.TotalErrorsEnhanced++
		} else {
			// when enhanceNextErr does nothing, the file is completely enhanced
			break
		}
	}
	report.After = src
	if len(report.Before) == len(report.After) && report.Before == report.After {
		return report, ErrNoChanges
	}
	return report, nil
}

const (
	Next = true // recursively visit each non-nil child node
	Skip = false
)

func enhanceNextErr(cfg SourceConfig, path, src string) (string, bool, error) {
	var hasEnhanced bool
	var newErr string

	// recompute AST after each replacement,
	// otherwise node positions will be inaccurate
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return src, hasEnhanced, fmt.Errorf("parser.ParseFile: %w", err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		// exit early after a single enhancement
		// to recalculate the AST positions
		if hasEnhanced {
			return Skip
		}
		if node == nil {
			return Next
		}
		switch n := node.(type) {
		case *ast.AssignStmt: // err := foo()
			newErr = generateWrappedErr(path, *n, fset) // fmt.Errorf("foo: %w", err)
		case *ast.IfStmt: // if err != nil
			if containsOmitComment(src[int((*n).Pos())-1 : int((*n).End()-1)]) {
				return Skip
			}
			if !isErrNilCheck(*n) {
				return Next
			}
			// ifStmt with assignStmt is shorthand err != nil syntax
			// e.g. if err := foo(); err != nil {}
			switch i := n.Init.(type) {
			case *ast.AssignStmt:
				newErr = generateWrappedErr(path, *i, fset)
			}
			// return err => return fmt.Errorf("foo: %w", err)
			var ok bool
			src, ok = replaceErr(cfg, src, *n, newErr)
			if !ok {
				break
			}
			hasEnhanced = true
			return Skip
		}
		// printNode(src, node)
		return Next
	})
	return src, hasEnhanced, nil
}

func replaceErr(cfg SourceConfig, src string, ifStmt ast.IfStmt, newErr string) (string, bool) {
	if newErr == "" {
		return src, false
	}
	// skip if err value is overwritten before being returned.
	/*
		if err != nil {
			err = doSomething(err)
			return err
		}
	*/
	if ifStmt.Body != nil {
		block := src[ifStmt.Body.Lbrace-1 : ifStmt.Body.Rbrace]
		if strings.Contains(block, "err :=") || strings.Contains(block, "err =") {
			return src, false
		}
	}
	pos, length := returnedErrPosLen(cfg, src, ifStmt)
	if pos == -1 || length == -1 {
		return src, false
	}
	// make no changes if current err and new err are identical
	if src[pos-1:pos+length-1] == newErr {
		return src, false
	}
	return src[:pos-1] + newErr + src[pos+length-1:], true
}

func returnedErrPosLen(cfg SourceConfig, src string, ifStmt ast.IfStmt) (int, int) {
	pos := -1
	length := -1
	if ifStmt.Body == nil {
		return pos, length
	}
	for _, stmt := range ifStmt.Body.List {
		returnStmt, ok := stmt.(*ast.ReturnStmt)
		if !ok {
			continue
		}
		if len(returnStmt.Results) == 0 {
			continue
		}
		switch n := returnStmt.Results[len(returnStmt.Results)-1].(type) {
		case *ast.Ident: // default case: return err
			if (*n).Name == "err" {
				pos = int((*n).Pos())
				length = 3
				return pos, length
			}
		case *ast.CallExpr: // requires an overwrite flag: return fmt.Errorf
			if isValidFmtErrorf(cfg, src, *n) {
				pos = int((*n).Pos())
				length = int((*n).End() - (*n).Pos())
				return pos, length
			}
		}
	}
	return pos, length
}

func isValidFmtErrorf(cfg SourceConfig, src string, callExpr ast.CallExpr) bool {
	// skip unless requesting either --overwrite or --force-overwrite
	if !cfg.overwrite && !cfg.forceOverwrite {
		return false
	}
	if !isFmtErrorf(callExpr) {
		return false
	}
	// if not forcefully overwriting, check if there is:
	if !cfg.forceOverwrite {
		var args []string
		for _, arg := range callExpr.Args {
			args = append(args, src[arg.Pos()-1:arg.End()-1])
		}
		// 1. more than one format argument: fmt.Errorf("failed: %s: %w", str, err)
		// > 2 because the format string is an arg too
		if len(args) > 2 {
			return false
		}
		// 2. sentinel errors: fmt.Errorf("failed: %w", ErrNotFound)
		if len(args) == 2 && args[1] != "err" {
			return false
		}
	}
	return true
}
