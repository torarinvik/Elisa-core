package backend

import (
	"strings"
	"testing"
)

// The -Wperf auto-vectorization warning names the construct that failed and the most likely blocker
// for that construct (docs/79 Part IV). The construct label rides in the loop marker (ForStmt
// .AutovecReason) so the post-optimization verifier can phrase a specific, actionable message rather
// than a generic one.
func TestAutovecPerfWarningIsConstructSpecific(t *testing.T) {
	cases := []struct {
		reason   string
		wantWord string // a phrase unique to that construct's hint
	}{
		{"fold reduction", "re-bracket"},
		{"comprehension map", "element store"},
		{"loop", "cost model"}, // user-written vector-eligible loop
		{"", "loop-carried"},   // unknown reason falls back to the generic construct/hint
	}
	for _, c := range cases {
		msg := autovecPerfWarning("file.elisa:3:5", c.reason, 3, false)
		if !strings.Contains(msg, "warning [-Wperf]") || !strings.Contains(msg, "did not vectorize") {
			t.Fatalf("reason %q: message missing the -Wperf marker phrasing: %s", c.reason, msg)
		}
		// Every variant names the `can Scalar` escape hatch, and -Wperf enforcement flips the
		// severity word to error (the exit-code promotion is tested end-to-end in
		// src/wperf_scalar_permission_test.go).
		if !strings.Contains(msg, "can Scalar") {
			t.Fatalf("reason %q: message must name the can Scalar escape hatch: %s", c.reason, msg)
		}
		if enforced := autovecPerfWarning("file.elisa:3:5", c.reason, 3, true); !strings.Contains(enforced, "error [-Wperf]") {
			t.Fatalf("reason %q: enforced message must carry error severity: %s", c.reason, enforced)
		}
		if !strings.Contains(msg, "-O3") || !strings.Contains(msg, "file.elisa:3:5") {
			t.Fatalf("reason %q: message missing position/opt level: %s", c.reason, msg)
		}
		if !strings.Contains(msg, c.wantWord) {
			t.Fatalf("reason %q: expected a construct-specific hint containing %q, got: %s", c.reason, c.wantWord, msg)
		}
		construct := c.reason
		if construct == "" {
			construct = "comprehension"
		}
		if !strings.Contains(msg, construct) {
			t.Fatalf("reason %q: message should name the construct %q: %s", c.reason, construct, msg)
		}
	}
}
