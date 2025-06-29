package errhance

import (
	"go/ast"
	"testing"
)

func Test_callExprName(t *testing.T) {
	type args struct {
		assignStmt ast.AssignStmt
	}

	assignStmt1, _ := castSnippet[*ast.AssignStmt]("err := c.Request().ParseMultipartForm(10 << 20)")

	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "assignStmt1",
			args: args{assignStmt: *assignStmt1},
			want: "c.Request.ParseMultipartForm",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := callExprName(tt.args.assignStmt); got != tt.want {
				t.Errorf("callExprName() = %v, want %v", got, tt.want)
			}
		})
	}
}
