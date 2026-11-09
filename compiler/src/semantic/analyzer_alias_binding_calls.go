package semantic

import (
	"elisacore/src/ast"
)

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
	ft, ok := a.exprType(call.Func).(*FuncType)
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
		var roots []string
		if ok {
			// An arg may resolve to several alias roots (a reference returned from a call that
			// aliases multiple params). Each is a storage the arg might touch, so any conflicting
			// with this mutable use is a real alias.
			roots = a.aliasRootsForExpr(args[i])
		} else if viewMode, isView := viewAliasAccessMode(paramTypes[i]); isView {
			// A view argument (`buf.as_sview()`, `buf[0:n]`, a bound view of buf) borrows the
			// storage it views exactly like `&buf[0]` does: `use(&buf, buf.as_sview())` lets the
			// callee grow buf through its mutable reference and then read the dangling view.
			roots = a.viewArgAliasRoots(args[i])
			mode = viewMode
			if len(roots) == 0 {
				continue
			}
		} else {
			continue
		}
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
		if _, isRef := a.exprType(n).(*RefType); isRef {
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
