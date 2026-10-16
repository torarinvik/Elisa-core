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
