package errhance

import (
	"go/ast"
	"testing"
)

func Test_CastSnippet(t *testing.T) {
	_, err := castSnippet[*ast.IfStmt]("if err != nil { return }")
	if err != nil {
		t.Errorf("castSnippet() error = %v", err)
		return
	}
	_, err = castSnippet[*ast.IfStmt]("")
	if err == nil {
		t.Errorf("castSnippet() expected error")
		return
	}
}
