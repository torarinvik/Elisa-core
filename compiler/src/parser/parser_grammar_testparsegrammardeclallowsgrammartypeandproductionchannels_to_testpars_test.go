package parser

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

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
func TestParseProtocolDecl(t *testing.T) {
	file, errs := parseSourceFile(t, `protocol SpanLike:
    type Range
    def combine(left: Range, right: Range) -> Range
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	_, ok := file.Decls[0].(*ast.InterfaceDecl)
	if !ok {
		t.Fatalf("expected interface decl, got %T", file.Decls[0])
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
        node = when(tok.kind == TokenKind.THEN, state.statement_core() recover(ParseMessageKey.ExpectedStatement, until(";", token(TokenKind.EOF)), zeroed.cast[Pascal.Stmt]), when(is_statement_start(state.current_token().kind), state.statement_core(), expr(zeroed.cast[Pascal.Stmt])))
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
		"node = when((tok.kind == TokenKind.THEN), state.statement_core() recover(ParseMessageKey.ExpectedStatement, until(\";\", token(TokenKind.EOF)), zeroed.cast[Pascal.Stmt]), when(is_statement_start(state.current_token().kind), state.statement_core(), expr(zeroed.cast[Pascal.Stmt])))",
		"return node",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestParseGrammarDeclAllowsOptionalTokenGateTerm(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    unit_body() -> darray[Pascal.Decl]:
        interface_uses = .USES? then uses_clause() else []
        initialization_block = .INITIALIZATION? then unit_initialization_block_optional()
        return interface_uses
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	firstBind := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	firstGate, ok := firstBind.Term.(*ast.GrammarWhenTerm)
	if !ok || firstGate.TokenKindGate != "USES" {
		t.Fatalf("expected USES optional token gate, got %T %#v", firstBind.Term, firstBind.Term)
	}
	if _, ok := firstGate.Else.(*ast.GrammarEmptyTerm); !ok {
		t.Fatalf("expected explicit [] else branch as empty grammar term, got %T", firstGate.Else)
	}
	secondBind := decl.Productions[0].Terms[1].(*ast.GrammarBindTerm)
	secondGate, ok := secondBind.Term.(*ast.GrammarWhenTerm)
	if !ok || secondGate.TokenKindGate != "INITIALIZATION" {
		t.Fatalf("expected INITIALIZATION optional token gate, got %T %#v", secondBind.Term, secondBind.Term)
	}
	if !grammarWhenElseIsImplicitNullForTest(secondGate.Else) {
		t.Fatalf("expected omitted else branch to become implicit null expr, got %T %#v", secondGate.Else, secondGate.Else)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"interface_uses = .USES? then uses_clause() else []",
		"initialization_block = .INITIALIZATION? then unit_initialization_block_optional()",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}
func grammarWhenElseIsImplicitNullForTest(term ast.GrammarTerm) bool {
	exprTerm, ok := term.(*ast.GrammarExprTerm)
	if !ok {
		return false
	}
	_, ok = exprTerm.Expr.(*ast.NullLit)
	return ok
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
