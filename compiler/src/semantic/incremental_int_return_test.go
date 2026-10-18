//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestIncrementalIntReturnsReuseCurrentTyping(t *testing.T) {
	for _, source := range []string{
		"def work() -> int:\n    return 20 + 1\n",
		"def work(value: int) -> int:\n    return value\n",
		"def work(value: int, delta: int) -> int:\n    return -(value + delta * 2)\n",
		"def work(value: int) -> i64:\n    return value + 1\n",
	} {
		t.Run(strings.Split(source, "\n")[0], func(t *testing.T) {
			session := NewIncrementalAnalysisSession()
			cold := Analyze(parseIncrementalSessionFile(t, source))
			if len(cold.Errors()) != 0 {
				t.Fatal(cold.Errors())
			}
			assertIncrementalResultParity(t, cold, session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{}))
			before := session.Stats()
			// Replay into a fresh AST with relocated source positions.
			moved := "\n\n" + source
			warm := session.Analyze(parseIncrementalSessionFile(t, moved), AnalyzeOptions{})
			if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
				t.Fatalf("int return did not reuse typing: before=%+v after=%+v", before, session.Stats())
			}
			assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, moved)), warm)
		})
	}
}

func TestIncrementalIntReturnEditsInvalidateOnlyChangedOwner(t *testing.T) {
	base := "def first(value: int) -> int:\n    return value + 1\n\ndef second(value: int) -> int:\n    return value * 2\n"
	session := NewIncrementalAnalysisSession()
	session.Analyze(parseIncrementalSessionFile(t, base), AnalyzeOptions{})
	for _, source := range []string{
		base,
		strings.Replace(base, "value + 1", "value + 3", 1),
		strings.Replace(base, "first(value: int) -> int", "first(value: i64) -> i64", 1),
		strings.Replace(base, "value + 1", "missing + 1", 1),
	} {
		before := session.Stats()
		current := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), current)
		want := uint64(1)
		if source == base {
			want = 2
		}
		if got := session.Stats().PrimaryBodiesReused - before.PrimaryBodiesReused; got != want {
			t.Fatalf("edit reused %d owners, want %d: %s", got, want, source)
		}
	}
}

func TestIncrementalIntOperatorCandidateInvalidatesReuse(t *testing.T) {
	source := "def work(value: int) -> int:\n    return value + 1\n"
	implementation := `protocol Add:
    def __add__(left: Self, right: Self) -> Self

impl Add for int:
    def __add__(left: int, right: int) -> int:
        return left

`
	session := NewIncrementalAnalysisSession()
	session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	before := session.Stats()
	changed := session.Analyze(parseIncrementalSessionFile(t, implementation+source), AnalyzeOptions{})
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, implementation+source)), changed)
	if session.Stats().PrimaryBodiesReused != before.PrimaryBodiesReused {
		t.Fatal("new int operator candidate reused stale primitive typing")
	}
	before = session.Stats()
	restored := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
	assertIncrementalResultParity(t, Analyze(parseIncrementalSessionFile(t, source)), restored)
	if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
		t.Fatal("removing the int operator failed to restore reuse")
	}
}
