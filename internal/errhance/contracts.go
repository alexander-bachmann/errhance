package errhance

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Keys survive independent package/test variants and generic instantiation.
func contractFunctionKey(fn *types.Func) string { return fn.Origin().FullName() }

// readerContract matches the standard reader methods, not merely their names.
func readerContract(fn *types.Func) bool {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil || sig.Variadic() {
		return false
	}
	var params, results []types.Type
	switch fn.Name() {
	case "Read":
		params = []types.Type{types.NewSlice(types.Typ[types.Byte])}
		results = []types.Type{types.Typ[types.Int], types.Universe.Lookup("error").Type()}
	case "ReadByte":
		results = []types.Type{types.Typ[types.Byte], types.Universe.Lookup("error").Type()}
	case "ReadRune":
		results = []types.Type{types.Typ[types.Rune], types.Typ[types.Int], types.Universe.Lookup("error").Type()}
	default:
		return false
	}
	for i, tuple := range []*types.Tuple{sig.Params(), sig.Results()} {
		want := params
		if i == 1 {
			want = results
		}
		if tuple.Len() != len(want) {
			return false
		}
		for j, t := range want {
			if !types.Identical(tuple.At(j).Type(), t) {
				return false
			}
		}
	}
	return true
}

func contractLiteralKey(fset *token.FileSet, fn *ast.FuncLit) string {
	p := fset.PositionFor(fn.Pos(), false)
	return fmt.Sprintf("literal:%s:%d", p.Filename, p.Offset)
}

type contractNode struct {
	key string
	obj types.Object
	// A path distinguishes tuple slots and successive calls of returned functions.
	path string
}

func contractResultNode(n contractNode, slot int) contractNode {
	n.path += fmt.Sprintf("/%d", slot)
	return n
}

func contractObjectNode(obj types.Object) contractNode {
	// Imported globals and test variants have distinct objects but shared storage.
	if obj != nil && obj.Pkg() != nil && obj.Parent() == obj.Pkg().Scope() {
		return contractNode{key: "var:" + obj.Pkg().Path() + "." + obj.Name()}
	}
	return contractNode{obj: obj}
}

