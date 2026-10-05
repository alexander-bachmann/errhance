package errhance

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/scanner"
	"go/token"
	"go/types"
	"sort"
	"strconv"
	"strings"
)

// Diagnostic explains why a potentially error-bearing return was not rewritten.
type Diagnostic struct {
	Position token.Position
	Message  string
}

type typedOrigin struct {
	name   string
	reason string
	nonNil bool
}

type typedEdit struct {
	start, end int
	text       string
}

type typedRewriter struct {
	fset        *token.FileSet
	file        *ast.File
	info        *types.Info
	src         string
	cfg         SourceConfig
	errType     types.Type
	edits       []typedEdit
	diagnostics []Diagnostic
	count       int
	fmtName     string
	fmtImport   *types.PkgName
	needImport  bool
	fmtChecked  bool
	fmtSafe     bool
}

// rewriteTyped requires a fully typechecked file parsed with parser.ParseComments.
// It leaves the AST and type information untouched; all edits use original offsets.
// A no-op is successful and returns the original source with a zero count.
func rewriteTyped(fset *token.FileSet, file *ast.File, info *types.Info, src string, cfg SourceConfig) (string, []Diagnostic, int, error) {
	if fset == nil || file == nil || info == nil || info.Types == nil || info.Defs == nil || info.Uses == nil {
		return src, nil, 0, fmt.Errorf("rewriteTyped requires a file set, AST, and complete type information")
	}
	tf := fset.File(file.Pos())
	if tf == nil || tf.Size() != len(src) || fset.File(file.End()) != tf {
		return src, nil, 0, fmt.Errorf("rewriteTyped source and token positions do not match")
	}
	if ast.IsGenerated(file) && !cfg.generated {
		return src, nil, 0, nil
	}
	r := &typedRewriter{fset: fset, file: file, info: info, src: src, cfg: cfg,
		errType: types.Universe.Lookup("error").Type()}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if path != "fmt" {
			continue
		}
		var obj types.Object
		if imp.Name != nil {
			obj = info.Defs[imp.Name]
		} else {
			obj = info.Implicits[imp]
		}
		r.fmtImport, _ = obj.(*types.PkgName)
		if r.fmtImport != nil {
			r.fmtName = r.fmtImport.Name()
		} else {
			r.fmtName = "_" // Missing binding: never guess from the import spelling.
		}
	}
	if r.fmtName == "" {
		names := make(map[string]bool)
		ast.Inspect(file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				names[id.Name] = true
			}
			return true
		})
		// The AST supplies local names; only file/package scopes can add
		// collisions from other files or dot imports.
		if scope := info.Scopes[file]; scope != nil {
			for _, scope := range []*types.Scope{scope, scope.Parent()} {
				if scope != nil {
					for _, name := range scope.Names() {
						names[name] = true
					}
				}
			}
		} else {
			packages := make(map[*types.Package]bool)
			for _, obj := range info.Defs {
				if obj != nil && obj.Pkg() != nil && !packages[obj.Pkg()] {
					packages[obj.Pkg()] = true
					for _, name := range obj.Pkg().Scope().Names() {
						names[name] = true
					}
				}
			}
		}
		r.fmtName = "errhancefmt"
		for i := 2; names[r.fmtName]; i++ {
			r.fmtName = "errhancefmt" + strconv.Itoa(i)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil {
				obj, _ := info.Defs[fn.Name].(*types.Func)
				if obj != nil {
					blocked := cfg.blockedFunctions[contractFunctionKey(obj)]
					if readerContract(obj) {
						blocked = "Read/ReadByte/ReadRune method may require sentinel error identity"
					}
					r.function(fn.Type, fn.Body, obj.Type().(*types.Signature), blocked)
					if blocked != "" {
						return false
					}
				}
			}
		case *ast.FuncLit:
			if sig, ok := info.TypeOf(fn).(*types.Signature); ok {
				blocked := cfg.blockedFunctions[contractLiteralKey(fset, fn)]
				r.function(fn.Type, fn.Body, sig, blocked)
				if blocked != "" {
					return false
				}
			}
		}
		return true // Each literal starts with fresh state, never its parent's origins.
	})
	if r.needImport {
		// Scan the package terminator rather than adding a second semicolon.
		// An implicit terminator can lie inside a multiline block comment.
		scanFile := token.NewFileSet().AddFile("", -1, len(src))
		var scan scanner.Scanner
		scan.Init(scanFile, []byte(src), nil, scanner.ScanComments)
		commentEnd := 0
		for {
			pos, tok, lit := scan.Scan()
			offset := scanFile.Offset(pos)
			if tok == token.COMMENT {
				// Scanner literals omit carriage returns; find the original end.
				if strings.HasPrefix(lit, "/*") {
					commentEnd = offset + strings.Index(src[offset:], "*/") + 2
				}
			}
			if tok == token.SEMICOLON {
				offset = max(offset+1, commentEnd)
				if lit == ";" {
					// Keep trailing package comments with the package clause.
					for {
						next, kind, text := scan.Scan()
						start := scanFile.Offset(next)
						if kind != token.COMMENT || tf.Line(tf.Pos(start)) != tf.Line(tf.Pos(offset-1)) {
							break
						}
						if strings.HasPrefix(text, "/*") {
							offset = start + strings.Index(src[start:], "*/") + 2
						} else if end := strings.IndexByte(src[start:], '\n'); end >= 0 {
							offset = start + end
						} else {
							offset = len(src)
						}
					}
				}
				r.edits = append(r.edits, typedEdit{offset, offset, "\nimport " + r.fmtName + " \"fmt\"\n"})
				break
			}
			if tok == token.EOF {
				return src, r.diagnostics, 0, fmt.Errorf("rewriteTyped could not locate package terminator")
			}
		}
	}
	sort.Slice(r.edits, func(i, j int) bool { return r.edits[i].start > r.edits[j].start })
	last := len(src)
	for _, edit := range r.edits {
		if edit.start < 0 || edit.end < edit.start || edit.end > last {
			return src, r.diagnostics, 0, fmt.Errorf("rewriteTyped produced overlapping or invalid edits")
		}
		src = src[:edit.start] + edit.text + src[edit.end:]
		last = edit.start
	}
	sort.SliceStable(r.diagnostics, func(i, j int) bool {
		return r.diagnostics[i].Position.Offset < r.diagnostics[j].Position.Offset
	})
	return src, r.diagnostics, r.count, nil
}

