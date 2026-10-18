//go:build cgo

package semantic

import (
	"fmt"
	"strings"
	"testing"
)

func TestIncrementalReturnTypingRechecksEnsureContracts(t *testing.T) {
	fixtures := []struct{ name, base, bad, changed string }{
		{"integer", "def step(value: int) -> int:\n    ensure result == value + 1\n    return value + 1\n", "ensure result == value + 2", "value + 2"},
		{"predicate", "def step(value: bool) -> bool:\n    ensure result == value\n    return value\n", "ensure result != value", "(not value)"},
		{"literal_body", "def step(value: int) -> int:\n    ensure result == 1 and value == value\n    return 1\n", "ensure result == 2 and value == value", "2"},
	}
	for _, fixture := range fixtures {
		for _, smt := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/smt=%t", fixture.name, smt), func(t *testing.T) {
				options := AnalyzeOptions{EnableSMT: smt, EnforceStrictProofs: true}
				session := NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{AuditLexicalReads: true})
				lines := strings.Split(fixture.base, "\n")
				clause := strings.TrimSpace(lines[1])
				bare := strings.Replace(fixture.base, lines[1]+"\n", "", 1)
				changed := strings.Replace(fixture.base, "return "+strings.TrimPrefix(strings.TrimSpace(lines[2]), "return "), "return "+fixture.changed, 1)
				// Give the changed body its own valid postcondition.
				changed = strings.Replace(changed, clause, "ensure result == "+fixture.changed, 1)
				history := []string{bare, fixture.base, fixture.base, strings.Replace(fixture.base, clause, fixture.bad, 1), strings.Replace(fixture.base, clause, "ensure result == missing", 1), fixture.base, changed}
				for revision, source := range history {
					cold := AnalyzeWithOptions(parseIncrementalSessionFile(t, source), options)
					before := session.Stats()
					warm := session.Analyze(parseIncrementalSessionFile(t, source), options)
					assertIncrementalResultParity(t, cold, warm)
					after := session.Stats()
					if revision == 3 || revision == 4 {
						if len(warm.Errors()) == 0 {
							t.Fatal("changed invalid contract reused a stale successful result")
						}
					} else if len(warm.Errors()) != 0 {
						t.Fatal(warm.Errors())
					}
					if revision > 0 && revision < len(history)-1 && after.PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
						t.Fatalf("revision %d did not reuse unchanged body typing", revision)
					}
					if revision == len(history)-1 && after.PrimaryBodiesChecked-before.PrimaryBodiesChecked != 1 {
						t.Fatal("changed body replayed stale typing")
					}
					if after.LexicalVerificationFailures != 0 || after.LexicalPublicationFailures != 0 {
						t.Fatalf("contract reads failed lexical audit: %+v", after)
					}
				}
				if session.Stats().LexicalSourceIndexesBuilt == 0 {
					t.Fatal("contract-bearing body was incorrectly treated as free of source reads")
				}
			})
		}
	}
}
