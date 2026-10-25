//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func TestSpecializationTypedExprTypesCloneMatchesExistingOpaqueInterfacePolicy(t *testing.T) {
	first, second, third := &ast.IntLit{Value: "1"}, &ast.IntLit{Value: "2"}, &ast.IntLit{Value: "3"}
	primitive := &BuiltinType{Name: "i64"}
	aggregate := &ArrayType{Elem: primitive, HasConstSize: true, ConstSize: 2, Size: "2"}
	var typedNil *BuiltinType
	source := &Analyzer{exprTypes: map[ast.Expr]Type{first: aggregate, second: nil, third: typedNil}}
	expected := deepCloneValue(reflect.ValueOf(source.exprTypes)).Interface().(map[ast.Expr]Type)
	actual := cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
	if !reflect.DeepEqual(expected, actual.exprTypes) {
		t.Fatal("typed clone changed existing interface/typed-nil membership")
	}
	if reflect.ValueOf(actual.exprTypes).Pointer() == reflect.ValueOf(source.exprTypes).Pointer() {
		t.Fatal("typed clone shared writable canonical map")
	}
	if actual.exprTypes[first] != aggregate || actual.exprTypes[third] != typedNil {
		t.Fatal("typed clone changed established value identity")
	}
	if _, present := actual.exprTypes[second]; !present {
		t.Fatal("present nil type entry lost")
	}
	delete(actual.exprTypes, first)
	actual.exprTypes[second] = primitive
	if source.exprTypes[first] != aggregate || source.exprTypes[second] != nil {
		t.Fatal("private map writes reached canonical expression table")
	}
	for _, value := range []map[ast.Expr]Type{nil, {}} {
		cloned := cloneAnalyzerValueForSpecialization(&Analyzer{exprTypes: value}).Addr().Interface().(*Analyzer).exprTypes
		if (value == nil) != (cloned == nil) {
			t.Fatal("nil/empty expression-map distinction changed")
		}
	}
}

func BenchmarkSpecializationExprTypeMapClone(b *testing.B) {
	// Isolates just the changed table; the existing real-source construction
	// benchmark remains the production end-to-end allocation/time authority.
	for _, count := range []int{0, 8192, 65536} {
		b.Run(fmt.Sprintf("rows_%d", count), func(b *testing.B) {
			source := &Analyzer{exprTypes: map[ast.Expr]Type{}}
			typ := &BuiltinType{Name: "i64"}
			for i := 0; i < count; i++ {
				source.exprTypes[&ast.IntLit{Value: fmt.Sprint(i)}] = typ
			}
			b.Run("reflective_worker", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					specializationAnalysisCloneSink = cloneAnalyzerValueForBodyWorker(source).Addr().Interface().(*Analyzer)
				}
			})
			b.Run("typed_specialization", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					specializationAnalysisCloneSink = cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
				}
			})
			specializationAnalysisCloneSink = nil
		})
	}
}
