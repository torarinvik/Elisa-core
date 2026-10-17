package semantic

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"unicode/utf8"
)

// FunctionTypeBinding is loader-authorized provenance, not a source-written
// #line. QualifiedName must identify the current resolved declaration. Named function
// types additionally require a current Result with completed effect inference.
type FunctionTypeBinding struct {
	Origin, QualifiedName string
	Type                  Type
}

// FunctionTypeEnvironment is invocation-local; products never retain it.
// Construct it from the current declaration environment, not a previous Result.
type FunctionTypeEnvironment struct {
	byKey         map[string]FunctionTypeBinding
	byType        map[Type]string
	currentResult *Result
}
type functionTypeReference struct {
	Key, Kind   string
	Fingerprint [32]byte
}
type functionTypeGraph struct {
	Root  uint32
	Nodes []functionTypeNode
}
type functionTypeNode struct {
	Kind      string
	Data      json.RawMessage        `json:",omitempty"`
	Children  []uint32               `json:",omitempty"`
	Reference *functionTypeReference `json:",omitempty"`
}

var semanticTypeInterface = reflect.TypeOf((*Type)(nil)).Elem()

// The exact field inventory is a schema gate. All fields are then walked; a
// new pointer/interface category cannot silently become an owned value.
var ownedFunctionTypeSchemas = map[reflect.Type]string{
	reflect.TypeOf(BuiltinType{}): "Name:string",
	reflect.TypeOf(BitIntType{}):  "Signed:bool Width:int",
	reflect.TypeOf(NeverType{}):   "", reflect.TypeOf(NullType{}): "",
	reflect.TypeOf(IDType{}):              "Tag:semantic.Type Storage:semantic.Type",
	reflect.TypeOf(AddressSpaceType{}):    "Space:string Elem:semantic.Type Storage:semantic.Type LayoutName:string",
	reflect.TypeOf(OptionalType{}):        "Value:semantic.Type",
	reflect.TypeOf(TupleType{}):           "Fields:[]semantic.TupleField",
	reflect.TypeOf(RefType{}):             "Elem:semantic.Type Mutable:bool State:semantic.RefState Storage:semantic.RefStorage Region:string ExplicitStorage:bool Linear:bool",
	reflect.TypeOf(ViewType{}):            "Elem:semantic.Type Mutable:bool Begin:string End:string SurfaceName:string Region:string",
	reflect.TypeOf(SViewType{}):           "Begin:string End:string Region:string",
	reflect.TypeOf(ArrayType{}):           "Elem:semantic.Type Size:string HasConstSize:bool ConstSize:int64 ConstParam:string SurfaceName:string ElemTypeExpr:ast.TypeExpr",
	reflect.TypeOf(DArrayType{}):          "Elem:semantic.Type Shape:semantic.Shape SurfaceName:string ElemTypeExpr:ast.TypeExpr Region:string",
	reflect.TypeOf(CStrType{}):            "Shape:semantic.Shape SurfaceName:string Region:string",
	reflect.TypeOf(DictType{}):            "Key:semantic.Type Value:semantic.Type SurfaceName:string Region:string",
	reflect.TypeOf(SetType{}):             "Elem:semantic.Type SurfaceName:string Region:string",
	reflect.TypeOf(DictEntryType{}):       "Dict:*semantic.DictType Mutable:bool",
	reflect.TypeOf(GenericInstanceType{}): "Name:string Base:semantic.Type Args:[]semantic.Type Region:string",
	reflect.TypeOf(AggregateStateType{}):  "Base:semantic.Type State:semantic.RefState States:[]semantic.RefState",
	reflect.TypeOf(TypeParamType{}):       "Name:string",
	reflect.TypeOf(ConstParamType{}):      "Name:string ValueType:semantic.Type",
	reflect.TypeOf(RegionParamType{}):     "Name:string",
	reflect.TypeOf(RegionValueType{}):     "Name:string",
	reflect.TypeOf(RefStorageValueType{}): "Storage:semantic.RefStorage",
	reflect.TypeOf(ErrorSetType{}):        "Name:string Tags:[]string Payloads:map[string][]semantic.Type Params:[]string",
	reflect.TypeOf(ErrorUnionType{}):      "Value:semantic.Type Errors:*semantic.ErrorSetType",
}
var nominalFunctionTypeKinds = map[string]reflect.Type{
	"StructType": reflect.TypeOf(StructType{}), "EnumType": reflect.TypeOf(EnumType{}),
	"ConstEnumType": reflect.TypeOf(ConstEnumType{}), "OpaqueType": reflect.TypeOf(OpaqueType{}),
	"ErrorSetType": reflect.TypeOf(ErrorSetType{}),
	"FuncType":     reflect.TypeOf(FuncType{}),
}

