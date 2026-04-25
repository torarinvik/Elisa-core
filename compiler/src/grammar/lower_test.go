package grammar

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/parser"
	"llcontext/src/unparse"
)

func parseGrammarTestDecl(t *testing.T, src string) *ast.GrammarDecl {
	t.Helper()
	l := lexer.New("grammar_lower_test.llcontext", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("grammar_lower_test.llcontext")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	return decl
}

func parseGrammarTestFile(t *testing.T, src string) *ast.File {
	t.Helper()
	l := lexer.New("grammar_lower_test.llcontext", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("grammar_lower_test.llcontext")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	return file
}

func TestLowerDeclCarriesGenericHeaderAndBindsTokens(t *testing.T) {
	decl := parseGrammarTestDecl(t, `grammar Pascal[T, region parse_region]:
    program(input: T) -> Pascal.Program:
        keyword = "program"
        name = parse_ident()
        expect(";")
`)
	funcs := LowerDecl(decl)
	if len(funcs) != 1 {
		t.Fatalf("expected one lowered function, got %d", len(funcs))
	}
	fn := funcs[0]
	if fn.Name != "program" {
		t.Fatalf("expected lowered function name program, got %q", fn.Name)
	}
	if len(fn.TypeParams) != 1 || fn.TypeParams[0] != "T" {
		t.Fatalf("expected lowered type params [T], got %#v", fn.TypeParams)
	}
	if len(fn.RegionParams) != 1 || fn.RegionParams[0] != "parse_region" {
		t.Fatalf("expected lowered region params [parse_region], got %#v", fn.RegionParams)
	}
	if len(fn.Body) != 4 {
		t.Fatalf("expected three lowered terms plus return, got %d statements", len(fn.Body))
	}
	keyword, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected first lowered stmt to be binding, got %T", fn.Body[0])
	}
	call, ok := keyword.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected keyword binding to lower to call expr, got %T", keyword.Value)
	}
	callee, ok := call.Func.(*ast.Ident)
	if !ok || callee.Name != "expect" {
		t.Fatalf("expected token term to lower to expect call, got %T %#v", call.Func, call.Func)
	}
	formatted := unparse.FormatDecl(fn)
	for _, want := range []string{
		"def program[T, region parse_region](input: T) -> Pascal.Program:",
		"keyword = expect(\"program\")",
		"name = parse_ident()",
		"expect(\";\")",
		"return zeroed",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered function to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerDeclPreservesChoiceOptionalAndListAsOrdinaryCalls(t *testing.T) {
	decl := parseGrammarTestDecl(t, `grammar PascalFrontend:
    block() -> Pascal.Block:
        declarations = optional(variable_section())
        expect("begin")
        statements = list(statement(), ";")
        expect("end")
    statement() -> Pascal.Stmt:
        choice(assignment(), compound_statement(), if_statement(), while_statement())
`)
	funcs := LowerDecl(decl)
	if len(funcs) != 2 {
		t.Fatalf("expected two lowered functions, got %d", len(funcs))
	}
	blockFormatted := unparse.FormatDecl(funcs[0])
	for _, want := range []string{
		"declarations = optional(variable_section())",
		"expect(\"begin\")",
		"statements = list(statement(), expect(\";\"))",
		"expect(\"end\")",
		"return zeroed",
	} {
		if !strings.Contains(blockFormatted, want) {
			t.Fatalf("expected lowered block production to contain %q, got:\n%s", want, blockFormatted)
		}
	}
	statementFormatted := unparse.FormatDecl(funcs[1])
	if !strings.Contains(statementFormatted, "choice(assignment(), choice(compound_statement(), choice(if_statement(), while_statement())))") {
		t.Fatalf("expected lowered choice production to stay as ordinary choice call, got:\n%s", statementFormatted)
	}
	if !strings.Contains(statementFormatted, "return zeroed.cast[Pascal.Stmt]") {
		t.Fatalf("expected lowered choice production to end with typed placeholder return, got:\n%s", statementFormatted)
	}
}

func TestLowerDeclPreservesListUntilStopSetAsOrdinaryCall(t *testing.T) {
	decl := parseGrammarTestDecl(t, `grammar PascalFrontend:
    block() -> Pascal.Block:
        statements = list(statement(), ";", until("end", token(TokenKind.EOF)))
`)
	funcs := LowerDecl(decl)
	if len(funcs) != 1 {
		t.Fatalf("expected one lowered function, got %d", len(funcs))
	}
	formatted := unparse.FormatDecl(funcs[0])
	if !strings.Contains(formatted, "statements = list(statement(), expect(\";\"), until(\"end\", token(TokenKind.EOF)))") {
		t.Fatalf("expected lowered function to preserve until stop set, got:\n%s", formatted)
	}
}

func TestLowerDeclExpandsGrammarTokenSets(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor state
    token_kind TokenKind
    token_field kind
    current current_token
    advance advance_token
    expect expect
    expect_kind expect_kind
    token:
        SEMICOLON ";"
        END "end"
    tokenset StatementSync:
        SEMICOLON
        END
    tokenset StatementOrFileSync:
        StatementSync
        token(TokenKind.EOF)
    recovery StatementRecovery:
        message ParseMessageKey.ExpectedStatement
        until StatementOrFileSync
    statement() -> Pascal.Stmt recover StatementRecovery:
        values = separated token(TokenKind.IDENT) by .SEMICOLON until(StatementOrFileSync)
        return zeroed as Pascal.Stmt
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.current_token().kind == TokenKind.SEMICOLON",
		"state.current_token().kind == TokenKind.END",
		"state.current_token().kind == TokenKind.EOF",
		"state.record_parse_error(ParseMessageKey.ExpectedStatement)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerDeclExpandsFirstTokenSets(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor state
	token_kind TokenKind
	token_field kind
	current current_token
	advance advance_token
	expect expect
	expect_kind expect_kind
	token:
		IDENT
		BEGIN "begin"
		END "end"
	tokenset StatementStart = first(statement)
	tokenset StatementOrEnd:
		StatementStart
		END
	statement() -> Pascal.Stmt:
		choice:
			seq:
				.IDENT(name)
				expr(name)
			seq:
				.BEGIN(begin_token)
				expr(begin_token)
	block() -> Pascal.Stmt:
		lookahead(StatementStart)
		values = separated statement() by .END until(StatementOrEnd)
		return zeroed as Pascal.Stmt
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.current_token().kind == TokenKind.IDENT",
		"state.current_token().kind == TokenKind.BEGIN",
		"state.current_token().kind == TokenKind.END",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered output to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "__grammar_choice_value_block_PascalFrontend_choice_value_11 <- first(statement)") {
		t.Fatalf("expected lookahead lowering to resolve first() refs, got:\n%s", formatted)
	}
}

func TestLowerDeclExpandsFirstTermLookahead(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor state
	token_kind TokenKind
	token_field kind
	current current_token
	advance advance_token
	expect expect
	expect_kind expect_kind
	token:
		IDENT
		BEGIN "begin"
	statement() -> Pascal.Stmt:
		choice:
			seq:
				.IDENT(name)
				expr(name)
			seq:
				.BEGIN(begin_token)
				expr(begin_token)
	block() -> Pascal.Stmt:
		lookahead(first(statement))
		return zeroed as Pascal.Stmt
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.BEGIN)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerDeclExpandsGrammarFns(t *testing.T) {
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
    grammarfn separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
        separated item by sep until(stop)
    args() -> darray[Token]:
        values = apply separated_by(item: token(TokenKind.IDENT), stop: RParenSync)
        return values
    semis() -> darray[Token]:
        values = apply separated_by(token(TokenKind.IDENT), RParenSync, sep: .SEMICOLON)
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
			t.Fatalf("expected lowered output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerDeclExpandsGrammarPipelineApply(t *testing.T) {
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
		RPAREN ")"
	tokenset RParenSync:
		RPAREN
		token(TokenKind.EOF)
	grammar type separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
		separated item by sep until(stop)
	args() -> darray[Token]:
		values = token(TokenKind.IDENT) |> separated_by(stop: RParenSync)
		return values
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.COMMA)",
		"state.current_token().kind == TokenKind.RPAREN",
		"state.current_token().kind == TokenKind.EOF",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered grammar pipeline output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerDeclExpandsGrammarAliases(t *testing.T) {
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
        RPAREN ")"
    tokenset RParenSync:
        RPAREN
        token(TokenKind.EOF)
    grammar type separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
        separated item by sep until(stop)
    grammar alias arg_list = separated_by(item: token(TokenKind.IDENT), stop: RParenSync)
    args() -> darray[Token]:
        values = arg_list
        return values
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.COMMA)",
		"state.current_token().kind == TokenKind.RPAREN",
		"state.current_token().kind == TokenKind.EOF",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered grammar alias output to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Count(formatted, "values = arg_list") != 1 {
		t.Fatalf("expected grammar alias to remain only in the source grammar declaration, got:\n%s", formatted)
	}
}

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
        return zeroed as Pascal.Stmt
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
	    maybe_item() -> Token:
	        value = .IDENT?
	        return value
	`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.current_token().kind == token_kind_for_text(\")\")",
		"__grammar_optional_cursor_",
		"def maybe_item(state: mutable ParserState&) -> Token:",
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
		{name: "internal try", fn: internalTryFn, wantReturn: "value: bool", wantBody: "zeroed.cast[bool]"},
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

func TestLowerFileStatefulAddsStablePublicTryWrapper(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        token(TokenKind.IDENT)
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def expression(state: mutable ParserState&) -> Token:",
		"def grammar_try_PascalFrontend_expression(state: mutable ParserState&) -> (matched: bool, value: Token):",
		"__grammar_committed_expression_PascalFrontend_committed_",
		"def __grammar_try__PascalFrontend__expression(state: mutable ParserState&) -> (matched: bool, committed: bool, value: Token):",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected stable public try wrapper output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerFileStatefulUsesGrantOnlyCanBlocksForGeneratedPermissions(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        token(TokenKind.IDENT)
`)
	lowered := LowerFile(file)
	fn, ok := lowered.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected first lowered decl after grammar to be func, got %T", lowered.Decls[1])
	}
	if len(fn.Body) != 1 {
		t.Fatalf("expected generated public production to be wrapped in one can stmt, got %d statements", len(fn.Body))
	}
	canStmt, ok := fn.Body[0].(*ast.CanStmt)
	if !ok {
		t.Fatalf("expected generated production body to start with can stmt, got %T", fn.Body[0])
	}
	if !canStmt.SuppressPermissionInference {
		t.Fatal("expected generated grammar can block to suppress outward permission inference")
	}
}

