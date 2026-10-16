package semantic

import (
	"strings"
	"testing"
)

const storeScanCallStore = "call may store a value in region"

// docs/128: the store scan judges a callee body that may not be typed yet. A value whose DECLARED
// type is program-lived (an element of a `darray[sview @source]` parameter row) reaches only
// program-region storage, so pushing it into the caller's table stores nothing short-lived.
// Before, the untyped fallback only recognised region-free types and the call was rejected.
func TestStoreScanDeclaredProgramLivedElementStoresNothing(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "program_lived_elem_ok.elisa", `global region source

struct Pos:
    line: u32

enum Node:
    Leaf(sview @source, Pos)
    Empty

struct Diag:
    line: u32 = 0
    kind: sview = ""

struct Tab:
    diag: Diag
    labels: mutable darray[sview @source] = []
    ambient: mutable darray[sview @source] = []

struct Holder:
    items: mutable darray[u8]
    node: Node
    labels: mutable darray[sview @source] = []

def caller(t: mutable Tab&) -> void:
    can Memory.Allocate, Abort.Panic:
        h: Holder = Holder{items: [], node: Node.Empty}
        note_label(t, h)

def note_line(t: mutable Tab&, h: Holder&) -> void:
    match h.node:
        Node.Leaf(name, at):
            t.diag.line <- at.line
        _:
            pass

def note_kind(t: mutable Tab&, h: Holder&) -> void:
    t.diag.kind <- "empty" if h.items.count == 0 else "full"

def note_label(t: mutable Tab&, h: Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        t.ambient <- t.ambient.push(h.labels[0])

def main() -> i32:
    return 0
`)
	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, storeScanCallStore) {
		t.Fatalf("program-lived element push was treated as a short-lived store:\n%s", all)
	}
}

// Negatives: text owned by a function-local buffer is still rejected when a callee stores it
// into a longer-lived container, a container of plain views, or an @source row.
func TestStoreScanLocalBufferTextStillRejected(t *testing.T) {
	cases := map[string]string{
		"view_into_plain_row": `global region source

struct Tab:
    ambient: mutable darray[sview] = []

struct Holder:
    items: mutable darray[u8]
    labels: mutable darray[sview] = []

def caller(t: mutable Tab&) -> void:
    can Memory.Allocate, Abort.Panic:
        h: Holder = Holder{items: [65.u8()]}
        note_label(t, h)

def note_label(t: mutable Tab&, h: Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        t.ambient <- t.ambient.push(h.items.as_sview())

def main() -> i32:
    return 0
`,
		"plain_row_element": `global region source

struct Tab:
    ambient: mutable darray[sview] = []

struct Holder:
    items: mutable darray[u8]
    labels: mutable darray[sview] = []

def caller(t: mutable Tab&) -> void:
    can Memory.Allocate, Abort.Panic:
        h: mutable Holder = Holder{items: [65.u8()]}
        h.labels.push(h.items.as_sview())
        note_label(t, h)

def note_label(t: mutable Tab&, h: Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        t.ambient <- t.ambient.push(h.labels[0])

def main() -> i32:
    return 0
`,
		"view_into_source_row": `global region source

struct Tab:
    ambient: mutable darray[sview @source] = []

struct Holder:
    items: mutable darray[u8]
    labels: mutable darray[sview] = []

def caller(t: mutable Tab&) -> void:
    can Memory.Allocate, Abort.Panic:
        h: Holder = Holder{items: [65.u8()]}
        note_label(t, h)

def note_label(t: mutable Tab&, h: Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        t.ambient <- t.ambient.push(h.items.as_sview())

def main() -> i32:
    return 0
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, name+".elisa", src)
			all := strings.Join(result.Errors(), "\n")
			if !strings.Contains(all, storeScanCallStore) {
				t.Fatalf("expected the local-buffer store to be rejected, got:\n%s", all)
			}
		})
	}
}
