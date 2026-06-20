//go:build cgo

package backend

import (
	"os/exec"
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
)

// parseAndAnalyzeBackendTestWithSMT mirrors parseAndAnalyzeBackendTest but enables the SMT discharge
// tier, which a `lemma` (and hence an `assert … by:` proof block) needs to verify its `ensure`.
func parseAndAnalyzeBackendTestWithSMT(t *testing.T, filename, src string) *semantic.Result {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; assert-by erasure test skipped")
	}
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		t.Fatalf("lexer errors:\n%s", strings.Join(errs, "\n"))
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parse errors:\n%s", strings.Join(errs, "\n"))
	}
	result := semantic.AnalyzeWithOptions(file, semantic.AnalyzeOptions{EnableSMT: true})
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("semantic errors:\n%s", strings.Join(errs, "\n"))
	}
	return result
}

// ERASURE: the `by:` proof block of an `assert … by:` is verification-only and must not appear in the
// generated LLVM IR. The lemma call inside the block is ghost code; emitting it would call an erased
// function. We confirm the block's lemma is never invoked in the IR for `use`.
func TestAssertByProofBlockErasedFromLLVM(t *testing.T) {
	result := parseAndAnalyzeBackendTestWithSMT(t, "assert_by_erase.elisa", `lemma weaken(x: i64):
	requires x >= 10
	ensure x >= 5
	pass

def use(n: i64) -> i64:
	assert n >= 5 by:
		assert(n >= 10)
		weaken(n)
	return n
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	// The ghost lemma from the proof block must never be invoked in the IR (the block is erased).
	if strings.Contains(output, "@weaken") {
		t.Fatalf("expected the proof-block lemma to be erased from IR, but @weaken appears:\n%s", output)
	}
}
