package semantic

import (
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

const storeScanTernarySource = `struct Table:
    names: mutable darray[sview] = []
    lists: mutable darray[darray[sview]] = []

struct HeldNames:
    names: mutable darray[sview] = []

def choose_view(table: mutable Table&, names: darray[sview]&, flag: bool) -> void:
    can Memory.Allocate, Abort.Panic:
        table.names <- table.names.push(names[0] if flag else "")

def choose_header(table: mutable Table&, names: darray[sview]&, flag: bool) -> void:
    can Memory.Allocate:
        table.lists <- table.lists.push(names if flag else [])

def choose_header_alt(table: mutable Table&, names: darray[sview]&, flag: bool) -> void:
    can Memory.Allocate:
        table.lists <- table.lists.push([] if flag else names)

def write_condition(table: mutable Table&, names: darray[sview]&) -> bool:
    can Memory.Allocate:
        table.lists <- table.lists.push(names)
        return true

def condition_effect(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate:
        table.names <- table.names.push("x" if write_condition(table, names) else "")

def choose_condition(table: mutable Table&, names: darray[sview]&, left: darray[sview]&, right: darray[sview]&) -> void:
    can Memory.Allocate:
        table.lists <- table.lists.push(left if names.count > 0 else right)

def choose_nested(table: mutable Table&, names: darray[sview]&, flag: bool) -> void:
    can Memory.Allocate, Abort.Panic:
        table.names <- table.names.push((names[0] if flag else "a") if names.count > 0 else "")

def choose_literal_condition(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate:
        choice: sview = "yes" if names.count > 0 else "no"
        table.names <- table.names.push(choice)

def choose_direct_literal_condition(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate:
        table.names <- table.names.push(("yes" if names.count > 0 else "no"))

def choose_local_view(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        choice: sview = names[0] if names.count > 0 else ""
        table.names <- table.names.push(choice)

def choose_local_header(table: mutable Table&, names: darray[sview]&, flag: bool) -> void:
    can Memory.Allocate:
        choice: darray[sview] = [] if flag else names
        table.lists <- table.lists.push(choice)

def condition_local_effect(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate:
        choice: sview = "yes" if write_condition(table, names) else "no"
        table.names <- table.names.push(choice)

def capture_condition(holder: mutable HeldNames&, names: darray[sview]&) -> bool:
    holder.names <- names
    return true

def condition_holder_effect(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate:
        holder: mutable HeldNames = HeldNames{names: []}
        choice: sview = "yes" if capture_condition(holder, names) else "no"
        table.names <- table.names.push(choice)
        table.lists <- table.lists.push(holder.names)
`

// The directed store scan must distinguish control dependence from stored data,
// including when the selected value first passes through a local. Calls made by
// the condition are still analyzed for their independent retention effects.
func TestStoreScanConditionalValueStores(t *testing.T) {
	for _, untyped := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			want storeMode
		}{
			{"choose_literal_condition", storeNone},
			{"choose_direct_literal_condition", storeNone},
			{"choose_view", storeViews},
			{"choose_local_view", storeViews},
			{"choose_nested", storeViews},
			{"choose_header", storeHeader},
			{"choose_header_alt", storeHeader},
			{"choose_local_header", storeHeader},
			{"condition_effect", storeHeader},
			{"condition_local_effect", storeHeader},
			{"condition_holder_effect", storeHeader},
		} {
			label := tc.name
			if untyped {
				label += "/untyped"
			}
			t.Run(label, func(t *testing.T) {
				result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_ternary.elisa", storeScanTernarySource, AnalyzeOptions{})
				if untyped {
					storeScanUntyped(result)
				}
				a := result.analyzer
				a.currentScope = a.globalScope
				a.storeScanCache = nil
				fn := storeScanFindFunc(t, result, tc.name)
				if got := a.calleeStoreModeInto(fn, 1, 0); got != tc.want {
					t.Errorf("store mode from names into table = %v, want %v", got, tc.want)
				}
			})
		}
	}
}

// The sharing summary can run before the callee body is typed. A conditional
// stores one result branch; its condition does not become part of that value.
func TestStoreScanUntypedConditionalSharing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		share bool
	}{
		{"choose_view", false},
		{"choose_nested", false},
		{"choose_condition", false},
		{"choose_header", true},
		{"choose_header_alt", true},
		{"condition_effect", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_ternary.elisa", storeScanTernarySource, AnalyzeOptions{})
			storeScanUntyped(result)
			a := result.analyzer
			a.currentScope = a.globalScope
			a.storeScanParamWriteCache = nil
			a.storeScanParamFactsCache = nil
			a.storeScanTaintCache = nil
			fn := storeScanFindFunc(t, result, tc.name)
			if got := a.storeScanParamReaches(storeScanShareKey(fn, 0, 1)); got != tc.share {
				t.Errorf("sharing from names into table = %v, want %v", got, tc.share)
			}
		})
	}
}

func BenchmarkStoreScanConditionalSharing(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l := lexer.New("store_ternary_bench.elisa", []byte(storeScanTernarySource))
		p := parser.New(l.Tokenize())
		file := p.ParseFile("store_ternary_bench.elisa")
		if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
			b.Fatal("invalid benchmark fixture")
		}
		result := AnalyzeWithOptions(file, AnalyzeOptions{})
		storeScanUntyped(result)
		a := result.analyzer
		a.currentScope = a.globalScope
		a.storeScanParamWriteCache = nil
		a.storeScanParamFactsCache = nil
		a.storeScanTaintCache = nil
		for fn := range a.funcDeclSymbols {
			_ = a.storeScanParamReaches(storeScanShareKey(fn, 0, 1))
		}
	}
}

