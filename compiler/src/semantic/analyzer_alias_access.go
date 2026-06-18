package semantic

import (
	"strings"

	"elisacore/src/ast"
)

type aliasAccessState struct {
	ReadShared     int
	WriteExclusive int
}

type aliasAccessBinding struct {
	// Roots names every storage root the bound reference may alias. Usually one (a `&x` borrow),
	// but a reference returned from a call that aliases several parameters carries one root per
	// aliased argument — the reference may be any of them, so all are tracked.
	Roots []string
	Mode  aliasAccessMode
}

type aliasAccessMode int

const (
	aliasAccessRead aliasAccessMode = iota
	aliasAccessWrite
)

func (a *Analyzer) cloneAliasAccesses() map[string]aliasAccessState {
	if len(a.currentAliasAccesses) == 0 {
		return map[string]aliasAccessState{}
	}
	clone := make(map[string]aliasAccessState, len(a.currentAliasAccesses))
	for root, state := range a.currentAliasAccesses {
		clone[root] = state
	}
	return clone
}

func (a *Analyzer) cloneAliasBindings() map[*Symbol]aliasAccessBinding {
	if len(a.currentAliasBindings) == 0 {
		return map[*Symbol]aliasAccessBinding{}
	}
	clone := make(map[*Symbol]aliasAccessBinding, len(a.currentAliasBindings))
	for sym, binding := range a.currentAliasBindings {
		clone[sym] = binding
	}
	return clone
}

func (a *Analyzer) cloneAliasCarriers() map[string][]string {
	if len(a.currentAliasCarriers) == 0 {
		return map[string][]string{}
	}
	clone := make(map[string][]string, len(a.currentAliasCarriers))
	for name, roots := range a.currentAliasCarriers {
		clone[name] = append([]string(nil), roots...)
	}
	return clone
}

func (a *Analyzer) cloneAliasCarrierFieldOverrides() map[string]map[string][]string {
	if len(a.currentAliasCarrierFieldOverrides) == 0 {
		return map[string]map[string][]string{}
	}
	clone := make(map[string]map[string][]string, len(a.currentAliasCarrierFieldOverrides))
	for name, fields := range a.currentAliasCarrierFieldOverrides {
		fieldClone := make(map[string][]string, len(fields))
		for field, roots := range fields {
			fieldClone[field] = append([]string(nil), roots...)
		}
		clone[name] = fieldClone
	}
	return clone
}

func mergeAliasCarrierRoots(dst []string, src []string) []string {
	if len(dst) == 0 {
		return append([]string(nil), src...)
	}
	seen := map[string]bool{}
	for _, root := range dst {
		seen[root] = true
	}
	out := append([]string(nil), dst...)
	for _, root := range src {
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		out = append(out, root)
	}
	return out
}

func mergeAliasCarriers(dst map[string][]string, src map[string][]string) map[string][]string {
	if dst == nil && src == nil {
		return nil
	}
	merged := map[string][]string{}
	for name, roots := range dst {
		merged[name] = mergeAliasCarrierRoots(nil, roots)
	}
	for name, roots := range src {
		merged[name] = mergeAliasCarrierRoots(merged[name], roots)
	}
	return merged
}

func mergeAliasCarrierFieldOverrides(
	dstCarriers map[string][]string,
	dstOverrides map[string]map[string][]string,
	srcCarriers map[string][]string,
	srcOverrides map[string]map[string][]string,
) map[string]map[string][]string {
	fieldsByName := map[string]map[string]bool{}
	collect := func(overrides map[string]map[string][]string) {
		for name, fields := range overrides {
			if fieldsByName[name] == nil {
				fieldsByName[name] = map[string]bool{}
			}
			for field := range fields {
				fieldsByName[name][field] = true
			}
		}
	}
	collect(dstOverrides)
	collect(srcOverrides)
	if len(fieldsByName) == 0 {
		return map[string]map[string][]string{}
	}
	merged := map[string]map[string][]string{}
	effective := func(carriers map[string][]string, overrides map[string]map[string][]string, name, field string) []string {
		if fields := overrides[name]; fields != nil {
			if roots, ok := fields[field]; ok {
				return roots
			}
		}
		return carriers[name]
	}
	for name, fields := range fieldsByName {
		for field := range fields {
			roots := mergeAliasCarrierRoots(nil, effective(dstCarriers, dstOverrides, name, field))
			roots = mergeAliasCarrierRoots(roots, effective(srcCarriers, srcOverrides, name, field))
			if merged[name] == nil {
				merged[name] = map[string][]string{}
			}
			merged[name][field] = roots
		}
	}
	return merged
}

