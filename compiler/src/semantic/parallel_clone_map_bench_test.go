package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func BenchmarkParallelCloneExpressionIndex(b *testing.B) {
	source := make(map[ast.Expr]Type, 4096)
	typ := &BuiltinType{Name: "i64"}
	for i := 0; i < 4096; i++ {
		source[&ast.IntLit{Value: "1"}] = typ
	}
	value := reflect.ValueOf(source)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if deepCloneValue(value).Len() != len(source) {
			b.Fatal("lost index entry")
		}
	}
}

func TestParallelCloneNestedMapValuesStayIndependent(t *testing.T) {
	type nested struct {
		values map[string]int
		names  []string
		child  *int
	}
	child := 7
	source := map[string]nested{
		"first":  {values: map[string]int{"value": 1}, names: []string{"first"}, child: &child},
		"second": {values: map[string]int{"value": 2}, names: []string{"second"}},
		"empty":  {},
	}
	clone := deepCloneValue(reflect.ValueOf(source)).Interface().(map[string]nested)
	if !reflect.DeepEqual(source, clone) {
		t.Fatal("clone changed values")
	}
	first := clone["first"]
	first.values["value"] = 3
	if source["first"].values["value"] != 1 || clone["second"].values["value"] != 2 {
		t.Fatal("nested maps alias another entry")
	}
	if cap(first.names) != len(first.names) || first.child != &child {
		t.Fatal("clone changed slice or pointer sharing policy")
	}
	first.names = append(first.names, "added")
	clone["first"] = first
	delete(clone, "second")
	if len(source) != 3 || len(source["first"].names) != 1 {
		t.Fatal("clone changed source index")
	}
	if clone["empty"].values != nil || clone["empty"].names != nil {
		t.Fatal("clone changed nil members")
	}
}

func TestParallelCloneExpressionIndexKeepsEveryEntry(t *testing.T) {
	source := map[ast.Expr]Type{}
	for i := 0; i < 128; i++ {
		source[&ast.IntLit{Value: "1"}] = &BuiltinType{Name: "i64"}
	}
	clone := deepCloneValue(reflect.ValueOf(source)).Interface().(map[ast.Expr]Type)
	if len(clone) != len(source) {
		t.Fatal("lost index entry")
	}
	for expr, typ := range source {
		if clone[expr] != typ {
			t.Fatal("map slot reuse changed a key or value")
		}
	}
}

func BenchmarkParallelCloneStructIndex(b *testing.B) {
	type entry struct {
		values map[string]int
		names  []string
	}
	source := make(map[int]entry, 1024)
	for i := 0; i < 1024; i++ {
		source[i] = entry{values: map[string]int{"value": i}, names: []string{"name"}}
	}
	value := reflect.ValueOf(source)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if deepCloneValue(value).Len() != len(source) {
			b.Fatal("lost index entry")
		}
	}
}
