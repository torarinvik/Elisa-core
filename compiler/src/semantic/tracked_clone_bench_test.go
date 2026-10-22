package semantic

import (
	"fmt"
	"testing"

	"elisacore/src/ast"
)

func BenchmarkTrackedCloneScalarFields(b *testing.B) {
	for _, width := range []int{8, 64, 256} {
		b.Run(fmt.Sprint(width), func(b *testing.B) {
			root := &StructType{Name: "Record", Decl: &ast.StructDecl{Fields: make([]ast.FieldDecl, width)}, Fields: map[string]Field{}}
			for i := 0; i < width; i++ {
				name := fmt.Sprint(i)
				root.Fields[name] = Field{Name: name, Type: &EnumType{Name: "E" + name}}
			}
			analyzer := &Analyzer{}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if clone := analyzer.cloneTrackedValueType(root); clone != root {
					b.Fatal("pointer-free root was copied")
				}
			}
		})
	}
}
