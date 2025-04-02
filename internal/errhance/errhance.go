package errhance

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

var (
	ErrNoChanges = errors.New("no changes")
)

type FileReport struct {
	Before        string
	After         string
	TotalEnhanced int
	Skipped       bool
}

func File(cfg Config, src string) (FileReport, error) {
	report := FileReport{
		Before:        src,
		After:         src,
		TotalEnhanced: 0,
		Skipped:       false,
	}
	if !cfg.generated && isGeneratedFile(report.Before) {
		report.Skipped = true
		return report, ErrNoChanges
	}
	for {
		// recompute AST after each replacement,
		// otherwise node positions will be inaccurate
		fset := token.NewFileSet()
		astFile, err := parser.ParseFile(fset, "", src, 0)
		if err != nil {
			return report, fmt.Errorf("parser.ParseFile: %w", err)
		}
		var ok bool
		src, ok = enhanceNextErr(cfg, src, astFile)
		if ok {
			report.TotalEnhanced++
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
	Next = true
	Skip = false
)

func enhanceNextErr(cfg Config, src string, file *ast.File) (string, bool) {
	var hasEnhanced bool
	var newErr string

	ast.Inspect(file, func(node ast.Node) bool {
		// exit early after a single replacement to recalculate the positions
		if hasEnhanced {
			return Skip
		}
		if node == nil {
			return Next
		}
		switch n := node.(type) {
		case *ast.AssignStmt: // err := foo()
			newErr = generateWrappedErr(*n) // fmt.Errorf("foo: %w", err)
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
				newErr = generateWrappedErr(*i)
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
	return src, hasEnhanced
}

func replaceErr(cfg Config, src string, ifStmt ast.IfStmt, newErr string) (string, bool) {
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

func returnedErrPosLen(cfg Config, src string, ifStmt ast.IfStmt) (int, int) {
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

func isValidFmtErrorf(cfg Config, src string, callExpr ast.CallExpr) bool {
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
