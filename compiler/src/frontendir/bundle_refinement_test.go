package frontendir

import (
	"testing"

	"elisacore/src/ast"
)

// A refinement type expression (`u32 is InRange[0, 127]`) must survive the gob round-trip used by the
// IR bundle. Regression: RefinementTypeExpr was unregistered, so `-emit ir` on any refinement-typed
// program failed with "gob: type not registered for interface: ast.RefinementTypeExpr".
func TestBundleRoundTripsRefinementType(t *testing.T) {
	refined := &ast.RefinementTypeExpr{
		Base: &ast.NamedType{Name: "u32"},
		Preds: []ast.RefinementPredExpr{{
			Name: "InRange",
			Args: []ast.Expr{&ast.IntLit{Value: "0"}, &ast.IntLit{Value: "127"}},
		}},
	}
	bundle := &Bundle{
		SourceFilename: "refined.elisa",
		File: &ast.File{
			Filename: "refined.elisa",
			Decls: []ast.Decl{&ast.ConstDecl{
				Name: "Limit",
				Type: refined,
			}},
		},
	}
	data, err := Encode(bundle)
	if err != nil {
		t.Fatalf("encoding a refinement-typed bundle must not fail, got: %v", err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("decoding a refinement-typed bundle must not fail, got: %v", err)
	}
	cd, ok := got.File.Decls[0].(*ast.ConstDecl)
	if !ok {
		t.Fatalf("expected a ConstDecl back, got %T", got.File.Decls[0])
	}
	rt, ok := cd.Type.(*ast.RefinementTypeExpr)
	if !ok {
		t.Fatalf("expected the refinement type to survive, got %T", cd.Type)
	}
	if len(rt.Preds) != 1 || rt.Preds[0].Name != "InRange" || len(rt.Preds[0].Args) != 2 {
		t.Fatalf("refinement predicate did not round-trip: %+v", rt.Preds)
	}
}