func (r *typedRewriter) diagnostic(n ast.Node, message string) {
	r.diagnostics = append(r.diagnostics, Diagnostic{r.fset.Position(n.Pos()), message})
}

func (r *typedRewriter) edit(n ast.Node, text string) {
	tf := r.fset.File(n.Pos())
	r.edits = append(r.edits, typedEdit{tf.Offset(n.Pos()), tf.Offset(n.End()), text})
	r.count++
}

func (r *typedRewriter) omitted(n ast.Node) bool {
	start, end := r.fset.PositionFor(n.Pos(), false).Line, r.fset.PositionFor(n.End(), false).Line
	for _, group := range r.file.Comments {
		for _, comment := range group.List {
			line := r.fset.PositionFor(comment.Pos(), false).Line
			if line < start-1 || line > end {
				continue
			}
			for _, marker := range []string{"noeh", "no eh", "noerrhance", "no errhance"} {
				if strings.Contains(comment.Text, marker) {
					return true
				}
			}
		}
	}
	return false
}

func (r *typedRewriter) fmtCall(call *ast.CallExpr) bool {
	var obj types.Object
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		obj = r.info.Uses[fun]
	case *ast.SelectorExpr:
		obj = r.info.Uses[fun.Sel]
	}
	fn, ok := obj.(*types.Func)
	return ok && fn.Pkg() != nil && fn.Pkg().Path() == "fmt" && fn.Name() == "Errorf" && fn.Type().(*types.Signature).Recv() == nil
}

