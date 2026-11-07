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
