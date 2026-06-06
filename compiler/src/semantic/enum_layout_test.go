package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func enumTypeByName(t *testing.T, result *Result, name string) *EnumType {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		// enums live in namedTypes, not the value scope
		if et, ok := result.NamedTypes[name].(*EnumType); ok {
			return et
		}
		t.Fatalf("enum %q not found", name)
	}
	if et, ok := sym.Type.(*EnumType); ok {
		return et
	}
	if et, ok := result.NamedTypes[name].(*EnumType); ok {
		return et
	}
	t.Fatalf("type %q is not an enum", name)
	return nil
}

// docs/76 Phase 1: `enum X layout soa:` parses and the layout suffix is carried onto the EnumType.
func TestEnumLayoutSoaCarried(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "el_soa.elisa", `packed enum Expr layout soa:
    common:
        span: int
    Int(value: int)
    Add(left: Expr, right: Expr)
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("layout soa enum must parse cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	et := enumTypeByName(t, result, "Expr")
	if !et.LayoutSet || et.Layout != ast.StructLayoutSOA {
		t.Fatalf("expected Layout=SOA set, got Layout=%v set=%v", et.Layout, et.LayoutSet)
	}
}

// The `(index: u16)` and `(sparse)` sub-options are carried.
func TestEnumLayoutSubOptionsCarried(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "el_opts.elisa", `packed enum Expr layout soa(sparse, index: u16):
    common:
        span: int
    Int(value: int)
    Add(left: Expr, right: Expr)
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("layout soa(sparse, index: u16) must parse cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	et := enumTypeByName(t, result, "Expr")
	if !et.LayoutSparse {
		t.Fatalf("expected LayoutSparse true")
	}
	if et.IndexWidth != "u16" {
		t.Fatalf("expected IndexWidth u16, got %q", et.IndexWidth)
	}
}

// An enum with no layout suffix has LayoutSet false (the compiler default applies).
func TestEnumLayoutDefaultUnset(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "el_def.elisa", `packed enum Expr:
    common:
        span: int
    Int(value: int)
    Add(left: Expr, right: Expr)
`, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors:\n%s", strings.Join(errs, "\n"))
	}
	et := enumTypeByName(t, result, "Expr")
	if et.LayoutSet {
		t.Fatalf("an enum with no layout suffix must have LayoutSet=false")
	}
}

// (Bad index width — e.g. `index: u128` — is rejected at parse time with "enum index width must be
// u8, u16, u32, or u64"; the parser-level rejection is covered by the parser package, not here,
// because this semantic harness fatals on parse errors rather than surfacing them.)
