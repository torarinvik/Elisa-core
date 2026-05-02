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

func TestParseGrammarDeclAllowsTokenLookupHeader(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalGrammar over Token using ParserState:
    token_lookup token_kind_for_text
    token:
        BEGIN "begin"
    program() -> Token:
        "begin"
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if decl.TokenLookupFunc != "token_kind_for_text" {
		t.Fatalf("expected token lookup function token_kind_for_text, got %q", decl.TokenLookupFunc)
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "token_lookup token_kind_for_text") {
		t.Fatalf("expected formatted output to contain token_lookup header, got:\n%s", formatted)
	}
}

func TestParseGrammarDeclAllowsTokenLookupCompareHeader(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalGrammar over Token using ParserState:
    token_lookup token_kind_for_text
    token_lookup_compare pascal_text_eq_keyword
    token:
        BEGIN "begin"
    program() -> Token:
        "begin"
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if decl.TokenLookupCompareFunc != "pascal_text_eq_keyword" {
		t.Fatalf("expected token lookup compare function pascal_text_eq_keyword, got %q", decl.TokenLookupCompareFunc)
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "token_lookup_compare pascal_text_eq_keyword") {
		t.Fatalf("expected formatted output to contain token_lookup_compare header, got:\n%s", formatted)
	}
}

func TestParseGrammarDeclAllowsTokenEnumHeader(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalGrammar over Token using ParserState:
    token_enum PascalTokenKind of u16
    token:
        PROGRAM "program"
    program() -> Token:
        "program"
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if decl.TokenEnumName != "PascalTokenKind" {
		t.Fatalf("expected token enum PascalTokenKind, got %q", decl.TokenEnumName)
	}
	if got := formatTypeExprForTest(t, decl.TokenEnumStorage); got != "u16" {
		t.Fatalf("expected token enum storage u16, got %q", got)
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "token_enum PascalTokenKind of u16") {
		t.Fatalf("expected formatted output to contain token_enum header, got:\n%s", formatted)
	}
}

func TestParseGrammarDeclAllowsTokenSets(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalStmtGrammar over Token using ParserState:
    token:
        SEMICOLON ";"
        END "end"
        ELSE "else"
    tokenset StatementSync:
        SEMICOLON
        END
        ELSE
        token(TokenKind.EOF)
    tokenset NestedSync:
        StatementSync
        RPAREN
    recovery StatementRecovery:
        message ParseMessageKey.ExpectedStatement
        until StatementSync
    statement() -> Pascal.Stmt recover StatementRecovery:
        statements = separated statement_core() by .SEMICOLON until(StatementSync)
        return zeroed as Pascal.Stmt
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.TokenSets) != 2 {
		t.Fatalf("expected two token sets, got %d", len(decl.TokenSets))
	}
	if decl.TokenSets[0].Name != "StatementSync" || len(decl.TokenSets[0].Terms) != 4 {
		t.Fatalf("expected StatementSync with four terms, got %#v", decl.TokenSets[0])
	}
	if _, ok := decl.RecoveryPolicies[0].Until[0].(*ast.GrammarTokenSetRefTerm); !ok {
		t.Fatalf("expected recovery until to reference token set, got %T", decl.RecoveryPolicies[0].Until[0])
	}
	if _, ok := decl.TokenSets[1].Terms[0].(*ast.GrammarTokenSetRefTerm); !ok {
		t.Fatalf("expected nested token set item to reference token set, got %T", decl.TokenSets[1].Terms[0])
	}
	if ref, ok := decl.TokenSets[1].Terms[1].(*ast.GrammarTokenSetRefTerm); !ok || ref.Name != "RPAREN" {
		t.Fatalf("expected parser to preserve bare token-set item for lowering, got %T %#v", decl.TokenSets[1].Terms[1], decl.TokenSets[1].Terms[1])
	}
	production := decl.Productions[0]
	assign, ok := production.Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first production term to be binding, got %T", production.Terms[0])
	}
	separated, ok := assign.Term.(*ast.GrammarSeparatedTerm)
	if !ok {
		t.Fatalf("expected assignment term to be separated, got %T", assign.Term)
	}
	if _, ok := separated.Until[0].(*ast.GrammarTokenSetRefTerm); !ok {
		t.Fatalf("expected separated until to reference token set, got %T", separated.Until[0])
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"tokenset StatementSync:",
		"SEMICOLON",
		"token(TokenKind.EOF)",
		"until StatementSync",
		"until(StatementSync)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsGrammarFns(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalArgsGrammar over Token using ParserState:
    token:
        COMMA ","
        RPAREN ")"
    tokenset RParenSync:
        RPAREN
        token(TokenKind.EOF)
    grammarfn separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
        separated item by sep until(stop)
    grammarfn recovered_expr[T](item: grammar -> T, message: expr, stop: tokenset, fallback: expr) -> grammar -> T:
        item recover(message, until(stop), fallback)
    args() -> darray[Pascal.Expr]:
        values = apply separated_by(item: expression(), stop: RParenSync)
        return values
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.GrammarFns) != 2 {
		t.Fatalf("expected two grammarfns, got %d", len(decl.GrammarFns))
	}
	if decl.GrammarFns[0].Name != "separated_by" || len(decl.GrammarFns[0].Params) != 3 {
		t.Fatalf("expected separated_by grammarfn with three params, got %#v", decl.GrammarFns[0])
	}
	if len(decl.GrammarFns[0].TypeParams) != 1 || decl.GrammarFns[0].TypeParams[0] != "T" {
		t.Fatalf("expected grammarfn type param T, got %#v", decl.GrammarFns[0].TypeParams)
	}
	if decl.GrammarFns[0].Params[0].Type.Kind != "grammar" || formatTypeExprForTest(t, decl.GrammarFns[0].Params[0].Type.Result) != "T" {
		t.Fatalf("expected first grammarfn param to be grammar -> T, got %#v", decl.GrammarFns[0].Params[0].Type)
	}
	if decl.GrammarFns[0].Params[1].Type.Kind != "tokenset" {
		t.Fatalf("expected second grammarfn param to be tokenset, got %#v", decl.GrammarFns[0].Params[1].Type)
	}
	if decl.GrammarFns[0].Params[2].Default == nil {
		t.Fatalf("expected third grammarfn param to have a default")
	}
	if decl.GrammarFns[0].Return.Kind != "grammar" || formatTypeExprForTest(t, decl.GrammarFns[0].Return.Result) != "darray[T]" {
		t.Fatalf("expected grammarfn return grammar -> darray[T], got %#v", decl.GrammarFns[0].Return)
	}
	bind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first production term to be binding, got %T", decl.Productions[0].Terms[0])
	}
	apply, ok := bind.Term.(*ast.GrammarApplyTerm)
	if !ok {
		t.Fatalf("expected binding term to be grammar apply, got %T", bind.Term)
	}
	if apply.Name != "separated_by" || len(apply.Args) != 2 {
		t.Fatalf("expected separated_by apply with two args, got %#v", apply)
	}
	if apply.Args[0].Name != "item" || apply.Args[1].Name != "stop" {
		t.Fatalf("expected named apply args, got %#v", apply.Args)
	}
	if _, ok := apply.Args[1].Term.(*ast.GrammarTokenSetRefTerm); !ok {
		t.Fatalf("expected second apply arg to be token set ref, got %T", apply.Args[1].Term)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"grammarfn separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:",
		"grammarfn recovered_expr[T](item: grammar -> T, message: expr, stop: tokenset, fallback: expr) -> grammar -> T:",
		"separated item by sep until(stop)",
		"values = apply separated_by(item: expression(), stop: RParenSync)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsDirectNamedGrammarFnApply(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalArgsGrammar over Token using ParserState:
    token:
        COMMA ","
        RPAREN ")"
    tokenset RParenSync:
        RPAREN
    grammar type separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
        separated item by sep until(stop)
    args() -> darray[Pascal.Expr]:
        values = separated_by(item: expression(), stop: RParenSync)
        return values
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
		t.Fatalf("expected first production term to be binding, got %T", decl.Productions[0].Terms[0])
	}
	apply, ok := bind.Term.(*ast.GrammarApplyTerm)
	if !ok {
		t.Fatalf("expected binding term to be grammar apply, got %T", bind.Term)
	}
	if !apply.Direct {
		t.Fatalf("expected direct grammar apply to be preserved")
	}
	if apply.Name != "separated_by" || len(apply.Args) != 2 {
		t.Fatalf("expected separated_by direct apply with two args, got %#v", apply)
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "values = separated_by(item: expression(), stop: RParenSync)") {
		t.Fatalf("expected direct grammar apply to format without apply keyword, got:\n%s", formatted)
	}
}

