//go:build cgo

package semantic

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// TestSMTProfileBatch answers the question "is SMT cheap or demanding?" with data. It generates a
// batch of functions each carrying a NON-LINEAR refinement obligation (the hard residue the linear
// prover declines and the solver must take), analyzes them with the SMT tier on, and reports the
// per-obligation cost. Run with: go test ./src/semantic -run TestSMTProfileBatch -v
func TestSMTProfileBatch(t *testing.T) {
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; SMT profile skipped")
	}
	const n = 200
	var b strings.Builder
	b.WriteString("law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi\n")
	b.WriteString("type Small = i64 is Bounded[2, 10]\n\n")
	// Each function returns a*b (nonlinear) with a true bound — the solver proves all of them.
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "def mul%d(a: Small, b: Small) -> i64 is Bounded[4, 100]:\n    return a * b\n\n", i)
	}
	src := b.String()

	l := lexer.New("smt_profile.elisa", []byte(src))
	tokens := l.Tokenize()
	p := parser.New(tokens)
	file := p.ParseFile("smt_profile.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}

	start := time.Now()
	result := AnalyzeWithOptions(file, AnalyzeOptions{EnableSMT: true})
	wall := time.Since(start)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	prof := result.SMTProfile
	if prof.Attempts < n {
		t.Fatalf("expected >= %d SMT attempts, got %d", n, prof.Attempts)
	}
	if prof.Proven < n {
		t.Fatalf("expected >= %d SMT proofs, got %d", n, prof.Proven)
	}
	perQuery := float64(prof.SolverTime.Microseconds()) / float64(prof.Attempts) / 1000.0
	t.Logf("SMT PROFILE over %d nonlinear obligations:", prof.Attempts)
	t.Logf("  proven=%d declined=%d", prof.Proven, prof.Declined)
	t.Logf("  solver total = %.1fms  (spawn %.1fms, slowest %.1fms)",
		float64(prof.SolverTime.Microseconds())/1000.0,
		float64(prof.SpawnTime.Microseconds())/1000.0,
		float64(prof.Slowest.Microseconds())/1000.0)
	t.Logf("  per-obligation solver cost = %.3fms", perQuery)
	t.Logf("  whole-analysis wall (lex+parse+analyze+SMT) = %.1fms", float64(wall.Microseconds())/1000.0)
}