func mergeAliasCarrierSnapshot(carriers map[string][]string, overrides map[string]map[string][]string, snapshot affineFlowSnapshot) (map[string][]string, map[string]map[string][]string) {
	overrides = mergeAliasCarrierFieldOverrides(carriers, overrides, snapshot.AliasCarriers, snapshot.AliasCarrierFieldOverrides)
	carriers = mergeAliasCarriers(carriers, snapshot.AliasCarriers)
	return carriers, overrides
}

func mergeAliasCarrierBaseline(carriers map[string][]string, overrides map[string]map[string][]string, baselineCarriers map[string][]string, baselineOverrides map[string]map[string][]string) (map[string][]string, map[string]map[string][]string) {
	overrides = mergeAliasCarrierFieldOverrides(carriers, overrides, baselineCarriers, baselineOverrides)
	carriers = mergeAliasCarriers(carriers, baselineCarriers)
	return carriers, overrides
}

func (a *Analyzer) propagateAliasCarrierBlockEffects(parent, block *Scope, savedCarriers map[string][]string, savedOverrides map[string]map[string][]string) (map[string][]string, map[string]map[string][]string) {
	carriers := cloneAliasCarrierMap(savedCarriers)
	overrides := cloneAliasCarrierOverrideMap(savedOverrides)
	names := map[string]bool{}
	for name := range savedCarriers {
		names[name] = true
	}
	for name := range savedOverrides {
		names[name] = true
	}
	for name := range a.currentAliasCarriers {
		names[name] = true
	}
	for name := range a.currentAliasCarrierFieldOverrides {
		names[name] = true
	}
	for name := range names {
		if block != nil {
			if _, shadowed := block.Symbols[name]; shadowed {
				continue
			}
		}
		if parent != nil {
			if _, ok := parent.Lookup(name); !ok {
				continue
			}
		}
		if roots, ok := a.currentAliasCarriers[name]; ok {
			carriers[name] = append([]string(nil), roots...)
		} else {
			delete(carriers, name)
		}
		if fields, ok := a.currentAliasCarrierFieldOverrides[name]; ok {
			if overrides == nil {
				overrides = map[string]map[string][]string{}
			}
			overrides[name] = cloneAliasCarrierFieldMap(fields)
		} else {
			delete(overrides, name)
		}
	}
	return carriers, overrides
}

func cloneAliasCarrierMap(src map[string][]string) map[string][]string {
	if len(src) == 0 {
		return map[string][]string{}
	}
	clone := make(map[string][]string, len(src))
	for name, roots := range src {
		clone[name] = append([]string(nil), roots...)
	}
	return clone
}

func cloneAliasCarrierOverrideMap(src map[string]map[string][]string) map[string]map[string][]string {
	if len(src) == 0 {
		return map[string]map[string][]string{}
	}
	clone := make(map[string]map[string][]string, len(src))
	for name, fields := range src {
		clone[name] = cloneAliasCarrierFieldMap(fields)
	}
	return clone
}

func cloneAliasCarrierFieldMap(src map[string][]string) map[string][]string {
	if len(src) == 0 {
		return map[string][]string{}
	}
	clone := make(map[string][]string, len(src))
	for field, roots := range src {
		clone[field] = append([]string(nil), roots...)
	}
	return clone
}

