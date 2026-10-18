package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestStoreFlowGenericDarrayPushRetainsNumericAddressExpressions(t *testing.T) {
	for _, numeric := range []string{"uintptr", "usize", "isize", "u8", "u16", "u32", "u64", "i8", "i16", "i32", "i64", "f32", "f64"} {
		t.Run(numeric, func(t *testing.T) {
			source := &ast.Ident{Name: "source"}
			cast := &ast.CastExpr{Operand: source, Target: &ast.NamedType{Name: numeric}, Origin: ast.CastExprOriginExplicitCast}
			for _, expr := range []ast.Expr{cast, &ast.BinaryExpr{Left: cast, Right: &ast.IntLit{Value: "0"}}, &ast.CallExpr{Func: &ast.Ident{Name: "encode"}, Args: []ast.Expr{source}}} {
				receiver := &ast.Ident{Name: "target"}
				a := &Analyzer{storeFlowDarrays: map[string]bool{"target": true}, exprTypes: map[ast.Expr]Type{
					receiver: &DArrayType{Elem: &TypeParamType{Name: "E"}}, source: &RefType{Elem: &BuiltinType{Name: "u8"}}, expr: &BuiltinType{Name: numeric}, cast: &BuiltinType{Name: numeric},
				}}
				call := &ast.CallExpr{Func: &ast.FieldExpr{Object: receiver, Field: "push"}, Args: []ast.Expr{expr}}
				c := &storeFlowCtx{written: map[string]bool{"target": true}, scalars: map[string]bool{}}
				ret, handled := a.applyCallStoreFlow(call, c, map[*ast.CallExpr][]string{})
				origin := "source"
				found := false
				for _, edge := range c.edges {
					if edge == [2]string{origin, "target"} {
						found = true
					}
				}
				if !handled || !found || len(ret) < 2 {
					t.Fatalf("numeric address expression %T lost conservative provenance: handled=%v edges=%v result=%v", expr, handled, c.edges, ret)
				}
			}
		})
	}
}