func (r *typedRewriter) fmtBindingSafe() bool {
	if r.fmtImport == nil {
		return r.fmtName != "_"
	}
	if r.fmtName == "_" || r.fmtName == "." {
		return false
	}
	if r.fmtChecked {
		return r.fmtSafe
	}
	r.fmtChecked, r.fmtSafe = true, true
	// A globally conservative check also works when the loader omits Scopes.
	for id, obj := range r.info.Defs {
		if id.Name == r.fmtName && obj != r.fmtImport && r.fset.File(id.Pos()) == r.fset.File(r.file.Pos()) {
			r.fmtSafe = false
			return false
		}
	}
	return true
}

func (r *typedRewriter) function(ft *ast.FuncType, body *ast.BlockStmt, sig *types.Signature, blocked string) {
	hasError := false
	for i := 0; i < sig.Results().Len(); i++ {
		hasError = hasError || types.AssignableTo(sig.Results().At(i).Type(), r.errType)
	}
	if !hasError {
		return
	}
	locals := make(map[types.Object]bool)
	unsafe := make(map[types.Object]string)
	collect := func(n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			if _, ok := n.(*ast.FuncLit); ok {
				return false
			}
			if id, ok := n.(*ast.Ident); ok {
				if obj, ok := r.info.Defs[id].(*types.Var); ok {
					locals[obj] = true
				}
			}
			return true
		})
	}
	collect(ft)
	collect(body)
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			ast.Inspect(n.Body, func(n ast.Node) bool {
				var writes []ast.Expr
				switch n := n.(type) {
				case *ast.AssignStmt:
					writes = n.Lhs
				case *ast.RangeStmt:
					writes = []ast.Expr{n.Key, n.Value}
				case *ast.IncDecStmt:
					writes = []ast.Expr{n.X}
				case *ast.UnaryExpr:
					if n.Op == token.AND {
						writes = []ast.Expr{n.X}
					}
				}
				for _, expr := range writes {
					if expr == nil {
						continue
					}
					if id, ok := ast.Unparen(expr).(*ast.Ident); ok && locals[r.info.ObjectOf(id)] {
						unsafe[r.info.ObjectOf(id)] = "captured error may be mutated by a closure"
					}
				}
				return true
			})
			return false
		case *ast.UnaryExpr:
			if n.Op == token.AND {
				if id, ok := ast.Unparen(n.X).(*ast.Ident); ok {
					unsafe[r.info.ObjectOf(id)] = "address-exposed error may be mutated"
				}
			}
		case *ast.BranchStmt:
			if n.Tok == token.GOTO {
				blocked = "goto control flow is unsupported"
			}
		}
		return true
	})
	for i := 0; i < sig.Results().Len(); i++ {
		result := sig.Results().At(i)
		if reason := unsafe[result]; reason != "" && types.AssignableTo(result.Type(), r.errType) && blocked == "" {
			blocked = "named result: " + reason
		}
	}
	type state = map[types.Object]typedOrigin
	clone := func(in state) state {
		out := make(state)
		for obj, origin := range in {
			out[obj] = origin
		}
		return out
	}
	var origin func(ast.Expr, state) typedOrigin
	origin = func(expr ast.Expr, env state) typedOrigin {
		switch expr := ast.Unparen(expr).(type) {
		case *ast.Ident:
			obj := r.info.ObjectOf(expr)
			if !locals[obj] {
				return typedOrigin{reason: "captured or non-local error has unknown origin"}
			}
			if reason := unsafe[obj]; reason != "" {
				return typedOrigin{reason: reason}
			}
			return env[obj]
		case *ast.CallExpr:
			if r.info.Types[expr.Fun].IsType() {
				return typedOrigin{reason: "error conversion has unknown origin"}
			}
			if r.fmtCall(expr) {
				return typedOrigin{reason: "error already formatted by fmt.Errorf"}
			}
			fun := ast.Unparen(expr.Fun)
			for {
				switch f := fun.(type) {
				case *ast.IndexExpr:
					fun = ast.Unparen(f.X)
				case *ast.IndexListExpr:
					fun = ast.Unparen(f.X)
				default:
					goto unwrapped
				}
			}
		unwrapped:
			if sel, ok := fun.(*ast.SelectorExpr); ok {
				if fn, ok := r.info.Uses[sel.Sel].(*types.Func); ok && readerContract(fn) {
					return typedOrigin{reason: "Read/ReadByte/ReadRune may return sentinel errors"}
				}
			}
			// Function variables can hide reader methods and other sentinel contracts.
			var obj types.Object
			switch f := fun.(type) {
			case *ast.Ident:
				obj = r.info.Uses[f]
			case *ast.SelectorExpr:
				obj = r.info.Uses[f.Sel]
			}
			if _, ok := obj.(*types.Func); !ok {
				return typedOrigin{reason: "indirect call has unknown error contract"}
			}
			var buf bytes.Buffer
			if err := printer.Fprint(&buf, r.fset, expr.Fun); err == nil {
				return typedOrigin{name: buf.String()}
			}
		}
		return typedOrigin{}
	}
	assign := func(lhs, rhs []ast.Expr, env state) {
		values := make([]typedOrigin, len(lhs))
		for i := range lhs {
			if len(lhs) == len(rhs) {
				values[i] = origin(rhs[i], env)
			} else if len(rhs) == 1 {
				if _, ok := r.info.TypeOf(rhs[0]).(*types.Tuple); ok {
					values[i] = origin(rhs[0], env)
				}
			}
		}
		// All RHS origins are read before any LHS object is updated (parallel assignment).
		for i, expr := range lhs {
			if id, ok := ast.Unparen(expr).(*ast.Ident); ok {
				env[r.info.ObjectOf(id)] = values[i]
			}
		}
	}
	guard := func(expr ast.Expr, truth bool, env state) {
		bin, ok := ast.Unparen(expr).(*ast.BinaryExpr)
		if !ok || (bin.Op != token.NEQ && bin.Op != token.EQL) {
			return
		}
		x, y := ast.Unparen(bin.X), ast.Unparen(bin.Y)
		isNil := func(e ast.Expr) bool {
			id, ok := e.(*ast.Ident)
			return ok && r.info.Uses[id] == types.Universe.Lookup("nil")
		}
		if isNil(x) {
			x, y = y, x
		}
		if id, ok := x.(*ast.Ident); ok && isNil(y) {
			obj := r.info.ObjectOf(id)
			value := env[obj]
			value.nonNil = truth == (bin.Op == token.NEQ)
			env[obj] = value
		}
	}
	// A single body pass must not inherit facts invalidated on another iteration.
	invalidateWrites := func(n ast.Node, env state) {
		ast.Inspect(n, func(n ast.Node) bool {
			var writes []ast.Expr
			switch n := n.(type) {
			case *ast.FuncLit:
				return false // Captured mutations are already unsafe for the whole function.
			case *ast.AssignStmt:
				writes = n.Lhs
			case *ast.RangeStmt:
				writes = []ast.Expr{n.Key, n.Value}
			case *ast.IncDecStmt:
				writes = []ast.Expr{n.X}
			case *ast.ValueSpec:
				for _, id := range n.Names {
					delete(env, r.info.ObjectOf(id))
				}
			}
			for _, expr := range writes {
				if expr != nil {
					if id, ok := ast.Unparen(expr).(*ast.Ident); ok {
						delete(env, r.info.ObjectOf(id))
					}
				}
			}
			return true
		})
	}
	var stmt func(ast.Stmt, state, bool) (state, bool)
	stmt = func(s ast.Stmt, env state, omit bool) (state, bool) {
		if _, block := s.(*ast.BlockStmt); !block {
			omit = omit || r.omitted(s)
		}
		switch s := s.(type) {
		case *ast.BlockStmt:
			for _, child := range s.List {
				var live bool
				env, live = stmt(child, env, omit)
				if !live {
					return env, false
				}
			}
		case *ast.AssignStmt:
			assign(s.Lhs, s.Rhs, env)
		case *ast.DeclStmt:
			if decl, ok := s.Decl.(*ast.GenDecl); ok {
				for _, spec := range decl.Specs {
					if spec, ok := spec.(*ast.ValueSpec); ok {
						lhs := make([]ast.Expr, len(spec.Names))
						for i, id := range spec.Names {
							lhs[i] = id
						}
						assign(lhs, spec.Values, env)
					}
				}
			}
		case *ast.IfStmt:
			if s.Init != nil {
				env, _ = stmt(s.Init, env, omit)
			}
			yes, no := clone(env), clone(env)
			guard(s.Cond, true, yes)
			guard(s.Cond, false, no)
			yes, yesLive := stmt(s.Body, yes, omit)
			noLive := true
			if s.Else != nil {
				no, noLive = stmt(s.Else, no, omit)
			}
			if !yesLive {
				return no, noLive
			}
			if !noLive {
				return yes, true
			}
			for obj, value := range yes {
				if other, ok := no[obj]; !ok || other != value {
					delete(yes, obj)
				}
			}
			return yes, true
		case *ast.ReturnStmt:
			if !omit {
				r.rewriteReturn(s, sig, env, origin, blocked)
			}
			return env, false
		case *ast.BranchStmt:
			if s.Tok == token.GOTO && !omit {
				r.diagnostic(s, "goto control flow is unsupported")
			}
			return env, false
		case *ast.LabeledStmt:
			return stmt(s.Stmt, env, omit)
		case *ast.ForStmt:
			if s.Init != nil {
				env, _ = stmt(s.Init, env, omit)
			}
			entry := clone(env)
			invalidateWrites(s, entry)
			if s.Cond != nil {
				guard(s.Cond, true, entry)
			}
			stmt(s.Body, entry, omit)
			return make(state), true
		case *ast.RangeStmt:
			entry := clone(env)
			invalidateWrites(s, entry)
			stmt(s.Body, entry, omit)
			return make(state), true
		case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
			var clauses []ast.Stmt
			switch s := s.(type) {
			case *ast.SwitchStmt:
				if s.Init != nil {
					env, _ = stmt(s.Init, env, omit)
				}
				clauses = s.Body.List
			case *ast.TypeSwitchStmt:
				if s.Init != nil {
					env, _ = stmt(s.Init, env, omit)
				}
				clauses = s.Body.List
			case *ast.SelectStmt:
				clauses = s.Body.List
			}
			fallsThrough := false
			for _, clause := range clauses {
				entry := clone(env)
				if fallsThrough {
					entry = make(state)
				}
				var list []ast.Stmt
				switch clause := clause.(type) {
				case *ast.CaseClause:
					list = clause.Body
				case *ast.CommClause:
					if clause.Comm != nil {
						entry, _ = stmt(clause.Comm, entry, omit)
					}
					list = clause.Body
				}
				// Case expressions are not guards: fallthrough bypasses their tests.
				// Local if guards still establish fresh, binding-aware facts.
				fallsThrough = false
				for _, child := range list {
					ast.Inspect(child, func(n ast.Node) bool {
						if _, ok := n.(*ast.FuncLit); ok {
							return false
						}
						if branch, ok := n.(*ast.BranchStmt); ok && branch.Tok == token.FALLTHROUGH {
							fallsThrough = true
						}
						return true
					})
				}
				for _, child := range list {
					var live bool
					entry, live = stmt(child, entry, omit)
					if !live {
						break
					}
				}
			}
			// Breaks (including labeled exits) and normal exits merge as unknown.
			return make(state), true
		case *ast.ExprStmt, *ast.DeferStmt, *ast.GoStmt, *ast.EmptyStmt:
			// Captures and address exposure were invalidated for the entire function.
		default:
			// Discard facts for any remaining unsupported statement.
			if !omit {
				r.diagnostic(s, "unsupported control flow; error origins invalidated")
			}
			return make(state), true
		}
		return env, true
	}
	stmt(body, make(state), false)
}

