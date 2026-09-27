package semantic_test

import (
	"strings"
	"testing"
)

// Packed-enum diagnostics name a nested enum by its `::` module path, the spelling the
// source uses; the internal registry key joins modules with `.`, which read as a field
// access ("Outer.Right.Message.A") in the message.
func TestAnalyzePackedDiagnosticsSpellModulePathWithColons(t *testing.T) {
	src := `module Outer:
	module Right:
		packed enum Message:
			A(x: i64)
			B(y: i64)

def make() -> i64:
	m: Outer::Right::Message = Outer::Right::Message.A(x: 1)
	return 0

def pick(m: Outer::Right::Message) -> i64:
	match m:
		Outer::Right::Message.A(x): return x
		Outer::Right::Message.B(y): return y
`
	_, errs := parseAndAnalyze(t, "packed_module_path_spelling.elisa", src)
	all := strings.Join(errs, "\n")
	for _, want := range []string{
		"packed enum constructor \"Outer::Right::Message.A\" requires an active in Outer::Right::Message.Store: scope or explicit new[Outer::Right::Message.Store]",
		"packed enum match over \"Outer::Right::Message\" requires an in Outer::Right::Message.Store clause",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("expected %q, got:\n%s", want, all)
		}
	}
	if strings.Contains(all, "Outer.Right") {
		t.Fatalf("diagnostic spelled the module path with '.':\n%s", all)
	}
}
