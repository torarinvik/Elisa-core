//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestIncrementalRangePredicateReplayMatchesCold(t *testing.T) {
	base := "def inside(value: int) -> bool:\n    return value in 1 ..= 9\n"
	session := NewIncrementalAnalysisSession()
	for revision, source := range []string{base, base, "\n" + base, strings.Replace(base, "..=", "..<", 1), strings.Replace(base, "1 ..= 9", "2 ..= 10", 1), base} {
		cold := Analyze(parseIncrementalSessionFile(t, source))
		before := session.Stats()
		warm := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		if len(warm.Errors()) != 0 {
			t.Fatal(warm.Errors())
		}
		assertIncrementalResultParity(t, cold, warm)
		if revision == 1 || revision == 2 || revision == 5 {
			if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
				t.Fatalf("revision %d did not reuse range predicate", revision)
			}
		}
	}
}
