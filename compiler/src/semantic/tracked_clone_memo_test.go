package semantic

import (
	"fmt"
	"testing"
)

func TestTrackedCloneInlineMemoCycleAndSpill(t *testing.T) {
	for _, length := range []int{1, 8, 9, 32} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			root := &RefType{}
			last := root
			for i := 1; i < length; i++ {
				next := &RefType{}
				last.Elem = next
				last = next
			}
			last.Elem = root
			a := &Analyzer{}
			clone := a.cloneTrackedValueType(root).(*RefType)
			if clone == root {
				t.Fatal("original graph returned")
			}
			cursor := clone
			for i := 0; i < length; i++ {
				cursor = cursor.Elem.(*RefType)
			}
			if cursor != clone {
				t.Fatal("cycle did not close in clone")
			}
			clone.Mutable = true
			if root.Mutable {
				t.Fatal("clone mutation reached original")
			}
		})
	}
}
func TestTrackedCloneExplicitMemoOverridesAndSharing(t *testing.T) {
	a := &Analyzer{}
	leaf := &BuiltinType{Name: "i64"}
	replacement := &BuiltinType{Name: "int"}
	memo := map[Type]Type{leaf: replacement}
	if a.cloneTrackedValueTypeWithSeen(leaf, memo) != replacement {
		t.Fatal("explicit replacement ignored")
	}
	inner := &OptionalType{Value: leaf}
	first := a.cloneTrackedValueTypeWithSeen(inner, memo)
	if first.(*OptionalType).Value != replacement || memo[inner] != first {
		t.Fatal("explicit memo not populated")
	}
	if a.cloneTrackedValueTypeWithSeen(inner, memo) != first {
		t.Fatal("explicit sharing lost")
	}
}
func BenchmarkTrackedCloneMemoShapes(b *testing.B) {
	leaf := &BuiltinType{Name: "i64"}
	cases := map[string]Type{"leaf": leaf, "optional": &OptionalType{Value: leaf}}
	for _, width := range []int{7, 64} {
		root := &StructType{Name: "R", Fields: map[string]Field{}}
		for i := 0; i < width; i++ {
			name := fmt.Sprint(i)
			root.Fields[name] = Field{Name: name, Type: &OptionalType{Value: leaf}}
		}
		cases[fmt.Sprintf("optional-fields-%d", width)] = root
	}
	for name, root := range cases {
		b.Run(name, func(b *testing.B) {
			a := &Analyzer{}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if a.cloneTrackedValueType(root) == nil {
					b.Fatal("nil clone")
				}
			}
		})
	}
}

func BenchmarkTrackedCloneRootMaps(b *testing.B) {
	for _, shape := range []string{"leaves", "shared-optional", "unique-optionals"} {
		b.Run(shape, func(b *testing.B) {
			leaf := &BuiltinType{Name: "i64"}
			shared := &OptionalType{Value: leaf}
			src := map[*Symbol]Type{}
			for i := 0; i < 64; i++ {
				var typ Type = leaf
				if shape == "shared-optional" {
					typ = shared
				}
				if shape == "unique-optionals" {
					typ = &OptionalType{Value: leaf}
				}
				src[&Symbol{Name: fmt.Sprint(i)}] = typ
			}
			a := &Analyzer{}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if len(a.cloneTrackedValueTypeMapWithSeen(src, nil)) != 64 {
					b.Fatal("missing roots")
				}
			}
		})
	}
}
