package semantic

import (
	"slices"
	"strings"
	"testing"
)

func TestAnalysisBuildCheckpointCompactsSMTHistory(t *testing.T) {
	build, err := BeginAnalysisBuild(retainedParse(t, retainedBefore), nil, []byte(retainedBefore), AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.Repeat("(declare-const foo Int)\n", 4096)
	want := []string{prefix + "(assert A)", prefix + "(assert B)", "independent", ""}
	build.retained.a.smtHistory = slices.Clone(want)
	checkpoint, err := build.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(build.retained.a.smtHistory, want) {
		t.Fatal("checkpoint modified the live proof history")
	}
	graph, compact, err := splitCheckpointHistory(checkpoint)
	if err != nil || len(graph) == 0 || len(compact) == 0 {
		t.Fatalf("missing compact history: %v", err)
	}
	restored, err := RestoreAnalysisBuild(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(restored.retained.a.smtHistory, want) {
		t.Fatal("restored proof history differs from the original queries")
	}

	checkpoint[len(graph)-1] ^= 1
	if _, _, err := splitCheckpointHistory(checkpoint); err == nil {
		t.Fatal("accepted compact history from a different graph")
	}
	checkpoint[len(graph)-1] ^= 1
	checkpoint[len(graph)] ^= 1
	if _, err := RestoreAnalysisBuild(checkpoint); err == nil {
		t.Fatal("accepted corrupted compact history")
	}
}

func TestRestoreCheckpointHistoryRejectsInvalidPrefix(t *testing.T) {
	// One query claiming to reuse a byte from the empty initial prefix.
	if _, err := restoreCheckpointHistory([]byte{1, 1, 0}); err == nil {
		t.Fatal("accepted a prefix beyond the preceding query")
	}
}