func TestLowerFileStatefulUsesGrammarHeaderReceiverForParamlessProductions(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor parser
    ident_expr() -> Token:
        .IDENT(tok)
        return tok
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def ident_expr(parser: mutable ParserState&) -> Token:",
		"def grammar_try_PascalFrontend_ident_expr(parser: mutable ParserState&) -> (matched: bool, value: Token):",
		"def __grammar_try__PascalFrontend__ident_expr(parser: mutable ParserState&) -> (matched: bool, committed: bool, value: Token):",
		"parser.cursor",
		"parser.expect_kind(TokenKind.IDENT)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected header-driven stateful lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerFileStatefulInjectsHeaderReceiverIntoBareProductionCalls(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor parser
    atom() -> Token:
        .IDENT(tok)
        return tok
    expr() -> Token:
        value = atom()
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "__grammar_try__PascalFrontend__atom(parser)") {
		t.Fatalf("expected bare production call to thread implicit header receiver, got:\n%s", formatted)
	}
}

func TestLowerFileMergesExtendGrammarProductionsAndHeader(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor parser
    atom() -> Token:
        .IDENT(tok)
        return tok

extend grammar PascalFrontend:
    expr() -> Token:
        value = atom()
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def atom(parser: mutable ParserState&) -> Token:",
		"def expr(parser: mutable ParserState&) -> Token:",
		"__grammar_try__PascalFrontend__atom(parser)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected merged extend-grammar lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Count(formatted, "def atom(parser: mutable ParserState&) -> Token:") != 1 {
		t.Fatalf("expected merged grammar lowering to emit atom exactly once, got:\n%s", formatted)
	}
	if strings.Count(formatted, "def expr(parser: mutable ParserState&) -> Token:") != 1 {
		t.Fatalf("expected merged grammar lowering to emit expr exactly once, got:\n%s", formatted)
	}
}

