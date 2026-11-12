package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/regionframesyntax"
)

// exprResultIsRegionAllocated reports whether the RESULT of an expression is a fresh region
// allocation (a `new[auto]` modulo parentheses) or the result of an already-region-polymorphic call.
// It deliberately does NOT descend into arguments: `f(new[auto] X())` yields f's result, not the
// new[auto], so it is not region-allocated unless f itself is region-polymorphic.
func (a *Analyzer) exprResultIsRegionAllocated(value ast.Expr) bool {
	value = unwrapParenForRegionPoly(value)
	switch e := value.(type) {
	case *ast.AllocExpr:
		if e == nil {
			return false
		}
		if e.AutoRegion {
			return true
		}
		// Bare `new` defaults to new[auto] (region-allocated) unless it is a
		// packed-enum constructor, where only region-backed (recursive-plain)
		// enums take the inferred-region path — mirroring the bare-constructor
		// rule (regionBackedEnumConstructor) and analyzeAllocExprWithExpected.
		if e.Owner == nil {
			if enumType, _, ok := a.packedAllocConstructorInfo(e.Value); ok && enumType != nil && enumType.Packed {
				return enumType.StoreBackedPlain
			}
			return true
		}
		return false
	case *ast.CallExpr:
		if ft := a.regionPolyCalleeFuncType(e); ft != nil && a.regionPolyReturnsCallerArena[ft] {
			return true
		}
		// A bare constructor of a region-backed (recursive-plain) enum is an implicit new[auto]
		// allocation (docs/76 Slice 0b), so a function returning one is region-polymorphic.
		if et, ok := a.regionBackedEnumConstructor(e); ok && et != nil {
			return true
		}
	case *ast.StructLitExpr:
		// Pre-analysis, `Name(args)` parses as a StructLitExpr whether Name is a struct
		// or a function — so a constructor-style CALL of a region-polymorphic function
		// (`return Manager(Users_New(), -1)` / `return Users_New()`) lands here, not in
		// the CallExpr case. Resolve the name: a region-poly function makes the value
		// region-allocated.
		if sym, _, ok := a.lookupVisibleGlobal(e.Name); ok && sym != nil {
			if ft, isFn := sym.Type.(*FuncType); isFn && ft != nil && a.regionPolyReturnsCallerArena[ft] {
				return true
			}
		}
		// Fallback for a constructor call shadowed by a same-named struct: the value
		// scope resolved `Name` to the struct (above), hiding the region-poly
		// `def Name(...)->Name` constructor. Resolve it from the classification index.
		if a.regionPolyConstructorIsRegionAllocated(e.Name) {
			return true
		}
		// A struct literal wrapping region-allocated values (the parse-result pattern:
		// `return Result{ast: ..., decls: decls}`) carries its fields' region — returning
		// it returns region-allocated data, so the function must thread the caller region
		// instead of freeing a local auto-region under the returned handles.
		for _, arg := range e.Args {
			if arg == nil {
				continue
			}
			if a.exprResultIsRegionAllocated(arg) {
				return true
			}
			// Region-less container literals/comprehensions as constructor args allocate
			// into the inferred region (the regiony() rule, applied transitively here).
			if lit, isLit := unwrapParenForRegionPoly(arg).(*ast.ListLitExpr); isLit && lit != nil && lit.Owner == nil {
				return true
			}
			if comp, isComp := unwrapParenForRegionPoly(arg).(*ast.ListComprehensionExpr); isComp && comp != nil && comp.Owner == nil {
				return true
			}
		}
	}
	return false
}

// regionPolyConstructorIsRegionAllocated reports whether a `Name(args)` call resolves
// to a region-polymorphic constructor (`def Name(...)->Name`) via the classification
// index. This is the fallback for the name-collision case: when a struct named `Name`
// shadows the same-named constructor in the value scope, lookupVisibleGlobal returns
// the struct and the constructor's region-poly classification is otherwise invisible.
// Only a constructor (return type names the same `Name`) qualifies, so a region-poly
// non-constructor written as `Name(args)` is unaffected (it has no shadowing struct and
// is already resolved by the normal lookup).
func (a *Analyzer) regionPolyConstructorIsRegionAllocated(name string) bool {
	if a == nil || a.regionPolyCandidateFnTypes == nil || name == "" {
		return false
	}
	simple := simpleTypeNameSegment(name)
	for _, ft := range a.regionPolyCandidateFnTypes[simple] {
		if ft != nil && a.regionPolyReturnsCallerArena[ft] && funcTypeReturnTypeNamed(ft, simple) {
			return true
		}
	}
	return false
}

// funcTypeReturnTypeNamed reports whether the function's return type is a struct/enum
// named `name` — the constructor shape (`def Name(...)->Name`). Names are compared by
// simple (unqualified) segment, since a return StructType inside a module carries a
// namespace-qualified Name (`Parser.Parser`) while the call site names it `Parser`.
// AggregateStateType wrappers are unwrapped so a mutable/region-qualified return matches.
func funcTypeReturnTypeNamed(ft *FuncType, name string) bool {
	if ft == nil {
		return false
	}
	want := simpleTypeNameSegment(name)
	t := ft.Return
	for {
		switch rt := t.(type) {
		case *AggregateStateType:
			if rt == nil {
				return false
			}
			t = rt.Base
		case *StructType:
			return rt != nil && simpleTypeNameSegment(rt.Name) == want
		case *EnumType:
			return rt != nil && simpleTypeNameSegment(rt.Name) == want
		default:
			return false
		}
	}
}

// simpleTypeNameSegment returns the last segment of a possibly namespace-qualified
// name, handling both `::` (source) and `.` (internal) separators.
func simpleTypeNameSegment(name string) string { return regionframesyntax.SimpleTypeNameSegment(name) }

// regionBackedEnumConstructor reports the enum if a call is a bare constructor of a region-backed
// (recursive-plain) enum — `Expr.Add(...)` where Expr is a promoted plain enum. Explicit `packed
// enum`s (e.g. an explicit-store JSON DOM) are NOT region-backed-by-default and are excluded, so
// their bare constructors keep the explicit-store path.
func (a *Analyzer) regionBackedEnumConstructor(call *ast.CallExpr) (*EnumType, bool) {
	if call == nil {
		return nil, false
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok {
		return nil, false
	}
	enumType, variant, ok := a.enumConstructorInfoFromFieldExpr(fieldExpr)
	if !ok || enumType == nil || variant == nil || !enumType.Packed || !enumType.RecursivePlain {
		return nil, false
	}
	return enumType, true
}

// callIsInlineEnumConstructor reports whether a call is a variant constructor of an INLINE
// (non-packed) enum — `Box.Full(...)`. Packed enums are excluded: their constructors are handle
// allocations into a store and are classified by regionBackedEnumConstructor instead. Used by the
// region-polymorphism classifier, where an inline enum's payload embeds its arguments and so
// carries their region the way a struct literal's fields do.
func (a *Analyzer) callIsInlineEnumConstructor(call *ast.CallExpr) bool {
	if call == nil {
		return false
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok {
		return false
	}
	enumType, variant, ok := a.enumConstructorInfoFromFieldExpr(fieldExpr)
	if !ok || enumType == nil || variant == nil {
		return false
	}
	return !enumType.Packed && !enumType.Root().Packed
}

func unwrapParenForRegionPoly(value ast.Expr) ast.Expr {
	return regionframesyntax.UnwrapRegionValue(value)
}
