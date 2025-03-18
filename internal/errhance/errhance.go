package errhance

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

func File(src string, cfg Config) (string, int, error) {
	var numErrsEnhanced int
	if !cfg.generated && isGeneratedFile(src) {
		// it is not an error to encounter a generated file
		return src, numErrsEnhanced, nil
	}
	for {
		// recompute AST after each replacement,
		// otherwise node positions will be inaccurate
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "", src, 0)
		if err != nil {
			return src, numErrsEnhanced, fmt.Errorf("parser.ParseFile: %w", err)
		}
		var hasErrEnhanced bool
		src, hasErrEnhanced = findAndEnhanceNextErr(file, src, cfg)
		if !hasErrEnhanced {
			// if no error was enhanced, break because the file is complete
			break
		} else {
			numErrsEnhanced++
		}
	}
	return src, numErrsEnhanced, nil
}

const (
	Next = true
	Skip = false
)

func findAndEnhanceNextErr(file *ast.File, src string, cfg Config) (string, bool) {
	hasErrEnhanced := false
	newErr := ""

	ast.Inspect(file, func(node ast.Node) bool {
		// exit early after a single replacement to recalculate the positions
		if hasErrEnhanced {
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
			hasErrEnhanced = true
			return Skip
		}
		// do not remove -- it is very helpful
		// fmt.Printf("%T: %s\n", node, src[int(node.Pos())-1:int(node.End()-1)])
		return Next
	})
	return src, hasErrEnhanced
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
	pos, len := returnErrPositionLength(ifStmt, src, overwrite, forceOverwrite)
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

func returnErrPositionLength(ifStmt ast.IfStmt, src string, overwrite, forceOverwrite bool) (int, int) {
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
				if isValidFmtErrorf(n, src, overwrite, forceOverwrite) {
					pos = int((*n).Pos())
					length = int((*n).End() - (*n).Pos())
					return pos, length
				}
			}
		}
	}
	return pos, length
}

func isValidFmtErrorf(call *ast.CallExpr, src string, overwrite bool, forceOverwrite bool) bool {
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
	// if not forcefully overwriting, check if there is:
	if !forceOverwrite {
		var args []string
		for _, arg := range call.Args {
			args = append(args, src[arg.Pos()-1:arg.End()-1])
		}
		// more than one format argument: fmt.Errorf("failed: %s: %w", str, err)
		if len(args) > 2 { // > 2 because the format string is an arg too
			return false
		}
		// sentinel errors: fmt.Errorf("failed: %w", ErrNotFound)
		if len(args) == 2 && args[1] != "err" {
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

func printNode(src string, node ast.Node) {
	fmt.Printf("%T: %s\n", node, src[int(node.Pos())-1:int(node.End()-1)])
}
