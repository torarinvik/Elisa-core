package semantic

import (
	"strings"
	"testing"
)

// A local that starts empty and is only grown by push/extend of view-only or scalar values
// (snap_copy's `copy`) holds, from a byte-free source, only views: the copy stored by add_return
// is a views store, and merging an immutable literal flow is accepted (line 48). deep_copy extends
// darray[darray[sview]] -- whole inner containers -- so merging the deep flow stays rejected (50).
func TestStoreScanViewFilledLocalIsViews(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_scan_view_filled.elisa", `struct Snap:
    slots: mutable darray[usize] = []
    names: mutable darray[sview] = []

struct DeepSnap:
    lists: mutable darray[darray[sview]] = []

struct Flow:
    seen: mutable bool = false
    returned: mutable Snap = Snap{}

struct DeepFlow:
    returned: mutable DeepSnap = DeepSnap{}

def snap_copy(state: Snap&) -> Snap:
    can Memory.Allocate:
        copy: mutable Snap = Snap{}
        copy.slots.extend(state.slots)
        copy.names.extend(state.names)
        return copy

def deep_copy(state: DeepSnap&) -> DeepSnap:
    can Memory.Allocate:
        copy: mutable DeepSnap = DeepSnap{}
        copy.lists.extend(state.lists)
        return copy

def add_return(flow: mutable Flow&, state: Snap) -> void:
    can Memory.Allocate:
        flow.returned <- snap_copy(state)
        flow.seen <- true

def add_deep(flow: mutable DeepFlow&, state: DeepSnap) -> void:
    can Memory.Allocate:
        flow.returned <- deep_copy(state)

def merge_exits(flow: mutable Flow&, alternate: Flow&) -> void:
    can Memory.Allocate:
        add_return(flow, alternate.returned) if alternate.seen

def merge_deep(flow: mutable DeepFlow&, alternate: DeepFlow&) -> void:
    can Memory.Allocate:
        add_deep(flow, alternate.returned)

def run(flow: mutable Flow&, deep: mutable DeepFlow&, other: mutable Flow&, src: sview, names: darray[sview]) -> void:
    can Memory.Allocate, Abort.Panic:
        block_flow: Flow = Flow{seen: true, returned: Snap{names: [src]}}
        merge_exits(flow, &block_flow)
        block_deep: DeepFlow = DeepFlow{returned: DeepSnap{lists: [names]}}
        merge_deep(deep, &block_deep)
        other.seen <- true
`)
	errs := result.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0], ":50:") {
		t.Fatalf("expected exactly line 50 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
	// The same program with the caller first: the copy's fields are typed from its literal
	// declaration before snap_copy is analyzed; only the deep merge (line 20) is rejected.
	result = analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_scan_view_filled_caller_first.elisa", `struct Snap:
    slots: mutable darray[usize] = []
    names: mutable darray[sview] = []

struct DeepSnap:
    lists: mutable darray[darray[sview]] = []

struct Flow:
    seen: mutable bool = false
    returned: mutable Snap = Snap{}

struct DeepFlow:
    returned: mutable DeepSnap = DeepSnap{}

def run(flow: mutable Flow&, deep: mutable DeepFlow&, other: mutable Flow&, src: sview, names: darray[sview]) -> void:
    can Memory.Allocate, Abort.Panic:
        block_flow: Flow = Flow{seen: true, returned: Snap{names: [src]}}
        merge_exits(flow, &block_flow)
        block_deep: DeepFlow = DeepFlow{returned: DeepSnap{lists: [names]}}
        merge_deep(deep, &block_deep)
        other.seen <- true

def snap_copy(state: Snap&) -> Snap:
    can Memory.Allocate:
        copy: mutable Snap = Snap{}
        copy.slots.extend(state.slots)
        copy.names.extend(state.names)
        return copy

def deep_copy(state: DeepSnap&) -> DeepSnap:
    can Memory.Allocate:
        copy: mutable DeepSnap = DeepSnap{}
        copy.lists.extend(state.lists)
        return copy

def add_return(flow: mutable Flow&, state: Snap) -> void:
    can Memory.Allocate:
        flow.returned <- snap_copy(state)
        flow.seen <- true

def add_deep(flow: mutable DeepFlow&, state: DeepSnap) -> void:
    can Memory.Allocate:
        flow.returned <- deep_copy(state)

def merge_exits(flow: mutable Flow&, alternate: Flow&) -> void:
    can Memory.Allocate:
        add_return(flow, alternate.returned) if alternate.seen

def merge_deep(flow: mutable DeepFlow&, alternate: DeepFlow&) -> void:
    can Memory.Allocate:
        add_deep(flow, alternate.returned)

`)
	errs = result.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0], ":20:") {
		t.Fatalf("caller-first: expected exactly line 20 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
}
