package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestRefParamBuiltinContainersAreNotPackedEnums(t *testing.T) {
	a := &Analyzer{}
	for _, name := range []string{"array", "darray", "dict", "set", "view", "cstr"} {
		t.Run(name, func(t *testing.T) {
			expr := &ast.RefType{Elem: &ast.BuiltinTypeExpr{Name: name}}
			if a.refParamIsPackedEnum(expr) {
				t.Fatal("builtin container classified as packed enum")
			}
		})
	}
}
func BenchmarkRefParamBuiltinContainer(b *testing.B) {
	a := &Analyzer{}
	expr := &ast.RefType{Elem: &ast.BuiltinTypeExpr{Name: "darray"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if a.refParamIsPackedEnum(expr) {
			b.Fatal("unexpected packed enum")
		}
	}
}
