package semantic

import (
	"strings"
	"testing"
)

const storeViewsModeSource = `struct Info:
    name: sview
    n: i64

struct Owned:
    text: dstr

struct Table:
    names: mutable darray[sview] = []
    infos: mutable darray[Info] = []
    lists: mutable darray[darray[sview]] = []

def keep_view(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        n: sview = names[0] if names.count > 0 else ""
        table.names.push(n)

def keep_info(table: mutable Table&, infos: darray[Info]&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.infos.push(infos[0])

def keep_list(table: mutable Table&, names: darray[sview]) -> void:
    can Memory.Allocate:
        table.lists.push(names)

def keep_owned_view(table: mutable Table&, owned: Owned&) -> void:
    can Memory.Allocate:
        table.names.push(owned.text.as_sview())
`

// storeViews is produced exactly for view-only values read out of a byte-free source; the
// source itself stays a header store, and a byte-holding source never yields storeViews.
func TestStoreScanProducesViewsMode(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_views_mode.elisa", storeViewsModeSource, AnalyzeOptions{})
	cases := []struct {
		fn   string
		want storeMode
	}{
		{"keep_view", storeViews},
		{"keep_info", storeViews},
		{"keep_list", storeHeader},
		{"keep_owned_view", storeHeader},
	}
	for _, c := range cases {
		decl := storeScanFindFunc(t, result, c.fn)
		if got := result.analyzer.calleeStoreModeInto(decl, 1, 0); got != c.want {
			t.Errorf("%s: store mode %d, want %d", c.fn, got, c.want)
		}
	}
}

// Call-site precision for storeViews (two writable targets, so no ambient adoption): a byte-free
// struct argument whose tracked state is complete -- an immutable local initialized without
// reading a mutable local -- is judged by that state alone, not by its own storage region
// (lines 19, 21 accepted). A view of a local dstr in it (line 25), a mutable struct local
// (line 27), and call results carrying a local dstr's view directly or through a copy (second
// source, lines 24 and 27) stay rejected.
func TestStoreViewsCallSiteUsesCompleteProvenance(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_views_call_site.elisa", `struct Wrap:
    names: darray[sview]
    n: i64

struct Table:
    names: mutable darray[sview] = []

def keep_first(table: mutable Table&, w: Wrap&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.names.push(w.names[0])

def make_wrap(src: sview) -> Wrap:
    can Memory.Allocate:
        return Wrap{names: [src, "lit"], n: 2}

def run(table: mutable Table&, other: mutable Table&, src: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        w: Wrap = Wrap{names: [src, "lit"], n: 2}
        keep_first(table, &w)
        w2: Wrap = make_wrap(src)
        keep_first(table, &w2)
        text: mutable dstr = []
        text.push(65)
        bad: Wrap = Wrap{names: [text.as_sview()], n: 1}
        keep_first(table, &bad)
        m: mutable Wrap = Wrap{names: [src], n: 1}
        keep_first(table, &m)
`)
	errs := result.Errors()
	if len(errs) != 2 || !strings.Contains(errs[0], ":25:") || !strings.Contains(errs[1], ":27:") {
		t.Fatalf("expected exactly lines 25 and 27 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
	result = analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_views_call_result.elisa", `struct Wrap:
    names: darray[sview]
    n: i64

struct Table:
    names: mutable darray[sview] = []

def keep_first(table: mutable Table&, w: Wrap&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.names.push(w.names[0])

def wrap_bytes(text: dstr&) -> Wrap:
    can Memory.Allocate:
        return Wrap{names: [text.as_sview()], n: 1}

def wrap_copy(w: Wrap&) -> Wrap:
    can Memory.Allocate, Abort.Panic:
        return Wrap{names: [w.names[0]], n: 1}

def run(table: mutable Table&, other: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        text: dstr = [65, 66]
        w3: Wrap = wrap_bytes(&text)
        keep_first(table, &w3)
        inner: Wrap = Wrap{names: [text.as_sview()], n: 1}
        w4: Wrap = wrap_copy(&inner)
        keep_first(table, &w4)
`)
	errs = result.Errors()
	if len(errs) != 2 || !strings.Contains(errs[0], ":24:") || !strings.Contains(errs[1], ":27:") {
		t.Fatalf("call results carrying a local dstr's view must stay rejected (lines 24, 27), got:\n%s", strings.Join(errs, "\n"))
	}
}