func NewFunctionTypeEnvironment(bindings []FunctionTypeBinding) (*FunctionTypeEnvironment, error) {
	return newFunctionTypeEnvironment(bindings, nil)
}

// NewFunctionTypeEnvironmentFromResult admits named function references only
// from this completed current analysis. It does not make other function/body
// products complete; loader provenance is still the caller's responsibility.
func NewFunctionTypeEnvironmentFromResult(bindings []FunctionTypeBinding, current *Result) (*FunctionTypeEnvironment, error) {
	return newFunctionTypeEnvironment(bindings, current)
}
func newFunctionTypeEnvironment(bindings []FunctionTypeBinding, current *Result) (*FunctionTypeEnvironment, error) {
	env := &FunctionTypeEnvironment{byKey: map[string]FunctionTypeBinding{}, byType: map[Type]string{}, currentResult: current}
	for _, binding := range bindings {
		kind, name, err := nominalFunctionTypeIdentity(binding.Type)
		if err != nil || binding.Origin == "" || binding.QualifiedName == "" || !utf8.ValidString(binding.Origin) || !utf8.ValidString(binding.QualifiedName) || name != binding.QualifiedName {
			return nil, fmt.Errorf("invalid current nominal type binding")
		}
		if len(binding.Origin) > functionScalarFactsMaxBytes-len(binding.QualifiedName) {
			return nil, fmt.Errorf("nominal identity byte budget exceeded")
		}
		if kind == "FuncType" {
			if err := validateCurrentFunctionTypeBinding(current, binding); err != nil {
				return nil, err
			}
		}
		encoded, _ := json.Marshal([]string{binding.Origin, binding.QualifiedName, kind})
		prefix := "nominal-v1"
		if kind == "FuncType" {
			prefix = "callee-type-v1"
		}
		key := fmt.Sprintf("%s:%x", prefix, sha256.Sum256(encoded))
		if _, exists := env.byKey[key]; exists {
			return nil, fmt.Errorf("duplicate nominal type binding")
		}
		if _, exists := env.byType[binding.Type]; exists {
			return nil, fmt.Errorf("ambiguous nominal type binding")
		}
		env.byKey[key] = binding
		env.byType[binding.Type] = key
	}
	return env, nil
}
func nominalFunctionTypeIdentity(t Type) (string, string, error) {
	v := reflect.ValueOf(t)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() {
		return "", "", fmt.Errorf("missing nominal type")
	}
	kind := v.Elem().Type().Name()
	if nominalFunctionTypeKinds[kind] != v.Elem().Type() {
		return "", "", fmt.Errorf("unsupported nominal type %T", t)
	}
	return kind, v.Elem().FieldByName("Name").String(), nil
}
func (env *FunctionTypeEnvironment) reference(t Type) (*functionTypeReference, error) {
	if env == nil {
		return nil, fmt.Errorf("nominal type requires current declaration environment")
	}
	key, ok := env.byType[t]
	if !ok {
		return nil, fmt.Errorf("nominal type is not bound in current environment")
	}
	binding := env.byKey[key]
	kind, name, err := nominalFunctionTypeIdentity(t)
	if err != nil || name != binding.QualifiedName {
		return nil, fmt.Errorf("nominal binding changed identity")
	}
	if kind == "FuncType" {
		if err := validateCurrentFunctionTypeBinding(env.currentResult, binding); err != nil {
			return nil, err
		}
	}
	// Conservative complete graph fingerprint, including layout, fields, drop
	// hooks and declaration metadata. Unsupported graphs decline capture. This
	// may over-invalidate on implementation changes; it never uses Name as shape.
	fingerprint, err := functionNominalTypeFingerprintWithResult(t, env.currentResult)
	if err != nil {
		return nil, err
	}
	return &functionTypeReference{key, kind, fingerprint}, nil
}

