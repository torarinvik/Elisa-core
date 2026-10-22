package semantic

import (
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func storageViewBodyScanFixture(tb testing.TB) *ast.FuncDecl {
	tb.Helper()
	source := "def work(value: i64) -> i64:\n    total: i64 = " + strings.Repeat("(value + 1) + ", 255) + "(value + 1)\n    return total\n"
	l := lexer.New("write_scan.elisa", []byte(source))
	p := parser.New(l.Tokenize())
	file := p.ParseFile("write_scan.elisa")
	if len(l.Errors())+len(p.Errors()) != 0 {
		tb.Fatal("invalid scan fixture")
	}
	return file.Decls[0].(*ast.FuncDecl)
}

func TestStorageViewBodyScanPreservesNamesAndRelevantOrder(t *testing.T) {
	decl := storageViewBodyScanFixture(t)
	var raw []any
	fillMayAdoptWalk(reflect.ValueOf(decl.Body), func(node any) { raw = append(raw, node) })
	got := storageViewScanBody(decl)
	if !reflect.DeepEqual(got.locals, storageViewDeclLocalNames(decl, raw)) || !got.locals["total"] || !got.locals["value"] {
		t.Fatal("streaming scan lost a local or parameter name")
	}
	var relevant []any
	for _, node := range raw {
		switch node.(type) {
		case ast.Stmt, *ast.CallExpr, *ast.AddrOfExpr, *ast.FieldExpr, *ast.IndexExpr, *ast.Ident:
			relevant = append(relevant, node)
		}
	}
	if !reflect.DeepEqual(got.nodes, relevant) {
		t.Fatal("write scan changed node order or identity")
	}
	if len(got.nodes) >= len(raw) {
		t.Fatal("irrelevant expression nodes were retained")
	}
}

func BenchmarkStorageViewBodyScan(b *testing.B) {
	decl := storageViewBodyScanFixture(b)
	for _, mode := range []string{"all_nodes", "relevant_nodes"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var body storageViewSolveBody
				if mode == "all_nodes" {
					fillMayAdoptWalk(reflect.ValueOf(decl.Body), func(node any) { body.nodes = append(body.nodes, node) })
					body.locals = storageViewDeclLocalNames(decl, body.nodes)
				} else {
					body = storageViewScanBody(decl)
				}
				if !body.locals["total"] {
					b.Fatal("local missing")
				}
			}
		})
	}
}
