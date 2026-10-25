//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestSpecializationParamFactsExactOldValuePolicy(t *testing.T) {
	if !specializationParamFactsCopySchemaValid {
		t.Fatal("schema drift needs reflective fallback qualification")
	}
	for _, typ := range []reflect.Type{nil, reflect.TypeFor[int](), reflect.TypeFor[struct {
		why    string
		at     any
		writes bool
		reach  []storeScanParamKey
		extra  map[string]int
	}](), reflect.TypeFor[struct {
		why    string
		at     any
		writes bool
		reach  []int
	}]()} {
		if specializationParamFactsSchemaMatches(typ) {
			t.Fatal("unknown schema admitted", typ)
		}
	}
	fn := &ast.FuncDecl{}
	node := &ast.Ident{Name: "origin"}
	key := storeScanParamKey{fn, 3, 17}
	for _, reach := range [][]storeScanParamKey{nil, make([]storeScanParamKey, 0, 7), make([]storeScanParamKey, 2, 8)} {
		if len(reach) > 0 {
			reach[0] = key
			reach[1] = storeScanParamKey{fn, 4, 2}
		}
		source := map[storeScanParamKey]storeScanParamFacts{key: {why: "reason", at: node, writes: true, reach: reach}}
		want := deepCloneValue(reflect.ValueOf(source)).Interface().(map[storeScanParamKey]storeScanParamFacts)
		private := cloneAnalyzerValueForSpecialization(&Analyzer{storeScanParamFactsCache: source}).Addr().Interface().(*Analyzer)
		got, present := private.lookupStoreScanParamFacts(key)
		if !present || !reflect.DeepEqual(got, want[key]) || cap(got.reach) != len(reach) || (got.reach == nil) != (reach == nil) || got.at != node {
			t.Fatal("old reflective policy differs")
		}
		if len(reach) > 0 && &got.reach[0] != &reach[0] {
			t.Fatal("backing alias changed")
		}
		private.setStoreScanParamFacts(key, storeScanParamFacts{})
		if !reflect.DeepEqual(source[key], storeScanParamFacts{why: "reason", at: node, writes: true, reach: reach}) {
			t.Fatal("outer alias escaped")
		}
		ordinary := cloneAnalyzerValueForBodyWorker(&Analyzer{storeScanParamFactsCache: source}).Addr().Interface().(*Analyzer)
		if !reflect.DeepEqual(ordinary.storeScanParamFactsCache, want) {
			t.Fatal("ordinary path changed")
		}
	}
	for _, source := range []map[storeScanParamKey]storeScanParamFacts{nil, {}} {
		got := cloneSpecializationParamFacts(source)
		if (got == nil) != (source == nil) {
			t.Fatal("nil/empty map changed")
		}
	}
}
func TestSpecializationParamFactsActualComputedRows(t *testing.T) {
	const source = "def keep(x: i64) -> i64:\n    return x\n"
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	fn := state.pristine.Decls[0].(*ast.FuncDecl)
	key := storeScanParamKey{fn, 0, storeScanModeWrite}
	want := state.a.storeScanParamLocalFacts(key)
	private := cloneAnalyzerValueForSpecialization(state.a).Addr().Interface().(*Analyzer)
	if got := private.storeScanParamLocalFacts(key); !reflect.DeepEqual(got, want) {
		t.Fatal("actual cached local facts changed")
	}
}

var paramCopySink map[storeScanParamKey]storeScanParamFacts

func BenchmarkSpecializationParamFactsCopy(b *testing.B) {
	source := map[storeScanParamKey]storeScanParamFacts{}
	for i := 0; i < 8192; i++ {
		key := storeScanParamKey{&ast.FuncDecl{}, i, 0}
		source[key] = storeScanParamFacts{why: "fixture", reach: make([]storeScanParamKey, 2, 8)}
	}
	for _, mode := range []string{"old_reflect", "typed_capped"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if mode == "old_reflect" {
					paramCopySink = deepCloneValue(reflect.ValueOf(source)).Interface().(map[storeScanParamKey]storeScanParamFacts)
				} else {
					paramCopySink = cloneSpecializationParamFacts(source)
				}
			}
		})
	}
}
