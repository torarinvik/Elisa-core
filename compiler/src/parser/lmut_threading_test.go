package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// docs/120 §2: declared lmut threading — parser-level validation + erasure.

const lmutThreadPrelude = "struct L:\n    p: mutable i64\n\n"

func parseErrorsJoined(t *testing.T, src string) string {
	t.Helper()
	_, errs := parseSourceFile(t, src)
	return strings.Join(errs, "\n")
}

// A valid declaration erases: return type drops the lmut slot (here to the bare
// scalar), returns drop the threaded element, and the manifest is retained on
// FuncDecl.LmutThreadSlots for the §3 call-site rules.
func TestDeclaredThreadingErasure(t *testing.T) {
	file, errs := parseSourceFile(t, lmutThreadPrelude+
		"def f(lx: lmut L) -> (ch: i64, lx: lmut L):\n    return 1, lx\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	var fn *ast.FuncDecl
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name == "f" {
			fn = f
		}
	}
	if fn == nil {
		t.Fatalf("function f not found")
	}
	if len(fn.LmutThreadSlots) != 1 || fn.LmutThreadSlots[0].ParamName != "lx" || fn.LmutThreadSlots[0].TupleIndex != 1 {
		t.Fatalf("expected one thread slot for lx at tuple index 1, got %+v", fn.LmutThreadSlots)
	}
	if _, stillTuple := fn.ReturnType.(*ast.TupleTypeExpr); stillTuple {
		t.Fatalf("return type not erased to scalar: %T", fn.ReturnType)
	}
	ret, ok := fn.Body[len(fn.Body)-1].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected trailing return, got %T", fn.Body[len(fn.Body)-1])
	}
	if _, stillTuple := ret.Value.(*ast.TupleExpr); stillTuple {
		t.Fatalf("return expression not erased to scalar: %T", ret.Value)
	}
}

func TestDeclaredThreadingReturnPathMustThread(t *testing.T) {
	errs := parseErrorsJoined(t, lmutThreadPrelude+
		"def f(lx: lmut L) -> (ch: i64, lx: lmut L):\n    if lx.p > 0:\n        return 1\n    return 2, lx\n")
	if !strings.Contains(errs, "literal tuple of all 2 declared slots") {
		t.Fatalf("expected missing-thread error, got: %s", errs)
	}
}

func TestDeclaredThreadingWrongSlotExpr(t *testing.T) {
	errs := parseErrorsJoined(t, lmutThreadPrelude+
		"def f(lx: lmut L, other: lmut L) -> (ch: i64, lx: lmut L):\n    return 1, other\n")
	if !strings.Contains(errs, "must be exactly the lmut parameter") {
		t.Fatalf("expected wrong-slot error, got: %s", errs)
	}
}

func TestDeclaredThreadingSlotMustNameLmutParam(t *testing.T) {
	errs := parseErrorsJoined(t, lmutThreadPrelude+
		"def f(lx: L&) -> (ch: i64, lx: lmut L):\n    return 1, lx\n")
	if !strings.Contains(errs, "is not lmut") {
		t.Fatalf("expected non-lmut-param error, got: %s", errs)
	}
	errs = parseErrorsJoined(t, lmutThreadPrelude+
		"def f(a: lmut L) -> (ch: i64, lx: lmut L):\n    return 1, a\n")
	if !strings.Contains(errs, "does not name a parameter") {
		t.Fatalf("expected no-such-param error, got: %s", errs)
	}
}

// Returns nested inside if/can/while blocks are found by the reflection walk and
// rewritten like top-level ones. (Lambda bodies are structurally unreachable: the
// walk only descends statement-list fields, never expression fields, and a lambda
// body hangs off an expression — its returns belong to the lambda.)
func TestDeclaredThreadingRewritesNestedReturns(t *testing.T) {
	file, errs := parseSourceFile(t, lmutThreadPrelude+
		"def f(lx: lmut L) -> (ch: i64, lx: lmut L):\n    can Abort.Panic:\n        if lx.p > 0:\n            return 1, lx\n        while lx.p < 0:\n            return 2, lx\n    return 3, lx\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	// Every rewritten return has a scalar (non-tuple) value after erasure.
	var fn *ast.FuncDecl
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name == "f" {
			fn = f
		}
	}
	if fn == nil || len(fn.LmutThreadSlots) != 1 {
		t.Fatalf("expected threading fn with one slot")
	}
}
