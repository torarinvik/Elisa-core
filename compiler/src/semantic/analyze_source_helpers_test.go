//go:build cgo

package semantic

import (
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"testing"
)

// analyzeTreeTestSource / analyzeTreeTestSourceWithSemanticErrors are the shared
// lex→parse→analyze test harness helpers (the "tree" in the name is historical —
// they predate the docs/81 tree retirement and are used across the semantic suite).
func analyzeTreeTestSource(t *testing.T, filename string, src string) *Result {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	result := Analyze(file)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	return result
}
func analyzeTreeTestSourceWithSemanticErrors(t *testing.T, filename string, src string) *Result {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	return Analyze(file)
}
