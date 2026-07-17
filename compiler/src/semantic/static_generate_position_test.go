package semantic

import (
	"strings"
	"testing"
)

// TestAnalyzeStaticGenerateDiagnosticPointsAtTheEmitSite pins the POSITION of a diagnostic
// raised against GENERATED code.
//
// `emit` bodies are rendered to source text and re-lexed, so their tokens carry offsets within
// that generated fragment -- but the filename is the user's REAL file. Left unrebased, a
// diagnostic on a generated declaration named the real file with a line number from the
// fragment, pointing at an arbitrary innocent line: the bad `emit` body below (line 8) was
// reported at line 2 -- an unrelated enum variant -- once per generated instance.
//
// A position that confidently names the wrong line is worse than no position: it sends the
// reader to code that is not the problem. The emit site is the only meaningful anchor, since
// the generated text has no independent existence on disk for the user to open.
func TestAnalyzeStaticGenerateDiagnosticPointsAtTheEmitSite(t *testing.T) {
	// The `emit` is on line 6; its bad statement is on line 7. Line 2 is `Int(value: i64)`,
	// which is what the unrebased position used to blame.
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "gen_pos.elisa", `enum Expr:
    Int(value: i64)
    Bool(value: bool)

static generate:
    for variant in variants(Expr):
        emit def is_${variant.name}(expr: Expr) -> bool:
            bad: bool = 7
            return expr is Expr.${variant.name}
`, AnalyzeOptions{})

	diags := allDiagnostics(result)
	if !strings.Contains(diags, `variable "bad" expects bool`) {
		t.Fatalf("expected the generated body's type error, got:\n%s", diags)
	}
	// The emit site is line 7 of this fixture (the `emit def ...` line).
	if !strings.Contains(diags, "gen_pos.elisa:7:") {
		t.Fatalf("expected the diagnostic to point at the emit site (line 7), got:\n%s", diags)
	}
	// The enum variant on line 2 is innocent; blaming it is the exact regression.
	if strings.Contains(diags, "gen_pos.elisa:2:") {
		t.Fatalf("diagnostic blames line 2 (fragment-relative position leaked into the real file):\n%s", diags)
	}
}
