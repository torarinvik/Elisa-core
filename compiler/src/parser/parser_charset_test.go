package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

func TestParseCharsetDeclAllowsAsciiLiteralsAndRanges(t *testing.T) {
	file, errs := parseSourceFile(t, "charset IdentStart = 'a'..'z' | 'A'..'Z' | '_'\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.CharsetDecl)
	if !ok {
		t.Fatalf("expected charset decl, got %T", file.Decls[0])
	}
	if decl.Name != "IdentStart" || len(decl.Terms) != 3 {
		t.Fatalf("expected IdentStart with three terms, got %#v", decl)
	}
	if !decl.Terms[0].Range || decl.Terms[0].Start != "a" || decl.Terms[0].End != "z" {
		t.Fatalf("expected first term to be a range, got %#v", decl.Terms[0])
	}
	if formatted := unparse.FormatFile(file); !strings.Contains(formatted, "charset IdentStart = 'a'..'z' | 'A'..'Z' | '_'") {
		t.Fatalf("expected charset to unparse, got:\n%s", formatted)
	}
}

func TestParseCharsetDeclKeepsReferencesForSemanticValidation(t *testing.T) {
	file, errs := parseSourceFile(t, "charset IdentContinue = IdentStart | '0'..'9'\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.CharsetDecl)
	if !ok {
		t.Fatalf("expected charset decl, got %T", file.Decls[0])
	}
	if len(decl.Terms) != 2 || !decl.Terms[0].Ref || decl.Terms[0].Name != "IdentStart" {
		t.Fatalf("expected first term to preserve charset reference, got %#v", decl.Terms)
	}
}
