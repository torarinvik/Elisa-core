//go:build cgo

package semantic

import (
	"crypto/sha256"
	"testing"

	"elisacore/src/ast"
)

const incrementalEnsureMemoProofSource = `def commute(x: i64, y: i64) -> i64:
    ensure x * y == y * x
    return x + y
`

func TestIncrementalEnsureProofMemoPositiveProofIsReused(t *testing.T) {
	options := AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true}
	session := NewIncrementalAnalysisSession()
	first := session.Analyze(parseIncrementalSessionFile(t, incrementalEnsureMemoProofSource), options)
	if errs := first.Errors(); len(errs) != 0 {
		t.Fatalf("expected the scalar commutativity ensure to prove, got: %v", errs)
	}
	afterFirst := session.Stats()
	if afterFirst.EnsureProofMemoPublished != 1 || afterFirst.EnsureProofMemoHits != 0 {
		t.Fatalf("expected one cold positive proof publication, got %+v", afterFirst)
	}

	// A new parse has new AST pointers and source positions, but the same syntax and proof inputs.
	shifted := "\n\n" + incrementalEnsureMemoProofSource
	warm := session.Analyze(parseIncrementalSessionFile(t, shifted), options)
	if errs := warm.Errors(); len(errs) != 0 {
		t.Fatalf("warm analysis changed proof result: %v", errs)
	}
	afterWarm := session.Stats()
	if afterWarm.EnsureProofMemoHits-afterFirst.EnsureProofMemoHits != 1 ||
		afterWarm.EnsureProofMemoPublished != afterFirst.EnsureProofMemoPublished {
		t.Fatalf("unchanged proof was not reused exactly once: first=%+v warm=%+v", afterFirst, afterWarm)
	}
}

func TestIncrementalEnsureProofMemoKeyTracksEveryInput(t *testing.T) {
	tree := sha256.Sum256([]byte("tree"))
	base := incrementalEnsureProofKey("owner", tree, 0, "config", "safety", "z3", 500_000)
	if got := incrementalEnsureProofKey("owner", tree, 0, "config", "safety", "z3", 500_000); got != base {
		t.Fatal("identical proof inputs produced different keys")
	}
	treeChanged := sha256.Sum256([]byte("edited tree"))
	cases := []struct {
		name string
		key  string
	}{
		{"owner", incrementalEnsureProofKey("other-owner", tree, 0, "config", "safety", "z3", 500_000)},
		{"syntax tree", incrementalEnsureProofKey("owner", treeChanged, 0, "config", "safety", "z3", 500_000)},
		{"clause", incrementalEnsureProofKey("owner", tree, 1, "config", "safety", "z3", 500_000)},
		{"configuration", incrementalEnsureProofKey("owner", tree, 0, "other-config", "safety", "z3", 500_000)},
		{"safety policy", incrementalEnsureProofKey("owner", tree, 0, "config", "other-safety", "z3", 500_000)},
		{"solver", incrementalEnsureProofKey("owner", tree, 0, "config", "safety", "other-z3", 500_000)},
		{"resource limit", incrementalEnsureProofKey("owner", tree, 0, "config", "safety", "z3", 500_001)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.key == base {
				t.Fatalf("changing %s did not invalidate the proof key", tc.name)
			}
		})
	}
}

func TestIncrementalEnsureProofMemoOnlyStagesPositiveResults(t *testing.T) {
	for _, outcome := range []string{"counterexample", "unknown", "solver error"} {
		t.Run(outcome, func(t *testing.T) {
			fn := parseIncrementalSessionFile(t, incrementalEnsureMemoProofSource).Decls[0].(*ast.FuncDecl)
			tree, _, err := functionSyntaxFingerprint(fn)
			if err != nil {
				t.Fatal(err)
			}
			session := &IncrementalAnalysisSession{ensureProofs: map[string]struct{}{}}
			invocation := &incrementalBodyInvocation{
				session: session,
				ensureProofInputs: map[*ast.FuncDecl]incrementalEnsureProofInput{
					fn: {tree: tree, keys: map[int]string{0: "positive-key"}},
				},
			}

			// trySMTProveRequires reduces counterexamples, unknown, and solver errors to proven=false.
			invocation.recordEnsureProof(fn, 0, false)
			invocation.publishEnsureProofs()
			if session.hasEnsureProof("positive-key") || session.stats.EnsureProofMemoPublished != 0 {
				t.Fatalf("%s result was retained as a positive proof", outcome)
			}
		})
	}
}

func TestIncrementalEnsureProofMemoRejectsStaleSyntaxBeforePublish(t *testing.T) {
	fn := parseIncrementalSessionFile(t, incrementalEnsureMemoProofSource).Decls[0].(*ast.FuncDecl)
	tree, _, err := functionSyntaxFingerprint(fn)
	if err != nil {
		t.Fatal(err)
	}
	session := &IncrementalAnalysisSession{ensureProofs: map[string]struct{}{}}
	invocation := &incrementalBodyInvocation{
		session: session,
		ensureProofInputs: map[*ast.FuncDecl]incrementalEnsureProofInput{
			fn: {tree: tree, keys: map[int]string{0: "positive-key"}},
		},
	}
	invocation.recordEnsureProof(fn, 0, true)

	// A mutation between recording the positive verdict and end-of-analysis publication must drop it.
	fn.Body[len(fn.Body)-1].(*ast.ReturnStmt).Value = fn.EnsureValues[0]
	invocation.publishEnsureProofs()
	if session.hasEnsureProof("positive-key") || session.stats.EnsureProofMemoPublished != 0 {
		t.Fatal("stale syntax was published as a reusable proof")
	}
}

func TestIncrementalEnsureProofMemoEligibilityRequiresStrictSMT(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options AnalyzeOptions
	}{
		{"smt disabled", AnalyzeOptions{EnforceStrictProofs: true}},
		{"strict proofs disabled", AnalyzeOptions{EnableSMT: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := NewIncrementalAnalysisSession()
			for range 2 {
				session.Analyze(parseIncrementalSessionFile(t, incrementalEnsureMemoProofSource), tc.options)
			}
			stats := session.Stats()
			if stats.EnsureProofMemoPublished != 0 || stats.EnsureProofMemoHits != 0 {
				t.Fatalf("ineligible proof context used the memo: %+v", stats)
			}
		})
	}
}
