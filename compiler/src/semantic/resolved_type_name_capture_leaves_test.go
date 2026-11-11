package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"reflect"
	"testing"
)

func resolvedTypeNameLeafSamples() []ast.Expr {
	return []ast.Expr{&ast.Ident{Name: "value"}, &ast.IntLit{Value: "1"}, &ast.FloatLit{Value: "1.5"}, &ast.StringLit{Value: "text"}, &ast.CharLit{Value: "x"}, &ast.BoolLit{Value: true}, &ast.NullLit{}, &ast.ZeroedLit{}}
}

func TestResolvedTypeNameLeafShortcutCannotHideTypeChildren(t *testing.T) {
	for _, leaf := range resolvedTypeNameLeafSamples() {
		if _, ok := leaf.(ast.TypeExpr); ok {
			t.Fatalf("leaf %T is itself a type expression", leaf)
		}
		typ := reflect.TypeOf(leaf).Elem()
		for i := 0; i < typ.NumField(); i++ {
			if resolvedTypeNameFieldMayContainAST(typ.Field(i).Type) {
				t.Fatalf("leaf shortcut must be updated: %s.%s can contain syntax", typ, typ.Field(i).Name)
			}
		}
	}
}

func TestResolvedTypeNameLeafShortcutMatchesReference(t *testing.T) {
	header, body, shared := &ast.NamedType{Name: "Header"}, &ast.NamedType{Name: "Body"}, &ast.NamedType{Name: "Shared"}
	fn := &ast.FuncDecl{Name: "normal", Params: []ast.ParamDecl{{Name: "value", Type: header}}}
	for _, leaf := range resolvedTypeNameLeafSamples() {
		fn.Body = append(fn.Body, &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "consume"}, Args: []ast.Expr{leaf, &ast.CastExpr{Operand: leaf, Target: body}}}})
	}
	left, right := &ast.FuncDecl{Name: "left", Params: []ast.ParamDecl{{Name: "x", Type: shared}}}, &ast.FuncDecl{Name: "right", Params: []ast.ParamDecl{{Name: "x", Type: shared}}}
	cyclic := &ast.ParenExpr{}
	cyclic.Inner = cyclic
	cycleFn := &ast.FuncDecl{Name: "cycle", Body: []ast.Stmt{&ast.ExprStmt{Expr: cyclic}}}
	cases := []struct {
		name      string
		functions []*ast.FuncDecl
		rows      map[ast.TypeExpr]string
	}{
		{"leaves and shared body type", []*ast.FuncDecl{fn}, map[ast.TypeExpr]string{header: "Header", body: "Body"}},
		{"shared type across owners", []*ast.FuncDecl{left, right}, map[ast.TypeExpr]string{shared: "Shared"}},
		{"cyclic expression", []*ast.FuncDecl{cycleFn}, nil},
		{"unowned row", []*ast.FuncDecl{fn}, map[ast.TypeExpr]string{&ast.NamedType{Name: "Detached"}: "Detached"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			decls := make([]scopedDecl, len(test.functions))
			keys := make(map[*ast.FuncDecl]string)
			for i, fn := range test.functions {
				decls[i] = scopedDecl{Decl: fn}
				keys[fn] = fmt.Sprintf("%s#%d", fn.Name, i)
			}
			for _, failUnknown := range []bool{false, true} {
				got, gotLocations := captureResolvedTypeNameContributions(test.rows, decls, keys, failUnknown)
				want, wantLocations := captureResolvedTypeNameContributionsReference(test.rows, decls, keys, failUnknown)
				got.walkWork, want.walkWork = 0, 0
				if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotLocations, wantLocations) {
					t.Fatalf("capture differs from reference (failUnknown=%v): got %#v, want %#v", failUnknown, got, want)
				}
			}
		})
	}
}
