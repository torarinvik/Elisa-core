package semantic

import "testing"

func TestSubstituteErrorSetUnboundPreservesOriginal(t *testing.T) {
	original := &ErrorSetType{Tags: []string{"Failure"}, Params: []string{"R", "S"}}
	for _, bindings := range []map[string]Type{nil, {"R": &BuiltinType{Name: "u32"}}, {"R": (*ErrorSetType)(nil)}} {
		if got := substituteErrorSetParams(original, bindings); got != original {
			t.Fatalf("unbound set changed: %#v", got)
		}
	}
	got := substituteErrorSetParams(original, map[string]Type{"S": &ErrorSetType{Tags: []string{"Other"}}})
	if got == original || len(got.Params) != 1 || got.Params[0] != "R" {
		t.Fatalf("partial binding lost symbolic parameter: %#v", got)
	}
}

func BenchmarkSubstituteErrorSetUnbound(b *testing.B) {
	original := &ErrorSetType{Tags: []string{"Failure"}, Params: []string{"R", "S"}}
	bindings := map[string]Type{"Other": &BuiltinType{Name: "u32"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if substituteErrorSetParams(original, bindings) != original {
			b.Fatal("unbound set changed")
		}
	}
}
