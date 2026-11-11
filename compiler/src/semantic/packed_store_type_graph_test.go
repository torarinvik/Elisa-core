package semantic

import "testing"

func TestPackedStoreTypeGraphRevisitsShallowerPath(t *testing.T) {
	packed := &EnumType{Name: "Root", Packed: true}
	shared := &StructType{Name: "Shared", Fields: map[string]Field{"payload": {Name: "payload", Type: packed}}}
	var deep Type = shared
	for i := 0; i < collectPackedEnumsMaxDepth-1; i++ {
		deep = &StructType{Fields: map[string]Field{"next": {Name: "next", Type: deep}}}
	}
	entry := &StructType{Fields: map[string]Field{
		"a_deep":  {Name: "a_deep", Type: deep},
		"z_short": {Name: "z_short", Type: shared},
	}}
	roots := map[string]*EnumType{}
	collectPackedEnumsInType(entry, roots)
	if len(roots) != 1 || roots["Root"] != packed {
		t.Fatalf("shallower path lost packed root: %v", roots)
	}
	clear(roots)
	collectPackedEnumsInSignature(&FuncType{Params: []Type{deep}, Return: shared}, true, roots)
	if len(roots) != 1 || roots["Root"] != packed {
		t.Fatalf("shallower return type lost packed root: %v", roots)
	}
}

func TestPackedStoreTypeGraphCyclesAndDistinctRoots(t *testing.T) {
	left, right := &EnumType{Name: "Left", Packed: true}, &EnumType{Name: "Right", Packed: true}
	cyclic := &StructType{Fields: map[string]Field{}}
	cyclic.Fields["cycle"] = Field{Name: "cycle", Type: cyclic}
	cyclic.Fields["left"] = Field{Name: "left", Type: left}
	cyclic.Fields["right"] = Field{Name: "right", Type: right}
	roots := map[string]*EnumType{}
	collectPackedEnumsInType(cyclic, roots)
	if len(roots) != 2 || roots["Left"] != left || roots["Right"] != right {
		t.Fatalf("incomplete roots: %v", roots)
	}
}

func TestPackedStoreSignatureCachesOnlyEmptyRoot(t *testing.T) {
	packed := &EnumType{Name: "Root", Packed: true}
	var deep Type = packed
	for range collectPackedEnumsMaxDepth + 1 {
		deep = &TupleType{Fields: []TupleField{{Type: deep}}}
	}
	roots := map[string]*EnumType{}
	empty := map[Type]bool{}
	signature := &FuncType{Params: []Type{deep}, Return: packed}
	for range 2 {
		collectPackedEnumsInSignatureWithOrder(signature, true, roots, nil, empty)
		if !empty[deep] || empty[packed] || len(roots) != 1 || roots["Root"] != packed {
			t.Fatalf("depth-limited parameter lost the shallower return root: roots=%v", roots)
		}
		clear(roots)
	}
}

func BenchmarkPackedStoreSharedTypeGraph(b *testing.B) {
	var typ Type = &BuiltinType{Name: "i64"}
	for i := 0; i < 7; i++ {
		typ = &StructType{Fields: map[string]Field{
			"a": {Name: "a", Type: typ}, "b": {Name: "b", Type: typ},
		}}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		collectPackedEnumsInType(typ, map[string]*EnumType{})
	}
}

func TestPackedStoreSignatureCachePreservesRootOrder(t *testing.T) {
	first := &EnumType{Name: "Shared", Packed: true}
	second := &EnumType{Name: "Shared", Packed: true}
	scalar := &BuiltinType{Name: "i64"}
	cache := map[[2]Type]map[string]*EnumType{}
	empty := map[Type]bool{}
	out := map[string]*EnumType{}
	for range 2 {
		for _, pair := range [][2]*EnumType{{first, second}, {second, first}} {
			signature := &FuncType{Params: []Type{scalar, pair[0]}, Return: pair[1]}
			collectPackedStoreSignature(signature, out, nil, empty, cache)
			if out["Shared"] != pair[0] {
				t.Fatal("cached signature changed the first root witness")
			}
			clear(out)
		}
	}
	if len(cache) != 2 {
		t.Fatalf("cached %d signatures, want 2", len(cache))
	}
}
