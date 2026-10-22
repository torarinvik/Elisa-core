package semantic

import (
	"reflect"
	"strconv"
	"testing"
)

func refElementFixture() (*StructType, *RefType) {
	fields := map[string]Field{}
	for i := 0; i < 128; i++ {
		name := "field" + strconv.Itoa(i)
		var typ Type = &BuiltinType{Name: "i64"}
		if i%2 == 0 {
			typ = &SViewType{}
		}
		fields[name] = Field{Name: name, Type: typ}
	}
	base := &StructType{Name: "SharedElement", Fields: fields}
	return base, &RefType{Elem: base, State: RefStateNonNull}
}

func TestAbstractRefElementSummaryMatchesUncachedWalk(t *testing.T) {
	plain, ref := refElementFixture()
	recursive := &StructType{Name: "RecursiveRef", Fields: map[string]Field{}}
	recursiveRef := &RefType{Elem: recursive, State: RefStateNonNull}
	recursive.Fields["next"] = Field{Name: "next", Type: recursiveRef}
	collision := &StructType{Name: "Collision", Fields: map[string]Field{}}
	collisionRef := &RefType{Elem: collision, State: RefStateNonNull}
	collision.Fields["nested"] = Field{Name: "nested", Type: &StructType{Name: collisionRef.String(), Fields: map[string]Field{"text": {Name: "text", Type: &SViewType{}}}}}
	generic := &StructType{Name: "GenericLoop", Fields: map[string]Field{}}
	genericRef := &RefType{Elem: generic, State: RefStateNonNull}
	box := &StructType{Name: "Box", TypeParams: []string{"T"}, Fields: map[string]Field{"next": {Name: "next", Type: &RefType{Elem: &TypeParamType{Name: "T"}, State: RefStateNonNull}}}}
	generic.Fields["box"] = Field{Name: "box", Type: &GenericInstanceType{Base: box, Args: []Type{generic}}}
	values := &EnumType{Name: "RecursiveValues"}
	values.Variants = []*EnumVariant{{Name: "More", Payload: []Type{&DArrayType{Elem: values, Shape: &WildcardShape{}}}}, {Name: "Text", Payload: []Type{&SViewType{}}}}
	aggregate := &StructType{Name: "ValueAggregate", Fields: map[string]Field{"values": {Name: "values", Type: values}, "array": {Name: "array", Type: &ArrayType{Elem: plain, Size: "3", ConstSize: 3, HasConstSize: true}}, "view": {Name: "view", Type: &ViewType{Elem: plain}}, "optional": {Name: "optional", Type: &OptionalType{Value: plain}}, "dict": {Name: "dict", Type: &DictType{Key: &BuiltinType{Name: "u64"}, Value: plain}}}}
	for _, base := range []*StructType{recursive, collision, generic, aggregate} {
		for len(base.Fields) < 8 {
			name := "padding" + strconv.Itoa(len(base.Fields))
			base.Fields[name] = Field{Name: name, Type: &BuiltinType{Name: "i64"}}
		}
	}
	for _, typ := range []*RefType{ref, recursiveRef, collisionRef, genericRef, {Elem: aggregate, State: RefStateNonNull}} {
		for _, param := range []int{0, 3, 70, 135} {
			baseline := &Analyzer{}
			want, known := baseline.abstractParamRegionRefStateWalk(typ, param, map[string]bool{})
			optimized := &Analyzer{typeShapesFrozen: true}
			// A cached standalone element must not bypass a recursive or
			// nominal marker introduced by a later reference traversal.
			optimized.abstractParamRegionRefState(typ.Elem, param, nil)
			for repeat := 0; repeat < 3; repeat++ {
				got, ok := optimized.abstractParamRegionRefStateWalk(typ, param, map[string]bool{})
				if ok != known || !reflect.DeepEqual(got, want) {
					t.Fatalf("%s parameter %d differs on repeat %d", typ.String(), param, repeat)
				}
			}
		}
	}
}

var abstractRefElementSink regionRefState

func BenchmarkAbstractRefElementSummary(b *testing.B) {
	_, typ := refElementFixture()
	a := &Analyzer{typeShapesFrozen: true}
	seen := map[string]bool{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		state, ok := a.abstractParamRegionRefStateWalk(typ, 3, seen)
		if !ok || len(state.Fields) != 64 {
			b.Fatal("lost view fields")
		}
		abstractRefElementSink = state
	}
}

func TestAbstractRefElementMemoSharesOnlyIndependentFrozenFields(t *testing.T) {
	base, firstRef := refElementFixture()
	a := &Analyzer{typeShapesFrozen: true}
	first, _ := a.abstractParamRegionRefStateWalk(firstRef, 3, map[string]bool{})
	second, _ := a.abstractParamRegionRefStateWalk(&RefType{Elem: base, Mutable: true, State: RefStateNonNull}, 3, map[string]bool{})
	if regionRefFieldsIdentity(first.Fields) == 0 || regionRefFieldsIdentity(first.Fields) != regionRefFieldsIdentity(second.Fields) {
		t.Fatal("equivalent reference wrappers rebuilt frozen element fields")
	}
	other, _ := a.abstractParamRegionRefStateWalk(firstRef, 70, map[string]bool{})
	if regionRefFieldsIdentity(other.Fields) == regionRefFieldsIdentity(first.Fields) || !regionRefStateHasParamDep(other.Fields["field0"], 70) {
		t.Fatal("element summaries crossed parameter identities")
	}
	baseline := &Analyzer{}
	want, known := baseline.abstractParamRegionRefStateWalk(firstRef, 3, map[string]bool{base.String(): true})
	got, ok := a.abstractParamRegionRefStateWalk(firstRef, 3, map[string]bool{base.String(): true})
	if ok != known || !reflect.DeepEqual(got, want) || len(got.Fields) != 0 {
		t.Fatal("a nonempty outer path reused a context-free summary")
	}
	unfrozen := &Analyzer{}
	if unfrozen.abstractRefElementCanRestart(base, firstRef.String()) || len(unfrozen.abstractRefRestartMemo) != 0 {
		t.Fatal("unfrozen layouts entered the restart cache")
	}
}

func TestAbstractRefRestartMemoUsesFrozenWorkerBase(t *testing.T) {
	element, ref := refElementFixture()
	key := abstractRefRestartKey{element, ref.String()}
	base := &Analyzer{typeShapesFrozen: true, abstractRefRestartMemo: map[abstractRefRestartKey]bool{key: false}}
	worker := cloneAnalyzerValueForBodyWorker(base).Addr().Interface().(*Analyzer)
	if worker.abstractRefRestartMemo != nil {
		t.Fatal("worker copied the frozen restart cache")
	}
	worker.parallel = &parallelWorkerState{abstractRefRestartMemoBase: base.abstractRefRestartMemo}
	if worker.abstractRefElementCanRestart(element, key.blocked) {
		t.Fatal("cached false result was recomputed")
	}
	mutableKey := abstractRefRestartKey{element, (&RefType{Elem: element, Mutable: true, State: RefStateNonNull}).String()}
	if !worker.abstractRefElementCanRestart(element, mutableKey.blocked) {
		t.Fatal("uncached independent layout was not classified")
	}
	if len(base.abstractRefRestartMemo) != 1 || len(worker.abstractRefRestartMemo) != 1 || base.abstractRefRestartMemo[key] {
		t.Fatal("worker classification mutated its frozen base")
	}
}
