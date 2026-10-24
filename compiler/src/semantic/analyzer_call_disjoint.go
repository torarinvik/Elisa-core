package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// recordCallArgDisjoint computes and stores, for one call site, which container-reference
// arguments are provably backed by DISJOINT buffers — the general per-call `proven_distinct`
// predicate of docs/84 §3.1 (Increment 2). It is the interprocedural lift of the band-only fact
// computed in computeBandSourceDisjoint.
//
// Soundness rests on POSITIVE evidence only: a pair is recorded distinct solely when both
// arguments are the address of a "private-fresh" darray local — a local declared once from a
// fresh allocation whose whole darray value never escapes (so it can neither be header-copied
// from another buffer nor copied into one). Two private-fresh locals always hold private,
// never-shared buffers, so they are disjoint. Everything else (parameters, header copies
// `b = a`, views, self-aliasing `f(&a,&a)`) fails the predicate and is omitted — conservatively
// forgoing the optimization, never miscompiling.
//
// CRITICAL: the name-based "distinct fresh local" shortcut alone is UNSOUND, because
// recordValueBinding drops value bindings for MUTABLE locals, so a mutable header copy
// `b: mutable darray = a` leaves `a` and `b` as two distinct fresh-local NAMES that share one
// buffer. The private-fresh escape scan (privateFreshDArrayLocals) closes that hole: `b = a`
// both makes `b`'s initializer non-fresh and reads `a`'s whole value (an escape), disqualifying
// both sides.
//
// `args` is the param-aligned argument list (receiver + reordered named args + implicits) that
// validateCallArgAliasAccess also consumes, so parameter indices map correctly.
func (a *Analyzer) computeCallArgDisjoint(request pendingDisjointCall) {
	call, paramTypes, args := request.call, request.params, request.args
	if call == nil || a.callArgDisjoint == nil {
		return
	}
	limit := len(args)
	if len(paramTypes) < limit {
		limit = len(paramTypes)
	}
	// No pairwise evidence is possible with fewer than two container refs.
	// Avoid a whole-function provenance scan for scalar and single-ref calls.
	containers := 0
	for _, typ := range paramTypes[:limit] {
		if isContainerRefType(typ) {
			containers++
		}
	}
	if containers < 2 {
		return
	}
	private := a.privateFreshDArrayLocals()

	var containerParams []int
	argInfoByIdx := map[int]disjointArgRoots{}
	for i := 0; i < limit; i++ {
		if !isContainerRefType(paramTypes[i]) {
			continue
		}
		containerParams = append(containerParams, i)
		roots := request.roots[i]
		if len(roots) == 0 {
			// Unidentifiable buffer (opaque expression / unresolved provenance): no roots, so we
			// can neither confirm freshness nor non-overlap. Absence of evidence, never aliasing.
			continue
		}
		argInfoByIdx[i] = disjointArgRoots{
			roots: roots,
			fresh: request.localDecls[i] != nil && wholePrivateDArrayBorrow(args[i], private) && rootsAllPrivateFresh(roots, private),
		}
	}
	if len(containerParams) < 2 {
		return
	}

	distinct := map[[2]int]bool{}
	distinctCount := map[int]int{}
	for x := 0; x < len(containerParams); x++ {
		for y := x + 1; y < len(containerParams); y++ {
			i, j := containerParams[x], containerParams[y]
			ai, oki := argInfoByIdx[i]
			aj, okj := argInfoByIdx[j]
			if !oki || !okj {
				continue
			}
			if aliasRootSetsOverlap(ai.roots, aj.roots) {
				// Shared root ⇒ the same buffer is reachable from both args (the degenerate
				// `f(&a, &a)`, or a field-path overlap). Not distinct.
				continue
			}
			if !ai.fresh && !aj.fresh {
				// Both buffers pre-existed this call. Distinct NAMES do not imply distinct
				// buffers — a caller may have header-copied one into the other (`b = a`), so two
				// pre-existing roots can alias. Un-provable here; fail closed.
				continue
			}
			// At least one side is a PRIVATE-FRESH local: a buffer freshly allocated in this
			// function (`[]`/comprehension/`clone`) whose whole value provably never escapes, so
			// nothing else — no parameter, local, or field — can reference it. A brand-new buffer
			// therefore cannot alias any distinct-rooted other buffer. With non-overlapping roots
			// confirming the other side is not that same fresh local, the pair is provably disjoint.
			distinct[[2]int{i, j}] = true
			distinctCount[i]++
			distinctCount[j]++
		}
	}
	selfNoalias := map[int]bool{}
	others := len(containerParams) - 1
	for _, i := range containerParams {
		if others > 0 && distinctCount[i] == others {
			selfNoalias[i] = true
		}
	}

	// Register this call site for the whole-program per-callee fixpoint, with per-pair evidence
	// (direct fresh-anchor `base`, or a `depend` on the enclosing function's own params). Done for
	// every candidate-kernel call — even one with no distinct pair, since a site that contributes
	// nothing must keep the pair out of the callee's fact.
	if request.callee != nil {
		a.recordDisjointCallObservation(request.callee, callDisjointObservation{enclosing: request.fn, containerParams: containerParams, evidence: buildCapturedPairEvidence(containerParams, distinct, request.forwarded)})
	}

	if len(distinct) == 0 {
		return
	}

	a.callArgDisjoint[call] = &CallArgDisjointInfo{
		ContainerParams: containerParams,
		DistinctPairs:   distinct,
		SelfNoalias:     selfNoalias,
	}
	if request.fn != nil {
		if a.callArgDisjointByFunc == nil {
			a.callArgDisjointByFunc = map[*ast.FuncDecl]map[*ast.CallExpr]bool{}
		}
		if a.callArgDisjointByFunc[request.fn] == nil {
			a.callArgDisjointByFunc[request.fn] = map[*ast.CallExpr]bool{}
		}
		a.callArgDisjointByFunc[request.fn][call] = true
	}
}