// projectContracts follows a flow-insensitive union of assignments backwards.
// Known conflicts block traced producers; uncertain provenance only warns.
// Only root syntax is inspected, not external consumers.
func projectContracts(pkgs []*packages.Package) (map[string]string, []Diagnostic) {
	blocked := make(map[string]string)
	edges := make(map[contractNode]map[contractNode]bool)
	known := make(map[contractNode]bool)
	unknown := make(map[contractNode]bool)
	callables := make(map[string]*types.Signature)
	owners := make(map[contractNode]string)
	parameters := make(map[string]map[contractNode]int)
	type invocation struct {
		targets []contractNode
		args    [][]contractNode
		params  int
	}
	calls := make(map[contractNode]invocation)
	seeds := make(map[contractNode]Diagnostic)
	errorNodes := make(map[contractNode]bool)
	type event struct {
		d           Diagnostic
		sources     []contractNode
		actualError bool
	}
	var events []event
	var diagnostics []Diagnostic
	seen := make(map[Diagnostic]bool)
	addDiagnostic := func(d Diagnostic) {
		if !seen[d] {
			seen[d] = true
			diagnostics = append(diagnostics, d)
		}
	}
	link := func(to contractNode, from []contractNode) {
		if len(from) == 0 {
			return
		}
		if edges[to] == nil {
			edges[to] = make(map[contractNode]bool)
		}
		for _, source := range from {
			edges[to][source] = true
		}
	}
	// Project aliases without merging sibling return slots. For example,
	// factory/0/1 follows factory/0 -> callback, then callback/1.
	sources := func(n contractNode) []contractNode {
		// Recursive higher-order types can grow projection paths indefinitely.
		// Treat excessive depth as uncertainty, never as a global conflict.
		if strings.Count(n.path, "/") > 64 {
			unknown[n] = true
			return nil
		}
		var out []contractNode
		for source := range edges[n] {
			out = append(out, source)
		}
		for i := range n.path {
			if n.path[i] != '/' {
				continue
			}
			prefix := n
			prefix.path = n.path[:i]
			for source := range edges[prefix] {
				source.path += n.path[i:]
				out = append(out, source)
			}
		}
		return out
	}
	errType := types.Universe.Lookup("error").Type()
	isError := func(t types.Type) bool {
		return t != nil && t != types.Typ[types.UntypedNil] && types.AssignableTo(t, errType)
	}
	relevant := func(t types.Type) bool {
		pending := []types.Type{t}
		visited := make(map[types.Type]bool)
		for i := 0; i < len(pending); i++ {
			t := pending[i]
			if t == nil || t == types.Typ[types.UntypedNil] || visited[t] {
				continue
			}
			visited[t] = true
			if isError(t) {
				return true
			}
			switch t := t.Underlying().(type) {
			case *types.Interface:
				return true
			case *types.Signature:
				pending = append(pending, t.Results())
			case *types.Tuple:
				for j := 0; j < t.Len(); j++ {
					pending = append(pending, t.At(j).Type())
				}
			}
		}
		return false
	}
	for _, pkg := range pkgs {
		info := pkg.TypesInfo
		if info == nil {
			continue
		}
		mayBeError := func(expr ast.Expr) bool {
			t := info.TypeOf(expr)
			if t == nil || t == types.Typ[types.UntypedNil] {
				return false
			}
			// Boxing does not remove identity/type contracts on dynamic errors.
			_, iface := t.Underlying().(*types.Interface)
			return iface || types.AssignableTo(t, errType)
		}
		isNil := func(expr ast.Expr) bool {
			id, ok := ast.Unparen(expr).(*ast.Ident)
			return ok && info.ObjectOf(id) == types.Universe.Lookup("nil")
		}
		unresolved := func(expr ast.Expr) []contractNode {
			p := pkg.Fset.PositionFor(expr.Pos(), false)
			n := contractNode{key: fmt.Sprintf("unknown:%s:%d", p.Filename, p.Offset)}
			unknown[n] = true
			return []contractNode{n}
		}
		var provenance func(ast.Expr, int) []contractNode
		provenance = func(expr ast.Expr, slot int) (out []contractNode) {
			t := info.TypeOf(expr)
			if tuple, ok := t.(*types.Tuple); ok && slot < tuple.Len() {
				t = tuple.At(slot).Type()
			}
			if !relevant(t) {
				return nil
			}
			defer func() {
				if isError(t) {
					for _, n := range out {
						errorNodes[n] = true
					}
				}
			}()
			switch e := ast.Unparen(expr).(type) {
			case *ast.Ident:
				switch obj := info.ObjectOf(e).(type) {
				case *types.Func:
					return []contractNode{{key: contractFunctionKey(obj)}}
				case *types.Var:
					return []contractNode{contractObjectNode(obj)}
				default:
					return nil
				}
			case *ast.FuncLit:
				return []contractNode{{key: contractLiteralKey(pkg.Fset, e)}}
			case *ast.SelectorExpr:
				if fn, ok := info.Uses[e.Sel].(*types.Func); ok {
					// Interface method objects are not concrete implementations.
					if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
						if _, ok := recv.Type().Underlying().(*types.Interface); ok {
							return unresolved(e)
						}
					}
					if sel := info.Selections[e]; sel != nil {
						if _, ok := sel.Recv().Underlying().(*types.Interface); ok {
							return unresolved(e)
						}
					}
					return []contractNode{{key: contractFunctionKey(fn)}}
				}
				if obj, ok := info.Uses[e.Sel].(*types.Var); ok && info.Selections[e] == nil {
					return []contractNode{contractObjectNode(obj)}
				}
				return unresolved(e)
			case *ast.CallExpr:
				if id, ok := ast.Unparen(e.Fun).(*ast.Ident); ok && info.Uses[id] == types.Universe.Lookup("recover") {
					return unresolved(e)
				}
				var out []contractNode
				if info.Types[e.Fun].IsType() {
					for _, arg := range e.Args {
						out = append(out, provenance(arg, 0)...)
					}
					return out
				}
				targets := provenance(e.Fun, 0)
				for _, fn := range targets {
					out = append(out, contractResultNode(fn, slot))
				}
				call := invocation{targets: targets}
				if t := info.TypeOf(e.Fun); t != nil {
					if sig, ok := t.Underlying().(*types.Signature); ok {
						call.params = sig.Params().Len()
					}
				}
				hasArgs := false
				for _, arg := range e.Args {
					count := 1
					if tuple, ok := info.TypeOf(arg).(*types.Tuple); ok {
						count = tuple.Len()
					}
					for i := 0; i < count; i++ {
						from := provenance(arg, i)
						call.args = append(call.args, from)
						hasArgs = hasArgs || len(from) != 0
					}
				}
				if hasArgs {
					p := pkg.Fset.PositionFor(e.Pos(), false)
					n := contractResultNode(contractNode{key: fmt.Sprintf("call:%s:%d", p.Filename, p.Offset)}, slot)
					link(n, out)
					calls[n] = call
					return []contractNode{n}
				}
				return out
			case *ast.IndexExpr:
				if _, ok := info.TypeOf(e).(*types.Signature); ok {
					if id, ok := ast.Unparen(e.X).(*ast.Ident); ok {
						if _, ok := info.Uses[id].(*types.Func); ok {
							return provenance(e.X, slot)
						}
					}
					if sel, ok := ast.Unparen(e.X).(*ast.SelectorExpr); ok {
						if _, ok := info.Uses[sel.Sel].(*types.Func); ok {
							return provenance(e.X, slot)
						}
					}
				}
				return unresolved(e)
			case *ast.IndexListExpr:
				return provenance(e.X, slot)
			case *ast.TypeAssertExpr:
				return provenance(e.X, slot)
			case *ast.BasicLit:
				return nil
			default:
				return unresolved(e)
			}
		}
		assign := func(lhs, rhs []ast.Expr) {
			for i, expr := range lhs {
				var obj types.Object
				switch e := ast.Unparen(expr).(type) {
				case *ast.Ident:
					obj = info.ObjectOf(e)
				case *ast.SelectorExpr:
					if info.Selections[e] == nil {
						obj = info.Uses[e.Sel]
					}
				}
				if obj == nil || !relevant(obj.Type()) {
					continue
				}
				n := contractObjectNode(obj)
				known[n] = true
				errorNodes[n] = errorNodes[n] || isError(obj.Type())
				if len(lhs) == len(rhs) {
					link(n, provenance(rhs[i], 0))
				} else if len(rhs) == 1 {
					if tuple, ok := info.TypeOf(rhs[0]).(*types.Tuple); ok && i < tuple.Len() {
						t := tuple.At(i).Type()
						if !relevant(t) {
							continue
						}
						errorNodes[n] = errorNodes[n] || isError(t)
					}
					link(n, provenance(rhs[0], i))
				}
			}
		}
		warn := func(n ast.Node, message string, operands ...ast.Expr) {
			d := Diagnostic{Position: pkg.Fset.PositionFor(n.Pos(), false), Message: message}
			e := event{d: d}
			if assertion, ok := n.(*ast.TypeAssertExpr); ok && assertion.Type != nil {
				e.actualError = isError(info.TypeOf(assertion.Type))
			}
			for _, expr := range operands {
				e.actualError = e.actualError || isError(info.TypeOf(expr))
				e.sources = append(e.sources, provenance(expr, 0)...)
			}
			events = append(events, e)
		}
		function := func(key string, ft *ast.FuncType, body *ast.BlockStmt, sig *types.Signature, reader bool) {
			n := contractNode{key: key}
			callables[key] = sig
			for i := 0; i < sig.Params().Len(); i++ {
				param := sig.Params().At(i)
				if relevant(param.Type()) {
					if parameters[key] == nil {
						parameters[key] = make(map[contractNode]int)
					}
					parameters[key][contractObjectNode(param)] = i
				}
			}
			if reader {
				seeds[contractResultNode(n, sig.Results().Len()-1)] = Diagnostic{Position: pkg.Fset.PositionFor(ft.Pos(), false), Message: "Read/ReadByte/ReadRune method may require sentinel error identity"}
			}
			if ft.Params != nil {
				for _, field := range ft.Params.List {
					for _, id := range field.Names {
						if relevant(info.TypeOf(id)) {
							unknown[contractObjectNode(info.ObjectOf(id))] = true
						}
					}
				}
			}
			var results []*ast.Ident
			if ft.Results != nil {
				for _, field := range ft.Results.List {
					if len(field.Names) == 0 {
						results = append(results, nil)
					}
					for _, id := range field.Names {
						results = append(results, id)
						if relevant(info.TypeOf(id)) {
							result := contractObjectNode(info.ObjectOf(id))
							known[result] = true
							owners[result] = key
							link(contractResultNode(n, len(results)-1), []contractNode{result})
						}
					}
				}
			}
			for i := 0; i < sig.Results().Len(); i++ {
				if !relevant(sig.Results().At(i).Type()) {
					continue
				}
				result := contractResultNode(n, i)
				errorNodes[result] = isError(sig.Results().At(i).Type())
				if body == nil {
					unknown[result] = true
				}
			}
			// The outer syntax walk still visits contracts inside scalar-only functions.
			if body == nil || !relevant(sig) {
				return
			}
			ast.Inspect(body, func(node ast.Node) bool {
				if _, ok := node.(*ast.FuncLit); ok {
					return false
				}
				if ret, ok := node.(*ast.ReturnStmt); ok {
					for i := range results {
						if len(ret.Results) == 0 {
							break
						}
						if !relevant(sig.Results().At(i).Type()) {
							continue
						}
						var from []contractNode
						if len(ret.Results) == len(results) {
							from = provenance(ret.Results[i], 0)
						} else {
							from = provenance(ret.Results[0], i)
						}
						link(contractResultNode(n, i), from)
						// Explicit returns assign named results before deferred code runs.
						if results[i] != nil {
							link(contractObjectNode(info.ObjectOf(results[i])), from)
						}
					}
				}
				return true
			})
		}
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(node ast.Node) bool {
				switch n := node.(type) {
				case *ast.FuncDecl:
					if fn, ok := info.Defs[n.Name].(*types.Func); ok {
						function(contractFunctionKey(fn), n.Type, n.Body, fn.Type().(*types.Signature), readerContract(fn))
					}
				case *ast.FuncLit:
					function(contractLiteralKey(pkg.Fset, n), n.Type, n.Body, info.TypeOf(n).(*types.Signature), false)
				case *ast.AssignStmt:
					assign(n.Lhs, n.Rhs)
				case *ast.RangeStmt:
					// Range overwrites existing bindings too; collection/iterator
					// element provenance is not modeled by ordinary assignments.
					for _, expr := range []ast.Expr{n.Key, n.Value} {
						var obj types.Object
						switch e := ast.Unparen(expr).(type) {
						case *ast.Ident:
							obj = info.ObjectOf(e)
						case *ast.SelectorExpr:
							if info.Selections[e] == nil {
								obj = info.Uses[e.Sel]
							}
						}
						if obj != nil && relevant(obj.Type()) {
							unknown[contractObjectNode(obj)] = true
						}
					}
				case *ast.UnaryExpr:
					if n.Op == token.AND {
						if id, ok := ast.Unparen(n.X).(*ast.Ident); ok {
							if relevant(info.TypeOf(id)) {
								unknown[contractObjectNode(info.ObjectOf(id))] = true
							}
						}
					}
				case *ast.ValueSpec:
					lhs := make([]ast.Expr, len(n.Names))
					for i, id := range n.Names {
						lhs[i] = id
					}
					assign(lhs, n.Values)
				case *ast.BinaryExpr:
					if (n.Op == token.EQL || n.Op == token.NEQ) && !isNil(n.X) && !isNil(n.Y) && (mayBeError(n.X) || mayBeError(n.Y)) {
						warn(n, "direct error identity comparison may change after wrapping; use errors.Is for wrapping compatibility", n.X, n.Y)
					}
				case *ast.SwitchStmt:
					if n.Tag != nil {
						for _, stmt := range n.Body.List {
							for _, expr := range stmt.(*ast.CaseClause).List {
								if !isNil(expr) && !isNil(n.Tag) && (mayBeError(n.Tag) || mayBeError(expr)) {
									warn(expr, "error expression switch uses direct identity; use errors.Is for wrapping compatibility", n.Tag, expr)
								}
							}
						}
					}
				case *ast.TypeAssertExpr:
					if mayBeError(n.X) {
						message := "direct error type assertion may fail after wrapping; use errors.As for wrapping compatibility"
						if n.Type == nil {
							message = "error type switch may change after wrapping; use errors.As for wrapping compatibility"
						}
						warn(n, message, n.X)
					}
				}
				return true
			})
		}
	}
	// Bind arguments only when the selected result reaches that parameter.
	// Iterate because a returned callback can itself be another call's target.
	for changed := true; changed; {
		changed = false
		for result, call := range calls {
			params := make(map[contractNode]int)
			pending := append([]contractNode(nil), call.targets...)
			visited := make(map[contractNode]bool)
			for i := 0; i < len(pending); i++ {
				n := pending[i]
				if visited[n] {
					continue
				}
				visited[n] = true
				if n.path == "" {
					offset := 0
					if sig := callables[n.key]; sig != nil && sig.Recv() != nil && call.params == sig.Params().Len()+1 {
						offset = 1
					}
					for param, j := range parameters[n.key] {
						params[param] = j + offset
					}
				}
				pending = append(pending, sources(n)...)
			}
			pending = []contractNode{result}
			visited = make(map[contractNode]bool)
			for i := 0; i < len(pending); i++ {
				n := pending[i]
				if visited[n] {
					continue
				}
				visited[n] = true
				base := n
				base.path = ""
				if j, ok := params[base]; ok && j < len(call.args) {
					for _, arg := range call.args[j] {
						arg.path += n.path
						if !edges[result][arg] {
							link(result, []contractNode{arg})
							changed = true
						}
					}
				}
				pending = append(pending, sources(n)...)
			}
		}
	}
	// Interface values are contracts only when their graph reaches an actual
	// error. Wait until all assignments and function returns have been collected.
	for _, e := range events {
		pending := append([]contractNode(nil), e.sources...)
		visited := make(map[contractNode]bool)
		for i := 0; i < len(pending) && !e.actualError; i++ {
			n := pending[i]
			if visited[n] {
				continue
			}
			visited[n] = true
			e.actualError = errorNodes[n]
			for _, source := range sources(n) {
				pending = append(pending, source)
			}
		}
		if e.actualError {
			addDiagnostic(e.d)
			for _, n := range e.sources {
				seeds[n] = e.d
			}
		}
	}
	// Reachability computes the least fixed point, including recursive functions
	// and cyclic aliases. Sort seeds so diagnostic reasons stay deterministic.
	var queue []contractNode
	for n := range seeds {
		queue = append(queue, n)
	}
	sort.Slice(queue, func(i, j int) bool {
		a, b := seeds[queue[i]], seeds[queue[j]]
		if a.Position.Filename != b.Position.Filename {
			return a.Position.Filename < b.Position.Filename
		}
		if a.Position.Offset != b.Position.Offset {
			return a.Position.Offset < b.Position.Offset
		}
		return a.Message < b.Message
	})
	visited := make(map[contractNode]bool)
	for i := 0; i < len(queue); i++ {
		n := queue[i]
		if visited[n] {
			continue
		}
		visited[n] = true
		d := seeds[n]
		from := sources(n)
		if callables[n.key] != nil && strings.Count(n.path, "/") == 1 {
			blocked[n.key] = d.Message
		}
		base := n
		base.path = ""
		// Blocking an owner must not traverse its other result slots.
		if owner := owners[base]; owner != "" && n.path == "" {
			blocked[owner] = d.Message
		}
		unresolved := unknown[n] || unknown[base] || (n.obj != nil && !known[base])
		for i := range n.path {
			if n.path[i] != '/' {
				continue
			}
			prefix := n
			prefix.path = n.path[:i]
			unresolved = unresolved || unknown[prefix]
		}
		if unresolved {
			d.Message = "unresolved error identity contract; unknown producers may be wrapped; use errors.Is/errors.As for wrapping compatibility"
			addDiagnostic(d)
		}
		for _, source := range from {
			if _, exists := seeds[source]; !exists {
				seeds[source] = d
			}
			queue = append(queue, source)
		}
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		a, b := diagnostics[i], diagnostics[j]
		if a.Position.Filename != b.Position.Filename {
			return a.Position.Filename < b.Position.Filename
		}
		if a.Position.Offset != b.Position.Offset {
			return a.Position.Offset < b.Position.Offset
		}
		return a.Message < b.Message
	})
	return blocked, diagnostics
}
