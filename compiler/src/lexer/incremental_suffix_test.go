package lexer

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func convergenceFixture() string {
	body := strings.Repeat("value <- 3 + 4 * 5\n", 180)
	return "#line 70 original.elisa\nseed <- 1\n" + body + "#line 200 original.elisa\n" + body + "#line 900 included.elisa\n" + body
}

func TestSourceConvergenceShiftedLinesOffsetsAndDirectiveResets(t *testing.T) {
	source := convergenceFixture()
	old := assertSourceTokensCold(t, source, nil)
	for _, replacement := range []string{"12345", "1 + 2", "1\n\n\n", "\"αβ\""} {
		changed := strings.Replace(source, "seed <- 1", "seed <- "+replacement, 1)
		current := assertSourceTokensCold(t, changed, old)
		if current.SuffixReusedTokens() < 256 {
			t.Fatalf("no convergence for %q", replacement)
		}
		if current.ReusedTokens() != 0 {
			t.Fatal("suffix reuse changed prefix metric")
		}
		// A subsequent late edit must still use checkpoints copied from the suffix.
		late := strings.LastIndex(changed, "value <- 3")
		next := changed[:late] + strings.Replace(changed[late:], "value <- 3", "value <- 99", 1)
		last := assertSourceTokensCold(t, next, current)
		if last.ReusedTokens() < len(current.tokens)/2 {
			t.Fatal("late edit lost prefix checkpoints")
		}
	}
}

func TestSourceConvergenceOwnsViewsAndCheckpointState(t *testing.T) {
	source := convergenceFixture()
	old := assertSourceTokensCold(t, source, nil)
	changed := strings.Replace(source, "seed <- 1", "seed <- 12345\n\n", 1)
	current := assertSourceTokensCold(t, changed, old)
	expected := current.CopyTokens()
	for i := range old.source {
		old.source[i] = '!'
	}
	for i := range old.tokens {
		old.tokens[i].Text = "corrupted"
	}
	if !reflect.DeepEqual(current.CopyTokens(), expected) {
		t.Fatal("new suffix retained previous source views")
	}
	for _, cp := range current.checkpoints {
		if len(cp.state.src) > 0 && &cp.state.src[0] != &current.source[0] {
			t.Fatal("checkpoint retained old source")
		}
	}
	// Exercise rebased state after its old owner has been destroyed.
	assertSourceTokensCold(t, changed+"tail <- 7\n", current)
}

func TestSourceConvergenceDiagnosticsAndGroupingStayFresh(t *testing.T) {
	body := strings.Repeat("value <- 3 + 4 * 5\n", 180)
	sources := []string{
		"seed <- 1\n" + body,
		"seed <- 1\nvalue <- (\n" + strings.Repeat("    3 +\n", 180) + "    4\n)\n" + body,
		"seed <- 1\nif true:\n" + strings.Repeat("    value <- 3 + 4\n", 180) + body,
	}
	for _, source := range sources {
		old := assertSourceTokensCold(t, source, nil)
		shifted := strings.Replace(source, "seed <- 1", "seed <- 12345\n\n", 1)
		current := assertSourceTokensCold(t, shifted, old)
		if current.SuffixReusedTokens() == 0 {
			t.Fatal("did not converge after lexical modes closed")
		}
		broken := strings.Replace(source, "seed <- 1", "seed <- \"unterminated", 1)
		bad := assertSourceTokensCold(t, broken, old)
		if bad.SuffixReusedTokens() != 0 {
			t.Fatal("replayed over current lexer error")
		}
	}
}

func TestSourceConvergenceChainedAttributionHistories(t *testing.T) {
	source := convergenceFixture()
	current := assertSourceTokensCold(t, source, nil)
	for revision := 0; revision < 20; revision++ {
		changed := strings.Replace(source, "seed <- 1", fmt.Sprintf("seed <- %d%s", revision+10, strings.Repeat("\n", revision%4)), 1)
		if revision%3 == 0 {
			changed = strings.Replace(changed, "#line 70 original.elisa", "#line 70 original.elisa\n#line 50 original.elisa", 1)
		}
		current = assertSourceTokensCold(t, changed, current)
		if current.checkpointBytes > sourceCheckpointBytes {
			t.Fatal("checkpoint ownership exceeded bound")
		}
	}
}

