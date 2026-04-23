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
