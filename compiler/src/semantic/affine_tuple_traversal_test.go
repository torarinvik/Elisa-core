package semantic

import (
	"strings"
	"testing"
)

func TestContainsAffineHandleValuesTraversesTuples(t *testing.T) {
	handle := &StructType{Name: "Handle", Affine: true}
	plain := &BuiltinType{Name: "i64"}
	for _, tc := range []struct {
		name string
		typ  Type
		want bool
	}{
		{"plain", &TupleType{Fields: []TupleField{{Type: plain}}}, false},
		{"affine_second_field", &TupleType{Fields: []TupleField{{Type: plain}, {Type: handle}}}, true},
		{"nested_optional", &OptionalType{Value: &TupleType{Fields: []TupleField{{Type: handle}}}}, true},
		{"borrow_is_not_owned", &TupleType{Fields: []TupleField{{Type: &RefType{Elem: handle}}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			analyzer := newSemanticHardeningTestAnalyzer()
			if got := analyzer.containsAffineHandleValues(tc.typ, nil); got != tc.want {
				t.Fatalf("containsAffineHandleValues = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReferenceToTupleContainingAffineHandleIsRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "affine_tuple_borrow.elisa", affineBorrowPrelude+`def borrow_tuple(value: (number: i64, handle: K)&) -> void:
    pass
`)
	if errors := strings.Join(result.Errors(), "\n"); !strings.Contains(errors, "references to values containing linear handles are not supported") {
		t.Fatalf("expected aggregate borrow rejection, got:\n%s", errors)
	}
}