func (r *typedRewriter) rewriteReturn(ret *ast.ReturnStmt, sig *types.Signature, env map[types.Object]typedOrigin, origin func(ast.Expr, map[types.Object]typedOrigin) typedOrigin, blocked string) {
	if len(ret.Results) != sig.Results().Len() {
		for i := 0; i < sig.Results().Len(); i++ {
			if types.AssignableTo(sig.Results().At(i).Type(), r.errType) {
				r.diagnostic(ret, "implicit or tuple error return is unsupported")
				break
			}
		}
		return
	}
	for i, expr := range ret.Results {
		result := sig.Results().At(i).Type()
		if !types.AssignableTo(result, r.errType) {
			continue
		}
		if id, ok := ast.Unparen(expr).(*ast.Ident); ok && r.info.Uses[id] == types.Universe.Lookup("nil") {
			continue
		}
		if blocked != "" {
			r.diagnostic(expr, blocked)
			continue
		}
		if !types.Identical(result, r.errType) || !types.Identical(r.info.TypeOf(expr), r.errType) {
			r.diagnostic(expr, "concrete or non-builtin error return is not rewritten")
			continue
		}
		if call, ok := ast.Unparen(expr).(*ast.CallExpr); ok && r.fmtCall(call) {
			if r.cfg.overwrite {
				r.overwriteCall(call, env, origin)
			}
			continue
		}
		if call, ok := ast.Unparen(expr).(*ast.CallExpr); ok {
			if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok && sel.Sel.Name == "Errorf" {
				if id, ok := ast.Unparen(sel.X).(*ast.Ident); ok && r.fmtImport != nil && id.Name == r.fmtImport.Name() && r.info.Uses[id] != r.fmtImport {
					r.diagnostic(expr, "fmt import is shadowed; Errorf is not the standard fmt function")
					continue
				}
			}
		}
		value := origin(expr, env)
		if value.reason != "" {
			r.diagnostic(expr, value.reason)
			continue
		}
		if value.name == "" || !value.nonNil {
			r.diagnostic(expr, "unknown error origin or error not proven non-nil")
			continue
		}
		if !r.fmtBindingSafe() {
			r.diagnostic(expr, "fmt import is shadowed or has an unsupported binding")
			continue
		}
		tf := r.fset.File(expr.Pos())
		text := r.src[tf.Offset(expr.Pos()):tf.Offset(expr.End())]
		format := strings.ReplaceAll(value.name, "%", "%%") + ": %w"
		r.edit(expr, r.fmtName+".Errorf("+strconv.Quote(format)+", "+text+")")
		if r.fmtImport == nil {
			r.needImport = true
		}
	}
}

