package errhance

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

type Config struct {
	// true: err := foo.Baz() => fmt.Errorf("Baz: %w", err)
	// false: err := foo.Baz() => fmt.Errorf("foo.Baz: %w", err)
	OmitMethodObjName bool
}

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
	imports := make(map[string]struct{})
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
		case *ast.GenDecl:
			collectImports(*n, imports)
		case *ast.AssignStmt:
			latestWrappedErr = wrappedErr(config, *n, imports)
		case *ast.IfStmt:
			var ok bool
			src, ok = replaceWithWrappedErr(config, *n, src, latestWrappedErr, imports)
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

func wrappedErr(config Config, assignStmt ast.AssignStmt, imports map[string]struct{}) string {
	if !returnsErr(assignStmt) {
		return ""
	}
	name, ok := callsFunc(config, assignStmt, imports)
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

func callsFunc(config Config, assignStmt ast.AssignStmt, imports map[string]struct{}) (string, bool) {
	for _, rhs := range assignStmt.Rhs {
		switch n := rhs.(type) {
		case *ast.CallExpr:
			return funcName(config, *n, imports), true
		}
	}
	return "", false
}

func funcName(config Config, callExpr ast.CallExpr, imports map[string]struct{}) string {
	var name string
	switch fun := callExpr.Fun.(type) {
	case *ast.Ident:
		name = fun.Name
	case *ast.SelectorExpr:
		switch x := fun.X.(type) {
		case *ast.CallExpr:
			// support package functions e.g. fee.Fi().Fo().Fum()
			name = funcName(config, *x, imports) + "." + fun.Sel.Name
		case *ast.Ident:
			// support package functions e.g. os.Read()
			if _, ok := imports[x.Name]; ok || !config.OmitMethodObjName {
				name = x.Name + "." + fun.Sel.Name
			} else {
				// don't support methods e.g. b.Baz()
				name = fun.Sel.Name
			}
		}
	}
	return name
}

func replaceWithWrappedErr(config Config, ifStmt ast.IfStmt, src, newErr string, imports map[string]struct{}) (string, bool) {
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
		newErr = wrappedErr(config, *a, imports)
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
