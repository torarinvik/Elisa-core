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