func TestParseGrammarDeclAllowsGrammarPipelineApply(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalArgsGrammar over Token using ParserState:
    token:
        COMMA ","
        RPAREN ")"
    tokenset RParenSync:
        RPAREN
    grammar type separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
        separated item by sep until(stop)
    args() -> darray[Pascal.Expr]:
        values = expression() |> separated_by(stop: RParenSync)
        return values
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
		t.Fatalf("expected first production term to be binding, got %T", decl.Productions[0].Terms[0])
	}
	apply, ok := bind.Term.(*ast.GrammarApplyTerm)
	if !ok {
		t.Fatalf("expected binding term to be grammar apply, got %T", bind.Term)
	}
	if !apply.Direct || !apply.Piped {
		t.Fatalf("expected grammar pipeline to be preserved as direct piped apply, got %#v", apply)
	}
	if apply.Name != "separated_by" || len(apply.Args) != 2 {
		t.Fatalf("expected separated_by piped apply with two args, got %#v", apply)
	}
	if apply.Args[0].Name != "" {
		t.Fatalf("expected piped input to be an injected positional arg, got %#v", apply.Args[0])
	}
	if call, ok := apply.Args[0].Term.(*ast.GrammarCallTerm); !ok || call.Name != "expression" {
		t.Fatalf("expected piped input to be expression(), got %#v", apply.Args[0].Term)
	}
	if apply.Args[1].Name != "stop" {
		t.Fatalf("expected explicit named stop arg, got %#v", apply.Args[1])
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "values = expression() |> separated_by(stop: RParenSync)") {
		t.Fatalf("expected grammar pipeline to round-trip, got:\n%s", formatted)
	}
}

func TestParseGrammarDeclAllowsFirstTokenSetItem(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalStmtGrammar over Token using ParserState:
    tokenset StatementStart = first(statement)
    statement() -> Pascal.Stmt:
        .IDENT(name)
        return zeroed as Pascal.Stmt
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.GrammarDecl)
	if len(decl.TokenSets) != 1 || len(decl.TokenSets[0].Terms) != 1 {
		t.Fatalf("expected one tokenset with one term, got %#v", decl.TokenSets)
	}
	first, ok := decl.TokenSets[0].Terms[0].(*ast.GrammarFirstTerm)
	if !ok {
		t.Fatalf("expected first tokenset item to be first(...), got %T", decl.TokenSets[0].Terms[0])
	}
	if first.Name != "statement" {
		t.Fatalf("expected first() to reference statement, got %q", first.Name)
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "first(statement)") {
		t.Fatalf("expected first() tokenset item to round-trip, got:\n%s", formatted)
	}
}

func TestParseGrammarDeclAllowsFirstTermLookahead(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalStmtGrammar over Token using ParserState:
    statement() -> Pascal.Stmt:
        .IDENT(name)
        return zeroed as Pascal.Stmt
    block() -> Pascal.Stmt:
        lookahead(first(statement))
        return zeroed as Pascal.Stmt
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.GrammarDecl)
	lookahead, ok := decl.Productions[1].Terms[0].(*ast.GrammarLookaheadTerm)
	if !ok {
		t.Fatalf("expected first block term to be lookahead, got %T", decl.Productions[1].Terms[0])
	}
	first, ok := lookahead.Term.(*ast.GrammarFirstTerm)
	if !ok {
		t.Fatalf("expected lookahead body to be first(...), got %T", lookahead.Term)
	}
	if first.Name != "statement" {
		t.Fatalf("expected first() to reference statement, got %q", first.Name)
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "lookahead(first(statement))") {
		t.Fatalf("expected first() lookahead to round-trip, got:\n%s", formatted)
	}
}

func TestParseGrammarFnApplyDiagnostics(t *testing.T) {
	_, errs := parseSourceFile(t, `grammar PascalArgsGrammar over Token using ParserState:
    token:
        COMMA ","
        RPAREN ")"
    tokenset RParenSync:
        RPAREN
    grammarfn comma_list(item: grammar, stop: tokenset, sep: grammar = .COMMA):
        separated item by .COMMA until(stop)
    grammarfn recovered(item: grammar, message: expr):
        item recover(message, until(RParenSync))
    args() -> darray[Token]:
        missing = apply comma_list(token(TokenKind.IDENT))
        wrong = apply comma_list(RParenSync, token(TokenKind.EOF))
        wrong_expr = apply recovered(token(TokenKind.IDENT), token(TokenKind.EOF))
        unknown_arg = apply comma_list(item: token(TokenKind.IDENT), nope: RParenSync)
        duplicate = apply comma_list(item: token(TokenKind.IDENT), item: token(TokenKind.STRING), stop: RParenSync)
        late_positional = apply comma_list(item: token(TokenKind.IDENT), RParenSync)
        too_many = apply comma_list(token(TokenKind.IDENT), RParenSync, .COMMA, .RPAREN)
        unknown = apply not_a_helper(token(TokenKind.IDENT))
        return missing
`)
	for _, want := range []string{
		"missing argument \"stop\" for grammarfn comma_list",
		"argument \"item\" expects grammar, got tokenset",
		"argument \"stop\" expects tokenset, got grammar",
		"argument \"message\" expects expr, got grammar",
		"unknown argument \"nope\" for grammarfn comma_list",
		"duplicate argument \"item\" for grammarfn comma_list",
		"positional argument cannot follow named argument in grammarfn comma_list",
		"too many positional arguments for grammarfn comma_list",
		"unknown grammar function \"not_a_helper\"",
	} {
		found := false
		for _, err := range errs {
			if strings.Contains(err, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected parser errors to contain %q, got:\n%s", want, strings.Join(errs, "\n"))
		}
	}
}

func TestParseGrammarDeclAllowsProductionAugmentationSyntax(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalStmtGrammar over Token using ParserState:
    statement() -> Token:
        .IDENT(tok)
        return tok

extend grammar PascalStmtGrammar:
    statement +=
        .INTEGER(tok)
        return tok
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 2 {
		t.Fatalf("expected two grammar decls, got %d", len(file.Decls))
	}
	extDecl, ok := file.Decls[1].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected extension grammar decl, got %T", file.Decls[1])
	}
	if len(extDecl.Productions) != 1 {
		t.Fatalf("expected one augmented production, got %d", len(extDecl.Productions))
	}
	production := extDecl.Productions[0]
	if !production.Append {
		t.Fatal("expected production augmentation to set Append")
	}
	if production.Name != "statement" || len(production.Terms) != 2 {
		t.Fatalf("unexpected augmented production shape: %#v", production)
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "statement +=") {
		t.Fatalf("expected formatted output to preserve += production header, got:\n%s", formatted)
	}
}

