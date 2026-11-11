package semantic

import "testing"

func TestIndexedReturnBorrowTargetsMatchPairwise(t *testing.T) {
	byteType := &BuiltinType{Name: "u8"}
	integer := &BuiltinType{Name: "i64"}
	darray := &DArrayType{Elem: byteType}
	dynArray := &GenericInstanceType{Name: "DynArray", Args: []Type{byteType}}
	dynArrayOther := &GenericInstanceType{Name: "DynArray", Args: []Type{integer}}
	sview := &SViewType{}
	stringView := &StructType{Name: "StringView"}
	for _, tc := range []struct {
		name      string
		target    Type
		component Type
		want      bool
	}{
		{"canonical", byteType, &BuiltinType{Name: "u8"}, true},
		{"disjoint", byteType, integer, false},
		{"darray bridge", darray, dynArray, true},
		{"reverse darray bridge", dynArray, darray, true},
		{"different elements", darray, dynArrayOther, false},
		{"string view bridge", sview, stringView, true},
		{"reverse string view bridge", stringView, sview, true},
		{"invalid component", integer, &InvalidType{}, true},
		{"invalid target", &InvalidType{}, integer, true},
		{"nil component", integer, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scratch := returnBorrowTargetScratch{targets: []Type{tc.target}, components: []Type{tc.component}}
			if got := scratch.anyMatchingComponent(); got != tc.want || got != SameType(tc.target, tc.component) {
				t.Fatalf("indexed type match = %v, want %v", got, tc.want)
			}
		})
	}
}
