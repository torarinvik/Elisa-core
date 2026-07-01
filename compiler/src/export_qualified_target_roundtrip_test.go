package main

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/unparse"
)

// A `::`-qualified export target survives parse -> unparse -> parse: the parser
// stores it as the internal dotted name and unparse renders it back to `::`
// form (dotted output would not reparse, since export-target position consumes
// `::` not `.`).
func TestExportQualifiedTargetRoundTrips(t *testing.T) {
	src := "export func probe(seed: u64) -> u32 = Semantic::hash_occupied_probe\n" +
		"export global Config::max_depth\n" +
		"export global Config::max_depth as elisa_max_depth\n"

	parse := func(text string) *ast.File {
		t.Helper()
		l := lexer.New("roundtrip.elisa", []byte(text))
		tokens := l.Tokenize()
		if errs := l.Errors(); len(errs) > 0 {
			t.Fatalf("lex errors on %q: %v", text, errs)
		}
		p := parser.New(tokens)
		file := p.ParseFile("roundtrip.elisa")
		if errs := p.Errors(); len(errs) > 0 {
			t.Fatalf("parse errors on %q: %v", text, errs)
		}
		return file
	}

	first := parse(src)
	rendered := unparse.FormatFile(first)

	// The rendered form must use `::`, not the internal dotted name.
	if strings.Contains(rendered, "Semantic.hash_occupied_probe") ||
		strings.Contains(rendered, "Config.max_depth") {
		t.Fatalf("unparse leaked internal dotted target name:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Semantic::hash_occupied_probe") {
		t.Fatalf("unparse dropped qualified func target:\n%s", rendered)
	}

	// Reparsing the rendered text yields the same targets/aliases.
	second := parse(rendered)
	if len(second.Decls) != 3 {
		t.Fatalf("expected 3 decls after round-trip, got %d:\n%s", len(second.Decls), rendered)
	}
	fn := second.Decls[0].(*ast.ExportFuncDecl)
	if fn.TargetName != "Semantic.hash_occupied_probe" {
		t.Fatalf("func target changed on round-trip: %q", fn.TargetName)
	}
	g0 := second.Decls[1].(*ast.ExportGlobalDecl)
	if g0.TargetName != "Config.max_depth" || g0.Alias != "max_depth" {
		t.Fatalf("global(default alias) changed on round-trip: target=%q alias=%q", g0.TargetName, g0.Alias)
	}
	g1 := second.Decls[2].(*ast.ExportGlobalDecl)
	if g1.TargetName != "Config.max_depth" || g1.Alias != "elisa_max_depth" {
		t.Fatalf("global(explicit alias) changed on round-trip: target=%q alias=%q", g1.TargetName, g1.Alias)
	}
}
