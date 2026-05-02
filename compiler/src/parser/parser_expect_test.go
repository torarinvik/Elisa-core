package parser

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/unparse"
)

func TestParseExpectPatternStatementPreservesBlocklessBindingForm(t *testing.T) {
	file, errs := parseSourceFile(t, `packed enum Expr:
    Int(value: int)
    Add(left: Expr, right: Expr)

def check(node: Expr) -> void:
    can Abort.Panic:
        expect node as Expr.Int(value)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	canStmt, ok := decl.Body[0].(*ast.CanStmt)
	if !ok {
		t.Fatalf("expected can stmt, got %T", decl.Body[0])
	}
	expectStmt, ok := canStmt.Body[0].(*ast.ExpectPatternStmt)
	if !ok {
		t.Fatalf("expected blockless expect pattern stmt, got %T", canStmt.Body[0])
	}
	if _, ok := expectStmt.Value.(*ast.Ident); !ok {
		t.Fatalf("expected expect value ident, got %T", expectStmt.Value)
	}
	if len(expectStmt.Patterns) != 1 {
		t.Fatalf("expected one expect pattern, got %d", len(expectStmt.Patterns))
	}
	variant, ok := expectStmt.Patterns[0].(*ast.MatchVariantPattern)
	if !ok || variant.EnumName != "Expr" || variant.Variant != "Int" || len(variant.Args) != 1 {
		t.Fatalf("expected Expr.Int(value) pattern, got %#v", expectStmt.Patterns[0])
	}
}

func TestParseExpectLetPatternStatementPreservesBlocklessBindingForm(t *testing.T) {
	file, errs := parseSourceFile(t, `packed enum Expr:
    Int(value: int)
    Add(left: Expr, right: Expr)

def check(node: Expr) -> void:
    can Abort.Panic:
        expect let Expr.Int(value) = node
        assert value != 0
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	canStmt, ok := decl.Body[0].(*ast.CanStmt)
	if !ok {
		t.Fatalf("expected can stmt, got %T", decl.Body[0])
	}
	expectStmt, ok := canStmt.Body[0].(*ast.ExpectPatternStmt)
	if !ok {
		t.Fatalf("expected blockless expect-let pattern stmt, got %T", canStmt.Body[0])
	}
	if _, ok := expectStmt.Value.(*ast.Ident); !ok {
		t.Fatalf("expected expect-let value ident, got %T", expectStmt.Value)
	}
	if len(expectStmt.Patterns) != 1 {
		t.Fatalf("expected one expect-let pattern, got %d", len(expectStmt.Patterns))
	}
	variant, ok := expectStmt.Patterns[0].(*ast.MatchVariantPattern)
	if !ok || variant.EnumName != "Expr" || variant.Variant != "Int" || len(variant.Args) != 1 {
		t.Fatalf("expected Expr.Int(value) pattern, got %#v", expectStmt.Patterns[0])
	}
}

func TestParseExpectLetPatternBlockLowersToMatchStmt(t *testing.T) {
	file, errs := parseSourceFile(t, `enum Expr:
    Int(value: int)

def check(node: Expr) -> int:
    can Abort.Panic:
        expect let Expr.Int(value) = node:
            return value
    return 0
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	canStmt := decl.Body[0].(*ast.CanStmt)
	matchStmt, ok := canStmt.Body[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected expect-let block to lower to match stmt, got %T", canStmt.Body[0])
	}
	if len(matchStmt.Arms) != 2 {
		t.Fatalf("expected pattern arm plus wildcard arm, got %d", len(matchStmt.Arms))
	}
	if _, ok := matchStmt.Arms[1].Body[0].(*ast.PanicStmt); !ok {
		t.Fatalf("expected wildcard fallback panic, got %T", matchStmt.Arms[1].Body[0])
	}
}

func TestParseExpectPatternBlockPreservesCaptureBody(t *testing.T) {
	file, errs := parseSourceFile(t, `enum Expr:
    Int(value: int)

def check(node: Expr) -> int:
    can Abort.Panic:
        expect node as Expr.Int(value):
            return value
    return 0
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	canStmt := decl.Body[0].(*ast.CanStmt)
	matchStmt, ok := canStmt.Body[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected expect block to lower to match stmt, got %T", canStmt.Body[0])
	}
	if len(matchStmt.Arms) != 2 {
		t.Fatalf("expected pattern arm plus wildcard arm, got %d", len(matchStmt.Arms))
	}
	ret, ok := matchStmt.Arms[0].Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected pattern arm body to preserve return, got %T", matchStmt.Arms[0].Body[0])
	}
	ident, ok := ret.Value.(*ast.Ident)
	if !ok || ident.Name != "value" {
		t.Fatalf("expected return value capture, got %#v", ret.Value)
	}
	if _, ok := matchStmt.Arms[1].Body[0].(*ast.PanicStmt); !ok {
		t.Fatalf("expected wildcard fallback panic, got %T", matchStmt.Arms[1].Body[0])
	}
}

func TestParseExpectPatternSupportsPinnedValues(t *testing.T) {
	file, errs := parseSourceFile(t, `enum Expr:
    Infix(op: int, left: int, right: int)

def check(node: Expr, expected_op: int) -> void:
    can Abort.Panic:
        expect node as Expr.Infix(^expected_op, _, _)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	canStmt := decl.Body[0].(*ast.CanStmt)
	expectStmt, ok := canStmt.Body[0].(*ast.ExpectPatternStmt)
	if !ok {
		t.Fatalf("expected blockless expect pattern stmt, got %T", canStmt.Body[0])
	}
	variant, ok := expectStmt.Patterns[0].(*ast.MatchVariantPattern)
	if !ok || len(variant.Args) != 3 {
		t.Fatalf("expected Expr.Infix pattern, got %#v", expectStmt.Patterns[0])
	}
	pinned, ok := variant.Args[0].Pattern.(*ast.MatchLiteralPattern)
	if !ok || !pinned.Pinned {
		t.Fatalf("expected pinned literal pattern, got %#v", variant.Args[0].Pattern)
	}
	ident, ok := pinned.Value.(*ast.Ident)
	if !ok || ident.Name != "expected_op" {
		t.Fatalf("expected pinned expected_op ident, got %#v", pinned.Value)
	}
	formatted := unparse.FormatStmt(expectStmt)
	if !strings.Contains(formatted, "Expr.Infix(^expected_op, _, _)") {
		t.Fatalf("expected formatted expect to preserve pin, got:\n%s", formatted)
	}
}

func TestParseExpectPatternSupportsListPatterns(t *testing.T) {
	file, errs := parseSourceFile(t, `enum Expr:
    Int(value: int)

def check(values: darray[Expr]) -> void:
    can Abort.Panic:
        expect values as [Expr.Int(nonzero), _]
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	canStmt := decl.Body[0].(*ast.CanStmt)
	expectStmt, ok := canStmt.Body[0].(*ast.ExpectPatternStmt)
	if !ok {
		t.Fatalf("expected blockless expect pattern stmt, got %T", canStmt.Body[0])
	}
	list, ok := expectStmt.Patterns[0].(*ast.MatchListPattern)
	if !ok || len(list.Elems) != 2 {
		t.Fatalf("expected two-element list pattern, got %#v", expectStmt.Patterns[0])
	}
	variant, ok := list.Elems[0].(*ast.MatchVariantPattern)
	if !ok || variant.EnumName != "Expr" || variant.Variant != "Int" || len(variant.Args) != 1 {
		t.Fatalf("expected Expr.Int(nonzero) element pattern, got %#v", list.Elems[0])
	}
	if bind, ok := variant.Args[0].Pattern.(*ast.MatchBindPattern); !ok || bind.Name != "nonzero" {
		t.Fatalf("expected nested nonzero predicate/bind pattern, got %#v", variant.Args[0].Pattern)
	}
	if _, ok := list.Elems[1].(*ast.MatchWildcardPattern); !ok {
		t.Fatalf("expected wildcard second element, got %#v", list.Elems[1])
	}
	formatted := unparse.FormatStmt(expectStmt)
	if !strings.Contains(formatted, "[Expr.Int(nonzero), _]") {
		t.Fatalf("expected formatted expect to preserve list pattern, got:\n%s", formatted)
	}
}

func TestParseExpectPatternSupportsAnonymousFieldShapeAndListRest(t *testing.T) {
	file, errs := parseSourceFile(t, `struct Block:
    stmts: int[3]

enum Decl:
    Program(name: int, params: int, block: Block)

def check(root: Decl) -> void:
    can Abort.Panic:
        expect root as Decl.Program(_, _, {stmts: [1, 2, ...]})
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[2].(*ast.FuncDecl)
	canStmt := decl.Body[0].(*ast.CanStmt)
	expectStmt, ok := canStmt.Body[0].(*ast.ExpectPatternStmt)
	if !ok {
		t.Fatalf("expected blockless expect pattern stmt, got %T", canStmt.Body[0])
	}
	variant, ok := expectStmt.Patterns[0].(*ast.MatchVariantPattern)
	if !ok || variant.EnumName != "Decl" || variant.Variant != "Program" || len(variant.Args) != 3 {
		t.Fatalf("expected Decl.Program pattern, got %#v", expectStmt.Patterns[0])
	}
	shape, ok := variant.Args[2].Pattern.(*ast.MatchStructPattern)
	if !ok || !shape.Brace || shape.TypeName != "" || len(shape.Args) != 1 || shape.Args[0].Name != "stmts" {
		t.Fatalf("expected anonymous field-shape pattern, got %#v", variant.Args[2].Pattern)
	}
	list, ok := shape.Args[0].Pattern.(*ast.MatchListPattern)
	if !ok || len(list.Elems) != 3 {
		t.Fatalf("expected list pattern with rest, got %#v", shape.Args[0].Pattern)
	}
	if _, ok := list.Elems[2].(*ast.MatchRestPattern); !ok {
		t.Fatalf("expected final rest marker, got %#v", list.Elems[2])
	}
	formatted := unparse.FormatStmt(expectStmt)
	if !strings.Contains(formatted, "Decl.Program(_, _, {stmts: [1, 2, ...]})") {
		t.Fatalf("expected formatted expect to preserve field-shape pattern, got:\n%s", formatted)
	}
}

func TestParseExpectIdentifierCallRemainsExpressionStatement(t *testing.T) {
	file, errs := parseSourceFile(t, `def expect(value: int) -> void:
    pass

def check() -> void:
    expect(3)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	if _, ok := decl.Body[0].(*ast.ExprStmt); !ok {
		t.Fatalf("expected normal expect(...) call to remain expression stmt, got %T", decl.Body[0])
	}
}

func TestParseAssertBareConditionStatement(t *testing.T) {
	file, errs := parseSourceFile(t, `def check(left: int, right: int) -> void:
    assert left != right
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	stmt, ok := decl.Body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected assert call expression statement, got %T", decl.Body[0])
	}
	call, ok := stmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected assert call, got %T", stmt.Expr)
	}
	callee, ok := call.Func.(*ast.Ident)
	if !ok || callee.Name != "assert" {
		t.Fatalf("expected assert callee, got %#v", call.Func)
	}
	if len(call.Args) != 1 {
		t.Fatalf("expected one assert argument, got %d", len(call.Args))
	}
	condition, ok := call.Args[0].(*ast.BinaryExpr)
	if !ok || condition.Op != lexer.TOKEN_BANGEQ {
		t.Fatalf("expected assert condition to parse as != binary expr, got %#v", call.Args[0])
	}
}
