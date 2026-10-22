package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestStoreScanPayloadNodesPreserveWalkOrder(t *testing.T) {
	const source = `def leaf(value: i64) -> i64:
    return value
def probe(flag: bool, value: i64) -> i64:
    current: mutable i64 = leaf(value)
    if flag:
        current <- leaf(leaf(current))
    else:
        current <- leaf(2)
    return leaf(current)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "payload_nodes.elisa", source, AnalyzeOptions{})
	fn := storeScanFindFunc(t, result, "probe")
	idx := result.analyzer.storeScanIndex(fn)
	var want []any
	walkStoreScanNodes(fn.Body, func(node any) {
		switch node.(type) {
		case *ast.AssignStmt, *ast.CallExpr:
			want = append(want, node)
		}
	})
	if len(want) < 7 {
		t.Fatalf("fixture did not include nested calls and branch writes: %d", len(want))
	}
	for repeat := 0; repeat < 3; repeat++ {
		var got []any
		idx.eachPayloadNode(func(node any) { got = append(got, node) })
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("payload traversal changed on repeat %d", repeat)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatal("payload index copied or reordered AST nodes")
			}
		}
	}
	empty := &storeScanDeclIndex{flat: []any{&ast.Ident{Name: "value"}}}
	empty.eachPayloadNode(func(any) { t.Fatal("visited non-payload node") })
	if !empty.payloadNodesReady {
		t.Fatal("empty payload subset was not cached")
	}
}

var storeScanPayloadNodeSink int

func BenchmarkStoreScanPayloadNodeTraversal(b *testing.B) {
	// Model a declaration with many value expressions and calls. The legacy path
	// dispatches every flat node; the cached path must visit the same payloads.
	var source strings.Builder
	source.WriteString("def leaf(value: i64) -> i64:\n    return value\ndef probe(value: i64) -> i64:\n")
	for i := 0; i < 128; i++ {
		fmt.Fprintf(&source, "    x%d: i64 = leaf(value + %d)\n", i, i)
	}
	source.WriteString("    return leaf(x127)\n")
	l := lexer.New("payload_traversal.elisa", []byte(source.String()))
	tokens := l.Tokenize()
	if len(l.Errors()) != 0 {
		b.Fatal(l.Errors())
	}
	p := parser.New(tokens)
	file := p.ParseFile("payload_traversal.elisa")
	if len(p.Errors()) != 0 {
		b.Fatal(p.Errors())
	}
	fn, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		b.Fatal("missing probe")
	}
	a := &Analyzer{}
	idx := a.storeScanIndex(fn)
	visit := func(node any) {
		switch node.(type) {
		case *ast.AssignStmt, *ast.CallExpr:
			storeScanPayloadNodeSink++
		}
	}
	idx.eachPayloadNode(visit)
	b.Logf("flat nodes=%d payload nodes=%d", len(idx.flat), len(idx.payloadNodes))
	b.Run("all-nodes", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, node := range idx.flat {
				visit(node)
			}
		}
	})
	b.Run("payload-nodes", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			idx.eachPayloadNode(visit)
		}
	})
}
