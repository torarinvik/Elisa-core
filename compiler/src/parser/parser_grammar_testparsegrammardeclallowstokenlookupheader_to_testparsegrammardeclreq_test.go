package parser

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

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
        return zeroed.cast[Pascal.Stmt]
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
        return zeroed.cast[Pascal.Stmt]
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
        return zeroed.cast[Pascal.Stmt]
    block() -> Pascal.Stmt:
        lookahead(first(statement))
        return zeroed.cast[Pascal.Stmt]
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
