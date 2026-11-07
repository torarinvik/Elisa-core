package semantic

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestStaticCollectionAppend(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_collection_append.elisa", `static def score() -> i64:
    values: mutable darray[i64] = []
    values += 4
    values += 9
    return values[0] + values[1]

def keep() -> void:
    static assert score() == 13
`)
}

func TestCollectionAppendRestoresShadowedBinding(t *testing.T) {
	makeAppend := func() *ast.AugAssignStmt {
		return &ast.AugAssignStmt{
			Op:     lexer.TOKEN_PLUSEQ,
			Target: &ast.Ident{Name: "values"},
			Value:  &ast.IntLit{Value: "1"},
		}
	}
	shadowed, outer := makeAppend(), makeAppend()
	integer := &ast.BuiltinTypeExpr{Name: "i64"}
	fn := &ast.FuncDecl{
		Params: []ast.ParamDecl{{Name: "values", Type: &ast.BuiltinTypeExpr{
			Name: "darray", TypeArgs: []ast.TypeExpr{integer},
		}}},
		Body: []ast.Stmt{
			&ast.IfStmt{Cond: &ast.BoolLit{Value: true}, Then: []ast.Stmt{
				&ast.VarDeclStmt{Name: "values", Type: integer, Value: &ast.IntLit{Value: "0"}},
				shadowed,
			}},
			outer,
		},
	}
	a := newAnalyzerWithScope(NewScope(nil))
	a.prepareCollectionAppends(fn)
	if shadowed.CollectionAppend != nil || outer.CollectionAppend == nil {
		t.Fatal("nested numeric update shadowed the outer darray append")
	}
}
