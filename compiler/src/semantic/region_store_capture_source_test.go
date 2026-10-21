package semantic

import "testing"

func TestStoreScanCaptureDoesNotSupplyIterableElements(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "capture_elements.elisa", `struct Source:
    values: mutable darray[sview] = []
struct Table:
    values: mutable darray[sview] = []
def independent(table: mutable Table&, source: mutable Source&, names: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        for name in names |source, table|:
            table.values.push(name)
def dependent(table: mutable Table&, source: mutable Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        for name in source.values |source, table|:
            table.values.push(name)
`, AnalyzeOptions{})
	a := result.analyzer
	a.currentScope = a.globalScope
	for _, test := range []struct {
		name string
		want bool
	}{{"independent", false}, {"dependent", true}} {
		decl := storeScanFindFunc(t, result, test.name)
		mode := a.calleeStoreModeInto(decl, 1, 0)
		t.Logf("%s mode=%v", test.name, mode)
		if (mode != storeNone) != test.want {
			t.Errorf("%s retains source=%v, want %v", test.name, mode != storeNone, test.want)
		}
	}
}

func TestStoreScanIndependentWritesDoNotTaintBuffer(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "unrelated_call_writes.elisa", `struct Source:
    values: mutable darray[sview] = []
struct Table:
    values: mutable darray[sview] = []
def fill(buffer: mutable darray[sview]&, source: mutable Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        buffer.push("literal")
        source.values.push("source literal")
def forward(table: mutable Table&, source: mutable Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        buffer: mutable darray[sview] = []
        fill(buffer, source)
        table.values.extend(buffer)
`, AnalyzeOptions{})
	a := result.analyzer
	a.currentScope = a.globalScope
	decl := storeScanFindFunc(t, result, "forward")
	mode := a.calleeStoreModeInto(decl, 1, 0)
	if mode != storeNone {
		t.Fatalf("writing independent literals to two destinations must not retain it; mode=%v", mode)
	}
}