func functionNominalTypeFingerprint(t Type) ([32]byte, error) {
	return functionNominalTypeFingerprintWithResult(t, nil)
}
func functionNominalTypeFingerprintWithResult(t Type, current *Result) ([32]byte, error) {

	encoder := &functionSyntaxEncoder{seen: map[functionSyntaxPointer]uint64{}, semanticBitSets: true, skipFields: map[reflect.Type]map[string]bool{
		// types_enums.go derives this memo solely from the current Enum/Variant.
		// It is not a declaration input and may be populated lazily by codegen.
		reflect.TypeOf(EnumVariant{}): {"packedViewType": true},
	}}
	encoder.validateSemanticFunction = func(*FuncType) error {
		if current == nil || !current.PermissionEffectsConverged {
			return fmt.Errorf("nested function type requires completed current effects")
		}
		return nil
	}
	encoder.WriteString("elisa-nominal-interface-v1\x00")
	if err := encoder.encode(reflect.ValueOf(t)); err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(encoder.Bytes()), nil
}
func (env *FunctionTypeEnvironment) resolve(ref *functionTypeReference) (Type, error) {
	if env == nil || ref == nil {
		return nil, fmt.Errorf("missing current nominal environment")
	}
	binding, ok := env.byKey[ref.Key]
	if !ok {
		return nil, fmt.Errorf("nominal declaration no longer available")
	}
	current, err := env.reference(binding.Type)
	if err != nil {
		return nil, err
	}
	if *current != *ref {
		return nil, fmt.Errorf("nominal declaration fingerprint changed")
	}
	return binding.Type, nil
}

type functionTypeWalk struct{ work, stringBytes, encodedBudget int }

const functionTypeMaxNodes = 100_000
const functionTypeMaxDepth = 128

func (walk *functionTypeWalk) reserve(bytes int) error {
	if bytes > functionScalarFactsMaxBytes-walk.encodedBudget {
		return fmt.Errorf("function type byte budget exceeded")
	}
	walk.encodedBudget += bytes
	return nil
}

// copy transforms Type slots into graph edges while copying all owned scalars
// and containers. Non-nil AST/Shape/other interfaces require their own adapters.
func (walk *functionTypeWalk) copy(v reflect.Value, depth int, edge func(reflect.Value) (reflect.Value, error)) (reflect.Value, error) {
	if depth > functionTypeMaxDepth || walk.work >= functionSyntaxMaxWork {
		return reflect.Value{}, fmt.Errorf("function type traversal budget exceeded")
	}
	walk.work++
	if err := walk.reserve(128); err != nil {
		return reflect.Value{}, err
	}
	if v.Type().Implements(semanticTypeInterface) {
		return edge(v)
	}
	out := reflect.New(v.Type()).Elem()
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if err := walk.reserve(6 * len(v.Type().Field(i).Name)); err != nil {
				return reflect.Value{}, err
			}
			if !v.Type().Field(i).IsExported() {
				return reflect.Value{}, fmt.Errorf("unowned function type field")
			}
			child, err := walk.copy(v.Field(i), depth+1, edge)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Field(i).Set(child)
		}
	case reflect.Slice:
		if v.IsNil() {
			return out, nil
		}
		if v.Len() > functionSyntaxMaxWork-walk.work {
			return reflect.Value{}, fmt.Errorf("function type slice budget exceeded")
		}
		out = reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			child, err := walk.copy(v.Index(i), depth+1, edge)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(child)
		}
	case reflect.Map:
		if v.IsNil() {
			return out, nil
		}
		if v.Type().Key().Kind() != reflect.String || v.Len() > functionSyntaxMaxWork-walk.work {
			return reflect.Value{}, fmt.Errorf("unsupported function type map")
		}
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		out = reflect.MakeMapWithSize(v.Type(), len(keys))
		for _, key := range keys {
			if err := walk.reserve(6 * len(key.String())); err != nil {
				return reflect.Value{}, err
			}
			walk.stringBytes += len(key.String())
			if walk.stringBytes > functionScalarFactsMaxBytes {
				return reflect.Value{}, fmt.Errorf("function type string budget exceeded")
			}
			child, err := walk.copy(v.MapIndex(key), depth+1, edge)
			if err != nil {
				return reflect.Value{}, err
			}
			out.SetMapIndex(key, child)
		}
	case reflect.Interface, reflect.Pointer:
		if !v.IsNil() {
			return reflect.Value{}, fmt.Errorf("function type contains unsupported %s", v.Type())
		}
	case reflect.String:
		if err := walk.reserve(6 * v.Len()); err != nil {
			return reflect.Value{}, err
		}
		if v.Len() > functionScalarFactsMaxBytes-walk.stringBytes {
			return reflect.Value{}, fmt.Errorf("function type string budget exceeded")
		}
		walk.stringBytes += v.Len()
		out.Set(v)
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		out.Set(v)
	default:
		return reflect.Value{}, fmt.Errorf("unsupported owned function type field %s", v.Type())
	}
	return out, nil
}
func validateOwnedFunctionType(typ reflect.Type) error {
	expected, ok := ownedFunctionTypeSchemas[typ]
	if !ok {
		return fmt.Errorf("unsupported owned function type %s", typ)
	}
	return validateFunctionTypeFields(typ, expected)
}

