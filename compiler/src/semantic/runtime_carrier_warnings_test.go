package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// Regression: the Arena/ArenaMark carrier WARNING uses a prose migration hint ("region
// scopes …"), but that prose must NOT leak into type-DISPLAY diagnostics, where it would
// render as "expects mutable region scopes and inferred container regions&". The two paths
// are split: runtimeCarrierTypeDisplayReplacement (type names only) excludes Arena/ArenaMark
// so they render under their plain name, while runtimeCarrierSurfaceReplacement (the warning)
// keeps the prose hint.
func TestArenaCarrierReplacementSplitDisplayVsWarning(t *testing.T) {
	// Type-display path: Arena/ArenaMark are NOT remapped (render as plain "Arena"/"ArenaMark").
	for _, name := range []string{"Arena", "ArenaMark"} {
		if got, ok := runtimeCarrierTypeDisplayReplacement(name); ok {
			t.Fatalf("type-display replacement for %q must be empty (render plain name), got %q", name, got)
		}
	}
	// Warning path: Arena/ArenaMark DO carry the prose migration hint.
	if got, ok := runtimeCarrierSurfaceReplacement("Arena"); !ok || got != "region scopes and inferred container regions" {
		t.Fatalf("warning replacement for Arena = (%q, %v); want the prose hint", got, ok)
	}
	if got, ok := runtimeCarrierSurfaceReplacement("ArenaMark"); !ok || got != "region scopes/checkpoints" {
		t.Fatalf("warning replacement for ArenaMark = (%q, %v); want the prose hint", got, ok)
	}
	// Hard carriers whose replacement IS a real type name remain shared across both paths.
	for _, tc := range []struct{ name, want string }{
		{"DynArray", "darray[T, shape]"},
		{"DynDict", "dict[K, V]"},
		{"StringView", "sview[...]"},
		{"DynArrayView", "view[T]"},
	} {
		if got, ok := runtimeCarrierTypeDisplayReplacement(tc.name); !ok || got != tc.want {
			t.Fatalf("type-display replacement for %q = (%q, %v); want %q", tc.name, got, ok, tc.want)
		}
	}
}

func TestUserSourceRejectsDirectArenaSurfaceType(t *testing.T) {
	filename := filepath.Join(".", "arena_surface_user_fixture.elisa")
	src := `def build(owner: Arena) -> void:
    pass
`
	if err := os.WriteFile(filename, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	defer func() { _ = os.Remove(filename) }()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	result := Analyze(file)
	all := allDiagnostics(result)
	if !strings.Contains(all, `internal runtime carrier type "Arena" is not supported in user-facing code`) ||
		!strings.Contains(all, `region scopes and inferred container regions`) {
		t.Fatalf("expected user-facing Arena diagnostic, got:\n%s", all)
	}
}

func TestVendoredStdlibSourceAllowsArenaCarrierSurfaceType(t *testing.T) {
	path := filepath.Join("project", "elisac.elisalib", "vendor", "elisacore_std", "collections.elisa")
	if !runtimeCarrierCarrierPathIsInternal(path) {
		t.Fatalf("expected vendored stdlib path to be internal: %s", path)
	}
	src := `def build(owner: Arena) -> void:
    pass
`
	l := lexer.New(path, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(path)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	result := Analyze(file)
	all := allDiagnostics(result)
	if strings.Contains(all, `internal runtime carrier type "Arena"`) {
		t.Fatalf("expected vendored stdlib Arena carrier use to stay quiet, got:\n%s", all)
	}
}
