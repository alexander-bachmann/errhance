package errhance

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

func File(src string, cfg Config) (string, error) {
	if !cfg.generated && isGeneratedFile(src) {
		return src, nil // not an error to encounter a generated file
	}
	for {
		// recompute AST after each replacement,
		// otherwise node positions will be inaccurate
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "", src, 0)
		if err != nil {
			return src, fmt.Errorf("parser.ParseFile: %w", err)
		}
		var ok bool
		src, ok = findReplaceNextErr(file, src, cfg)
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

func findReplaceNextErr(file *ast.File, src string, cfg Config) (string, bool) {
	hasReplaced := false
	newErr := ""

	ast.Inspect(file, func(node ast.Node) bool {
		if hasReplaced {
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
			var ok bool
			// return err => return fmt.Errorf("foo: %w", err)
			src, ok = replaceWithNewErr(*n, src, newErr, cfg.overwrite, cfg.forceOverwrite)
			if !ok {
				break
			}
			hasReplaced = true
			return Skip
		}
		// do not remove -- it is very helpful
		// fmt.Printf("%T: %s\n", node, src[int(node.Pos())-1:int(node.End()-1)])
		return Next
	})
	return src, hasReplaced
}

func generateWrappedErr(assignStmt ast.AssignStmt) string {
	if !hasErrReturn(assignStmt) {
		return ""
	}
	// val, err := Foo().Bar(Baz()).Flop() => Foo.Bar.Flop
	name, ok := calledFuncsName(assignStmt)
	if !ok || name == "" {
		return ""
	}
	return "fmt.Errorf(\"" + name + ": %w\", err)"
}

func hasErrReturn(assignStmt ast.AssignStmt) bool {
	// expect err to be the last return value
	// 'foo, bar, err := Baz()', not 'foo, err, bar := Baz()'
	if ident, ok := assignStmt.Lhs[len(assignStmt.Lhs)-1].(*ast.Ident); ok {
		return ident.Name == "err"
	}
	return false
}

func calledFuncsName(assignStmt ast.AssignStmt) (string, bool) {
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
	if len(name) > 0 && name[len(name)-1] != '.' {
		return name + "."
	}
	return name
}

func replaceWithNewErr(ifStmt ast.IfStmt, src string, newErr string, overwrite, forceOverwrite bool) (string, bool) {
	if newErr == "" {
		return src, false
	}
	if !isErrNilCheck(ifStmt) {
		return src, false
	}
	pos, len := returnErrPositionLength(ifStmt, overwrite, forceOverwrite)
	if pos == -1 || len == -1 {
		return src, false
	}
	// if ifStmt has an assignStmt, it means it uses the shorthand err check syntax
	// e.g. if err := foo(); err != nil {}
	switch a := ifStmt.Init.(type) {
	case *ast.AssignStmt:
		newErr = generateWrappedErr(*a)
	}
	// make no changes if current err is what errhance generates
	if src[pos-1:pos+len-1] == newErr {
		return src, false
	}
	return src[:pos-1] + newErr + src[pos+len-1:], true
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

func returnErrPositionLength(ifStmt ast.IfStmt, overwrite, forceOverwrite bool) (int, int) {
	pos := -1
	length := -1 // prefer len but shadows len()
	if ifStmt.Body == nil {
		return pos, length
	}
	for _, stmt := range ifStmt.Body.List {
		returnStmt, ok := stmt.(*ast.ReturnStmt)
		if !ok {
			continue
		}
		for _, node := range returnStmt.Results {
			switch n := node.(type) {
			case *ast.Ident: // default case: return err
				if (*n).Name == "err" {
					pos = int((*n).Pos())
					length = 3
					return pos, length
				}
			case *ast.CallExpr: // requires an overwrite flag: return fmt.Errorf
				if isValidFmtErrorf(n, overwrite, forceOverwrite) {
					pos = int((*n).Pos())
					length = int((*n).End() - (*n).Pos())
					return pos, length
				}
			}
		}
	}
	return pos, length
}

func isValidFmtErrorf(call *ast.CallExpr, overwrite bool, forceOverwrite bool) bool {
	// skip unless requesting either --overwrite or --force-overwrite
	if !overwrite && !forceOverwrite {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if pkgIdent.Name != "fmt" || sel.Sel.Name != "Errorf" {
		return false
	}
	var idents []string
	for _, arg := range call.Args {
		ident, ok := arg.(*ast.Ident)
		if !ok {
			continue
		}
		idents = append(idents, ident.Name)
	}
	// if not forcefully overwriting, don't enhance if there's:
	if !forceOverwrite {
		// multiple identifiers: fmt.Errorf("failed: %s: %w", str, err)
		if len(idents) != 1 {
			return false
		}
		// sentinel errors: fmt.Errorf("failed: %w", ErrNotFound)
		if len(idents) == 1 && idents[0] != "err" {
			return false
		}
	}
	return true
}

func containsOmitComment(src string) bool {
	omitComments := []string{
		"noeh", "no eh",
		"noerrhance", "no errhance",
	}
	for _, comment := range omitComments {
		if strings.Contains(src, "//") && strings.Contains(src, comment) {
			return true
		}
	}
	return false
}

func isGeneratedFile(src string) bool {
	const snippetLen = 200
	snippets := []string{
		"code generated",
		"generated code",
		"do not edit",
		"autogenerated",
		"auto-generated",
		"generated by",
		"do not modify",
		"this file was generated",
	}
	// use entire src file if it's shorter than snippetLen
	end := snippetLen
	if len(src) < snippetLen {
		end = len(src)
	}
	top := strings.ToLower(src[:end])
	for _, snippet := range snippets {
		if strings.Contains(top, snippet) {
			return true
		}
	}
	return false
}