// disjointArgRoots is the per-argument buffer evidence used by the pairwise disjointness check:
// the argument's alias roots and whether they all name private-fresh locals (brand-new buffers).
type disjointArgRoots struct {
	roots []string
	fresh bool
}

// isContainerRefType reports whether t is a reference to a growable container whose backing buffer
// participates in the disjointness analysis — currently `darray[T]&` / `mutable darray[T]&`.
// Immutability is irrelevant: disjointness is a property of buffers, not the access mode.
func isContainerRefType(t Type) bool {
	ref, ok := t.(*RefType)
	if !ok || ref == nil {
		return false
	}
	_, ok = StripAggregateStateType(ref.Elem).(*DArrayType)
	return ok
}

// rootsAllPrivateFresh reports whether every alias root names a whole private-fresh darray local
// (a bare name in `private`, no field/element path). Field-path roots are rejected conservatively.
func rootsAllPrivateFresh(roots []string, private map[string]bool) bool {
	if len(private) == 0 {
		return false
	}
	for _, root := range roots {
		if indexOfByte(root, '.') >= 0 {
			return false
		}
		if !private[root] {
			return false
		}
	}
	return true
}

// privateFreshDArrayLocals returns (memoized per function) the set of local names that hold a
// provably private, never-shared darray buffer in the current function body: declared exactly
// once from a fresh allocation, with the whole darray value never escaping. See the soundness
// note on recordCallArgDisjoint.
func (a *Analyzer) privateFreshDArrayLocals() map[string]bool {
	fn := a.currentFuncDecl
	if fn == nil {
		return nil
	}
	if a.privateFreshDArrayCache == nil {
		a.privateFreshDArrayCache = map[*ast.FuncDecl]map[string]bool{}
	}
	if set, ok := a.privateFreshDArrayCache[fn]; ok {
		return set
	}
	scan := &privateFreshScan{
		analyzer:  a,
		declCount: map[string]int{},
		fresh:     map[string]bool{},
		escaped:   map[string]bool{},
		shadowed:  headerLocalBindingNames(fn),
	}
	for _, param := range fn.Params {
		scan.declCount[param.Name]++
	}
	for _, stmt := range fn.Body {
		scan.scanValue(reflect.ValueOf(stmt))
	}
	set := map[string]bool{}
	for name, n := range scan.declCount {
		if n == 1 && scan.fresh[name] && !scan.escaped[name] {
			set[name] = true
		}
	}
	a.privateFreshDArrayCache[fn] = set
	return set
}

