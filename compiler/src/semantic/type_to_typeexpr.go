package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// typeToTypeExpr reconstructs a surface ast.TypeExpr from a resolved semantic Type, for synthesizing
// typed declarations during analyzer-side desugaring (e.g. the `[... by par]` map lowering, which
// must declare `out: darray[U]` where U is the analyzer-computed element type). It is intentionally
// conservative: it covers the types that realistically appear as data-parallel element types and
// returns nil for anything it cannot faithfully reconstruct (generic instantiations, refs, tuples,
// views, stores, ...). A nil result lets the caller emit a clear "not supported" diagnostic rather
// than guess. Even when it returns a TypeExpr, the synthesized code is re-analyzed normally, so any
// inaccuracy surfaces as an ordinary type error — never a silently-wrong lowering.
func typeToTypeExpr(t Type, pos lexer.Pos) ast.TypeExpr {
	switch n := t.(type) {
	case *BuiltinType:
		return &ast.NamedType{Position: pos, Name: n.Name}
	case *BitIntType:
		return &ast.NamedType{Position: pos, Name: n.String()}
	case *StructType:
		// Only non-generic structs reconstruct faithfully by bare name; a generic instantiation
		// would lose its type arguments (NamedType carries no args), so defer to nil.
		if len(n.TypeParams) != 0 {
			return nil
		}
		return &ast.NamedType{Position: pos, Name: n.Name}
	case *EnumType:
		return &ast.NamedType{Position: pos, Name: n.Name}
	case *DStrType:
		if n.SurfaceName != "" {
			return &ast.NamedType{Position: pos, Name: n.SurfaceName}
		}
		return nil
	case *DArrayType:
		elem := typeToTypeExpr(n.Elem, pos)
		if elem == nil {
			return nil
		}
		return &ast.BuiltinTypeExpr{Position: pos, Name: "darray", TypeArgs: []ast.TypeExpr{elem}}
	case *OptionalType:
		inner := typeToTypeExpr(n.Value, pos)
		if inner == nil {
			return nil
		}
		return &ast.OptionalTypeExpr{Position: pos, Value: inner}
	default:
		return nil
	}
}
