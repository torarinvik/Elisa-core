package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

func TestParseKeywordMapDecl(t *testing.T) {
	file, errs := parseSourceFile(t, `keywordmap lua_keyword: sview -> LuaTokenKind:
    "and" => .AND
    "break" => .BREAK
    _ => .NAME
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.KeywordMapDecl)
	if !ok {
		t.Fatalf("expected keywordmap decl, got %T", file.Decls[0])
	}
	if decl.Name != "lua_keyword" || len(decl.Entries) != 2 {
		t.Fatalf("expected lua_keyword with two entries, got %#v", decl)
	}
	if decl.Entries[0].Text != "and" {
		t.Fatalf("expected first keyword text to be and, got %#v", decl.Entries[0])
	}
	if formatted := unparse.FormatFile(file); !strings.Contains(formatted, `keywordmap lua_keyword: sview -> LuaTokenKind:`) || !strings.Contains(formatted, `"break" => .BREAK`) || !strings.Contains(formatted, `_ => .NAME`) {
		t.Fatalf("expected keywordmap to unparse, got:\n%s", formatted)
	}
}

func TestParseKeywordMapRejectsDuplicateKeyword(t *testing.T) {
	_, errs := parseSourceFile(t, `keywordmap lua_keyword: sview -> LuaTokenKind:
    "and" => .AND
    "and" => .ALSO
    _ => .NAME
`)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), `duplicate keywordmap entry "and"`) {
		t.Fatalf("expected duplicate keywordmap entry diagnostic, got %v", errs)
	}
}
