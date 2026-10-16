package semantic

import (
	"strings"
	"testing"
)

const programLivedViewSource = `global region source

struct Errors:
    set_names: mutable darray[sview @source] = []

struct Table:
    errors: Errors

def pick(names: darray[sview @source]&) -> sview @source can[Abort.Panic]:
    return names[0]

def table_pick(table: Table&) -> sview @source can[Abort.Panic]:
    return pick(table.errors.set_names)

def grow(table: mutable Table&, extra: sview @source) -> bool can[Memory.Allocate]:
    table.errors.set_names.push(extra)
    return true

def use_after(table: mutable Table&, extra: sview @source) -> i64 can[Memory.Allocate, Abort.Panic]:
    head: HEAD_TYPE = table_pick(table)
    return 0 if not grow(table, extra)
    return head.len
`

// docs/128: a binding declared `sview @source` views program-region bytes, not the buffer of
// the darray it was read from, so a callee growing that darray through a mutable borrow does
// not make it stale. Before, the return-origin summary of table_pick tied `head` to
// table.errors.set_names and the later grow(table, ...) invalidated it.
func TestStorageViewProgramLivedBindingSurvivesCalleeGrowth(t *testing.T) {
	src := strings.Replace(programLivedViewSource, "HEAD_TYPE", "sview @source", 1)
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "program_lived_view_ok.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, "storage dependency facts were invalidated") {
		t.Fatalf("program-lived view was invalidated by callee growth:\n%s", all)
	}
}

// Negative: a plain view into a byte buffer is still invalidated when the buffer grows.
func TestStorageViewBufferViewStillInvalidatedByGrowth(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "buffer_view_bad.elisa", `global region source

def view_then_grow(buf: mutable darray[u8]&) -> i64:
    can Memory.Allocate, Abort.Panic:
        head: sview = buf.as_sview()
        buf.push(65.u8())
        return head.len
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `"head" cannot be used: storage dependency facts were invalidated`) {
		t.Fatalf("expected the buffer view to be invalidated, got:\n%s", all)
	}
}

// Negative: the same shape over a plain `darray[sview]` row (no program region anywhere)
// keeps its storage dependency, so the exemption is keyed on a program-lived type.
func TestStorageViewPlainBindingStillInvalidatedByCalleeGrowth(t *testing.T) {
	src := strings.ReplaceAll(strings.Replace(programLivedViewSource, "HEAD_TYPE", "sview", 1), "sview @source", "sview")
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "plain_view_bad.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `"head" cannot be used: storage dependency facts were invalidated by mutable borrow of table by grow`) {
		t.Fatalf("expected the plain view to be invalidated, got:\n%s", all)
	}
}
