package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func TestStoreScanIndexedHeaderFastPath(t *testing.T) {
	id := func(name string) *ast.Ident { return &ast.Ident{Name: name} }
	for _, expr := range []ast.Expr{
		&ast.IndexExpr{Object: id("values"), Index: id("i")},
		&ast.IndexExpr{Object: id("values"), Index: id("values")},
		&ast.IndexExpr{Object: &ast.FieldExpr{Object: id("values"), Field: "items"}, Index: id("i")},
		&ast.IndexExpr{Object: id("values"), Index: id("i"), Index2: id("j")},
		&ast.IndexExpr{Object: id("other"), Index: id("i")},
		&ast.FieldExpr{Object: &ast.IndexExpr{Object: id("values"), Index: id("i")}, Field: "count"},
	} {
		for _, names := range []map[string]bool{{"values": true}, {"i": true}, {"other": true, "j": true}, {"values": false}} {
			got := storeScanMentionsHeader(expr, names)
			want := storeScanMentionsHeaderWalk(reflect.ValueOf(expr), names, map[uintptr]bool{})
			if got != want {
				t.Fatalf("%#v, %v: got %v want %v", expr, names, got, want)
			}
		}
	}
}

func BenchmarkStoreScanIndexedHeader(b *testing.B) {
	expr := &ast.IndexExpr{Object: &ast.Ident{Name: "values"}, Index: &ast.Ident{Name: "i"}}
	names := map[string]bool{"values": true}
	for _, fast := range []bool{false, true} {
		label := "reflective"
		if fast {
			label = "direct"
		}
		b.Run(label, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var result bool
				if fast {
					result = storeScanMentionsHeader(expr, names)
				} else {
					result = storeScanMentionsHeaderWalk(reflect.ValueOf(expr), names, map[uintptr]bool{})
				}
				if result {
					b.Fatal("index should not carry its source header")
				}
			}
		})
	}
}