// privateFreshScan classifies darray locals by buffer provenance over a function body. It is an
// exhaustive reflect walk whose DEFAULT for any identifier is "escaped" (whole-value read); only
// the explicit place contexts below (`&x`, `x[i]`, `x.field`, slice bases) exempt an identifier.
// This fail-safe default is what makes the scan sound against AST shapes it does not special-case:
// an unrecognized container can over-mark (lose optimization) but can never under-mark an escape.
type privateFreshScan struct {
	analyzer            *Analyzer
	declCount           map[string]int
	fresh               map[string]bool
	escaped             map[string]bool
	valueSeen           map[any]struct{}
	escapeSeen          map[any]struct{}
	storedBorrowSeen    map[any]bool
	headerStable        map[*ast.FuncDecl]bool
	shadowed            map[string]bool
	discardedCalls      map[*ast.CallExpr]bool
	directDiscardedCall *ast.CallExpr
}

func (s *privateFreshScan) scanValue(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return
		}
		if v.Kind() == reflect.Interface {
			s.scanValue(v.Elem())
			return
		}
		// Place identifiers bypass scanValue, so a later whole-value use still
		// marks them escaped. This set only deduplicates value traversal.
		node := v.Interface()
		if call, ok := node.(*ast.CallExpr); ok {
			// Use context is an occurrence property, so inspect it before the
			// pointer-visited guard. A value use revokes freshness even when the
			// same call was already visited as a discarded statement.
			if s.directDiscardedCall != call && s.isPrivateReceiverBuiltin(call) {
				if field, ok := stripOptimizationParens(call.Func).(*ast.FieldExpr); ok {
					s.markAllNamesEscaped(reflect.ValueOf(field.Object))
				}
			}
			saved := s.directDiscardedCall
			s.directDiscardedCall = nil
			defer func() { s.directDiscardedCall = saved }()
		}
		if _, visited := s.valueSeen[node]; visited {
			return
		}
		if s.valueSeen == nil {
			s.valueSeen = make(map[any]struct{})
		}
		s.valueSeen[node] = struct{}{}
		switch n := v.Interface().(type) {
		case *ast.ExprStmt:
			saved := s.directDiscardedCall
			if call, ok := stripOptimizationParens(n.Expr).(*ast.CallExpr); ok {
				s.directDiscardedCall = call
				if s.discardedCalls == nil {
					s.discardedCalls = make(map[*ast.CallExpr]bool)
				}
				s.discardedCalls[call] = true
			} else {
				s.directDiscardedCall = nil
			}
			s.scanExpr(n.Expr)
			s.directDiscardedCall = saved
			return
		case *ast.Ident:
			s.escaped[n.Name] = true
			return
		case *ast.CastExpr:
			if _, reference := n.Target.(*ast.RefType); reference {
				s.markAllNamesEscaped(reflect.ValueOf(n.Operand))
			}
		case *ast.AddrOfExpr:
			s.scanPlace(n.Operand)
			return
		case *ast.FieldExpr:
			if n.Field == "items" {
				s.markAllNamesEscaped(reflect.ValueOf(n.Object))
			}
			s.scanPlace(n.Object)
			return
		case *ast.IndexExpr:
			s.scanPlace(n.Object)
			s.scanExpr(n.Index)
			s.scanExpr(n.Fallback)
			return
		case *ast.SliceExpr:
			// A view retains the backing buffer rather than copying its elements.
			s.markAllNamesEscaped(reflect.ValueOf(n.Object))
			s.scanExpr(n.Start)
			s.scanExpr(n.End)
			return
		case *ast.AssignStmt:
			if _, field := stripOptimizationParens(n.Target).(*ast.FieldExpr); field && containerHeaderRoot(n.Target) != "" {
				s.markAllNamesEscaped(reflect.ValueOf(n.Target))
			}
			s.markStoredBorrowRoots(reflect.ValueOf(n.Value))
		case *ast.ReturnStmt:
			s.markStoredBorrowRoots(reflect.ValueOf(n.Value))
		case *ast.VarDeclStmt:
			s.markStoredBorrowRoots(reflect.ValueOf(n.Value))
			s.recordDecl(n)
			// Fall through to generic recursion so the initializer (and any nested
			// statements) are scanned for escapes of OTHER names.
		case *ast.CallExpr:
			// A UFCS/method receiver is also an exposed header. Unresolved
			// methods have no header-preservation proof.
			if field, ok := stripOptimizationParens(n.Func).(*ast.FieldExpr); ok && (s.analyzer == nil || !s.isPrivateReceiverBuiltin(n) || !s.discardedCalls[n]) {
				s.markAllNamesEscaped(reflect.ValueOf(field.Object))
			}
			// Aggregate arguments have no preservation summary for their nested
			// references. Only a direct container borrow may use the exemption.
			actualArgs := n.Args
			if n.ResolvedArgsValid {
				actualArgs = n.ResolvedArgs
			}
			for _, args := range [2][]ast.Expr{actualArgs, n.ResolvedImplicitArgs} {
				for _, arg := range args {
					if !aliasExprIsExplicitBorrow(arg) {
						s.markStoredBorrowRoots(reflect.ValueOf(arg))
					}
				}
			}
			if s.analyzer != nil && s.isPrivateReceiverBuiltin(n) {
				for _, arg := range n.Args {
					s.markStoredBorrowRoots(reflect.ValueOf(arg))
				}
			}
			borrowed := false
			for _, args := range [2][]ast.Expr{actualArgs, n.ResolvedImplicitArgs} {
				for _, arg := range args {
					borrowed = borrowed || aliasExprIsExplicitBorrow(arg)
				}
			}
			if borrowed && s.analyzer != nil && !s.isCloneCallExpr(n) && !(s.discardedCalls[n] && s.isPrivateReceiverBuiltin(n)) {
				decl, resolved := s.analyzer.uniqueHeaderCallTarget(n, s.shadowed)
				stable, known := s.headerStable[decl]
				if resolved && !known {
					// Bodies and resolution remain unchanged during this provenance scan.
					stable = s.analyzer.containerHeadersStable(decl)
					if s.headerStable == nil {
						s.headerStable = make(map[*ast.FuncDecl]bool)
					}
					s.headerStable[decl] = stable
				}
				if !resolved || !stable {
					for _, args := range [2][]ast.Expr{actualArgs, n.ResolvedImplicitArgs} {
						for _, arg := range args {
							if aliasExprIsExplicitBorrow(arg) {
								s.markAllNamesEscaped(reflect.ValueOf(arg))
							}
						}
					}
				}
			}
			if s.isCloneCallExpr(n) {
				// clone[darray[T]](x) deep-copies elements into a fresh destination. Reading
				// the source container to copy its elements does not header-copy or share its
				// backing buffer, so a bare source local remains private-fresh.
				for _, arg := range n.Args {
					s.scanPlace(arg)
				}
				return
			}
		case *ast.TupleBindStmt, *ast.LetDestructureStmt, *ast.MoveBindStmt, *ast.AsRefAssignStmt:
			// Binding forms whose target names are plain strings (not *ast.Ident), so the
			// default value-position walk would miss them. Mark every name they touch escaped.
			s.markAllNamesEscaped(v)
			return
		}
		s.scanValue(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			s.scanValue(v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			s.scanValue(v.Index(i))
		}
	}
}

