package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// The layout clause is unified: one parenthesized postfix clause `layout(...)` across
// struct, enum, and guest-overlay declarations. The removed spellings — the prefix form
// `layout soa struct X:`, the bare-word postfix `struct X layout soa:`, and the
// standalone overlay `layout Name size N:` — all get directed diagnostics with
// recover-as-canonical (same layout recorded, member block parses, no cascade).

func TestParseStructLayoutClauseCanonical(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Store layout(soa):\n    xs: darray[int]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if decl.Layout != ast.StructLayoutSOA {
		t.Fatalf("expected SOA layout, got %v", decl.Layout)
	}
}

func TestParseStructLayoutBareWordRemoved(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Store layout soa:\n    xs: darray[int]\n")
	if len(errs) != 1 || !strings.Contains(errs[0], "bare `layout soa` has been removed; use the parenthesized clause `layout(soa)`") {
		t.Fatalf("expected exactly the bare-layout removal diagnostic, got: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl to survive recovery, got %T", file.Decls[0])
	}
	if decl.Layout != ast.StructLayoutSOA {
		t.Fatalf("expected recovery to keep SOA layout, got %v", decl.Layout)
	}
}

func TestParseStructLayoutPrefixRemoved(t *testing.T) {
	file, errs := parseSourceFile(t, "layout soa struct Store:\n    xs: darray[int]\n")
	if len(errs) != 1 || !strings.Contains(errs[0], "`layout soa struct Name:` has been removed; use the postfix clause `struct Name layout(soa):`") {
		t.Fatalf("expected exactly the prefix removal diagnostic, got: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl to survive recovery, got %T", file.Decls[0])
	}
	if decl.Layout != ast.StructLayoutSOA {
		t.Fatalf("expected recovery to keep SOA layout, got %v", decl.Layout)
	}
}

func TestParseEnumLayoutClauseCanonical(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr layout(soa, sparse, handle: u16):\n    Int(value: int)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.EnumDecl)
	if !ok {
		t.Fatalf("expected enum decl, got %T", file.Decls[0])
	}
	if decl.Layout != ast.StructLayoutSOA || !decl.LayoutSparse || decl.IndexWidth != "u16" {
		t.Fatalf("expected soa+sparse+handle u16, got %v sparse=%v handle=%q", decl.Layout, decl.LayoutSparse, decl.IndexWidth)
	}
}

func TestParseEnumLayoutBareWordRemoved(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr layout soa(sparse):\n    Int(value: int)\n")
	if len(errs) != 1 || !strings.Contains(errs[0], "bare `layout soa` has been removed") {
		t.Fatalf("expected exactly the bare-layout removal diagnostic, got: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.EnumDecl)
	if !ok {
		t.Fatalf("expected enum decl to survive recovery, got %T", file.Decls[0])
	}
	if decl.Layout != ast.StructLayoutSOA || !decl.LayoutSparse {
		t.Fatalf("expected recovery to keep soa+sparse, got %v sparse=%v", decl.Layout, decl.LayoutSparse)
	}
}

func TestParseGuestOverlayCanonical(t *testing.T) {
	file, errs := parseSourceFile(t, `struct ProcParamOverlay layout(guest, size: 72):
    size: u64 at 0
    mem_param: u64 at 64 requires size >= 72
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.LayoutDecl)
	if !ok {
		t.Fatalf("expected guest overlay to parse as LayoutDecl, got %T", file.Decls[0])
	}
	if decl.Name != "ProcParamOverlay" || decl.Size != 72 {
		t.Fatalf("expected ProcParamOverlay size 72, got %q size %d", decl.Name, decl.Size)
	}
	if len(decl.Fields) != 2 {
		t.Fatalf("expected 2 overlay fields, got %d", len(decl.Fields))
	}
	if decl.Fields[0].Name != "size" || decl.Fields[0].Offset != 0 {
		t.Fatalf("field 0 wrong: %#v", decl.Fields[0])
	}
	if decl.Fields[1].Name != "mem_param" || decl.Fields[1].Offset != 64 || decl.Fields[1].RequiresSizeAtLeast != 72 {
		t.Fatalf("field 1 wrong: %#v", decl.Fields[1])
	}
}

func TestParseGuestOverlayFieldNeedsOffset(t *testing.T) {
	_, errs := parseSourceFile(t, "struct P layout(guest, size: 8):\n    size: u64\n")
	if len(errs) != 1 || !strings.Contains(errs[0], "needs an explicit offset") {
		t.Fatalf("expected the missing-offset diagnostic, got: %v", errs)
	}
}

func TestParseStandaloneLayoutDeclRemoved(t *testing.T) {
	file, errs := parseSourceFile(t, "layout ProcParamOverlay size 72:\n    0 size: u64\n    64 mem_param: u64 requires size >= 72\n")
	if len(errs) != 1 || !strings.Contains(errs[0], "standalone `layout ProcParamOverlay size 72:` has been removed; use `struct ProcParamOverlay layout(guest, size: 72):`") {
		t.Fatalf("expected exactly the standalone removal diagnostic, got: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.LayoutDecl)
	if !ok {
		t.Fatalf("expected LayoutDecl to survive recovery, got %T", file.Decls[0])
	}
	if len(decl.Fields) != 2 || decl.Fields[1].RequiresSizeAtLeast != 72 {
		t.Fatalf("expected recovery to keep the overlay fields, got %#v", decl.Fields)
	}
}

// A struct rejecting enum-only options, and vice versa.
func TestParseLayoutClauseContextValidation(t *testing.T) {
	_, errs := parseSourceFile(t, "struct S layout(soa, sparse):\n    xs: darray[int]\n")
	if len(errs) != 1 || !strings.Contains(errs[0], "`sparse` is an enum layout option") {
		t.Fatalf("expected sparse-on-struct rejection, got: %v", errs)
	}
	_, errs = parseSourceFile(t, "packed enum E layout(guest):\n    A(x: int)\n")
	if len(errs) != 1 || !strings.Contains(errs[0], "`guest` is a struct overlay layout") {
		t.Fatalf("expected guest-on-enum rejection, got: %v", errs)
	}
}
