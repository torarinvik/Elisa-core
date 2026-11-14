package semantic

import (
	"elisacore/src/ast"
	"maps"
	"strings"
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
	if len(a.currentAliasAccesses) >= 16 {
		return maps.Clone(a.currentAliasAccesses)
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
	if len(a.currentAliasBindings) >= 16 {
		return maps.Clone(a.currentAliasBindings)
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
	if expr == nil {
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

// viewAliasAccessMode classifies a by-value view parameter (sview, cstr, view[T]) as a borrow of the
// storage its argument views: read-only, or writable for a `mutable view[T]`.
func viewAliasAccessMode(t Type) (aliasAccessMode, bool) {
	switch tt := t.(type) {
	case *SViewType, *CStrType:
		return aliasAccessRead, true
	case *ViewType:
		if tt != nil && tt.Mutable {
			return aliasAccessWrite, true
		}
		return aliasAccessRead, true
	}
	return aliasAccessRead, false
}

// viewArgAliasRoots names the container storage a view argument's bytes ARE: `xs.as_sview()`,
// `xs.as_cstr()`, `xs.view()`, `xs[a:b]`, or a local bound to one of those. A view merely read out
// of a container (an sview element or field, a helper's result) points at bytes the container
// does not own, so a callee growing the container cannot dangle it; it has no root here. This is
// the same syntactic set stage1's check_call_argument_exclusivity treats as `&xs[..]`.
func (a *Analyzer) viewArgAliasRoots(arg ast.Expr) []string {
	return a.viewArgAliasRootsDepth(arg, 0)
}

func (a *Analyzer) viewArgAliasRootsDepth(arg ast.Expr, depth int) []string {
	if arg == nil || depth > 8 {
		return nil
	}
	switch n := stripOptimizationParens(arg).(type) {
	case *ast.SliceExpr:
		if !a.viewSourceOwnsBuffer(n.Object) {
			return nil
		}
		if root := a.aliasRootForExpr(n.Object); root != "" {
			return []string{root}
		}
	case *ast.CallExpr:
		field, ok := n.Func.(*ast.FieldExpr)
		if !ok || field == nil || field.Object == nil || len(n.Args) != 0 || !a.viewSourceOwnsBuffer(field.Object) {
			return nil
		}
		switch field.Field {
		case "as_sview", "as_cstr", "view":
			if root := a.aliasRootForExpr(field.Object); root != "" {
				return []string{root}
			}
		}
	case *ast.Ident:
		if a.currentScope == nil {
			return nil
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil || sym.Kind != SymbolLocal {
			return nil
		}
		if value, bound := a.currentValueBindings[sym]; bound && value != nil {
			return a.viewArgAliasRootsDepth(value, depth+1)
		}
	}
	return nil
}

// viewSourceOwnsBuffer: the viewed expression is a container whose own buffer the view points
// into (a darray, a fixed array, a dstr), not another view (slicing an sview views ITS bytes).
func (a *Analyzer) viewSourceOwnsBuffer(source ast.Expr) bool {
	switch t := StripAggregateStateType(stripRefForBounds(a.exprType(source))).(type) {
	case *DArrayType, *ArrayType:
		return true
	case *BuiltinType:
		return t != nil && t.Name == "dstr"
	case *StructType:
		return t != nil && t.Name == "dstr"
	}
	return false
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
	for {
		left, leftTail, leftMore := strings.Cut(a, ".")
		right, rightTail, rightMore := strings.Cut(b, ".")
		if left == "[*]" || right == "[*]" {
			return true
		}
		if left != right {
			return false
		}
		if !leftMore || !rightMore {
			return true
		}
		a, b = leftTail, rightTail
	}
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
