package semantic

import (
	"fmt"
	"testing"

	"elisacore/src/ast"
)

func TestParameterSpecBinderIDFormatting(t *testing.T) {
	for _, i := range []int{-1, 0, 7, 15, 16, 127, 1024} {
		if got, want := string(paramSpecBinderID(i)), fmt.Sprintf("param:%d", i); got != want {
			t.Fatalf("parameter %d: got %q, want %q", i, got, want)
		}
	}
}

func TestCloneSpecSignatureBinderIsolation(t *testing.T) {
	result := SpecBinder{SourceName: "result"}
	original := &SpecSignature{Params: []SpecBinder{{SourceName: "first"}, {SourceName: "second"}}, Result: &result}
	clone := cloneSpecSignature(original)
	clone.Params[0].SourceName = "changed"
	clone.Result.SourceName = "changed-result"
	if original.Params[0].SourceName != "first" || original.Result.SourceName != "result" {
		t.Fatal("clone shares binders")
	}
	clone.Params = append(clone.Params, SpecBinder{SourceName: "third"})
	if clone.Result.SourceName != "changed-result" {
		t.Fatal("appending params overwrote result binder")
	}
}

func BenchmarkCloneSpecSignature(b *testing.B) {
	result := SpecBinder{SourceName: "result"}
	sig := &SpecSignature{Params: make([]SpecBinder, 12), Result: &result}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if cloneSpecSignature(sig).Result == nil {
			b.Fatal("missing result")
		}
	}
}

func BenchmarkBuildSpecSignature(b *testing.B) {
	params := make([]ast.ParamDecl, 12)
	types := make([]Type, len(params))
	for i := range params {
		params[i].Name = "value"
		types[i] = &BuiltinType{Name: "i64"}
	}
	a := &Analyzer{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if sig := a.buildSpecSignature("f", params, types, nil, nil, nil, nil, nil); len(sig.Params) != len(params) {
			b.Fatal("missing binders")
		}
	}
}
