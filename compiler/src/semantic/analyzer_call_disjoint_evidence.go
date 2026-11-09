package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

// enclosingParamIndexForArg resolves a call argument to the index of the enclosing function's
// parameter it forwards (`p` or `&p` where p is a parameter), or false. A parameter that the
// enclosing function whole-value-REASSIGNS (`p <- other`) is rejected: such a param no longer
// holds its incoming buffer, so the caller's disjointness guarantee about it would be stale —
// trusting it would be unsound. Element stores (`p[i] <- x`) keep p's buffer and do not disqualify.
func (a *Analyzer) enclosingParamIndexForArg(arg ast.Expr) (int, bool) {
	if a.currentFuncDecl == nil || a.currentScope == nil {
		return 0, false
	}
	ident, ok := stripAddrAndParens(arg).(*ast.Ident)
	if !ok || ident == nil {
		return 0, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil || sym.Kind != SymbolParam {
		return 0, false
	}
	if a.reassignedParamNames()[ident.Name] {
		return 0, false
	}
	return sym.ParamIndex, true
}

// stripAddrAndParens peels `&` and parens so `&p`, `(p)`, and `p` all resolve to the inner place.
func stripAddrAndParens(e ast.Expr) ast.Expr {
	for {
		e = stripOptimizationParens(e)
		if addr, ok := e.(*ast.AddrOfExpr); ok && addr != nil {
			e = addr.Operand
			continue
		}
		return e
	}
}

// reassignedParamNames returns (memoized per function) the set of names that appear as the target
// of a whole-value reassignment (`name <- …`, `name += …`, `name as … = …`) anywhere in the
// current function body. A bare-identifier target rebinds the variable's whole value; an indexed
// or field target (`name[i] <- …`) does not and is ignored. Conservative and flow-insensitive: a
// param reassigned anywhere is excluded from forwarding evidence everywhere (lost optimization,
// never a miscompile).
func (a *Analyzer) reassignedParamNames() map[string]bool {
	fn := a.currentFuncDecl
	if fn == nil {
		return nil
	}
	if a.reassignedParamCache == nil {
		a.reassignedParamCache = map[*ast.FuncDecl]map[string]bool{}
	}
	if set, ok := a.reassignedParamCache[fn]; ok {
		return set
	}
	set := map[string]bool{}
	for _, stmt := range fn.Body {
		collectReassignedTargets(reflect.ValueOf(stmt), set)
	}
	a.reassignedParamCache[fn] = set
	return set
}

// collectReassignedTargets reflect-walks statements, adding the bare-identifier target name of
// every whole-value assignment form to `set`.
func collectReassignedTargets(v reflect.Value, set map[string]bool) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return
		}
		switch n := v.Interface().(type) {
		case *ast.AssignStmt:
			recordReassignTarget(n.Target, set)
		case *ast.AugAssignStmt:
			recordReassignTarget(n.Target, set)
		case *ast.AsRefAssignStmt:
			recordReassignTarget(n.Target, set)
		}
		collectReassignedTargets(v.Elem(), set)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			collectReassignedTargets(v.Field(i), set)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			collectReassignedTargets(v.Index(i), set)
		}
	}
}

// recordReassignTarget adds the target name when it is a bare identifier (a whole-value rebind);
// indexed/field targets are element/field stores that do not rebind the variable, so are ignored.
func recordReassignTarget(target ast.Expr, set map[string]bool) {
	if ident, ok := stripOptimizationParens(target).(*ast.Ident); ok && ident != nil {
		set[ident.Name] = true
	}
}

