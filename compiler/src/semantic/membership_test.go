package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeMembershipExprUsesBoolAndArrayLiteralType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "membership_expr.elisa", `def keep(value: i64) -> bool:
    return value in [1, 2, 3]
`)

	decl := result.File.Decls[0].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr, ok := ret.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected membership binary expr, got %T", ret.Value)
	}
	if got := result.ExprTypes[inExpr].String(); got != "bool" {
		t.Fatalf("expected membership expr type bool, got %s", got)
	}
	list, ok := inExpr.Right.(*ast.ListLitExpr)
	if !ok {
		t.Fatalf("expected membership rhs list literal, got %T", inExpr.Right)
	}
	arrayType, ok := result.ExprTypes[list].(*ArrayType)
	if !ok || arrayType == nil {
		t.Fatalf("expected membership rhs list type, got %T %#v", result.ExprTypes[list], result.ExprTypes[list])
	}
	if builtin, ok := arrayType.Elem.(*BuiltinType); !ok || builtin.Name != "i64" {
		t.Fatalf("expected membership rhs element type i64, got %#v", arrayType.Elem)
	}
	if !arrayType.HasConstSize || arrayType.ConstSize != 3 {
		t.Fatalf("expected fixed-size membership array, got %#v", arrayType)
	}
}

func TestAnalyzeBraceMembershipExprUsesBoolAndArrayLiteralType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "brace_membership_expr.elisa", `def keep(value: i64) -> bool:
    return value in {1, 2, 3}
`)

	decl := result.File.Decls[0].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr, ok := ret.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected membership binary expr, got %T", ret.Value)
	}
	if got := result.ExprTypes[inExpr].String(); got != "bool" {
		t.Fatalf("expected membership expr type bool, got %s", got)
	}
	list, ok := inExpr.Right.(*ast.ListLitExpr)
	if !ok || !list.Brace {
		t.Fatalf("expected brace membership rhs literal, got %T %#v", inExpr.Right, inExpr.Right)
	}
	arrayType, ok := result.ExprTypes[list].(*ArrayType)
	if !ok || arrayType == nil {
		t.Fatalf("expected membership rhs list type, got %T %#v", result.ExprTypes[list], result.ExprTypes[list])
	}
	if builtin, ok := arrayType.Elem.(*BuiltinType); !ok || builtin.Name != "i64" {
		t.Fatalf("expected membership rhs element type i64, got %#v", arrayType.Elem)
	}
}

func TestAnalyzeNotInMembershipExprUsesBool(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "not_in_membership_expr.elisa", `def keep(value: i64) -> bool:
    return value not in {1, 2, 3}
`)

	decl := result.File.Decls[0].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	notExpr, ok := ret.Value.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected not-in to parse as unary not expr, got %T", ret.Value)
	}
	if got := result.ExprTypes[notExpr].String(); got != "bool" {
		t.Fatalf("expected not-in expr type bool, got %s", got)
	}
	inExpr, ok := notExpr.Operand.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected not-in operand to be membership binary expr, got %T", notExpr.Operand)
	}
	if got := result.ExprTypes[inExpr].String(); got != "bool" {
		t.Fatalf("expected membership operand type bool, got %s", got)
	}
}

func TestAnalyzeBraceMembershipRangeExprUsesBool(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "brace_membership_range.elisa", `def keep(value: i64) -> bool:
    return value in {1..=3, 8..<10}
`)

	decl := result.File.Decls[0].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr, ok := ret.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected membership binary expr, got %T", ret.Value)
	}
	if got := result.ExprTypes[inExpr].String(); got != "bool" {
		t.Fatalf("expected membership expr type bool, got %s", got)
	}
	list, ok := inExpr.Right.(*ast.ListLitExpr)
	if !ok || !list.Brace || len(list.Elems) != 2 {
		t.Fatalf("expected brace membership range literal, got %T %#v", inExpr.Right, inExpr.Right)
	}
	if _, ok := list.Elems[0].(*ast.MembershipRangeExpr); !ok {
		t.Fatalf("expected first candidate range, got %T", list.Elems[0])
	}
}

func TestAnalyzeBraceMembershipRangeAcceptsCharBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "brace_membership_range_char.elisa", `def keep(value: char) -> bool:
    return value in {'0'..='9'}
`)

	decl := result.File.Decls[0].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr, ok := ret.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected membership binary expr, got %T", ret.Value)
	}
	if got := result.ExprTypes[inExpr].String(); got != "bool" {
		t.Fatalf("expected membership expr type bool, got %s", got)
	}
}

