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
		{"", "loop-carried"}, // unknown reason falls back to the generic construct/hint
	}
	for _, c := range cases {
		msg := autovecPerfWarning("file.elisa:3:5", c.reason, 3)
		if !strings.Contains(msg, "-Wperf") || !strings.Contains(msg, "did not vectorize") {
			t.Fatalf("reason %q: message missing the -Wperf marker phrasing: %s", c.reason, msg)
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
