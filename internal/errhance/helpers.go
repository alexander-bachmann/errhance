package errhance

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
)

// castSnippet makes it easier to test functions that interact with ast.Node types
// Usage: ifStmt, err := castSnippet[*ast.IfStmt]("if err != nil { return }")
func castSnippet[T ast.Node](snippet string) (T, error) {
	var t T
	src := fmt.Sprintf(`package main
	func main() {
		%s
}`, snippet)
	f, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		return t, fmt.Errorf("parser.ParseFile: %w", err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if node, ok := n.(T); ok {
			t = node
			return false
		}
		return true
	})
	v := reflect.ValueOf(t)
	if (v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface) && v.IsNil() {
		return t, fmt.Errorf("%T not found", t)
	}
	return t, nil
}