// recordStructAliasCarrier records (or clears) a non-reference local's laundered-reference content.
// When `value` is a reference-returning call that aliases parameters, the local carries those
// param roots; otherwise any stale carrier is dropped (a whole-local rebind replaces the content).
func (a *Analyzer) recordStructAliasCarrier(name string, value ast.Expr) {
	if name == "" {
		return
	}
	var roots []string
	if call, ok := stripOptimizationParens(value).(*ast.CallExpr); ok {
		roots = a.callReturnAliasedRoots(call)
	}
	if len(roots) == 0 {
		delete(a.currentAliasCarriers, name)
		delete(a.currentAliasCarrierFieldOverrides, name)
		return
	}
	if a.currentAliasCarriers == nil {
		a.currentAliasCarriers = map[string][]string{}
	}
	a.currentAliasCarriers[name] = roots
	delete(a.currentAliasCarrierFieldOverrides, name)
}

// recordStructAliasCarrierFieldAssignment overrides the carried roots for a reference field on a
// struct/value local. Whole-local carriers are coarse ("this value carries roots from wrap(v)"), so
// `r.p <- w` needs a field-specific replacement to avoid stale `r.p` roots while preserving any
// other fields on `r`.
func (a *Analyzer) recordStructAliasCarrierFieldAssignment(target ast.Expr, targetType Type, value ast.Expr) {
	field, ok := stripOptimizationParens(target).(*ast.FieldExpr)
	if !ok {
		return
	}
	if _, isRef := StripAggregateStateType(targetType).(*RefType); !isRef {
		return
	}
	ident, ok := stripOptimizationParens(field.Object).(*ast.Ident)
	if !ok || ident.Name == "" {
		return
	}
	if _, hasCarrier := a.currentAliasCarriers[ident.Name]; !hasCarrier {
		return
	}
	roots := a.aliasRootsForExpr(value)
	if a.currentAliasCarrierFieldOverrides == nil {
		a.currentAliasCarrierFieldOverrides = map[string]map[string][]string{}
	}
	fields := a.currentAliasCarrierFieldOverrides[ident.Name]
	if fields == nil {
		fields = map[string][]string{}
		a.currentAliasCarrierFieldOverrides[ident.Name] = fields
	}
	fields[field.Field] = append([]string(nil), roots...)
}

func (a *Analyzer) recordUnsafeAliasExpr(expr ast.Expr) {
	if expr == nil || !a.enforceUnsafePermissions {
		return
	}
	if a.unsafeAliasExprs != nil {
		a.unsafeAliasExprs[expr] = true
	}
	a.recordFunctionPermissionRefs(unsafeAliasRefs(expr.Pos()))
}

func (a *Analyzer) recordUnsafeAliasStmt(stmt ast.Stmt) {
	if stmt == nil || !a.enforceUnsafePermissions {
		return
	}
	if a.unsafeAliasStmts != nil {
		a.unsafeAliasStmts[stmt] = true
	}
	a.recordFunctionPermissionRefs(unsafeAliasRefs(stmt.Pos()))
}

func (a *Analyzer) exprRequiresUnsafeAlias(expr ast.Expr) bool {
	if expr == nil || a.unsafeAliasExprs == nil {
		return false
	}
	return a.unsafeAliasExprs[expr]
}

func (a *Analyzer) stmtRequiresUnsafeAlias(stmt ast.Stmt) bool {
	if stmt == nil || a.unsafeAliasStmts == nil {
		return false
	}
	return a.unsafeAliasStmts[stmt]
}

func refAliasAccessMode(t Type) (aliasAccessMode, bool) {
	ref, ok := t.(*RefType)
	if !ok || ref == nil {
		return aliasAccessRead, false
	}
	if ref.Mutable {
		return aliasAccessWrite, true
	}
	return aliasAccessRead, true
}

func typeExprHasExplicitMutableRef(expr ast.TypeExpr) bool {
	switch n := expr.(type) {
	case *ast.MutableType:
		return true
	case *ast.RefType:
		return typeExprHasExplicitMutableRef(n.Elem)
	default:
		return false
	}
}

