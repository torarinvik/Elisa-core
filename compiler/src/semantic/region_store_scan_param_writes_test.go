package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

const storeScanParamWriteSource = `struct Deep:
    lists: mutable darray[darray[sview]] = []

struct Table:
    deep: mutable Deep = Deep{}

def run(table: mutable Table&, other: mutable Table&, src: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        inner: darray[sview] = [src]
        block: Deep = Deep{lists: [inner]}
        keep_index(table, &block)
        keep_alias(table, &block)
        keep_rec(table, &block)
        keep_holder(table, &block)
        keep_read(table, &block)
        other.deep.lists.push([])

def keep_index(table: mutable Table&, block: Deep&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.deep <- merge_index(block)

def keep_alias(table: mutable Table&, block: Deep&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.deep <- merge_alias(block)

def keep_rec(table: mutable Table&, block: Deep&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.deep <- merge_rec(block)

def keep_holder(table: mutable Table&, block: Deep&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.deep <- merge_holder(block)

def keep_read(table: mutable Table&, block: Deep&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.deep <- merge_read(block)

def put_index(deep: Deep, list: darray[sview]) -> void:
    can Abort.Panic:
        deep.lists[0] <- list

def put_alias(deep: Deep, list: darray[sview]) -> void:
    can Abort.Panic:
        lists: darray[darray[sview]] = deep.lists
        lists[0] <- list

def put_rec(deep: Deep, list: darray[sview], n: i64) -> void:
    can Abort.Panic:
        if n > 0:
            put_rec(deep, list, n - 1)
        else:
            put_index(deep, list)

def stash(holder: mutable darray[darray[darray[sview]]]&, lists: darray[darray[sview]]) -> void:
    can Memory.Allocate:
        holder.push(lists)

def put_holder(deep: Deep, list: darray[sview]) -> void:
    can Memory.Allocate, Abort.Panic:
        holder: mutable darray[darray[darray[sview]]] = []
        stash(holder, deep.lists)
        holder[0][0] <- list

def count_lists(deep: Deep, list: darray[sview]) -> i64:
    return deep.lists.count.i64() + list.count.i64()

def merge_index(right: Deep&) -> Deep:
    can Memory.Allocate, Abort.Panic:
        merged: Deep = Deep{lists: [[]]}
        put_index(merged, right.lists[0])
        return merged

def merge_alias(right: Deep&) -> Deep:
    can Memory.Allocate, Abort.Panic:
        merged: Deep = Deep{lists: [[]]}
        put_alias(merged, right.lists[0])
        return merged

def merge_rec(right: Deep&) -> Deep:
    can Memory.Allocate, Abort.Panic:
        merged: Deep = Deep{lists: [[]]}
        put_rec(merged, right.lists[0], 3)
        return merged

def merge_holder(right: Deep&) -> Deep:
    can Memory.Allocate, Abort.Panic:
        merged: Deep = Deep{lists: [[]]}
        put_holder(merged, right.lists[0])
        return merged

def merge_read(right: Deep&) -> Deep:
    can Memory.Allocate, Abort.Panic:
        merged: Deep = Deep{lists: [[]]}
        n: i64 = count_lists(merged, right.lists[0])
        return merged
`

