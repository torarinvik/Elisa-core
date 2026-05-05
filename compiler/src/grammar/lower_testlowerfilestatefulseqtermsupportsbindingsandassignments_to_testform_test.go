package grammar

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

func TestLowerFileStatefulSeqTermSupportsBindingsAndAssignments(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    atom(state: mutable ParserState&) -> Token:
        value = seq(.IDENT(token), span <- expr(token.span), expr(token))
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"token = __grammar_token_",
		"token.span",
		"__grammar_seq_matched_",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered seq-term binding production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulLookaheadTermRestoresCursor(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor state
    atom() -> Token:
        lookahead(.IDENT)
        .IDENT(current)
        return current
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_lookahead_cursor_",
		"state.expect_kind(TokenKind.IDENT)",
		"state.cursor <- __grammar_lookahead_cursor_",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered lookahead production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulSeparatedTermBuildsListLoop(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    args(state: mutable ParserState&) -> darray[Token]:
        values = separated(token(TokenKind.IDENT), ",", until(")", token(TokenKind.EOF)))
        return values
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect(\",\")",
		"state.current_token().kind == token_kind_for_text(\")\")",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered separated-term production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulOptionalAndRepeatSuffixShorthandLowerThroughExistingTerms(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	    cursor state
	    items() -> darray[Token]:
	        values = .IDENT* until(")", token(TokenKind.EOF))
	        return values
	    maybe_item() -> Token?:
	        value = .IDENT?
	        return value
	`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.current_token().kind == token_kind_for_text(\")\")",
		"__grammar_optional_cursor_",
		"def maybe_item(state: mutable ParserState&) -> Token?:",
		"null as Token?",
		"as Token?",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered shorthand production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulProductionWithRecoverClauseRecordsAndSynchronizes(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    statement(state: mutable ParserState&) -> Pascal.Stmt recover(ParseMessageKey.ExpectedStatement, until(";", "end", token(TokenKind.EOF))):
        stmt = choice(state.assignment(), state.compound_statement())
        return stmt
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.record_parse_error(ParseMessageKey.ExpectedStatement)",
		"state.current_token().kind == token_kind_for_text(\";\")",
		"state.current_token().kind == token_kind_for_text(\"end\")",
		"state.current_token().kind != TokenKind.EOF",
		"state.advance_token()",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered recover production to contain %q, got:\n%s", want, formatted)
		}
	}
	if !strings.Contains(formatted, "def statement(state: mutable ParserState&) -> Pascal.Stmt:") {
		t.Fatalf("expected public recovered production, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulProductionWithNamedRecoverPolicyRecordsAndSynchronizes(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    recovery StatementRecovery:
        message ParseMessageKey.ExpectedStatement
        until ";", "end", token(TokenKind.EOF)
    statement(state: mutable ParserState&) -> Pascal.Stmt recover StatementRecovery:
        stmt = choice(state.assignment(), state.compound_statement())
        return stmt
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.record_parse_error(ParseMessageKey.ExpectedStatement)",
		"state.current_token().kind == token_kind_for_text(\";\")",
		"state.current_token().kind == token_kind_for_text(\"end\")",
		"state.current_token().kind != TokenKind.EOF",
		"state.advance_token()",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered named recover production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulProductionWithRecoverFallbackReturnsFallbackValue(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    statement(state: mutable ParserState&) -> Pascal.Stmt recover(ParseMessageKey.ExpectedStatement, until(";", token(TokenKind.EOF)), zeroed as Pascal.Stmt):
		stmt = choice(state.assignment(), state.compound_statement())
		return stmt
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.record_parse_error(ParseMessageKey.ExpectedStatement)",
		"state.advance_token()",
		"return zeroed as Pascal.Stmt",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered recover fallback production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulWhenTermBranchesWithoutRunningBothSides(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	    cursor state
	    body(flag: bool) -> Token:
	        node = when(flag, token(TokenKind.IDENT), token(TokenKind.INTEGER))
	        return node
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_when_cond_",
		"if __grammar_when_cond_",
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.INTEGER)",
		"mutable bool = false",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered when-term production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulOptionalTokenGateTermUsesCurrentToken(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	    cursor state
	    body() -> Token:
	        node = .IDENT? then .IDENT else .INTEGER
	        return node
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_when_cond_",
		"state.current_token().kind == TokenKind.IDENT",
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.INTEGER)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered optional token gate to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulChoicePromotesValueBranchToOptional(t *testing.T) {
	file := parseGrammarTestFile(t, `struct Token:
    kind: TokenKind

struct ParserState:
    cursor: mutable usize

const enum TokenKind of i16:
    EOF = 0
    IDENT = 1

grammar DemoGrammar over Token using ParserState:
    cursor state
    token_kind TokenKind
    eof TokenKind.EOF
    token_field kind
    token:
        IDENT
    atom() -> Token?:
        value = choice(.IDENT, expr[Token?](null))
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "as Token?") {
		t.Fatalf("expected lowered choice to promote Token branch to Token?, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulTermLevelRecoverClauseRecordsAndContinues(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	    cursor state
	    statement() -> Token:
	        stmt = token(TokenKind.IDENT) recover(ParseMessageKey.ExpectedStatement, until(";", token(TokenKind.EOF)), zeroed as Token)
	        return stmt
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.record_parse_error(ParseMessageKey.ExpectedStatement)",
		"state.current_token().kind == token_kind_for_text(\";\")",
		"state.advance_token()",
		"zeroed as Token",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered term-level recover production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulTermLevelNamedRecoverPolicyRecordsAndContinues(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	    cursor state
	    recovery StatementRecovery:
	        message ParseMessageKey.ExpectedStatement
	        until ";", token(TokenKind.EOF)
	        fallback zeroed as Token
	    statement() -> Token:
	        stmt = token(TokenKind.IDENT) recover StatementRecovery
	        return stmt
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.record_parse_error(ParseMessageKey.ExpectedStatement)",
		"state.current_token().kind == token_kind_for_text(\";\")",
		"state.advance_token()",
		"zeroed as Token",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered named term-level recover production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulReturnlessRecoverClauseUsesVoidTupleValue(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	    cursor state
	    block_tail() recover(ParseMessageKey.ExpectedStatement, until("end", token(TokenKind.EOF))):
	        guard(state.current_token().kind == TokenKind.END or state.current_token().kind == TokenKind.EOF)
`)
	lowered := LowerFile(file)
	var publicFn *ast.FuncDecl
	var publicTryFn *ast.FuncDecl
	var internalTryFn *ast.FuncDecl
	for _, decl := range lowered.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		switch fn.Name {
		case "block_tail":
			publicFn = fn
		case "grammar_try_PascalFrontend_block_tail":
			publicTryFn = fn
		case "__grammar_try__PascalFrontend__block_tail":
			internalTryFn = fn
		}
	}
	if publicFn == nil || publicTryFn == nil || internalTryFn == nil {
		t.Fatalf("expected lowered returnless recover helpers, got:\n%s", unparse.FormatFile(lowered))
	}
	for _, tc := range []struct {
		name       string
		fn         *ast.FuncDecl
		wantReturn string
		wantBody   string
	}{
		{name: "public", fn: publicFn, wantBody: "record_parse_error(ParseMessageKey.ExpectedStatement)"},
		{name: "public try", fn: publicTryFn, wantReturn: "value: bool"},
		{name: "internal try", fn: internalTryFn, wantReturn: "value: bool", wantBody: "zeroed as bool"},
	} {
		formatted := unparse.FormatDecl(tc.fn)
		if tc.wantReturn != "" && !strings.Contains(formatted, tc.wantReturn) {
			t.Fatalf("expected lowered %s helper to contain %q, got:\n%s", tc.name, tc.wantReturn, formatted)
		}
		if tc.wantBody != "" && !strings.Contains(formatted, tc.wantBody) {
			t.Fatalf("expected lowered %s helper to contain %q, got:\n%s", tc.name, tc.wantBody, formatted)
		}
		if strings.Contains(formatted, "<invalid>") {
			t.Fatalf("expected lowered %s helper to avoid invalid placeholder types, got:\n%s", tc.name, formatted)
		}
	}
}
func TestLowerFileStatefulListCallsRecoveredProductionDirectly(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    statement(state: mutable ParserState&) -> Pascal.Stmt recover(ParseMessageKey.ExpectedStatement, until(";", "end", token(TokenKind.EOF))):
        stmt = choice(state.assignment(), state.compound_statement())
        return stmt
    block(state: mutable ParserState&) -> Pascal.Block:
        statements = list(state.statement(), ";", until("end", token(TokenKind.EOF)))
        return zeroed as Pascal.Block
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "value_statement_PascalFrontend") {
		t.Fatalf("expected recovered production call to lower through public statement() path, got:\n%s", formatted)
	}
	if strings.Contains(formatted, "__grammar_try__PascalFrontend__statement") && strings.Contains(formatted, "items") {
		itemIndex := strings.Index(formatted, "__grammar_try__PascalFrontend__statement")
		blockIndex := strings.LastIndex(formatted, "statements =")
		if blockIndex >= 0 && itemIndex > blockIndex {
			t.Fatalf("expected block list to avoid try-call for recovered production, got:\n%s", formatted)
		}
	}
}
func TestLowerFileStatefulPrecedenceTermBuildsLoopAndAssignsLeft(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        precedence(left = token(TokenKind.IDENT)):
			"+" right = token(TokenKind.IDENT) -> right
			"-" right = token(TokenKind.IDENT) -> right
        return left
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"left: mutable Token",
		"while (not __grammar_precedence_stop_expression_PascalFrontend",
		"state.expect(\"+\")",
		"state.expect_kind(TokenKind.IDENT)",
		"right = __grammar_token_expression_PascalFrontend_token_",
		"left <- right",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered precedence production to contain %q, got:\n%s", want, formatted)
		}
	}
	plusIndex := strings.Index(formatted, "state.expect(\"+\")")
	minusIndex := strings.Index(formatted, "state.expect(\"-\")")
	if plusIndex < 0 || minusIndex < 0 || plusIndex >= minusIndex {
		t.Fatalf("expected precedence arms to lower in declaration order, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulInfixTableUseBuildsLoopAndAssignsLeft(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    infix table ExprTable(additive):
        atom = token(TokenKind.IDENT)
		left multiplicative(left = atom()):
			op = choice("*", "/") -> right
		left additive(left = multiplicative()):
			op = choice("+", "-") -> right
    expression(state: mutable ParserState&) -> Token:
        result = infix(ExprTable)
        return result
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"while (not __grammar_precedence_stop___grammar_precedence_PascalFrontend_expression_1_additive",
		"state.expect(\"*\")",
		"state.expect(\"+\")",
		"state.expect_kind(TokenKind.IDENT)",
		"result = __grammar_value_expression_PascalFrontend_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered infix table production to contain %q, got:\n%s", want, formatted)
		}
	}
	if !strings.Contains(formatted, "def __grammar_try__PascalFrontend____grammar_precedence_PascalFrontend_expression_1_additive") {
		t.Fatalf("expected infix table use to lower through generated precedence helpers, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulInfixTableExpandsGrammarAliases(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    token:
        IDENT
        INTEGER
    grammar alias atom_choice:
        choice:
            .IDENT
            .INTEGER
    infix table ExprTable(additive):
        atom = atom_choice
        left additive(left = atom()):
            op = "+" -> right
    expression(state: mutable ParserState&) -> Token:
        result = infix(ExprTable)
        return result
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.INTEGER)",
		"state.expect(\"+\")",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected infix table alias expansion lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulInfixTableExpandsGrammarTypes(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    token:
        IDENT
    grammar type required_atom[T](item: grammar -> T, message: expr) -> grammar -> T:
        required(item, message)
    infix table ExprTable(additive):
        atom = required_atom(item: .IDENT, message: expr(ParseMessageKey.ExpectedName))
        left additive(left = atom()):
            op = "+" -> right
    expression(state: mutable ParserState&) -> Token:
        result = infix(ExprTable)
        return result
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.record_parse_error(ParseMessageKey.ExpectedName)",
		"state.expect(\"+\")",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected infix table grammar type expansion lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulAssociativeInlinePrecedenceUsesGeneratedHelper(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        precedence right(left = token(TokenKind.IDENT)):
            "^" -> right
        return left
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def __grammar_try__PascalFrontend____grammar_precedence_PascalFrontend_expression_1_inline(state: mutable ParserState&)",
		"left = __grammar_value_expression_PascalFrontend_value_",
		"right = __grammar_value___grammar_precedence_PascalFrontend_expression_1_inline_PascalFrontend_value_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered associative inline precedence to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulAssociativityAnnotatedInfixTableSynthesizesRightBinding(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    infix table ExprTable(power):
        atom = token(TokenKind.IDENT)
        right power(left = atom()):
            "^" -> right
    expression(state: mutable ParserState&) -> Token:
        result = infix(ExprTable)
        return result
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_try__PascalFrontend____grammar_precedence_PascalFrontend_expression_1_power(state)",
		"right = __grammar_value___grammar_precedence_PascalFrontend_expression_1_power_PascalFrontend_value_",
		"__grammar_precedence_stop___grammar_precedence_PascalFrontend_expression_1_power",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered associative infix table to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileAppendsLoweredFunctionsAfterGrammarDecl(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    program() -> Pascal.Decl:
        expect("program")
`)
	lowered := LowerFile(file)
	if len(lowered.Decls) != 2 {
		t.Fatalf("expected grammar decl plus lowered function, got %d decls", len(lowered.Decls))
	}
	if _, ok := lowered.Decls[0].(*ast.GrammarDecl); !ok {
		t.Fatalf("expected first decl to remain grammar decl, got %T", lowered.Decls[0])
	}
	fn, ok := lowered.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected lowered function after grammar decl, got %T", lowered.Decls[1])
	}
	if fn.Name != "program" {
		t.Fatalf("expected lowered function name program, got %q", fn.Name)
	}
}
func TestFormatFilePreservesLexerHelperDecl(t *testing.T) {
	file := parseGrammarTestFile(t, `lexer DemoLex:
    token_kind DemoTokenKind
    mode_enum DemoLexMode
    mode NORMAL
    mode STRING
    charclass digit = '0'..'9'
    charclass ident = '_' | digit
    keywords fallback IDENT:
        "if" -> IF
    literals longest fallback EOF:
        "==" -> EQEQ
        "=" -> EQ
`)
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"lexer DemoLex:",
		"token_kind DemoTokenKind",
		"mode_enum DemoLexMode",
		"mode NORMAL",
		"mode STRING",
		"charclass digit = '0'..'9'",
		"charclass ident = '_' | digit",
		"keywords fallback IDENT:",
		`"if" -> IF`,
		"literals longest fallback EOF:",
		`"==" -> EQEQ`,
		`"=" -> EQ`,
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted lexer decl to contain %q, got:\n%s", want, formatted)
		}
	}
}
