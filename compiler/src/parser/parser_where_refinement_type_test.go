package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

func TestParseWhereRefinementTypeInBinderPositions(t *testing.T) {
	file, errs := parseSourceFile(t, `
def get(xs: darray[i64], i: i64 where 0 <= i and i < xs.count) -> i64:
    return xs[i]

def pick(n: i64, k: i64 where 0 <= k and k < n) -> i64 where 0 <= result and result < n:
    return k

def local(xs: darray[i64]) -> i64:
    index: i64 where 0 <= index and index < xs.count = 0
    return index
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	get := file.Decls[0].(*ast.FuncDecl)
	if _, ok := get.Params[1].Type.(*ast.WhereRefinementTypeExpr); !ok {
		t.Fatalf("expected parameter where refinement, got %T", get.Params[1].Type)
	}
	pick := file.Decls[1].(*ast.FuncDecl)
	if _, ok := pick.ReturnType.(*ast.WhereRefinementTypeExpr); !ok {
		t.Fatalf("expected return where refinement, got %T", pick.ReturnType)
	}
	local := file.Decls[2].(*ast.FuncDecl)
	varDecl := local.Body[0].(*ast.VarDeclStmt)
	if _, ok := varDecl.Type.(*ast.WhereRefinementTypeExpr); !ok {
		t.Fatalf("expected local where refinement, got %T", varDecl.Type)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"i: i64 where ((0 <= i) and (i < xs.count))",
		"-> i64 where ((0 <= result) and (result < n))",
		"index: i64 where ((0 <= index) and (index < xs.count)) = 0",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted source to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseWhereRefinementDoesNotReplaceNamedLawRefinement(t *testing.T) {
	file, errs := parseSourceFile(t, `
def f(x: i64 is Bounded[0, 10]) -> i64:
    return x
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	if _, ok := fn.Params[0].Type.(*ast.RefinementTypeExpr); !ok {
		t.Fatalf("expected named law refinement, got %T", fn.Params[0].Type)
	}
	if formatted := unparse.FormatFile(file); !strings.Contains(formatted, "x: i64 is Bounded[0, 10]") {
		t.Fatalf("expected named refinement formatting to survive, got:\n%s", formatted)
	}
}
