//go:build cgo

package semantic

import (
	"elisacore/src/ast"
)

// The `__fstr` builtin — the desugar target of f-string literals (parser_fstring.go):
//
//	f"a{x}b"  ->  __fstr("a", x, "b")
//
// Each argument is one part of the interpolation, either a literal chunk (StringLit) or an embedded
// expression, restricted in Stage A to the string-like types (sview / dstr / cstr / string literal).
// The call's value is a freshly built `dstr`; building it allocates, so the call carries
// Memory.Allocate + Abort.Panic permission refs (recorded via the FuncType stamped on the callee
// ident, which the permissions collector already consumes for every CallExpr).
//
// `__fstr` is reserved: it resolves here BEFORE ordinary name resolution, so a user function of the
// same name can never intercept an f-string lowering.
func (a *Analyzer) analyzeBuiltinFStrCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident == nil || ident.Name != "__fstr" {
		return nil, false
	}
	for _, arg := range expr.Args {
		argType := a.analyzeExpr(arg)
		if !a.fstrPartTypeOK(arg, argType) {
			a.errorf(arg.Pos(), "f-string interpolation expects a string-like value (sview/dstr/cstr), got %s; convert explicitly", argType)
		}
	}
	dstrType := &DArrayType{Elem: a.namedTypes["u8"], Shape: &WildcardShape{}, SurfaceName: "dstr"}
	fnType := &FuncType{
		Name:   "__fstr",
		Return: dstrType,
		PermissionRefs: []ast.PermissionRef{
			{Position: expr.Position, Name: "Memory", Member: "Allocate"},
			{Position: expr.Position, Name: "Abort", Member: "Panic"},
		},
		Permissions: []string{"Memory", "Abort"},
	}
	a.exprTypes[expr.Func] = fnType
	a.exprTypes[expr] = dstrType
	return dstrType, true
}

// fstrPartTypeOK reports whether one f-string part is string-like: a string literal, an sview, a
// dstr (any region), or a cstr. Everything else must be converted explicitly (Stage A).
func (a *Analyzer) fstrPartTypeOK(arg ast.Expr, t Type) bool {
	if _, isLit := arg.(*ast.StringLit); isLit {
		return true
	}
	for {
		ref, ok := t.(*RefType)
		if !ok {
			break
		}
		t = ref.Elem
	}
	if isDStrType(t) {
		return true
	}
	if t == a.namedTypes["sview"] {
		return true
	}
	if t == a.namedTypes["cstr"] {
		return true
	}
	// Named-type identity can differ across instantiations; fall back to the surface name.
	type surfaceNamed interface{ String() string }
	if sn, ok := t.(surfaceNamed); ok {
		switch sn.String() {
		case "sview", "cstr":
			return true
		}
	}
	return false
}
