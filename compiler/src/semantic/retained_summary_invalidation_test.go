package semantic

import (
	"reflect"
	"strings"
	"testing"
)

func TestRetainedRestoreRecomputesTransitiveInferredReturnSummary(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const before = `struct BorrowedBox:
    value: i64&

global root: i64 = 2

def pack(value: i64&) -> BorrowedBox:
    return BorrowedBox{value: value}

def relay(value: i64&) -> BorrowedBox:
    return pack(value)

def escapes_local() -> BorrowedBox:
    local: i64 = 3
    return relay(&local)
`
	const after = `struct BorrowedBox:
    value: i64&

global root: i64 = 2

def pack(value: i64&) -> BorrowedBox:
    return BorrowedBox{value: &root}

def relay(value: i64&) -> BorrowedBox:
    return pack(value)

def escapes_local() -> BorrowedBox:
    local: i64 = 3
    return relay(&local)
`
	if len(before) != len(after) {
		t.Fatal("fixture edit must preserve source offsets")
	}
	initial, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if !strings.Contains(retainedDiagnostics(initial), "function-local storage") {
		t.Fatalf("fixture lost its initial escape error:\n%s", retainedDiagnostics(initial))
	}
	data, err := retained.MarshalState()
	if err != nil {
		t.Fatalf("marshal retained state: %v", err)
	}
	retained, err = RestoreAnalysisState(data)
	if err != nil {
		t.Fatalf("restore retained state: %v", err)
	}
	warm, err := retained.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatalf("retained update: %v", err)
	}
	cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	assertRetainedLiveResultParity(t, cold, warm)
	if strings.Contains(retainedDiagnostics(warm), "function-local storage") {
		t.Fatalf("retained update kept the transitive stale escape diagnostic:\n%s", retainedDiagnostics(warm))
	}
}

// Retained analyses keep facts for old body nodes in their private backing maps.
// Code generation queries the current AST. Compare every current key, including
// unexpected live facts, while ignoring only unreachable historical nodes.
func assertRetainedLiveResultParity(t testing.TB, cold, warm *Result, extra ...map[PositionedFunctionNode]PositionedFunctionNode) {
	t.Helper()
	mapping := map[PositionedFunctionNode]PositionedFunctionNode{}
	for _, supplied := range extra {
		for source, current := range supplied {
			mapping[source] = current
		}
	}
	mapIncrementalCurrentAST(t, reflect.ValueOf(cold.File), reflect.ValueOf(warm.File), mapping)
	live := map[PositionedFunctionNode]bool{}
	for _, node := range mapping {
		live[node] = true
	}
	projected := *warm
	fields := reflect.ValueOf(&projected).Elem()
	for i := 0; i < fields.NumField(); i++ {
		field := fields.Field(i)
		if field.Kind() != reflect.Map || !field.CanSet() || field.IsNil() {
			continue
		}
		filtered := reflect.MakeMapWithSize(field.Type(), field.Len())
		iterator := field.MapRange()
		for iterator.Next() {
			key := iterator.Key()
			if key.CanInterface() {
				if node, ok := key.Interface().(PositionedFunctionNode); ok && !live[node] {
					continue
				}
			}
			filtered.SetMapIndex(key, iterator.Value())
		}
		field.Set(filtered)
	}
	assertIncrementalResultParity(t, cold, &projected, mapping)
}
