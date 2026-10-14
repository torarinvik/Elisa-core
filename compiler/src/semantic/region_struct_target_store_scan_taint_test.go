package semantic

import (
	"testing"

	"elisacore/src/ast"
)

const storeScanMatchTaintSource = `enum Ex:
    Ident(name: sview, line: i64)
    Other(line: i64)

enum St:
    Assign(target: Ex, op: i64, line: i64)
    VarDecl(name: sview, line: i64)
    If(cond: Ex, then_body: darray[St], else_body: darray[St], line: i64)

struct Diag:
    name: sview
    line: i64

struct Table:
    diagnostics: mutable darray[Diag] = []

def in_list(name: sview, list: darray[sview]) -> bool:
    for item in list:
        if item == name:
            return true
    return false

def remove_name(name: sview, list: darray[sview]) -> darray[sview]:
    can Memory.Allocate, Abort.Panic:
        result: mutable darray[sview] = []
        result.extend([item for item in list if item != name])
        return result

def walk(statements: darray[St], tracked_loop_vars: darray[sview], table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        current_tracked: mutable darray[sview] = tracked_loop_vars
        for statement in statements |table, current_tracked|:
            match statement:
                St.Assign(target, op, line):
                    match target:
                        Ex.Ident(name, _):
                            table.diagnostics.push(Diag{name: name, line: line}) if op == 1 and in_list(name, current_tracked)
                        _:
                            pass
                St.VarDecl(name, line):
                    current_tracked <- remove_name(name, current_tracked)
                St.If(cond, then_body, else_body, line):
                    walk(then_body, current_tracked, table)
                    walk(else_body, current_tracked, table)
`

// A match statement's header is its scrutinee (and patterns), not its arms' bodies: a tainted
// name used INSIDE an arm (`in_list(name, current_tracked)`) must not taint the binders of a
// match over an untainted value. Tainting them made every Diagnostic built from the walked
// statements look like it stored the tracked list into the table.
func TestStoreScanMatchArmBodyIsNotTheHeader(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_scan_match_taint.elisa", storeScanMatchTaintSource, AnalyzeOptions{})
	walk := storeScanFindFunc(t, result, "walk")
	// Resolve callees as during analysis (the scan runs with a scope in place).
	result.analyzer.currentScope = result.analyzer.globalScope
	// Source: tracked_loop_vars (1). Its content is the tracked list and nothing matched out of
	// `statements`.
	_, _, content, _, _ := result.analyzer.storeScanTaint(walk, 1, "table")
	// then_body is passed to walk next to current_tracked. walk's by-value darray parameter
	// shares then_body's storage, but walk's own summary stores nothing into `statements`, so
	// the callee-write taint leaves then_body clean.
	for _, name := range []string{"target", "name", "then_body", "cond", "line"} {
		if content[name] {
			t.Errorf("binder %q of a match over an untainted scrutinee was tainted: %v", name, content)
		}
	}
	if !content["current_tracked"] {
		t.Errorf("the local bound from the source must stay tainted: %v", content)
	}
	// Source: statements (0). Now the scrutinee is tainted, so the binders are content.
	_, _, content, _, _ = result.analyzer.storeScanTaint(walk, 0, "table")
	for _, name := range []string{"statement", "target", "name", "then_body"} {
		if !content[name] {
			t.Errorf("binder %q of a match over a tainted scrutinee must be content: %v", name, content)
		}
	}
}

func storeScanFindFunc(t *testing.T, result *Result, name string) *ast.FuncDecl {
	t.Helper()
	for _, d := range result.File.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name == name {
			return fn
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}