// aliasRootsOverlap reports whether two alias roots name overlapping memory. Exact
// equality aside, a whole-object root (`node`) overlaps any of its field/element roots
// (`node.value`) — borrowing both mutably aliases the same storage. Disjoint fields
// (`node.x` vs `node.y`) do not overlap.
func aliasRootsOverlap(a string, b string) bool {
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+".") || strings.HasPrefix(b, a+".")
}

// aliasMapHasOverlapConflict reports whether any root in `accesses` that overlaps `root`
// (including `root` itself) holds an access conflicting with `mode`.
func aliasMapHasOverlapConflict(accesses map[string]aliasAccessState, root string, mode aliasAccessMode) bool {
	for other, state := range accesses {
		if !aliasRootsOverlap(root, other) {
			continue
		}
		if aliasAccessConflicts(state, mode) {
			return true
		}
	}
	return false
}

func aliasAccessConflicts(existing aliasAccessState, mode aliasAccessMode) bool {
	if mode == aliasAccessWrite {
		return existing.WriteExclusive > 0 || existing.ReadShared > 0
	}
	return existing.WriteExclusive > 0
}

func aliasAccessStateWith(state aliasAccessState, mode aliasAccessMode) aliasAccessState {
	if mode == aliasAccessWrite {
		state.WriteExclusive++
	} else {
		state.ReadShared++
	}
	return state
}

func aliasAccessStateWithout(state aliasAccessState, mode aliasAccessMode) aliasAccessState {
	if mode == aliasAccessWrite {
		if state.WriteExclusive > 0 {
			state.WriteExclusive--
		}
	} else if state.ReadShared > 0 {
		state.ReadShared--
	}
	return state
}

func aliasAccessStateEmpty(state aliasAccessState) bool {
	return state.ReadShared == 0 && state.WriteExclusive == 0
}

func (a *Analyzer) recordLocalRefAliasAccess(stmt ast.Stmt, value ast.Expr, bindingType Type) {
	a.recordLocalRefAliasBinding(stmt, nil, value, bindingType)
}

func (a *Analyzer) recordLocalRefAliasBinding(stmt ast.Stmt, sym *Symbol, value ast.Expr, bindingType Type) {
	mode, ok := refAliasAccessMode(bindingType)
	if !ok {
		// Not a reference binding — but a struct/value local can still carry a laundered reference
		// into a parameter (`r = wrap(v)`). Track that so a later `r.refField` access is caught.
		if sym != nil {
			a.recordStructAliasCarrier(sym.Name, value)
		}
		return
	}
	roots := a.aliasBorrowRootsForValue(value)
	if len(roots) == 0 {
		a.releaseLocalAliasBinding(sym)
		return
	}
	if a.currentAliasAccesses == nil {
		a.currentAliasAccesses = map[string]aliasAccessState{}
	}
	a.releaseLocalAliasBinding(sym)
	conflict := false
	for _, root := range roots {
		existing := a.currentAliasAccesses[root]
		if aliasAccessConflicts(existing, mode) {
			conflict = true
		} else {
			// A live borrow on an OVERLAPPING root (whole-object vs one of its fields)
			// aliases the same storage even though the root strings differ.
			for otherRoot, state := range a.currentAliasAccesses {
				if otherRoot == root || !aliasRootsOverlap(root, otherRoot) {
					continue
				}
				if aliasAccessConflicts(state, mode) {
					conflict = true
					break
				}
			}
		}
		a.currentAliasAccesses[root] = aliasAccessStateWith(existing, mode)
	}
	if conflict {
		a.recordUnsafeAliasStmt(stmt)
	}
	if sym != nil {
		if a.currentAliasBindings == nil {
			a.currentAliasBindings = map[*Symbol]aliasAccessBinding{}
		}
		a.currentAliasBindings[sym] = aliasAccessBinding{Roots: roots, Mode: mode}
	}
}

