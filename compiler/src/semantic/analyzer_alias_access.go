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
	Root string
	Mode aliasAccessMode
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
		return
	}
	if !a.valueIsAliasBorrow(value) {
		a.releaseLocalAliasBinding(sym)
		return
	}
	root := a.aliasRootForExpr(value)
	if root == "" {
		a.releaseLocalAliasBinding(sym)
		return
	}
	if a.currentAliasAccesses == nil {
		a.currentAliasAccesses = map[string]aliasAccessState{}
	}
	a.releaseLocalAliasBinding(sym)
	existing := a.currentAliasAccesses[root]
	conflict := aliasAccessConflicts(existing, mode)
	if !conflict {
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
	if conflict {
		a.recordUnsafeAliasStmt(stmt)
	}
	a.currentAliasAccesses[root] = aliasAccessStateWith(existing, mode)
	if sym != nil {
		if a.currentAliasBindings == nil {
			a.currentAliasBindings = map[*Symbol]aliasAccessBinding{}
		}
		a.currentAliasBindings[sym] = aliasAccessBinding{Root: root, Mode: mode}
	}
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
	state := aliasAccessStateWithout(a.currentAliasAccesses[binding.Root], binding.Mode)
	if aliasAccessStateEmpty(state) {
		delete(a.currentAliasAccesses, binding.Root)
		return
	}
	a.currentAliasAccesses[binding.Root] = state
}

// valueIsAliasBorrow reports whether binding a reference to `value` borrows tracked storage —
// either a literal `&…` borrow, or a call whose return provably aliases one of its arguments
// (so `r = get_ref(&x)` is a borrow of x, not an opaque value). Without this, a borrow laundered
// through a reference-returning call would lose its root and defeat the call-site alias checker.
func (a *Analyzer) valueIsAliasBorrow(value ast.Expr) bool {
	if aliasExprIsExplicitBorrow(value) {
		return true
	}
	if call, ok := stripOptimizationParens(value).(*ast.CallExpr); ok {
		return a.callReturnAliasedArg(call) != nil
	}
	return false
}

// callReturnAliasedArg returns the single argument expression whose storage a call's returned
// reference borrows, using the callee's already-computed return-isolation provenance. It is
// deliberately conservative: it fires only for a simple positional free-function call (Ident
// callee, no named args) whose return provably aliases EXACTLY ONE parameter, so the parameter
// index maps 1:1 to call.Args. Method calls (receiver shifts the index) and multi-param alias
// returns return nil and fall back to the prior behavior — a documented soundness follow-up.
func (a *Analyzer) callReturnAliasedArg(call *ast.CallExpr) ast.Expr {
	if call == nil || call.HasArgForward {
		return nil
	}
	for _, name := range call.ArgNames {
		if name != "" {
			return nil
		}
	}
	if _, ok := call.Func.(*ast.Ident); !ok {
		return nil
	}
	ft, ok := a.exprTypes[call.Func].(*FuncType)
	if !ok || ft == nil || !ft.ReturnIsolationKnown {
		return nil
	}
	indices := ft.ReturnIsolation.AliasParamIndices
	if len(indices) != 1 {
		return nil
	}
	idx := indices[0]
	if idx < 0 || idx >= len(call.Args) {
		return nil
	}
	return call.Args[idx]
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
		root := a.aliasRootForExpr(args[i])
		if root == "" {
			continue
		}
		// Exact-root live state, discounting the arg's own outstanding binding so
		// `&x` doesn't self-conflict with x's own borrow.
		if existingLive := a.liveAliasAccessStateForArg(root, args[i]); aliasAccessConflicts(existingLive, mode) {
			a.recordUnsafeAliasExpr(call)
			return
		}
		// Overlapping (whole-object vs field) live borrows on a DIFFERENT root.
		for other, state := range a.currentAliasAccesses {
			if other == root || !aliasRootsOverlap(root, other) {
				continue
			}
			if aliasAccessConflicts(state, mode) {
				a.recordUnsafeAliasExpr(call)
				return
			}
		}
		// Earlier args of THIS call, exact or overlapping (e.g. update(node, node.value)).
		if aliasMapHasOverlapConflict(seen, root, mode) {
			a.recordUnsafeAliasExpr(call)
			return
		}
		seen[root] = aliasAccessStateWith(seen[root], mode)
	}
}

func (a *Analyzer) liveAliasAccessStateForArg(root string, arg ast.Expr) aliasAccessState {
	if a.currentAliasAccesses == nil {
		return aliasAccessState{}
	}
	state := a.currentAliasAccesses[root]
	if sym := a.aliasBindingSymbolForExpr(arg); sym != nil {
		if binding, ok := a.currentAliasBindings[sym]; ok && binding.Root == root {
			state = aliasAccessStateWithout(state, binding.Mode)
		}
	}
	return state
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
				if binding, ok := a.currentAliasBindings[sym]; ok && binding.Root != "" {
					return binding.Root
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
		// A call whose return borrows exactly one argument (proven provenance) roots to that
		// argument's storage, so an inline `f(get_ref(&x), &x)` is caught like the two-step form.
		if arg := a.callReturnAliasedArg(n); arg != nil {
			return a.aliasRootForExprSeen(arg, seen)
		}
		return ""
	default:
		return ""
	}
}
