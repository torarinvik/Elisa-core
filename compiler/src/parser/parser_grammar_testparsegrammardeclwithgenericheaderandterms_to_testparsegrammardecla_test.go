package parser

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"strings"
	"testing"
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
func TestParseGrammarDeclAllowsHelperOnlyGrammar(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalListGrammar over Token using ParserState:
    token:
        COMMA ","
        SEMICOLON ";"
    grammarfn separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
        separated item by sep until(stop)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if decl.Name != "PascalListGrammar" {
		t.Fatalf("expected helper grammar name, got %q", decl.Name)
	}
	if len(decl.Productions) != 0 {
		t.Fatalf("expected no productions in helper grammar, got %d", len(decl.Productions))
	}
	if len(decl.GrammarFns) != 1 {
		t.Fatalf("expected one helper grammar function, got %d", len(decl.GrammarFns))
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"grammar PascalListGrammar over Token using ParserState:",
		"token:",
		"grammarfn separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted helper grammar to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestParseGrammarDeclAllowsGrammarAliases(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalArgsGrammar over Token using ParserState:
    token:
        COMMA ","
        RPAREN ")"
    tokenset RParenSync:
        RPAREN
    grammar type separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
        separated item by sep until(stop)
    grammar alias arg_list = separated_by(item: expression(), stop: RParenSync)
    args() -> darray[Pascal.Expr]:
        values = arg_list
        return values
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.GrammarAliases) != 1 {
		t.Fatalf("expected one grammar alias, got %d", len(decl.GrammarAliases))
	}
	if decl.GrammarAliases[0].Name != "arg_list" {
		t.Fatalf("expected grammar alias name arg_list, got %q", decl.GrammarAliases[0].Name)
	}
	if _, ok := decl.GrammarAliases[0].Term.(*ast.GrammarApplyTerm); !ok {
		t.Fatalf("expected grammar alias term to be a grammar type application, got %T", decl.GrammarAliases[0].Term)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"grammar alias arg_list = separated_by(item: expression(), stop: RParenSync)",
		"values = arg_list",
		"return values",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestParseGrammarDeclAllowsBlockGrammarAliases(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalAtomGrammar over Token using ParserState:
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
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.GrammarAliases) != 1 {
		t.Fatalf("expected one grammar alias, got %d", len(decl.GrammarAliases))
	}
	if _, ok := decl.GrammarAliases[0].Term.(*ast.GrammarChoiceTerm); !ok {
		t.Fatalf("expected block grammar alias term to be a choice, got %T", decl.GrammarAliases[0].Term)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"grammar alias atom_choice:",
		"choice:",
		".IDENT",
		".INTEGER",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted block grammar alias output to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestParseGrammarDeclAllowsMultiTermBlockGrammarAliases(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalParenGrammar over Token using ParserState:
    token:
        LPAREN "("
        RPAREN ")"
    grammar alias parenthesized_atom:
        .LPAREN
        atom()
        .RPAREN
    atom() -> Token:
        value = parenthesized_atom
        return value
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	alias, ok := decl.GrammarAliases[0].Term.(*ast.GrammarSeqTerm)
	if !ok {
		t.Fatalf("expected multi-term grammar alias to be a seq, got %T", decl.GrammarAliases[0].Term)
	}
	if len(alias.Terms) != 3 {
		t.Fatalf("expected three alias seq terms, got %d", len(alias.Terms))
	}
	formatted := unparse.FormatFile(file)
	if strings.Contains(formatted, "grammar alias parenthesized_atom:\n        seq:") {
		t.Fatalf("expected alias block to avoid redundant nested seq, got:\n%s", formatted)
	}
	for _, want := range []string{
		"grammar alias parenthesized_atom:",
		".LPAREN",
		"atom()",
		".RPAREN",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted multi-term grammar alias output to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestParseGrammarDeclAllowsParameterizedGrammarAliases(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalArgsGrammar over Token using ParserState:
    token:
        COMMA ","
        SEMICOLON ";"
        RPAREN ")"
    tokenset RParenSync:
        RPAREN
    grammar type separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
        separated item by sep until(stop)
    grammar alias expr_items(stop: tokenset, sep: grammar = .COMMA):
        expression() |> separated_by(stop: stop, sep: sep)
    args() -> darray[Pascal.Expr]:
        values = expr_items(stop: RParenSync, sep: .SEMICOLON)
        return values
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.GrammarAliases) != 1 {
		t.Fatalf("expected one grammar alias, got %d", len(decl.GrammarAliases))
	}
	alias := decl.GrammarAliases[0]
	if alias.Name != "expr_items" || len(alias.Params) != 2 {
		t.Fatalf("expected parameterized expr_items alias with two params, got %#v", alias)
	}
	if alias.Params[0].Type.Kind != "tokenset" || alias.Params[1].Type.Kind != "grammar" || alias.Params[1].Default == nil {
		t.Fatalf("expected tokenset stop and defaulted grammar sep params, got %#v", alias.Params)
	}
	if _, ok := alias.Term.(*ast.GrammarApplyTerm); !ok {
		t.Fatalf("expected alias body to be pipeline apply, got %T", alias.Term)
	}
	bind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected production binding, got %T", decl.Productions[0].Terms[0])
	}
	apply, ok := bind.Term.(*ast.GrammarApplyTerm)
	if !ok || apply.Name != "expr_items" || len(apply.Args) != 2 {
		t.Fatalf("expected expr_items apply with two args, got %#v", bind.Term)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"grammar alias expr_items(stop: tokenset, sep: grammar = .COMMA) = expression() |> separated_by(stop: stop, sep: sep)",
		"expression() |> separated_by(stop: stop, sep: sep)",
		"values = expr_items(stop: RParenSync, sep: .SEMICOLON)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted parameterized alias output to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestParseGrammarDeclAllowsInterleavedSupportDecls(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalArgsGrammar over Token using ParserState:
    token:
        IDENT
        COMMA ","
        RPAREN ")"
    first_arg() -> Token:
        token = .IDENT
        return token
    tokenset RParenSync:
        RPAREN
        token(TokenKind.EOF)
    grammar alias arg_items = required(.IDENT, ParseMessageKey.ExpectedDeclName) |> separated_by(stop: RParenSync)
    recovery ArgRecovery:
        message ParseMessageKey.ExpectedDeclName
        until RParenSync
    args() -> darray[Token]:
        values = arg_items recover ArgRecovery
        return values
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors for interleaved support declarations: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.Productions) != 2 {
		t.Fatalf("expected two productions around interleaved support declarations, got %d", len(decl.Productions))
	}
	if len(decl.TokenSets) != 1 || len(decl.GrammarAliases) != 1 || len(decl.RecoveryPolicies) != 1 {
		t.Fatalf("expected one token set, alias, and recovery policy, got tokenSets=%d aliases=%d recoveries=%d", len(decl.TokenSets), len(decl.GrammarAliases), len(decl.RecoveryPolicies))
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"first_arg() -> Token:",
		"tokenset RParenSync:",
		"grammar alias arg_items = required(.IDENT, ParseMessageKey.ExpectedDeclName) |> separated_by(stop: RParenSync)",
		"recovery ArgRecovery:",
		"args() -> darray[Token]:",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted interleaved grammar output to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestParseGrammarDeclAllowsParameterizedAliasBodyToUseImportedHelper(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalArgsGrammar over Token using ParserState uses PascalListGrammar:
    token:
        COMMA ","
        RPAREN ")"
    tokenset RParenSync:
        RPAREN
    grammar alias expr_items(stop: tokenset, sep: grammar = .COMMA):
        expression() |> separated_by(stop: stop, sep: sep)
    args() -> darray[Pascal.Expr]:
        values = expr_items(stop: RParenSync)
        return values
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors for imported helper in alias body: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.Uses) != 1 {
		t.Fatalf("expected PascalListGrammar use, got %#v", decl.Uses)
	}
}
func TestParseGrammarDeclRejectsRecursiveGrammarAliases(t *testing.T) {
	_, errs := parseSourceFile(t, `grammar PascalArgsGrammar over Token using ParserState:
    grammar alias alpha = beta
    grammar alias beta:
        alpha
    args() -> Token:
        value = alpha
        return value
`)
	if len(errs) == 0 {
		t.Fatal("expected parser error for recursive grammar aliases")
	}
	for _, err := range errs {
		if strings.Contains(err, "recursive grammar alias") {
			return
		}
	}
	t.Fatalf("expected recursive grammar alias parser error, got:\n%s", strings.Join(errs, "\n"))
}
func TestParseGrammarDeclRejectsDuplicateGrammarAliases(t *testing.T) {
	_, errs := parseSourceFile(t, `grammar PascalArgsGrammar over Token using ParserState:
    grammar alias arg_items = .IDENT
    grammar alias arg_items = .INTEGER
    args() -> Token:
        value = arg_items
        return value
`)
	if len(errs) == 0 {
		t.Fatal("expected parser error for duplicate grammar aliases")
	}
	for _, err := range errs {
		if strings.Contains(err, "duplicate grammar alias \"arg_items\"") {
			return
		}
	}
	t.Fatalf("expected duplicate grammar alias parser error, got:\n%s", strings.Join(errs, "\n"))
}
func TestParseGrammarDeclAllowsStructuredHeaderMetadata(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalGrammar[B] over PascalToken using PascalParseCtx:
    error PascalFrontendError
    cursor parser
    alloc arena
    token_kind PascalTokenKind
    eof PascalTokenKind.EOF
    token_field tag
    current peek_token
    advance bump_token
    expect expect_text
    expect_kind expect_token
    record_error note_error
	token .PROGRAM "program"
	token .IDENT
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
	if got := formatTypeExprForTest(t, decl.TokenKindType); got != "PascalTokenKind" {
		t.Fatalf("expected token_kind type PascalTokenKind, got %q", got)
	}
	if got := formatExprForTest(t, decl.EOFExpr); got != "PascalTokenKind.EOF" {
		t.Fatalf("expected eof expr PascalTokenKind.EOF, got %q", got)
	}
	if decl.TokenKindField != "tag" {
		t.Fatalf("expected token_field tag, got %q", decl.TokenKindField)
	}
	if decl.CurrentFunc != "peek_token" {
		t.Fatalf("expected current peek_token, got %q", decl.CurrentFunc)
	}
	if decl.AdvanceFunc != "bump_token" {
		t.Fatalf("expected advance bump_token, got %q", decl.AdvanceFunc)
	}
	if decl.ExpectFunc != "expect_text" {
		t.Fatalf("expected expect expect_text, got %q", decl.ExpectFunc)
	}
	if decl.ExpectKindFunc != "expect_token" {
		t.Fatalf("expected expect_kind expect_token, got %q", decl.ExpectKindFunc)
	}
	if decl.RecordErrorFunc != "note_error" {
		t.Fatalf("expected record_error note_error, got %q", decl.RecordErrorFunc)
	}
	if len(decl.TokenAliases) != 2 {
		t.Fatalf("expected two token aliases, got %d", len(decl.TokenAliases))
	}
	if decl.TokenAliases[0].Kind != "PROGRAM" || !decl.TokenAliases[0].HasLiteral || decl.TokenAliases[0].Literal != "program" {
		t.Fatalf("expected first token alias to be .PROGRAM \"program\", got %#v", decl.TokenAliases[0])
	}
	if decl.TokenAliases[1].Kind != "IDENT" || decl.TokenAliases[1].HasLiteral {
		t.Fatalf("expected second token alias to be bare .IDENT, got %#v", decl.TokenAliases[1])
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
		"token_kind PascalTokenKind",
		"eof PascalTokenKind.EOF",
		"token_field tag",
		"current peek_token",
		"advance bump_token",
		"expect expect_text",
		"expect_kind expect_token",
		"record_error note_error",
		"token:",
		"PROGRAM \"program\"",
		"IDENT",
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
func TestParseGrammarDeclAllowsTokenAliasBlock(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalGrammar over Token using ParserState:
    token:
        IDENT
        INTEGER
        LPAREN "("
        .RPAREN ")"
    program() -> Token:
        token = .IDENT
        return token
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.TokenAliases) != 4 {
		t.Fatalf("expected four token aliases, got %d", len(decl.TokenAliases))
	}
	wants := []struct {
		kind       string
		literal    string
		hasLiteral bool
	}{
		{kind: "IDENT"},
		{kind: "INTEGER"},
		{kind: "LPAREN", literal: "(", hasLiteral: true},
		{kind: "RPAREN", literal: ")", hasLiteral: true},
	}
	for i, want := range wants {
		alias := decl.TokenAliases[i]
		if alias.Kind != want.kind || alias.Literal != want.literal || alias.HasLiteral != want.hasLiteral {
			t.Fatalf("token alias %d: expected %#v, got %#v", i, want, alias)
		}
	}
}
