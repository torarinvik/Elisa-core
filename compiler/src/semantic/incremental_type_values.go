package semantic

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

// DiagnosticCodegenTypeFingerprint captures the supported semantic type graph
// for comparison experiments. It is not a complete code-generation dependency
// certificate: callers must separately validate source, results, target settings,
// proof facts, and every other input. Unsupported graphs fail closed.
func DiagnosticCodegenTypeFingerprint(t Type, current *Result) ([32]byte, error) {
	if t == nil || current == nil {
		return [32]byte{}, fmt.Errorf("missing diagnostic type or result")
	}
	return functionNominalTypeFingerprintWithResult(t, current)
}

func functionNominalTypeFingerprintWithResult(t Type, current *Result) ([32]byte, error) {

	encoder := &functionSyntaxEncoder{seen: map[functionSyntaxPointer]uint64{}, semanticBitSets: true, skipFields: map[reflect.Type]map[string]bool{
		// types_enums.go derives this memo solely from the current Enum/Variant.
		// It is not a declaration input and may be populated lazily by codegen.
		reflect.TypeOf(EnumVariant{}): {"packedViewType": true},
		// Declaration field order is already encoded through Decl. This memo
		// only caches that flattening and is not an additional semantic input.
		reflect.TypeOf(StructType{}): {"declarationFieldOrder": true},
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
