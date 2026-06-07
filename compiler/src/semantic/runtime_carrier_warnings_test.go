package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

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
