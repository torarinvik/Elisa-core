package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"testing"
)

func TestContractBlockExpressionBoundary(t *testing.T) {
	for _, clause := range []string{"ensure", "requires"} {
		for _, table := range []string{"match x:\n        true: true\n        false: false", "when x:\n        true -> true\n        false -> false"} {
			t.Run(clause+table[:5], func(t *testing.T) {
				file, errs := parseSourceFile(t, "def f(x: bool) -> bool:\n    "+clause+" "+table+"\n    return x\n")
				if len(errs) != 0 {
					t.Fatalf("unexpected parse errors: %v", errs)
				}
				fn := file.Decls[0].(*ast.FuncDecl)
				contracts := fn.EnsureValues
				if clause == "requires" {
					contracts = fn.Requires
				}
				if len(contracts) != 1 {
					t.Fatalf("contracts = %#v", contracts)
				}
				if m, ok := contracts[0].(*ast.MatchExpr); !ok || len(m.Arms) != 2 {
					t.Fatalf("contract condition = %#v", contracts[0])
				}

				if len(fn.Body) != 1 {
					t.Fatalf("body = %#v", fn.Body)
				}
				if _, ok := fn.Body[0].(*ast.ReturnStmt); !ok {
					t.Fatalf("following statement = %T", fn.Body[0])
				}
			})
		}
	}
}

func TestIndentedQuantifiedContractBoundary(t *testing.T) {
	file, errs := parseSourceFile(t, `def f(xs: darray[i64]) -> bool:
    ensure forall i in 0..<xs.count:
        xs[i] >= 0
        and xs[i] < 10
    invariant forall i in 0..<xs.count:
        xs[i] >= 0
    return true
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	q := fn.EnsureValues[0].(*ast.QuantifierExpr)
	guarded := q.Body.(*ast.BinaryExpr)
	if conjunction, ok := guarded.Right.(*ast.BinaryExpr); !ok || conjunction.Op != lexer.TOKEN_AND {
		t.Fatalf("predicate = %#v", guarded.Right)
	}
}

func TestBooleanContinuationBoundary(t *testing.T) {
	_, errs := parseSourceFile(t, `def f(x: bool, y: bool) -> bool:
    value: bool = x
        and y
    return value
        or x
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
}

func TestRuntimeQuantifierRefusesUnboundedAndKeepsIdentifiers(t *testing.T) {
	_, errs := parseSourceFile(t, `def invalid() -> bool:
    return forall i: i == i
`)
	if len(errs) == 0 {
		t.Fatal("unbounded runtime quantifier accepted")
	}
	_, errs = parseSourceFile(t, `def forall(x: bool) -> bool:
    return x
def valid() -> bool:
    return forall(true)
`)
	if len(errs) != 0 {
		t.Fatalf("ordinary forall call rejected: %v", errs)
	}
}