func TestStoreScanConditionalReturnValue(t *testing.T) {
	source := `struct Table:
    lists: mutable darray[darray[sview]] = []
def literal(names: darray[sview]&) -> sview:
    return "yes" if names.count > 0 else "no"
def nested(names: darray[sview]&, flag: bool) -> sview:
    return ("yes" if names.count > 0 else "no") if flag else "other"
def view(names: darray[sview]&) -> sview:
    can Abort.Panic:
        return names[0] if names.count > 0 else ""
def header(names: darray[sview]&, flag: bool) -> darray[sview]:
    return [] if flag else names
def write(table: mutable Table&, names: darray[sview]&) -> bool:
    can Memory.Allocate:
        table.lists.push(names)
        return true
def condition_effect(table: mutable Table&, names: darray[sview]&) -> sview:
    return "yes" if write(table, names) else "no"
`
	for _, untyped := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			source int
			want   storeMode
		}{
			{"literal", 0, storeNone}, {"nested", 0, storeNone}, {"view", 0, storeViews},
			{"header", 0, storeHeader}, {"condition_effect", 1, storeNone},
		} {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "returned_ternary.elisa", source, AnalyzeOptions{})
			if untyped {
				storeScanUntyped(result)
			}
			a := result.analyzer
			a.currentScope = a.globalScope
			a.storeScanCache = nil
			fn := storeScanFindFunc(t, result, tc.name)
			if got := a.calleeReturnModeOf(fn, tc.source, map[storeScanKey]bool{}); got != tc.want {
				t.Errorf("%s/untyped=%v return mode = %v, want %v", tc.name, untyped, got, tc.want)
			}
			if tc.name == "condition_effect" {
				a.storeScanCache = nil
				if got := a.calleeStoreModeInto(fn, 1, 0); got != storeHeader {
					t.Errorf("condition's independent store lost: %v", got)
				}
			}
		}
	}
}

func TestStoreScanScalarReturnDoesNotRetainInput(t *testing.T) {
	source := `def count_names(names: darray[sview]&) -> usize:
    return names.count

def choose_count(names: darray[sview]&, flag: bool) -> usize:
    return names.count if flag else 0

def identity_address(value: uintptr) -> uintptr:
    return value
`
	for _, cold := range []bool{false, true} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scalar_return_store.elisa", source, AnalyzeOptions{})
		if cold {
			storeScanUntyped(result)
		}
		a := result.analyzer
		a.currentScope = a.globalScope
		a.storeScanCache = nil
		for _, name := range []string{"count_names", "choose_count"} {
			fn := storeScanFindFunc(t, result, name)
			if got := a.calleeReturnModeOf(fn, 0, map[storeScanKey]bool{}); got != storeNone {
				t.Errorf("%s/cold=%v: scalar return mode %v", name, cold, got)
			}
		}
		fn := storeScanFindFunc(t, result, "identity_address")
		if got := a.calleeReturnModeOf(fn, 0, map[storeScanKey]bool{}); got != storeHeader {
			t.Errorf("cold=%v address return lost retention: %v", cold, got)
		}

	}
}

func TestStoreScanScalarTupleBindBeforeBodyTyping(t *testing.T) {
	source := `struct Table:
    counts: mutable darray[usize] = []
    names: mutable darray[sview] = []
def probe(names: darray[sview]&) -> (slot: usize, found: bool):
    return (names.count, true)
def probe_header(names: darray[sview]&) -> (names: darray[sview], count: usize):
    return (names, names.count)
def probe_view(names: darray[sview]&) -> (name: sview, count: usize):
    can Abort.Panic:
        return (names[0], names.count)
def note(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate:
        slot, found = probe(names)
        table.counts.push(slot)
def note_view(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        name, count = probe_view(names)
        table.names.push(name)
`
	for _, cold := range []bool{false, true} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scalar_tuple_bind.elisa", source, AnalyzeOptions{})
		if cold {
			storeScanUntyped(result)
		}
		a := result.analyzer
		a.currentScope = a.globalScope
		a.storeScanCache = nil
		for _, tc := range []struct {
			name string
			want storeMode
		}{{"note", storeNone}, {"note_view", storeViews}} {
			fn := storeScanFindFunc(t, result, tc.name)
			got := a.calleeStoreModeInto(fn, 1, 0)
			if (got == storeNone) != (tc.want == storeNone) {
				t.Errorf("%s/cold=%v retention mode=%v want-none=%v", tc.name, cold, got, tc.want == storeNone)
			}
		}
		probe := storeScanFindFunc(t, result, "probe")
		if got := a.calleeReturnModeOf(probe, 0, map[storeScanKey]bool{}); got != storeNone {
			t.Errorf("cold=%v scalar tuple return retains input: %v", cold, got)
		}

		header := storeScanFindFunc(t, result, "probe_header")
		if got := a.calleeReturnModeOf(header, 0, map[storeScanKey]bool{}); got == storeNone {
			t.Errorf("cold=%v tuple container header retention lost", cold)
		}

	}
}
