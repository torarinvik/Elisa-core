package parser

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/unparse"
)

func TestParseExplicitArgErgonomicsAndDestructuring(t *testing.T) {
	file, errs := parseSourceFile(t, `params Pair:
    left: i64
    right: i64 = 7

struct PairRow:
    first: i64
    second: i64

def add(use Pair) -> i64:
    return left + right

def build(left: i64, width: i64, pair: PairRow, rows: darray[PairRow]) -> i64:
    with args(use Pair(left:), width:):
        let PairRow{first: local_first, second} = pair
        for {first, second} in rows:
            return add(use Pair(right: 5, left:), right: width)
    return 0
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	paramsDecl, ok := file.Decls[0].(*ast.ParamsDecl)
	if !ok {
		t.Fatalf("expected params decl, got %T", file.Decls[0])
	}
	if paramsDecl.Name != "Pair" || len(paramsDecl.Params) != 2 || paramsDecl.Params[1].DefaultValue == nil {
		t.Fatalf("expected Pair params declaration with a defaulted field, got %#v", paramsDecl)
	}
	addDecl, ok := file.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected add function decl, got %T", file.Decls[2])
	}
	if len(addDecl.ParamPacks) != 1 || addDecl.ParamPacks[0].Name != "Pair" {
		t.Fatalf("expected add signature to use Pair pack, got %#v", addDecl.ParamPacks)
	}
	buildDecl, ok := file.Decls[3].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected build function decl, got %T", file.Decls[3])
	}
	argsScope, ok := buildDecl.Body[0].(*ast.ArgsScopeStmt)
	if !ok {
		t.Fatalf("expected args scope stmt, got %T", buildDecl.Body[0])
	}
	if len(argsScope.ParamPacks) != 1 || argsScope.ParamPacks[0].Name != "Pair" {
		t.Fatalf("expected args scope to use Pair pack, got %#v", argsScope.ParamPacks)
	}
	if len(argsScope.Args) != 1 || argsScope.Args[0].Name != "width" || !argsScope.Args[0].Shorthand {
		t.Fatalf("expected shorthand width ambient arg, got %#v", argsScope.Args)
	}
	letStmt, ok := argsScope.Body[0].(*ast.LetDestructureStmt)
	if !ok {
		t.Fatalf("expected let destructure stmt, got %T", argsScope.Body[0])
	}
	if letStmt.Pattern.TypeName != "PairRow" || len(letStmt.Pattern.Fields) != 2 {
		t.Fatalf("expected typed destructure pattern, got %#v", letStmt.Pattern)
	}
	if letStmt.Pattern.Fields[0].Field != "first" || letStmt.Pattern.Fields[0].Name != "local_first" {
		t.Fatalf("expected renamed destructure field, got %#v", letStmt.Pattern.Fields[0])
	}
	iterStmt, ok := argsScope.Body[1].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iter for stmt, got %T", argsScope.Body[1])
	}
	pattern, ok := iterStmt.Pattern.(*ast.StructDestructurePattern)
	if !ok {
		t.Fatalf("expected struct destructure loop pattern, got %T", iterStmt.Pattern)
	}
	if pattern.TypeName != "" || len(pattern.Fields) != 2 || pattern.Fields[0].Field != "first" || pattern.Fields[1].Field != "second" {
		t.Fatalf("unexpected loop destructure pattern %#v", pattern)
	}
	ret, ok := iterStmt.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt inside loop, got %T", iterStmt.Body[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expr, got %T", ret.Value)
	}
	if len(call.ParamPacks) != 1 || call.ParamPacks[0].Name != "Pair" {
		t.Fatalf("expected call to use Pair pack, got %#v", call.ParamPacks)
	}
	foundPackShorthand := false
	for _, arg := range call.ParamPacks[0].Args {
		if arg.Name == "left" && arg.Shorthand {
			foundPackShorthand = true
		}
	}
	if !foundPackShorthand {
		t.Fatalf("expected pack application to preserve shorthand args, got %#v", call.ParamPacks[0].Args)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"params Pair:",
		"def add(use Pair) -> i64:",
		"with args(use Pair(left:), width:):",
		"let PairRow{first: local_first, second} = pair",
		"for {first, second} in rows:",
		"return add(use Pair(right: 5, left:), right: width)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}
