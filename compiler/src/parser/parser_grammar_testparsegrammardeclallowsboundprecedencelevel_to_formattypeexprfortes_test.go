package parser

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

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
        return zeroed.cast[Pascal.Stmt]
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
