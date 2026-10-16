package semantic

import (
	"testing"

	"elisacore/src/ast"
)

// The shapes behind stage1's codegen_stmt_blocks_exprs:142 and
// codegen_stmt_expr_calls:233. The call-site check can query a callee whose body is not typed
// yet; storeScanUntyped reproduces that by dropping the expression types before the query.
const storeScanBinderSource = `global region source

struct Pos:
    line: u32

enum Stmt:
    Block(kind: sview @source, clause: darray[sview @source] @source, at: Pos)
    Plain(kind: sview, clause: darray[sview], at: Pos)
    Nop

struct Diag:
    line: u32 = 0
    kind: sview = ""

struct Table:
    diag: Diag
    labels: mutable darray[sview @source] = []
    plain_labels: mutable darray[sview] = []

def label_of_block(table: mutable Table&, statement: Stmt) -> void:
    can Memory.Allocate, Abort.Panic:
        match statement:
            Stmt.Block(block_kind, block_clause, block_at):
                table.labels <- table.labels.push(block_clause[0]) if block_clause.count == 1
                table.diag.line <- block_at.line
            _:
                pass

def label_of_plain(table: mutable Table&, statement: Stmt) -> void:
    can Memory.Allocate, Abort.Panic:
        match statement:
            Stmt.Plain(plain_kind, plain_clause, plain_at):
                table.plain_labels <- table.plain_labels.push(plain_clause[0]) if plain_clause.count == 1
            _:
                pass
`

func storeScanUntyped(result *Result) {
	a := result.analyzer
	a.exprTypes = map[ast.Expr]Type{}
	a.storeScanCache = nil
	a.storeScanFrameAt = nil
	a.storeScanProvisionalMemo = nil
	a.storeScanViewCache = nil
	a.storeScanScalarCache = nil
	a.storeScanBinderTypes = nil
}

func storeScanModeOf(t *testing.T, untyped bool, fn string) storeMode {
	t.Helper()
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_binder.elisa", storeScanBinderSource, AnalyzeOptions{})
	if untyped {
		storeScanUntyped(result)
	}
	return result.analyzer.calleeStoreModeInto(storeScanFindFunc(t, result, fn), 1, 0)
}

// An untyped callee body types a match binder from the subject's declared enum payload: a
// `darray[sview @source] @source` clause element and a scalar Pos field store nothing
// short-lived. Before, both were element stores of the matched statement.
func TestStoreScanMatchBinderPayloadTyped(t *testing.T) {
	if got := storeScanModeOf(t, true, "label_of_block"); got != storeNone {
		t.Errorf("program-lived binder payload store mode %d, want storeNone", got)
	}
	// Negative: a plain darray[sview] payload element is still a store of the statement's views.
	if got := storeScanModeOf(t, true, "label_of_plain"); got == storeNone {
		t.Errorf("a plain-view binder payload must stay a store")
	}
}

// The check_borrow_after_move:2006 shape: a diagnostic literal built from elements of the
// callee's own parameter rows. Untyped, an element of a `darray[u32]&` or `darray[sview @source]&`
// parameter is judged by the row's declared element type.
const storeScanParamRowSource = `global region source

struct Note:
    name: sview @source
    count: u32 = 0

struct Table:
    notes: mutable darray[Note] = []
    plain: mutable darray[sview] = []

def note_rows(table: mutable Table&, kinds: darray[u32]&, details: darray[sview @source]&, slot: usize) -> void:
    can Memory.Allocate, Abort.Panic:
        table.notes.push(Note{name: details[slot], count: kinds[slot]})

def note_plain(table: mutable Table&, names: darray[sview]&, slot: usize) -> void:
    can Memory.Allocate, Abort.Panic:
        table.plain.push(names[slot])
`

