//go:build cgo

package semantic

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestFunctionTypeGraphOwnedAttributesAndSharing(t *testing.T) {
	leaf := &BuiltinType{Name: "i64"}
	cases := []Type{
		&RefType{Elem: leaf, Mutable: true, State: RefStateNullable, Storage: RefStorageStack, Region: "arena", ExplicitStorage: true, Linear: true},
		&ViewType{Elem: leaf, Mutable: true, Begin: "lo", End: "hi", SurfaceName: "view", Region: "r"},
		&SViewType{Begin: "lo", End: "hi", Region: "r"},
		&ArrayType{Elem: leaf, Size: "N", HasConstSize: true, ConstSize: 8, ConstParam: "N", SurfaceName: "array"},
		&DArrayType{Elem: leaf, SurfaceName: "string", Region: "r"},
		&CStrType{SurfaceName: "cstr", Region: "r"},
		&DictType{Key: leaf, Value: &OptionalType{Value: leaf}, SurfaceName: "dict", Region: "r"},
		&SetType{Elem: leaf, SurfaceName: "set", Region: "r"},
		&DictEntryType{Dict: &DictType{Key: leaf, Value: leaf}, Mutable: true},
		&IDType{Tag: &TypeParamType{Name: "T"}, Storage: leaf},
		&AddressSpaceType{Space: "guest", Elem: leaf, Storage: leaf, LayoutName: "Layout"},
		&GenericInstanceType{Name: "Wrapper", Base: &TypeParamType{Name: "B"}, Args: []Type{leaf, leaf}, Region: "r"},
		&AggregateStateType{Base: leaf, State: RefStateNullable, States: []RefState{RefStateNull, RefStateNonNull}},
		&ConstParamType{Name: "N", ValueType: leaf}, &RegionParamType{Name: "r"}, &RegionValueType{Name: "arena"}, &RefStorageValueType{Storage: RefStorageStatic},

		&ErrorUnionType{Value: leaf, Errors: &ErrorSetType{Tags: []string{"Bad"}, Payloads: map[string][]Type{"Bad": {leaf, leaf}}, Params: []string{"R"}}},
	}
	for _, original := range cases {
		graph, err := captureFunctionTypeGraph(original, nil)
		if err != nil {
			t.Fatalf("%T: %v", original, err)
		}
		bytes, err := json.Marshal(graph)
		if err != nil {
			t.Fatal(err)
		}
		var stored functionTypeGraph
		if err := json.Unmarshal(bytes, &stored); err != nil {
			t.Fatal(err)
		}
		restored, err := stored.restore(nil, false)
		if err != nil {
			t.Fatalf("%T: %v", original, err)
		}
		if !reflect.DeepEqual(original, restored) {
			t.Fatalf("lost attributes in %T: %#v / %#v", original, original, restored)
		}
		if original == restored {
			t.Fatal("owned root retained old pointer")
		}
	}
	shared := &OptionalType{Value: leaf}
	tuple := &TupleType{Fields: []TupleField{{Name: "left", Type: shared}, {Name: "right", Type: shared}}}
	graph, err := captureFunctionTypeGraph(tuple, nil)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := graph.restore(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	fields := restored.(*TupleType).Fields
	if fields[0].Type != fields[1].Type || fields[0].Type == shared {
		t.Fatal("sharing or ownership lost")
	}
	shared.Value = shared
	graph, err = captureFunctionTypeGraph(shared, nil)
	if err != nil {
		t.Fatal(err)
	}
	cycle, err := graph.restore(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if cycle.(*OptionalType).Value != cycle || cycle == shared {
		t.Fatal("cycle not restored into fresh graph")
	}
}

func TestFunctionTypeGraphCurrentNominalEnvironment(t *testing.T) {
	makeType := func() *StructType {
		node := &StructType{Name: "M.Node", Fields: map[string]Field{}, DropHook: "M.drop"}
		node.Fields["next"] = Field{Name: "next", Type: &RefType{Elem: node, Region: "r"}}
		node.Decl = &ast.StructDecl{Position: lexer.Pos{File: "old.elisa", Line: 2}}
		return node
	}
	bind := func(node *StructType) *FunctionTypeEnvironment {
		env, err := NewFunctionTypeEnvironment([]FunctionTypeBinding{{Origin: "/project/node.elisa", QualifiedName: "M.Node", Type: node}})
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	previous := makeType()
	graph, err := captureFunctionTypeGraph(&OptionalType{Value: previous}, bind(previous))
	if err != nil {
		t.Fatal(err)
	}
	current := makeType()
	current.Decl.Position = lexer.Pos{File: "current.elisa", Line: 200, Col: 3, Offset: 900}
	env := bind(current)
	restored, err := graph.restore(env, false)
	if err != nil {
		t.Fatal(err)
	}
	if restored.(*OptionalType).Value != current {
		t.Fatal("nominal type did not resolve to current declaration")
	}
	for _, edit := range []func(*StructType){
		func(n *StructType) { n.Fields["other"] = Field{Name: "other", Type: &BuiltinType{Name: "i32"}} },
		func(n *StructType) { n.Alignment = 32; n.HasAlignment = true },
		func(n *StructType) { n.DropHook = "M.changed_drop" },
	} {
		changed := makeType()
		changedEnv := bind(changed)
		edit(changed)
		if _, err := graph.restore(changedEnv, false); err == nil {
			t.Fatal("same-name changed nominal type reused")
		}
	}
	// Changes after environment construction must be observed too.
	current.Fields["tail"] = Field{Name: "tail", Type: &BuiltinType{Name: "u8"}}
	if _, err := graph.restore(env, false); err == nil {
		t.Fatal("environment retained a stale fingerprint")
	}
	if _, err := graph.restore(nil, false); err == nil {
		t.Fatal("nominal reference restored without current environment")
	}
}

func TestFunctionTypeGraphDeclinesUnownedAndCorruptValues(t *testing.T) {
	leaf := &BuiltinType{Name: "i64"}
	for _, typ := range []Type{
		&ArrayType{Elem: leaf, ElemTypeExpr: &ast.BuiltinTypeExpr{Name: "i64"}},
		&DArrayType{Elem: leaf, Shape: &NamedShape{Name: "S"}},
		&StructStateCaseType{StructName: "M.X", Case: "Open"}, &StructStateSetType{StructName: "M.X", Cases: []string{"Open"}}, &FuncType{}, &InvalidType{}, &BuiltinType{Name: "future"},
	} {
		if _, err := captureFunctionTypeGraph(typ, nil); err == nil {
			t.Fatalf("unowned type accepted: %T", typ)
		}
	}
	original := &OptionalType{Value: leaf}
	graph, err := captureFunctionTypeGraph(original, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*functionTypeGraph){
		func(g *functionTypeGraph) { g.Root = 0 },
		func(g *functionTypeGraph) { g.Nodes[0].Children = []uint32{99} },
		func(g *functionTypeGraph) { g.Nodes[0].Children = nil },
		func(g *functionTypeGraph) { g.Nodes[0].Kind = "FutureType" },
		func(g *functionTypeGraph) { g.Nodes[0].Data = json.RawMessage(`{"Value":null,"Future":1}`) },
		func(g *functionTypeGraph) { g.Nodes[0].Data = json.RawMessage(`{"Value":{}}`) },
	} {
		data, _ := json.Marshal(graph)
		var bad functionTypeGraph
		json.Unmarshal(data, &bad)
		edit(&bad)
		if _, err := bad.restore(nil, false); err == nil {
			t.Fatal("corrupt recipe accepted")
		}
	}
	var deep Type = leaf
	for i := 0; i < functionTypeMaxDepth+5; i++ {
		deep = &OptionalType{Value: deep}
	}
	if _, err := captureFunctionTypeGraph(deep, nil); err == nil {
		t.Fatal("depth budget missing")
	}
	if _, err := captureFunctionTypeGraph(&SViewType{Begin: strings.Repeat("x", functionScalarFactsMaxBytes+1)}, nil); err == nil {
		t.Fatal("string budget missing")
	}
}

func TestFunctionTypeGraphEnumMemoIsDerived(t *testing.T) {
	variant := &EnumVariant{Name: "Leaf", Payload: []Type{&BuiltinType{Name: "i64"}}, Tag: 1}
	enum := &EnumType{Name: "M.Tree", Variants: []*EnumVariant{variant}, VariantMap: map[string]*EnumVariant{"Leaf": variant}}
	env, err := NewFunctionTypeEnvironment([]FunctionTypeBinding{{Origin: "/project/tree.elisa", QualifiedName: "M.Tree", Type: enum}})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := captureFunctionTypeGraph(enum, env)
	if err != nil {
		t.Fatal(err)
	}
	variant.packedViewType = &PackedVariantViewType{Enum: enum, Variant: variant}
	if _, err := graph.restore(env, false); err != nil {
		t.Fatalf("derived cache invalidated interface: %v", err)
	}
	variant.Payload[0] = &BuiltinType{Name: "u8"}
	if _, err := graph.restore(env, false); err == nil {
		t.Fatal("changed enum payload reused")
	}
}

func TestFunctionTypeGraphRejectsZeroChildrenAndEnumDiscriminators(t *testing.T) {
	graph, err := captureFunctionTypeGraph(&OptionalType{Value: &BuiltinType{Name: "i64"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	graph.Nodes[0].Children[0] = 0
	for _, validationOnly := range []bool{false, true} {
		if _, err := graph.restore(nil, validationOnly); err == nil {
			t.Fatal("nil required child accepted")
		}
	}
	for _, typ := range []Type{
		&RefStorageValueType{Storage: 99},
		&AggregateStateType{Base: &BuiltinType{Name: "i64"}, State: 99},
		&AggregateStateType{Base: &BuiltinType{Name: "i64"}, States: []RefState{99}},
	} {
		if _, err := captureFunctionTypeGraph(typ, nil); err == nil {
			t.Fatalf("invalid discriminator accepted: %T", typ)
		}
	}
}

func TestFunctionScalarFactsCompositeNominalPublicRoundTrip(t *testing.T) {
	fn := parseProductFunction(t, functionProductSource)
	index, _ := IndexFunctionSource(fn, productContext())
	expr := fn.Body[0].(*ast.ReturnStmt).Value
	makeType := func() *StructType {
		return &StructType{Name: "M.Item", Fields: map[string]Field{"id": {Name: "id", Type: &BuiltinType{Name: "i64"}}}}
	}
	bind := func(typ *StructType) *FunctionTypeEnvironment {
		env, err := NewFunctionTypeEnvironment([]FunctionTypeBinding{{Origin: "/project/item.elisa", QualifiedName: "M.Item", Type: typ}})
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	old := makeType()
	facts, err := CaptureFunctionScalarFactsWithTypes(index, FunctionScalarObservations{ExprTypes: map[ast.Expr]Type{expr: &RefType{Elem: old, Mutable: true, Region: "arena", Linear: true}}}, bind(old))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := DecodeFunctionScalarFacts(facts.Encoded())
	if err != nil {
		t.Fatal(err)
	}
	fresh := parseProductFunction(t, "\n\n"+functionProductSource)
	target, _ := IndexFunctionSource(fresh, productContext())
	current := makeType()
	env := bind(current)
	restored, err := stored.RestoreWithTypes(target, env)
	if err != nil {
		t.Fatal(err)
	}
	node := fresh.Body[0].(*ast.ReturnStmt).Value
	ref := restored.ExprTypes[node].(*RefType)
	if ref.Elem != current || !ref.Mutable || !ref.Linear || ref.Region != "arena" {
		t.Fatal("public product lost nominal ownership or wrapper flags")
	}
	if _, err := stored.Restore(target); err == nil {
		t.Fatal("public product restored nominal type without current environment")
	}
	deps, err := stored.TypeDependencies()
	if err != nil || len(deps) != 1 {
		t.Fatalf("missing tracked nominal dependency: %v / %v", deps, err)
	}
	currentDependency, err := env.CurrentDependency(deps[0].Key)
	if err != nil || currentDependency != deps[0] {
		t.Fatalf("dependency identity mismatch: %v", err)
	}
	current.Alignment = 64
	changedDependency, err := env.CurrentDependency(deps[0].Key)
	if err != nil || changedDependency.Fingerprint == deps[0].Fingerprint {
		t.Fatal("declaration edit did not change tracked input")
	}
	if output, err := stored.RestoreWithTypes(target, env); err == nil || !reflect.DeepEqual(output, FunctionScalarObservations{}) {
		t.Fatal("changed same-name type did not decline atomically")
	}
}

func TestFunctionTypeGraphRejectsCorruptedEnumValues(t *testing.T) {
	leaf := &BuiltinType{Name: "i64"}
	for _, item := range []struct {
		typ  Type
		data string
	}{
		{&RefStorageValueType{Storage: RefStorageStatic}, `{"Storage":99}`},
		{&AggregateStateType{Base: leaf}, `{"Base":null,"State":99,"States":null}`},
		{&AggregateStateType{Base: leaf}, `{"Base":null,"State":0,"States":[99]}`},
	} {
		graph, err := captureFunctionTypeGraph(item.typ, nil)
		if err != nil {
			t.Fatal(err)
		}
		graph.Nodes[0].Data = json.RawMessage(item.data)
		for _, validateOnly := range []bool{false, true} {
			if _, err := graph.restore(nil, validateOnly); err == nil {
				t.Fatal("corrupt enum discriminator accepted")
			}
		}
	}
}

func TestFunctionTypeGraphNamedErrorSetRequiresCurrentDeclaration(t *testing.T) {
	old := &ErrorSetType{Name: "M.Errors", Tags: []string{"Bad"}}
	env, err := NewFunctionTypeEnvironment([]FunctionTypeBinding{{Origin: "/project/errors.elisa", QualifiedName: "M.Errors", Type: old}})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := captureFunctionTypeGraph(&ErrorUnionType{Value: &BuiltinType{Name: "i64"}, Errors: old}, env)
	if err != nil {
		t.Fatal(err)
	}
	current := &ErrorSetType{Name: "M.Errors", Tags: []string{"Bad"}}
	currentEnv, err := NewFunctionTypeEnvironment([]FunctionTypeBinding{{Origin: "/project/errors.elisa", QualifiedName: "M.Errors", Type: current}})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := graph.restore(currentEnv, false)
	if err != nil {
		t.Fatal(err)
	}
	if restored.(*ErrorUnionType).Errors != current {
		t.Fatal("named errorset was serialized as an old owned definition")
	}
	current.Tags = append(current.Tags, "Other")
	if _, err := graph.restore(currentEnv, false); err == nil {
		t.Fatal("changed named errorset reused")
	}
	if _, err := captureFunctionTypeGraph(old, nil); err == nil {
		t.Fatal("named errorset captured without declaration input")
	}
}

func TestFunctionTypeGraphRejectsDisguisedOwnedNominal(t *testing.T) {
	graph, err := captureFunctionTypeGraph(&ErrorSetType{Tags: []string{"Bad"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	graph.Nodes[0].Data = json.RawMessage(`{"Name":"M.Errors","Tags":["Bad"],"Payloads":null,"Params":null}`)
	for _, validateOnly := range []bool{false, true} {
		if _, err := graph.restore(nil, validateOnly); err == nil {
			t.Fatal("named declaration bypassed environment validation")
		}
	}
}

func TestFunctionScalarFactsSharesTypesAcrossExpressions(t *testing.T) {
	fn := parseProductFunction(t, functionProductSource)
	index, _ := IndexFunctionSource(fn, productContext())
	binary := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
	shared := &RefType{Elem: &BuiltinType{Name: "i64"}, Mutable: true, Region: "r"}
	input := FunctionScalarObservations{ExprTypes: map[ast.Expr]Type{binary: shared, binary.Left: shared, binary.Right: &OptionalType{Value: shared}}}
	facts, err := CaptureFunctionScalarFacts(index, input)
	if err != nil {
		t.Fatal(err)
	}
	reversed := FunctionScalarObservations{ExprTypes: map[ast.Expr]Type{}}
	for _, node := range []ast.Expr{binary.Right, binary.Left, binary} {
		reversed.ExprTypes[node] = input.ExprTypes[node]
	}
	second, err := CaptureFunctionScalarFacts(index, reversed)
	if err != nil || second.Encoded() != facts.Encoded() {
		t.Fatal("shared type table is not deterministic")
	}
	stored, err := DecodeFunctionScalarFacts(facts.Encoded())
	if err != nil {
		t.Fatal(err)
	}
	fresh := parseProductFunction(t, "\n"+functionProductSource)
	target, _ := IndexFunctionSource(fresh, productContext())
	output, err := stored.Restore(target)
	if err != nil {
		t.Fatal(err)
	}
	current := fresh.Body[0].(*ast.ReturnStmt).Value.(*ast.BinaryExpr)
	ref := output.ExprTypes[current]
	if ref == shared || ref != output.ExprTypes[current.Left] || output.ExprTypes[current.Right].(*OptionalType).Value != ref {
		t.Fatal("cross-expression type sharing lost")
	}
	repeated, err := stored.Restore(target)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.ExprTypes[current] == ref {
		t.Fatal("owned type table leaked between restores")
	}
}

func TestFunctionTypeSchemaAndTableRootGates(t *testing.T) {
	original := reflect.TypeOf(RefType{})
	fields := make([]reflect.StructField, original.NumField())
	for i := range fields {
		fields[i] = original.Field(i)
	}
	expected := ownedFunctionTypeSchemas[original]
	if err := validateFunctionTypeFields(reflect.StructOf(fields), expected); err != nil {
		t.Fatalf("unchanged synthetic schema rejected: %v", err)
	}
	fields[0].Type = reflect.TypeOf("")
	if err := validateFunctionTypeFields(reflect.StructOf(fields), expected); err == nil {
		t.Fatal("same-name retyped type field accepted")
	}
	fields[0] = original.Field(0)
	fields[0].Name = "NewElem"
	if err := validateFunctionTypeFields(reflect.StructOf(fields), expected); err == nil {
		t.Fatal("renamed type field accepted")
	}
	fn := parseProductFunction(t, functionProductSource)
	index, _ := IndexFunctionSource(fn, productContext())
	expr := fn.Body[0].(*ast.ReturnStmt).Value
	facts, err := CaptureFunctionScalarFacts(index, FunctionScalarObservations{ExprTypes: map[ast.Expr]Type{expr: &BuiltinType{Name: "i64"}}})
	if err != nil {
		t.Fatal(err)
	}
	var originalPayload scalarFactsPayload
	if err := json.Unmarshal([]byte(facts.Encoded()), &originalPayload); err != nil {
		t.Fatal(err)
	}
	for _, root := range []uint32{0, 999999} {
		payload := originalPayload
		payload.Entries = append([]scalarFactEntry(nil), originalPayload.Entries...)
		value := *payload.Entries[0].Type
		value.Root = root
		payload.Entries[0].Type = &value
		encoded, _ := json.Marshal(payload)
		if _, err := DecodeFunctionScalarFacts(string(encoded)); err == nil {
			t.Fatal("invalid shared type root accepted")
		}
	}
}