// scanPlace handles the base of a place expression (`x` in `&x`, `x[i]`, `x.f`): a bare identifier
// there is a borrow / element / header-field access that does NOT copy or alias the whole buffer,
// so it is exempt from the escape default. Compound bases recurse as values.
func (s *privateFreshScan) scanPlace(e ast.Expr) {
	e = stripOptimizationParens(e)
	if e == nil {
		return
	}
	if _, ok := e.(*ast.Ident); ok {
		return
	}
	s.scanValue(reflect.ValueOf(e))
}

func (s *privateFreshScan) scanExpr(e ast.Expr) {
	if e == nil {
		return
	}
	s.scanValue(reflect.ValueOf(e))
}

func (s *privateFreshScan) recordDecl(n *ast.VarDeclStmt) {
	if n == nil || n.Name == "" {
		return
	}
	s.declCount[n.Name]++
	if s.isFreshDArrayAllocExpr(n.Value) {
		s.fresh[n.Name] = true
	}
}

// markAllNamesEscaped conservatively marks every identifier and every `Name string` struct field
// reachable from a node as escaped. Used for binding statements whose targets are name strings.
func (s *privateFreshScan) markAllNamesEscaped(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return
		}
		// This mode has broader escape semantics than scanValue; never share
		// its visited set with the place-aware traversal.
		if v.Kind() == reflect.Pointer {
			node := v.Interface()
			if _, visited := s.escapeSeen[node]; visited {
				return
			}
			if s.escapeSeen == nil {
				s.escapeSeen = make(map[any]struct{})
			}
			s.escapeSeen[node] = struct{}{}
		}
		if ident, ok := v.Interface().(*ast.Ident); ok {
			s.escaped[ident.Name] = true
			return
		}
		s.markAllNamesEscaped(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.Name == "Name" && v.Field(i).Kind() == reflect.String {
				if name := v.Field(i).String(); name != "" {
					s.escaped[name] = true
				}
				continue
			}
			s.markAllNamesEscaped(v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			s.markAllNamesEscaped(v.Index(i))
		}
	}
}

