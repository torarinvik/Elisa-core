package parser

import (
	"testing"

	"elisacore/src/ast"
)

// `grant Name = ref, ref` parses into a AliasDecl with its permission refs.
func TestParseAliasDecl(t *testing.T) {
	file, errs := parseSourceFile(t, `
alias HostSeg = Segment.Host, Unsafe.SegmentMutation
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.AliasDecl)
	if !ok {
		t.Fatalf("expected AliasDecl, got %T", file.Decls[0])
	}
	if decl.Name != "HostSeg" {
		t.Fatalf("expected name HostSeg, got %q", decl.Name)
	}
	if len(decl.Refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %#v", len(decl.Refs), decl.Refs)
	}
	if decl.Refs[0].Name != "Segment" || decl.Refs[0].Member != "Host" {
		t.Fatalf("expected Segment.Host, got %q.%q", decl.Refs[0].Name, decl.Refs[0].Member)
	}
	if decl.Refs[1].Name != "Unsafe" || decl.Refs[1].Member != "SegmentMutation" {
		t.Fatalf("expected Unsafe.SegmentMutation, got %q.%q", decl.Refs[1].Name, decl.Refs[1].Member)
	}
}
