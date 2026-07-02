package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// The double-colon protocol-inheritance form `protocol Ord: Eq:` has been removed in
// favor of the language's one subtype spelling: `protocol Ord is Eq:`. The old form now
// fails with a directed diagnostic, and recovery still records the bases so the member
// block parses into a correct AST (no cascade).
func TestParseProtocolDoubleColonSupertypeRejected(t *testing.T) {
	src := "protocol Eq:\n" +
		"    def eq(self: Self, other: Self) -> bool\n" +
		"\n" +
		"protocol Ord: Eq:\n" +
		"    def lt(self: Self, other: Self) -> bool\n"
	file, errs := parseSourceFile(t, src)
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "`protocol Ord: Parent:` has been removed; use `protocol Ord is Parent:`") {
		t.Fatalf("expected protocol supertype removal diagnostic, got: %v", errs)
	}
	if len(errs) != 1 {
		t.Fatalf("expected exactly one diagnostic, got %d: %v", len(errs), errs)
	}
	// Recovery keeps the AST intact: Ord still inherits Eq, member still parses.
	for _, decl := range file.Decls {
		if iface, ok := decl.(*ast.InterfaceDecl); ok && iface.Name == "Ord" {
			if len(iface.Bases) != 1 || iface.Bases[0] != "Eq" {
				t.Fatalf("expected recovery to keep Ord's base Eq, got %v", iface.Bases)
			}
			if len(iface.Members) != 1 {
				t.Fatalf("expected Ord's member block to survive recovery, got %d members", len(iface.Members))
			}
			return
		}
	}
	t.Fatal("expected protocol Ord to parse despite the removed form")
}

// Multiple bases with the canonical `is` spelling.
func TestParseProtocolMultipleBasesWithIs(t *testing.T) {
	src := "protocol Eq:\n" +
		"    def eq(self: Self, other: Self) -> bool\n" +
		"\n" +
		"protocol Show:\n" +
		"    def show(self: Self) -> u64\n" +
		"\n" +
		"protocol Ord is Eq, Show:\n" +
		"    def lt(self: Self, other: Self) -> bool\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	for _, decl := range file.Decls {
		if iface, ok := decl.(*ast.InterfaceDecl); ok && iface.Name == "Ord" {
			if len(iface.Bases) != 2 || iface.Bases[0] != "Eq" || iface.Bases[1] != "Show" {
				t.Fatalf("expected bases [Eq Show], got %v", iface.Bases)
			}
			return
		}
	}
	t.Fatal("expected protocol Ord to parse")
}

// A base-less protocol is unchanged: plain `protocol Name:` still opens the member block.
func TestParseProtocolWithoutBasesUnchanged(t *testing.T) {
	src := "protocol Eq:\n" +
		"    def eq(self: Self, other: Self) -> bool\n"
	_, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
}
