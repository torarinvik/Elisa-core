package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestTypeSymbolKeyConstructorsAndCanonicalGraphs(t *testing.T) {
	u8 := &BuiltinType{Name: "u8"}
	cases := []Type{u8, &RefType{Elem: u8}, &OptionalType{Value: u8}, &RefType{Elem: u8, Mutable: true}, &BitIntType{Width: 13}, &BitIntType{Width: 14}, &StructType{Name: "Item", Namespace: "A"}, &StructType{Name: "Item", Namespace: "B"}, &ArrayType{Elem: u8, HasConstSize: true, ConstSize: 2}, &ArrayType{Elem: u8, HasConstSize: true, ConstSize: 3}, &FuncType{Params: []Type{u8}, Return: u8, CallConv: "c"}, &FuncType{Params: []Type{u8}, Return: u8, CallConv: "fast"}}
	seen := map[string]bool{}
	for _, typ := range cases {
		key, err := TypeSymbolKey(typ)
		if err != nil {
			t.Fatal(err)
		}
		if seen[key] {
			t.Fatal("distinct constructors or payloads collided", typ)
		}
		seen[key] = true
	}
	shared := &TupleType{Fields: []TupleField{{Type: u8}, {Type: u8}}}
	copied := &TupleType{Fields: []TupleField{{Type: &BuiltinType{Name: "u8"}}, {Type: &BuiltinType{Name: "u8"}}}}
	same := func(a, b Type) {
		t.Helper()
		ka, ea := TypeSymbolKey(a)
		kb, eb := TypeSymbolKey(b)
		if ea != nil || eb != nil || ka != kb {
			t.Fatal("equivalent graphs differ", ka, kb, ea, eb)
		}
	}
	same(shared, copied)
	same(&ArrayType{Elem: u8, HasConstSize: true, ConstSize: 2, Size: "1+1"}, &ArrayType{Elem: u8, HasConstSize: true, ConstSize: 2, Size: "2"})
	a := &FuncType{Name: "first", Params: []Type{u8}, Return: u8}
	b := &FuncType{Name: "second", Params: []Type{u8}, Return: u8}
	if !SameType(a, b) {
		t.Fatal("control function types differ")
	}
	same(a, b)
	recursive := &RefType{}
	recursive.Elem = recursive
	if _, err := TypeSymbolKey(recursive); err == nil {
		t.Fatal("anonymous recursive shape was accepted")
	}
	var missing *RefType
	if _, err := TypeSymbolKey(missing); err == nil {
		t.Fatal("typed nil was accepted")
	}
}

func TestTypeSymbolKeyConstPayloads(t *testing.T) {
	values := []ConstValue{{Kind: ConstInt, Int: 1}, {Kind: ConstInt, Int: 2}, {Kind: ConstFloat, Float: 1}, {Kind: ConstFloat, Float: 2}, {Kind: ConstList, Elems: []ConstValue{{Kind: ConstInt, Int: 1}}}, {Kind: ConstList, Elems: []ConstValue{{Kind: ConstInt, Int: 2}}}, {Kind: ConstOptional}, {Kind: ConstOptional, Some: true, Value: &ConstValue{Kind: ConstInt, Int: 1}}}
	seen := map[string]bool{}
	for _, value := range values {
		key, err := TypeSymbolKey(&ConstValueType{Value: value})
		if err != nil {
			t.Fatal(err)
		}
		if seen[key] {
			t.Fatal("distinct constant payloads collided")
		}
		seen[key] = true
	}
	left := &ConstValueType{Value: ConstValue{Kind: ConstRecord, Fields: map[string]ConstValue{"a": {Kind: ConstInt, Int: 1}, "b": {Kind: ConstInt, Int: 2}}}}
	right := &ConstValueType{Value: ConstValue{Kind: ConstRecord, Fields: map[string]ConstValue{"b": {Kind: ConstInt, Int: 2}, "a": {Kind: ConstInt, Int: 1}}}}
	a, _ := TypeSymbolKey(left)
	b, _ := TypeSymbolKey(right)
	if a != b {
		t.Fatal("record map insertion order affected identity")
	}
}