func TestStoreScanParamRowElementsTyped(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_param_rows.elisa", storeScanParamRowSource, AnalyzeOptions{})
	storeScanUntyped(result)
	for _, src := range []int{1, 2} {
		if got := result.analyzer.calleeStoreModeInto(storeScanFindFunc(t, result, "note_rows"), src, 0); got != storeNone {
			t.Errorf("param %d: row element store mode %d, want storeNone", src, got)
		}
	}
	// Negative: an element of a plain darray[sview] parameter is still a store of its views.
	if got := result.analyzer.calleeStoreModeInto(storeScanFindFunc(t, result, "note_plain"), 1, 0); got == storeNone {
		t.Errorf("a plain-view row element must stay a store")
	}
}

func TestStoreScanOpaqueLoopBinderDoesNotCarrySource(t *testing.T) {
	src := `extern LLVMValueRef

def copy_handles(out: mutable darray[LLVMValueRef]&, values: darray[LLVMValueRef]&) -> void:
    for value in values:
        out.push(value)

def copy_views(out: mutable darray[sview]&, values: darray[sview]&):
    for value in values:
        out.push(value)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_opaque_loop_binder.elisa", src, AnalyzeOptions{})
	storeScanUntyped(result)
	if got := result.analyzer.calleeStoreModeInto(storeScanFindFunc(t, result, "copy_handles"), 1, 0); got != storeNone {
		t.Errorf("opaque handles copied from a source must not carry source storage, got mode %d", got)
	}
	if got := result.analyzer.calleeStoreModeInto(storeScanFindFunc(t, result, "copy_views"), 1, 0); got == storeNone {
		t.Errorf("view elements copied from a source must remain tainted")
	}
}

// The check_never_leak_push_loop:144 shape: a for-capture over a local declared
// `darray[u32]` copies a scalar each iteration, read off the declared type.
const storeScanLocalLoopSource = `struct Block:
    lines: mutable darray[u32] = []
    names: mutable darray[sview] = []
    text: mutable darray[u8] = []

struct Walk:
    clause_lines: mutable darray[u32] = []
    names: mutable darray[sview] = []

def fill_lines(name: sview, out: mutable darray[u32]&) -> bool:
    can Memory.Allocate, Abort.Panic:
        out.push(name.len.u32() + 1)
        return true

def fill_names(block: Block&, out: mutable darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        out.push(block.names[0])

def copy_lines(walk: mutable Walk&, block: Block&) -> void:
    can Memory.Allocate, Abort.Panic:
        found: mutable darray[bool] = []
        for local in 0..<block.names.count |found, block, walk|:
            found.push(false)
            name: sview = block.names[local]
            lines: mutable darray[u32] = []
            continue if not fill_lines(name, lines)
            for line in lines |walk|:
                walk.clause_lines.push(line)
            found[local] <- true

def copy_names(walk: mutable Walk&, block: Block&) -> void:
    can Memory.Allocate, Abort.Panic:
        names: mutable darray[sview] = []
        fill_names(block, names)
        for name in names |walk|:
            walk.names.push(name)
`

func TestStoreScanLocalScalarLoopCaptureTyped(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_local_loop.elisa", storeScanLocalLoopSource, AnalyzeOptions{})
	storeScanUntyped(result)
	lineType, ok := result.analyzer.storeScanLoopBinderElemType(storeScanFindFunc(t, result, "copy_lines"), &ast.Ident{Name: "line"})
	if !ok || lineType == nil || lineType.String() != "u32" {
		t.Fatalf("capture over a local darray[u32]: got %v %v, want u32", lineType, ok)
	}
	if !result.analyzer.storeScanHoldsNoRegionStorage(lineType) {
		t.Fatalf("a u32 capture must hold no region storage")
	}
	// Negative: a capture over a local darray[sview] is not typed from the spelling (its views
	// may point into the source), so the scan keeps judging it as a store.
	if nameType, ok := result.analyzer.storeScanLoopBinderElemType(storeScanFindFunc(t, result, "copy_names"), &ast.Ident{Name: "name"}); ok {
		t.Fatalf("capture over a local darray[sview] must not be typed, got %v", nameType)
	}
	if got := result.analyzer.calleeStoreModeInto(storeScanFindFunc(t, result, "copy_names"), 1, 0); got == storeNone {
		t.Errorf("a view loop capture must stay a store")
	}
}
