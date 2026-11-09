package semantic

import (
	"strings"
	"testing"
)

// A builtin darray `extend(xs)` copies xs's elements into the receiver and never retains xs's
// header, so a callee that extends a target field from a grown local's darray stores no local
// backing (accepted); pushing the darray itself as an element does retain it (rejected).
func TestStoreScanExtendCopiesElementsNotHeader(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_scan_extend.elisa", `struct Path:
    names: mutable darray[i64] = []
    nested: mutable darray[darray[i64]] = []

struct Log:
    paths: mutable darray[Path] = []

def absorb(path: mutable Path&, xs: darray[i64]&) -> void:
    can Memory.Allocate:
        path.names.extend(xs)

def keep(path: mutable Path&, xs: darray[i64]) -> void:
    can Memory.Allocate:
        path.nested.push(xs)

def record(log: mutable Log&, xs: darray[i64]&) -> void:
    can Memory.Allocate:
        p: mutable Path = Path{}
        p.names.extend(xs)
        log.paths.push(p)

def fill(path: mutable Path&, log: mutable Log&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        for i in 0..<10 |local|:
            local.push(i.i64())
        absorb(path, &local)
        record(log, &local)
        keep(path, local)
`)
	errs := result.Errors()
	// record's own local `p` (line 20) is built in record's ambient region, adopted into `log`'s
	// (its sole writable target). The record CALL (line 28) must not add a store of `local`'s
	// header: extend copied only its elements.
	if len(errs) != 1 || !strings.Contains(errs[0], ":29:") {
		t.Fatalf("expected only line 29 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
}

// The extend rule must not depend on analysis order: a callee declared AFTER its caller is
// scanned before its body has types, so the receiver is recognized from its declared
// `darray[..]` syntax. (A receiver also bound by a pattern/loop binder of the same name stays
// conservative.)
func TestStoreScanExtendRecognizedBeforeCalleeAnalyzed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_scan_extend_order.elisa", `struct Table:
    texts: mutable darray[darray[u8]] = []

def run(table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable dstr = []
        local.push(65)
        parent_text(local.as_sview(), table)

def parent_text(text: sview, table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        bytes: mutable darray[u8] = []
        bytes.extend(text)
        table.texts.push(bytes)
`)
	for _, e := range result.Errors() {
		if strings.Contains(e, ":8:") {
			t.Fatalf("the extend-copied view must not be reported as stored at the call, got:\n%s", strings.Join(result.Errors(), "\n"))
		}
	}
}

// Adoption covers only what the function allocates: a merge that may RETURN one of its arguments
// stores that argument's storage into the target, so a caller passing a local alternate (built in
// its own region) must still be rejected.
func TestMergeReturningArgumentFromLocalRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "merge_returns_argument.elisa", `struct Snap:
    names: mutable darray[i64] = []

struct Flow:
    normal: mutable Snap = Snap{}

def pick(a: Snap, b: Snap) -> Snap:
    return b

def merge_normal(flow: mutable Flow&, alternate: Snap) -> void:
    flow.normal <- pick(flow.normal, alternate)

def add_round(flow: mutable Flow&) -> void:
    can Memory.Allocate, Abort.Panic:
        alt: mutable Snap = Snap{}
        alt.names.push(1)
        merge_normal(flow, alt)
`)
	errs := result.Errors()
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), ":17:") {
		t.Fatalf("passing a local into a merge that may return it must be rejected at line 17, got:\n%s", strings.Join(errs, "\n"))
	}
}

// A view-typed value derived from a NON-BYTE darray source (`n: sview = pick(names)`) cannot be or
// view that darray's backing, so a callee storing it stores an element value, not the header: the
// caller then judges the argument by its tracked element provenance. Elements that view the
// caller's own bytes still escape (line 30), and a BYTE darray source viewed with as_sview is the
// header (line 31).
func TestStoreScanViewFromNonByteDArrayIsElement(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_scan_view_element.elisa", `struct Table:
    names: mutable darray[sview] = []

def pick(names: darray[sview]&) -> sview:
    can Abort.Panic:
        return names[0] if names.count > 0 else ""

def note(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        n: sview = pick(names)
        table.names.push(n)

def note_bytes(table: mutable Table&, bytes: darray[u8]&) -> void:
    can Memory.Allocate:
        n: sview = bytes.as_sview()
        table.names.push(n)

def run(table: mutable Table&, src: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        safe: mutable darray[sview] = []
        safe.push(src)
        safe.push("lit")
        note(table, &safe)
        text: mutable dstr = []
        text.push(65)
        unsafe_names: mutable darray[sview] = []
        unsafe_names.push(text.as_sview())
        bytes: mutable darray[u8] = []
        bytes.push(66)
        note(table, &unsafe_names)
        note_bytes(table, &bytes)
`)
	errs := result.Errors()
	joined := strings.Join(errs, "\n")
	if len(errs) != 2 || !strings.Contains(errs[0], ":30:") || !strings.Contains(errs[1], ":31:") {
		t.Fatalf("expected exactly lines 30 and 31 rejected, got:\n%s", joined)
	}
}

// The same for a local whose type is built only from scalars and plain views (a struct of sview +
// i64): it cannot hold a non-byte darray's header either, so storing it stores element values.
func TestStoreScanViewOnlyStructFromNonByteDArrayIsElement(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_scan_viewonly_struct.elisa", `struct Info:
    name: sview
    n: i64

struct Table:
    infos: mutable darray[Info] = []

def describe(names: darray[sview]&) -> Info:
    can Abort.Panic:
        return Info{name: names[0] if names.count > 0 else "", n: names.count.i64()}

def note(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        info: Info = describe(names)
        table.infos.push(info)

def run(table: mutable Table&, src: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        safe: mutable darray[sview] = []
        safe.push(src)
        note(table, &safe)
        text: mutable dstr = []
        text.push(65)
        unsafe_names: mutable darray[sview] = []
        unsafe_names.push(text.as_sview())
        note(table, &unsafe_names)
`)
	errs := result.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0], ":26:") {
		t.Fatalf("expected only the unsafe call (line 26) rejected, got:\n%s", strings.Join(errs, "\n"))
	}
}

// The non-byte rule uses the callee's RESOLVED source type: a darray of byte-free structs
// (`darray[Entry]`, Entry = sview + i64) qualifies, so a view picked out of it is an element value.
// An element struct holding a dstr has bytes a view could point into, so the same shape over
// `darray[Owned]` keeps the header verdict and the call with a local argument is rejected.
func TestStoreScanByteFreeStructDArraySourceIsElement(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_scan_bytefree_struct.elisa", `struct Entry:
    name: sview
    n: i64

struct Owned:
    text: dstr
    n: i64

struct Table:
    names: mutable darray[sview] = []

def first_entry(entries: darray[Entry]&) -> sview:
    can Abort.Panic:
        return entries[0].name if entries.count > 0 else ""

def first_owned(owned: darray[Owned]&) -> sview:
    can Abort.Panic:
        return owned[0].text.as_sview() if owned.count > 0 else ""

def note(table: mutable Table&, entries: darray[Entry]&) -> void:
    can Memory.Allocate, Abort.Panic:
        n: sview = first_entry(entries)
        table.names.push(n)

def note_owned(table: mutable Table&, owned: darray[Owned]&) -> void:
    can Memory.Allocate, Abort.Panic:
        n: sview = first_owned(owned)
        table.names.push(n)

def run(table: mutable Table&, src: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        entries: mutable darray[Entry] = []
        entries.push(Entry{name: src, n: 1})
        note(table, &entries)
        owned: mutable darray[Owned] = []
        owned.push(Owned{text: [65], n: 1})
        note_owned(table, &owned)
`)
	errs := result.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0], ":37:") {
		t.Fatalf("expected only the note_owned call (line 37) rejected, got:\n%s", strings.Join(errs, "\n"))
	}
}

// SOUNDNESS: an element-mode store out of a STRUCT argument was never checked: the call site asked
// containerMayHoldRegionRefs(Wrap), which only answers for containers (false for a struct), and
// skipped the argument. A view into a local dstr carried in an immutable struct local was stored
// into the caller's table and read freed bytes at runtime (two writable targets, so no adoption).
func TestElementStoreOutOfStructArgumentIsChecked(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "element_store_struct_arg.elisa", `struct Wrap:
    names: darray[sview]
    n: i64

struct Table:
    names: mutable darray[sview] = []

def keep_first(table: mutable Table&, w: Wrap&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.names.push(w.names[0])

def run(table: mutable Table&, other: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        text: mutable dstr = []
        text.push(65)
        bad: Wrap = Wrap{names: [text.as_sview()], n: 1}
        keep_first(table, &bad)
`)
	errs := result.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0], ":17:") || !strings.Contains(errs[0], "longer-lived storage") {
		t.Fatalf("storing a view of a local dstr carried in a struct argument must be rejected (line 17), got:\n%s", strings.Join(errs, "\n"))
	}
}
