package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"sort"
	"strings"
	"sync"
)

// storageViewCalleeWriteSpare returns the spare predicate for one mutable-ref argument of
// CALL: a view whose source lies strictly inside ARG (`arg.F…`) survives when no possible
// callee writes field F through that parameter. Nil (spare nothing) when the callee or the
// argument position cannot be resolved.
func (a *Analyzer) storageViewCalleeWriteSpare(call *ast.CallExpr, arg ast.Expr) func(source, candidate string) bool {
	if call == nil || arg == nil {
		return nil
	}
	target := stripOptimizationParens(arg)
	if addr, isAddr := target.(*ast.AddrOfExpr); isAddr && addr != nil {
		target = stripOptimizationParens(addr.Operand)
	}
	argKey := optimizationExprString(target)
	if argKey == "" {
		return nil
	}
	pairs, ok := a.storageViewWriteCallees(call, a.storageViewScopeLocals(call))
	if !ok {
		return nil
	}
	// The argument is located by node identity; a lowering that rebuilt the argument list
	// (resolved/defaulted args, an lmut desugar) is matched by its spelling instead, and every
	// position spelled the same is unioned.
	writes := storageViewCalleeWrites{}
	for _, pair := range pairs {
		var indices []int
		for i, candidate := range pair.args {
			c := stripOptimizationParens(candidate)
			if addr, isAddr := c.(*ast.AddrOfExpr); isAddr && addr != nil {
				c = stripOptimizationParens(addr.Operand)
			}
			if c == target {
				indices = []int{i}
				break
			}
			if optimizationExprString(c) == argKey {
				indices = append(indices, i)
			}
		}
		if len(indices) == 0 {
			return nil
		}
		for _, index := range indices {
			next := a.storageViewCalleeWritesFor([]*ast.FuncDecl{pair.decl}, index)
			if next.All {
				return nil
			}
			// Completed summaries are immutable. Reuse a covering set, and
			// copy only when distinct possible callees add different fields.
			if next.covers(writes) {
				writes = next
			} else if !writes.covers(next) {
				var joined storageViewCalleeWrites
				joined.union(writes)
				joined.union(next)
				writes = joined
			}
		}
		if writes.All {
			break
		}
	}
	if writes.All {
		return nil
	}
	return func(source, candidate string) bool {
		if !strings.HasPrefix(source, argKey+".") {
			return false
		}
		rest := source[len(argKey)+1:]
		if cut := strings.IndexAny(rest, ".["); cut >= 0 {
			rest = rest[:cut]
		}
		return rest != "" && !writes.Fields[rest]
	}
}

type storageViewWriteCallee struct {
	decl *ast.FuncDecl
	args []ast.Expr
}

