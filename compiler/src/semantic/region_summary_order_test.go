package semantic

import (
	"strings"
	"testing"
)

// A function with several `return []` plus a tracked-container return still publishes its return-element summary.
func TestRegionReturnSummaryKeptAcrossEmptyReturns(t *testing.T) {
	src := `struct ST:
    names: mutable darray[sview]
    field_names: mutable darray[sview]
    field_count: mutable darray[usize]
    field_types: mutable darray[i64]
    owners: mutable darray[sview]

def labels_at(line: u32, structs: mutable ST&) -> darray[sview] can[Memory.Allocate]:
    can Memory.Allocate:
        out: mutable darray[sview] = []
        out.push(structs.names[0])
        return out

def window_of(line: u32, structs: mutable ST&) -> darray[sview] can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        all_labels: darray[sview] = labels_at(line, structs)
        if line == 0:
            return []
        window: mutable darray[sview] = []
        cursor: mutable usize = 0
        while cursor < all_labels.count |cursor, all_labels, window|:
            window.push(all_labels[cursor])
            cursor <- cursor + 1
        if window.count != 3:
            return []
        return window

def lookup(labels: darray[sview]&, structs: mutable ST&) -> i64 can[Abort.Panic]:
    return -1

def reg(line: u32, structs: mutable ST&) -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        labels: darray[sview] = window_of(line, structs)
        return if labels.count == 0
        return if lookup(labels, structs) >= 0
        element_types: mutable darray[i64] = []
        element_types.push(1)
        fi: mutable usize = 0
        while fi < labels.count |fi, labels, element_types, structs|:
            structs.field_names <- structs.field_names.push(labels[fi])
            structs.field_types <- structs.field_types.push(element_types[fi])
            fi <- fi + 1
        structs.owners <- structs.owners.push("")
        structs.field_count <- structs.field_count.push(labels.count)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSource(t, "region_summary_order.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}

// A callee declared after its caller still contributes its fill and return-element summaries at the call site.
func TestRegionSummariesForLaterDeclaredCallee(t *testing.T) {
	src := `struct ST:
    names: mutable darray[sview]
    field_names: mutable darray[sview]
    field_count: mutable darray[usize]
    tl_lines: mutable darray[u32]
    tl_names: mutable darray[sview]
    field_types: mutable darray[i64]
    owners: mutable darray[sview]

def span(e: i64) -> usize:
    return 0

def lookup(labels: darray[sview]&, structs: mutable ST&) -> i64 can[Abort.Panic]:
    return -1

def reg(line: u32, structs: mutable ST&) -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        labels: darray[sview] = window_of(line, [1], structs)
        return if labels.count == 0
        return if lookup(labels, structs) >= 0
        element_types: mutable darray[i64] = []
        element_types.push(1)
        fi: mutable usize = 0
        while fi < labels.count |fi, labels, element_types, structs|:
            structs.field_names <- structs.field_names.push(labels[fi])
            structs.field_types <- structs.field_types.push(element_types[fi])
            fi <- fi + 1
        structs.owners <- structs.owners.push("")
        structs.field_count <- structs.field_count.push(labels.count)

def labels_at(line: u32, structs: mutable ST&) -> darray[sview] can[Memory.Allocate, Abort.Panic]:
    result: mutable darray[sview] = []
    probe: mutable usize = 0
    while probe < structs.tl_lines.count |probe, line, result, structs|:
        result.push(structs.tl_names[probe]) if structs.tl_lines[probe] == line
        probe <- probe + 1
    return result


def window_of(line: u32, elements: darray[i64], structs: mutable ST&) -> darray[sview] can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        all_labels: darray[sview] = labels_at(line, structs)
        empty: mutable darray[sview] = []
        empty return if line == 0
        window: mutable darray[sview] = []
        cursor: mutable usize = 0
        for element in elements |window, cursor, all_labels|:
            break if cursor >= all_labels.count
            window.push(all_labels[cursor])
            cursor <- cursor + 1 + span(element)
        empty return if window.count != 3
        return window


def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSource(t, "region_summary_order.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}
