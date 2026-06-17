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
func (a *Analyzer) recordCallArgDisjoint(call *ast.CallExpr, paramTypes []Type, args []ast.Expr) {
	if call == nil || a.callArgDisjoint == nil {
		return
	}
	limit := len(args)
	if len(paramTypes) < limit {
		limit = len(paramTypes)
	}
	private := a.privateFreshDArrayLocals()

	var containerParams []int
	argInfoByIdx := map[int]disjointArgRoots{}
	for i := 0; i < limit; i++ {
		if !isContainerRefType(paramTypes[i]) {
			continue
		}
		containerParams = append(containerParams, i)
		roots := a.aliasRootsForExpr(args[i])
		if len(roots) == 0 {
			// Unidentifiable buffer (opaque expression / unresolved provenance): no roots, so we
			// can neither confirm freshness nor non-overlap. Absence of evidence, never aliasing.
			continue
		}
		argInfoByIdx[i] = disjointArgRoots{
			roots: roots,
			fresh: rootsAllPrivateFresh(roots, private),
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

	// Register this call site for whole-program per-callee aggregation (Increment 3a),
	// EVEN WHEN no pair is distinct here: a non-distinct site must kill pairs that other
	// sites prove, so the cross-call-site intersection stays sound. Done before the
	// len(distinct)==0 early return below for exactly that reason.
	a.registerCallDisjointObservation(call, containerParams, distinct, selfNoalias)

	if len(distinct) == 0 {
		return
	}

	a.callArgDisjoint[call] = &CallArgDisjointInfo{
		ContainerParams: containerParams,
		DistinctPairs:   distinct,
		SelfNoalias:     selfNoalias,
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
		declCount: map[string]int{},
		fresh:     map[string]bool{},
		escaped:   map[string]bool{},
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
	declCount map[string]int
	fresh     map[string]bool
	escaped   map[string]bool
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
		switch n := v.Interface().(type) {
		case *ast.Ident:
			s.escaped[n.Name] = true
			return
		case *ast.AddrOfExpr:
			s.scanPlace(n.Operand)
			return
		case *ast.FieldExpr:
			s.scanPlace(n.Object)
			return
		case *ast.IndexExpr:
			s.scanPlace(n.Object)
			s.scanExpr(n.Index)
			s.scanExpr(n.Fallback)
			return
		case *ast.SliceExpr:
			s.scanPlace(n.Object)
			s.scanExpr(n.Start)
			s.scanExpr(n.End)
			return
		case *ast.VarDeclStmt:
			s.recordDecl(n)
			// Fall through to generic recursion so the initializer (and any nested
			// statements) are scanned for escapes of OTHER names.
		case *ast.CallExpr:
			if isCloneCallExpr(n) {
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
	if isFreshDArrayAllocExpr(n.Value) {
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
func isFreshDArrayAllocExpr(e ast.Expr) bool {
	switch n := stripOptimizationParens(e).(type) {
	case *ast.ListLitExpr:
		// Non-brace `[...]` is a list/darray literal; brace `{...}` is a dict/set literal.
		return !n.Brace
	case *ast.ListComprehensionExpr:
		// Darray comprehension only (dict/set comprehensions are not darray buffers).
		return n.Key == nil && !n.Set
	case *ast.CallExpr:
		if isCloneCallExpr(n) {
			return true
		}
	}
	return false
}

func isCloneCallExpr(call *ast.CallExpr) bool {
	if call == nil {
		return false
	}
	fn := stripOptimizationParens(call.Func)
	if spec, ok := fn.(*ast.SpecializeExpr); ok && spec != nil {
		fn = stripOptimizationParens(spec.Operand)
	}
	ident, ok := fn.(*ast.Ident)
	return ok && ident.Name == "clone"
}
