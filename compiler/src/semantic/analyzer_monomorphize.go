package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// Monomorphized re-analysis of a generic function body.
//
// The analyzer types a generic body ONCE, with its type parameters opaque. That is fine
// for rules that do not depend on the concrete type, and wrong for the ones that do. The
// motivating case is `+`/`-` on a reference: scalarRefValueContextOperandType makes
// `i64& + n` VALUE arithmetic (add to the referent) while leaving `u8& + n` as genuine
// byte-pointer stepping — a decision that cannot be made while T is still a type
// parameter. A body analyzed once as `T& + n` therefore records POINTER arithmetic and
// keeps that meaning at every instantiation, so `T&` with T := i64 meant something
// different from the identical code spelled `i64&`.
//
// Specialization itself is a backend step (llvm_specialize.go), so the backend asks for
// the instantiated types at exactly the points where instantiations occur, rather than
// requiring a separate discovery pass over the program.

// SpecializedExprTypes is the compatibility projection for consumers which need
// only changed template-keyed types. It analyzes private syntax and leaves the
// template untouched. Backend lowering uses SpecializeFunction instead: a type
// map alone cannot represent expressions rewritten by concrete specialization.
// Isolation failures return nil here; lowering uses the error-returning API.
func (r *Result) SpecializedExprTypes(fn *ast.FuncDecl, typeArgs []Type) map[ast.Expr]Type {
	if r == nil || r.CanonicalCodegenResult().analyzer == nil || fn == nil || len(typeArgs) == 0 {
		return nil
	}
	body, err := r.SpecializeFunction(fn, typeArgs)
	if err != nil {
		return nil
	} // Compatibility API; backend uses the error-returning product API.
	overlay := map[ast.Expr]Type{}
	for private, original := range body.originalExprs {
		typ, ok := body.Result.LookupExprTypes(private)
		if !ok {
			continue
		}
		previous, existed := r.LookupExprTypes(original)
		if !existed || (previous != typ && !SameType(previous, typ)) {
			overlay[original] = typ
		}
	}
	if len(overlay) == 0 {
		return nil
	}
	return overlay
}

func collectSpecializedFunctionExprs(fn *ast.FuncDecl, exprs map[ast.Expr]struct{}) {
	fillMayAdoptWalk(reflect.ValueOf(fn), func(node any) {
		if expr, ok := node.(ast.Expr); ok {
			exprs[expr] = struct{}{}
		}
	})
}

// funcTypeParamBindings pairs a declaration's generic parameters with concrete arguments,
// in declaration order.
//
// VALUE (const) parameters are bound here too. The old rule skipped them — "bound by their
// own scopes, not by type substitution" — but substitution is exactly what consumes them:
// substituteTypeWithDepth's ArrayType branch resolves `n.ConstParam` by looking it up in
// THIS map and expecting a *ConstValueType. Skipping them left the re-analysed body
// holding `Box[i64, N]` with N unresolved, so the backend lowered an opaque instance and
// emitted `GEP into unsized type!` — invalid IR for a program the analyzer accepted:
//
//	struct Box[T, N: usize]:
//	    items: mutable T[N]
//	def box_len[T, N: usize](b: mutable Box[T, N]&) -> usize: ...
//
// That is what made elisacore_std's own InlineVec[T, N] uncompilable.
//
// The cursor now advances for EVERY generic parameter, not just the ones that get bound.
// typeArgs is built over the whole parameter list (orderedGenericTypeArgs), so skipping a
// parameter without advancing misaligned every argument after it: `[N: usize, T]` bound T
// to the CONST argument. `[T, N]` only worked because the skipped parameter was last.
func funcTypeParamBindings(fn *ast.FuncDecl, typeArgs []Type) map[string]Type {
	if fn == nil || len(typeArgs) == 0 {
		return nil
	}
	bindings := map[string]Type{}
	next := 0
	for _, param := range fn.GenericParams {
		if next >= len(typeArgs) {
			break
		}
		arg := typeArgs[next]
		next++
		switch param.Kind {
		case ast.GenericParamType, ast.GenericParamValue:
			if arg != nil {
				bindings[param.Name] = arg
			}
		}
	}
	if len(bindings) == 0 {
		for i, name := range fn.TypeParams {
			if i < len(typeArgs) && typeArgs[i] != nil {
				bindings[name] = typeArgs[i]
			}
		}
	}
	return bindings
}

// preserveFunctionExprTypes scopes speculative annotations to one function body.
func (a *Analyzer) preserveFunctionExprTypes(fn *ast.FuncDecl) func() {
	exprs := map[ast.Expr]struct{}{}
	collectSpecializedFunctionExprs(fn, exprs)
	saved := make(map[ast.Expr]Type, len(exprs))
	for expr := range exprs {
		if typ, ok := a.exprTypes[expr]; ok {
			saved[expr] = typ
		}
	}
	return func() {
		collectSpecializedFunctionExprs(fn, exprs)
		for expr := range exprs {
			if typ, ok := saved[expr]; ok {
				a.exprTypes[expr] = typ
			} else {
				delete(a.exprTypes, expr)
			}
		}
	}
}
