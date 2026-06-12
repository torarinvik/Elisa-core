package grammar

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

func TestLowerFileStatefulBoundPrecedenceLevelsBuildNestedLoops(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        atom = token(TokenKind.IDENT)
        term_level = precedence(term_left = atom):
			op = "+" right = atom -> right
        expr_level = precedence(expr_left = term_level):
			op = "-" right = term_level -> right
        return expr_level
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"atom = __grammar_token_expression_PascalFrontend_token_",
		"term_left: mutable Token = __grammar_value_expression_PascalFrontend_value_",
		"expr_left: mutable Token = __grammar_value_expression_PascalFrontend_value_",
		"op = __grammar_token_expression_PascalFrontend_token_",
		"right = atom",
		"right = term_level",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered bound precedence levels to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulNamedPrecedenceLevelsCallLowerLevelsAsParsers(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
		result = precedence(additive):
			atom = token(TokenKind.IDENT)
			multiplicative(term_left = token(TokenKind.IDENT)):
				op = "+" right = atom() -> right
			additive(expr_left = multiplicative()):
				op = "-" right = multiplicative() -> right
		return result
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def __grammar_try__PascalFrontend____grammar_precedence_PascalFrontend_expression_1_atom(state: mutable ParserState&)",
		"def __grammar_try__PascalFrontend____grammar_precedence_PascalFrontend_expression_1_multiplicative(state: mutable ParserState&)",
		"def __grammar_try__PascalFrontend____grammar_precedence_PascalFrontend_expression_1_additive(state: mutable ParserState&)",
		"__grammar_try__PascalFrontend____grammar_precedence_PascalFrontend_expression_1_atom(state)",
		"__grammar_try__PascalFrontend____grammar_precedence_PascalFrontend_expression_1_multiplicative(state)",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered named precedence levels to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulNamedPrecedenceLevelsAllowRecursiveHelperCalls(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
		result = precedence(power):
			atom = token(TokenKind.IDENT)
			power(left = atom()):
				"^" right = power() -> right
		return result
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def __grammar_try__PascalFrontend____grammar_precedence_PascalFrontend_expression_1_power(state: mutable ParserState&)",
		"__grammar_try__PascalFrontend____grammar_precedence_PascalFrontend_expression_1_power(state)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered recursive named precedence helper to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerDeclBuildsFieldCallsForMemberStyleGrammarCallees(t *testing.T) {
	decl := parseGrammarTestDecl(t, `grammar PascalFrontend:
    program(state: mutable ParserState&) -> Pascal.Decl:
        state.expect("program")
        name = state.parse_ident()
`)
	funcs := LowerDecl(decl)
	if len(funcs) != 1 {
		t.Fatalf("expected one lowered function, got %d", len(funcs))
	}
	firstExpr, ok := funcs[0].Body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected first lowered stmt to be expr stmt, got %T", funcs[0].Body[0])
	}
	call, ok := firstExpr.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected first lowered expr to be call, got %T", firstExpr.Expr)
	}
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected member-style call to lower to field expr, got %T", call.Func)
	}
	receiver, ok := field.Object.(*ast.Ident)
	if !ok || receiver.Name != "state" || field.Field != "expect" {
		t.Fatalf("expected lowered field call state.expect, got %T %#v", field.Object, field)
	}
	formatted := unparse.FormatDecl(funcs[0])
	for _, want := range []string{
		"state.expect(\"program\")",
		"name = state.parse_ident()",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered function to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerDeclPreservesExplicitReturnWithoutPlaceholder(t *testing.T) {
	decl := parseGrammarTestDecl(t, `grammar Demo:
    produce() -> i64:
        value = helper()
        return value + 1
`)
	funcs := LowerDecl(decl)
	if len(funcs) != 1 {
		t.Fatalf("expected one lowered function, got %d", len(funcs))
	}
	fn := funcs[0]
	if len(fn.Body) != 2 {
		t.Fatalf("expected binding plus explicit return, got %d statements", len(fn.Body))
	}
	if _, ok := fn.Body[1].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected second lowered stmt to be explicit return, got %T", fn.Body[1])
	}
	formatted := unparse.FormatDecl(fn)
	if !strings.Contains(formatted, "return (value + 1)") {
		t.Fatalf("expected lowered function to preserve explicit return, got:\n%s", formatted)
	}
	if strings.Contains(formatted, "zeroed") {
		t.Fatalf("expected explicit return to suppress placeholder zeroed return, got:\n%s", formatted)
	}
}
func TestLowerDeclPreservesReturnSeqBlockTermWithoutPlaceholder(t *testing.T) {
	decl := parseGrammarTestDecl(t, `grammar Demo:
    produce() -> i64:
        return seq:
            value = expr(helper())
            expr(value + 1)
`)
	funcs := LowerDecl(decl)
	if len(funcs) != 1 {
		t.Fatalf("expected one lowered function, got %d", len(funcs))
	}
	fn := funcs[0]
	if len(fn.Body) != 1 {
		t.Fatalf("expected explicit return seq to lower into one block stmt, got %d statements", len(fn.Body))
	}
	if _, ok := fn.Body[0].(*ast.CanStmt); !ok {
		t.Fatalf("expected lowered explicit return seq to use a block stmt, got %T", fn.Body[0])
	}
	formatted := unparse.FormatDecl(fn)
	for _, want := range []string{
		"value = helper()",
		"return (value + 1)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered return seq to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "zeroed") {
		t.Fatalf("expected explicit return seq to suppress placeholder zeroed return, got:\n%s", formatted)
	}
}
func TestLowerFileReturnSeqResolvesNestedFlatRepeatUntil(t *testing.T) {
	file := parseGrammarTestFile(t, `struct Token:
	kind: TokenKind

struct ParserState:
	cursor: mutable usize

const enum TokenKind of i16:
	EOF = 0
	RESOURCESTRING = 1
	IDENT = 2
	TYPE = 3

grammarenv DemoEnv over Token using ParserState:
	cursor state
	token_kind TokenKind
	eof TokenKind.EOF
	token_field kind
	current current_token
	advance advance_token
	expect_kind expect_kind

grammar Demo with DemoEnv:
	token:
		RESOURCESTRING
		IDENT
		TYPE
	tokenset SectionSync:
		TYPE
		token(TokenKind.EOF)
	item() -> darray[Token]:
		token = .IDENT
		return singleton[Token](token)
	section() -> darray[Token]:
		return seq:
			.RESOURCESTRING
			flatrepeat item() until(SectionSync)
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.current_token().kind == TokenKind.TYPE",
		"state.current_token().kind == TokenKind.EOF",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered return seq bracket while until to contain %q, got:\n%s", want, formatted)
		}
	}
}
