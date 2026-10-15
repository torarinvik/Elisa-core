package semantic

import "testing"

// SameType's fast paths (builtin names, pointer identity) must agree with canonical-ID equality.
func TestSameTypeFastPathsMatchCanonicalIDs(t *testing.T) {
	i64 := &BuiltinType{Name: "i64"}
	i64b := &BuiltinType{Name: "i64"}
	u8 := &BuiltinType{Name: "u8"}
	arr := &DArrayType{Elem: i64}
	arrb := &DArrayType{Elem: i64b}
	cases := []struct{ a, b Type }{{i64, i64b}, {i64, u8}, {arr, arr}, {arr, arrb}, {arr, &DArrayType{Elem: u8}}}
	for _, c := range cases {
		want := CanonicalTypeID(c.a) == CanonicalTypeID(c.b)
		if got := SameType(c.a, c.b); got != want {
			t.Fatalf("SameType(%s, %s) = %v, canonical IDs say %v", c.a, c.b, got, want)
		}
	}
}
