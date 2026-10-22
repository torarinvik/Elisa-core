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
	for _, tc := range []struct {
		name string
		expr ast.Expr
	}{
		{"ident", &ast.Ident{Name: "value"}},
		{"field", &ast.FieldExpr{Object: &ast.FieldExpr{Object: &ast.Ident{Name: "value"}, Field: "state"}, Field: "count"}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			expr := tc.expr
			idx := &storeScanDeclIndex{exprTokens: map[ast.Expr][]string{expr: storeScanMentionTokens(expr)}}
			left, right := map[string]bool{"target": true}, map[string]bool{"source": true}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				a, c := idx.mentionsTwo(expr, left, right)
				storeScanMentionsSink = a || c
			}
		})
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

func TestStoreScanSimpleNamesMatchReflectiveMembership(t *testing.T) {
	deep := ast.Expr(&ast.Ident{Name: "value"})
	for i := 0; i < 32; i++ {
		deep = &ast.FieldExpr{Object: deep, Field: "ignored"}
	}
	cycle := &ast.FieldExpr{Field: "ignored"}
	cycle.Object = cycle
	var nilIdent *ast.Ident
	expressions := []ast.Expr{nil, nilIdent, &ast.Ident{Name: "value"}, &ast.Ident{Name: "pkg.value"}, &ast.Ident{Name: ""}, &ast.FieldExpr{Object: &ast.Ident{Name: "holder"}, Field: "ignored"}, deep, cycle, &ast.IndexExpr{Object: &ast.Ident{Name: "holder"}, Index: &ast.Ident{Name: "offset"}}, &ast.IntLit{Value: "value"}}
	sets := []map[string]bool{nil, {}, {"value": true}, {"value": false}, {"pkg": true}, {"pkg.value": true}, {"holder": true}, {"offset": true}, {"ignored": true}, {"": true}}
	idx := &storeScanDeclIndex{exprTokens: map[ast.Expr][]string{}}
	for _, expr := range expressions {
		for _, left := range sets {
			wantLeft := storeScanMentions(expr, left)
			if idx.mentions(expr, left) != wantLeft {
				t.Fatalf("single-set fast path differs for %T, %v", expr, left)
			}
			for _, right := range sets {
				a, b := idx.mentionsTwo(expr, left, right)
				if a != wantLeft || b != storeScanMentions(expr, right) {
					t.Fatalf("paired fast path differs for %T", expr)
				}
			}
		}
	}
}

// Reference lookup for performance controls: the pre-fast-path implementation.
func storeScanCachedPairReference(idx *storeScanDeclIndex, expr ast.Expr, left, right map[string]bool) (bool, bool) {
	if expr == nil || (len(left) == 0 && len(right) == 0) {
		return false, false
	}
	tokens, ok := idx.exprTokens[expr]
	if !ok {
		tokens = storeScanMentionTokens(expr)
		idx.exprTokens[expr] = tokens
	}
	a, c := false, false
	for _, token := range tokens {
		a = a || left[token]
		c = c || right[token]
		if (a || len(left) == 0) && (c || len(right) == 0) {
			break
		}
	}
	return a, c
}

func BenchmarkStoreScanLookupStrategies(b *testing.B) {
	for _, tc := range []struct {
		name string
		expr ast.Expr
	}{
		{"ident", &ast.Ident{Name: "value"}},
		{"field", &ast.FieldExpr{Object: &ast.FieldExpr{Object: &ast.Ident{Name: "value"}, Field: "state"}, Field: "count"}},
		{"index", &ast.IndexExpr{Object: &ast.Ident{Name: "value"}, Index: &ast.Ident{Name: "offset"}}},
	} {
		for _, cold := range []bool{false, true} {
			for _, fast := range []bool{false, true} {
				state, strategy := "warm", "cached"
				if cold {
					state = "cold"
				}
				if fast {
					strategy = "simple"
				}
				b.Run(tc.name+"/"+state+"/"+strategy, func(b *testing.B) {
					expr := tc.expr
					idx := &storeScanDeclIndex{exprTokens: map[ast.Expr][]string{expr: storeScanMentionTokens(expr)}}
					left, right := map[string]bool{"target": true}, map[string]bool{"source": true}
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if cold {
							delete(idx.exprTokens, expr)
						}
						var a, c bool
						if fast {
							a, c = idx.mentionsTwo(expr, left, right)
						} else {
							a, c = storeScanCachedPairReference(idx, expr, left, right)
						}
						storeScanMentionsSink = a || c
					}
				})
			}
		}
	}
}