func TestAnalyzeDirectMembershipRangeAcceptsCharBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "direct_membership_range_char.elisa", `def is_digit(ch: char) -> bool:
    return ch in '0'..'9'

def is_lower(ch: char) -> bool:
    return ch in 'a'..'z'

def is_upper(ch: char) -> bool:
    return ch in 'A'..'Z'
`)

	for i, declNode := range result.File.Decls {
		decl := declNode.(*ast.FuncDecl)
		ret := decl.Body[0].(*ast.ReturnStmt)
		inExpr, ok := ret.Value.(*ast.BinaryExpr)
		if !ok {
			t.Fatalf("decl %d: expected membership binary expr, got %T", i, ret.Value)
		}
		if got := result.ExprTypes[inExpr].String(); got != "bool" {
			t.Fatalf("decl %d: expected membership expr type bool, got %s", i, got)
		}
		list, ok := inExpr.Right.(*ast.ListLitExpr)
		if !ok || len(list.Elems) != 1 {
			t.Fatalf("decl %d: expected direct range normalized to one membership candidate, got %T %#v", i, inExpr.Right, inExpr.Right)
		}
		if _, ok := list.Elems[0].(*ast.MembershipRangeExpr); !ok {
			t.Fatalf("decl %d: expected range candidate, got %T", i, list.Elems[0])
		}
	}
}

func TestAnalyzeBraceMembershipRangeAcceptsConstEnumBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "brace_membership_range_enum.elisa", `const enum TokenKind of u32:
    IF
    LET
    IDENT
    NUMBER
    STRING

def keep(kind: TokenKind) -> bool:
    return kind in {.IF..=LET, .NUMBER..<STRING}
`)

	decl := result.File.Decls[1].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr, ok := ret.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected membership binary expr, got %T", ret.Value)
	}
	if got := result.ExprTypes[inExpr].String(); got != "bool" {
		t.Fatalf("expected membership expr type bool, got %s", got)
	}
	list, ok := inExpr.Right.(*ast.ListLitExpr)
	if !ok || !list.Brace || len(list.Elems) != 2 {
		t.Fatalf("expected brace membership range literal, got %T %#v", inExpr.Right, inExpr.Right)
	}
	rangeExpr, ok := list.Elems[0].(*ast.MembershipRangeExpr)
	if !ok {
		t.Fatalf("expected first candidate range, got %T", list.Elems[0])
	}
	if got := result.ExprTypes[rangeExpr.Start].String(); got != "TokenKind" {
		t.Fatalf("expected range start type TokenKind, got %s", got)
	}
}

