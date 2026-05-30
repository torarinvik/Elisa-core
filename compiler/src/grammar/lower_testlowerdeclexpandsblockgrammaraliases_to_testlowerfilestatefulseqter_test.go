package grammar

import (
	"elisacore/src/unparse"
	"strings"
	"testing"
)

func TestLowerDeclExpandsBlockGrammarAliases(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalAtomGrammar over Token using ParserState:
    cursor state
    token_kind TokenKind
    token_field kind
    current current_token
    advance advance_token
    expect expect
    expect_kind expect_kind
    token:
        IDENT
        INTEGER
    grammar alias atom_choice:
        choice:
            .IDENT
            .INTEGER
    atom() -> Token:
        value = atom_choice
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.INTEGER)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered block grammar alias output to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerDeclExpandsParameterizedGrammarAliases(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalArgsGrammar over Token using ParserState:
	cursor state
	token_kind TokenKind
	token_field kind
	current current_token
	advance advance_token
	expect expect
	expect_kind expect_kind
	token:
		COMMA ","
		SEMICOLON ";"
		RPAREN ")"
	tokenset RParenSync:
		RPAREN
		token(TokenKind.EOF)
	grammar type separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
		separated item by sep until(stop)
	grammar alias ident_items(stop: tokenset, sep: grammar = .COMMA):
		token(TokenKind.IDENT) |> separated_by(stop: stop, sep: sep)
	args() -> darray[Token]:
		values = ident_items(stop: RParenSync)
		return values
	semis() -> darray[Token]:
		values = ident_items(stop: RParenSync, sep: .SEMICOLON)
		return values
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.COMMA)",
		"state.expect_kind(TokenKind.SEMICOLON)",
		"state.current_token().kind == TokenKind.RPAREN",
		"state.current_token().kind == TokenKind.EOF",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered parameterized alias output to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerDeclImportsGrammarAliasesFromHelperGrammar(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalArgsListGrammar over Token using ParserState:
    cursor state
    token_kind TokenKind
    token_field kind
    current current_token
    advance advance_token
    expect expect
    expect_kind expect_kind
    token:
        COMMA ","
        IDENT
        RPAREN ")"
    tokenset RParenSync:
        RPAREN
        token(TokenKind.EOF)
    grammar type separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
        separated item by sep until(stop)
    grammar alias ident_args = separated_by(item: token(TokenKind.IDENT), stop: RParenSync)
grammar PascalArgsGrammar over Token using ParserState uses PascalArgsListGrammar:
    cursor state
    token_kind TokenKind
    token_field kind
    current current_token
    advance advance_token
    expect expect
    expect_kind expect_kind
    args() -> darray[Token]:
        values = ident_args
        return values
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.COMMA)",
		"state.current_token().kind == TokenKind.RPAREN",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected imported grammar alias output to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerDeclImportsParameterizedGrammarAliasesFromHelperGrammar(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalListGrammar over Token using ParserState:
	cursor state
	token_kind TokenKind
	token_field kind
	current current_token
	advance advance_token
	expect expect
	expect_kind expect_kind
	token:
		COMMA ","
		SEMICOLON ";"
	grammar type separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
		separated item by sep until(stop)
	grammar alias token_items(item: grammar, stop: tokenset, sep: grammar = .COMMA):
		item |> separated_by(stop: stop, sep: sep)

grammar PascalArgsGrammar over Token using ParserState uses PascalListGrammar:
	cursor state
	token_kind TokenKind
	token_field kind
	current current_token
	advance advance_token
	expect expect
	expect_kind expect_kind
	token:
		IDENT
		RPAREN ")"
	tokenset RParenSync:
		RPAREN
		token(TokenKind.EOF)
	args() -> darray[Token]:
		values = token_items(item: token(TokenKind.IDENT), stop: RParenSync, sep: .SEMICOLON)
		return values
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.SEMICOLON)",
		"state.current_token().kind == TokenKind.RPAREN",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected imported parameterized alias lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerDeclExpandsParameterizedAliasConcatWithGrammarTypeContinuation(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalItemsGrammar over Token using ParserState:
	cursor state
	token_kind TokenKind
	token_field kind
	current current_token
	advance advance_token
	expect expect
	expect_kind expect_kind
	token:
		IDENT
		SEMICOLON ";"
		END "end"
	tokenset GroupStop:
		SEMICOLON
		END
		token(TokenKind.EOF)
	tokenset EndSync:
		END
		token(TokenKind.EOF)
	grammar type group_tail(sep: grammar) -> grammar -> darray[Token]:
		seq:
			sep
			group()
	grammar alias group_items(stop: tokenset, sep: grammar = .SEMICOLON):
		group() + [group_tail(sep)] while token in tokens != [stop]
	group() -> darray[Token]:
		values = list(token(TokenKind.IDENT), until(GroupStop))
		return values
	items() -> darray[Token]:
		values = group_items(stop: EndSync)
		return values
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def items(state: mutable ParserState&) -> darray[Token]:",
		"state.expect_kind(TokenKind.SEMICOLON)",
		"flatrepeat_group",
		"state.current_token().kind == TokenKind.END",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered concat+grammar-type alias output to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerDeclImportsGrammarFnsFromHelperOnlyGrammar(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalListGrammar over Token using ParserState:
    cursor state
    token_kind TokenKind
    token_field kind
    current current_token
    advance advance_token
    expect expect
    expect_kind expect_kind
    token:
        COMMA ","
        SEMICOLON ";"
    grammarfn separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
        separated item by sep until(stop)

grammar PascalArgsGrammar over Token using ParserState uses PascalListGrammar:
    cursor state
    token_kind TokenKind
    token_field kind
    current current_token
    advance advance_token
    expect expect
    expect_kind expect_kind
    token:
        IDENT
        RPAREN ")"
    tokenset RParenSync:
        RPAREN
        token(TokenKind.EOF)
    args() -> darray[Token]:
        values = apply separated_by(item: token(TokenKind.IDENT), stop: RParenSync)
        return values
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.COMMA)",
		"state.current_token().kind == TokenKind.RPAREN",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected helper-only grammar import lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Count(formatted, "values = apply separated_by") != 1 {
		t.Fatalf("expected helper grammar function call to remain only in the source grammar declaration, got:\n%s", formatted)
	}
}
func TestLowerDeclExpandsGrammarFnExprParams(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalExprRecoveryGrammar over Token using ParserState:
    cursor state
    token_kind TokenKind
    token_field kind
    current current_token
    advance advance_token
    expect expect
    expect_kind expect_kind
    record_error record_parse_error
    token:
        IDENT
        RPAREN ")"
    tokenset RParenSync:
        RPAREN
        token(TokenKind.EOF)
    grammarfn recovered_item[T](item: grammar -> T, message: expr, stop: tokenset, fallback: expr) -> grammar -> T:
        item recover(message, until(stop), fallback)
    name() -> Token:
        value = apply recovered_item(item: token(TokenKind.IDENT), message: expr(ParseMessageKey.ExpectedName), stop: RParenSync, fallback: expr(state.current_token()))
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.record_parse_error(ParseMessageKey.ExpectedName)",
		"__grammar_recover_value_name_PascalExprRecoveryGrammar_recover_value_",
		"state.current_token()",
		"state.current_token().kind == TokenKind.RPAREN",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected grammarfn expr-param lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerDeclExpandsParameterizedGrammarAliasExprParams(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalExprRecoveryGrammar over Token using ParserState:
	cursor state
	token_kind TokenKind
	token_field kind
	current current_token
	advance advance_token
	expect expect
	expect_kind expect_kind
	record_error record_parse_error
	token:
		IDENT
		RPAREN ")"
	tokenset RParenSync:
		RPAREN
		token(TokenKind.EOF)
	grammar type recovered_item[T](item: grammar -> T, message: expr, stop: tokenset, fallback: expr) -> grammar -> T:
		item recover(message, until(stop), fallback)
	grammar alias safe_ident(message: expr, stop: tokenset, fallback: expr):
		token(TokenKind.IDENT) |> recovered_item(message: message, stop: stop, fallback: fallback)
	name() -> Token:
		value = safe_ident(message: expr(ParseMessageKey.ExpectedName), stop: RParenSync, fallback: expr(state.current_token()))
		return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.record_parse_error(ParseMessageKey.ExpectedName)",
		"__grammar_recover_value_name_PascalExprRecoveryGrammar_recover_value_",
		"state.current_token()",
		"state.current_token().kind == TokenKind.RPAREN",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected parameterized grammar alias expr-param lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerDeclPreservesRepeatAndSeparatedAsOrdinaryCalls(t *testing.T) {
	decl := parseGrammarTestDecl(t, `grammar PascalFrontend:
    block() -> darray[Pascal.Stmt]:
        items = repeat(statement(), until("end", token(TokenKind.EOF)))
    args() -> darray[Pascal.Expr]:
        values = separated(expression(), ",", until(")", token(TokenKind.EOF)))
`)
	funcs := LowerDecl(decl)
	if len(funcs) != 2 {
		t.Fatalf("expected two lowered functions, got %d", len(funcs))
	}
	blockFormatted := unparse.FormatDecl(funcs[0])
	if !strings.Contains(blockFormatted, "items = repeat(statement(), until(\"end\", token(TokenKind.EOF)))") {
		t.Fatalf("expected lowered repeat term to stay as ordinary call, got:\n%s", blockFormatted)
	}
	argsFormatted := unparse.FormatDecl(funcs[1])
	if !strings.Contains(argsFormatted, "values = separated(expression(), expect(\",\"), until(\")\", token(TokenKind.EOF)))") {
		t.Fatalf("expected lowered separated term to stay as ordinary call, got:\n%s", argsFormatted)
	}
}
func TestLowerDeclRoutesBareTokenTermsThroughStateReceiverWhenPresent(t *testing.T) {
	decl := parseGrammarTestDecl(t, `grammar PascalFrontend:
    expression_tail(state: mutable ParserState&) -> Pascal.Expr:
        op = choice("+", "-")
        state.term()
`)
	funcs := LowerDecl(decl)
	if len(funcs) != 1 {
		t.Fatalf("expected one lowered function, got %d", len(funcs))
	}
	formatted := unparse.FormatDecl(funcs[0])
	for _, want := range []string{
		"op = choice(state.expect(\"+\"), state.expect(\"-\"))",
		"state.term()",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered function to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerDeclPreservesWhenTermAsTernaryExpr(t *testing.T) {
	decl := parseGrammarTestDecl(t, `grammar PascalFrontend:
    body(flag: bool) -> Pascal.Stmt:
        node = when(flag, expr(make_then()), expr(make_else()))
        return node
`)
	funcs := LowerDecl(decl)
	if len(funcs) != 1 {
		t.Fatalf("expected one lowered function, got %d", len(funcs))
	}
	formatted := unparse.FormatDecl(funcs[0])
	if !strings.Contains(formatted, "node = (make_then() if flag else make_else())") {
		t.Fatalf("expected lowered when term to become ternary expr, got:\n%s", formatted)
	}
}
func TestLowerDeclRoutesTokenKindMatcherThroughStateReceiverWhenPresent(t *testing.T) {
	decl := parseGrammarTestDecl(t, `grammar PascalFrontend:
    assignment(state: mutable ParserState&) -> Pascal.Stmt:
        name_token = token(TokenKind.IDENT)
        state.expect(":=")
        return zeroed.cast[Pascal.Stmt]
`)
	funcs := LowerDecl(decl)
	if len(funcs) != 1 {
		t.Fatalf("expected one lowered function, got %d", len(funcs))
	}
	formatted := unparse.FormatDecl(funcs[0])
	for _, want := range []string{
		"name_token = state.expect_kind(TokenKind.IDENT)",
		"state.expect(\":=\")",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered function to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulListCollectsValuesIntoDarray(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    collect(state: mutable ParserState&) -> Token:
        items = list(token(TokenKind.IDENT))
        return items[0u]
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"mutable darray[Token] = zeroed.cast[darray[Token]]",
		"<- []",
		".push(",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered stateful list helper to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulListChecksUntilStopSetBeforeParsingNextItem(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    collect(state: mutable ParserState&) -> darray[Token]:
        items = list(token(TokenKind.IDENT), until("end", token(TokenKind.EOF)))
        return items
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.current_token().kind == token_kind_for_text(\"end\")",
		"state.current_token().kind == TokenKind.EOF",
		"stop_collect_PascalFrontend",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered stateful list helper to contain %q, got:\n%s", want, formatted)
		}
	}
	endIndex := strings.Index(formatted, "state.current_token().kind == token_kind_for_text(\"end\")")
	itemIndex := strings.Index(formatted, "state.expect_kind(TokenKind.IDENT)")
	if endIndex < 0 || itemIndex < 0 || endIndex > itemIndex {
		t.Fatalf("expected stop-set check before item parse, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulFlatRepeatTermFlattensNestedLists(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    group(state: mutable ParserState&) -> darray[Token]:
        values = list(token(TokenKind.IDENT), until(";", token(TokenKind.EOF)))
        return values
    groups(state: mutable ParserState&) -> darray[Token]:
        values = flatrepeat(group(), until("end", token(TokenKind.EOF)))
        return values
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"flatrepeat_group",
		"flatrepeat_index",
		".count",
		".push(",
		"state.current_token().kind == token_kind_for_text(\"end\")",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered flatrepeat production to contain %q, got:\n%s", want, formatted)
		}
	}
	if !strings.Contains(formatted, "def groups(state: mutable ParserState&) -> darray[Token]:") {
		t.Fatalf("expected lowered groups production, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulBracketWhileTermUsesFlatRepeatLowering(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    group(state: mutable ParserState&) -> darray[Token]:
        values = list(token(TokenKind.IDENT), until(";", token(TokenKind.EOF)))
        return values
    groups(state: mutable ParserState&) -> darray[Token]:
        values = [group()] while token in tokens != ["end", token(TokenKind.EOF)]
        return values
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"flatrepeat_group",
		"flatrepeat_index",
		".count",
		".push(",
		"state.current_token().kind == token_kind_for_text(\"end\")",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered bracket while production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulRequiredTermRecordsSpecificParseError(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    ident(state: mutable ParserState&) -> Token:
        name = required(.IDENT, ParseMessageKey.ExpectedProgramName)
        required(")", ParseMessageKey.ExpectedRightParen)
        return name
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.record_parse_error(ParseMessageKey.ExpectedProgramName)",
		"state.record_parse_error(ParseMessageKey.ExpectedRightParen)",
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect(\")\")",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered required-term production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulDelimitedTermPreservesBodyValueAndRecordsCloseError(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    atom(state: mutable ParserState&) -> Token:
        value = delimited("(", .IDENT, ")", ParseMessageKey.ExpectedRightParen)
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect(\"(\")",
		"state.expect_kind(TokenKind.IDENT)",
		"state.record_parse_error(ParseMessageKey.ExpectedRightParen)",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered delimited-term production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulSeqTermWorksInsideOptionalAndReturnsLastValue(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    else_clause(state: mutable ParserState&) -> Token:
        value = optional(seq("else", required(.IDENT, ParseMessageKey.ExpectedProgramName)))
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect(\"else\")",
		"state.expect_kind(TokenKind.IDENT)",
		"state.record_parse_error(ParseMessageKey.ExpectedProgramName)",
		"__grammar_seq_matched_",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered seq-term production to contain %q, got:\n%s", want, formatted)
		}
	}
}
