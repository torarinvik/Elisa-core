package semantic

import (
	"fmt"
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func TestStoreScanHeaderMentionsTwo(t *testing.T) {
	stmt := &ast.IfStmt{Cond: &ast.BinaryExpr{Left: &ast.Ident{Name: "left"}, Right: &ast.Ident{Name: "right"}}}
	sets := []map[string]bool{nil, {"left": true}, {"right": true}, {"left": false}, {"other": true}}
	for _, left := range sets {
		for _, right := range sets {
			idx := &storeScanDeclIndex{}
			l, r := idx.headerMentionsTwo(stmt, left, right)
			if l != idx.headerMentions(stmt, left) || r != idx.headerMentions(stmt, right) {
				t.Fatalf("different results for %v and %v", left, right)
			}
		}
	}
}

func BenchmarkStoreScanHeaderMentionsTwo(b *testing.B) {
	stmt := &ast.IfStmt{Cond: &ast.BinaryExpr{Left: &ast.Ident{Name: "left"}, Right: &ast.Ident{Name: "right"}}}
	left, right := map[string]bool{"left": true}, map[string]bool{"right": true}
	for _, combined := range []bool{false, true} {
		name := "separate"
		if combined {
			name = "combined"
		}
		b.Run(name, func(b *testing.B) {
			idx := &storeScanDeclIndex{}
			idx.headerMentionsTwo(stmt, left, right)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if combined {
					idx.headerMentionsTwo(stmt, left, right)
				} else {
					idx.headerMentions(stmt, left)
					idx.headerMentions(stmt, right)
				}
			}
		})
	}
}

func TestStoreScanIndexedHeaderFastPath(t *testing.T) {
	id := func(name string) *ast.Ident { return &ast.Ident{Name: name} }
	cycle := &ast.ParenExpr{}
	cycle.Inner = cycle
	for _, expr := range []ast.Expr{
		cycle,
		&ast.AddrOfExpr{Operand: id("values")},
		&ast.MoveExpr{Operand: id("values")},
		&ast.UnaryExpr{Operand: id("other"), LoweredCall: &ast.CallExpr{Func: id("values")}},
		&ast.UnaryExpr{Operand: id("values")},
		&ast.ParenExpr{Inner: &ast.ParenExpr{Inner: id("values")}},
		&ast.ParenExpr{Inner: &ast.FieldExpr{Object: id("values"), Field: "count"}},
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

func BenchmarkStoreScanHeaderUnary(b *testing.B) {
	expr := &ast.UnaryExpr{Operand: &ast.Ident{Name: "values"}}
	names := map[string]bool{"values": true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		storeScanMentionsSink = storeScanMentionsHeader(expr, names)
	}
}

func BenchmarkStoreScanHeaderAddress(b *testing.B) {
	expr := &ast.AddrOfExpr{Operand: &ast.Ident{Name: "values"}}
	names := map[string]bool{"values": true}
	for _, direct := range []bool{false, true} {
		name := "reflective"
		if direct {
			name = "direct"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if direct {
					storeScanMentionsSink = storeScanMentionsHeader(expr, names)
				} else {
					storeScanMentionsSink = storeScanMentionsHeaderPooled(expr, names)
				}
			}
		})
	}
}

func BenchmarkStoreScanHeaderParenthesized(b *testing.B) {
	var expr ast.Expr = &ast.Ident{Name: "values"}
	for i := 0; i < 4; i++ {
		expr = &ast.ParenExpr{Inner: expr}
	}
	names := map[string]bool{"values": true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		storeScanMentionsSink = storeScanMentionsHeader(expr, names)
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

func BenchmarkStoreScanHeaderCompound(b *testing.B) {
	var expr ast.Expr = &ast.FieldExpr{Object: &ast.Ident{Name: "values"}, Field: "count"}
	for range 16 {
		expr = &ast.BinaryExpr{Left: expr, Right: &ast.Ident{Name: "other"}}
	}
	names := map[string]bool{"values": true}
	for _, pooled := range []bool{false, true} {
		label := "fresh"
		if pooled {
			label = "pooled"
		}
		b.Run(label, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var seen map[uintptr]bool
				if pooled {
					seen = borrowStoreScanSeen()
				} else {
					seen = map[uintptr]bool{}
				}
				storeScanMentionsSink = storeScanMentionsHeaderWalk(reflect.ValueOf(expr), names, seen)
				if pooled {
					releaseStoreScanSeen(seen)
				}
			}
		})
	}
}

func BenchmarkStoreScanHeaderMapCapacity(b *testing.B) {
	var expr ast.Expr = &ast.Ident{Name: "other"}
	for i := 0; i < 16; i++ {
		expr = &ast.BinaryExpr{Left: expr, Right: &ast.Ident{Name: "other"}}
	}
	names := map[string]bool{"values": true}
	for _, size := range []int{32, 8192} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			seen := make(map[uintptr]bool, size)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				storeScanMentionsSink = storeScanMentionsHeaderWalk(reflect.ValueOf(expr), names, seen)
				clear(seen)
			}
		})
	}
}

func TestStoreScanHeaderLiteralFastPath(t *testing.T) {
	var nilInt *ast.IntLit
	var nilFloat *ast.FloatLit
	var nilString *ast.StringLit
	var nilChar *ast.CharLit
	cases := []any{nilInt, nilFloat, nilString, nilChar, &ast.IntLit{Value: "source", Suffix: "u32"}, &ast.FloatLit{Value: "1.0", Suffix: "source"}, &ast.StringLit{Value: "source"}, &ast.CharLit{Value: "x"}, &ast.BoolLit{Value: true}, &ast.NullLit{}, &ast.ZeroedLit{}}
	for _, node := range cases {
		for _, names := range []map[string]bool{{"source": true}, {"u32": true}, {"": true}, {"other": true}} {
			got := storeScanMentionsHeader(node, names)
			want := storeScanMentionsHeaderWalk(reflect.ValueOf(node), names, map[uintptr]bool{})
			if got != want {
				t.Fatalf("%T %v: got %v want %v", node, names, got, want)
			}
		}
	}
}

func BenchmarkStoreScanHeaderLiteral(b *testing.B) {
	node := &ast.IntLit{Value: "1", Suffix: "u32"}
	names := map[string]bool{"source": true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if storeScanMentionsHeader(node, names) {
			b.Fatal("unexpected match")
		}
	}
}

func BenchmarkStoreScanHeaderOtherLiterals(b *testing.B) {
	nodes := []any{&ast.FloatLit{Value: "1.0"}, &ast.StringLit{Value: "text"}, &ast.CharLit{Value: "x"}, &ast.BoolLit{Value: true}, &ast.NullLit{}, &ast.ZeroedLit{}}
	names := map[string]bool{"source": true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, node := range nodes {
			storeScanMentionsSink = storeScanMentionsHeader(node, names)
		}
	}
}
