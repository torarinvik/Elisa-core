package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

func TestParseLexerDeclAllowsGrammarTokenImport(t *testing.T) {
	file, errs := parseSourceFile(t, `lexer PascalLex:
    token_kind PascalTokenKind
    tokens Pascal.Frontend
	keyword_compare pascal_sview_eq_keyword
    keywords fallback IDENT:
        "unit" -> UNIT
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.LexerDecl)
	if !ok {
		t.Fatalf("expected lexer decl, got %T", file.Decls[0])
	}
	if decl.GrammarName != "Pascal.Frontend" {
		t.Fatalf("expected grammar token import Pascal.Frontend, got %q", decl.GrammarName)
	}
	if decl.KeywordCompareFunc != "pascal_sview_eq_keyword" {
		t.Fatalf("expected lexer keyword compare pascal_sview_eq_keyword, got %q", decl.KeywordCompareFunc)
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "tokens Pascal.Frontend") {
		t.Fatalf("expected formatted lexer to preserve token import, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "keyword_compare pascal_sview_eq_keyword") {
		t.Fatalf("expected formatted lexer to preserve keyword compare, got:\n%s", formatted)
	}
}