func TestParseGrammarDeclAllowsGroupedProductionAugmentationSyntax(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalStmtGrammar over Token using ParserState:
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
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 2 {
		t.Fatalf("expected two grammar decls, got %d", len(file.Decls))
	}
	extDecl, ok := file.Decls[1].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected extension grammar decl, got %T", file.Decls[1])
	}
	if len(extDecl.Productions) != 2 {
		t.Fatalf("expected grouped augmentation to expand into two productions, got %d", len(extDecl.Productions))
	}
	for index, production := range extDecl.Productions {
		if !production.Append {
			t.Fatalf("expected grouped production %d to set Append", index)
		}
		if production.Name != "statement" || len(production.Terms) != 2 {
			t.Fatalf("unexpected grouped production %d shape: %#v", index, production)
		}
	}
	formatted := unparse.FormatFile(file)
	if strings.Count(formatted, "statement +=:") != 1 {
		t.Fatalf("expected formatted output to use grouped +=: form once, got:\n%s", formatted)
	}
	for _, want := range []string{".INTEGER(tok)", ".STRING(tok)", "|"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclRequiresPipeToSplitGroupedProductionAugmentationArms(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalStmtGrammar over Token using ParserState:
    statement() -> Token:
        .IDENT(tok)
        return tok

extend grammar PascalStmtGrammar:
    statement +=:
        .INTEGER(tok)
        return tok

        .STRING(tok)
        return tok
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	extDecl, ok := file.Decls[1].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected extension grammar decl, got %T", file.Decls[1])
	}
	if len(extDecl.Productions) != 1 {
		t.Fatalf("expected grouped append without '|' to stay a single production, got %d", len(extDecl.Productions))
	}
	production := extDecl.Productions[0]
	if !production.Append {
		t.Fatal("expected grouped append production to set Append")
	}
	if len(production.Terms) != 4 {
		t.Fatalf("expected missing '|' case to keep all terms in one append arm, got %d", len(production.Terms))
	}
	formatted := unparse.FormatFile(file)
	if strings.Contains(formatted, "statement +=:") {
		t.Fatalf("expected formatter to avoid grouped +=: form when only one append arm remains, got:\n%s", formatted)
	}
	if strings.Contains(formatted, "|") {
		t.Fatalf("expected formatter output to omit pipe separators when none were parsed, got:\n%s", formatted)
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

func TestParseGrammarEnvDeclAndGrammarWithEnv(t *testing.T) {
	file, errs := parseSourceFile(t, `grammarenv PascalEnv over Token using ParserState:
    cursor state
    alloc alloc
    token_kind TokenKind
	tokens PascalTokenGrammar
    eof TokenKind.EOF
    token_field kind
    current current_token
    advance advance_token
    expect expect
    expect_kind expect_kind
    record_error record_parse_error

grammar PascalExprGrammar with PascalEnv uses PascalCommonGrammar:
    token:
        IDENT
    expr() -> Token:
        token = .IDENT
        return token
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	env, ok := file.Decls[0].(*ast.GrammarEnvDecl)
	if !ok {
		t.Fatalf("expected grammarenv decl, got %T", file.Decls[0])
	}
	if env.Name != "PascalEnv" {
		t.Fatalf("expected env name PascalEnv, got %q", env.Name)
	}
	if got := formatTypeExprForTest(t, env.OverType); got != "Token" {
		t.Fatalf("expected env over type Token, got %q", got)
	}
	if got := formatTypeExprForTest(t, env.UsingType); got != "ParserState" {
		t.Fatalf("expected env using type ParserState, got %q", got)
	}
	if env.TokenGrammarName != "PascalTokenGrammar" {
		t.Fatalf("expected env token grammar PascalTokenGrammar, got %q", env.TokenGrammarName)
	}
	decl, ok := file.Decls[1].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[1])
	}
	if got := formatTypeExprForTest(t, decl.EnvType); got != "PascalEnv" {
		t.Fatalf("expected grammar env PascalEnv, got %q", got)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"grammarenv PascalEnv over Token using ParserState:",
		"cursor state",
		"tokens PascalTokenGrammar",
		"record_error record_parse_error",
		"grammar PascalExprGrammar with PascalEnv uses PascalCommonGrammar:",
		"token:",
		"IDENT",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsNamedRecoveryPolicies(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor state
	alloc scratch
	recovery StatementRecovery:
		message ParseMessageKey.ExpectedStatement
		until .SEMICOLON, .END, token(TokenKind.EOF)
		fallback zeroed as Pascal.Stmt
	statement() -> Pascal.Stmt recover StatementRecovery:
		stmt = state.statement_core() recover StatementRecovery
		return stmt
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.RecoveryPolicies) != 1 {
		t.Fatalf("expected one recovery policy, got %d", len(decl.RecoveryPolicies))
	}
	policy := decl.RecoveryPolicies[0]
	if policy.Name != "StatementRecovery" {
		t.Fatalf("expected policy name StatementRecovery, got %q", policy.Name)
	}
	if policy.Message == nil {
		t.Fatal("expected recovery policy message")
	}
	if len(policy.Until) != 3 {
		t.Fatalf("expected three recovery stop terms, got %d", len(policy.Until))
	}
	if policy.Fallback == nil {
		t.Fatal("expected recovery policy fallback")
	}
	production := decl.Productions[0]
	if production.RecoverPolicy != "StatementRecovery" {
		t.Fatalf("expected production to use StatementRecovery, got %q", production.RecoverPolicy)
	}
	bind, ok := production.Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first production term to be binding, got %T", production.Terms[0])
	}
	recoverTerm, ok := bind.Term.(*ast.GrammarRecoverTerm)
	if !ok {
		t.Fatalf("expected binding inner term to be recover wrapper, got %T", bind.Term)
	}
	if recoverTerm.RecoverPolicy != "StatementRecovery" {
		t.Fatalf("expected term to use StatementRecovery, got %q", recoverTerm.RecoverPolicy)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"recovery StatementRecovery:",
		"message ParseMessageKey.ExpectedStatement",
		"until .SEMICOLON, .END, token(TokenKind.EOF)",
		"fallback zeroed as Pascal.Stmt",
		"statement() -> Pascal.Stmt recover StatementRecovery:",
		"stmt = state.statement_core() recover StatementRecovery",
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
	expr, ok := ret.Term.(*ast.GrammarExprTerm)
	if !ok {
		t.Fatalf("expected return term to wrap a grammar expr term, got %T", ret.Term)
	}
	if _, ok := expr.Expr.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected return term to carry expression AST, got %T", expr.Expr)
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

func TestParseGrammarDeclAllowsReturnSeqBlockTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar Demo:
    produce() -> Token:
        return seq:
            .IDENT(token)
            expr(token)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	ret, ok := decl.Productions[0].Terms[0].(*ast.GrammarReturnTerm)
	if !ok {
		t.Fatalf("expected first term to be explicit return, got %T", decl.Productions[0].Terms[0])
	}
	seq, ok := ret.Term.(*ast.GrammarSeqTerm)
	if !ok {
		t.Fatalf("expected return term to hold seq block, got %T", ret.Term)
	}
	if len(seq.Terms) != 2 {
		t.Fatalf("expected return seq to contain 2 terms, got %d", len(seq.Terms))
	}
	if _, ok := seq.Terms[0].(*ast.GrammarBindTerm); !ok {
		t.Fatalf("expected first return seq term to bind token, got %T", seq.Terms[0])
	}
	if _, ok := seq.Terms[1].(*ast.GrammarExprTerm); !ok {
		t.Fatalf("expected second return seq term to be expr term, got %T", seq.Terms[1])
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"return seq:",
		".IDENT(token)",
		"expr(token)",
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
		"declarations = variable_section()?",
		"statements = separated statement() by \";\"",
		"choice(assignment(), compound_statement(), if_statement(), while_statement())",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarChoiceBlockTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalExprGrammar over Token using ParserState:
    expression() -> Expr:
        atom = choice:
            prefix(.PLUS, .MINUS, .NOT) atom() -> build_unary(op, operand)
            if_expr()
            fn_expr()
            let_expr()
            list_expr()
            paren_or_tuple_expr()
            seq:
                .IDENT(token)
                expr(make_name_expr(token))
        return atom
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
	choice, ok := bind.Term.(*ast.GrammarChoiceTerm)
	if !ok {
		t.Fatalf("expected binding to use choice term, got %T", bind.Term)
	}
	if len(choice.Options) != 7 {
		t.Fatalf("expected seven choice options, got %d", len(choice.Options))
	}
	if _, ok := choice.Options[0].(*ast.GrammarSeqTerm); !ok {
		t.Fatalf("expected prefix choice option to desugar to a seq term, got %T", choice.Options[0])
	}
	if _, ok := choice.Options[6].(*ast.GrammarSeqTerm); !ok {
		t.Fatalf("expected final choice option to be a seq term, got %T", choice.Options[6])
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"atom = choice:",
		"prefix(.PLUS, .MINUS, .NOT) atom() -> build_unary(op, operand)",
		"if_expr()",
		"paren_or_tuple_expr()",
		"seq:",
		".IDENT(token)",
		"expr(make_name_expr(token))",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarChoicePipeShorthandDesugarsToChoiceTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalExprGrammar over Token using ParserState:
	operator() -> Token:
		op = "*" | "/" | "div"
		return op
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
		t.Fatalf("expected first term to be a binding, got %T", decl.Productions[0].Terms[0])
	}
	choice, ok := bind.Term.(*ast.GrammarChoiceTerm)
	if !ok {
		t.Fatalf("expected pipe shorthand to parse as choice term, got %T", bind.Term)
	}
	if len(choice.Options) != 3 {
		t.Fatalf("expected three choice options, got %d", len(choice.Options))
	}
	for index, want := range []string{"*", "/", "div"} {
		token, ok := choice.Options[index].(*ast.GrammarTokenTerm)
		if !ok || token.Value != want {
			t.Fatalf("expected option %d to be %q token term, got %T %#v", index, want, choice.Options[index], choice.Options[index])
		}
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, `op = choice("*", "/", "div")`) {
		t.Fatalf("expected formatted output to contain canonical choice form, got:\n%s", formatted)
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
		"statements = separated state.statement() by \";\" until(\"end\", token(TokenKind.EOF))",
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
		"values = separated state.expression() by \",\" until(\")\", token(TokenKind.EOF))",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsOptionalAndRepeatSuffixShorthand(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor state
	items() -> darray[Token]:
		many = .IDENT* until(")", token(TokenKind.EOF))
		maybe = .IDENT?
		return many
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
	manyBind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first term to be binding, got %T", decl.Productions[0].Terms[0])
	}
	manyRepeat, ok := manyBind.Term.(*ast.GrammarRepeatTerm)
	if !ok {
		t.Fatalf("expected first binding to use repeat shorthand, got %T", manyBind.Term)
	}
	if _, ok := manyRepeat.Elem.(*ast.GrammarTokenKindTerm); !ok {
		t.Fatalf("expected repeat shorthand elem to stay token kind term, got %T", manyRepeat.Elem)
	}
	if len(manyRepeat.Until) != 2 {
		t.Fatalf("expected repeat shorthand to preserve until stop set, got %d stops", len(manyRepeat.Until))
	}
	maybeBind, ok := decl.Productions[0].Terms[1].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected second term to be binding, got %T", decl.Productions[0].Terms[1])
	}
	maybeOptional, ok := maybeBind.Term.(*ast.GrammarOptionalTerm)
	if !ok {
		t.Fatalf("expected second binding to use optional shorthand, got %T", maybeBind.Term)
	}
	if tokenKind, ok := maybeOptional.Term.(*ast.GrammarTokenKindTerm); !ok || tokenKind.Kind != "IDENT" {
		t.Fatalf("expected optional shorthand inner term to be .IDENT, got %T %#v", maybeOptional.Term, maybeOptional.Term)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"many = repeat(.IDENT, until(\")\", token(TokenKind.EOF)))",
		"maybe = .IDENT?",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarListFamilyReadableSugar(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    block(state: mutable ParserState&) -> darray[Pascal.Stmt]:
        statements = list statement() separated by .SEMICOLON until(.END, token(TokenKind.EOF))
        declarations = flatrepeat variable_decl_group() until(.BEGIN, token(TokenKind.EOF))
        values = separated expression() by .COMMA until(.RPAREN, token(TokenKind.EOF))
        maybe = optional .IDENT
        return statements
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.Productions) != 1 || len(decl.Productions[0].Terms) != 5 {
		t.Fatalf("expected one production with five terms, got %#v", decl.Productions)
	}
	stmtsBind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first term to be binding, got %T", decl.Productions[0].Terms[0])
	}
	stmtsList, ok := stmtsBind.Term.(*ast.GrammarListTerm)
	if !ok {
		t.Fatalf("expected readable list sugar to produce GrammarListTerm, got %T", stmtsBind.Term)
	}
	if stmtsList.Separator == nil || len(stmtsList.Until) != 2 {
		t.Fatalf("expected readable list sugar to preserve separator and stops, got %#v", stmtsList)
	}
	declsBind, ok := decl.Productions[0].Terms[1].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected second term to be binding, got %T", decl.Productions[0].Terms[1])
	}
	if _, ok := declsBind.Term.(*ast.GrammarFlatRepeatTerm); !ok {
		t.Fatalf("expected readable flatrepeat sugar to produce GrammarFlatRepeatTerm, got %T", declsBind.Term)
	}
	valuesBind, ok := decl.Productions[0].Terms[2].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected third term to be binding, got %T", decl.Productions[0].Terms[2])
	}
	valuesSeparated, ok := valuesBind.Term.(*ast.GrammarSeparatedTerm)
	if !ok {
		t.Fatalf("expected readable separated sugar to produce GrammarSeparatedTerm, got %T", valuesBind.Term)
	}
	if valuesSeparated.Separator == nil || len(valuesSeparated.Until) != 2 {
		t.Fatalf("expected readable separated sugar to preserve separator and stops, got %#v", valuesSeparated)
	}
	maybeBind, ok := decl.Productions[0].Terms[3].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected fourth term to be binding, got %T", decl.Productions[0].Terms[3])
	}
	if _, ok := maybeBind.Term.(*ast.GrammarOptionalTerm); !ok {
		t.Fatalf("expected readable optional sugar to produce GrammarOptionalTerm, got %T", maybeBind.Term)
	}
}

func TestParseGrammarDeclAllowsBracketWhileTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    block(state: mutable ParserState&) -> darray[Pascal.Stmt]:
        declarations = [variable_decl_group()] while token in tokens != [.BEGIN, token(TokenKind.EOF)]
        return declarations
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
		t.Fatalf("expected binding term, got %T", decl.Productions[0].Terms[0])
	}
	whileTerm, ok := bind.Term.(*ast.GrammarWhileTerm)
	if !ok {
		t.Fatalf("expected GrammarWhileTerm, got %T", bind.Term)
	}
	if len(whileTerm.Until) != 2 {
		t.Fatalf("expected two stop terms, got %#v", whileTerm.Until)
	}
	if _, ok := whileTerm.Until[0].(*ast.GrammarTokenKindTerm); !ok {
		t.Fatalf("expected first stop term to preserve token kind term, got %T", whileTerm.Until[0])
	}
	if _, ok := whileTerm.Until[1].(*ast.GrammarCallTerm); !ok {
		t.Fatalf("expected second stop term to preserve token call, got %T", whileTerm.Until[1])
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "[variable_decl_group()] while token in tokens != [.BEGIN, token(TokenKind.EOF)]") {
		t.Fatalf("expected formatted output to preserve bracket while term, got:\n%s", formatted)
	}
}

func TestParseGrammarSeqBlockAndPrefixSugar(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    expression() -> Expr:
        unary = prefix(.PLUS, .MINUS, .NOT) atom() -> build_unary(op, operand)
        grouped = seq:
            open = .LPAREN
            value = expression()
            close = .RPAREN
            expr(value)
        compact = seq(op = choice(.PLUS, .MINUS) operand = atom() expr(build_unary(op, operand)))
        return unary
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
	unaryBind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first term to bind prefix result, got %T", decl.Productions[0].Terms[0])
	}
	unarySeq, ok := unaryBind.Term.(*ast.GrammarSeqTerm)
	if !ok || len(unarySeq.Terms) != 3 {
		t.Fatalf("expected prefix sugar to desugar to three-term seq, got %#v", unaryBind.Term)
	}
	if opBind, ok := unarySeq.Terms[0].(*ast.GrammarBindTerm); !ok || opBind.Name != "op" {
		t.Fatalf("expected prefix sugar to bind op, got %#v", unarySeq.Terms[0])
	}
	if operandBind, ok := unarySeq.Terms[1].(*ast.GrammarBindTerm); !ok || operandBind.Name != "operand" {
		t.Fatalf("expected prefix sugar to bind operand, got %#v", unarySeq.Terms[1])
	}
	groupedBind, ok := decl.Productions[0].Terms[1].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected second term to bind grouped seq, got %T", decl.Productions[0].Terms[1])
	}
	groupedSeq, ok := groupedBind.Term.(*ast.GrammarSeqTerm)
	if !ok || len(groupedSeq.Terms) != 4 {
		t.Fatalf("expected seq block to parse four terms, got %#v", groupedBind.Term)
	}
	compactBind, ok := decl.Productions[0].Terms[2].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected third term to bind compact seq, got %T", decl.Productions[0].Terms[2])
	}
	compactSeq, ok := compactBind.Term.(*ast.GrammarSeqTerm)
	if !ok || len(compactSeq.Terms) != 3 {
		t.Fatalf("expected comma-free seq(...) to parse three terms, got %#v", compactBind.Term)
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

func TestParseGrammarDeclAllowsAssociativeInlinePrecedenceTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Pascal.Expr:
        precedence left(left = state.term()):
			op = choice("+", "-") -> build_binary(op, left, right)
        return left
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.GrammarDecl)
	precedence := decl.Productions[0].Terms[0].(*ast.GrammarPrecedenceTerm)
	if precedence.Assoc != ast.GrammarAssociativityLeft {
		t.Fatalf("expected inline precedence to be left-associative, got %q", precedence.Assoc)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"precedence left(left = state.term()):",
		"op = choice(\"+\", \"-\") -> build_binary(op, left, right)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclRejectsAssociativityOnHelperLevel(t *testing.T) {
	_, errs := parseSourceFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Pascal.Expr:
		result = precedence(additive):
			left atom = state.factor()
			left additive(left = atom()):
				"+" -> build_add(left, right)
		return result
`)
	if len(errs) == 0 {
		t.Fatal("expected parser error for associativity on helper level")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "precedence associativity requires a looping level") {
		t.Fatalf("expected helper-level associativity error, got %v", errs)
	}
}

func TestParseGrammarDeclRejectsAssociativityOnNamedPrecedenceHeader(t *testing.T) {
	_, errs := parseSourceFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Pascal.Expr:
		result = precedence right(additive):
			atom = state.factor()
			left additive(left = atom()):
				"+" -> build_add(left, right)
		return result
`)
	if len(errs) == 0 {
		t.Fatal("expected parser error for associativity on named precedence header")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "precedence associativity requires inline looping precedence") {
		t.Fatalf("expected named-header associativity error, got %v", errs)
	}
}

func TestParseGrammarDeclAllowsInfixTableDeclAndUse(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    infix table ExprTable(additive):
        atom = state.factor()
		left multiplicative(left = atom()):
			op = choice("*", "/") -> build_binary(op, left, right)
		left additive(left = multiplicative()):
			op = choice("+", "-") -> build_binary(op, left, right)
    expression(state: mutable ParserState&) -> Pascal.Expr:
        result = infix(ExprTable)
        return result
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.InfixTables) != 1 {
		t.Fatalf("expected one infix table, got %d", len(decl.InfixTables))
	}
	table := decl.InfixTables[0]
	if table.Name != "ExprTable" {
		t.Fatalf("expected infix table name ExprTable, got %q", table.Name)
	}
	if table.Result != "additive" {
		t.Fatalf("expected infix table result additive, got %q", table.Result)
	}
	if len(table.Levels) != 3 {
		t.Fatalf("expected three infix table levels, got %d", len(table.Levels))
	}
	if table.Levels[1].Assoc != ast.GrammarAssociativityLeft {
		t.Fatalf("expected multiplicative level to be left-associative, got %q", table.Levels[1].Assoc)
	}
	bind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first production term to be binding, got %T", decl.Productions[0].Terms[0])
	}
	infixTerm, ok := bind.Term.(*ast.GrammarInfixTableTerm)
	if !ok {
		t.Fatalf("expected bound term to be infix table use, got %T", bind.Term)
	}
	if infixTerm.TableName != "ExprTable" {
		t.Fatalf("expected infix term to reference ExprTable, got %q", infixTerm.TableName)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"infix table ExprTable(additive):",
		"atom = state.factor()",
		"left multiplicative(left = atom()):",
		"op = choice(\"*\", \"/\") -> build_binary(op, left, right)",
		"left additive(left = multiplicative()):",
		"op = choice(\"+\", \"-\") -> build_binary(op, left, right)",
		"result = infix(ExprTable)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsAssociativityAnnotatedInfixTableLevels(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    infix table ExprTable(compare):
        atom = state.factor()
        right power(left = atom()):
            "^" -> build_power(left, right)
        nonassoc compare(left = power()):
            op = choice("<", ">") -> build_compare(op, left, right)
    expression(state: mutable ParserState&) -> Pascal.Expr:
        result = infix(ExprTable)
        return result
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.GrammarDecl)
	table := decl.InfixTables[0]
	if table.Levels[1].Assoc != ast.GrammarAssociativityRight {
		t.Fatalf("expected power level to be right-associative, got %q", table.Levels[1].Assoc)
	}
	if table.Levels[2].Assoc != ast.GrammarAssociativityNonAssoc {
		t.Fatalf("expected compare level to be non-associative, got %q", table.Levels[2].Assoc)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"right power(left = atom()):",
		"\"^\" -> build_power(left, right)",
		"nonassoc compare(left = power()):",
		"op = choice(\"<\", \">\") -> build_compare(op, left, right)",
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

func TestParseGrammarDeclAllowsBlockPrecedenceArm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Pascal.Expr:
        precedence(left = state.term()):
            op = choice("+", "-"):
                right = state.term()
                -> build_binary(op, left, right)
        return left
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.GrammarDecl)
	precedence := decl.Productions[0].Terms[0].(*ast.GrammarPrecedenceTerm)
	if len(precedence.Arms) != 1 {
		t.Fatalf("expected one precedence arm, got %d", len(precedence.Arms))
	}
	arm := precedence.Arms[0]
	if !arm.Block {
		t.Fatal("expected precedence arm block flag to be preserved")
	}
	if arm.OpName != "op" {
		t.Fatalf("expected precedence arm operator binding name op, got %q", arm.OpName)
	}
	if len(arm.Bindings) != 1 || arm.Bindings[0].Name != "right" {
		t.Fatalf("expected precedence arm to preserve right binding, got %#v", arm.Bindings)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"precedence(left = state.term()):",
		"op = choice(\"+\", \"-\"):",
		"right = state.term()",
		"-> build_binary(op, left, right)",
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

func TestParseGrammarDeclAllowsSuffixTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    condition(state: mutable ParserState&) -> Pascal.Expr:
        node = suffix(left = state.expression()):
			op = choice("=", "<>", "<=", "<", ">=", ">") right = state.expression() -> build_condition(left, op, right)
        return node
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
		t.Fatalf("expected first term to be suffix binding, got %T", decl.Productions[0].Terms[0])
	}
	suffix, ok := bind.Term.(*ast.GrammarSuffixTerm)
	if !ok {
		t.Fatalf("expected bound term to be suffix, got %T", bind.Term)
	}
	if suffix.LeftName != "left" {
		t.Fatalf("expected suffix left name left, got %q", suffix.LeftName)
	}
	if len(suffix.Arms) != 1 {
		t.Fatalf("expected one suffix arm, got %d", len(suffix.Arms))
	}
	if suffix.Arms[0].OpName != "op" {
		t.Fatalf("expected suffix arm to bind op, got %q", suffix.Arms[0].OpName)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"node = suffix(left = state.expression()):",
		"op = choice(\"=\", \"<>\", \"<=\", \"<\", \">=\", \">\") right = state.expression() -> build_condition(left, op, right)",
		"return node",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsSuffixTermWithPipeTokenKinds(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend over Token using ParserState:
    condition() -> Pascal.Expr:
        node = suffix(left = expression()):
            op = .EQ | .NOTEQ | .LTEQ | .LT | .GTEQ | .GT right = expression() -> build_condition(left, op, right)
        return node
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
		t.Fatalf("expected first term to be suffix binding, got %T", decl.Productions[0].Terms[0])
	}
	suffix, ok := bind.Term.(*ast.GrammarSuffixTerm)
	if !ok {
		t.Fatalf("expected bound term to be suffix, got %T", bind.Term)
	}
	if len(suffix.Arms) != 1 {
		t.Fatalf("expected one suffix arm, got %d", len(suffix.Arms))
	}
	choice, ok := suffix.Arms[0].Op.(*ast.GrammarChoiceTerm)
	if !ok {
		t.Fatalf("expected suffix arm operator to be a choice term, got %T", suffix.Arms[0].Op)
	}
	if len(choice.Options) != 6 {
		t.Fatalf("expected six relational operator choices, got %d", len(choice.Options))
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "op = choice(.EQ, .NOTEQ, .LTEQ, .LT, .GTEQ, .GT) right = expression() -> build_condition(left, op, right)") {
		t.Fatalf("expected formatted output to contain canonical suffix-arm choice form, got:\n%s", formatted)
	}
}

func TestParseGrammarDeclAllowsBlockPostfixArm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PerlFrontend:
    member_expr() -> Perl.Expr:
        node = postfix(left = primary_expr()):
            .ARROW:
                member = member_tail()
                -> build_member(left, member.name_token, member.close_token)
        return node
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
	if len(postfix.Arms) != 1 {
		t.Fatalf("expected one postfix arm, got %d", len(postfix.Arms))
	}
	arm := postfix.Arms[0]
	if !arm.Block {
		t.Fatal("expected postfix arm block flag to be preserved")
	}
	if len(arm.Bindings) != 1 || arm.Bindings[0].Name != "member" {
		t.Fatalf("expected block postfix arm to preserve member binding, got %#v", arm.Bindings)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"node = postfix(left = primary_expr()):",
		".ARROW:",
		"member = member_tail()",
		"-> build_member(left, member.name_token, member.close_token)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsFlatRepeatTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    groups(state: mutable ParserState&) -> darray[Token]:
        values = flatrepeat(state.group(), until("end", token(TokenKind.EOF)))
        return values
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
		t.Fatalf("expected first term to be flatrepeat binding, got %T", decl.Productions[0].Terms[0])
	}
	flat, ok := bind.Term.(*ast.GrammarFlatRepeatTerm)
	if !ok {
		t.Fatalf("expected bound term to be flatrepeat, got %T", bind.Term)
	}
	if len(flat.Until) != 2 {
		t.Fatalf("expected two until stop terms, got %d", len(flat.Until))
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"values = flatrepeat(state.group(), until(\"end\", token(TokenKind.EOF)))",
		"return values",
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

func TestParseGrammarDeclAllowsTypedExprTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    param_name_ids(state: mutable ParserState&) -> darray[NameId]:
        ids = separated required(seq(.IDENT(param_token), expr[NameId](param_token.lexeme_key)), ParseMessageKey.ExpectedProgramParamName) by .COMMA until(.RPAREN, token(TokenKind.EOF))
        return ids
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
	separated, ok := firstBind.Term.(*ast.GrammarSeparatedTerm)
	if !ok {
		t.Fatalf("expected bound term to be separated term, got %T", firstBind.Term)
	}
	required, ok := separated.Elem.(*ast.GrammarRequiredTerm)
	if !ok {
		t.Fatalf("expected separated elem to be required term, got %T", separated.Elem)
	}
	seq, ok := required.Term.(*ast.GrammarSeqTerm)
	if !ok || len(seq.Terms) != 2 {
		t.Fatalf("expected required term to wrap seq with two terms, got %#v", required.Term)
	}
	exprTerm, ok := seq.Terms[1].(*ast.GrammarExprTerm)
	if !ok {
		t.Fatalf("expected second seq term to be typed expr, got %T", seq.Terms[1])
	}
	if got := formatTypeExprForTest(t, exprTerm.Type); got != "NameId" {
		t.Fatalf("expected typed expr term to preserve result type, got %q", got)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"expr[NameId](param_token.lexeme_key)",
		"return ids",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsBareListComprehensionExprTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
	    variable_decls(names: darray[Token], type_token: Token) -> darray[Pascal.Decl]:
	        decls = [build_var_decl(name_token, type_token) for name_token in names if name_token.kind == TokenKind.IDENT]
	        return decls
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
		t.Fatalf("expected first term to be grammar bind, got %T", decl.Productions[0].Terms[0])
	}
	exprTerm, ok := bind.Term.(*ast.GrammarExprTerm)
	if !ok {
		t.Fatalf("expected bound term to be grammar expr term, got %T", bind.Term)
	}
	comp, ok := exprTerm.Expr.(*ast.ListComprehensionExpr)
	if !ok {
		t.Fatalf("expected grammar expr term to carry list comprehension, got %T", exprTerm.Expr)
	}
	if comp.Name != "name_token" {
		t.Fatalf("expected comprehension binder name_token, got %q", comp.Name)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"decls = [build_var_decl(name_token, type_token) for name_token in names if (name_token.kind == TokenKind.IDENT)]",
		"return decls",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclRejectsFlatMapListTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    variable_decls(names: darray[Token], type_token: Token) -> darray[Pascal.Decl]:
        decls = flatmaplist[Pascal.Decl](names, name_token, [build_var_decl(name_token, type_token)] if name_token.kind == TokenKind.IDENT else [])
        return decls
`)
	if file == nil {
		t.Fatal("expected parser to return a file even on error")
	}
	if len(errs) == 0 {
		t.Fatal("expected flatmaplist grammar syntax to be rejected")
	}
}

func TestParseGrammarDeclRejectsMapListTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    param_names(names: darray[Token]) -> darray[NameId]:
        ids = maplist[NameId](names, name_token, name_token.lexeme_key)
        return ids
`)
	if file == nil {
		t.Fatal("expected parser to return a file even on error")
	}
	if len(errs) == 0 {
		t.Fatal("expected maplist grammar syntax to be rejected")
	}
}

func TestParseGrammarDeclAllowsSingletonTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    const_decls(decl: Pascal.Decl) -> darray[Pascal.Decl]:
        decls = singleton[Pascal.Decl](decl)
        return decls
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
		t.Fatalf("expected first term to be singleton binding, got %T", decl.Productions[0].Terms[0])
	}
	singleton, ok := bind.Term.(*ast.GrammarSingletonTerm)
	if !ok {
		t.Fatalf("expected bound term to be singleton, got %T", bind.Term)
	}
	if got := formatTypeExprForTest(t, singleton.Type); got != "Pascal.Decl" {
		t.Fatalf("expected singleton term to preserve element type, got %q", got)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"singleton[Pascal.Decl](decl)",
		"return decls",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsGrammarTypeAndProductionChannels(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PerlExprGrammar:
    grammar type recovered[T](item: grammar -> T, message: expr, stop: tokenset, fallback: expr) -> grammar -> T:
        item recover(message, until(stop), fallback)
    member_tail() -> PerlMemberTail:
        channel name_token
        channel close_token
        name_token <- required(.IDENT, ExpectedMemberName)
        close_token <- expr(name_token)
        pass
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.GrammarFns) != 1 || !decl.GrammarFns[0].TypeCtor {
		t.Fatalf("expected grammar type constructor, got %#v", decl.GrammarFns)
	}
	if got := len(decl.Productions[0].Channels); got != 2 {
		t.Fatalf("expected two production-local channels, got %d", got)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"grammar type recovered[T](item: grammar -> T, message: expr, stop: tokenset, fallback: expr) -> grammar -> T:",
		"channel name_token",
		"channel close_token",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseProtocolAliasForStaticInterface(t *testing.T) {
	file, errs := parseSourceFile(t, `protocol SpanLike:
    type Range
    def combine(left: Range, right: Range) -> Range
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.InterfaceDecl)
	if !ok {
		t.Fatalf("expected interface decl, got %T", file.Decls[0])
	}
	if !decl.Protocol {
		t.Fatalf("expected protocol surface to be preserved")
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "protocol SpanLike:") {
		t.Fatalf("expected protocol unparse, got:\n%s", formatted)
	}
}

func TestParseGrammarDeclAllowsConcatTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    declarations() -> darray[Pascal.Decl]:
        decls = const_prefixed_decl_sections() + type_prefixed_decl_sections() + empty[Pascal.Decl]
        return decls
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
		t.Fatalf("expected first term to be concat binding, got %T", decl.Productions[0].Terms[0])
	}
	concat, ok := bind.Term.(*ast.GrammarConcatTerm)
	if !ok {
		t.Fatalf("expected bound term to be concat term, got %T", bind.Term)
	}
	if len(concat.Terms) != 3 {
		t.Fatalf("expected concat term to preserve three operands, got %d", len(concat.Terms))
	}
	if _, ok := concat.Terms[2].(*ast.GrammarEmptyTerm); !ok {
		t.Fatalf("expected final concat operand to be empty term, got %T", concat.Terms[2])
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"const_prefixed_decl_sections() + type_prefixed_decl_sections() + empty[Pascal.Decl]",
		"return decls",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsWhenTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    body(tok: Token, state: mutable ParserState&) -> Pascal.Stmt:
        node = when(tok.kind == TokenKind.THEN, state.statement_core() recover(ParseMessageKey.ExpectedStatement, until(";", token(TokenKind.EOF)), zeroed as Pascal.Stmt), when(is_statement_start(state.current_token().kind), state.statement_core(), expr(zeroed as Pascal.Stmt)))
        return node
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
	whenTerm, ok := bind.Term.(*ast.GrammarWhenTerm)
	if !ok {
		t.Fatalf("expected bound term to be when, got %T", bind.Term)
	}
	if _, ok := whenTerm.Then.(*ast.GrammarRecoverTerm); !ok {
		t.Fatalf("expected then branch to be recovered term, got %T", whenTerm.Then)
	}
	if _, ok := whenTerm.Else.(*ast.GrammarWhenTerm); !ok {
		t.Fatalf("expected else branch to be nested when, got %T", whenTerm.Else)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"node = when((tok.kind == TokenKind.THEN), state.statement_core() recover(ParseMessageKey.ExpectedStatement, until(\";\", token(TokenKind.EOF)), zeroed as Pascal.Stmt), when(is_statement_start(state.current_token().kind), state.statement_core(), expr(zeroed as Pascal.Stmt)))",
		"return node",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsMatchTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    type_ref(state: mutable ParserState&) -> PascalType.Type:
        type_expr = match state.current_token().kind:
            TokenKind.CARET: pointer_type_ref()
            TokenKind.PACKED | TokenKind.SET: compact_or_set_type_ref()
            _: range_type_ref()
        return type_expr
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
	matchTerm, ok := bind.Term.(*ast.GrammarMatchTerm)
	if !ok {
		t.Fatalf("expected bound term to be match, got %T", bind.Term)
	}
	if len(matchTerm.Arms) != 3 {
		t.Fatalf("expected three match arms, got %d", len(matchTerm.Arms))
	}
	if len(matchTerm.Arms[1].Patterns) != 2 {
		t.Fatalf("expected second arm to contain two dispatch patterns, got %d", len(matchTerm.Arms[1].Patterns))
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"type_expr = match state.current_token().kind:",
		"TokenKind.PACKED | TokenKind.SET: compact_or_set_type_ref()",
		"_: range_type_ref()",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsRequiredTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    ident(state: mutable ParserState&) -> Token:
        name = required(.IDENT, ParseMessageKey.ExpectedProgramName)
        required(")", ParseMessageKey.ExpectedRightParen)
        return name
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
		t.Fatalf("expected first term to be required binding, got %T", decl.Productions[0].Terms[0])
	}
	required, ok := bind.Term.(*ast.GrammarRequiredTerm)
	if !ok {
		t.Fatalf("expected bound term to be required, got %T", bind.Term)
	}
	if _, ok := required.Term.(*ast.GrammarTokenKindTerm); !ok {
		t.Fatalf("expected required inner term to be token kind, got %T", required.Term)
	}
	if _, ok := decl.Productions[0].Terms[1].(*ast.GrammarRequiredTerm); !ok {
		t.Fatalf("expected second term to be required, got %T", decl.Productions[0].Terms[1])
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"name = required(.IDENT, ParseMessageKey.ExpectedProgramName)",
		"required(\")\", ParseMessageKey.ExpectedRightParen)",
		"return name",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsDelimitedTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    atom(state: mutable ParserState&) -> Token:
        value = delimited("(", .IDENT, ")", ParseMessageKey.ExpectedRightParen)
        return value
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
		t.Fatalf("expected first term to be delimited binding, got %T", decl.Productions[0].Terms[0])
	}
	delimited, ok := bind.Term.(*ast.GrammarDelimitedTerm)
	if !ok {
		t.Fatalf("expected bound term to be delimited, got %T", bind.Term)
	}
	if _, ok := delimited.Open.(*ast.GrammarTokenTerm); !ok {
		t.Fatalf("expected delimited open term to be token, got %T", delimited.Open)
	}
	if _, ok := delimited.Body.(*ast.GrammarTokenKindTerm); !ok {
		t.Fatalf("expected delimited body term to be token kind, got %T", delimited.Body)
	}
	if _, ok := delimited.Close.(*ast.GrammarTokenTerm); !ok {
		t.Fatalf("expected delimited close term to be token, got %T", delimited.Close)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"value = delimited(\"(\", .IDENT, \")\", ParseMessageKey.ExpectedRightParen)",
		"return value",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsSeqTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    else_clause(state: mutable ParserState&) -> Token:
        value = seq("else", .IDENT recover(ParseMessageKey.ExpectedProgramName, until(";", token(TokenKind.EOF)), expr(state.current_token())))
        return value
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
		t.Fatalf("expected first term to be seq binding, got %T", decl.Productions[0].Terms[0])
	}
	seq, ok := bind.Term.(*ast.GrammarSeqTerm)
	if !ok {
		t.Fatalf("expected bound term to be seq, got %T", bind.Term)
	}
	if len(seq.Terms) != 2 {
		t.Fatalf("expected seq to contain 2 terms, got %d", len(seq.Terms))
	}
	if _, ok := seq.Terms[0].(*ast.GrammarTokenTerm); !ok {
		t.Fatalf("expected seq open term to be token, got %T", seq.Terms[0])
	}
	if _, ok := seq.Terms[1].(*ast.GrammarRecoverTerm); !ok {
		t.Fatalf("expected seq second term to be recover term, got %T", seq.Terms[1])
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"value = seq:",
		"\"else\"",
		".IDENT recover(ParseMessageKey.ExpectedProgramName, until(\";\", token(TokenKind.EOF)), expr(state.current_token()))",
		"return value",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsSeqBindingsAndAssignments(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    atom(state: mutable ParserState&) -> Token:
        value = seq(.IDENT(token), span <- expr(token.span), expr(token))
        return value
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
		t.Fatalf("expected first term to be seq binding, got %T", decl.Productions[0].Terms[0])
	}
	seq, ok := bind.Term.(*ast.GrammarSeqTerm)
	if !ok {
		t.Fatalf("expected bound term to be seq, got %T", bind.Term)
	}
	if _, ok := seq.Terms[0].(*ast.GrammarBindTerm); !ok {
		t.Fatalf("expected seq first term to be bind term, got %T", seq.Terms[0])
	}
	if _, ok := seq.Terms[1].(*ast.GrammarAssignTerm); !ok {
		t.Fatalf("expected seq second term to be assign term, got %T", seq.Terms[1])
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"value = seq:",
		".IDENT(token)",
		"span <- expr(token.span)",
		"expr(token)",
		"return value",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarDeclAllowsLookaheadTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
	atom(state: mutable ParserState&) -> Token:
		lookahead(choice(":=", "."))
		.IDENT(token)
		value = expr(token)
		return token
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	lookahead, ok := decl.Productions[0].Terms[0].(*ast.GrammarLookaheadTerm)
	if !ok {
		t.Fatalf("expected first term to be lookahead, got %T", decl.Productions[0].Terms[0])
	}
	if _, ok := lookahead.Term.(*ast.GrammarChoiceTerm); !ok {
		t.Fatalf("expected lookahead inner term to be choice, got %T", lookahead.Term)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"lookahead(choice(\":=\", \".\"))",
		".IDENT(token)",
		"value = expr(token)",
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
			left multiplicative(term_left = atom()):
				op = choice("*", "/") -> build_binary(op, term_left, right)
			left additive(expr_left = multiplicative()):
				op = choice("+", "-") -> build_binary(op, expr_left, right)
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
	if precedence.Levels[1].Assoc != ast.GrammarAssociativityLeft {
		t.Fatalf("expected multiplicative level to be left-associative, got %q", precedence.Levels[1].Assoc)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"result = precedence(additive):",
		"atom = state.factor()",
		"precedence(additive):",
		"left multiplicative(term_left = atom()):",
		"op = choice(\"*\", \"/\") -> build_binary(op, term_left, right)",
		"left additive(expr_left = multiplicative()):",
		"op = choice(\"+\", \"-\") -> build_binary(op, expr_left, right)",
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

func TestParseGrammarProductionBodyRejectsNonGrammarStatements(t *testing.T) {
	_, errs := parseSourceFile(t, `grammar SMLFrontend:
    token:
        REC "rec"
        WHILE "while"
    val_decl() -> SMLDecl:
        is_recursive: mutable bool = false
        if is_recursive:
            .REC
        .IDENT
`)
	for _, want := range []string{
		"grammar production body cannot contain general statements (found \"mutable\")",
		"grammar production body cannot contain general statements (found \"if\")",
		"grammar terms should use token matches, bindings, choices",
	} {
		found := false
		for _, err := range errs {
			if strings.Contains(err, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected parser errors to contain %q, got:\n%s", want, strings.Join(errs, "\n"))
		}
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
