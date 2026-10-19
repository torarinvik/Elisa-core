package semantic

import "testing"

func TestCommonRegionGraphOrderRegression(t *testing.T) {
	for _, shared := range []bool{false, true} {
		d := &DArrayType{Region: "r"}
		child := d
		if !shared {
			child = &DArrayType{Region: "r"}
		}
		ref := &RefType{Elem: child}
		for _, fields := range [][]TupleField{{{Type: d}, {Type: ref}}, {{Type: ref}, {Type: d}}} {
			if got := commonExplicitTypeRegion(&TupleType{Fields: fields}); got != "r" {
				t.Fatalf("shared=%v got %q", shared, got)
			}
		}
	}
}
func TestCommonRegionGraphRecursiveEvidence(t *testing.T) {
	s := &StructType{}
	ref := &RefType{Elem: s}
	s.Fields = map[string]Field{"self": {Type: ref}, "buffer": {Type: &DArrayType{Region: "r"}}}
	if got := commonExplicitTypeRegion(s); got != "r" {
		t.Fatalf("recursive evidence: %q", got)
	}
	loop := &RefType{}
	loop.Elem = loop
	for _, bad := range []Type{loop, &TupleType{Fields: []TupleField{{Type: s}, {Type: &DArrayType{}}}}, &TupleType{Fields: []TupleField{{Type: s}, {Type: &DArrayType{Region: "other"}}}}, &TupleType{Fields: []TupleField{{Type: s}, {Type: &RefType{}}}}} {
		if got := commonExplicitTypeRegion(bad); got != "" {
			t.Fatalf("unknown/mixed evidence inferred %q", got)
		}
	}
}

func TestCommonRegionGraphPureCycleSibling(t *testing.T) {
	loop := &RefType{}
	loop.Elem = loop
	root := &TupleType{Fields: []TupleField{{Type: loop}, {Type: &DArrayType{Region: "r"}}}}
	if got := commonExplicitTypeRegion(root); got != "" {
		t.Fatalf("pure cycle inherited unrelated sibling %q", got)
	}
}
