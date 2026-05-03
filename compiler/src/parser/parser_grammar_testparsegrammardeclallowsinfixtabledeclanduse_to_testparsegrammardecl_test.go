package parser

import (
	"llcontext/src/ast"
	"llcontext/src/unparse"
	"strings"
	"testing"
)

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
