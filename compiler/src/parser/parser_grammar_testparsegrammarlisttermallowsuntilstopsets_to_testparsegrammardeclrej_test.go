package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

func TestParseGrammarListTermAllowsUntilStopSets(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    block(state: mutable ParserState&) -> Pascal.Block:
        statements = separated state.statement() by ";" until("end", token(TokenKind.EOF))
    declarations(state: mutable ParserState&) -> darray[Pascal.Decl]:
        items = state.variable_decl()* until("begin")
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
	blockList, ok := blockBind.Term.(*ast.GrammarSeparatedTerm)
	if !ok {
		t.Fatalf("expected block binding to be separated term, got %T", blockBind.Term)
	}
	if blockList.Separator == nil {
		t.Fatal("expected block separated term to preserve separator")
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
	declList, ok := declBind.Term.(*ast.GrammarRepeatTerm)
	if !ok {
		t.Fatalf("expected declarations binding to be repeat term, got %T", declBind.Term)
	}
	if declList.MinOne {
		t.Fatalf("expected declarations repeat (`*`) to be zero-or-more, got MinOne")
	}
	if len(declList.Until) != 1 {
		t.Fatalf("expected one stop term without separator, got %d", len(declList.Until))
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"statements = separated state.statement() by \";\" until(\"end\", token(TokenKind.EOF))",
		"items = repeat(state.variable_decl(), until(\"begin\"))",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestParseGrammarRepeatAndSeparatedTerms(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend:
    block(state: mutable ParserState&) -> darray[Pascal.Stmt]:
        items = state.statement()* until("end", token(TokenKind.EOF))
    args(state: mutable ParserState&) -> darray[Pascal.Expr]:
        values = separated state.expression() by "," until(")", token(TokenKind.EOF))
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
        statements = separated statement() by .SEMICOLON until(.END, token(TokenKind.EOF))
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
	stmtsList, ok := stmtsBind.Term.(*ast.GrammarSeparatedTerm)
	if !ok {
		t.Fatalf("expected readable separated sugar to produce GrammarSeparatedTerm, got %T", stmtsBind.Term)
	}
	if stmtsList.Separator == nil || len(stmtsList.Until) != 2 {
		t.Fatalf("expected readable separated sugar to preserve separator and stops, got %#v", stmtsList)
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
	statement(state: mutable ParserState&) -> Pascal.Stmt recover(ParseMessageKey.ExpectedStatement, until(";", token(TokenKind.EOF)), zeroed.cast[Pascal.Stmt]):
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
	want := "statement(state: mutable ParserState&) -> Pascal.Stmt recover(ParseMessageKey.ExpectedStatement, until(\";\", token(TokenKind.EOF)), zeroed.cast[Pascal.Stmt]):"
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
		stmt = state.statement_core() recover(ParseMessageKey.ExpectedStatement, until(";", token(TokenKind.EOF)), zeroed.cast[Pascal.Stmt])
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
		"stmt = state.statement_core() recover(ParseMessageKey.ExpectedStatement, until(\";\", token(TokenKind.EOF)), zeroed.cast[Pascal.Stmt])",
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

func TestParseGrammarPlusPostfixShorthand(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor state
	items() -> darray[Token]:
		many = .IDENT+ until(")", token(TokenKind.EOF))
		bare = .IDENT+
		return many
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	manyBind, ok := decl.Productions[0].Terms[0].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected first term to be binding, got %T", decl.Productions[0].Terms[0])
	}
	manyRepeat, ok := manyBind.Term.(*ast.GrammarRepeatTerm)
	if !ok {
		t.Fatalf("expected plus shorthand to parse as repeat term, got %T", manyBind.Term)
	}
	if !manyRepeat.MinOne {
		t.Fatalf("expected plus shorthand to set MinOne")
	}
	if len(manyRepeat.Until) != 2 {
		t.Fatalf("expected plus shorthand to preserve until stop set, got %d stops", len(manyRepeat.Until))
	}
	bareBind, ok := decl.Productions[0].Terms[1].(*ast.GrammarBindTerm)
	if !ok {
		t.Fatalf("expected second term to be binding, got %T", decl.Productions[0].Terms[1])
	}
	bareRepeat, ok := bareBind.Term.(*ast.GrammarRepeatTerm)
	if !ok || !bareRepeat.MinOne {
		t.Fatalf("expected bare plus shorthand to be MinOne repeat, got %T", bareBind.Term)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"many = .IDENT+ until(\")\", token(TokenKind.EOF))",
		"bare = .IDENT+",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseGrammarPlusPostfixDoesNotBreakConcat(t *testing.T) {
	file, errs := parseSourceFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor state
	grammar alias both(stop: tokenset):
		first_rule() + flatrepeat second_rule() until(stop)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.GrammarDecl)
	if !ok {
		t.Fatalf("expected grammar decl, got %T", file.Decls[0])
	}
	if len(decl.GrammarAliases) != 1 {
		t.Fatalf("expected one alias, got %d", len(decl.GrammarAliases))
	}
	concat, ok := decl.GrammarAliases[0].Term.(*ast.GrammarConcatTerm)
	if !ok {
		t.Fatalf("expected binary + to stay concat, got %T", decl.GrammarAliases[0].Term)
	}
	if len(concat.Terms) != 2 {
		t.Fatalf("expected concat of two terms, got %d", len(concat.Terms))
	}
}

func TestParseGrammarRemovedRepetitionSpellingsRejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "list call form",
			src: `grammar PascalFrontend over Token using ParserState:
	cursor state
	args(state: mutable ParserState&) -> darray[Pascal.Expr]:
		values = list(state.expression(), until(")"))
`,
		},
		{
			name: "repeat form",
			src: `grammar PascalFrontend over Token using ParserState:
	cursor state
	block(state: mutable ParserState&) -> darray[Pascal.Stmt]:
		items = repeat(state.statement(), until("end", token(TokenKind.EOF)))
`,
		},
		{
			name: "bracket while form",
			src: `grammar PascalFrontend over Token using ParserState:
	cursor state
	block(state: mutable ParserState&) -> darray[Pascal.Stmt]:
		items = [state.statement()] while token in tokens != [".END"]
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := lexer.New("test.elisa", []byte(tc.src))
			tokens := l.Tokenize()
			p := New(tokens)
			p.ParseFile("test.elisa")
			errs := p.Errors()
			if len(errs) == 0 {
				t.Fatalf("expected parse error for removed spelling, got none")
			}
			if !strings.Contains(strings.Join(errs, "\n"), "has been removed") {
				t.Fatalf("expected removal error, got %v", errs)
			}
		})
	}
}
