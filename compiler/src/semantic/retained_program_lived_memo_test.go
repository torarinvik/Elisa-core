package semantic

import (
	"reflect"
	"strings"
	"testing"
)

// This deliberately keeps an ordinary EnumType before and after the edit:
// switching a payload-less const enum into a storage enum tests another protocol.
const retainedProgramLivedMemoSource = `global region source
enum Payload:
    Empty
    Marker(i64)
struct Record:
    safe: sview @source
    payload: Payload
def identity(value: Record) -> Record:
    return value
`

func TestRetainedEnumEditInvalidatesProgramLivedTypeMemo(t *testing.T) {
	src := retainedProgramLivedMemoSource
	result, retained := AnalyzeRetained(retainedParse(t, src), retainedParse(t, src), []byte(src), AnalyzeOptions{})
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatal(errors)
	}
	typ := result.NamedTypes["Record"]
	if typ == nil || !retained.a.typeIsProgramLivedCached(typ) {
		t.Fatal("fixture must prime positive Record memo")
	}
	next := strings.Replace(src, "    Marker(i64)\n", "    Marker(i64)\n    Items(values: darray[sview])\n", 1)
	updated, err := retained.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
	if err != nil {
		t.Fatalf("ordinary enum payload addition declined; accepted-path coverage required: %v", err)
	}
	if updated.NamedTypes["Record"] != typ {
		t.Fatal("fixture did not preserve nominal Record identity")
	}
	fresh := AnalyzeWithOptions(retainedParse(t, next), AnalyzeOptions{})
	want := fresh.analyzer.typeIsProgramLivedCached(fresh.NamedTypes["Record"])
	if want {
		t.Fatal("fixture hidden local-storage payload remained program-lived")
	}
	if got := retained.a.typeIsProgramLivedCached(typ); got != want {
		t.Fatalf("retained program-lived=%v fresh=%v", got, want)
	}
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("diagnostics differ: %s / %s", got, want)
	}
}

func TestRetainedTypeMemoClearDropsPositiveAndNegativeProgramOwnership(t *testing.T) {
	a := &Analyzer{programRegions: map[string]bool{"source": true}}
	typ := &StructType{Name: "Record", Fields: map[string]Field{"safe": {Name: "safe", Type: &SViewType{Region: "source"}}}}
	if !a.typeIsProgramLivedCached(typ) {
		t.Fatal("initial program-owned record")
	}
	typ.Fields["local"] = Field{Name: "local", Type: &DArrayType{Elem: &SViewType{}}}
	if a.typeIsProgramLived(typ, map[Type]bool{}) {
		t.Fatal("fixture must acquire local storage")
	}
	a.clearRetainedTypeMemos()
	if a.typeIsProgramLivedCached(typ) {
		t.Fatal("positive memo survived mutation")
	}
	delete(typ.Fields, "local")
	a.clearRetainedTypeMemos()
	if !a.typeIsProgramLivedCached(typ) {
		t.Fatal("negative memo survived reverse mutation")
	}

	// Body workers must inherit the post-invalidation base, never the old map.
	worker := cloneAnalyzerValueForBodyWorker(a).Addr().Interface().(*Analyzer)
	worker.parallel = &parallelWorkerState{programLivedTypesBase: a.programLivedTypes}
	if !worker.typeIsProgramLivedCached(typ) {
		t.Fatal("fresh worker lost current base")
	}
	typ.Fields["local"] = Field{Name: "local", Type: &DArrayType{Elem: &SViewType{}}}
	a.clearRetainedTypeMemos()
	worker = cloneAnalyzerValueForBodyWorker(a).Addr().Interface().(*Analyzer)
	worker.parallel = &parallelWorkerState{programLivedTypesBase: a.programLivedTypes}
	if worker.typeIsProgramLivedCached(typ) {
		t.Fatal("fresh worker inherited pre-edit base")
	}
}

func TestRetainedTypeMemoClearResetsEveryListedMap(t *testing.T) {
	a := &Analyzer{}
	value := reflect.ValueOf(a).Elem()
	for _, name := range retainedTypeMemos {
		field := value.FieldByName(name)
		if !field.IsValid() || field.Kind() != reflect.Map {
			t.Fatalf("invalid type memo %s", name)
		}
		seeded := reflect.MakeMap(field.Type())
		seeded.SetMapIndex(reflect.Zero(field.Type().Key()), reflect.Zero(field.Type().Elem()))
		settable(field).Set(seeded)
	}
	a.clearRetainedTypeMemos()
	for _, name := range retainedTypeMemos {
		if got := value.FieldByName(name).Len(); got != 0 {
			t.Errorf("memo %s retains %d old answers", name, got)
		}
	}
}