// aliasBorrowRootsForValue returns every storage root a reference bound to `value` may alias: the
// single root of a literal `&…` borrow, or the per-aliased-argument roots of a reference-returning
// call (see callReturnAliasedRoots). Empty when `value` is not a tracked borrow.
func (a *Analyzer) aliasBorrowRootsForValue(value ast.Expr) []string {
	if call, ok := stripOptimizationParens(value).(*ast.CallExpr); ok && !aliasExprIsExplicitBorrow(value) {
		return a.callReturnAliasedRoots(call)
	}
	if root := a.aliasRootForExpr(value); root != "" {
		return []string{root}
	}
	return nil
}

func (a *Analyzer) recordLocalRefAliasAssignment(stmt *ast.AssignStmt, targetType Type) {
	if stmt == nil || stmt.Optional {
		return
	}
	ident, ok := stmt.Target.(*ast.Ident)
	if !ok || a.currentScope == nil {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return
	}
	bindingType := targetType
	if _, ok := bindingType.(*RefType); !ok {
		bindingType = sym.Type
	}
	if sym.Mutable && a.valueIsAliasBorrow(stmt.Value) {
		if ref, ok := bindingType.(*RefType); ok && !ref.Mutable {
			cloned := cloneRefType(ref)
			cloned.Mutable = true
			bindingType = cloned
		}
	}
	a.recordLocalRefAliasBinding(stmt, sym, stmt.Value, bindingType)
}

func (a *Analyzer) releaseLocalAliasBinding(sym *Symbol) {
	if sym == nil || a.currentAliasBindings == nil {
		return
	}
	binding, ok := a.currentAliasBindings[sym]
	if !ok {
		return
	}
	delete(a.currentAliasBindings, sym)
	if a.currentAliasAccesses == nil {
		return
	}
	for _, root := range binding.Roots {
		state := aliasAccessStateWithout(a.currentAliasAccesses[root], binding.Mode)
		if aliasAccessStateEmpty(state) {
			delete(a.currentAliasAccesses, root)
			continue
		}
		a.currentAliasAccesses[root] = state
	}
}

func (a *Analyzer) recordCallAlignedAliasArgs(call *ast.CallExpr, args []ast.Expr) {
	if call == nil {
		return
	}
	if a.callAlignedAliasArgs == nil {
		a.callAlignedAliasArgs = map[*ast.CallExpr][]ast.Expr{}
	}
	a.callAlignedAliasArgs[call] = args
}

// valueIsAliasBorrow reports whether binding a reference to `value` borrows tracked storage —
// either a literal `&…` borrow, or a call whose return provably aliases one or more of its
// arguments (so `r = get_ref(&x)` is a borrow of x, not an opaque value). Without this, a borrow
// laundered through a reference-returning call would lose its root and defeat the alias checker.
func (a *Analyzer) valueIsAliasBorrow(value ast.Expr) bool {
	if aliasExprIsExplicitBorrow(value) {
		return true
	}
	if call, ok := stripOptimizationParens(value).(*ast.CallExpr); ok {
		return len(a.callReturnAliasedRoots(call)) != 0
	}
	return false
}

// callReturnAliasedRoots returns the alias roots of every argument whose storage a call's
// returned reference may borrow, using the callee's already-computed return-isolation provenance
// (ReturnIsolation.AliasParamIndices). Argument expressions are taken from the call's
// param-aligned alias-arg list (receiver + reordered named args + implicits), so method and
// free-function calls are handled uniformly and the parameter index maps correctly. A return
// aliasing several parameters yields several roots; the caller treats the result as aliasing any
// of them. Empty when provenance is unknown or no aligned args were recorded (non-enforcing pass).
func (a *Analyzer) callReturnAliasedRoots(call *ast.CallExpr) []string {
	if call == nil {
		return nil
	}
	ft, ok := a.exprTypes[call.Func].(*FuncType)
	if !ok || ft == nil || !ft.ReturnIsolationKnown {
		return nil
	}
	indices := ft.ReturnIsolation.AliasParamIndices
	if len(indices) == 0 {
		return nil
	}
	aligned, ok := a.callAlignedAliasArgs[call]
	if !ok {
		return nil
	}
	roots := make([]string, 0, len(indices))
	seenRoot := map[string]bool{}
	for _, idx := range indices {
		if idx < 0 || idx >= len(aligned) {
			// Without a resolvable argument for every aliased parameter we cannot prove the
			// full borrow set, so report none rather than an incomplete (unsound) subset.
			return nil
		}
		root := a.aliasRootForExpr(aligned[idx])
		if root == "" {
			return nil
		}
		if !seenRoot[root] {
			seenRoot[root] = true
			roots = append(roots, root)
		}
	}
	return roots
}

