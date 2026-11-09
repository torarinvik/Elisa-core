package semantic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
)

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
