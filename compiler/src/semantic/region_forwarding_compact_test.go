package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func forwardingFixture(t testing.TB) (*ast.FuncDecl, *ast.FuncDecl) {
	t.Helper()
	source := "def sink(value: i64):\n    pass\ndef caller(value: i64):\n" + strings.Repeat("    sink(value)\n    sink(local)\n", 128)
	l := lexer.New("forward.elisa", []byte(source))
	p := parser.New(l.Tokenize())
	file := p.ParseFile("forward.elisa")
	if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
		t.Fatal("invalid forwarding fixture")
	}
	return file.Decls[0].(*ast.FuncDecl), file.Decls[1].(*ast.FuncDecl)
}

func TestRegionForwardingIgnoresLocalsAndDuplicateTargets(t *testing.T) {
	sink, caller := forwardingFixture(t)
	forwarding := collectRegionParamForwarding([]*ast.FuncDecl{caller}, map[string]*ast.FuncDecl{"sink": sink})[caller]
	if targets := forwarding.direct["value"]; len(targets) != 1 || targets[0].callee != sink || targets[0].argPos != 0 {
		t.Fatalf("unexpected forwarding targets: %#v", targets)
	}
	if _, ok := forwarding.direct["local"]; ok {
		t.Fatal("local name entered parameter forwarding")
	}
}

func BenchmarkRegionForwardingRepeatedCalls(b *testing.B) {
	sink, caller := forwardingFixture(b)
	cands := []*ast.FuncDecl{caller}
	byName := map[string]*ast.FuncDecl{"sink": sink}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		collectRegionParamForwarding(cands, byName)
	}
}