// isFreshDArrayAllocExpr reports whether an initializer is a fresh darray allocation that yields a
// brand-new, unaliased buffer: a list literal `[...]` (including empty `[]`), a darray
// comprehension, or `clone(...)`. Anything else (an identifier, a call returning a view, a slice)
// is conservatively NOT fresh.
func (s *privateFreshScan) isFreshDArrayAllocExpr(e ast.Expr) bool {
	switch n := stripOptimizationParens(e).(type) {
	case *ast.ListLitExpr:
		// Non-brace `[...]` is a list/darray literal; brace `{...}` is a dict/set literal.
		return !n.Brace
	case *ast.ListComprehensionExpr:
		// Darray comprehension only (dict/set comprehensions are not darray buffers).
		return n.Key == nil && !n.Set
	case *ast.CallExpr:
		if s.isCloneCallExpr(n) {
			return true
		}
	}
	return false
}

func (s *privateFreshScan) isCloneCallExpr(call *ast.CallExpr) bool {
	if s == nil || s.analyzer == nil || call == nil {
		return false
	}
	return s.analyzer.isCompilerBuiltinHelperCall(call, "clone")
}

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
	defer func() { a.currentFuncDecl = savedFn }()
	a.privateFreshDArrayCache = nil
	// Bodies and call identities are now fixed for this analysis.
	a.headerStabilityCompleted = make(map[*ast.FuncDecl]bool)
	pending := a.pendingDisjointCalls
	a.pendingDisjointCalls = nil
	for _, request := range pending {
		a.currentFuncDecl = request.fn
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
