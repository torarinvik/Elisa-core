package parser

import (
	"elisacore/src/ast"
	"testing"
)

func TestParseNamedTupleValuePreservesSourceOrder(t *testing.T) {
	file, errs := parseSourceFile(t, `def pair() -> (ready: bool, count: i64):
    return (ready: true, count: 7)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function declaration, got %T", file.Decls[0])
	}
	returned, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return statement, got %T", fn.Body[0])
	}
	tuple, ok := returned.Value.(*ast.TupleExpr)
	if !ok || len(tuple.Elems) != 2 {
		t.Fatalf("expected two-element tuple value, got %#v", returned.Value)
	}
	if first, ok := tuple.Elems[0].(*ast.BoolLit); !ok || !first.Value {
		t.Fatalf("first named field value was not preserved in order: %#v", tuple.Elems[0])
	}
	if second, ok := tuple.Elems[1].(*ast.IntLit); !ok || second.Value != "7" {
		t.Fatalf("second named field value was not preserved in order: %#v", tuple.Elems[1])
	}
}

func TestTypedFoldHeadIsNotParsedAsNamedTupleValue(t *testing.T) {
	file, errs := parseSourceFile(t, `def sum(values: darray[i64]) -> i64:
    return (acc: i64 = 0, acc + value for value in values with acc = 0)
`)
	if len(errs) != 0 {
		t.Fatalf("typed fold head was rejected: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	returned := fn.Body[0].(*ast.ReturnStmt)
	if _, ok := returned.Value.(*ast.ExprBlock); !ok {
		t.Fatalf("typed fold head did not lower to an expression block: %T", returned.Value)
	}
}
