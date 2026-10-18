package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
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
// (lines 19, 21 accepted; 27 too, its mutable local holding only a param view). A view of a
// local dstr in it (line 25), and call results carrying a local dstr's view directly or through a copy (second
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
	// Line 27's mutable local holds only a view of the param src (localViewProvenance).
	if len(errs) != 1 || !strings.Contains(errs[0], ":25:") {
		t.Fatalf("expected exactly line 25 rejected, got:\n%s", strings.Join(errs, "\n"))
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

func TestStoreViewsUsesOnlyStoredProgramLivedFieldPath(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_views_field_path.elisa", `global region source
struct Entry:
    safe: sview @source
    unsafe: sview

struct ScopeLike:
    entries: darray[Entry]

struct Table:
    names: mutable darray[sview] = []

def keep_safe(table: mutable Table&, state: ScopeLike&) -> void:
    can Memory.Allocate, Abort.Panic:
        text: sview = state.entries[0].safe
        table.names.push(text)

def keep_unannotated(table: mutable Table&, state: ScopeLike&) -> void:
    can Memory.Allocate, Abort.Panic:
        text: sview = state.entries[0].unsafe
        table.names.push(text)

def keep_mutable(table: mutable Table&, state: ScopeLike&) -> void:
    can Memory.Allocate, Abort.Panic:
        text: mutable sview = state.entries[0].safe
        text <- state.entries[0].unsafe
        table.names.push(text)

def keep_mixed(table: mutable Table&, state: ScopeLike&) -> void:
    can Memory.Allocate, Abort.Panic:
        safe_text: sview = state.entries[0].safe
        unsafe_text: sview = state.entries[0].unsafe
        table.names.push(safe_text)
        table.names.push(unsafe_text)

def put_view(table: mutable Table&, text: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        table.names.push(text)

def keep_via_helper(table: mutable Table&, state: ScopeLike&) -> void:
    can Memory.Allocate, Abort.Panic:
        text: sview = state.entries[0].unsafe
        put_view(table, text)

def select_entry(state: ScopeLike&) -> Entry:
    return state.entries[0]

def keep_unknown(table: mutable Table&, state: ScopeLike&) -> void:
    can Memory.Allocate, Abort.Panic:
        text: sview = select_entry(state).safe
        table.names.push(text)

`, AnalyzeOptions{})
	for _, tc := range []struct {
		name           string
		wantStoreViews bool
		want           bool
	}{
		{name: "keep_safe", wantStoreViews: true, want: true},
		{name: "keep_unannotated", wantStoreViews: true, want: false},
		{name: "keep_mutable", wantStoreViews: true, want: false},
		{name: "keep_mixed", wantStoreViews: true, want: false},
		{name: "keep_via_helper", wantStoreViews: true, want: false},
		{name: "keep_unknown", wantStoreViews: true, want: false},
	} {
		decl := storeScanFindFunc(t, result, tc.name)
		for _, node := range result.analyzer.storeScanIndex(decl).flat {
			switch n := node.(type) {
			case *ast.VarDeclStmt:
				if n != nil && n.Name == "text" {
					delete(result.analyzer.exprTypes, n.Value)
				}
			case *ast.CallExpr:
				if n != nil && len(n.Args) > 0 {
					delete(result.analyzer.exprTypes, n.Args[0])
				}
			}
		}
		if got := result.analyzer.calleeStoreModeInto(decl, 1, 0); (got == storeViews) != tc.wantStoreViews {
			t.Fatalf("%s: store mode %d, wantStoreViews=%v", tc.name, got, tc.wantStoreViews)
		}
		got := result.analyzer.calleeStoresProgramLivedViews(decl, 1, 0, map[storeScanKey]bool{})
		if got != tc.want {
			t.Errorf("%s: program-lived stored views = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestStoreElementsUsesOnlyStoredProgramLivedPayload(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_elements_payload.elisa", `global region source
struct SafeEntry:
    text: sview @source

struct UnsafeEntry:
    text: sview

struct Source:
    safe: darray[SafeEntry]
    unsafe: darray[UnsafeEntry]

struct Table:
    entries: mutable darray[SafeEntry] = []

struct DefaultPayload:
    text: sview @source
    address: uintptr = 0

struct DefaultSource:
    text: sview @source

struct DefaultTable:
    entries: mutable darray[DefaultPayload] = []

def keep_safe(table: mutable Table&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.entries.push(state.safe[0])

def keep_unsafe(table: mutable Table&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.entries.push(state.unsafe[0])

def keep_with_omitted_address(table: mutable DefaultTable&, state: DefaultSource&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.entries.push(DefaultPayload{text: state.text})

`, AnalyzeOptions{})
	for _, tc := range []struct {
		name string
		want bool
	}{{"keep_safe", true}, {"keep_unsafe", false}, {"keep_with_omitted_address", false}} {
		decl := storeScanFindFunc(t, result, tc.name)
		got := result.analyzer.calleeStoresProgramLivedElements(decl, 1, 0, map[storeScanKey]bool{})
		if got != tc.want {
			t.Errorf("%s: program-lived stored payload = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestStorePayloadAddressIntegersFailClosed(t *testing.T) {
	a := &Analyzer{}
	if a.storeScanTypeMayEncodeAddress(&BuiltinType{Name: "i64"}, map[Type]bool{}) {
		t.Fatal("ordinary integer payload must not be treated as an address")
	}
	if !a.storeScanTypeMayEncodeAddress(&BuiltinType{Name: "uintptr"}, map[Type]bool{}) {
		t.Fatal("uintptr payload may encode an address")
	}
	withAddress := &StructType{Fields: map[string]Field{"handle": {Name: "handle", Type: &BuiltinType{Name: "uintptr"}}}}
	if !a.storeScanTypeMayEncodeAddress(withAddress, map[Type]bool{}) {
		t.Fatal("aggregate containing uintptr may encode an address")
	}
	if !a.storeScanTypeMayEncodeAddress(&DArrayType{Elem: &BuiltinType{Name: "uintptr"}}, map[Type]bool{}) {
		t.Fatal("container wrapping uintptr may encode an address")
	}
	if !a.storeScanTypeMayEncodeAddress(&RefType{Elem: withAddress}, map[Type]bool{}) {
		t.Fatal("reference wrapping uintptr may encode an address")
	}
}

func TestStoreElementsMixedViewAndAddressHelperFailsClosed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_elements_mixed_address.elisa", `global region source
struct Payload:
    text: sview @source
    address: uintptr

struct Source:
    values: darray[Payload]

struct Table:
    views: mutable darray[sview] = []
    addresses: mutable darray[uintptr] = []

def record_address(table: mutable Table&, address: uintptr) -> void:
    can Memory.Allocate, Abort.Panic:
        table.addresses.push(address)

def inspect_address(address: uintptr) -> bool:
    return address != 0

def keep_safe_with_address_read(table: mutable Table&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        value: Payload = state.values[0]
        table.views.push(value.text)
        _ = inspect_address(value.address)

def keep_mixed(table: mutable Table&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        value: Payload = state.values[0]
        table.views.push(value.text)
        record_address(table, value.address)

`, AnalyzeOptions{})
	for _, tc := range []struct {
		name string
		want bool
	}{{"keep_mixed", false}, {"keep_safe_with_address_read", true}} {
		decl := storeScanFindFunc(t, result, tc.name)
		got := result.analyzer.calleeStoresProgramLivedElements(decl, 1, 0, map[storeScanKey]bool{})
		if got != tc.want {
			t.Errorf("%s: program-lived stored payload proof = %v, want %v", tc.name, got, tc.want)
		}
	}
}
