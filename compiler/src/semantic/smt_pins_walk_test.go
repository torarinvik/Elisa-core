package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func oldSMTFactPins(scope *Scope, expr ast.Expr, names map[string]bool) map[string]*Symbol {
	pins := make(map[string]*Symbol, len(names))
	add := func(name string) {
		if _, done := pins[name]; done {
			return
		}
		sym, _ := scope.Lookup(name)
		pins[name] = sym
	}
	for name := range names {
		add(name)
	}
	for _, name := range exprIdentNames(expr) {
		add(name)
	}
	return pins
}

func TestSMTFactPinsVisitMatchesNames(t *testing.T) {
	scope := NewScope(nil)
	expr := &ast.IndexExpr{Object: &ast.Ident{Name: "x"}, Index: &ast.Ident{Name: "i"}, Index2: &ast.Ident{Name: "x"}, Fallback: &ast.Ident{Name: "fallback"}}
	deps := map[string]bool{"x": true, "extra": true}
	a := &Analyzer{}
	if got, want := a.smtFactPinsWithDeps(scope, expr, deps), oldSMTFactPins(scope, expr, deps); !reflect.DeepEqual(got, want) {
		t.Fatalf("pins = %#v; want %#v", got, want)
	}
}

func TestSMTFactPinsLiteralHasNoPins(t *testing.T) {
	a := &Analyzer{}
	if got := a.smtFactPinsWithDeps(NewScope(nil), &ast.BinaryExpr{}, nil); len(got) != 0 {
		t.Fatalf("literal-only expression has pins: %#v", got)
	}
}

func BenchmarkSMTFactPinsWalk(b *testing.B) {
	scope := NewScope(nil)
	expr := &ast.BinaryExpr{Left: &ast.Ident{Name: "x"}, Right: &ast.Ident{Name: "y"}}
	deps := map[string]bool{"x": true, "y": true}
	a := &Analyzer{}
	for _, old := range []bool{true, false} {
		name := "direct"
		if old {
			name = "slice"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if old {
					_ = oldSMTFactPins(scope, expr, deps)
				} else {
					_ = a.smtFactPinsWithDeps(scope, expr, deps)
				}
			}
		})
	}
}

func BenchmarkSMTFactPinsNoNames(b *testing.B) {
	scope := NewScope(nil)
	expr := &ast.BinaryExpr{}
	a := &Analyzer{}
	for _, old := range []bool{true, false} {
		name := "lazy"
		if old {
			name = "eager"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if old {
					_ = oldSMTFactPins(scope, expr, nil)
				} else {
					_ = a.smtFactPinsWithDeps(scope, expr, nil)
				}
			}
		})
	}
}
