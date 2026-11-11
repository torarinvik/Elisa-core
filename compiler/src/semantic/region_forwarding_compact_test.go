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
	callers := make(map[*ast.FuncDecl][]*ast.FuncDecl)
	forwarding := collectRegionParamForwarding([]*ast.FuncDecl{caller}, map[string]*ast.FuncDecl{"sink": sink}, callers)[caller]
	if dependencies := callers[sink]; len(dependencies) != 1 || dependencies[0] != caller {
		t.Fatalf("unexpected reverse dependencies: %v", dependencies)
	}
	if targets := forwarding.direct["value"]; len(targets) != 1 || targets[0].callee != sink || targets[0].argPos != 0 {
		t.Fatalf("unexpected forwarding targets: %#v", targets)
	}
	if _, ok := forwarding.direct["local"]; ok {
		t.Fatal("local name entered parameter forwarding")
	}
}

func TestRegionForwardingTracksReferencesWithoutParameters(t *testing.T) {
	sink, _ := forwardingFixture(t)
	file := retainedParse(t, "def callback():\n    consume((sink))\n")
	caller := file.Decls[0].(*ast.FuncDecl)
	callers := make(map[*ast.FuncDecl][]*ast.FuncDecl)
	collectRegionParamForwarding([]*ast.FuncDecl{caller}, map[string]*ast.FuncDecl{"sink": sink}, callers)
	if dependencies := callers[sink]; len(dependencies) != 1 || dependencies[0] != caller {
		t.Fatalf("unexpected callback dependencies: %v", dependencies)
	}
}

func BenchmarkRegionForwardingRepeatedCalls(b *testing.B) {
	sink, caller := forwardingFixture(b)
	cands := []*ast.FuncDecl{caller}
	byName := map[string]*ast.FuncDecl{"sink": sink}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		collectRegionParamForwarding(cands, byName, make(map[*ast.FuncDecl][]*ast.FuncDecl))
	}
}