func BenchmarkSourceConvergenceEarlyShift(b *testing.B) {
	source := convergenceFixture()
	previous := TokenizeSource("convergence.elisa", []byte(source), nil)
	for _, retained := range []bool{false, true} {
		b.Run(fmt.Sprint(retained), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				changed := []byte(strings.Replace(source, "seed <- 1", fmt.Sprintf("seed <- %d\n\n", 10000+i), 1))
				var old *SourceTokens
				if retained {
					old = previous
				}
				current := TokenizeSource("convergence.elisa", changed, old)
				if retained && current.SuffixReusedTokens() == 0 {
					b.Fatal("no convergence")
				}
			}
		})
	}
}

func TestSourceConvergenceCopiesPatternHeaderOffsets(t *testing.T) {
	body := strings.Repeat("value <- 3 + 4 * 5\n", 180)
	source := "seed <- 1\n" + body + "value <- (match " + strings.Repeat("value + ", 300) + "target:\n    _:\n        42\n)\n" + body
	old := assertSourceTokensCold(t, source, nil)
	shifted := strings.Replace(source, "seed <- 1", "seed <- 12345\n\n", 1)
	current := assertSourceTokensCold(t, shifted, old)
	if current.SuffixReusedTokens() == 0 {
		t.Fatal("no suffix convergence")
	}
	header := strings.Index(shifted, "(match ") + len("(match")
	found := false
	for _, cp := range current.checkpoints {
		for _, pattern := range cp.state.pendingPatternBlocks {
			found = true
			if pattern.HeadStart != header {
				t.Fatalf("header offset=%d want %d", pattern.HeadStart, header)
			}
		}
	}
	if !found {
		t.Fatal("fixture has no copied pattern-header checkpoint")
	}
	edited := strings.Replace(shifted, "target:", "changed:", 1)
	assertSourceTokensCold(t, edited, current)
}

func TestSourceConvergenceRandomValidEditHistories(t *testing.T) {
	source := strings.ReplaceAll(convergenceFixture(), "3 + 4", "3u64 + 4")
	source = strings.Replace(source, "seed <- 1", "seed <- 1\nmessage <- \"α\\nb\"", 1)
	current := assertSourceTokensCold(t, source, nil)
	if len(current.errors) != 0 {
		t.Fatal("fixture errors", current.errors)
	}
	random := rand.New(rand.NewSource(913))
	convergences := 0
	for revision := 0; revision < 120; revision++ {
		changed := source
		switch revision % 5 {
		case 0:
			changed = strings.Replace(changed, "seed <- 1", fmt.Sprintf("seed <- %d + 2%s", random.Intn(100000), strings.Repeat("\n", random.Intn(5))), 1)
		case 1:
			changed = strings.Replace(changed, "#line 200 original.elisa", fmt.Sprintf("#line %d original.elisa", 10+random.Intn(1000)), 1)
		case 2:
			changed = "#line 10 prelude.elisa\n" + changed
		case 3:
			changed = strings.Replace(changed, "#line 200 original.elisa", "#line 200 renamed.elisa", 1)
		case 4:
			index := random.Intn(strings.Count(changed, "3u64"))
			offset := 0
			for i := 0; i <= index; i++ {
				offset += strings.Index(changed[offset:], "3u64")
				if i < index {
					offset += 4
				}
			}
			changed = changed[:offset] + strings.Replace(changed[offset:], "3u64", fmt.Sprintf("%du64", random.Intn(99999)), 1)
		}
		current = assertSourceTokensCold(t, changed, current)
		if len(current.errors) != 0 {
			t.Fatal("valid edit became a lexer error", current.errors)
		}
		if current.SuffixReusedTokens() > 0 {
			convergences++
		}
	}
	if convergences < 40 {
		t.Fatalf("only %d valid histories converged", convergences)
	}
}

func TestSourceConvergenceCertifiesBackwardLineIndentReads(t *testing.T) {
	// Boolean continuation suppresses ordinary indentation tracking. Compensate
	// changed leading whitespace with a shorter identifier so columns and stored
	// indentation agree, although a later grouped block sees a different header indent.
	source := "value <- true\n    and leftlong + " + strings.Repeat("value + ", 200) + "(do:\n        42\n    )\n" + strings.Repeat("value <- 3 + 4 * 5\n", 180)
	old := assertSourceTokensCold(t, source, nil)
	if len(old.errors) != 0 {
		t.Fatal("fixture errors", old.errors)
	}
	changed := strings.Replace(source, "    and leftlong", "        and left", 1)
	assertSourceTokensCold(t, changed, old)
}
