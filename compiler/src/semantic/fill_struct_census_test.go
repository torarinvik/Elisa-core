package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func fillStructCensusFixture() *ast.File {
	var body []ast.Stmt
	for i := 0; i < 10000; i++ {
		body = append(body, &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "f"}, Args: []ast.Expr{&ast.Ident{Name: "x"}}}})
	}
	return &ast.File{Decls: []ast.Decl{
		&ast.NamespaceDecl{Name: "M", Decls: []ast.Decl{&ast.StructDecl{Name: "Outer", Fields: []ast.FieldDecl{{Name: "callback", Type: &ast.FuncTypeExpr{}}}}}},
		&ast.StaticIfDecl{Then: []ast.Decl{&ast.StructDecl{Name: "First"}}, Else: []ast.Decl{&ast.StructDecl{Name: "Second"}}},
		&ast.FuncDecl{Name: "f", Body: body},
	}}
}

func BenchmarkFillStructCensus(b *testing.B) {
	file := fillStructCensusFixture()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fillMayAdoptStructFacts(file)
	}
}

func TestFillStructCensusMatchesFullSyntaxWalk(t *testing.T) {
	file := fillStructCensusFixture()
	branch := file.Decls[1].(*ast.StaticIfDecl)
	branch.Elifs = []ast.StaticElifDecl{{Body: []ast.Decl{&ast.StructDecl{Name: "Third"}}}}
	wantNames, wantFields := map[string]bool{}, map[string]bool{}
	fillMayAdoptWalk(reflect.ValueOf(file), func(v any) {
		if s, ok := v.(*ast.StructDecl); ok && s != nil {
			wantNames[fillMayAdoptLastSegment(s.Name)] = true
			for _, field := range s.Fields {
				if fillMayAdoptHasFuncType(reflect.ValueOf(field)) {
					wantFields[field.Name] = true
				}
			}
		}
	})
	gotNames, gotFields := fillMayAdoptStructFacts(file)
	if !reflect.DeepEqual(gotNames, wantNames) || !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("census differs: got %v %v; want %v %v", gotNames, gotFields, wantNames, wantFields)
	}
}
