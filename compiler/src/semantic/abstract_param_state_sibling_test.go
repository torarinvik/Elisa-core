package semantic

import "testing"

// Two fields of the SAME type must each get the full abstract state. The `seen` set
// exists only to stop a recursive type from descending forever; when it survived past
// the field it guarded, the second field of that type got the truncated answer — and
// since StructType.Fields is a map, WHICH field got truncated changed run to run. A
// function returning `scope.origins[i]` then reported `isolated` (it borrows nothing)
// on some runs and the correct `alias_params=[0]` on others.
func TestAbstractParamRegionRefStateExpandsEverySameTypedField(t *testing.T) {
	a := &Analyzer{}
	elems := &DArrayType{Elem: &SViewType{}, Shape: &WildcardShape{}, SurfaceName: "darray"}
	scope := &StructType{
		Name: "Scope",
		Fields: map[string]Field{
			"names":   {Name: "names", Type: elems},
			"origins": {Name: "origins", Type: elems},
		},
	}
	state, ok := a.abstractParamRegionRefState(&RefType{Elem: scope, State: RefStateNonNull}, 0, map[string]bool{})
	if !ok {
		t.Fatalf("abstractParamRegionRefState returned ok=false for Scope&")
	}
	for _, name := range []string{"names", "origins"} {
		fieldState, ok := state.Fields[name]
		if !ok {
			t.Fatalf("field %q missing from the abstract param state", name)
		}
		if _, ok := fieldState.Fields[regionAnyIndexFieldKey()]; !ok {
			t.Fatalf("field %q has no element state: indexing it would look isolated", name)
		}
	}
}

// The guard it replaced still has to hold: a self-referential type must terminate.
func TestAbstractParamRegionRefStateTerminatesOnRecursiveType(t *testing.T) {
	a := &Analyzer{}
	node := &StructType{Name: "Node", Fields: map[string]Field{}}
	node.Fields["next"] = Field{Name: "next", Type: &RefType{Elem: node, State: RefStateNullable}}
	node.Fields["text"] = Field{Name: "text", Type: &SViewType{}}
	if _, ok := a.abstractParamRegionRefState(&RefType{Elem: node, State: RefStateNonNull}, 0, map[string]bool{}); !ok {
		t.Fatalf("abstractParamRegionRefState returned ok=false for Node&")
	}
}
