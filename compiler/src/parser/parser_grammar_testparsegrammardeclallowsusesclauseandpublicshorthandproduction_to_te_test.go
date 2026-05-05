package parser

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

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
