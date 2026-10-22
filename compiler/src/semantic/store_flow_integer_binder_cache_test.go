package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"testing"
)

func TestStoreFlowIntegerBinderCachePreservesShadows(t *testing.T) {
	idx := &storeFlowBinderIndex{
		typed: []storeFlowTypedBinder{
			{name: "number", t: &ast.NamedType{Name: "i64"}},
			{name: "shadowed", t: &ast.NamedType{Name: "usize"}},
			{name: "mixed", t: &ast.NamedType{Name: "i64"}},
			{name: "mixed", t: &ast.NamedType{Name: "sview"}},
		},
		other: []string{"shadowed"}, rangeNames: []string{"index"},
	}
	for round := 0; round < 2; round++ {
		got := idx.integerBinderNames()
		if !got["number"] || !got["index"] || got["shadowed"] || got["mixed"] || got["missing"] {
			t.Fatalf("incorrect integer classification: %v", got)
		}
	}
	// Other predicate queries still return independent, mutable result sets.
	idx.typedNames(func(ast.TypeExpr) bool { return true })["mixed"] = true
	if idx.integerBinderNames()["mixed"] {
		t.Fatal("predicate query changed integer cache")
	}
}

func TestRetainedUpdateInvalidatesChangedBinderIndex(t *testing.T) {
	const before = "def value() -> i64:\n    item: i64 = 1\n    return item + item\n"
	const after = "def value() -> i64:\n    item: sview = \"x\"\n    return item.len.i64()\n"
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if retained == nil {
		t.Fatal("analysis not retained")
	}
	var fn *ast.FuncDecl
	for candidate := range retained.a.funcDeclSymbols {
		if candidate.Name == "value" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatal("missing live function")
	}
	if !retained.a.storeFlowBinderIndexFor(fn).integerBinderNames()["item"] {
		t.Fatal("fixture did not cache integer binder")
	}
	oldScan := retained.a.storeScanIndex(fn)
	if oldScan.bodyIdentCount(fn.Body, "item") != 2 {
		t.Fatal("fixture did not populate body counts")
	}
	_, err := retained.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	newScan := retained.a.storeScanIndex(fn)
	if newScan.bodyIdentCount(fn.Body, "item") != 1 {
		t.Fatal("replacement body reused prior identifier counts")
	}
	if newScan == oldScan {
		t.Fatal("replaced body retained its scan index")
	}
	if retained.a.storeFlowBinderIndexFor(fn).integerBinderNames()["item"] {
		t.Fatal("replaced body reused its former integer binder")
	}
}

func BenchmarkStoreFlowIntegerBinderLookup(b *testing.B) {
	idx := &storeFlowBinderIndex{}
	for i := 0; i < 64; i++ {
		idx.typed = append(idx.typed, storeFlowTypedBinder{name: fmt.Sprint("v", i), t: &ast.NamedType{Name: "i64"}})
	}
	idx.integerBinderNames()
	for _, memo := range []bool{false, true} {
		b.Run(fmt.Sprint("memo=", memo), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var names map[string]bool
				if memo {
					names = idx.integerBinderNames()
				} else {
					names = idx.typedNames(storeFlowIntegerTypeExpr)
				}
				if !names["v1"] {
					b.Fatal("lost integer binder")
				}
			}
		})
	}
}