func TestLowerFileStatefulProductionAugmentationBuildsSingleMergedProduction(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalStmtGrammar over Token using ParserState:
    cursor state
    statement() -> Token:
        .IDENT(tok)
        return tok

extend grammar PascalStmtGrammar:
    statement +=
        .INTEGER(tok)
        return tok
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if strings.Count(formatted, "def statement(state: mutable ParserState&) -> Token:") != 1 {
		t.Fatalf("expected exactly one merged statement production, got:\n%s", formatted)
	}
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.INTEGER)",
		"__grammar_augment_statement_base_0",
		"__grammar_augment_statement_append_1",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected augmented lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerFileStatefulGroupedProductionAugmentationBuildsMergedProduction(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalStmtGrammar over Token using ParserState:
    cursor state
    statement() -> Token:
        .IDENT(tok)
        return tok

extend grammar PascalStmtGrammar:
    statement +=:
        .INTEGER(tok)
		pass
		|
        .STRING(tok)
        return tok
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if strings.Count(formatted, "def statement(state: mutable ParserState&) -> Token:") != 1 {
		t.Fatalf("expected exactly one merged statement production, got:\n%s", formatted)
	}
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.INTEGER)",
		"state.expect_kind(TokenKind.STRING)",
		"__grammar_augment_statement_base_0",
		"__grammar_augment_statement_append_1",
		"__grammar_augment_statement_append_2",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected grouped augmented lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerFileStatefulTokenAliasesRewriteLiteralTokensToKinds(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor state
    token .PROGRAM "program"
    program() -> Token:
        "program"
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "state.expect_kind(TokenKind.PROGRAM)") {
		t.Fatalf("expected token alias lowering to prefer expect_kind for aliased literal, got:\n%s", formatted)
	}
	if strings.Contains(formatted, `state.expect("program")`) {
		t.Fatalf("expected token alias lowering to avoid raw string match for aliased literal, got:\n%s", formatted)
	}
}

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
        return zeroed as Pascal.Stmt
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
        return zeroed as Pascal.Stmt
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
		return zeroed as Pascal.Stmt
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
        fallback zeroed as Token

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
		"__grammar_recover_value_statement_PascalStmtGrammar_recover_value_11 <- zeroed as Token",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected uses-imported recovery policy lowering to contain %q, got:\n%s", want, formatted)
		}
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

