package semantic

import "testing"

const storeScanShareFromSource = `struct Node:
    names: darray[sview] = []

struct Entry:
    target: Node
    copies: darray[sview] = []

struct Path:
    names: mutable darray[sview] = []

def stage(target: Node, names: darray[sview]&, entries: mutable darray[Entry]&) -> void:
    can Memory.Allocate, Abort.Panic:
        entry: mutable Entry = Entry{target: target}
        for name in names |entry|:
            entry.copies.push(name)
        entries.push(entry)

def keep_header(target: Node, names: darray[sview]&, lists: mutable darray[darray[sview]]&) -> void:
    can Memory.Allocate:
        lists.push(names)

def capture(paths: mutable darray[Path]&, names: darray[sview]&) -> void:
    can Memory.Allocate:
        path: mutable Path = Path{}
        path.names.extend(names)
        paths.push(path)

struct Holder:
    all: mutable darray[darray[sview]] = []
    entries: mutable darray[Entry] = []

def sink_header(table: mutable Holder&, names: darray[sview]&) -> void:
    can Memory.Allocate:
        lists: mutable darray[darray[sview]] = []
        keep_header(Node{}, names, lists)
        table.all <- lists

def sink_staged(table: mutable Holder&, names: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        staged: mutable darray[Entry] = []
        stage(Node{}, names, staged)
        table.entries <- staged

def forward(target: Node, names: darray[sview]&, lists: mutable darray[darray[sview]]&) -> void:
    can Memory.Allocate:
        keep_header(target, names, lists)
`

// Share facts are per (into, from): stage stores its target handle into entries, never the
// header of names (only sview copies); capture stores sview copies only. keep_header, and
// forward through it, really share names' header with lists -- still reported.
func TestStoreScanShareIsPerSourceParameter(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_scan_share_from.elisa", storeScanShareFromSource, AnalyzeOptions{})
	a := result.analyzer
	a.currentScope = a.globalScope
	cases := []struct {
		fn         string
		into, from int
		want       bool
	}{
		{"stage", 2, 0, true},
		{"stage", 2, 1, false},
		{"capture", 0, 1, false},
		{"keep_header", 2, 1, true},
		{"keep_header", 2, 0, false},
		{"forward", 2, 1, true},
	}
	for _, c := range cases {
		if got := a.storeScanParamReaches(storeScanShareKey(storeScanFindFunc(t, result, c.fn), c.into, c.from)); got != c.want {
			t.Errorf("%s share %d <- %d: %v, want %v", c.fn, c.into, c.from, got, c.want)
		}
	}

	// A successful multi-function query also proves every node on its discovered path.
	a.storeScanParamWriteCache = nil
	a.storeScanParamFactsCache = nil
	forward := storeScanShareKey(storeScanFindFunc(t, result, "forward"), 2, 1)
	keepHeader := storeScanShareKey(storeScanFindFunc(t, result, "keep_header"), 2, 1)
	if !a.storeScanParamReaches(forward) {
		t.Fatal("forward should reach the keep_header share writer")
	}
	if !a.storeScanParamWriteCache[keepHeader] {
		t.Fatal("successful reachability path should cache its intermediate callee node")
	}
}

// The call-site taint follows the per-pair fact: a local the callee fills with names' header
// carries it to the target (header); one filled with an unrelated handle and sview copies
// carries at most names' content.
func TestStoreScanCallWriteTaintIsPerSourceParameter(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_scan_share_from.elisa", storeScanShareFromSource, AnalyzeOptions{})
	a := result.analyzer
	a.currentScope = a.globalScope
	if got := a.calleeStoreModeInto(storeScanFindFunc(t, result, "sink_header"), 1, 0); got != storeHeader {
		t.Errorf("sink_header: mode %d, want header", got)
	}
	if got := a.calleeStoreModeInto(storeScanFindFunc(t, result, "sink_staged"), 1, 0); got >= storeHeader {
		t.Errorf("sink_staged: mode %d, want below header", got)
	}
}
