package parser

import (
	"testing"

	"elisacore/src/ast"
)

// `export fn f(...) = Mod::g` accepts a `::`-qualified target and stores it as
// the internal dotted name, so the analyzer resolves it like any module member.
// Previously the target parsed as a single IDENT and the `::` tripped
// expectNewline ("expected newline, got ::").
func TestParseExportFuncQualifiedTarget(t *testing.T) {
	file, errs := parseSourceFile(t, `
export fn probe(seed: u64) -> u32 = Semantic::hash_occupied_probe
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.ExportFuncDecl)
	if !ok {
		t.Fatalf("expected ExportFuncDecl, got %T", file.Decls[0])
	}
	if decl.Name != "probe" {
		t.Fatalf("expected exported name probe, got %q", decl.Name)
	}
	if decl.TargetName != "Semantic.hash_occupied_probe" {
		t.Fatalf("expected target Semantic.hash_occupied_probe, got %q", decl.TargetName)
	}
}

// An unqualified target still parses (regression guard for the common case).
func TestParseExportFuncBareTarget(t *testing.T) {
	file, errs := parseSourceFile(t, `
export fn probe(seed: u64) -> u32 = local_impl
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.ExportFuncDecl)
	if decl.TargetName != "local_impl" {
		t.Fatalf("expected target local_impl, got %q", decl.TargetName)
	}
}

// `export global Mod::g` accepts a qualified target; the default public alias is
// its last segment (a dotted name is not a valid C export symbol), and an
// explicit `as` still overrides it.
func TestParseExportGlobalQualifiedTargetDefaultsAliasToLastSegment(t *testing.T) {
	file, errs := parseSourceFile(t, `
export global Config::max_depth
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.ExportGlobalDecl)
	if !ok {
		t.Fatalf("expected ExportGlobalDecl, got %T", file.Decls[0])
	}
	if decl.TargetName != "Config.max_depth" {
		t.Fatalf("expected target Config.max_depth, got %q", decl.TargetName)
	}
	if decl.Alias != "max_depth" {
		t.Fatalf("expected default alias max_depth, got %q", decl.Alias)
	}
}

func TestParseExportGlobalQualifiedTargetExplicitAlias(t *testing.T) {
	file, errs := parseSourceFile(t, `
export global Config::max_depth as elisa_max_depth
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.ExportGlobalDecl)
	if decl.TargetName != "Config.max_depth" {
		t.Fatalf("expected target Config.max_depth, got %q", decl.TargetName)
	}
	if decl.Alias != "elisa_max_depth" {
		t.Fatalf("expected alias elisa_max_depth, got %q", decl.Alias)
	}
}
