package errhance

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

type Config struct{}

func Do(config Config, src string) (string, error) {
	for {
		// recompute AST after each replacement
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "", src, 0)
		if err != nil {
			return src, fmt.Errorf("parser.ParseFile: %w", err)
		}
		var ok bool
		src, ok = replace(config, file, src)
		if !ok {
			break
		}
	}
	return src, nil
}

const (
	Next = true
	Skip = false
)

func replace(config Config, file *ast.File, src string) (string, bool) {
	hasReplaced := false
	latestWrappedErr := ""

	ast.Inspect(file, func(node ast.Node) bool {
		if hasReplaced {
			return Skip
		}
		if node == nil {
			return Next
		}
		switch n := node.(type) {
		case *ast.AssignStmt:
			latestWrappedErr = wrappedErr(*n)
		case *ast.IfStmt:
			var ok bool
			src, ok = replaceWithWrappedErr(*n, src, latestWrappedErr)
			if !ok {
				break
			}
			hasReplaced = true
			return Skip
		}
		// fmt.Printf("%T: %s\n", node, src[int(node.Pos())-1:int(node.End()-1)])
		return Next
	})
	return src, hasReplaced
}

func wrappedErr(assignStmt ast.AssignStmt) string {
	if !returnsErr(assignStmt) {
		return ""
	}
	name, ok := callsFunc(assignStmt)
	if !ok || name == "" {
		return ""
	}
	return "fmt.Errorf(\"" + name + ": %w\", err)"
}

func returnsErr(assignStmt ast.AssignStmt) bool {
	if ident, ok := assignStmt.Lhs[len(assignStmt.Lhs)-1].(*ast.Ident); ok {
		return ident.Name == "err"
	}
	return false
}

func callsFunc(assignStmt ast.AssignStmt) (string, bool) {
	for _, rhs := range assignStmt.Rhs {
		switch n := rhs.(type) {
		case *ast.CallExpr:
			return callTrace(*n), true
		}
	}
	return "", false
}

// a.b.c().d() => a.b.c.d
// a(b(c(), d()) => a
func callTrace(callExpr ast.CallExpr) string {
	return strings.TrimSuffix(callExprNext(callExpr, ""), ".")
}

func callExprNext(callExpr ast.CallExpr, name string) string {
	switch f := callExpr.Fun.(type) {
	case *ast.Ident:
		name += f.Name
	case *ast.SelectorExpr:
		if f.Sel != nil {
			name += selectorExprNext(*f, name) + f.Sel.Name
		} else {
			name += selectorExprNext(*f, name)
		}
	}
	return appendDot(name)
}

func selectorExprNext(selectorExpr ast.SelectorExpr, name string) string {
	switch x := selectorExpr.X.(type) {
	case *ast.Ident:
		name += x.Name
	case *ast.CallExpr:
		name += callExprNext(*x, name)
	case *ast.SelectorExpr:
		if x.Sel != nil {
			name += selectorExprNext(*x, name) + x.Sel.Name
		} else {
			name += selectorExprNext(*x, name)
		}
	}
	return appendDot(name)
}

func appendDot(name string) string {
	if name[len(name)-1] != '.' {
		return name + "."
	}
	return name
}

func replaceWithWrappedErr(ifStmt ast.IfStmt, src, newErr string) (string, bool) {
	if newErr == "" {
		return src, false
	}
	if !isErrNilCheck(ifStmt) {
		return src, false
	}
	pos := returnErrPos(ifStmt)
	if pos == -1 {
		return src, false
	}
	// if ifStmt has an assignStmt, it means it uses the shorthand err check syntax
	// e.g. if err := foo(); err != nil { }
	switch a := ifStmt.Init.(type) {
	case *ast.AssignStmt:
		newErr = wrappedErr(*a)
	}
	return src[:pos-1] + newErr + src[pos+2:], true
}

func isErrNilCheck(ifStmt ast.IfStmt) bool {
	binExpr, ok := ifStmt.Cond.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	if binExpr.Op != token.NEQ {
		return false
	}
	if ident, ok := binExpr.X.(*ast.Ident); !ok || ident.Name != "err" {
		return false
	}
	return true
}

func returnErrPos(ifStmt ast.IfStmt) int {
	if ifStmt.Body == nil {
		return -1
	}
	for _, stmt := range ifStmt.Body.List {
		returnStmt, ok := stmt.(*ast.ReturnStmt)
		if !ok {
			continue
		}
		for _, result := range returnStmt.Results {
			ident, ok := result.(*ast.Ident)
			if !ok || ident.Name != "err" {
				continue
			}
			return int(ident.Pos())
		}
	}
	return -1
}

// unused but interesting
func collectImports(genDecl ast.GenDecl, imports map[string]struct{}) {
	if genDecl.Tok != token.IMPORT {
		return
	}
	for _, spec := range genDecl.Specs {
		switch s := spec.(type) {
		case *ast.ImportSpec:
			// if overwriting import package name
			if s.Name != nil {
				imports[s.Name.Name] = struct{}{}
			} else {
				imports[stripPackage(s.Path.Value)] = struct{}{}
			}
		}
	}
}

func stripPackage(pkg string) string {
	// path/filepath => filepath
	pkg = pkg[strings.LastIndex(pkg, "/")+1 : len(pkg)-1]
	pkg = strings.ReplaceAll(pkg, "\"", "")
	return pkg
}
