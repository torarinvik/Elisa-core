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

func TestStorageViewNodePoolClearsUnusedCapacity(t *testing.T) {
	nodes := make([]any, 8)
	for i := range nodes {
		nodes[i] = &ast.Ident{Name: "retained"}
	}
	releaseStorageViewNodes(nodes[:1])
	for i, node := range nodes {
		if node != nil {
			t.Fatalf("node %d retained a reference", i)
		}
	}
}

func TestStorageViewHandledPoolClearsPreviousWalk(t *testing.T) {
	handled := borrowStorageViewHandled()
	ident := &ast.Ident{Name: "value"}
	handled[ident] = true
	releaseStorageViewHandled(handled)
	if len(handled) != 0 {
		t.Fatal("handled identifiers retained after release")
	}
	next := borrowStorageViewHandled()
	defer releaseStorageViewHandled(next)
	if next[ident] {
		t.Fatal("previous walk contaminated the next one")
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

func BenchmarkStorageViewBodyScanWide(b *testing.B) {
	decl := &ast.FuncDecl{Body: make([]ast.Stmt, 128)}
	for i := range decl.Body {
		decl.Body[i] = &ast.ReturnStmt{Value: &ast.Ident{Name: "x"}}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if len(storageViewScanBody(decl).nodes) != 256 {
			b.Fatal("wrong node count")
		}
	}
}

func BenchmarkStorageViewBodyScanSmallBuffer(b *testing.B) {
	decl := &ast.FuncDecl{Body: make([]ast.Stmt, 128)}
	for i := range decl.Body {
		decl.Body[i] = &ast.ReturnStmt{Value: &ast.Ident{Name: "x"}}
	}
	seed := make([]any, 0, 8)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if len(storageViewScanBodyWithNodes(decl, seed).nodes) != 256 {
			b.Fatal("wrong node count")
		}
	}
}

func BenchmarkStorageViewHandledSet(b *testing.B) {
	idents := make([]*ast.Ident, 64)
	for i := range idents {
		idents[i] = &ast.Ident{}
	}
	for _, pooled := range []bool{false, true} {
		name := "fresh"
		if pooled {
			name = "pooled"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var handled map[*ast.Ident]bool
				if pooled {
					handled = borrowStorageViewHandled()
				} else {
					handled = make(map[*ast.Ident]bool)
				}
				for _, ident := range idents {
					handled[ident] = true
				}
				if pooled {
					releaseStorageViewHandled(handled)
				}
			}
		})
	}
}

func BenchmarkStorageViewBodyScanReusable(b *testing.B) {
	decl := &ast.FuncDecl{Body: make([]ast.Stmt, 128)}
	for i := range decl.Body {
		decl.Body[i] = &ast.ReturnStmt{Value: &ast.Ident{Name: "x"}}
	}
	for _, reusable := range []bool{false, true} {
		name := "fresh"
		if reusable {
			name = "reusable"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var body storageViewSolveBody
				if reusable {
					body = storageViewScanBodyWithNodes(decl, borrowStorageViewNodes())
				} else {
					body = storageViewScanBody(decl)
				}
				if len(body.nodes) != 256 {
					b.Fatal("wrong node count")
				}
				if reusable {
					releaseStorageViewNodes(body.nodes)
				}
			}
		})
	}
}
