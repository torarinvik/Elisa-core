package semantic

import (
	"testing"

	"elisacore/src/ast"
)

// A by-value parameter whose type is program-lived (docs/128) is no write target for a local's
// storage: whatever the callee writes there must itself be program-lived, which the callee's own
// checks enforce. A recursive walker passing an AST child next to a body-local id list must not
// plan a write of the list into the child (that taint made every view read back through the
// child a header store of the list into the table).
const storeScanProgramLivedWriteSource = `global region source

struct Id:
    owner: u32
    tag: sview

struct Node:
    names: darray[sview @source] @source = []

struct Diag:
    name: sview
    line: u32

struct Table:
    diagnostics: mutable darray[Diag] = []
    moves: mutable darray[Id] = []
    count: mutable u32 = 0

def record(operand: Node, ids: darray[Id]&, table: lmut Table) -> void:
    can Memory.Allocate, Abort.Panic:
        if ids.count > 0 and operand.names.count > 0:
            table.moves <- table.moves.push(ids[0])

def text_of(node: Node, table: lmut Table) -> sview:
    can Memory.Allocate, Abort.Panic:
        table.count <- table.count + 1
        return node.names[0]

def walk(node: Node, table: lmut Table, ids: darray[Id]&) -> void:
    can Memory.Allocate, Abort.Panic:
        rebind text: sview, table = text_of(node, table)
        table <- record(node, ids, table)
        for name in node.names |table, ids, node|:
            child: Node = Node{names: []}
            table <- walk(child, table, ids)
            rebind child_text: sview, table = text_of(child, table)
            table.diagnostics <- table.diagnostics.push(Diag{name: child_text, line: 0})

def caller(node: Node, table: lmut Table) -> void:
    can Memory.Allocate, Abort.Panic:
        ids: mutable darray[Id] = []
        ids.push(Id{owner: 1, tag: ""})
        local_ids: mutable darray[Id] = [id for id in ids]
        table <- walk(node, table, local_ids)
`

func TestStoreScanProgramLivedParamIsNoWriteTarget(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_scan_program_lived_write.elisa", storeScanProgramLivedWriteSource, AnalyzeOptions{})
	a := result.analyzer
	walk := storeScanFindFunc(t, result, "walk")
	idx := a.storeScanIndex(walk)
	params := map[string]bool{}
	for _, p := range walk.Params {
		params[p.Name] = true
	}
	found := false
	walkStoreScanNodes(walk.Body, func(node any) {
		call, ok := node.(*ast.CallExpr)
		if !ok || call == nil {
			return
		}
		if id, isIdent := call.Func.(*ast.Ident); !isIdent || id.Name != "walk" {
			return
		}
		found = true
		for _, w := range a.storeScanCallWritePlan(walk, call, params, idx.locals) {
			if w.local == "child" {
				t.Fatalf("planned a write into the program-lived child (from param %d)", w.from)
			}
		}
	})
	if !found {
		t.Fatalf("recursive walk call not found")
	}
}