func TestAnalyzeBraceMembershipRangeRejectsNonIntegralBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "brace_membership_range_float.elisa", `def keep(value: f64) -> bool:
    return value in {1.0..=3.0}
`)

	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "membership ranges require integer- or const-enum-compatible bounds") {
		t.Fatalf("expected membership range diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeBraceMembershipInfersShorthandMembers(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "brace_membership_shorthand.elisa", `const enum TokenKind of u32:
    IF
    LET
    IDENT

def keep(kind: TokenKind) -> bool:
    return kind in {.IF, .LET}
`)

	decl := result.File.Decls[1].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr, ok := ret.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected membership binary expr, got %T", ret.Value)
	}
	if got := result.ExprTypes[inExpr].String(); got != "bool" {
		t.Fatalf("expected membership expr type bool, got %s", got)
	}
	list, ok := inExpr.Right.(*ast.ListLitExpr)
	if !ok || !list.Brace {
		t.Fatalf("expected brace membership rhs literal, got %T %#v", inExpr.Right, inExpr.Right)
	}
	arrayType, ok := result.ExprTypes[list].(*ArrayType)
	if !ok || arrayType == nil {
		t.Fatalf("expected membership rhs list type, got %T %#v", result.ExprTypes[list], result.ExprTypes[list])
	}
	if got := arrayType.Elem.String(); got != "TokenKind" {
		t.Fatalf("expected membership rhs element type TokenKind, got %s", got)
	}
	first, ok := list.Elems[0].(*ast.ShorthandMemberExpr)
	if !ok {
		t.Fatalf("expected first candidate shorthand member, got %T", list.Elems[0])
	}
	if got := result.ExprTypes[first].String(); got != "TokenKind" {
		t.Fatalf("expected shorthand member type TokenKind, got %s", got)
	}
}

func TestAnalyzeBraceMembershipRejectsShorthandWithoutEnumContext(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "brace_membership_shorthand_bad.elisa", `def keep(value: i64) -> bool:
    return value in {.IF}
`)

	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "shorthand member \".IF\" requires an expected const enum type") {
		t.Fatalf("expected shorthand enum-context diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsStandaloneBraceMembershipSetLiteral(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "standalone_brace_membership_literal.elisa", `def keep() -> i64:
    return {1, 2, 3}
`)

	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "brace membership set literals are only valid on the right-hand side of `in`") {
		t.Fatalf("expected standalone brace membership diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeMembershipAllowsEmptyListLiteral(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "membership_empty.elisa", `def keep(value: i64) -> bool:
    return value in []
`)

	decl := result.File.Decls[0].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr := ret.Value.(*ast.BinaryExpr)
	list := inExpr.Right.(*ast.ListLitExpr)
	arrayType, ok := result.ExprTypes[list].(*ArrayType)
	if !ok || arrayType == nil {
		t.Fatalf("expected empty membership rhs list type, got %T %#v", result.ExprTypes[list], result.ExprTypes[list])
	}
	if !arrayType.HasConstSize || arrayType.ConstSize != 0 {
		t.Fatalf("expected empty fixed-size membership array, got %#v", arrayType)
	}
}

func TestAnalyzeMembershipAllowsTokenSetDecl(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "membership_tokenset.elisa", `const enum TokenKind of u32:
    IF
    LET
    IDENT

tokenset ExprStart = [TokenKind.IF, TokenKind.LET]

def keep(kind: TokenKind) -> bool:
    return kind in ExprStart
`)

	decl := result.File.Decls[2].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr := ret.Value.(*ast.BinaryExpr)
	if got := result.ExprTypes[inExpr].String(); got != "bool" {
		t.Fatalf("expected membership expr type bool, got %s", got)
	}
	tokenSet := result.File.Decls[1].(*ast.TokenSetDecl)
	arrayType, ok := result.ExprTypes[tokenSet.Value].(*ArrayType)
	if !ok || arrayType == nil {
		t.Fatalf("expected tokenset list type, got %T %#v", result.ExprTypes[tokenSet.Value], result.ExprTypes[tokenSet.Value])
	}
	if !arrayType.HasConstSize || arrayType.ConstSize != 2 {
		t.Fatalf("expected fixed-size tokenset array, got %#v", arrayType)
	}
}

func TestAnalyzeTokenSetDeclQualifiesBareMembersFromElementType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "membership_tokenset_bare.elisa", `const enum TokenKind of u32:
    IF
    LET
    IDENT

tokenset ExprStart: TokenKind = [IF, LET]

def keep(kind: TokenKind) -> bool:
    return kind in ExprStart
`)

	tokenSet := result.File.Decls[1].(*ast.TokenSetDecl)
	if len(tokenSet.Value.Elems) != 2 {
		t.Fatalf("expected two token set entries, got %#v", tokenSet.Value.Elems)
	}
	first, ok := tokenSet.Value.Elems[0].(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected bare member to be qualified to field expr, got %T", tokenSet.Value.Elems[0])
	}
	if object, ok := first.Object.(*ast.Ident); !ok || object.Name != "TokenKind" || first.Field != "IF" {
		t.Fatalf("expected TokenKind.IF, got %#v", first)
	}
	decl := result.File.Decls[2].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr := ret.Value.(*ast.BinaryExpr)
	if got := result.ExprTypes[inExpr].String(); got != "bool" {
		t.Fatalf("expected membership expr type bool, got %s", got)
	}
}

func TestAnalyzeMembershipRejectsNonLiteralRightHandSide(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "membership_non_literal.elisa", `def keep(value: i64, xs: i64[2]) -> bool:
    return value in xs
`)

	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "membership operator requires a list literal or tokenset on the right-hand side") {
		t.Fatalf("expected membership rhs diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeExpectPatternSupportsAnonymousFieldShapeAndListRest(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "expect_field_shape_list_rest.elisa", `struct Block:
    stmts: int[3]

enum Decl:
    Program(name: int, params: int, block: Block)

def check(root: Decl) -> void:
    can Abort.Panic:
        expect root as Decl.Program(_, _, {stmts: [1, 2, ...]})
`)

	decl := result.File.Decls[2].(*ast.FuncDecl)
	canStmt := decl.Body[0].(*ast.CanStmt)
	expectStmt := canStmt.Body[0].(*ast.ExpectPatternStmt)
	variant := expectStmt.Patterns[0].(*ast.MatchVariantPattern)
	shape := variant.Args[2].Pattern.(*ast.MatchStructPattern)
	if shape.TypeName != "" || !shape.Brace {
		t.Fatalf("expected anonymous brace field shape, got %#v", shape)
	}
	if len(shape.ResolvedArgs) == 0 {
		t.Fatalf("expected semantic analysis to resolve anonymous field-shape args")
	}
}
