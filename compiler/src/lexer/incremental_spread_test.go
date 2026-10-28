package lexer

import (
	"strings"
	"testing"
)

func TestSourceCheckpointSpacingCoversLargeChainedEdits(t *testing.T) {
	part := strings.Repeat("value <- 1 + 2\n", 80000)
	original := "#line 40 first.elisa\nearly <- 7\n" + part + "#line 70 middle.elisa\nmidpoint <- 7\n" + part + "#line 90 last.elisa\nlatepoint <- 7\n" + part
	before := assertSourceTokensCold(t, original, nil)
	if len(before.tokens) <= sourceCheckpointLimit*sourceCheckpointStride {
		t.Fatal("fixture too small")
	}
	if len(before.checkpoints) == 0 {
		t.Fatal("missing checkpoints")
	}
	if before.checkpoints[len(before.checkpoints)-1].count < len(before.tokens)*9/10 {
		t.Fatalf("checkpoints did not span source: %d of %d", before.checkpoints[len(before.checkpoints)-1].count, len(before.tokens))
	}
	if len(before.checkpoints) > sourceCheckpointLimit || before.checkpointBytes > sourceCheckpointBytes {
		t.Fatal("checkpoint budget exceeded")
	}
	middle := strings.Replace(original, "midpoint <- 7", "midpoint <- 12345\n\n", 1)
	second := assertSourceTokensCold(t, middle, before)
	if second.SuffixReusedTokens() < len(before.tokens)/4 {
		t.Fatalf("middle suffix not reused: %d", second.SuffixReusedTokens())
	}
	late := strings.Replace(middle, "latepoint <- 7", "latepoint <- 23", 1)
	third := assertSourceTokensCold(t, late, second)
	if third.ReusedTokens() < len(second.tokens)/2 {
		t.Fatalf("late prefix not reused: %d", third.ReusedTokens())
	}
	broken := strings.Replace(late, "early <- 7", "early <- \"unterminated", 1)
	fourth := assertSourceTokensCold(t, broken, third)
	if fourth.SuffixReusedTokens() != 0 {
		t.Fatal("lexer error allowed suffix replay")
	}
}

func TestSourceCheckpointSpacingThresholdAndDenseLayout(t *testing.T) {
	// Cross the spacing threshold in both directions while layout tokens and
	// grouped pending tokens are present. Compare every token and diagnostic.
	block := strings.Repeat("if true:\n    value <- (1 +\n        2)\n", 32000)
	below := strings.Repeat("if true:\n    value <- (1 +\n        2)\n", 24000) + "\ntail <- 7\n"
	above := block + "\ntail <- 7\n"
	small := strings.Repeat("x <- 1\n", 100) + "tail <- 7\n"
	current := assertSourceTokensCold(t, below, nil)
	for _, source := range []string{above, strings.Replace(above, "tail <- 7", "tail <- 8", 1), small, above, below} {
		current = assertSourceTokensCold(t, source, current)
		if len(current.checkpoints) > sourceCheckpointLimit || current.checkpointBytes > sourceCheckpointBytes {
			t.Fatal("checkpoint limit exceeded")
		}
	}
}

func TestSourceCheckpointSpacingSparseAndDenseBudget(t *testing.T) {
	// Long sparse text makes byte spacing conservative; densely nested layout
	// pushes checkpoint-state memory without allowing a wrong cached token.
	sparse := strings.Repeat("# comment padding padding padding\n", 50000) + "end <- 1\n"
	old := assertSourceTokensCold(t, sparse, nil)
	if old.checkpointBytes > sourceCheckpointBytes {
		t.Fatal("sparse budget exceeded")
	}
	_ = assertSourceTokensCold(t, strings.Replace(sparse, "end <- 1", "end <- 24", 1), old)
	var b strings.Builder
	for i := 0; i < 1500; i++ {
		b.WriteString(strings.Repeat(" ", i))
		b.WriteString("x\n")
	}
	dense := b.String()
	state := assertSourceTokensCold(t, dense, nil)
	if state.checkpointBytes > sourceCheckpointBytes || len(state.checkpoints) > sourceCheckpointLimit {
		t.Fatal("dense budget exceeded")
	}
	_ = assertSourceTokensCold(t, dense+"y\n", state)
}