func aliasExprIsExplicitBorrow(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.AddrOfExpr:
		return true
	case *ast.MoveExpr:
		return aliasExprIsExplicitBorrow(n.Operand)
	case *ast.CastExpr:
		return aliasExprIsExplicitBorrow(n.Operand)
	default:
		return false
	}
}

func (a *Analyzer) validateCallArgAliasAccess(call *ast.CallExpr, paramTypes []Type, args []ast.Expr) {
	if call == nil || !a.enforceUnsafePermissions {
		return
	}
	a.recordCallAlignedAliasArgs(call, args)
	seen := map[string]aliasAccessState{}
	limit := len(args)
	if len(paramTypes) < limit {
		limit = len(paramTypes)
	}
	for i := 0; i < limit; i++ {
		mode, ok := refAliasAccessMode(paramTypes[i])
		if !ok {
			continue
		}
		// An arg may resolve to several alias roots (a reference returned from a call that
		// aliases multiple params). Each is a storage the arg might touch, so any conflicting
		// with this mutable use is a real alias.
		roots := a.aliasRootsForExpr(args[i])
		conflict := false
		for _, root := range roots {
			// Exact-root live state, discounting the arg's own outstanding binding so
			// `&x` doesn't self-conflict with x's own borrow.
			if existingLive := a.liveAliasAccessStateForArg(root, args[i]); aliasAccessConflicts(existingLive, mode) {
				conflict = true
				break
			}
			// Overlapping (whole-object vs field) live borrows on a DIFFERENT root.
			if a.overlappingAliasConflict(root, mode, args[i]) {
				conflict = true
				break
			}
			// Earlier args of THIS call, exact or overlapping (e.g. update(node, node.value)).
			if aliasMapHasOverlapConflict(seen, root, mode) {
				conflict = true
				break
			}
		}
		if conflict {
			a.recordUnsafeAliasExpr(call)
			return
		}
		// Roots of one arg are alternatives (the ref is one of them), not mutual conflicts,
		// so add them to `seen` only after checking this arg against earlier args.
		for _, root := range roots {
			seen[root] = aliasAccessStateWith(seen[root], mode)
		}
	}
}

// overlappingAliasConflict reports a live borrow on a DIFFERENT root that overlaps `root`
// (whole-object vs one of its fields), discounting `arg`'s own outstanding binding on that root.
func (a *Analyzer) overlappingAliasConflict(root string, mode aliasAccessMode, arg ast.Expr) bool {
	for other := range a.currentAliasAccesses {
		if other == root || !aliasRootsOverlap(root, other) {
			continue
		}
		if aliasAccessConflicts(a.liveAliasAccessStateForArg(other, arg), mode) {
			return true
		}
	}
	return false
}

func (a *Analyzer) liveAliasAccessStateForArg(root string, arg ast.Expr) aliasAccessState {
	if a.currentAliasAccesses == nil {
		return aliasAccessState{}
	}
	state := a.currentAliasAccesses[root]
	if sym := a.aliasBindingSymbolForExpr(arg); sym != nil {
		if binding, ok := a.currentAliasBindings[sym]; ok {
			for _, bound := range binding.Roots {
				if bound == root {
					state = aliasAccessStateWithout(state, binding.Mode)
					break
				}
			}
		}
	}
	return state
}

