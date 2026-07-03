//go:build cgo

package semantic

import (
	"strings"
	"testing"
	"time"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// A deeply-nested field access `a.b.b.b…` must analyze in LINEAR time. The FieldExpr case of analyzeExpr
// called both analyzeFieldExpr and fieldExprProvidesWritableRef on the same node, and each independently
// re-analyzed the object subtree — so every level DOUBLED the work, making a chain of depth ~40 hang for
// seconds (found by the mutating fuzzer). fieldExprProvidesWritableRef now reuses the object's cached
// type. This pins the fix: depth 400 must finish near-instantly, not exponentially.
func TestDeepFieldChainAnalyzesLinearly(t *testing.T) {
	src := "def f(a: i64) -> i64:\n    return a" + strings.Repeat(".b", 400) + "\n"
	l := lexer.New("chain.elisa", []byte(src))
	p := parser.New(l.Tokenize())
	file := p.ParseFile("chain.elisa")
	if file == nil {
		t.Fatal("parse produced no file")
	}
	done := make(chan struct{})
	go func() {
		AnalyzeWithOptions(file, AnalyzeOptions{})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("analyzing a depth-400 field chain did not finish in 3s — the exponential re-analysis regressed")
	}
}
