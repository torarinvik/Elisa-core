//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestIncrementalRangePredicateReplayMatchesCold(t *testing.T) {
	base := "def inside(value: int) -> bool:\n    return value in 1 ..= 9\n"
	session := NewIncrementalAnalysisSession()
	for revision, source := range []string{base, base, "\n" + base, strings.Replace(base, "..=", "..<", 1), strings.Replace(base, "1 ..= 9", "2 ..= 10", 1), base, base} {
		cold := Analyze(parseIncrementalSessionFile(t, source))
		before := session.Stats()
		warm := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		if len(warm.Errors()) != 0 {
			t.Fatal(warm.Errors())
		}
		assertIncrementalResultParity(t, cold, warm)
		if revision == 1 || revision == 2 || revision == 6 {
			if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
				t.Fatalf("revision %d did not reuse range predicate", revision)
			}
		} else if session.Stats().PrimaryBodiesChecked-before.PrimaryBodiesChecked != 1 {
			t.Fatalf("revision %d did not recheck changed range semantics", revision)
		}
	}
}

func TestIncrementalRangePredicateScalarAndLogicalContexts(t *testing.T) {
	for _, source := range []string{
		"def inside(value: char) -> bool:\n    return value in 'a' ..= 'z'\n",
		"def inside(value: u8) -> bool:\n    return value in 1 ..< 200\n",
		"def inside(value: i8) -> bool:\n    return value in -2 ..= 2\n",
		"def inside(value: i32, first: i32, last: i32) -> bool:\n    return value in first ..< last\n",
		"def inside(value: int) -> bool:\n    return (value in 1 ..= 9 or value in 20 ..< 30) and not (value in 22 ..= 24)\n",
		"def inside(value: int) -> bool:\n    ensure result == (value in 1 ..= 9)\n    return value in 1 ..= 9\n",
	} {
		session := NewIncrementalAnalysisSessionWithOptions(IncrementalAnalysisSessionOptions{AuditLexicalReads: true})
		for revision := 0; revision < 2; revision++ {
			cold := Analyze(parseIncrementalSessionFile(t, source))
			before := session.Stats()
			warm := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
			if len(warm.Errors()) != 0 {
				t.Fatalf("%s: %v", source, warm.Errors())
			}
			assertIncrementalResultParity(t, cold, warm)
			after := session.Stats()
			if revision == 1 && after.PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
				t.Fatalf("range context did not reuse: %s", source)
			}
			if after.LexicalVerificationFailures != 0 || after.LexicalPublicationFailures != 0 {
				t.Fatalf("range lexical audit failed: %+v", after)
			}
		}
	}
}

func TestIncrementalRangePredicateInvalidBoundRecovery(t *testing.T) {
	base := "def inside(value: u8) -> bool:\n    return value in 1 ..= 9\n"
	session := NewIncrementalAnalysisSession()
	for revision, source := range []string{base, strings.Replace(base, "..= 9", "..= 300", 1), base} {
		cold := Analyze(parseIncrementalSessionFile(t, source))
		before := session.Stats()
		warm := session.Analyze(parseIncrementalSessionFile(t, source), AnalyzeOptions{})
		assertIncrementalResultParity(t, cold, warm)
		if revision == 1 && (len(warm.Errors()) == 0 || session.Stats().PrimaryBodiesChecked-before.PrimaryBodiesChecked != 1) {
			t.Fatal("invalid bound bypassed cold diagnostics")
		}
		if revision == 2 && (len(warm.Errors()) != 0 || session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1) {
			t.Fatal("valid range did not recover its retained typing")
		}
	}
}

func TestRangePredicateTypeMarkersStayOnRangeContainers(t *testing.T) {
	fn := parseProductFunction(t, "def inside(value: int) -> bool:\n    return value in 1 ..= 9\n")
	nodes, ok := predicateReturnExpressions(fn)
	if !ok || !predicateTypingMatchesNodes(nodes, "bnRnn") {
		t.Fatal("valid range product rejected")
	}
	for _, encoded := range []string{"bnnnn", "bRnnn", "bnnRn", "bnRRn", "RnRnn", "bnR?n"} {
		if predicateTypingMatchesNodes(nodes, encoded) {
			t.Fatalf("accepted misplaced or invalid range marker %q", encoded)
		}
	}
	if validPredicateTyping("bnRnn") {
		t.Fatal("range-only marker entered legacy predicate protocol")
	}
}

func TestIncrementalRangePredicateRechecksCurrentPostcondition(t *testing.T) {
	const source = "def inside(value: int) -> bool:\n    ensure 1 == 1\n    return value in 1 ..= 9\n"
	for _, smt := range []bool{false, true} {
		session := NewIncrementalAnalysisSession()
		options := AnalyzeOptions{EnableSMT: smt, EnforceStrictProofs: true}
		for revision, current := range []string{source, source, strings.Replace(source, "ensure 1 == 1", "ensure 1 == 2", 1), source} {
			cold := AnalyzeWithOptions(parseIncrementalSessionFile(t, current), options)
			before := session.Stats()
			warm := session.Analyze(parseIncrementalSessionFile(t, current), options)
			assertIncrementalResultParity(t, cold, warm)
			if revision == 2 && len(warm.Errors()) == 0 {
				t.Fatal("cached range bypassed current strict postcondition checking")
			}
			if revision != 2 && len(warm.Errors()) != 0 {
				t.Fatal(warm.Errors())
			}
			if revision > 0 && session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
				t.Fatal("unchanged range body typing was not reused independently of its contract")
			}
		}
	}
}