func TestLowerFileStatefulChoiceArmEndingInChannelAssignUsesProductionValue(t *testing.T) {
	file := parseGrammarTestFile(t, `struct Tail:
	name_token: Token
	close_token: Token

grammar PerlFrontend over Token using ParserState:
	cursor parser
	member_tail() -> Tail:
		channel name_token: Token
		channel close_token: Token
		choice:
			seq:
				name_token <- token(TokenKind.IDENT)
				close_token <- expr(name_token)
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_seq_value_",
		"zeroed.cast[Tail]",
		"Tail(name_token:, close_token:)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected channel-ending seq lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "<- name_token") && strings.Contains(formatted, "mutable Tail") {
		t.Fatalf("expected channel-ending seq to avoid assigning token channel as Tail value, got:\n%s", formatted)
	}
}

func TestLowerFileStatefulSynthesizesNamedTupleReturnFromChannels(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel item: i64 = 7
	channel count: usize = 1
	summary() -> (item: i64, count: usize):
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "def summary(parser: mutable ParserState&) -> (item: i64, count: usize):") {
		t.Fatalf("expected public production to preserve named tuple return type, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "(item, count)") {
		t.Fatalf("expected synthesized named tuple return to assemble channel fields, got:\n%s", formatted)
	}
}

func TestLowerFileStatefulInfersUntypedChannelsFromNamedTupleReturnFields(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel item
	channel count
	summary() -> (item: i64, count: usize):
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"item: mutable i64 = zeroed.cast[i64]",
		"count: mutable usize = zeroed.cast[usize]",
		"return (true, __grammar_committed_",
		"(item, count)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected named tuple channel inference lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "item: mutable (item: i64, count: usize)") || strings.Contains(formatted, "count: mutable (item: i64, count: usize)") {
		t.Fatalf("expected tuple-field inference to avoid seeding channels with the whole tuple type, got:\n%s", formatted)
	}
}

func TestLowerFileStatefulTypedExprTermInSeparatedListInfersElementType(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel param_name_ids
	param_name_ids_core() -> darray[PascalNameId]:
		param_name_ids <- separated required(seq(.IDENT(param_token), expr[PascalNameId](param_token.lexeme_key)), ParseMessageKey.ExpectedProgramParamName) by .COMMA until(.RPAREN, token(TokenKind.EOF))
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"param_name_ids: mutable darray[PascalNameId] = zeroed.cast[darray[PascalNameId]]",
		"__grammar_list_value_",
		"mutable PascalNameId = zeroed.cast[PascalNameId]",
		".push(__grammar_seq_value_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected typed expr list lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "darray[void]") {
		t.Fatalf("expected typed expr term to prevent void list inference, got:\n%s", formatted)
	}
}

func TestLowerFileStatefulTypedExprTermCarriesDeclaredTypeForEmptyList(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel decls
	empty_decls() -> darray[Pascal.Decl]:
		decls <- expr[darray[Pascal.Decl]]([])
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_value_empty_decls_",
		": darray[Pascal.Decl] = []",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected typed expr lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "zeroed as darray[Pascal.Decl]") {
		t.Fatalf("expected typed expr lowering to avoid cast-based empty darray scaffolding, got:\n%s", formatted)
	}
}

func TestLowerFileStatefulFlatMapListInfersElementTypeFromReturnType(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel decls
	variable_decl_group(names: darray[Token], type_token: Token) -> darray[Pascal.Decl]:
		decls <- flatmaplist(names, name_token, [build_var_decl(name_token, type_token)] if name_token.kind == TokenKind.IDENT else [])
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"decls: mutable darray[Pascal.Decl] = zeroed.cast[darray[Pascal.Decl]]",
		"__grammar_maplist_value_",
		": darray[Pascal.Decl] = ([build_var_decl(name_token, type_token)] if (name_token.kind == TokenKind.IDENT) else [])",
		"([build_var_decl(name_token, type_token)] if (name_token.kind == TokenKind.IDENT) else [])",
		".push(__grammar_maplist_group_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected flatmaplist lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "darray[void]") {
		t.Fatalf("expected flatmaplist to infer element type from production return type, got:\n%s", formatted)
	}
}

func TestLowerFileStatefulSingletonBuildsSingleItemList(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	alloc arena
	channel decls
	const_decl_group(decl: Pascal.Decl) -> darray[Pascal.Decl]:
		decls <- singleton[Pascal.Decl](decl)
		return decls
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"decls: mutable darray[Pascal.Decl] = zeroed.cast[darray[Pascal.Decl]]",
		"__grammar_singleton_value_",
		"in arena:",
		".push(decl)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected singleton lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "single_decl_list") || strings.Contains(formatted, "darray[void]") {
		t.Fatalf("expected singleton lowering to avoid helper calls and infer element type, got:\n%s", formatted)
	}
}

func TestLowerFileStatefulEmptyBuildsTypedEmptyList(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel decls
	empty_decls() -> darray[Pascal.Decl]:
		decls <- empty[Pascal.Decl]
		return decls
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"decls: mutable darray[Pascal.Decl] = zeroed.cast[darray[Pascal.Decl]]",
		"__grammar_empty_value_",
		": darray[Pascal.Decl] = []",
		"decls <- __grammar_empty_value_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected empty lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "darray[void]") {
		t.Fatalf("expected empty lowering to infer element type, got:\n%s", formatted)
	}
}

func TestLowerFileStatefulConcatTermFlattensListOperands(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel decls
	declarations() -> darray[Token]:
		decls <- expr[darray[Token]]([]) + list(token(TokenKind.IDENT)) + expr[darray[Token]]([])
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"decls: mutable darray[Token] = zeroed.cast[darray[Token]]",
		"__grammar_concat_value_",
		"__grammar_concat_group_",
		"parser.expect_kind(TokenKind.IDENT)",
		".push(__grammar_concat_group_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected concat lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "darray[void]") {
		t.Fatalf("expected concat lowering to preserve list element type, got:\n%s", formatted)
	}
}

func TestLowerFileStatefulSynthesizesStructReturnFromNamespacedChannels(t *testing.T) {
	file := parseGrammarTestFile(t, `namespace PascalFrontend:
	struct BuiltSummary:
		items: darray[i64]
		checksum_total: i64
		arg_count: usize
		close_span: Span

	grammar Parser over Token using ParserState:
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
	if !strings.Contains(formatted, "BuiltSummary(items:, checksum_total:, arg_count:, close_span:)") {
		t.Fatalf("expected namespaced struct return synthesis to use the local struct scope, got:\n%s", formatted)
	}
}

func TestLowerFileStatefulTryWrapperCarriesProductionErrorUnion(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar ATPLFrontend:
    postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> ATPLExpr.Expr error[ATPLFrontendError]:
        base = expr(try self.parse_primary(owner))
        return base
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> ATPLExpr.Expr error[ATPLFrontendError]:",
		"def grammar_try_ATPLFrontend_postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> (matched: bool, value: ATPLExpr.Expr) error[ATPLFrontendError]:",
		"def __grammar_try__ATPLFrontend__postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> (matched: bool, committed: bool, value: ATPLExpr.Expr) error[ATPLFrontendError]:",
		"try __grammar_try__ATPLFrontend__postfix_expr(self, owner)",
		"try self.parse_primary(owner)",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected error-union stateful grammar lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerFileStatefulAutoTriesNestedErrorUnionProductionCalls(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar ATPLFrontend:
    atom(self: mutable ATPLParser&, owner: mutable Arena&) -> ATPLExpr.Expr error[ATPLFrontendError]:
        base = expr(try self.parse_primary(owner))
        return base
    postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> ATPLExpr.Expr error[ATPLFrontendError]:
        value = atom(self, owner)
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "try __grammar_try__ATPLFrontend__atom(self, owner)") {
		t.Fatalf("expected nested error-union production calls to lower through try-wrapped internal helpers, got:\n%s", formatted)
	}
}

func TestLowerFileStatefulSupportsPosBasedReceiverForAttemptDrivenPostfix(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar ATPLFrontend:
    postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> Token:
        result = postfix(left = attempt(self.try_parse_primary(owner))):
			step = attempt(self.try_parse_suffix(owner, left)) -> step
        return result
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> Token:",
		"def grammar_try_ATPLFrontend_postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> (matched: bool, value: Token):",
		"def __grammar_try__ATPLFrontend__postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> (matched: bool, committed: bool, value: Token):",
		"self.pos",
		"= self.try_parse_primary(owner)",
		"= self.try_parse_suffix(owner, left)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected pos-based stateful postfix lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	for _, unwanted := range []string{
		"self.current_token()",
		"self.advance_token()",
		"self.record_parse_error(",
	} {
		if strings.Contains(formatted, unwanted) {
			t.Fatalf("expected pos-based receiver lowering to avoid %q, got:\n%s", unwanted, formatted)
		}
	}
}

