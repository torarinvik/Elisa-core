package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// A `::`-only path followed by `{` or `(` names a TYPE, so it is a struct pattern, not an
// enum variant path. Before this, `Pack::Item{v}:` in a match parsed as a variant pattern
// with an empty arg list and the `{` fell out as a syntax error.
func TestParseQualifiedStructMatchPattern(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"brace", "module Pack:\n    struct Item:\n        v: int\n\ndef pick(it: Pack::Item) -> int:\n    match it:\n        Pack::Item{v}:\n            return v\n"},
		{"paren", "module Pack:\n    struct Item:\n        v: int\n\ndef pick(it: Pack::Item) -> int:\n    match it:\n        Pack::Item(v: v):\n            return v\n"},
	} {
		file, errs := parseSourceFile(t, tc.src)
		if len(errs) != 0 {
			t.Fatalf("%s: unexpected parse errors: %v", tc.name, errs)
		}
		fn, ok := file.Decls[1].(*ast.FuncDecl)
		if !ok {
			t.Fatalf("%s: second decl is %T, want *ast.FuncDecl", tc.name, file.Decls[1])
		}
		match, ok := fn.Body[0].(*ast.MatchStmt)
		if !ok {
			t.Fatalf("%s: body[0] is %T, want *ast.MatchStmt", tc.name, fn.Body[0])
		}
		structPat, ok := match.Arms[0].Pattern.(*ast.MatchStructPattern)
		if !ok {
			t.Fatalf("%s: arm pattern is %T, want *ast.MatchStructPattern", tc.name, match.Arms[0].Pattern)
		}
		if structPat.TypeName != "Pack.Item" {
			t.Fatalf("%s: pattern type name = %q, want %q", tc.name, structPat.TypeName, "Pack.Item")
		}
	}
}

// The dotted spelling stays rejected, with the sentence the `let` destructure position
// already gives for the identical path -- one rule, one message, wherever it is written.
func TestParseQualifiedStructPatternStillRejectsDots(t *testing.T) {
	_, errs := parseSourceFile(t, "def f(it: int) -> int:\n    match it:\n        Pack.Item{v}:\n            return v\n")
	if len(errs) == 0 {
		t.Fatal("expected the dotted module path to be rejected")
	}
	if !strings.Contains(errs[0], "module paths are separated by `::`, not `.`; write `Pack::Item`") {
		t.Fatalf("unexpected diagnostic: %s", errs[0])
	}
}
