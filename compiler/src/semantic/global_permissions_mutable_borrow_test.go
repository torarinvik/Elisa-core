package semantic

import (
	"fmt"
	"testing"
)

func TestMutableBorrowCapabilityTerminalTypesSkipCycleMap(t *testing.T) {
	var nilRows *StoreRowsViewType
	var nilRow *StoreRowViewType
	tests := []struct {
		name string
		typ  Type
		want bool
	}{
		{name: "builtin", typ: &BuiltinType{Name: "i64"}},
		{name: "never", typ: &NeverType{}},
		{name: "null", typ: &NullType{}},
		{name: "invalid", typ: &InvalidType{}},
		{name: "error set", typ: &ErrorSetType{Payloads: map[string][]Type{"E": {&RefType{Mutable: true}}}}},
		{name: "opaque", typ: &OpaqueType{Name: "Handle"}},
		{name: "type parameter", typ: &TypeParamType{Name: "T"}, want: true},
		{name: "store rows view", typ: &StoreRowsViewType{}, want: true},
		{name: "store row view", typ: &StoreRowViewType{}, want: true},
		{name: "nil store rows view", typ: nilRows},
		{name: "nil store row view", typ: nilRow},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			seen := map[Type]bool{}
			analyzer := &Analyzer{}
			if got := analyzer.typeContainsMutableBorrowCapability(test.typ, seen, map[*StructType][][]Type{}, 0); got != test.want {
				t.Fatalf("capability = %t, want %t", got, test.want)
			}
			if len(seen) != 0 {
				t.Fatalf("terminal type added %d cycle-map entries", len(seen))
			}
		})
	}
}

func TestMutableBorrowCapabilityTerminalFastPathKeepsDepthGuard(t *testing.T) {
	seen := map[Type]bool{}
	analyzer := &Analyzer{}
	got := analyzer.typeContainsMutableBorrowCapability(&BuiltinType{Name: "i64"}, seen, map[*StructType][][]Type{}, semanticTraversalDepthLimit+1)
	if !got {
		t.Fatal("depth cutoff must conservatively report a mutable borrow")
	}
	if analyzer.semanticLimitHits != 1 {
		t.Fatalf("depth cutoff reported %d limit hits, want 1", analyzer.semanticLimitHits)
	}
	if len(seen) != 0 {
		t.Fatalf("depth cutoff added %d cycle-map entries", len(seen))
	}
}

func TestMutableBorrowTerminalFastPathPreservesSeededSeen(t *testing.T) {
	for _, typ := range []Type{&BuiltinType{Name: "i64"}, &TypeParamType{Name: "T"}, &StoreRowsViewType{}, &StoreRowViewType{}} {
		a := &Analyzer{}
		seen := map[Type]bool{typ: true}
		if a.typeContainsMutableBorrowCapability(typ, seen, map[*StructType][][]Type{}, 0) {
			t.Fatalf("seeded %T was revisited", typ)
		}
		if len(seen) != 1 {
			t.Fatal("seeded state changed")
		}
	}
}

func TestMutableBorrowTraversalKeepsRecursiveCapability(t *testing.T) {
	scalar := &BuiltinType{Name: "i64"}
	root := &StructType{Name: "Node", Fields: map[string]Field{}}
	root.Fields["next"] = Field{Type: &OptionalType{Value: root}}
	root.Fields["value"] = Field{Type: scalar}
	a := &Analyzer{}
	if a.typeContainsMutableBorrowCapability(root, map[Type]bool{}, map[*StructType][][]Type{}, 0) {
		t.Fatal("non-borrowing recursive type changed")
	}
	root.Fields["borrow"] = Field{Type: &RefType{Elem: scalar, Mutable: true}}
	if !a.typeContainsMutableBorrowCapability(root, map[Type]bool{}, map[*StructType][][]Type{}, 0) {
		t.Fatal("recursive writable borrow lost")
	}
	if a.semanticLimitHits != 0 {
		t.Fatal("ordinary recursion escaped cycle guard")
	}
}

var benchmarkMutableBorrowSink bool

func BenchmarkMutableBorrowCapabilityTraversal(b *testing.B) {
	scalar := &BuiltinType{Name: "i64"}
	makeRow := func(fields int) Type {
		row := &StructType{Name: "Row", Fields: map[string]Field{}}
		for i := 0; i < fields; i++ {
			row.Fields[fmt.Sprint(i)] = Field{Type: &OptionalType{Value: scalar}}
		}
		return row
	}
	for _, test := range []struct {
		name string
		typ  Type
	}{{"scalar", scalar}, {"seven-elements", makeRow(7)}, {"wide", makeRow(64)}} {
		b.Run(test.name, func(b *testing.B) {
			analyzer := &Analyzer{}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got := analyzer.typeContainsMutableBorrowCapability(test.typ, map[Type]bool{}, map[*StructType][][]Type{}, 0)
				if got {
					b.Fatal("unexpected mutable capability")
				}
				benchmarkMutableBorrowSink = got
			}
		})
	}
}
