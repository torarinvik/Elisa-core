package parser

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/unparse"
)

func TestParseGrammarDeclWithGenericHeaderAndTerms(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar Pascal[T, region parse_region]:
    program(input: T) -> Pascal.Program:
        keyword = "program"
        name = parse_ident()
        expect(";")
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if decl.Name != "Pascal" {
		t.Fatalf("expected grammar name Pascal, got %q", decl.Name)
	}
	if len(decl.TypeParams) != 1 || decl.TypeParams[0] != "T" {
		t.Fatalf("expected one type param T, got %#v", decl.TypeParams)
	}
	if len(decl.RegionParams) != 1 || decl.RegionParams[0] != "parse_region" {
		t.Fatalf("expected one region param parse_region, got %#v", decl.RegionParams)
	}
	if len(decl.Productions) != 1 {
		t.Fatalf("expected one production, got %d", len(decl.Productions))
	}
	production := decl.Productions[0]
	if production.Name != "program" || len(production.Params) != 1 {
		t.Fatalf("expected program production with one param, got %#v", production)
	}
	if len(production.Terms) != 3 {
		t.Fatalf("expected three production terms, got %d", len(production.Terms))
	}
	binding, ok := production.Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first term to be a binding, got %T", production.Terms[0])
	}
	if binding.Name != "keyword" {
		t.Fatalf("expected first binding name keyword, got %q", binding.Name)
	}
	if token, ok := binding.Term.(*ast.GrammarTokenTerm); !ok || token.Value != "program" {
		t.Fatalf("expected keyword binding to token \"program\", got %T %#v", binding.Term, binding.Term)
	}
	call, ok := production.Terms[2].(*ast.GrammarCallTerm)
	if !ok {
		t.Fatalf("expected third term to be a call, got %T", production.Terms[2])
	}
	if call.Name != "expect" || len(call.Args) != 1 {
		t.Fatalf("expected expect call with one arg, got %#v", call)
	}

	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"grammar Pascal[T, region parse_region]:",
		"program(input: T) -> Pascal.Program:",
		"keyword = \"program\"",
		"name = parse_ident()",
		"expect(\";\")",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsStructuredHeaderMetadata(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalGrammar[B] over PascalToken using PascalParseCtx:
    error PascalFrontendError
    cursor parser
    alloc arena
    channel node
	channel span: PascalSpan = span($start, $end)
    channel checksum: i64
    program(state: mutable ParserState&) -> Pascal.Decl:
        token = "program"
        return zeroed as Pascal.Decl
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if got := formatTypeExprForTest(t, decl.OverType); got != "PascalToken" {
		t.Fatalf("expected over type PascalToken, got %q", got)
	}
	if got := formatTypeExprForTest(t, decl.UsingType); got != "PascalParseCtx" {
		t.Fatalf("expected using type PascalParseCtx, got %q", got)
	}
	if got := formatTypeExprForTest(t, decl.ErrorType); got != "PascalFrontendError" {
		t.Fatalf("expected error type PascalFrontendError, got %q", got)
	}
	if got := formatExprForTest(t, decl.CursorExpr); got != "parser" {
		t.Fatalf("expected cursor expr parser, got %q", got)
	}
	if got := formatExprForTest(t, decl.AllocExpr); got != "arena" {
		t.Fatalf("expected alloc expr arena, got %q", got)
	}
	if len(decl.Channels) != 3 {
		t.Fatalf("expected three channels, got %d", len(decl.Channels))
	}
	if decl.Channels[0].Name != "node" || decl.Channels[0].Type != nil || decl.Channels[0].Default != nil {
		t.Fatalf("expected node channel without type or default, got %#v", decl.Channels[0])
	}
	if got := formatTypeExprForTest(t, decl.Channels[1].Type); got != "PascalSpan" {
		t.Fatalf("expected span channel type PascalSpan, got %q", got)
	}
	if got := formatExprForTest(t, decl.Channels[1].Default); got != "span($start, $end)" {
		t.Fatalf("expected span channel default span($start, $end), got %q", got)
	}
	if got := formatTypeExprForTest(t, decl.Channels[2].Type); got != "i64" {
		t.Fatalf("expected checksum channel type i64, got %q", got)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"grammar PascalGrammar[B] over PascalToken using PascalParseCtx:",
		"error PascalFrontendError",
		"cursor parser",
		"alloc arena",
		"channel node",
		"channel span: PascalSpan = span($start, $end)",
		"channel checksum: i64",
		"program(state: mutable ParserState&) -> Pascal.Decl:",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsUsesClauseAndPublicShorthandProduction(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalStmtGrammar[B] uses PascalExprGrammar[B], PascalCommonGrammar:
    pub Stmt -> Pascal.Stmt recover(ParseMessageKey.ExpectedStatement, until(";", token(TokenKind.EOF))):
        stmt = state.statement_core()
        return stmt
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.Uses) != 2 {
		t.Fatalf("expected two used grammars, got %d", len(decl.Uses))
	}
	if got := formatTypeExprForTest(t, decl.Uses[0]); got != "PascalExprGrammar[B]" {
		t.Fatalf("expected first used grammar PascalExprGrammar[B], got %q", got)
	}
	if got := formatTypeExprForTest(t, decl.Uses[1]); got != "PascalCommonGrammar" {
		t.Fatalf("expected second used grammar PascalCommonGrammar, got %q", got)
	}
	production := decl.Productions[0]
	if !production.Public {
		t.Fatal("expected production to be public")
	}
	if production.HasParamList {
		t.Fatal("expected shorthand production to omit parameter list")
	}
	if len(production.Params) != 0 {
		t.Fatalf("expected no production params, got %#v", production.Params)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"grammar PascalStmtGrammar[B] uses PascalExprGrammar[B], PascalCommonGrammar:",
		"pub Stmt -> Pascal.Stmt recover(ParseMessageKey.ExpectedStatement, until(\";\", token(TokenKind.EOF))):",
		"stmt = state.statement_core()",
		"return stmt",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseExtendGrammarDecl(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor state
    atom() -> Token:
        .IDENT(tok)
        return tok

extend grammar PascalFrontend uses PascalExprGrammar:
    expr() -> Token:
        value = atom()
        return value
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 2 {
		t.Fatalf("expected two grammar decls, got %d", len(file.Decls))
	}
	base, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected base grammar decl, got %T", file.Decls[0])
	}
	if base.Extend {
		t.Fatal("expected base grammar not to be marked as extension")
	}
	ext, ok := file.Decls[1].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected extension grammar decl, got %T", file.Decls[1])
	}
	if !ext.Extend {
		t.Fatal("expected extend grammar decl to be marked as extension")
	}
	if len(ext.Uses) != 1 || formatTypeExprForTest(t, ext.Uses[0]) != "PascalExprGrammar" {
		t.Fatalf("expected extension to preserve uses clause, got %#v", ext.Uses)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"grammar PascalFrontend over Token using ParserState:",
		"extend grammar PascalFrontend uses PascalExprGrammar:",
		"expr() -> Token:",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsAssignmentTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor parser
    channel span: Span = combine_span($start.span, $end.span)
    ident_span() -> Span:
        .IDENT(tok)
		span <- expr(tok.span)
        return span
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.Productions) != 1 || len(decl.Productions[0].Terms) != 3 {
		t.Fatalf("expected one production with three terms, got %#v", decl.Productions)
	}
	assign, ok := decl.Productions[0].Terms[1].(*ast.GrammarAssignTerm)
	if !ok {
		t.Fatalf("expected second term to be an assignment, got %T", decl.Productions[0].Terms[1])
	}
	if assign.Name != "span" {
		t.Fatalf("expected assignment target span, got %q", assign.Name)
	}
	exprTerm, ok := assign.Term.(*ast.GrammarExprTerm)
	if !ok {
		t.Fatalf("expected assignment term to be expr(...), got %T", assign.Term)
	}
	if got := formatExprForTest(t, exprTerm.Expr); got != "tok.span" {
		t.Fatalf("expected assignment value tok.span, got %q", got)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"channel span: Span = combine_span($start.span, $end.span)",
		".IDENT(tok)",
		"span <- expr(tok.span)",
		"return span",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsPassProduction(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar Pascal:
    declarations() -> Pascal.Decls:
        pass
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.Productions) != 1 || len(decl.Productions[0].Terms) != 1 {
		t.Fatalf("expected one pass production, got %#v", decl.Productions)
	}
	if _, ok := decl.Productions[0].Terms[0].(*ast.GrammarPassTerm); !ok {
		t.Fatalf("expected pass term, got %T", decl.Productions[0].Terms[0])
	}
	formatted := strings.TrimSpace(unparse.FormatFile(file))
	if !strings.Contains(formatted, "pass") {
		t.Fatalf("expected formatted output to contain pass, got:\n%s", formatted)
	}
}

func TestParseGrammarDeclAllowsCutTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar Pascal:
    statement() -> Pascal.Stmt:
        "if"
        cut
        "then"
        return zeroed as Pascal.Stmt
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.Productions) != 1 || len(decl.Productions[0].Terms) != 4 {
		t.Fatalf("expected one production with four terms, got %#v", decl.Productions)
	}
	if _, ok := decl.Productions[0].Terms[1].(*ast.GrammarCutTerm); !ok {
		t.Fatalf("expected second term to be cut, got %T", decl.Productions[0].Terms[1])
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{"cut", "\"if\"", "\"then\""} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsExplicitReturnTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar Demo:
    produce() -> i64:
        value = helper()
        return value + 1
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.Productions) != 1 || len(decl.Productions[0].Terms) != 2 {
		t.Fatalf("expected one production with two terms, got %#v", decl.Productions)
	}
	ret, ok := decl.Productions[0].Terms[1].(*ast.GrammarReturnTerm)
	if !ok {
		t.Fatalf("expected second term to be explicit return, got %T", decl.Productions[0].Terms[1])
	}
	if _, ok := ret.Value.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected return term to carry expression AST, got %T", ret.Value)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"value = helper()",
		"return (value + 1)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarChoiceOptionalAndListTerms(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    block() -> Pascal.Block:
        declarations = optional(variable_section())
        expect("begin")
        statements = list(statement(), ";")
        expect("end")
    statement() -> Pascal.Stmt:
        choice(assignment(), compound_statement(), if_statement(), while_statement())
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	block := decl.Productions[0]
	declsBind, ok := block.Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first block term to be binding, got %T", block.Terms[0])
	}
	if _, ok := declsBind.Term.(*ast.GrammarOptionalTerm); !ok {
		t.Fatalf("expected declarations binding to optional term, got %T", declsBind.Term)
	}
	stmtsBind, ok := block.Terms[2].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected statements term to be binding, got %T", block.Terms[2])
	}
	listTerm, ok := stmtsBind.Term.(*ast.GrammarListTerm)
	if !ok {
		t.Fatalf("expected statements binding to list term, got %T", stmtsBind.Term)
	}
	if listTerm.Separator == nil {
		t.Fatal("expected list term to record a separator")
	}
	choiceProd := decl.Productions[1]
	choiceTerm, ok := choiceProd.Terms[0].(*ast.GrammarChoiceTerm)
	if !ok {
		t.Fatalf("expected statement production to use choice term, got %T", choiceProd.Terms[0])
	}
	if len(choiceTerm.Options) != 4 {
		t.Fatalf("expected four choice options, got %d", len(choiceTerm.Options))
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"declarations = optional(variable_section())",
		"statements = list(statement(), \";\")",
		"choice(assignment(), compound_statement(), if_statement(), while_statement())",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarListTermAllowsUntilStopSets(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    block(state: mutable ParserState&) -> Pascal.Block:
        statements = list(state.statement(), ";", until("end", token(TokenKind.EOF)))
    declarations(state: mutable ParserState&) -> darray[Pascal.Decl]:
        items = list(state.variable_decl(), until("begin"))
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	blockBind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected block production binding, got %T", decl.Productions[0].Terms[0])
	}
	blockList, ok := blockBind.Term.(*ast.GrammarListTerm)
	if !ok {
		t.Fatalf("expected block binding to be list term, got %T", blockBind.Term)
	}
	if blockList.Separator == nil {
		t.Fatal("expected block list term to preserve separator")
	}
	if len(blockList.Until) != 2 {
		t.Fatalf("expected two stop terms, got %d", len(blockList.Until))
	}
	if token, ok := blockList.Until[0].(*ast.GrammarTokenTerm); !ok || token.Value != "end" {
		t.Fatalf("expected first stop term to be token \"end\", got %T %#v", blockList.Until[0], blockList.Until[0])
	}
	if call, ok := blockList.Until[1].(*ast.GrammarCallTerm); !ok || call.Name != "token" || len(call.Args) != 1 {
		t.Fatalf("expected second stop term to be token(TokenKind.EOF), got %T %#v", blockList.Until[1], blockList.Until[1])
	}
	declBind, ok := decl.Productions[1].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected declarations binding, got %T", decl.Productions[1].Terms[0])
	}
	declList, ok := declBind.Term.(*ast.GrammarListTerm)
	if !ok {
		t.Fatalf("expected declarations binding to be list term, got %T", declBind.Term)
	}
	if declList.Separator != nil {
		t.Fatalf("expected declarations list to omit separator, got %#v", declList.Separator)
	}
	if len(declList.Until) != 1 {
		t.Fatalf("expected one stop term without separator, got %d", len(declList.Until))
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"statements = list(state.statement(), \";\", until(\"end\", token(TokenKind.EOF)))",
		"items = list(state.variable_decl(), until(\"begin\"))",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarRepeatAndSeparatedTerms(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    block(state: mutable ParserState&) -> darray[Pascal.Stmt]:
        items = repeat(state.statement(), until("end", token(TokenKind.EOF)))
    args(state: mutable ParserState&) -> darray[Pascal.Expr]:
        values = separated(state.expression(), ",", until(")", token(TokenKind.EOF)))
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	repeatBind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected repeat binding, got %T", decl.Productions[0].Terms[0])
	}
	if _, ok := repeatBind.Term.(*ast.GrammarRepeatTerm); !ok {
		t.Fatalf("expected repeat term, got %T", repeatBind.Term)
	}
	separatedBind, ok := decl.Productions[1].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected separated binding, got %T", decl.Productions[1].Terms[0])
	}
	if _, ok := separatedBind.Term.(*ast.GrammarSeparatedTerm); !ok {
		t.Fatalf("expected separated term, got %T", separatedBind.Term)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"items = repeat(state.statement(), until(\"end\", token(TokenKind.EOF)))",
		"values = separated(state.expression(), \",\", until(\")\", token(TokenKind.EOF)))",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarProductionAllowsRecoverClause(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    statement(state: mutable ParserState&) -> Pascal.Stmt recover(ParseMessageKey.ExpectedStatement, until(";", "end", "else", token(TokenKind.EOF))):
        stmt = choice(state.compound_statement(), state.if_statement(), state.while_statement(), state.assignment())
        return stmt
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	production := decl.Productions[0]
	if production.RecoverMsg == nil {
		t.Fatal("expected recover message expression")
	}
	if len(production.RecoverUntil) != 4 {
		t.Fatalf("expected four recover stop terms, got %d", len(production.RecoverUntil))
	}
	formatted := unparse.FormatFile(file)
	want := "statement(state: mutable ParserState&) -> Pascal.Stmt recover(ParseMessageKey.ExpectedStatement, until(\";\", \"end\", \"else\", token(TokenKind.EOF))):"
	if !strings.Contains(formatted, want) {
		t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
	}
}

func TestParseGrammarProductionAllowsRecoverFallbackValue(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
	statement(state: mutable ParserState&) -> Pascal.Stmt recover(ParseMessageKey.ExpectedStatement, until(";", token(TokenKind.EOF)), zeroed as Pascal.Stmt):
		stmt = state.assignment()
		return stmt
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	production := decl.Productions[0]
	if production.RecoverValue == nil {
		t.Fatal("expected recover fallback expression")
	}
	formatted := unparse.FormatFile(file)
	want := "statement(state: mutable ParserState&) -> Pascal.Stmt recover(ParseMessageKey.ExpectedStatement, until(\";\", token(TokenKind.EOF)), zeroed as Pascal.Stmt):"
	if !strings.Contains(formatted, want) {
		t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
	}
}

func TestParseGrammarProductionAllowsReturnlessRecoverClause(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor state
	block_tail() recover(ParseMessageKey.ExpectedStatement, until("end", token(TokenKind.EOF))):
		guard(state.current_token().kind == TokenKind.END or state.current_token().kind == TokenKind.EOF)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	production := decl.Productions[0]
	if production.ReturnType != nil {
		t.Fatalf("expected returnless recover production, got return type %T", production.ReturnType)
	}
	if production.RecoverMsg == nil {
		t.Fatal("expected recover message expression")
	}
	if len(production.RecoverUntil) != 2 {
		t.Fatalf("expected two recover stop terms, got %d", len(production.RecoverUntil))
	}
	if production.RecoverValue != nil {
		t.Fatal("expected no recover fallback expression")
	}
	formatted := unparse.FormatFile(file)
	want := "block_tail() recover(ParseMessageKey.ExpectedStatement, until(\"end\", token(TokenKind.EOF))):"
	if !strings.Contains(formatted, want) {
		t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
	}
}

func TestParseGrammarDeclAllowsTermLevelRecoverClause(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor state
	statement(state: mutable ParserState&) -> Pascal.Stmt:
		stmt = state.statement_core() recover(ParseMessageKey.ExpectedStatement, until(";", token(TokenKind.EOF)), zeroed as Pascal.Stmt)
		guard(state.current_token().kind == TokenKind.END) recover(ParseMessageKey.ExpectedStatement, until("end", token(TokenKind.EOF)))
		return stmt
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	bind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first term to be binding, got %T", decl.Productions[0].Terms[0])
	}
	recoveredBind, ok := bind.Term.(*ast.GrammarRecoverTerm)
	if !ok {
		t.Fatalf("expected binding term to be recover wrapper, got %T", bind.Term)
	}
	if _, ok := recoveredBind.Term.(*ast.GrammarCallTerm); !ok {
		t.Fatalf("expected recovered binding inner term to be call, got %T", recoveredBind.Term)
	}
	if recoveredBind.RecoverValue == nil {
		t.Fatal("expected recovered binding to keep fallback value")
	}
	recoveredGuard, ok := decl.Productions[0].Terms[1].(*ast.GrammarRecoverTerm)
	if !ok {
		t.Fatalf("expected second term to be recover wrapper, got %T", decl.Productions[0].Terms[1])
	}
	if _, ok := recoveredGuard.Term.(*ast.GrammarGuardTerm); !ok {
		t.Fatalf("expected recovered guard inner term, got %T", recoveredGuard.Term)
	}
	if recoveredGuard.RecoverValue != nil {
		t.Fatal("expected guard recover term to omit fallback value")
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"stmt = state.statement_core() recover(ParseMessageKey.ExpectedStatement, until(\";\", token(TokenKind.EOF)), zeroed as Pascal.Stmt)",
		"guard((state.current_token().kind == TokenKind.END)) recover(ParseMessageKey.ExpectedStatement, until(\"end\", token(TokenKind.EOF)))",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsPrecedenceTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Pascal.Expr:
        precedence(left = state.term()):
			"+" right = state.term() -> build_add(left, right)
			"-" right = state.term() -> build_sub(left, right)
        return left
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	production := decl.Productions[0]
	if len(production.Terms) != 2 {
		t.Fatalf("expected precedence term plus return, got %d", len(production.Terms))
	}
	precedence, ok := production.Terms[0].(*ast.GrammarPrecedenceTerm)
	if !ok {
		t.Fatalf("expected first term to be precedence, got %T", production.Terms[0])
	}
	if precedence.LeftName != "left" {
		t.Fatalf("expected precedence left name left, got %q", precedence.LeftName)
	}
	if len(precedence.Arms) != 2 {
		t.Fatalf("expected two precedence arms, got %d", len(precedence.Arms))
	}
	if len(precedence.Arms[0].Bindings) != 1 || precedence.Arms[0].Bindings[0].Name != "right" {
		t.Fatalf("expected first precedence arm to bind right, got %#v", precedence.Arms[0].Bindings)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"precedence(left = state.term()):",
		"\"+\" right = state.term() -> build_add(left, right)",
		"\"-\" right = state.term() -> build_sub(left, right)",
		"return left",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsPrecedenceOperatorBinding(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Pascal.Expr:
        precedence(left = state.term()):
			op = choice("+", "-") right = state.term() -> build_binary(op, left, right)
        return left
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	precedence, ok := decl.Productions[0].Terms[0].(*ast.GrammarPrecedenceTerm)
	if !ok {
		t.Fatalf("expected first term to be precedence, got %T", decl.Productions[0].Terms[0])
	}
	if len(precedence.Arms) != 1 {
		t.Fatalf("expected one precedence arm, got %d", len(precedence.Arms))
	}
	if precedence.Arms[0].OpName != "op" {
		t.Fatalf("expected precedence arm operator binding name op, got %q", precedence.Arms[0].OpName)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"precedence(left = state.term()):",
		"op = choice(\"+\", \"-\") right = state.term() -> build_binary(op, left, right)",
		"return left",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsPostfixTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar LuaFrontend:
    suffix_expr(state: mutable ParserState&) -> Lua.Expr:
        result = postfix(left = state.atom()):
			"." name = token(TokenKind.IDENT) -> build_field(left, name)
			"(" arg = state.atom() close = ")" -> build_call(left, arg)
        return result
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	bind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first term to be postfix binding, got %T", decl.Productions[0].Terms[0])
	}
	postfix, ok := bind.Term.(*ast.GrammarPostfixTerm)
	if !ok {
		t.Fatalf("expected bound term to be postfix, got %T", bind.Term)
	}
	if postfix.LeftName != "left" {
		t.Fatalf("expected postfix left name left, got %q", postfix.LeftName)
	}
	if len(postfix.Arms) != 2 {
		t.Fatalf("expected two postfix arms, got %d", len(postfix.Arms))
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"result = postfix(left = state.atom()):",
		"\".\" name = token(TokenKind.IDENT) -> build_field(left, name)",
		"\"(\" arg = state.atom() close = \")\" -> build_call(left, arg)",
		"return result",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsExprTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        current = expr(state.current_token())
        parsed = expr(try state.expect_name_token(ParseMessageKey.ExpectedProgramName))
        return current
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	firstBind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first term to be expr binding, got %T", decl.Productions[0].Terms[0])
	}
	if _, ok := firstBind.Term.(*ast.GrammarExprTerm); !ok {
		t.Fatalf("expected bound term to be expr, got %T", firstBind.Term)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"current = expr(state.current_token())",
		"parsed = expr(try state.expect_name_token(ParseMessageKey.ExpectedProgramName))",
		"return current",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsTokenKindShorthandTerms(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    atom() -> Token:
        .INTEGER(tok)
        result = postfix(left = .IDENT):
			.DOT .IDENT(name) -> name
        return tok
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	firstBind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first term to be token-kind binding, got %T", decl.Productions[0].Terms[0])
	}
	if tokenKind, ok := firstBind.Term.(*ast.GrammarTokenKindTerm); !ok || tokenKind.Kind != "INTEGER" {
		t.Fatalf("expected .INTEGER(tok) binding, got %T %#v", firstBind.Term, firstBind.Term)
	}
	postfixBind, ok := decl.Productions[0].Terms[1].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected postfix binding, got %T", decl.Productions[0].Terms[1])
	}
	postfix, ok := postfixBind.Term.(*ast.GrammarPostfixTerm)
	if !ok {
		t.Fatalf("expected bound postfix term, got %T", postfixBind.Term)
	}
	if seed, ok := postfix.Seed.(*ast.GrammarTokenKindTerm); !ok || seed.Kind != "IDENT" {
		t.Fatalf("expected postfix seed .IDENT, got %T %#v", postfix.Seed, postfix.Seed)
	}
	if len(postfix.Arms) != 1 || len(postfix.Arms[0].Bindings) != 1 {
		t.Fatalf("expected one postfix arm with one inline token binding, got %#v", postfix.Arms)
	}
	if tokenKind, ok := postfix.Arms[0].Bindings[0].Term.(*ast.GrammarTokenKindTerm); !ok || tokenKind.Kind != "IDENT" || postfix.Arms[0].Bindings[0].Name != "name" {
		t.Fatalf("expected inline .IDENT(name) binding, got %#v", postfix.Arms[0].Bindings[0])
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		".INTEGER(tok)",
		"result = postfix(left = .IDENT):",
		".DOT .IDENT(name) -> name",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsAttemptTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        suffix = attempt(state.try_parse_suffix())
        return suffix
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	firstBind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first term to be attempt binding, got %T", decl.Productions[0].Terms[0])
	}
	if _, ok := firstBind.Term.(*ast.GrammarAttemptTerm); !ok {
		t.Fatalf("expected bound term to be attempt, got %T", firstBind.Term)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"suffix = attempt(state.try_parse_suffix())",
		"return suffix",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsGuardTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    statement(state: mutable ParserState&) -> Token:
        .IDENT(name_token)
        guard(state.lookahead_token(1).kind == TokenKind.ASSIGN)
        return name_token
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	guard, ok := decl.Productions[0].Terms[1].(*ast.GrammarGuardTerm)
	if !ok {
		t.Fatalf("expected second term to be guard, got %T", decl.Productions[0].Terms[1])
	}
	if guard.Cond == nil {
		t.Fatalf("expected guard to capture predicate expression")
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"guard((state.lookahead_token(1).kind == TokenKind.ASSIGN))",
		"return name_token",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsBoundPrecedenceLevel(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Pascal.Expr:
        atom = state.factor()
        term_level = precedence(left = atom):
			op = choice("*", "/") right = atom -> build_binary(op, left, right)
        expr_level = precedence(left = term_level):
			op = choice("+", "-") right = term_level -> build_binary(op, left, right)
        return expr_level
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.Productions[0].Terms) != 4 {
		t.Fatalf("expected bound atom, bound term precedence, bound expr precedence, and return; got %d terms", len(decl.Productions[0].Terms))
	}
	termLevel, ok := decl.Productions[0].Terms[1].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected second term to be bound precedence level, got %T", decl.Productions[0].Terms[1])
	}
	if _, ok := termLevel.Term.(*ast.GrammarPrecedenceTerm); !ok {
		t.Fatalf("expected bound term level to hold precedence term, got %T", termLevel.Term)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"atom = state.factor()",
		"term_level = precedence(left = atom):",
		"op = choice(\"*\", \"/\") right = atom -> build_binary(op, left, right)",
		"expr_level = precedence(left = term_level):",
		"op = choice(\"+\", \"-\") right = term_level -> build_binary(op, left, right)",
		"return expr_level",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsNamedPrecedenceLevels(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Pascal.Expr:
		result = precedence(additive):
			atom = state.factor()
			multiplicative(term_left = atom()):
				op = choice("*", "/") right = atom() -> build_binary(op, term_left, right)
			additive(expr_left = multiplicative()):
				op = choice("+", "-") right = multiplicative() -> build_binary(op, expr_left, right)
		return result
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	bind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first term to be precedence binding, got %T", decl.Productions[0].Terms[0])
	}
	precedence, ok := bind.Term.(*ast.GrammarPrecedenceTerm)
	if !ok {
		t.Fatalf("expected bound term to be precedence, got %T", bind.Term)
	}
	if precedence.Result != "additive" {
		t.Fatalf("expected precedence result additive, got %q", precedence.Result)
	}
	if len(precedence.Levels) != 3 {
		t.Fatalf("expected three named precedence levels, got %d", len(precedence.Levels))
	}
	if precedence.Levels[0].Name != "atom" || precedence.Levels[0].LeftName != "" {
		t.Fatalf("expected first named level to be helper atom, got %#v", precedence.Levels[0])
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"result = precedence(additive):",
		"atom = state.factor()",
		"precedence(additive):",
		"multiplicative(term_left = atom()):",
		"op = choice(\"*\", \"/\") right = atom() -> build_binary(op, term_left, right)",
		"additive(expr_left = multiplicative()):",
		"op = choice(\"+\", \"-\") right = multiplicative() -> build_binary(op, expr_left, right)",
		"return result",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsMemberStyleCallees(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    program(state: mutable ParserState&) -> Pascal.Decl:
        state.expect("program")
        name = state.parse_ident()
        state.expect(";")
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	production := decl.Productions[0]
	call, ok := production.Terms[0].(*ast.GrammarCallTerm)
	if !ok {
		t.Fatalf("expected first term to be grammar call, got %T", production.Terms[0])
	}
	if call.Name != "state.expect" || len(call.Args) != 1 {
		t.Fatalf("expected member-style callee state.expect with one arg, got %#v", call)
	}
	binding, ok := production.Terms[1].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected second term to be binding, got %T", production.Terms[1])
	}
	boundCall, ok := binding.Term.(*ast.GrammarCallTerm)
	if !ok || boundCall.Name != "state.parse_ident" {
		t.Fatalf("expected binding term to be state.parse_ident call, got %T %#v", binding.Term, binding.Term)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"state.expect(\"program\")",
		"name = state.parse_ident()",
		"state.expect(\";\")",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclPreservesNonCallMemberExpressions(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    assignment(state: mutable ParserState&) -> Pascal.Stmt:
        name_token = state.expect_ident_token()
        name_id = name_token.lexeme_key
        return zeroed as Pascal.Stmt
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	production := decl.Productions[0]
	binding, ok := production.Terms[1].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected second term to be binding, got %T", production.Terms[1])
	}
	memberExpr, ok := binding.Term.(*ast.GrammarCallTerm)
	if !ok {
		t.Fatalf("expected binding term to use grammar call term wrapper, got %T", binding.Term)
	}
	if memberExpr.Name != "name_token.lexeme_key" {
		t.Fatalf("expected member expression name_token.lexeme_key, got %#v", memberExpr)
	}
	if memberExpr.Explicit {
		t.Fatalf("expected member expression without explicit call syntax, got %#v", memberExpr)
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "name_id = name_token.lexeme_key") {
		t.Fatalf("expected formatted output to preserve member expression without call syntax, got:\n%s", formatted)
	}
}

func formatExprForTest(t *testing.T, expr ast.Expr) string {
	t.Helper()
	if expr == nil {
		return ""
	}
	file := &ast.File{Decls: []ast.Decl{&ast.ConstDecl{Name: "tmp", Value: expr}}}
	formatted := unparse.FormatFile(file)
	for _, line := range strings.Split(formatted, "\n") {
		if strings.HasPrefix(line, "const tmp = ") {
			return strings.TrimPrefix(line, "const tmp = ")
		}
	}
	t.Fatalf("failed to format expression %#v", expr)
	return ""
}

func formatTypeExprForTest(t *testing.T, typeExpr ast.TypeExpr) string {
	t.Helper()
	if typeExpr == nil {
		return ""
	}
	file := &ast.File{Decls: []ast.Decl{&ast.StructDecl{Name: "Tmp", Fields: []ast.FieldDecl{{Name: "value", Type: typeExpr}}}}}
	formatted := unparse.FormatFile(file)
	for _, line := range strings.Split(formatted, "\n") {
		if strings.Contains(line, "value: ") {
			parts := strings.SplitN(strings.TrimSpace(line), ": ", 2)
			if len(parts) == 2 {
				return parts[1]
			}
		}
	}
	t.Fatalf("failed to format type %#v", typeExpr)
	return ""
}
