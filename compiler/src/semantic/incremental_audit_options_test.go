//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestIncrementalLexicalAuditIsIndependentOfTypingReplay(t *testing.T) {
	base := "def first(value: int) -> int:\n    return value + 1\n\ndef second(value: i64) -> i64:\n    local: i64 = value * 2\n    return local\n"
	plain := NewIncrementalAnalysisSession()
	audited := NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{AuditLexicalReads: true})
	for _, src := range []string{
		base, "\n\n" + base,
		strings.Replace(base, "value + 1", "value + 3", 1),
		strings.Replace(base, "value + 1", "missing + 1", 1),
		strings.Replace(base, "value: int", "value: i64", 1),
		base,
	} {
		fresh := Analyze(parseIncrementalSessionFile(t, src))
		beforePlain, beforeAudit := plain.Stats(), audited.Stats()
		normal := plain.Analyze(parseIncrementalSessionFile(t, src), AnalyzeOptions{})
		audit := audited.Analyze(parseIncrementalSessionFile(t, src), AnalyzeOptions{})
		assertIncrementalResultParity(t, fresh, normal)
		assertIncrementalResultParity(t, fresh, audit)
		afterPlain, afterAudit := plain.Stats(), audited.Stats()
		if afterPlain.PrimaryBodiesReused-beforePlain.PrimaryBodiesReused != afterAudit.PrimaryBodiesReused-beforeAudit.PrimaryBodiesReused ||
			afterPlain.LocalInitializersReused-beforePlain.LocalInitializersReused != afterAudit.LocalInitializersReused-beforeAudit.LocalInitializersReused {
			t.Fatalf("audit changed replay decisions: plain=%+v audit=%+v", afterPlain, afterAudit)
		}
	}
	if plain.lexical != nil || plain.Stats().LexicalSourceIndexesBuilt != 0 || plain.Stats().LexicalReadsObserved != 0 {
		t.Fatal("default session allocated a shadow lexical world")
	}
	if audited.lexical == nil || audited.Stats().LexicalSelectionsVerified == 0 || audited.Stats().LexicalVerificationFailures != 0 {
		t.Fatalf("explicit audit did not verify resolver observations: %+v", audited.Stats())
	}
	if plain.Stats().PrimaryBodiesReused == 0 || plain.Stats().LocalInitializersReused == 0 {
		t.Fatal("audit-independent replay was not exercised")
	}
}