// storageViewWriteCallees resolves CALL for the write summary. A summary is computed for a callee
// whose body may not have been analyzed yet, so the analysis-time resolution
// (storageViewOriginCallee) often fails there; the fallback is every declaration sharing the
// callee's last name segment (the callEdgesOf over-approximation, including bodiless
// declarations, which summarize as writing everything). The union over all of them covers the
// real target. A name bound locally (a parameter or a declared local, possibly a closure) is not
// resolved by name, and neither is a call whose arity does not line up with every candidate.
func (a *Analyzer) storageViewWriteCallees(call *ast.CallExpr, locals map[string]bool) ([]storageViewWriteCallee, bool) {
	if decls, args, ok := a.storageViewOriginCallee(call); ok {
		out := make([]storageViewWriteCallee, 0, len(decls))
		for _, decl := range decls {
			out = append(out, storageViewWriteCallee{decl: decl, args: args})
		}
		return out, true
	}
	args := call.Args
	if call.ResolvedArgsValid {
		args = call.ResolvedArgs
	}
	target := call.Func
	if special, ok := target.(*ast.SpecializeExpr); ok && special != nil {
		target = special.Operand
	}
	var receiver ast.Expr
	name := ""
	switch callee := target.(type) {
	case *ast.Ident:
		if callee == nil || locals[callee.Name] {
			return nil, false
		}
		name = callee.Name
	case *ast.FieldExpr:
		if callee == nil {
			return nil, false
		}
		name = callee.Field
		receiver = callee.Object
	default:
		return nil, false
	}
	candidates := a.storageViewDeclsByLastName()[fillMayAdoptLastSegment(name)]
	if len(candidates) == 0 {
		return nil, false
	}
	// A candidate whose arity cannot accept this call (counting defaulted trailing parameters)
	// cannot be its target and is dropped; a call no candidate accepts is unresolved.
	out := make([]storageViewWriteCallee, 0, len(candidates))
	var methodArgs []ast.Expr
	for _, decl := range candidates {
		required := len(decl.Params)
		for required > 0 && decl.Params[required-1].DefaultValue != nil {
			required--
		}
		fits := func(count int) bool { return count >= required && count <= len(decl.Params) }
		if receiver != nil && fits(len(args)+1) {
			if methodArgs == nil {
				methodArgs = make([]ast.Expr, 1, len(args)+1)
				methodArgs[0] = receiver
				methodArgs = append(methodArgs, args...)
			}
			out = append(out, storageViewWriteCallee{decl: decl, args: methodArgs})
		}
		if fits(len(args)) {
			if receiver != nil {
				// `Type.f(x)` static form; a receiver VALUE here would be an unknown builtin.
				rootIdent, rooted := receiver.(*ast.Ident)
				if !rooted || rootIdent == nil {
					return nil, false
				}
				// A receiver that names a local or parameter is a value: the method form above
				// is the call, never the static one.
				if locals[rootIdent.Name] {
					continue
				}
			}
			out = append(out, storageViewWriteCallee{decl: decl, args: args})
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// storageViewScopeLocals answers the by-name fallback's "is this callee name a local?" at a call
// site under analysis: a name the current scope binds to a parameter or local (a closure value)
// is never resolved to a declaration by name.
func (a *Analyzer) storageViewScopeLocals(call *ast.CallExpr) map[string]bool {
	out := map[string]bool{}
	ident, isIdent := call.Func.(*ast.Ident)
	if !isIdent || ident == nil || a.currentScope == nil {
		return out
	}
	if sym, ok := a.currentScope.Lookup(ident.Name); ok && sym != nil && (sym.Kind == SymbolParam || sym.Kind == SymbolLocal) {
		out[ident.Name] = true
	}
	return out
}

func (a *Analyzer) storageViewDeclsByLastName() map[string][]*ast.FuncDecl {
	if a.storageViewWriteDeclsByName != nil {
		return a.storageViewWriteDeclsByName
	}
	a.storageViewWriteDeclsByName = map[string][]*ast.FuncDecl{}
	for decl := range a.funcDeclSymbols {
		if decl != nil {
			last := fillMayAdoptLastSegment(decl.Name)
			a.storageViewWriteDeclsByName[last] = append(a.storageViewWriteDeclsByName[last], decl)
		}
	}
	for _, decls := range a.storageViewWriteDeclsByName {
		sort.Slice(decls, func(i, j int) bool {
			if decls[i].Position.File != decls[j].Position.File {
				return decls[i].Position.File < decls[j].Position.File
			}
			return decls[i].Position.Offset < decls[j].Position.Offset
		})
	}
	return a.storageViewWriteDeclsByName
}

// storageViewDeclLocalNames: parameter names plus every name a node in the body binds.
func storageViewDeclLocalNames(decl *ast.FuncDecl, nodes []any) map[string]bool {
	out := map[string]bool{}
	for _, param := range decl.Params {
		out[param.Name] = true
	}
	for _, node := range nodes {
		storageViewCollectLocalNames(node, out)
	}
	return out
}

// Keep only nodes inspected by the write and unhandled-identifier passes.
// Names still come from every visited node, including type and pattern nodes.
var storageViewNodesPool sync.Pool

var storageViewHandledPool = sync.Pool{New: func() any { return make(map[*ast.Ident]bool) }}

func borrowStorageViewHandled() map[*ast.Ident]bool {
	return storageViewHandledPool.Get().(map[*ast.Ident]bool)
}

func releaseStorageViewHandled(handled map[*ast.Ident]bool) {
	if len(handled) > 8192 {
		return
	}
	clear(handled)
	storageViewHandledPool.Put(handled)
}

func borrowStorageViewNodes() []any {
	if nodes := storageViewNodesPool.Get(); nodes != nil {
		return nodes.([]any)[:0]
	}
	return nil
}

func releaseStorageViewNodes(nodes []any) {
	if cap(nodes) == 0 || cap(nodes) > 1<<14 {
		return
	}
	clear(nodes[:cap(nodes)])
	storageViewNodesPool.Put(nodes[:0])
}

func storageViewScanBody(decl *ast.FuncDecl) storageViewSolveBody {
	return storageViewScanBodyWithNodes(decl, nil)
}

func storageViewScanBodyWithNodes(decl *ast.FuncDecl, nodes []any) storageViewSolveBody {
	// Each top-level statement contributes a node, often with at least one
	// relevant expression. Cap the hint so unusually wide bodies do not reserve
	// unbounded unused space; nested bodies still grow normally.
	body := storageViewSolveBody{locals: make(map[string]bool, len(decl.Params))}
	hint := min(2*len(decl.Body), 2048)
	if cap(nodes) < hint {
		body.nodes = make([]any, len(nodes), hint)
		copy(body.nodes, nodes)
	} else {
		body.nodes = nodes
	}
	for _, param := range decl.Params {
		body.locals[param.Name] = true
	}
	fillMayAdoptWalk(reflect.ValueOf(decl.Body), func(node any) {
		storageViewCollectLocalNames(node, body.locals)
		switch node.(type) {
		case ast.Stmt, *ast.CallExpr, *ast.AddrOfExpr, *ast.FieldExpr, *ast.IndexExpr, *ast.Ident:
			body.nodes = append(body.nodes, node)
		}
	})
	return body
}

func storageViewCollectLocalNames(node any, out map[string]bool) {
	switch node.(type) {
	case *ast.Ident, *ast.FieldExpr:
		return
	}
	v := reflect.ValueOf(node)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			break
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	for _, i := range reflectMetaOf(v.Type()).storageViewNameFields {
		collectStorageViewStrings(v.Field(i), out)
	}
}

func collectStorageViewStrings(value reflect.Value, out map[string]bool) {
	switch value.Kind() {
	case reflect.String:
		if value.String() != "" {
			out[value.String()] = true
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			collectStorageViewStrings(value.Index(i), out)
		}
	}
}