func TestTypeSymbolKeyFreshShapeAlphaRenaming(t *testing.T) {
	elem := &BuiltinType{Name: "u8"}
	makeType := func(id int) Type { return &DArrayType{Elem: elem, Shape: &FreshShape{ID: id}} }
	a, err := TypeSymbolKeys([]Type{makeType(1), makeType(1)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := TypeSymbolKeys([]Type{makeType(99), makeType(99)})
	if err != nil {
		t.Fatal(err)
	}
	c, err := TypeSymbolKeys([]Type{makeType(99), makeType(100)})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("analyzer fresh allocation IDs affected symbols")
	}
	if b == c {
		t.Fatal("distinct fresh-shape alias relations collapsed")
	}
}

func TestTypeSymbolKeyNominalRegionRepresentation(t *testing.T) {
	a := []Type{&RegionParamType{Name: "r"}}
	b := []Type{&RegionValueType{Name: "r"}}
	ka, ea := TypeSymbolKeys(a)
	kb, eb := TypeSymbolKeys(b)
	if ea != nil || eb != nil || ka == kb {
		t.Fatal("semantic region kinds collapsed", ea, eb)
	}
	la, ea := TypeSymbolKeysForNominalLayout(a)
	lb, eb := TypeSymbolKeysForNominalLayout(b)
	if ea != nil || eb != nil || la != lb {
		t.Fatal("formal/bound region representation differs", ea, eb)
	}
}

func TestTypeSymbolKeyNominalRegionNamesErasedOnlyForLayout(t *testing.T) {
	base := &StructType{Name: "StringHandle", RegionParams: []string{"r"}}
	left := []Type{&GenericInstanceType{Name: "StringHandle", Base: base, Args: []Type{&RegionParamType{Name: "r"}}}}
	right := []Type{&GenericInstanceType{Name: "StringHandle", Base: base, Args: []Type{&RegionValueType{Name: "input_region"}}}}
	a, _ := TypeSymbolKeys(left)
	b, _ := TypeSymbolKeys(right)
	if a == b {
		t.Fatal("semantic lifetime authority was erased")
	}
	a, ea := TypeSymbolKeysForNominalLayout(left)
	b, eb := TypeSymbolKeysForNominalLayout(right)
	if ea != nil || eb != nil || a != b {
		t.Fatal("lifetime-only argument changed nominal LLVM layout", ea, eb)
	}
	u8 := &BuiltinType{Name: "u8"}
	a, _ = TypeSymbolKeysForNominalLayout([]Type{&RefType{Elem: u8, Region: "r"}})
	b, _ = TypeSymbolKeysForNominalLayout([]Type{&RefType{Elem: u8, Region: "input_region"}})
	if a != b {
		t.Fatal("opaque reference provenance changed LLVM nominal layout")
	}
}

func TestTypeSymbolKeyFunctionFieldRegionLayout(t *testing.T) {
	u8 := &BuiltinType{Name: "u8"}
	box := &StructType{Name: "FunctionBox"}
	field := func(region string) Type {
		return &GenericInstanceType{Name: "FunctionBox", Base: box, Args: []Type{&FuncType{GenericParams: []ast.GenericParam{{Kind: ast.GenericParamRegion, Name: region}}, RegionParams: []string{region}, ReturnRegion: region, Params: []Type{&RefType{Elem: u8, Region: region}}, Return: &RefType{Elem: u8, Region: region}, CallConv: "c"}}}
	}
	a, b := []Type{field("r")}, []Type{field("input_region")}
	sa, _ := TypeSymbolKeys(a)
	sb, _ := TypeSymbolKeys(b)
	if sa == sb {
		t.Fatal("semantic function lifetime metadata collapsed")
	}
	la, ea := TypeSymbolKeysForNominalLayout(a)
	lb, eb := TypeSymbolKeysForNominalLayout(b)
	if ea != nil || eb != nil || la != lb {
		t.Fatal("function-field lifetime-only names changed nominal representation", ea, eb)
	}
}
