package semantic

import (
	"strings"
	"testing"
)

func TestStoreFlowSnapshotScratchDoesNotRetainInTable(t *testing.T) {
	source := `
struct Snapshot:
    names: darray[sview] = []
struct Table:
    stored: mutable darray[sview] = []

def project(snapshot: Snapshot&, caller: sview, edge_from: mutable darray[sview]&, edge_to: mutable darray[sview]&) -> bool:
    requires snapshot.names.count > 0
    can Memory.Allocate, Abort.Panic:
        edge_from.push(caller)
        edge_to.push(snapshot.names[0])
        return true

def axis(snapshot: Snapshot&, input_names: darray[sview]&, out_names: mutable darray[sview]&, direct: mutable darray[bool]&, edge_from: mutable darray[sview]&, edge_to: mutable darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        return if snapshot.names.count == 0
        for name in input_names |out_names, direct, edge_from, edge_to|:
            out_names.push(name)
            projected: mutable bool = false
            if project(snapshot, name, edge_from, edge_to):
                direct.push(projected)

def close_axis(names: darray[sview]&, direct: mutable darray[bool]&, edge_from: darray[sview]&, edge_to: darray[sview]&) -> void:
    can Abort.Panic:
        for edge in 0..<edge_to.count |direct|:
            for slot in 0..<names.count |direct|:
                if names[slot] == edge_to[edge] and slot < direct.count:
                    direct[slot] <- true

def add_row(name: sview, table: lmut Table) -> void:
    can Memory.Allocate, Abort.Panic:
        table.stored <- table.stored.push(name)

def check(table: lmut Table, snapshot: Snapshot&, input_names: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        names: mutable darray[sview] = []
        direct: mutable darray[bool] = []
        edge_from: mutable darray[sview] = []
        edge_to: mutable darray[sview] = []
        axis(snapshot, input_names, names, direct, edge_from, edge_to)
        close_axis(names, direct, edge_from, edge_to)
        for index in 0..<names.count |table|:
            if index < direct.count and direct[index]:
                table <- add_row(names[index], table)
`

	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "snapshot_scratch.elisa", source, AnalyzeOptions{})
	if es := r.Errors(); len(es) != 0 {
		t.Fatal(es)
	}
	checks := []struct {
		name     string
		from, to int
		want     bool
	}{
		{"project", 0, 3, true}, {"project", 0, 2, false}, {"project", 1, 2, true},
		{"axis", 0, 5, true}, {"axis", 0, 2, false}, {"axis", 0, 3, false}, {"axis", 0, 4, false}, {"axis", 1, 2, true},
		{"close_axis", 3, 1, false}, {"add_row", 0, 1, true}, {"check", 1, 0, false}, {"check", 2, 0, true},
	}
	for _, tc := range checks {
		summary := r.analyzer.storeFlowSummaryFor(storeScanFindFunc(t, r, tc.name))
		if summary == nil || summary.reaches(tc.from, tc.to) != tc.want {
			t.Fatalf("%s source %d -> target %d want %v: %+v", tc.name, tc.from, tc.to, tc.want, summary)
		}
	}
	marker := "        axis(snapshot, input_names, names, direct, edge_from, edge_to)"
	if strings.Count(source, marker) != 1 {
		t.Fatal("scratch projection missing or ambiguous")
	}
	leaked := strings.Replace(source, marker, "        if snapshot.names.count > 0:\n            table.stored <- table.stored.push(snapshot.names[0])\n"+marker, 1)
	bad := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "snapshot_scratch_leak.elisa", leaked, AnalyzeOptions{})
	if es := bad.Errors(); len(es) != 0 {
		t.Fatalf("retention control must be well-typed: %v", es)
	}
	summary := bad.analyzer.storeFlowSummaryFor(storeScanFindFunc(t, bad, "check"))
	if summary == nil || !summary.reaches(1, 0) {
		t.Fatalf("actual snapshot view retention lost: %+v", summary)
	}
}
