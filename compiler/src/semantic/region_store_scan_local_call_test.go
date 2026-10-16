package semantic

import "testing"

const storeScanLocalCallSource = `global region source

struct Table:
    names: mutable darray[sview] = []
    lists: mutable darray[darray[sview]] = []

def source_first(names: darray[sview @source]&) -> sview can[Abort.Panic]:
    return names[0]

def first(names: darray[sview]&) -> sview can[Abort.Panic]:
    return names[0]

def identity(names: darray[sview]&) -> darray[sview]&:
    return names

def fixed(names: darray[sview]&) -> sview:
    return "fixed"

def recursive_header(names: darray[sview]&, flag: bool) -> darray[sview]&:
    return names if flag
    return recursive_header(names, true)

def store_and_return(table: mutable Table&, names: darray[sview]&) -> sview:
    can Memory.Allocate:
        table.lists.push(names)
        return "fixed"

def publish_source(table: mutable Table&, names: darray[sview @source]&) -> void:
    can Memory.Allocate, Abort.Panic:
        selected: sview = source_first(names)
        table.names.push(selected)

def publish_plain(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        selected: sview = first(names)
        table.names.push(selected)

def publish_header(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate:
        selected: darray[sview]& = identity(names)
        table.lists.push(selected)

def publish_fixed(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate:
        selected: sview = fixed(names)
        table.names.push(selected)

def publish_recursive_header(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate:
        selected: darray[sview]& = recursive_header(names, false)
        table.lists.push(selected)

def publish_side_effect(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate:
        selected: sview = store_and_return(table, names)
        table.names.push(selected)
`

func TestStoreScanLocalCallResultProvenance(t *testing.T) {
	for _, untyped := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			want storeMode
		}{
			{"publish_source", storeNone},
			{"publish_plain", storeViews},
			{"publish_header", storeHeader},
			{"publish_fixed", storeNone},
			{"publish_recursive_header", storeHeader},
			{"publish_side_effect", storeHeader},
		} {
			label := tc.name
			if untyped {
				label += "/untyped"
			}
			t.Run(label, func(t *testing.T) {
				result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "local_call_store.elisa", storeScanLocalCallSource, AnalyzeOptions{})
				if untyped {
					storeScanUntyped(result)
				}
				a := result.analyzer
				a.currentScope = a.globalScope
				a.storeScanCache = nil
				if got := a.calleeStoreModeInto(storeScanFindFunc(t, result, tc.name), 1, 0); got != tc.want {
					t.Errorf("store mode = %v, want %v", got, tc.want)
				}
			})
		}
	}
}
