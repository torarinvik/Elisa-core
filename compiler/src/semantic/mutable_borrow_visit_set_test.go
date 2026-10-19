package semantic

import (
	"fmt"
	"testing"
)

func TestMutableBorrowInlineVisitsCycleSpillAndMutation(t *testing.T) {
	for _, n := range []int{1, 7, 8, 9, 32} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			root := &StructType{Name: "Root", Fields: map[string]Field{}}
			last := root
			for i := 1; i < n; i++ {
				next := &StructType{Name: fmt.Sprint(i), Fields: map[string]Field{}}
				last.Fields["next"] = Field{Type: next}
				last = next
			}
			last.Fields["cycle"] = Field{Type: root}
			a := &Analyzer{}
			if a.hasMutableBorrowCapability(root) {
				t.Fatal("borrow-free cycle gained capability")
			}
			last.Fields["borrow"] = Field{Type: &RefType{Mutable: true}}
			if !a.hasMutableBorrowCapability(root) {
				t.Fatal("mutation lost writable capability")
			}
			if a.semanticLimitHits != 0 {
				t.Fatal("ordinary cycle reached depth limit")
			}
		})
	}
}
func TestMutableBorrowInlineVisitsDepthGuard(t *testing.T) {
	a := &Analyzer{}
	var seen mutableBorrowVisitSet
	if !a.typeContainsMutableBorrowCapabilityWithVisits(&BuiltinType{Name: "i64"}, &seen, nil, semanticTraversalDepthLimit+1) {
		t.Fatal("cutoff must remain conservative")
	}
	if a.semanticLimitHits != 1 || seen.count != 0 || seen.large != nil {
		t.Fatal("depth guard changed")
	}
}

func TestMutableBorrowInlineVisitsSharedTailDepthOrdering(t *testing.T) {
	tail := &OptionalType{Value: &BuiltinType{Name: "i64"}}
	var prefix Type = tail
	for i := 0; i < semanticTraversalDepthLimit; i++ {
		prefix = &OptionalType{Value: prefix}
	}
	root := &TupleType{Fields: []TupleField{{Type: tail}, {Type: prefix}}}
	original := &Analyzer{}
	want := original.typeContainsMutableBorrowCapability(root, map[Type]bool{}, map[*StructType][][]Type{}, 0)
	candidate := &Analyzer{}
	got := candidate.hasMutableBorrowCapability(root)
	if got != want || !got || candidate.semanticLimitHits != original.semanticLimitHits || candidate.semanticLimitHits != 1 {
		t.Fatalf("shared-tail depth behavior got=%v/%d want=%v/%d", got, candidate.semanticLimitHits, want, original.semanticLimitHits)
	}
}

func TestMutableBorrowInlineVisitsExplicitMapUpdates(t *testing.T) {
	leaf := &BuiltinType{Name: "i64"}
	seeded := &OptionalType{Value: &RefType{Mutable: true}}
	root := &TupleType{Fields: []TupleField{{Type: seeded}}}
	seen := map[Type]bool{seeded: true}
	for i := 0; i < 12; i++ {
		node := &OptionalType{Value: leaf}
		root.Fields = append(root.Fields, TupleField{Type: node})
		seen[node] = false
	}
	a := &Analyzer{}
	if a.typeContainsMutableBorrowCapability(root, seen, map[*StructType][][]Type{}, 0) {
		t.Fatal("preseeded node was revisited")
	}
	if !seen[root] || !seen[seeded] || len(seen) != 14 {
		t.Fatal("external visited map lost updates")
	}
	for _, field := range root.Fields {
		if !seen[field.Type] {
			t.Fatal("false preseed entry was treated as visited")
		}
	}
}
