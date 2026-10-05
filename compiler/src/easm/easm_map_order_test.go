package easm

import (
	"strings"
	"testing"
)

// Hole substitution is sequential text rewriting, so its order must not come from a map: the
// longest hole name goes first (a hole whose name contains another's is never clipped), ties by
// name. The result is the same on every run.
func TestSubstituteParamsIsOrderStable(t *testing.T) {
	subst := map[string]string{"x": "1", "xy": "x", "y": "2"}
	want := "1 + 1 + 2"
	for run := 0; run < 50; run++ {
		if got := substituteParams("xy + x + y", subst); got != want {
			t.Fatalf("run %d: got %q, want %q", run, got, want)
		}
	}
}

// Merge-consistency issues are reported in label order, not map order.
func TestCheckMergeConsistencyReportsLabelsInOrder(t *testing.T) {
	fn := &Function{Name: "f"}
	live := func(regs ...string) map[string]bool {
		out := map[string]bool{}
		for _, r := range regs {
			out[r] = true
		}
		return out
	}
	// Each label's walk assumes a definite fs state that its one predecessor does not establish,
	// so every label yields a merge-fs-state-unsound issue.
	jumpPreds := map[string][]easmMergeSnap{
		"zz": {{live: live("rax"), fs: ""}},
		"aa": {{live: live("rax"), fs: ""}},
		"mm": {{live: live("rax"), fs: ""}},
	}
	entry := map[string]easmMergeSnap{
		"zz": {live: live("rax"), fs: "tls"},
		"aa": {live: live("rax"), fs: "tls"},
		"mm": {live: live("rax"), fs: "tls"},
	}
	var first []string
	for run := 0; run < 30; run++ {
		issues := checkMergeConsistency("f.easm", fn, jumpPreds, entry, map[string]bool{}, map[string]LabelContract{})
		if len(issues) != 3 {
			t.Fatalf("expected 3 issues, got %d: %+v", len(issues), issues)
		}
		var msgs []string
		for _, issue := range issues {
			msgs = append(msgs, issue.Message)
		}
		if run == 0 {
			first = msgs
			for i, label := range []string{"aa", "mm", "zz"} {
				if !strings.Contains(msgs[i], "merge label "+label+":") {
					t.Fatalf("issue %d should name label %s: %v", i, label, msgs)
				}
			}
			continue
		}
		for i := range msgs {
			if i >= len(first) || msgs[i] != first[i] {
				t.Fatalf("run %d issue order differs: %v vs %v", run, first, msgs)
			}
		}
	}
}
