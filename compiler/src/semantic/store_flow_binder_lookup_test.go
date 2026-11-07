package semantic

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"elisacore/src/ast"
)

func TestStoreFlowBinderLookupPreservesDuplicates(t *testing.T) {
	t1, t2 := &ast.NamedType{Name: "A"}, &ast.NamedType{Name: "B"}
	idx := &storeFlowBinderIndex{
		typed:      []storeFlowTypedBinder{{name: "x", t: t1}, {name: "y", t: t1}, {name: "x", t: t2}},
		rangeNames: []string{"i", "j"},
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, want := idx.typedBindings("x"), []storeFlowTypedBinder{{name: "x", t: t1}, {name: "x", t: t2}}; !reflect.DeepEqual(got, want) {
				t.Errorf("bindings = %v, want %v", got, want)
			}
			if !idx.hasRangeName("i") || idx.hasRangeName("x") || len(idx.typedBindings("missing")) != 0 {
				t.Error("incorrect name lookup")
			}
		}()
	}
	wg.Wait()
}

func TestStoreFlowBinderClassifications(t *testing.T) {
	idx := &storeFlowBinderIndex{typed: []storeFlowTypedBinder{
		{name: "xs", t: &ast.BuiltinTypeExpr{Name: "darray"}},
		{name: "view", t: &ast.BuiltinTypeExpr{Name: "sview"}},
		{name: "named", t: &ast.NamedType{Name: "Item"}},
		{name: "shadow", t: &ast.NamedType{Name: "Item"}},
		{name: "shadow", t: &ast.NamedType{Name: "Other"}},
	}}
	wantDarrays := idx.typedNames(storeFlowDarrayTypeExpr)
	wantViews := idx.typedNames(storeFlowViewTypeExpr)
	wantValues := idx.typedNames(storeFlowValueNamedTypeExpr)
	wantNamed := idx.singleNamedTypes()
	idx.classify()
	if !reflect.DeepEqual(idx.darrayNames, wantDarrays) || !reflect.DeepEqual(idx.viewNames, wantViews) || !reflect.DeepEqual(idx.valueNamedNames, wantValues) || !reflect.DeepEqual(idx.namedTypes, wantNamed) {
		t.Fatal("cached classifications differ from the original classifiers")
	}
	if _, ok := idx.namedTypes["shadow"]; ok {
		t.Fatal("conflicting shadowed type was accepted")
	}
}

func TestStoreFlowScalarNamesMatchesOriginalCalculation(t *testing.T) {
	decl := &ast.FuncDecl{}
	idx := &storeFlowBinderIndex{
		typed: []storeFlowTypedBinder{
			{name: "scalar", t: &ast.BuiltinTypeExpr{Name: "u32"}},
			{name: "mixed", t: &ast.BuiltinTypeExpr{Name: "u32"}},
			{name: "mixed", t: &ast.BuiltinTypeExpr{Name: "darray"}},
			{name: "shadow", t: &ast.BuiltinTypeExpr{Name: "u32"}},
		},
		rangeNames: []string{"range"}, other: []string{"shadow"}, untyped: map[string]bool{"shadow": true},
	}
	a := &Analyzer{storeFlowBinderIndexes: map[*ast.FuncDecl]*storeFlowBinderIndex{decl: idx}}
	want := idx.typedNames(storeFlowScalarTypeExpr)
	total, pointerFree := map[string]int{}, map[string]int{}
	for _, binder := range idx.typed {
		total[binder.name]++
		if storeFlowScalarTypeExpr(binder.t) || a.storeFlowPointerFreeTypeExpr(binder.t, map[Type]bool{}) {
			pointerFree[binder.name]++
		}
	}
	for name, count := range total {
		if count > 0 && pointerFree[name] == count && !idx.untyped[name] {
			want[name] = true
		}
	}
	for i := 0; i < 2; i++ {
		if got := a.storeFlowScalarNames(decl); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

var binderLookupSink int

func BenchmarkStoreFlowBinderLookup(b *testing.B) {
	idx := &storeFlowBinderIndex{}
	for i := 0; i < 1000; i++ {
		idx.typed = append(idx.typed, storeFlowTypedBinder{name: fmt.Sprintf("var%d", i)})
	}
	idx.typedBindings("var999")
	b.Run("linear", func(b *testing.B) {
		count := 0
		for i := 0; i < b.N; i++ {
			for _, binder := range idx.typed {
				if binder.name == "var999" {
					count++
				}
			}
		}
		binderLookupSink = count
	})
	b.Run("indexed", func(b *testing.B) {
		count := 0
		for i := 0; i < b.N; i++ {
			for _, binder := range idx.typedBindings("var999") {
				if binder.name == "var999" {
					count++
				}
			}
		}
		binderLookupSink = count
	})
}

func BenchmarkStoreFlowBinderClassifications(b *testing.B) {
	idx := &storeFlowBinderIndex{}
	for i := 0; i < 300; i++ {
		idx.typed = append(idx.typed, storeFlowTypedBinder{name: fmt.Sprintf("var%d", i), t: &ast.NamedType{Name: "Record"}})
	}
	idx.classify()
	for _, cached := range []bool{false, true} {
		name := "recompute"
		if cached {
			name = "reuse"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if cached {
					idx.classify()
					binderLookupSink = len(idx.namedTypes)
				} else {
					binderLookupSink = len(idx.typedNames(storeFlowDarrayTypeExpr)) + len(idx.typedNames(storeFlowViewTypeExpr)) + len(idx.typedNames(storeFlowValueNamedTypeExpr)) + len(idx.singleNamedTypes())
				}
			}
		})
	}
}

func BenchmarkStoreFlowScalarNames(b *testing.B) {
	decl := &ast.FuncDecl{}
	for i := 0; i < 300; i++ {
		decl.Params = append(decl.Params, ast.ParamDecl{Name: fmt.Sprintf("var%d", i), Type: &ast.BuiltinTypeExpr{Name: "u32"}})
	}
	a := &Analyzer{}
	a.storeFlowScalarNames(decl)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		binderLookupSink = len(a.storeFlowScalarNames(decl))
	}
}
