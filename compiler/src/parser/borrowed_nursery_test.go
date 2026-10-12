package parser

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

func TestBorrowedNurseryKeepsHiddenPoolAliasAndSurfaceSyntax(t *testing.T) {
	const source = `def run(pool: mutable ThreadPool&):
	nursery on(pool):
		pass
`
	file, errs := parseSourceFile(t, source)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || len(fn.Body) != 1 {
		t.Fatalf("expected one function with one body statement, got %#v", file.Decls)
	}
	nursery, ok := fn.Body[0].(*ast.PoolStmt)
	if !ok || !nursery.Nursery || nursery.BorrowedPool != "pool" {
		t.Fatalf("expected borrowed nursery AST, got %#v", fn.Body[0])
	}
	if nursery.Name == "" || nursery.Name == nursery.BorrowedPool || !strings.HasPrefix(nursery.Name, "__nursery_pool_") || !strings.ContainsRune(nursery.Name, '\x00') {
		t.Fatalf("borrowed pool needs a private alias, got %q for source binding %q", nursery.Name, nursery.BorrowedPool)
	}
	groupDecl, ok := nursery.Body[0].(*ast.VarDeclStmt)
	if !ok || !strings.ContainsRune(groupDecl.Name, '\x00') {
		t.Fatalf("generated task-group name must not be source-spellable, got %#v", nursery.Body[0])
	}
	if len(nursery.Body) != 3 {
		t.Fatalf("expected generated group setup, source body, and wait; got %d statements", len(nursery.Body))
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "nursery on(pool):") || strings.Contains(formatted, "task_group_new") || strings.Contains(formatted, "task_group_wait_all") {
		t.Fatalf("unparse should preserve source nursery syntax without exposing generated scaffolding:\n%s", formatted)
	}
	if _, roundTripErrors := parseSourceFile(t, formatted); len(roundTripErrors) != 0 {
		t.Fatalf("formatted borrowed nursery did not parse: %v\n%s", roundTripErrors, formatted)
	}
}

func TestNurseryGeneratedAliasesCannotBeShadowedBySourceNames(t *testing.T) {
	const source = `def work(value: i64) -> i64:
	return value

def run(pool: mutable ThreadPool&):
	nursery on(pool):
		__nursery_pool_1: mutable ThreadPool = zeroed
		__nursery_grp_1: mutable TaskGroup = zeroed
		submit work(1)
`
	file, errs := parseSourceFile(t, source)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[1].(*ast.FuncDecl)
	nursery := fn.Body[0].(*ast.PoolStmt)
	groupName := nursery.Body[0].(*ast.VarDeclStmt).Name
	if groupName == "__nursery_grp_1" || groupName != "__nursery_grp_1\x00" {
		t.Fatalf("expected unspellable generated group name, got %q", groupName)
	}
	if len(nursery.Body) != 5 {
		t.Fatalf("expected generated group, two source declarations, submit, and wait; got %d statements", len(nursery.Body))
	}
	add := nursery.Body[3].(*ast.ExprStmt).Expr.(*ast.CallExpr)
	groupRef := add.Args[0].(*ast.CastExpr).Operand.(*ast.AddrOfExpr).Operand.(*ast.Ident)
	if groupRef.Name != groupName {
		t.Fatalf("submit group reference was redirected by a source name: got %q, want %q", groupRef.Name, groupName)
	}
	submit := add.Args[1].(*ast.CallExpr)
	poolRef := submit.Args[0].(*ast.CastExpr).Operand.(*ast.AddrOfExpr).Operand.(*ast.Ident)
	if poolRef.Name != nursery.Name || poolRef.Name != "__nursery_pool_1\x00" {
		t.Fatalf("submit pool reference was redirected by a source name: got %q, want %q", poolRef.Name, nursery.Name)
	}
}

func TestOwnedNurseryFormsRoundTripWithoutGeneratedPoolNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{name: "default workers", src: "nursery:\n", want: "nursery:"},
		{name: "explicit workers", src: "nursery workers(4):\n", want: "nursery workers(4):"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, errs := parseSourceFile(t, "def run() -> void:\n\t"+tc.src+"\t\tpass\n")
			if len(errs) != 0 {
				t.Fatalf("unexpected parser errors: %v", errs)
			}
			formatted := unparse.FormatFile(file)
			if !strings.Contains(formatted, tc.want) || strings.ContainsRune(formatted, '\x00') || strings.Contains(formatted, "task_group_new") || strings.Contains(formatted, "task_group_wait_all") {
				t.Fatalf("unparse should preserve nursery spelling and hide generated scaffolding:\n%s", formatted)
			}
			if _, roundTripErrors := parseSourceFile(t, formatted); len(roundTripErrors) != 0 {
				t.Fatalf("formatted nursery did not parse: %v\n%s", roundTripErrors, formatted)
			}
		})
	}
}
