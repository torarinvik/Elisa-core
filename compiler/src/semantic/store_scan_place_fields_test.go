package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestStoreScanParamWriteNodesPreserveRelevantOrder(t *testing.T) {
	ident := &ast.Ident{Name: "root"}
	assign := &ast.AssignStmt{Target: ident}
	call := &ast.CallExpr{}
	nodes := []any{ident, &ast.ReturnStmt{Value: ident}, assign, call}
	idx := &storeScanDeclIndex{flat: nodes}
	got := idx.paramWriteNodes()
	if !reflect.DeepEqual(got, []any{assign, call}) {
		t.Fatalf("relevant nodes = %#v", got)
	}
}

func TestStoreScanTouchedLocalsReadsUpdatedCallArgs(t *testing.T) {
	call := &ast.CallExpr{Args: []ast.Expr{&ast.Ident{Name: "before"}}}
	idx := &storeScanDeclIndex{flat: []any{call}}
	if !storeScanTouchedLocals(idx)["before"] {
		t.Fatal("missing original argument")
	}
	call.Args = []ast.Expr{&ast.Ident{Name: "after"}}
	got := storeScanTouchedLocals(idx)
	if !got["after"] || got["before"] {
		t.Fatalf("did not inspect updated arguments: %v", got)
	}
}

func TestStoreScanPlaceMentionsEitherMatchesSeparate(t *testing.T) {
	idx := &storeScanDeclIndex{}
	nodes := []any{&ast.AssignStmt{Target: &ast.Ident{Name: "left"}}, &ast.AssignStmt{Target: &ast.Ident{Name: "right"}}, &ast.CallExpr{}, &ast.ReturnStmt{}}
	sets := []map[string]bool{nil, {"left": true}, {"right": true}, {"left": false}}
	for _, node := range nodes {
		for _, left := range sets {
			for _, right := range sets {
				want := storeScanNodePlaceMentions(idx, node, left) || storeScanNodePlaceMentions(idx, node, right)
				if got := storeScanNodePlaceMentionsEither(idx, node, left, right); got != want {
					t.Fatalf("%T %v %v: got %v, want %v", node, left, right, got, want)
				}
			}
		}
	}
}

func BenchmarkStoreScanPlaceFields(b *testing.B) {
	idx := &storeScanDeclIndex{}
	names := map[string]bool{"root": true}
	nodes := []any{&ast.Ident{Name: "root"}, &ast.ReturnStmt{Value: &ast.Ident{Name: "root"}}, &ast.AssignStmt{Target: &ast.Ident{Name: "root"}}, &ast.CallExpr{}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, node := range nodes {
			_ = storeScanNodePlaceMentions(idx, node, names)
		}
	}
}

func BenchmarkStoreScanOrdinaryCallConstructorCheck(b *testing.B) {
	a := &Analyzer{}
	decl := &ast.FuncDecl{Name: "caller"}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "callee"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		storeScanMentionsSink = a.storeScanIsEnumConstructor(decl, call)
	}
}

func BenchmarkStoreScanPlaceMentionsEither(b *testing.B) {
	idx := &storeScanDeclIndex{}
	node := &ast.AssignStmt{Target: &ast.FieldExpr{Object: &ast.Ident{Name: "other"}, Field: "value"}}
	left, right := map[string]bool{"left": true}, map[string]bool{"right": true}
	for _, combined := range []bool{false, true} {
		name := "separate"
		if combined {
			name = "combined"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if combined {
					storeScanMentionsSink = storeScanNodePlaceMentionsEither(idx, node, left, right)
				} else {
					storeScanMentionsSink = storeScanNodePlaceMentions(idx, node, left) || storeScanNodePlaceMentions(idx, node, right)
				}
			}
		})
	}
}

func BenchmarkStoreScanTouchedLocals(b *testing.B) {
	idx := &storeScanDeclIndex{flat: make([]any, 256)}
	for i := range idx.flat {
		idx.flat[i] = &ast.ReturnStmt{Value: &ast.Ident{Name: "x"}}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = storeScanTouchedLocals(idx)
	}
}
