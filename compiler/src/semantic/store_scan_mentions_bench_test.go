package semantic

import (
	"elisacore/src/ast"
	"testing"
)

var storeScanMentionsSink bool
var storeScanTokensSink []string

func BenchmarkStoreScanIdentifierTokens(b *testing.B) {
	for _, name := range []string{"value", "pkg.value"} {
		b.Run(name, func(b *testing.B) {
			expr := &ast.Ident{Name: name}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				storeScanTokensSink = storeScanMentionTokens(expr)
			}
		})
	}
}

func BenchmarkStoreScanTwoNameSets(b *testing.B) {
	expr := &ast.Ident{Name: "value"}
	idx := &storeScanDeclIndex{exprTokens: map[ast.Expr][]string{expr: storeScanMentionTokens(expr)}}
	left, right := map[string]bool{"target": true}, map[string]bool{"source": true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a, c := idx.mentionsTwo(expr, left, right)
		storeScanMentionsSink = a || c
	}
}

func TestStoreScanMentionTokensPreserveNameMembership(t *testing.T) {
	expr := &ast.CallExpr{Func: &ast.Ident{Name: "pkg.func"}, Args: []ast.Expr{&ast.Ident{Name: "value"}, &ast.FieldExpr{Object: &ast.Ident{Name: "holder"}, Field: "ignored"}}}
	tokens := storeScanMentionTokens(expr)
	for _, name := range []string{"pkg.func", "pkg", "value", "holder", "ignored", "unknown"} {
		for _, enabled := range []bool{false, true} {
			names := map[string]bool{name: enabled}
			if got, want := storeScanTokensHit(tokens, names), storeScanMentions(expr, names); got != want {
				t.Fatalf("membership %q/%v: got %v want %v", name, enabled, got, want)
			}
		}
	}
}

func TestStoreScanTwoNameSetsMatchesIndependentQueries(t *testing.T) {
	expr := &ast.CallExpr{Func: &ast.Ident{Name: "pkg.func"}, Args: []ast.Expr{&ast.Ident{Name: "value"}}}
	idx := &storeScanDeclIndex{exprTokens: map[ast.Expr][]string{}}
	sets := []map[string]bool{nil, {}, {"pkg": true}, {"value": true}, {"value": false}, {"absent": true}, {"pkg.func": true, "value": true}}
	for _, left := range sets {
		for _, right := range sets {
			gotLeft, gotRight := idx.mentionsTwo(expr, left, right)
			if gotLeft != idx.mentions(expr, left) || gotRight != idx.mentions(expr, right) {
				t.Fatal("paired membership differs from independent queries")
			}
		}
	}
	if a, b := idx.mentionsTwo(nil, sets[2], sets[3]); a || b {
		t.Fatal("nil expression mentions a name")
	}
}
