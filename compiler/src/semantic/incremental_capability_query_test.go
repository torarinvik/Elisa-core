package semantic

import (
	"fmt"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/incremental"
)

func capabilityTestAnalyzer(session *IncrementalAnalysisSession) *Analyzer {
	return &Analyzer{typeShapesFrozen: true, capabilityQueries: &capabilityQueryInvocation{session: session, local: map[Type]capabilityLocalResult{}}}
}
func capabilityTestGraph(mutable bool) Type {
	scalar := &BuiltinType{Name: "i64"}
	a := &StructType{Name: "A", Fields: map[string]Field{}}
	b := &StructType{Name: "B", Fields: map[string]Field{}}
	a.Fields["next"] = Field{Type: &OptionalType{Value: b}}
	b.Fields["back"] = Field{Type: a}
	a.Fields["value"] = Field{Type: &RefType{Elem: scalar, Mutable: mutable}}
	return b
}
func TestCapabilityQueryReusesFreshResolvedGraphs(t *testing.T) {
	session := NewIncrementalAnalysisSession()
	for i, mutable := range []bool{false, false, true, true, false} {
		analyzer := capabilityTestAnalyzer(session)
		typ := capabilityTestGraph(mutable)
		want := analyzer.typeContainsMutableBorrowCapability(typ, map[Type]bool{}, map[*StructType][][]Type{}, 0)
		before := session.stats.CapabilityProductsReused
		if got := analyzer.hasMutableBorrowCapability(typ); got != want {
			t.Fatalf("step%d stale capability: %v/%v", i, got, want)
		}
		if (i == 1 || i == 3 || i == 4) && session.stats.CapabilityProductsReused != before+1 {
			t.Fatalf("fresh graph did not reuse retained product at%d", i)
		}
		// Root-only memo: B reached recursively under an already-seen A must not
		// publish a false answer which would be wrong for B as a fresh root.
		if mutable && !want {
			t.Fatal("cyclic root lost nested mutable borrow")
		}
		if analyzer.hasMutableBorrowCapability(typ) != want || session.stats.CapabilityLocalHits == 0 {
			t.Fatal("current analyzer memo not used")
		}
	}
}
func TestCapabilityProjectionCoversPayloadsFlagsAndOwnership(t *testing.T) {
	scalar := &BuiltinType{Name: "i64"}
	ref := &RefType{Elem: scalar, Mutable: true}
	cases := []Type{
		&StructType{Fields: map[string]Field{"borrow": {Type: ref}}},
		&StructType{Fields: map[string]Field{"borrow": {Type: ref, Ghost: true}}},
		&StructType{Fields: map[string]Field{"borrow": {Type: ref, Phantom: true}}},
		&ViewType{Elem: ref}, &ArrayType{Elem: ref}, &DArrayType{Elem: ref}, &OptionalType{Value: ref},
		&DictType{Key: scalar, Value: ref}, &DictEntryType{Mutable: true}, &SetType{Elem: ref},
		&TupleType{Fields: []TupleField{{Type: scalar}, {Type: ref}}},
		&ErrorUnionType{Value: scalar, Errors: &ErrorSetType{Payloads: map[string][]Type{"Oops": {ref}}}},
		&EnumType{Common: map[string]Field{"common": {Type: ref}}},
		&EnumType{Variants: []*EnumVariant{{Payload: []Type{ref}}}},
		&PackedVariantViewType{Variant: &EnumVariant{Payload: []Type{ref}}},
		&PackedEnumStoreType{State: ref}, &StoreRowViewType{}, &StoreRowsViewType{},
		&StructType{Fields: map[string]Field{"opaque": {Type: &TypeParamType{Name: "T"}}}},
	}
	session := NewIncrementalAnalysisSession()
	for _, typ := range cases {
		a := capabilityTestAnalyzer(session)
		want := a.typeContainsMutableBorrowCapability(typ, map[Type]bool{}, map[*StructType][][]Type{}, 0)
		if _, ok := capabilityTypeProjection(typ); !ok {
			t.Fatalf("missing projection for%T", typ)
		}
		if got := a.hasMutableBorrowCapability(typ); got != want {
			t.Fatalf("%T query=%v walk=%v", typ, got, want)
		}
	}
	// Same nominal name is insufficient: changing a field or Ghost/Phantom flag
	// in a new resolved environment must change the query input.
	first := &StructType{Name: "Same", Fields: map[string]Field{"field": {Type: ref, Ghost: true}}}
	a := capabilityTestAnalyzer(session)
	if a.hasMutableBorrowCapability(first) {
		t.Fatal("ghost contributed")
	}
	second := &StructType{Name: "Same", Fields: map[string]Field{"field": {Type: ref}}}
	if !capabilityTestAnalyzer(session).hasMutableBorrowCapability(second) {
		t.Fatal("stale nominal-name cache")
	}
	// Field-map insertion order does not change the immutable input.
	left := &StructType{Fields: map[string]Field{"a": {Type: scalar}, "b": {Type: ref}}}
	right := &StructType{Fields: map[string]Field{}}
	right.Fields["b"] = Field{Type: ref}
	right.Fields["a"] = Field{Type: scalar}
	x, _ := capabilityTypeProjection(left)
	y, _ := capabilityTypeProjection(right)
	if x != y {
		t.Fatal("map order changed projection")
	}
}
func TestCapabilityProjectionDeclinesUnrepresentedAndOversizedInputs(t *testing.T) {
	scalar := &BuiltinType{Name: "i64"}
	deep := Type(scalar)
	for i := 0; i < semanticTraversalDepthLimit; i++ {
		deep = &OptionalType{Value: deep}
	}
	nilVariants := &EnumType{Variants: make([]*EnumVariant, capabilityMaxEdges+1)}
	emptyPayloads := &ErrorUnionType{Value: scalar, Errors: &ErrorSetType{Payloads: map[string][]Type{}}}
	for i := 0; i < capabilityMaxEdges+1; i++ {
		emptyPayloads.Errors.Payloads[fmt.Sprint(i)] = nil
	}
	hugeName := &StructType{Fields: map[string]Field{strings.Repeat("x", capabilityMaxSourceBytes+1): {Type: scalar}}}
	for _, typ := range []Type{&GenericInstanceType{Base: &StructType{Name: "Generic"}}, deep, nilVariants, emptyPayloads, hugeName} {
		if _, ok := capabilityTypeProjection(typ); ok {
			t.Fatalf("unbounded/unrepresented input admitted: %T", typ)
		}
	}
	session := NewIncrementalAnalysisSession()
	a := capabilityTestAnalyzer(session)
	tooDeep := &OptionalType{Value: deep}
	a.currentFuncDecl = &ast.FuncDecl{Name: "first"}
	a.hasMutableBorrowCapability(tooDeep)
	firstHits := a.semanticLimitHits
	a.currentFuncDecl = &ast.FuncDecl{Name: "second"}
	a.hasMutableBorrowCapability(tooDeep)
	if a.semanticLimitHits <= firstHits || session.stats.CapabilityProductsPublished != 0 {
		t.Fatal("incomplete traversal cached/suppressed")
	}
	a.typeShapesFrozen = false
	before := session.stats.CapabilityReads
	a.hasMutableBorrowCapability(capabilityTestGraph(false))
	if session.stats.CapabilityReads != before {
		t.Fatal("pre-freeze graph queried")
	}
}
func TestCapabilityQueryCacheChurnIsBounded(t *testing.T) {
	cache := newCapabilityQueryCache()
	for i := 0; i < 3*capabilityMaxProducts; i++ {
		source := fmt.Sprintf("bounded-shape-%d", i)
		if !cache.store(source, i%2 == 0) {
			t.Fatal("store failed")
		}
		if len(cache.entries) > capabilityMaxProducts || cache.bytes > capabilityMaxProductBytes {
			t.Fatal("retained products escaped bound")
		}
	}
	inputs, queries := cache.db.EntryCounts()
	if inputs > 4*capabilityMaxProducts || queries > capabilityMaxProducts {
		t.Fatalf("unbounded retired metadata: %d/%d", inputs, queries)
	}
	if value, ok := cache.lookup(fmt.Sprintf("bounded-shape-%d", 3*capabilityMaxProducts-1)); !ok || value != "0" {
		t.Fatal("live product lost during compaction")
	}
}

func TestCapabilityQueryRejectsInvalidatedProducts(t *testing.T) {
	for _, kind := range []string{"capability-shape", "capability-product"} {
		for _, remove := range []bool{false, true} {
			cache := newCapabilityQueryCache()
			source, ok := capabilityTypeProjection(capabilityTestGraph(true))
			if !ok || !cache.store(source, true) {
				t.Fatal("could not seed query")
			}
			for name := range cache.entries {
				cache.db.Apply(incremental.Change{Key: incremental.Key{Kind: kind, Name: name}, Value: "corrupt", Remove: remove})
			}
			if _, hit := cache.lookup(source); hit {
				t.Fatalf("accepted invalidated %s remove=%v", kind, remove)
			}
			if !cache.store(source, true) {
				t.Fatal("could not replace invalidated product")
			}
			if value, hit := cache.lookup(source); !hit || value != "1" {
				t.Fatal("replacement did not restore current product")
			}
		}
	}
}