func validateFunctionTypeFields(typ reflect.Type, expected string) error {
	fields := ""
	for i := 0; i < typ.NumField(); i++ {
		if i > 0 {
			fields += " "
		}
		fields += typ.Field(i).Name + ":" + typ.Field(i).Type.String()
	}
	if fields != expected {
		return fmt.Errorf("function type schema changed: %s", typ)
	}
	return nil
}
func captureFunctionTypeGraph(root Type, env *FunctionTypeEnvironment) (*functionTypeGraph, error) {
	graph, _, err := captureFunctionTypeGraphs([]Type{root}, env)
	return graph, err
}

func captureFunctionTypeGraphs(roots []Type, env *FunctionTypeEnvironment) (*functionTypeGraph, []uint32, error) {
	graph := &functionTypeGraph{}
	seen := map[Type]uint32{}
	walk := &functionTypeWalk{}
	var visit func(Type, int) (uint32, error)
	visit = func(t Type, depth int) (uint32, error) {
		v := reflect.ValueOf(t)
		if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() {
			return 0, fmt.Errorf("invalid function type graph node")
		}
		if id, ok := seen[t]; ok {
			return id, nil
		}
		if len(graph.Nodes) >= functionTypeMaxNodes || depth > functionTypeMaxDepth {
			return 0, fmt.Errorf("function type graph budget exceeded")
		}
		if err := walk.reserve(512 + 6*len(v.Elem().Type().Name())); err != nil {
			return 0, err
		}
		id := uint32(len(graph.Nodes) + 1)
		seen[t] = id
		graph.Nodes = append(graph.Nodes, functionTypeNode{})
		kind := v.Elem().Type().Name()
		if nominalFunctionTypeKinds[kind] == v.Elem().Type() && (kind != "ErrorSetType" || v.Elem().FieldByName("Name").String() != "") {
			ref, err := env.reference(t)
			if err != nil {
				return 0, err
			}
			graph.Nodes[id-1] = functionTypeNode{Kind: kind, Reference: ref}
			return id, nil
		}
		if err := validateOwnedFunctionType(v.Elem().Type()); err != nil {
			return 0, err
		}
		var children []uint32
		data, err := walk.copy(v.Elem(), 0, func(slot reflect.Value) (reflect.Value, error) {
			if slot.IsNil() {
				return reflect.Value{}, fmt.Errorf("missing composite child type")
			}
			child, ok := slot.Interface().(Type)
			if !ok {
				return reflect.Value{}, fmt.Errorf("invalid semantic type slot")
			}
			childID, err := visit(child, depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			children = append(children, childID)
			return reflect.Zero(slot.Type()), nil
		})
		if err != nil {
			return 0, err
		}
		encoded, err := json.Marshal(data.Interface())
		if err != nil {
			return 0, err
		}
		graph.Nodes[id-1] = functionTypeNode{Kind: kind, Data: encoded, Children: children}
		return id, nil
	}
	if len(roots) == 0 {
		return nil, nil, nil
	}
	ids := make([]uint32, len(roots))
	for i, root := range roots {
		id, err := visit(root, 0)
		if err != nil {
			return nil, nil, err
		}
		ids[i] = id
	}
	graph.Root = ids[0]
	encoded, err := json.Marshal(graph)
	if err != nil {
		return nil, nil, err
	}
	if len(encoded) > functionScalarFactsMaxBytes {
		return nil, nil, fmt.Errorf("function type graph byte budget exceeded")
	}
	// Validate before publishing; no missing field/edge can become a stored value.
	if _, err := graph.restore(env, false); err != nil {
		return nil, nil, err
	}
	return graph, ids, nil
}

func (graph *functionTypeGraph) restore(env *FunctionTypeEnvironment, validateOnly bool) (Type, error) {
	values, err := graph.restoreAll(env, validateOnly)
	if err != nil {
		return nil, err
	}
	return values[graph.Root-1], nil
}

func (graph *functionTypeGraph) restoreAll(env *FunctionTypeEnvironment, validateOnly bool) ([]Type, error) {
	if graph == nil || graph.Root == 0 || uint64(graph.Root) > uint64(len(graph.Nodes)) || len(graph.Nodes) > functionTypeMaxNodes {
		return nil, fmt.Errorf("invalid function type graph root")
	}
	values := make([]Type, len(graph.Nodes))
	walk := &functionTypeWalk{}
	types := map[string]reflect.Type{}
	for typ := range ownedFunctionTypeSchemas {
		types[typ.Name()] = typ
	}
	resolved := map[string]Type{}
	references := map[string]functionTypeReference{}
	for i, node := range graph.Nodes {
		if err := walk.reserve(512 + 6*len(node.Kind)); err != nil {
			return nil, err
		}
		if node.Reference != nil {
			ref := node.Reference
			typ, ok := nominalFunctionTypeKinds[node.Kind]
			if !ok || ref.Key == "" || ref.Kind != node.Kind || len(node.Data) != 0 || len(node.Children) != 0 {
				return nil, fmt.Errorf("invalid nominal type recipe")
			}
			if previous, ok := references[ref.Key]; ok && previous != *ref {
				return nil, fmt.Errorf("inconsistent nominal type references")
			}
			references[ref.Key] = *ref
			value, ok := resolved[ref.Key]
			if !ok {
				var err error
				if validateOnly {
					value = reflect.New(typ).Interface().(Type)
				} else {
					value, err = env.resolve(ref)
				}
				if err != nil {
					return nil, err
				}
				resolved[ref.Key] = value
			}
			values[i] = value
			continue
		}
		typ, ok := types[node.Kind]
		if !ok {
			return nil, fmt.Errorf("unsupported function type recipe %q", node.Kind)
		}
		if err := validateOwnedFunctionType(typ); err != nil {
			return nil, err
		}
		values[i] = reflect.New(typ).Interface().(Type)
	}
	for i, node := range graph.Nodes {
		if node.Reference != nil {
			continue
		}
		v := reflect.ValueOf(values[i])
		decoder := json.NewDecoder(bytes.NewReader(node.Data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(v.Interface()); err != nil {
			return nil, err
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return nil, fmt.Errorf("trailing function type data")
		}
		next := 0
		data, err := walk.copy(v.Elem(), 0, func(slot reflect.Value) (reflect.Value, error) {
			if !slot.IsNil() || next >= len(node.Children) {
				return reflect.Value{}, fmt.Errorf("invalid function type edges")
			}
			id := node.Children[next]
			next++
			if id == 0 {
				return reflect.Value{}, fmt.Errorf("missing composite child type")
			}
			if uint64(id) > uint64(len(values)) {
				return reflect.Value{}, fmt.Errorf("function type edge out of bounds")
			}
			child := reflect.ValueOf(values[id-1])
			if !child.Type().AssignableTo(slot.Type()) {
				return reflect.Value{}, fmt.Errorf("function type edge kind mismatch")
			}
			return child, nil
		})
		if err != nil {
			return nil, err
		}
		if next != len(node.Children) {
			return nil, fmt.Errorf("extra function type edges")
		}
		v.Elem().Set(data)
		if errors, ok := values[i].(*ErrorSetType); ok && errors.Name != "" {
			return nil, fmt.Errorf("named errorset requires a current nominal reference")
		}
		if scalar, ok := scalarLeafFromType(values[i]); ok {
			if _, err := restoreScalarType(scalar); err != nil {
				return nil, err
			}
		}
		if ref, ok := values[i].(*RefType); ok && (ref.State < RefStateNonNull || ref.State > RefStateNull || ref.Storage < RefStorageAny || ref.Storage > RefStorageStatic) {
			return nil, fmt.Errorf("invalid reference flags")
		}
		if storage, ok := values[i].(*RefStorageValueType); ok && (storage.Storage < RefStorageAny || storage.Storage > RefStorageStatic) {
			return nil, fmt.Errorf("invalid storage discriminator")
		}
		if aggregate, ok := values[i].(*AggregateStateType); ok {
			if aggregate.State < RefStateNonNull || aggregate.State > RefStateNull {
				return nil, fmt.Errorf("invalid aggregate state")
			}
			for _, state := range aggregate.States {
				if state < RefStateNonNull || state > RefStateNull {
					return nil, fmt.Errorf("invalid aggregate field state")
				}
			}
		}
	}
	return values, nil
}
func scalarLeafFromType(t Type) (*scalarTypeValue, bool) {
	switch t := t.(type) {
	case *BuiltinType:
		return &scalarTypeValue{Kind: "builtin", Name: t.Name}, true
	case *BitIntType:
		return &scalarTypeValue{Kind: "bitint", Signed: t.Signed, Width: t.Width}, true
	case *NeverType:
		return &scalarTypeValue{Kind: "never"}, true
	case *NullType:
		return &scalarTypeValue{Kind: "null"}, true
	}
	return nil, false
}

// FunctionTypeDependency is an owned declaration-backed type input to the enclosing
// query. It is only the type-reference portion of that query's dependencies.
type FunctionTypeDependency struct {
	Key         string
	Fingerprint [32]byte
}

func (env *FunctionTypeEnvironment) CurrentDependency(key string) (FunctionTypeDependency, error) {
	if env == nil {
		return FunctionTypeDependency{}, fmt.Errorf("missing current type environment")
	}
	binding, ok := env.byKey[key]
	if !ok {
		return FunctionTypeDependency{}, fmt.Errorf("nominal declaration no longer available")
	}
	ref, err := env.reference(binding.Type)
	if err != nil {
		return FunctionTypeDependency{}, err
	}
	return FunctionTypeDependency{ref.Key, ref.Fingerprint}, nil
}

func (facts FunctionScalarFacts) TypeDependencies() ([]FunctionTypeDependency, error) {
	payload, err := decodeScalarFacts(facts.encoded)
	if err != nil {
		return nil, err
	}
	refs := map[string][32]byte{}
	if payload.Types != nil {
		for _, node := range payload.Types.Nodes {
			if node.Reference == nil {
				continue
			}
			ref := node.Reference
			if previous, ok := refs[ref.Key]; ok && previous != ref.Fingerprint {
				return nil, fmt.Errorf("inconsistent nominal dependencies")
			}
			refs[ref.Key] = ref.Fingerprint
		}
	}
	output := make([]FunctionTypeDependency, 0, len(refs))
	for key, fingerprint := range refs {
		output = append(output, FunctionTypeDependency{key, fingerprint})
	}
	sort.Slice(output, func(i, j int) bool { return output[i].Key < output[j].Key })
	return output, nil
}

// This check is repeated at use: an environment is not a memo of a completion
// flag or a substitute for a current function symbol. Type replacement/removal
// after construction must fail instead of returning the former declaration.
func validateCurrentFunctionTypeBinding(current *Result, binding FunctionTypeBinding) error {
	if current == nil || !current.PermissionEffectsConverged || current.GlobalScope == nil {
		return fmt.Errorf("function type requires completed current effect inference")
	}
	symbol, found := current.GlobalScope.Symbols[binding.QualifiedName]
	if !found || symbol == nil || symbol.Kind != SymbolFunc || symbol.Type != binding.Type {
		return fmt.Errorf("function type is not the current declaration binding")
	}
	return nil
}

// Encode semantic set membership, including overflow words, without reading
// private representation through reflection or ignoring meaningful bits. The
// field inventory fails closed if IntBitSet gains additional state.
func (e *functionSyntaxEncoder) encodeSemanticIntBitSet(set IntBitSet) error {
	if err := validateFunctionTypeFields(reflect.TypeOf(set), "inline:uint64 extra:[]uint64"); err != nil {
		return err
	}
	remaining := functionSyntaxMaxBytes - e.Len()
	if len(set.extra) > functionSyntaxMaxWork-e.work || remaining < 16 || len(set.extra) > (remaining-16)/8 {
		return ErrFunctionSyntaxBudget
	}
	e.work += len(set.extra)
	used := len(set.extra)
	for used > 0 && set.extra[used-1] == 0 {
		used--
	}
	e.number(set.inline)
	e.number(uint64(used))
	for _, word := range set.extra[:used] {
		e.number(word)
	}
	return e.failure
}
