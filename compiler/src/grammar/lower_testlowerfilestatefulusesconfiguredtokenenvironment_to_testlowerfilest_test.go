package grammar

import (
	"elisacore/src/unparse"
	"strings"
	"testing"
)

func TestLowerFileStatefulUsesConfiguredTokenEnvironment(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar SMLFrontend over SMLToken using SMLParserState:
    cursor state
    token_kind SMLTokenKind
    eof SMLTokenKind.EOF
    token_field tag
    current peek_token
    advance bump_token
    expect expect_text
    expect_kind expect_token
    record_error note_error
    token:
        IDENT
        END "end"
    expr() -> Pascal.Stmt recover(SMLParseMessageKey.ExpectedExpression, until(.END, .EOF)):
        token = .IDENT
        "raw"
        return zeroed.cast[Pascal.Stmt]
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_token(SMLTokenKind.IDENT)",
		"state.expect_text(\"raw\")",
		"state.peek_token().tag == SMLTokenKind.END",
		"state.peek_token().tag == SMLTokenKind.EOF",
		"state.peek_token().tag != SMLTokenKind.EOF",
		"state.bump_token()",
		"state.note_error(SMLParseMessageKey.ExpectedExpression)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected configured token kind lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	for _, old := range []string{
		"current_token()",
		".kind",
		"advance_token()",
		"expect_kind(",
		"record_parse_error(",
	} {
		if strings.Contains(formatted, old) {
			t.Fatalf("expected configured token environment lowering to avoid %q, got:\n%s", old, formatted)
		}
	}
	if strings.Contains(formatted, " TokenKind.") || strings.Contains(formatted, "(TokenKind.") || strings.Contains(formatted, "== TokenKind.") || strings.Contains(formatted, "!= TokenKind.") {
		t.Fatalf("expected configured token environment lowering to avoid canonical TokenKind, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulAppliesGrammarEnvDefaults(t *testing.T) {
	file := parseGrammarTestFile(t, `grammarenv SMLGrammarEnv over SMLToken using SMLParserState:
    cursor state
    token_kind SMLTokenKind
    eof SMLTokenKind.EOF
    token_field tag
    current peek_token
    advance bump_token
    expect expect_text
    expect_kind expect_token
    record_error note_error

grammar SMLFrontend with SMLGrammarEnv:
    expect_kind require_token
    token:
        IDENT
        END "end"
    expr() -> Pascal.Stmt recover(SMLParseMessageKey.ExpectedExpression, until(.END, .EOF)):
        token = .IDENT
        "raw"
        return zeroed.cast[Pascal.Stmt]
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if strings.Contains(formatted, "grammarenv SMLGrammarEnv") {
		t.Fatalf("expected grammarenv declarations to be compile-time only in lowered output, got:\n%s", formatted)
	}
	for _, want := range []string{
		"grammar SMLFrontend with SMLGrammarEnv:",
		"def expr(state: mutable SMLParserState&) -> Pascal.Stmt",
		"state.require_token(SMLTokenKind.IDENT)",
		"state.expect_text(\"raw\")",
		"state.peek_token().tag == SMLTokenKind.END",
		"state.peek_token().tag != SMLTokenKind.EOF",
		"state.bump_token()",
		"state.note_error(SMLParseMessageKey.ExpectedExpression)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected grammarenv lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "state.expect_token(") {
		t.Fatalf("expected local expect_kind header to override grammarenv default, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulGrammarEnvTokenSourceImportsAliases(t *testing.T) {
	file := parseGrammarTestFile(t, `const enum DemoTokenKind of i16:
	EOF = 0
	IDENT = 1
	BEGIN = 2

grammar DemoTokens:
	token_kind DemoTokenKind
	token:
		IDENT
		BEGIN "begin"

grammarenv DemoEnv over Token using ParserState:
	cursor state
	token_kind DemoTokenKind
	tokens DemoTokens

grammar DemoGrammar with DemoEnv:
	program() -> Token:
		"begin"
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "state.expect_kind(DemoTokenKind.BEGIN)") {
		t.Fatalf("expected grammarenv token source aliases to rewrite literals to token kinds, got:\n%s", formatted)
	}
	if strings.Contains(formatted, `state.expect("begin")`) {
		t.Fatalf("expected grammarenv token source to avoid raw text expect, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulAppliesHeaderErrorTypeToImplicitProductions(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor parser
    error ParseError
    atom() -> Token:
        .IDENT(tok)
        return tok
    expr() -> Token:
        value = atom()
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def atom(parser: mutable ParserState&) -> Token error[ParseError]:",
		"def grammar_try_PascalFrontend_atom(parser: mutable ParserState&) -> (matched: bool, value: Token) error[ParseError]:",
		"def __grammar_try__PascalFrontend__atom(parser: mutable ParserState&) -> (matched: bool, committed: bool, value: Token) error[ParseError]:",
		"def expr(parser: mutable ParserState&) -> Token error[ParseError]:",
		"try __grammar_try__PascalFrontend__atom(parser)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected header-driven error lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulInjectsHeaderAllocIntoListsAndBareCalls(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor parser
    alloc alloc
    atom() -> darray[Token]:
        items = list(.IDENT)
        return items
    expr() -> darray[Token]:
        value = atom()
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def atom(parser: mutable ParserState&, alloc: mutable Arena&) -> darray[Token]:",
		"def grammar_try_PascalFrontend_atom(parser: mutable ParserState&, alloc: mutable Arena&) -> (matched: bool, value: darray[Token]):",
		"in alloc:",
		"__grammar_try__PascalFrontend__atom(parser, alloc)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected header-driven alloc lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "in parser.owner:") {
		t.Fatalf("expected header alloc to replace default parser.owner store, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulInjectsHeaderArgsIntoRecoveredProductionCalls(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor parser
    alloc alloc
	statement() -> Pascal.Stmt recover(ParseMessageKey.ExpectedStatement, until(";", token(TokenKind.EOF))):
        .IDENT(tok)
		return zeroed.cast[Pascal.Stmt]
	block() -> darray[Pascal.Stmt]:
        items = list(statement(), ";", until(token(TokenKind.EOF)))
        return items
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "statement(parser, alloc)") {
		t.Fatalf("expected recovered production call to thread implicit header args through the public production path, got:\n%s", formatted)
	}
	if strings.Contains(formatted, "= statement()") {
		t.Fatalf("expected recovered production call to avoid zero-arg public calls after header normalization, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulUsesClauseResolvesImportedProductionCalls(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalExprGrammar over Token using ParserState:
    cursor state
    alloc alloc
    atom() -> Token:
        .IDENT(tok)
        return tok

grammar PascalStmtGrammar over Token using ParserState uses PascalExprGrammar:
    cursor state
    alloc alloc
    statement() -> Token:
        value = atom()
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"grammar PascalStmtGrammar over Token using ParserState uses PascalExprGrammar:",
		"def atom(state: mutable ParserState&, alloc: mutable Arena&) -> Token:",
		"def statement(state: mutable ParserState&, alloc: mutable Arena&) -> Token:",
		"__grammar_try__PascalExprGrammar__atom(state, alloc)",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected uses-aware lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulUsesClauseResolvesImportedInfixTables(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalExprGrammar over Token using ParserState:
    cursor state
    alloc alloc
    token:
        PLUS "+"
    infix table ExprTable(additive):
        atom = token(TokenKind.IDENT)
        left additive(left = atom()):
            .PLUS -> right

grammar PascalStmtGrammar over Token using ParserState uses PascalExprGrammar:
    cursor state
    alloc alloc
    statement() -> Token:
        value = infix(ExprTable)
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"grammar PascalStmtGrammar over Token using ParserState uses PascalExprGrammar:",
		"def __grammar_try__PascalStmtGrammar____grammar_precedence_PascalStmtGrammar_statement_1_additive(state: mutable ParserState&, alloc: mutable Arena&)",
		"state.expect_kind(TokenKind.PLUS)",
		"state.expect_kind(TokenKind.IDENT)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected uses-imported infix table lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulUsesClauseResolvesImportedRecoveryPolicies(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalExprGrammar over Token using ParserState:
    cursor state
    token:
        SEMICOLON ";"
    recovery StatementRecovery:
        message ParseMessageKey.ExpectedStatement
        until .SEMICOLON, token(TokenKind.EOF)
        fallback zeroed.cast[Token]

grammar PascalStmtGrammar over Token using ParserState uses PascalExprGrammar:
    cursor state
	statement() -> Token:
		stmt = token(TokenKind.IDENT) recover StatementRecovery
        return stmt
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.record_parse_error(ParseMessageKey.ExpectedStatement)",
		"state.current_token().kind == TokenKind.SEMICOLON",
		"state.current_token().kind != TokenKind.EOF",
		"__grammar_recover_value_statement_PascalStmtGrammar_recover_value_11 <- zeroed.cast[Token]",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected uses-imported recovery policy lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulUsesClauseResolvesImportedTokenSets(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalBaseGrammar over Token using ParserState:
	cursor state
	token_kind TokenKind
	token_field kind
	current current_token
	advance advance_token
	expect expect
	expect_kind expect_kind
	token:
		EOF
		RPAREN ")"
	tokenset FileEndSync:
		EOF

grammar PascalArgsGrammar over Token using ParserState uses PascalBaseGrammar:
	cursor state
	token_kind TokenKind
	token_field kind
	current current_token
	advance advance_token
	expect expect
	expect_kind expect_kind
	token:
		IDENT
		LPAREN "("
		COMMA ","
	tokenset RParenOrFileSync:
		RPAREN
		FileEndSync
	grammarfn separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
		separated item by sep until(stop)
	args() -> darray[Token]:
		lookahead(.LPAREN | FileEndSync)
		values = apply separated_by(token(TokenKind.IDENT), RParenOrFileSync)
		eof_values = apply separated_by(token(TokenKind.IDENT), FileEndSync)
		return values
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.current_token().kind == TokenKind.EOF",
		"state.current_token().kind == TokenKind.RPAREN",
		"state.expect_kind(TokenKind.LPAREN)",
		"state.expect_kind(TokenKind.COMMA)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected uses-imported token set lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "TokenKind.FileEndSync") {
		t.Fatalf("expected imported token set refs to resolve before token-kind lowering, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulExposesTypedHeaderChannelDefaultsAsLocals(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor parser
    channel span: Span = combine_span($start.span, $end.span)
    span_of_ident() -> Span:
        .IDENT
        return span
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"$start: Token = parser.current_token()",
		"$end: mutable Token = $start",
		"span: mutable Span = combine_span($start.span, $end.span)",
		"$end <- parser.current_token()",
		"span <- combine_span($start.span, $end.span)",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected header channel lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	assignIndex := strings.Index(formatted, "span <- combine_span($start.span, $end.span)")
	returnIndex := strings.Index(formatted, "return (true, __grammar_committed_")
	if assignIndex < 0 || returnIndex < 0 || assignIndex > returnIndex {
		t.Fatalf("expected channel defaults to finalize before explicit success return, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulSeedsBareHeaderChannelFromProductionReturnType(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor parser
    channel node
    ident_expr() -> Token:
        .IDENT
        return node
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "node: mutable Token = zeroed.cast[Token]") {
		t.Fatalf("expected bare header channel to seed from the production return type, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "return (true, __grammar_committed_") {
		t.Fatalf("expected explicit return to see synthesized channel locals, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulAssignmentTermUpdatesChannelAndGuardsDefaultRefresh(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor parser
    channel span: Span = combine_span($start.span, $end.span)
    ident_span() -> Span:
        .IDENT(tok)
		span <- expr(tok.span)
        return span
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_channel_set_span_PascalFrontend_ident_span: mutable bool = false",
		"span <- __grammar_value_ident_span_PascalFrontend_value_",
		"__grammar_channel_set_span_PascalFrontend_ident_span <- true",
		"if (not __grammar_channel_set_span_PascalFrontend_ident_span):",
		"span <- combine_span($start.span, $end.span)",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected assignment-aware channel lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	assignIndex := strings.Index(formatted, "span <- __grammar_value_ident_span_PascalFrontend_value_")
	guardIndex := strings.Index(formatted, "if (not __grammar_channel_set_span_PascalFrontend_ident_span):")
	if assignIndex < 0 || guardIndex < 0 || assignIndex > guardIndex {
		t.Fatalf("expected explicit channel assignment before guarded default refresh, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulSynthesizesStructReturnFromChannels(t *testing.T) {
	file := parseGrammarTestFile(t, `struct BuiltSummary:
	items: darray[i64]
	checksum_total: i64
	arg_count: usize
	close_span: Span

grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel items: darray[i64] = []
	channel checksum_total: i64 = 0
	channel arg_count: usize = 0
	channel close_span: Span = $end.span
	summary() -> BuiltSummary:
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "return (true, __grammar_committed_") {
		t.Fatalf("expected synthesized struct return to produce success tuple, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "BuiltSummary(items:, checksum_total:, arg_count:, close_span:)") {
		t.Fatalf("expected synthesized struct return to assemble channel fields, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulSynthesizesGenericStructReturnFromChannels(t *testing.T) {
	file := parseGrammarTestFile(t, `struct BuiltSummary[T]:
	item: T
	count: usize

grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel item: i64 = 7
	channel count: usize = 1
	summary() -> BuiltSummary[i64]:
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "BuiltSummary[i64](item:, count:)") {
		t.Fatalf("expected synthesized generic struct return to preserve type args, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulSynthesizesStructReturnOnlyFromMatchingChannels(t *testing.T) {
	file := parseGrammarTestFile(t, `struct Tail:
	name_token: Token
	close_token: Token

grammar PerlFrontend over Token using ParserState:
	cursor parser
	channel node
	member_tail() -> Tail:
		token(TokenKind.IDENT)
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if strings.Contains(formatted, "Tail(node:)") {
		t.Fatalf("expected non-matching tree channel not to synthesize struct literal, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "zeroed.cast[Tail]") {
		t.Fatalf("expected unmatched struct channels to fall back to zeroed return value, got:\n%s", formatted)
	}
	if strings.Contains(formatted, "node: mutable Tail") {
		t.Fatalf("expected bare non-field channel not to infer whole struct type, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulInfersUntypedChannelsFromStructReturnFields(t *testing.T) {
	file := parseGrammarTestFile(t, `struct Tail:
	name_token: Token
	close_token: Token

grammar PerlFrontend over Token using ParserState:
	cursor parser
	channel name_token
	channel close_token
	member_tail() -> Tail:
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"name_token: mutable Token = zeroed.cast[Token]",
		"close_token: mutable Token = zeroed.cast[Token]",
		"Tail(name_token:, close_token:)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected struct field channel inference lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulUsesProductionLocalChannelsForStructSynthesis(t *testing.T) {
	file := parseGrammarTestFile(t, `struct Tail:
	name_token: Token
	close_token: Token

grammar PerlFrontend over Token using ParserState:
	cursor parser
	channel node
	member_tail() -> Tail:
		channel name_token
		channel close_token
		name_token <- token(TokenKind.IDENT)
		close_token <- expr(name_token)
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"name_token: mutable Token = zeroed.cast[Token]",
		"close_token: mutable Token = zeroed.cast[Token]",
		"Tail(name_token:, close_token:)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected production-local channel lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "node: mutable Tail") || strings.Contains(formatted, "Tail(node:") {
		t.Fatalf("expected grammar-wide node channel not to shape helper struct, got:\n%s", formatted)
	}
}
