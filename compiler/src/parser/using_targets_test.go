package parser

import (
	"testing"

	"elisacore/src/ast"
)

func TestParseCommaSeparatedUsingTargetsInStaticBranch(t *testing.T) {
	file, errs := parseSourceFile(t, `
static if true:
    using First, Parent::Second as S, Third::member,
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 1 {
		t.Fatalf("expected only the static branch at file scope, got %d declarations", len(file.Decls))
	}
	branch, ok := file.Decls[0].(*ast.StaticIfDecl)
	if !ok || len(branch.Then) != 3 {
		t.Fatalf("expected three declarations inside the static branch, got %#v", file.Decls[0])
	}
	want := []ast.UsingDecl{
		{Name: "First"},
		{Name: "Parent.Second", Alias: "S"},
		{Name: "Third", Member: "member"},
	}
	for i, expected := range want {
		decl, ok := branch.Then[i].(*ast.UsingDecl)
		if !ok || decl.Name != expected.Name || decl.Alias != expected.Alias || decl.Member != expected.Member {
			t.Errorf("target %d: got %#v, want %#v", i, branch.Then[i], expected)
		}
	}
}
