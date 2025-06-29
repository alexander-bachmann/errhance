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

	"github.com/alexander-bachmann/errhance/internal/fmtstr"
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
	var name string

	// recompute AST after each replacement,
	// otherwise node positions will be inaccurate
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return src, hasEnhanced, fmt.Errorf("parser.ParseFile: %w", err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		// after a single enhancement, exit early to recalculate node positions
		if hasEnhanced {
			return Skip
		}
		if node == nil {
			return Next
		}
		switch n := node.(type) {
		case *ast.AssignStmt: // err := foo().bar()
			if !returnsErr(*n) {
				return Next
			}
			name = callExprName(*n) // foo.bar

		case *ast.IfStmt: // if err != nil
			if !hasErrNotEqualNilCond(*n) {
				return Next
			}
			block := src[int((*n).Pos())-1 : int((*n).End()-1)]
			if containsOmitComment(block) {
				return Skip
			}
			// AssignStmt within IfStmt indicates shorthand syntax
			// if err := foo.Bar(); err != nil
			if i, ok := n.Init.(*ast.AssignStmt); ok {
				name = callExprName(*i) // foo.Bar
			}
			// err => fmt.Errorf("foo.Bar: %w", err)
			var ok bool
			src, ok = replaceErr(cfg, src, *n, name)
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
	// skip if err value is overwritten before being returned
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
	newErr, pos, length := newErrPosLen(cfg, src, ifStmt, newErr)
	if pos == -1 || length == -1 {
		return src, false
	}
	// make no changes if current err and new err are identical
	if src[pos-1:pos+length-1] == newErr {
		return src, false
	}

	return src[:pos-1] + newErr + src[pos+length-1:], true
}

func newErrPosLen(cfg SourceConfig, src string, ifStmt ast.IfStmt, callFn string) (string, int, int) {
	pos := -1
	length := -1
	if ifStmt.Body == nil {
		return "", pos, length
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
		case *ast.Ident:
			if (*n).Name == "err" {
				fmtErrorf := FmtErrorf{Args: []string{"err"}}
				newErr := fmtErrorf.Errhance(callFn)
				pos = int((*n).Pos())
				length = 3
				return newErr, pos, length
			}
		case *ast.CallExpr:
			if cfg.overwrite && isFmtErrorf(*n) {
				fmtErrorf, err := ParseFmtErrorf(src, *n)
				if err != nil {
					// TODO: consider passing error up
					return "", pos, length
				}
				newErr := fmtErrorf.Errhance(callFn)
				pos = int((*n).Pos())
				length = int((*n).End() - (*n).Pos())
				return newErr, pos, length
			}
		}
	}
	return "", pos, length
}

func nodeStr(src string, node ast.Node) string {
	return src[node.Pos()-1 : node.End()-1]
}

type FmtErrorf struct {
	Format     string
	Directives []string
	Args       []string
}

func ParseFmtErrorf(src string, callExpr ast.CallExpr) (FmtErrorf, error) {
	if !isFmtErrorf(callExpr) {
		return FmtErrorf{}, fmt.Errorf("call expr is not fmt.Errorf")
	}
	if len(callExpr.Args) == 0 {
		return FmtErrorf{}, fmt.Errorf("call expr has no args")
	}
	format := nodeStr(src, callExpr.Args[0])
	// throw away error since it's okay if directives are missing
	directives, _ := parseDirectives(format)
	var args []string
	for i := 1; i < len(callExpr.Args); i++ {
		args = append(args, nodeStr(src, callExpr.Args[i]))
	}
	fmtErrorf := FmtErrorf{
		Format:     format,
		Directives: directives,
		Args:       args,
	}
	return fmtErrorf, nil
}

// e.g. io.ReadAll => fmt.Errorf("io.ReadAll: %w: %s", err, url)
func (f *FmtErrorf) Errhance(callFn string) string {
	f.tryDirectivesSupplement()
	f.Format = strings.Join(append([]string{callFn}, f.Directives...), ": ")
	return fmt.Sprintf(`fmt.Errorf(%s)`,
		strings.Join(append([]string{`"` + f.Format + `"`}, f.Args...), ", "))
}

func parseDirectives(str string) ([]string, error) {
	ops, err := fmtstr.Parse(str, -1)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, op := range ops {
		if op == nil {
			continue
		}
		// %% isn't a directive, it's an escape to print %
		if op.Text == "%%" {
			continue
		}
		dirs = append(dirs, op.Text)
	}
	return dirs, nil
}

func (f *FmtErrorf) tryDirectivesSupplement() {
	if len(f.Directives) == len(f.Args) {
		return
	}
	f.Directives = []string{}
	for _, arg := range f.Args {
		lowerArg := strings.ToLower(arg)
		if strings.HasPrefix(lowerArg, "err") ||
			strings.HasSuffix(lowerArg, "err") {
			f.Directives = append(f.Directives, "%w")
		} else {
			f.Directives = append(f.Directives, "%v")
		}
	}
}
