package semantic

import (
	"strconv"
	"testing"
)

func genericProvenanceCapacityFixture() Type {
	integer := &BuiltinType{Name: "i64"}
	fields := make(map[string]Field)
	for i := 0; i < 128; i++ {
		name := "counter" + strconv.Itoa(i)
		fields[name] = Field{Name: name, Type: integer}
	}
	fields["value"] = Field{Name: "value", Type: &TypeParamType{Name: "T"}}
	fields["text"] = Field{Name: "text", Type: &SViewType{}}
	base := &StructType{Name: "GenericCounters", TypeParams: []string{"T"}, Fields: fields}
	return &GenericInstanceType{Base: base, Args: []Type{integer}}
}

func TestAbstractGenericProvenanceSkipsScalarFields(t *testing.T) {
	for _, frozen := range []bool{false, true} {
		for _, argument := range []Type{&BuiltinType{Name: "i64"}, &SViewType{}, &RefType{Elem: &BuiltinType{Name: "i64"}, State: RefStateNonNull}} {
			typ := genericProvenanceCapacityFixture().(*GenericInstanceType)
			typ.Args[0] = argument
			a := &Analyzer{typeShapesFrozen: frozen}
			state, ok := a.abstractParamRegionRefState(typ, 3, map[string]bool{"parent": true})
			wantFields := 2
			if IsNumericType(argument) {
				wantFields = 1
			}
			if !ok || len(state.Fields) != wantFields || !regionRefStateHasParamDep(state.Fields["text"], 3) {
				t.Fatalf("generic layout changed field provenance (frozen=%v, argument=%T)", frozen, argument)
			}
			if wantFields == 2 && !regionRefStateHasParamDep(state.Fields["value"], 3) {
				t.Fatal("substituted generic reference lost its parameter dependency")
			}
		}
	}
}

func BenchmarkAbstractGenericProvenanceCapacity(b *testing.B) {
	a := &Analyzer{typeShapesFrozen: true}
	typ := genericProvenanceCapacityFixture()
	seen := map[string]bool{"parent": true}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		state, ok := a.abstractParamRegionRefState(typ, 3, seen)
		if !ok || len(state.Fields) != 1 {
			b.Fatal("generic provenance changed")
		}
	}
}