func TestLowerFileStatefulCutPropagatesCommittedFailureThroughChoice(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    statement(state: mutable ParserState&) -> Token:
        value = choice(if_statement(), ident_statement())
        return value
    if_statement(state: mutable ParserState&) -> Token:
        "if"
        cut
        "then"
        return zeroed as Token
    ident_statement(state: mutable ParserState&) -> Token:
        .IDENT(tok)
        return tok
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def __grammar_try__PascalFrontend__if_statement(state: mutable ParserState&) -> (matched: bool, committed: bool, value: Token):",
		"choice_committed_statement_PascalFrontend",
		"return (false, __grammar_committed_if_statement_PascalFrontend",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected cut-aware lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerFileStatefulPrecedenceTermBindsOperatorValue(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        precedence(left = token(TokenKind.IDENT)):
			op = "+" right = token(TokenKind.IDENT) -> right
        return left
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect(\"+\")",
		"op = __grammar_token_expression_PascalFrontend_token_",
		"right = __grammar_token_expression_PascalFrontend_token_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered precedence production to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerFileStatefulPostfixTermBuildsLoopAndBindings(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar LuaFrontend:
    suffix_expr(state: mutable ParserState&) -> Token:
        result = postfix(left = token(TokenKind.IDENT)):
			"." field = token(TokenKind.IDENT) -> field
			"(" arg = token(TokenKind.IDENT) close = ")" -> arg
        return result
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"left: mutable Token = __grammar_token_suffix_expr_LuaFrontend_token_",
		"while (not __grammar_postfix_stop_suffix_expr_LuaFrontend_postfix_stop_",
		"state.expect(\".\")",
		"field = __grammar_token_suffix_expr_LuaFrontend_token_",
		"state.expect(\"(\")",
		"arg = __grammar_token_suffix_expr_LuaFrontend_token_",
		"close = __grammar_token_suffix_expr_LuaFrontend_token_",
		"state.expect(\")\")",
		"result = left",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered postfix production to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerFileStatefulSuffixTermRunsArmsOnce(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    condition(state: mutable ParserState&) -> Token:
        result = suffix(left = token(TokenKind.IDENT)):
			op = choice("=", ">") right = token(TokenKind.IDENT) -> right
        return result
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"left: mutable Token = __grammar_token_condition_PascalFrontend_token_",
		"__grammar_suffix_cursor_condition_PascalFrontend_suffix_cursor_",
		"state.expect(\"=\")",
		"state.expect(\">\")",
		"op = __grammar_choice_value_condition_PascalFrontend_choice_value_",
		"right = __grammar_token_condition_PascalFrontend_token_",
		"result = left",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered suffix production to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "while (not __grammar_suffix_stop_condition_PascalFrontend_suffix_stop_") {
		t.Fatalf("expected suffix term to avoid postfix loop, got:\n%s", formatted)
	}
}

func TestLowerFileStatefulExprTermEvaluatesHelperExpression(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        current = expr(state.current_token())
        parsed = expr(try state.expect_name_token(ParseMessageKey.ExpectedProgramName))
        return current
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_value_expression_PascalFrontend_value_",
		"current = __grammar_value_expression_PascalFrontend_value_",
		"parsed = __grammar_value_expression_PascalFrontend_value_",
		"try state.expect_name_token(ParseMessageKey.ExpectedProgramName)",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered expr-term production to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerFileStatefulTokenKindShorthandUsesExpectKind(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    ident_expr(state: mutable ParserState&) -> Token:
        .IDENT(tok)
        return tok
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def ident_expr(state: mutable ParserState&) -> Token:",
		"state.expect_kind(TokenKind.IDENT)",
		"tok = __grammar_token_ident_expr_PascalFrontend_token_",
		"return tok",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered token-kind shorthand production to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerFileStatefulAttemptTermBuildsTupleBind(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        suffix = attempt(state.try_parse_suffix())
        return suffix
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_matched_expression_PascalFrontend_matched_",
		"__grammar_value_expression_PascalFrontend_value_",
		"= state.try_parse_suffix()",
		"if (not __grammar_matched_expression_PascalFrontend_matched_",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered attempt-term production to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestLowerFileStatefulGuardTermBuildsFailurePredicate(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    statement(state: mutable ParserState&) -> Token:
        .IDENT(name_token)
        guard(state.lookahead_token(1).kind == TokenKind.ASSIGN)
        return name_token
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_guard_statement_PascalFrontend_guard_",
		"state.lookahead_token(1).kind == TokenKind.ASSIGN",
		"if (not __grammar_guard_statement_PascalFrontend_guard_",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered guard-term production to contain %q, got:\n%s", want, formatted)
		}
	}
}

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