// The callee-write taint skips a resolved callee only when nothing reachable from the parameter
// writes. Every way a helper can write the caller's fresh `merged` with the block's inner list
// header must keep the merge result tainted, so each keep_* store is rejected: a direct index
// store into a by-value parameter (line 11), a store through a local alias (12), a recursive
// path ending at a writer (13), a holder local the helper stashes the parameter's storage in and
// then writes through (14). A helper that only reads its parameter (keep_read, 15) is skipped.
func TestStoreScanParamWriteReachability(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_scan_param_writes.elisa", storeScanParamWriteSource)
	errs := result.Errors()
	want := []string{":11:", ":12:", ":13:", ":14:"}
	if len(errs) != len(want) {
		t.Fatalf("expected lines 11-14 rejected (and 15 accepted), got:\n%s", strings.Join(errs, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(errs[i], w) {
			t.Fatalf("expected lines 11-14 rejected (and 15 accepted), got:\n%s", strings.Join(errs, "\n"))
		}
	}
}

// A holder local that keeps a target's inner lists header (through a callee, or an inline push)
// and is then written through (`holder[0][0] <- list`) stores the caller's short-lived list into
// the target. Both shapes ran as use-after-free before (accepted, "dangling holder list").
func TestStoreScanHolderWriteThroughRejected(t *testing.T) {
	for name, src := range map[string]string{"callee": storeScanHolderCalleeSource, "inline": storeScanHolderInlineSource} {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_scan_holder_"+name+".elisa", src)
		errs := result.Errors()
		if len(errs) != 1 || !strings.Contains(errs[0], ":22:") {
			t.Fatalf("%s: expected the put_holder call (line 22) rejected, got:\n%s", name, strings.Join(errs, "\n"))
		}
	}
}

const storeScanHolderCalleeSource = `struct Deep:
    lists: mutable darray[darray[sview]] = []

struct Table:
    deep: mutable Deep = Deep{}

def stash(holder: mutable darray[darray[darray[sview]]]&, lists: darray[darray[sview]]) -> void:
    can Memory.Allocate:
        holder.push(lists)

def put_holder(table: mutable Table&, list: darray[sview]) -> void:
    can Memory.Allocate, Abort.Panic:
        holder: mutable darray[darray[darray[sview]]] = []
        stash(holder, table.deep.lists)
        holder[0][0] <- list

def run(table: mutable Table&, other: mutable Table&, src: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        inner: mutable darray[sview] = []
        for i in 0..<64 |inner, src|:
            inner.push(src)
        put_holder(table, inner)
        other.deep.lists.push([])
`

const storeScanHolderInlineSource = `struct Deep:
    lists: mutable darray[darray[sview]] = []

struct Table:
    deep: mutable Deep = Deep{}

def stash(holder: mutable darray[darray[darray[sview]]]&, lists: darray[darray[sview]]) -> void:
    can Memory.Allocate:
        holder.push(lists)

def put_holder(table: mutable Table&, list: darray[sview]) -> void:
    can Memory.Allocate, Abort.Panic:
        holder: mutable darray[darray[darray[sview]]] = []
        holder.push(table.deep.lists)
        holder[0][0] <- list

def run(table: mutable Table&, other: mutable Table&, src: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        inner: mutable darray[sview] = []
        for i in 0..<64 |inner, src|:
            inner.push(src)
        put_holder(table, inner)
        other.deep.lists.push([])
`

// A loop's capture list (`while c |result, table|:`) names existing bindings; it binds none.
// Treating the captures as binders of the header made every captured local an alias of whatever
// the condition read, so a helper that only reads its table looked like it stored into it.
func TestStoreScanLoopCapturesBindNothing(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_scan_captures.elisa", `struct Table:
    names: mutable darray[sview] = []
    lines: mutable darray[u32] = []

def labels_at(line: u32, table: mutable Table&) -> darray[sview]:
    can Memory.Allocate, Abort.Panic:
        result: mutable darray[sview] = []
        probe: mutable usize = 0
        while probe < table.names.count |probe, line, result, table|:
            result.push(table.names[probe]) if table.lines[probe] == line
            probe <- probe + 1
        return result
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	f := storeScanFindFunc(t, result, "labels_at")
	a := result.analyzer
	a.currentScope = a.globalScope
	loops := 0
	for _, node := range a.storeScanIndex(f).flat {
		if loop, isWhile := node.(*ast.WhileStmt); isWhile {
			loops++
			if names := stmtBoundNames(loop); len(names) != 0 {
				t.Fatalf("a while loop binds nothing, got %v", names)
			}
		}
	}
	if loops != 1 {
		t.Fatalf("expected one while loop, found %d", loops)
	}
	alias, _ := a.storeScanAliasClosure(f, a.storeScanIndex(f), map[string]bool{"table": true})
	if alias["result"] || alias["probe"] || alias["line"] {
		t.Fatalf("captured locals became aliases of the table: %v", alias)
	}
}
