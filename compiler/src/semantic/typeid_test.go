package semantic

import (
	"testing"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func requireCanonicalTypeID(t *testing.T, typ Type) TypeID {
	t.Helper()
	id, ok := TryCanonicalTypeID(typ)
	if !ok || id == 0 {
		t.Fatalf("expected canonical type id for %T %#v", typ, typ)
	}
	return id
}

func TestCanonicalTypeIDMatchesSeparateEqualRefTypes(t *testing.T) {
	left := &RefType{Elem: &BuiltinType{Name: "i32"}, State: RefStateNullable, Storage: RefStorageHeap}
	right := &RefType{Elem: &BuiltinType{Name: "i32"}, State: RefStateNullable, Storage: RefStorageHeap}
	if !SameType(left, right) {
		t.Fatalf("expected ref types to remain structurally equal")
	}
	if leftID, rightID := requireCanonicalTypeID(t, left), requireCanonicalTypeID(t, right); leftID != rightID {
		t.Fatalf("expected equal ref types to share a canonical type id, got %d and %d", leftID, rightID)
	}
}

func TestCanonicalTypeIDDistinguishesRefQualifiers(t *testing.T) {
	nonNull := &RefType{Elem: &BuiltinType{Name: "i32"}, State: RefStateNonNull}
	nullable := &RefType{Elem: &BuiltinType{Name: "i32"}, State: RefStateNullable}
	if SameType(nonNull, nullable) {
		t.Fatalf("expected distinct ref states not to be equal")
	}
	if leftID, rightID := requireCanonicalTypeID(t, nonNull), requireCanonicalTypeID(t, nullable); leftID == rightID {
		t.Fatalf("expected distinct ref qualifiers to produce distinct canonical ids")
	}
}

func TestCanonicalTypeIDDistinguishesWritableRefs(t *testing.T) {
	readonly := &RefType{Elem: &BuiltinType{Name: "i32"}, State: RefStateNonNull}
	writable := &RefType{Elem: &BuiltinType{Name: "i32"}, Mutable: true, State: RefStateNonNull}
	if SameType(readonly, writable) {
		t.Fatalf("expected readonly and writable refs not to be equal")
	}
	if leftID, rightID := requireCanonicalTypeID(t, readonly), requireCanonicalTypeID(t, writable); leftID == rightID {
		t.Fatalf("expected writable refs to produce distinct canonical ids")
	}
}

func TestCanonicalTypeIDPreservesFuncGenericParamPositions(t *testing.T) {
	left := &FuncType{
		GenericParams: []ast.GenericParam{{Position: lexer.Pos{File: "left.llcontext", Line: 1, Col: 1, Offset: 0}, Kind: ast.GenericParamType, Name: "T"}},
		TypeParams:    []string{"T"},
		Params:        []Type{&TypeParamType{Name: "T"}},
		Return:        &TypeParamType{Name: "T"},
	}
	right := &FuncType{
		GenericParams: []ast.GenericParam{{Position: lexer.Pos{File: "right.llcontext", Line: 1, Col: 2, Offset: 1}, Kind: ast.GenericParamType, Name: "T"}},
		TypeParams:    []string{"T"},
		Params:        []Type{&TypeParamType{Name: "T"}},
		Return:        &TypeParamType{Name: "T"},
	}
	if SameType(left, right) {
		t.Fatalf("expected function types with different generic param positions to remain unequal")
	}
	if leftID, rightID := requireCanonicalTypeID(t, left), requireCanonicalTypeID(t, right); leftID == rightID {
		t.Fatalf("expected function generic param positions to contribute to canonical type ids")
	}
}

func TestCanonicalTypeIDRemainsStricterThanRuntimeBridgeEquality(t *testing.T) {
	darray := &DArrayType{Elem: &BuiltinType{Name: "i32"}, Shape: &WildcardShape{}}
	dynArray := &GenericInstanceType{Name: "DynArray", Base: &StructType{Name: "DynArray"}, Args: []Type{&BuiltinType{Name: "i32"}}}
	if !SameType(darray, dynArray) {
		t.Fatalf("expected runtime bridge compatibility to preserve SameType equality")
	}
	if leftID, rightID := requireCanonicalTypeID(t, darray), requireCanonicalTypeID(t, dynArray); leftID == rightID {
		t.Fatalf("expected runtime bridge types to keep distinct internal canonical ids")
	}
}

func TestCanonicalTypeIDRejectsMalformedPackedVariantView(t *testing.T) {
	if id, ok := TryCanonicalTypeID(&PackedVariantViewType{}); ok || id != 0 {
		t.Fatalf("expected malformed packed variant views not to receive a canonical type id, got %d", id)
	}
}