// aliasRootsForExpr returns every alias root an expression may name when passed as a call argument:
// the roots a ref-typed ident is bound to (one, or several for a multi-param-alias return), the
// roots of an inline reference-returning call, or otherwise the single structural root.
func (a *Analyzer) aliasRootsForExpr(expr ast.Expr) []string {
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.Ident:
		if sym := a.aliasBindingSymbolForExpr(n); sym != nil {
			if binding, ok := a.currentAliasBindings[sym]; ok && len(binding.Roots) != 0 {
				return append([]string(nil), binding.Roots...)
			}
		}
	case *ast.CallExpr:
		if roots := a.callReturnAliasedRoots(n); len(roots) != 0 {
			return roots
		}
	case *ast.FieldExpr:
		// A reference-typed field of a struct local that carries a laundered borrow aliases the
		// borrowed parameter(s), e.g. `r.p` where `r = wrap(v)`. Union the carried roots with the
		// structural root so same-field and param-direct uses both conflict.
		if _, isRef := a.exprTypes[n].(*RefType); isRef {
			if ident, ok := stripOptimizationParens(n.Object).(*ast.Ident); ok {
				if fields := a.currentAliasCarrierFieldOverrides[ident.Name]; fields != nil {
					if roots, ok := fields[n.Field]; ok {
						out := append([]string(nil), roots...)
						if root := a.aliasRootForExpr(expr); root != "" {
							out = append(out, root)
						}
						return out
					}
				}
				if carried, ok := a.currentAliasCarriers[ident.Name]; ok && len(carried) != 0 {
					roots := append([]string(nil), carried...)
					if root := a.aliasRootForExpr(expr); root != "" {
						roots = append(roots, root)
					}
					return roots
				}
			}
		}
	}
	if root := a.aliasRootForExpr(expr); root != "" {
		return []string{root}
	}
	return nil
}

func (a *Analyzer) aliasBindingSymbolForExpr(expr ast.Expr) *Symbol {
	if a.currentScope == nil {
		return nil
	}
	ident, ok := stripOptimizationParens(expr).(*ast.Ident)
	if !ok {
		return nil
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return nil
	}
	return sym
}

func (a *Analyzer) aliasRootForExpr(expr ast.Expr) string {
	return a.aliasRootForExprSeen(expr, map[*Symbol]bool{})
}

func (a *Analyzer) aliasRootForExprSeen(expr ast.Expr, seen map[*Symbol]bool) string {
	if expr == nil {
		return ""
	}
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.AddrOfExpr:
		return a.aliasRootForExprSeen(n.Operand, seen)
	case *ast.MoveExpr:
		return a.aliasRootForExprSeen(n.Operand, seen)
	case *ast.CastExpr:
		return a.aliasRootForExprSeen(n.Operand, seen)
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok && sym != nil {
				if seen[sym] {
					return n.Name
				}
				if binding, ok := a.currentAliasBindings[sym]; ok && len(binding.Roots) != 0 {
					// Representative single root (callers needing all roots use aliasRootsForExpr).
					return binding.Roots[0]
				}
				if value, ok := a.currentValueBindings[sym]; ok && value != nil {
					seen[sym] = true
					if root := a.aliasRootForExprSeen(value, seen); root != "" {
						return root
					}
				}
			}
		}
		return n.Name
	case *ast.FieldExpr:
		root := a.aliasRootForExprSeen(n.Object, seen)
		if root == "" {
			return ""
		}
		return root + "." + n.Field
	case *ast.IndexExpr:
		return a.aliasRootForExprSeen(n.Object, seen)
	case *ast.SliceExpr:
		return a.aliasRootForExprSeen(n.Object, seen)
	case *ast.CallExpr:
		// A call whose return borrows its arguments (proven provenance) roots to that storage, so
		// an inline `f(get_ref(&x), &x)` is caught like the two-step form. Representative first root
		// for the single-string API; aliasRootsForExpr returns the full set for conflict checks.
		if roots := a.callReturnAliasedRoots(n); len(roots) != 0 {
			return roots[0]
		}
		return ""
	default:
		return ""
	}
}
