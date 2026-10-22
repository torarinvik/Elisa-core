package semantic

import (
	"fmt"
	"testing"
)

func TestReturnBorrowScopeSnapshotKeepsShadowingAndOwnsBindings(t *testing.T) {
	global := NewScope(nil)
	global.Define(&Symbol{Name: "global"})
	outer := NewScope(global)
	outer.Define(&Symbol{Name: "shadowed"})
	inner := NewScope(outer)
	shadow := &Symbol{Name: "shadowed"}
	inner.Define(shadow)
	for i := 0; i < 24; i++ {
		outer.Define(&Symbol{Name: fmt.Sprintf("outer%d", i)})
	}
	a := &Analyzer{currentScope: inner, globalScope: global}
	snapshot, ok := a.snapshotReturnBorrowScope()
	if !ok || snapshot.Parent != global || len(snapshot.Symbols) != 25 || snapshot.Symbols["shadowed"] != shadow {
		t.Fatal("snapshot lost lexical shadowing")
	}
	outer.Define(&Symbol{Name: "later"})
	delete(inner.Symbols, "shadowed")
	if snapshot.Symbols["shadowed"] != shadow || snapshot.Symbols["later"] != nil {
		t.Fatal("snapshot bindings alias the live scope maps")
	}
	if snapshot.symbolsVersion != 25 {
		t.Fatalf("snapshot version = %d", snapshot.symbolsVersion)
	}
	a.currentScope = NewScope(nil)
	if _, ok := a.snapshotReturnBorrowScope(); ok {
		t.Fatal("scope outside the analyzer's global chain was accepted")
	}
	deep := global
	for i := 0; i < 12; i++ {
		deep = NewScope(deep)
		deep.Define(&Symbol{Name: fmt.Sprintf("deep%d", i)})
	}
	a.currentScope = deep
	if snapshot, ok := a.snapshotReturnBorrowScope(); !ok || len(snapshot.Symbols) != 12 || snapshot.Parent != global {
		t.Fatal("deep scope chain lost bindings after overflowing the inline stack")
	}
}

func BenchmarkReturnBorrowScopeSnapshot(b *testing.B) {
	for _, shadowed := range []bool{false, true} {
		name := "distinct"
		want := 80
		if shadowed {
			name = "shadowed"
			want = 20
		}
		b.Run(name, func(b *testing.B) {
			global := NewScope(nil)
			current := global
			for level := 0; level < 4; level++ {
				current = NewScope(current)
				for i := 0; i < 20; i++ {
					name := fmt.Sprintf("name%d_%d", level, i)
					if shadowed {
						name = fmt.Sprintf("name%d", i)
					}
					current.Define(&Symbol{Name: name})
				}
			}
			a := &Analyzer{currentScope: current, globalScope: global}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				snapshot, ok := a.snapshotReturnBorrowScope()
				if !ok || len(snapshot.Symbols) != want {
					b.Fatal("bad snapshot")
				}
			}
		})
	}
}