// Stored branch/block results can retain a header reference even when the RHS
// is not itself an AddrOfExpr. Calls are handled separately by callee summaries.
func (s *privateFreshScan) markStoredBorrowRoots(root reflect.Value) {
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() || s.storedBorrowSeen[v.Interface()] {
				return
			}
			if s.storedBorrowSeen == nil {
				s.storedBorrowSeen = make(map[any]bool)
			}
			s.storedBorrowSeen[v.Interface()] = true
			switch n := v.Interface().(type) {
			case *ast.CallExpr:
				if field, ok := stripOptimizationParens(n.Func).(*ast.FieldExpr); ok {
					s.markAllNamesEscaped(reflect.ValueOf(field.Object))
				}
				return
			case *ast.AddrOfExpr:
				s.markAllNamesEscaped(reflect.ValueOf(n.Operand))
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(root)
}

// These compiler-identified mutators preserve private buffer ownership when
// their returned header reference is discarded. Unknown methods fail closed.
func (s *privateFreshScan) isPrivateReceiverBuiltin(call *ast.CallExpr) bool {
	return s.analyzer != nil && (s.analyzer.isCompilerBuiltinHelperCall(call, "darray.push") || s.analyzer.isCompilerBuiltinHelperCall(call, "darray.reserve"))
}

// An outer array allocation proves nothing about buffers of its elements.
// Freshness applies only to the exact whole local whose allocation was scanned.
func wholePrivateDArrayBorrow(expr ast.Expr, private map[string]bool) bool {
	addr, ok := stripOptimizationParens(expr).(*ast.AddrOfExpr)
	if !ok {
		return false
	}
	id, ok := stripOptimizationParens(addr.Operand).(*ast.Ident)
	return ok && private[id.Name]
}

// Resolve provenance only after all bodies have acquired compiler-owned call
// identities. Retain the call's lexical context for forwarding/name resolution.
type pendingDisjointCall struct {
	call       *ast.CallExpr
	params     []Type
	args       []ast.Expr
	fn         *ast.FuncDecl
	roots      [][]string
	localDecls map[int]*ast.VarDeclStmt
	forwarded  map[int]int
	callee     *ast.FuncDecl
}

func (a *Analyzer) recordCallArgDisjoint(call *ast.CallExpr, params []Type, args []ast.Expr) {
	if call == nil || a.callArgDisjoint == nil {
		return
	}
	limit := len(params)
	if len(args) < limit {
		limit = len(args)
	}
	count := 0
	for _, t := range params[:limit] {
		if isContainerRefType(t) {
			count++
		}
	}
	if count < 2 {
		return
	}
	request := pendingDisjointCall{call: call, params: append([]Type(nil), params...), args: append([]ast.Expr(nil), args...), fn: a.currentFuncDecl, roots: make([][]string, limit), forwarded: map[int]int{}, localDecls: map[int]*ast.VarDeclStmt{}}
	request.callee, _ = a.resolveDirectCallFuncDecl(call)
	for i, t := range params[:limit] {
		if !isContainerRefType(t) {
			continue
		}
		request.roots[i] = append([]string(nil), a.aliasRootsForExpr(args[i])...)
		if addr, ok := stripOptimizationParens(args[i]).(*ast.AddrOfExpr); ok && a.currentScope != nil {
			if id, ok := stripOptimizationParens(addr.Operand).(*ast.Ident); ok {
				if symbol, ok := a.currentScope.Lookup(id.Name); ok && symbol.Kind == SymbolLocal {
					request.localDecls[i], _ = symbol.Node.(*ast.VarDeclStmt)
				}
			}
		}
		if index, ok := a.enclosingParamIndexForArg(args[i]); ok {
			request.forwarded[i] = index
		}
	}
	a.pendingDisjointCalls = append(a.pendingDisjointCalls, request)
}
func (a *Analyzer) finalizeCallArgDisjoint() {
	savedFn := a.currentFuncDecl
	defer func() { a.currentFuncDecl, a.storeLivedCache = savedFn, nil }()
	a.privateFreshDArrayCache = nil
	// Bodies and call identities are now fixed for this analysis.
	a.headerStabilityCompleted = make(map[*ast.FuncDecl]bool)
	pending := a.pendingDisjointCalls
	a.pendingDisjointCalls = nil
	for _, request := range pending {
		a.currentFuncDecl = request.fn
		a.storeLivedCache = nil
		a.computeCallArgDisjoint(request)
	}
}

func buildCapturedPairEvidence(params []int, distinct map[[2]int]bool, forwarded map[int]int) map[[2]int]pairDisjointEvidence {
	evidence := map[[2]int]pairDisjointEvidence{}
	for x, i := range params {
		for _, j := range params[x+1:] {
			pair := [2]int{i, j}
			if distinct[pair] {
				evidence[pair] = pairDisjointEvidence{base: true}
				continue
			}
			p, okp := forwarded[i]
			q, okq := forwarded[j]
			if okp && okq && p != q {
				if p > q {
					p, q = q, p
				}
				evidence[pair] = pairDisjointEvidence{depend: true, depP: p, depQ: q}
			}
		}
	}
	return evidence
}