func (r *typedRewriter) overwriteCall(call *ast.CallExpr, env map[types.Object]typedOrigin, origin func(ast.Expr, map[types.Object]typedOrigin) typedOrigin) {
	if call.Ellipsis.IsValid() || len(call.Args) == 0 {
		r.diagnostic(call, "variadic fmt.Errorf overwrite is unsupported")
		return
	}
	literal, ok := call.Args[0].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		r.diagnostic(call, "fmt.Errorf overwrite requires a literal constant format")
		return
	}
	format, err := strconv.Unquote(literal.Value)
	if err != nil {
		r.diagnostic(call, "invalid fmt.Errorf format literal")
		return
	}
	name := ""
	for _, arg := range call.Args[1:] {
		if !types.Identical(r.info.TypeOf(arg), r.errType) {
			continue
		}
		value := origin(arg, env)
		if value.reason != "" || value.name == "" || !value.nonNil || (name != "" && name != value.name) {
			r.diagnostic(call, "fmt.Errorf arguments have unknown, captured, or ambiguous error origins")
			return
		}
		name = value.name
	}
	if name == "" {
		r.diagnostic(call, "fmt.Errorf has no proven error origin")
		return
	}
	// Preserve the whole format, including explicit indexes, widths, and every %w.
	// A literal prefix consumes no arguments, so evaluation and mapping stay intact.
	prefix := strings.ReplaceAll(name, "%", "%%") + ": "
	if !strings.HasPrefix(format, prefix) {
		r.edit(literal, strconv.Quote(prefix+format))
	}
}
