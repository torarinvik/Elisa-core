package semantic

import "testing"

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
