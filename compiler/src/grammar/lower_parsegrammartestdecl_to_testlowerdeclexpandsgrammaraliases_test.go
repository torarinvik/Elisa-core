package grammar

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

func parseGrammarTestDecl(t *testing.T, src string) *ast.GrammarDecl {
	t.Helper()
	l := lexer.New("grammar_lower_test.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("grammar_lower_test.elisa")
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
	l := lexer.New("grammar_lower_test.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("grammar_lower_test.elisa")
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
        return zeroed.cast[Pascal.Stmt]
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
func TestLowerDeclExpandsGrammarTokenFamilies(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar OperatorTokens over Token using ParserState:
	cursor state
	token_kind TokenKind
	token_field kind
	current current_token
	advance advance_token
	expect expect
	expect_kind expect_kind
	token:
		IDENT
		SYMBOL_IDENT
		PLUS "+"
	token family OperatorName:
		IDENT
		SYMBOL_IDENT
		PLUS

grammar PascalFrontend over Token using ParserState uses OperatorTokens:
	operator_name() -> Token:
		lookahead(OperatorName)
		name = required(OperatorName, ParseMessageKey.ExpectedOperatorName)
		return name
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "token family OperatorName:") {
		t.Fatalf("expected formatted grammar to preserve token family declaration, got:\n%s", formatted)
	}
	for _, want := range []string{
		"expect_kind(TokenKind.IDENT)",
		"expect_kind(TokenKind.SYMBOL_IDENT)",
		"expect_kind(TokenKind.PLUS)",
		"required(OperatorName, ParseMessageKey.ExpectedOperatorName)",
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
		return zeroed.cast[Pascal.Stmt]
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
		return zeroed.cast[Pascal.Stmt]
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
func TestGrammarFirstFactsWalkNullablePrefixes(t *testing.T) {
	pos := lexer.Pos{File: "grammar_facts_test.elisa", Line: 1, Col: 1}
	productions := map[string]resolvedGrammarProduction{
		"maybe_prefix": {
			Production: ast.GrammarProductionDecl{
				Name: "maybe_prefix",
				Terms: []ast.GrammarTerm{
					&ast.GrammarOptionalTerm{
						Position: pos,
						Term:     &ast.GrammarTokenKindTerm{Position: pos, Kind: "IDENT"},
					},
					&ast.GrammarTokenKindTerm{Position: pos, Kind: "BEGIN"},
				},
			},
		},
		"empty_tail": {
			Production: ast.GrammarProductionDecl{
				Name: "empty_tail",
				Terms: []ast.GrammarTerm{
					&ast.GrammarOptionalTerm{
						Position: pos,
						Term:     &ast.GrammarTokenKindTerm{Position: pos, Kind: "END"},
					},
				},
			},
		},
	}
	first, nullable := grammarFirstTermsForTerm(&ast.GrammarCallTerm{Position: pos, Name: "maybe_prefix"}, productions, nil)
	if nullable {
		t.Fatalf("expected maybe_prefix to require BEGIN after nullable prefix")
	}
	if got := grammarFirstTokenKinds(first); strings.Join(got, ",") != "IDENT,BEGIN" {
		t.Fatalf("expected first set [IDENT BEGIN], got %#v", got)
	}
	first, nullable = grammarFirstTermsForTerm(&ast.GrammarCallTerm{Position: pos, Name: "empty_tail"}, productions, nil)
	if !nullable {
		t.Fatalf("expected empty_tail to be nullable")
	}
	if got := grammarFirstTokenKinds(first); strings.Join(got, ",") != "END" {
		t.Fatalf("expected first set [END], got %#v", got)
	}
}
func grammarFirstTokenKinds(terms []ast.GrammarTerm) []string {
	kinds := make([]string, 0, len(terms))
	for _, term := range terms {
		if kind, ok := term.(*ast.GrammarTokenKindTerm); ok {
			kinds = append(kinds, kind.Kind)
		}
	}
	return kinds
}
func TestLowerFilePreservesInlineNodeConstructionInStatefulGrammar(t *testing.T) {
	file := parseGrammarTestFile(t, `struct Span:
    start: i32
    end: i32

struct Token:
    kind: TokenKind
    span: Span
    lexeme_key: u32

struct ParserState:
    tokens: darray[Token]
    cursor: mutable usize

const enum TokenKind of i16:
    EOF = 0
    IDENT = 1
    STAR = 2

enum DemoExpr:
    Invalid(span: Span)
    Name(span: Span, name_id: u32)
    Pair(span: Span, left: DemoExpr, right: DemoExpr)

grammarenv DemoEnv over Token using ParserState:
    cursor state
    alloc alloc
    token_kind TokenKind
    eof TokenKind.EOF
    token_field kind
    current current_token
    advance advance_token
    expect_kind expect_kind

grammar DemoGrammar with DemoEnv:
    token:
        IDENT
        STAR "*"
    atom() -> DemoExpr:
        .IDENT(token)
        node <- expr(DemoExpr.Name(span: token.span, name_id: token.lexeme_key))
        return node
    maybe_pair(left: DemoExpr) -> DemoExpr:
        node <- when(state.current_token().kind == TokenKind.STAR, pair_tail(left), expr(DemoExpr.Invalid(span: left.span)))
        return node
    pair_tail(left: DemoExpr) -> DemoExpr:
        .STAR
        right = atom()
        return DemoExpr.Pair(span: left.span + right.span, left: left, right: right)
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"DemoExpr.Name(span: token.span, name_id: token.lexeme_key)",
		"DemoExpr.Invalid(span: left.span)",
		"DemoExpr.Pair(span: (left.span + right.span), left: left, right: right)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered inline enum construction to contain %q, got:\n%s", want, formatted)
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
func TestLowerDeclExpandsGrammarHelperShorthandNoneAndPipeTokenSets(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar DemoGrammar over Token using ParserState:
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
	tokenset Stop = RPAREN | token(TokenKind.EOF)
	grammar pick(item: grammar -> DemoValue) -> DemoValue:
		item
	maybe_value() -> DemoValue?:
		node <- when(state.current_token().kind == TokenKind.IDENT, pick(item: present_value()), none[DemoValue])
		return node
	present_value() -> DemoValue:
		.IDENT
		node <- expr(make_demo_value())
		return node
	values() -> darray[DemoValue]:
		node = [present_value()] while token in tokens != [Stop]
		return node
`)
	formattedSource := unparse.FormatFile(file)
	if !strings.Contains(formattedSource, "grammar pick(item: grammar -> DemoValue) -> DemoValue:") {
		t.Fatalf("expected formatted source to preserve shorthand grammar helper, got:\n%s", formattedSource)
	}
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.current_token().kind == TokenKind.RPAREN",
		"state.current_token().kind == TokenKind.EOF",
		"DemoValue?",
		"null",
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
