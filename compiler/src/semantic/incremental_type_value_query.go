package semantic

import (
	"bytes"
	"elisacore/src/incremental"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

const functionTypeValueQueryKind = "function-type-value-v1"
const functionTypeValueMaxDeclarations = 4096

type functionTypeValueSpec struct {
	Site FunctionSourceAnchor
	Role string
}

func functionTypeValueKey(spec functionTypeValueSpec) (incremental.Key, error) {
	if spec.Site.FunctionKey == "" || spec.Site.Tree == ([32]byte{}) || spec.Site.Kind == "" || spec.Role == "" {
		return incremental.Key{}, fmt.Errorf("type value requires source owner/site/role")
	}
	size := 0
	for _, value := range []string{spec.Site.FunctionKey, spec.Site.Kind, spec.Role} {
		if !utf8.ValidString(value) || len(value) > lookupSelectionMaxBytes/6-size {
			return incremental.Key{}, fmt.Errorf("type value key budget exceeded")
		}
		size += len(value)
	}
	encoded, err := json.Marshal(spec)
	if err != nil || len(encoded) > lookupSelectionMaxBytes {
		return incremental.Key{}, fmt.Errorf("invalid type value key")
	}
	return incremental.Key{Kind: functionTypeValueQueryKind, Name: string(encoded)}, nil
}
func functionTypeValueRecipeInput(key incremental.Key) incremental.Key {
	return incremental.Key{Kind: "function-type-recipe-v1", Name: key.Name}
}
func functionTypeValueReadyInput(key incremental.Key) incremental.Key {
	return incremental.Key{Kind: "function-type-ready-v1", Name: key.Name}
}
func functionDeclarationFingerprintInput(name string) incremental.Key {
	return incremental.Key{Kind: "declaration-type-fingerprint-v1", Name: name}
}

func decodeFunctionTypeValue(encoded string, target any) error {
	if len(encoded) > functionScalarFactsMaxBytes || !utf8.ValidString(encoded) {
		return fmt.Errorf("type value encoding budget exceeded")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("trailing type value data")
	}
	canonical, err := json.Marshal(target)
	if err != nil || string(canonical) != encoded {
		return fmt.Errorf("noncanonical type value encoding")
	}
	return nil
}
func typeValueDeclarationKeys(graph *functionTypeGraph) ([]string, error) {
	if graph == nil || len(graph.Nodes) > functionTypeMaxNodes {
		return nil, fmt.Errorf("invalid type graph")
	}
	keys := map[string]bool{}
	budget := 0
	for _, node := range graph.Nodes {
		if len(node.Children) > functionTypeMaxNodes {
			return nil, fmt.Errorf("type edge budget exceeded")
		}
		size := 512 + 6*len(node.Kind) + 6*len(node.Data) + 12*len(node.Children)
		if node.Reference != nil {
			size += 6*len(node.Reference.Key) + 6*len(node.Reference.Kind)
		}
		if size > functionScalarFactsMaxBytes-budget {
			return nil, fmt.Errorf("type recipe preflight budget exceeded")
		}
		budget += size

		if node.Reference == nil {
			continue
		}
		ref := node.Reference
		if ref.Fingerprint != ([32]byte{}) || ref.Key == "" || !utf8.ValidString(ref.Key) || len(ref.Key) > lookupSelectionMaxBytes {
			return nil, fmt.Errorf("type recipe must carry declaration identity, not old fingerprints")
		}
		keys[ref.Key] = true
		if len(keys) > functionTypeValueMaxDeclarations {
			return nil, fmt.Errorf("type declaration budget exceeded")
		}
	}
	if _, err := graph.restore(nil, true); err != nil {
		return nil, err
	}
	output := make([]string, 0, len(keys))
	for key := range keys {
		output = append(output, key)
	}
	sort.Strings(output)
	return output, nil
}
func evaluateFunctionTypeValue(r *incremental.Reader, name string) (string, error) {
	var spec functionTypeValueSpec
	if err := decodeFunctionTypeValue(name, &spec); err != nil {
		return "", err
	}
	key, err := functionTypeValueKey(spec)
	if err != nil || key.Name != name {
		return "", fmt.Errorf("invalid type value query")
	}
	if ready, present := r.Input(functionTypeValueReadyInput(key)); !present || ready != "ready" {
		return "", fmt.Errorf("type value environment unavailable")
	}
	encoded, present := r.Input(functionTypeValueRecipeInput(key))
	if !present {
		return "", fmt.Errorf("type value recipe unavailable")
	}
	var graph functionTypeGraph
	if err := decodeFunctionTypeValue(encoded, &graph); err != nil {
		return "", err
	}
	keys, err := typeValueDeclarationKeys(&graph)
	if err != nil {
		return "", err
	}
	current := map[string][32]byte{}
	kinds := map[string]string{}
	for _, name := range keys {
		encoded, present := r.Input(functionDeclarationFingerprintInput(name))
		kind, hexValue, valid := strings.Cut(encoded, ":")
		if !present || !valid || nominalFunctionTypeKinds[kind] == nil || len(hexValue) != 64 {
			return "", fmt.Errorf("current type declaration unavailable")
		}
		data, err := hex.DecodeString(hexValue)
		if err != nil || hex.EncodeToString(data) != hexValue {
			return "", fmt.Errorf("invalid declaration fingerprint")
		}
		var fingerprint [32]byte
		copy(fingerprint[:], data)
		current[name] = fingerprint
		kinds[name] = kind
	}
	for i := range graph.Nodes {
		if ref := graph.Nodes[i].Reference; ref != nil {
			if kinds[ref.Key] != ref.Kind {
				return "", fmt.Errorf("type declaration kind changed")
			}
			ref.Fingerprint = current[ref.Key]
		}
	}
	data, err := json.Marshal(graph)
	if err != nil || len(data) > functionScalarFactsMaxBytes {
		return "", fmt.Errorf("type value product budget exceeded")
	}
	return string(data), nil
}

// A retained recipe describes owned type structure and referenced declarations.
// All changing declaration fingerprints come from the CURRENT environment. This
// does not validate name selection, generic context or complete body coverage.
// Declaration-backed values are final-stage products: the environment must have
// a completed current Result, including for nested function signatures.
func functionTypeValuePublication(spec functionTypeValueSpec, source *functionTypeGraph, env *FunctionTypeEnvironment) (incremental.Publication, error) {
	return functionTypeValuePublicationWithDependencies(spec, source, env, env.CurrentDependency)
}
func functionTypeValuePublicationWithDependencies(spec functionTypeValueSpec, source *functionTypeGraph, env *FunctionTypeEnvironment, current func(string) (FunctionTypeDependency, error)) (incremental.Publication, error) {

	key, err := functionTypeValueKey(spec)
	if err != nil {
		return incremental.Publication{}, err
	}
	if source == nil || len(source.Nodes) > functionTypeMaxNodes {
		return incremental.Publication{}, fmt.Errorf("missing/oversized type graph")
	}
	historical := map[string]functionTypeReference{}
	for _, node := range source.Nodes {
		if ref := node.Reference; ref != nil {
			if previous, exists := historical[ref.Key]; exists && previous != *ref {
				return incremental.Publication{}, fmt.Errorf("inconsistent source type references")
			}
			historical[ref.Key] = *ref
			if len(historical) > functionTypeValueMaxDeclarations {
				return incremental.Publication{}, fmt.Errorf("source type declaration budget exceeded")
			}
		}
	}
	graph := *source
	graph.Nodes = append([]functionTypeNode(nil), source.Nodes...)
	for i, node := range graph.Nodes {
		if node.Reference != nil {
			ref := *node.Reference
			ref.Fingerprint = [32]byte{}
			graph.Nodes[i].Reference = &ref
		}
	}
	keys, err := typeValueDeclarationKeys(&graph)
	if err != nil {
		return incremental.Publication{}, err
	}
	if len(keys) > 0 && (env == nil || env.currentResult == nil || !env.currentResult.PermissionEffectsConverged) {
		return incremental.Publication{}, fmt.Errorf("declaration type values require completed current environment")
	}
	recipe, err := json.Marshal(graph)
	if err != nil || len(recipe) > functionScalarFactsMaxBytes {
		return incremental.Publication{}, fmt.Errorf("type recipe budget exceeded")
	}
	inputs := []incremental.Change{{Key: functionTypeValueReadyInput(key), Value: "ready"}, {Key: functionTypeValueRecipeInput(key), Value: string(recipe)}}
	fingerprints := map[string][32]byte{}
	expectedKinds := map[string]string{}
	for _, node := range graph.Nodes {
		if ref := node.Reference; ref != nil {
			expectedKinds[ref.Key] = ref.Kind
		}
	}
	for _, name := range keys {
		dependency, err := current(name)
		if err != nil {
			return incremental.Publication{}, err
		}
		kind, _, err := nominalFunctionTypeIdentity(env.byKey[name].Type)
		if err != nil {
			return incremental.Publication{}, err
		}
		if expectedKinds[name] != kind {
			return incremental.Publication{}, fmt.Errorf("type recipe declaration kind mismatch")
		}
		inputs = append(inputs, incremental.Change{Key: functionDeclarationFingerprintInput(name), Value: kind + ":" + hex.EncodeToString(dependency.Fingerprint[:])})
		fingerprints[name] = dependency.Fingerprint
	}
	for i := range graph.Nodes {
		if ref := graph.Nodes[i].Reference; ref != nil {
			ref.Fingerprint = fingerprints[ref.Key]
		}
	}
	output, err := json.Marshal(graph)
	if err != nil || len(output) > functionScalarFactsMaxBytes {
		return incremental.Publication{}, fmt.Errorf("type product budget exceeded")
	}
	return incremental.Publication{Key: key, Inputs: inputs, MaxDependencies: len(inputs), MaxResultBytes: len(output)}, nil
}
func functionTypeValueEvaluators() map[string]incremental.Evaluator {
	return map[string]incremental.Evaluator{functionTypeValueQueryKind: evaluateFunctionTypeValue, functionScalarTypeGateKind: evaluateFunctionScalarTypeGate}
}

type functionTypeValueRequest struct {
	Spec  functionTypeValueSpec
	Graph *functionTypeGraph
}
type functionTypeValueBatch struct {
	Publications                             []incremental.Publication
	Declarations, AccountedConstructionBytes int
}

// Build within one exclusive compiler metadata snapshot/PublishPrepared call.
// The memo is local to this batch: later mutations or publications must fetch
// current fingerprints again. It never persists mutable Type/Result pointers.
func prepareFunctionTypeValueBatch(requests []functionTypeValueRequest, env *FunctionTypeEnvironment) (functionTypeValueBatch, error) {
	result := functionTypeValueBatch{}
	if len(requests) > lookupTraceMaxEntries {
		return result, fmt.Errorf("type value request budget exceeded")
	}
	dependencies := map[string]FunctionTypeDependency{}
	current := func(key string) (FunctionTypeDependency, error) {
		if value, present := dependencies[key]; present {
			return value, nil
		}
		if len(dependencies) >= lookupTraceMaxEntries {
			return FunctionTypeDependency{}, fmt.Errorf("type value batch declaration budget exceeded")
		}
		value, err := env.CurrentDependency(key)
		if err != nil {
			return value, err
		}
		dependencies[key] = value
		return value, nil
	}
	seen := map[incremental.Key]bool{}
	for _, request := range requests {
		publication, err := functionTypeValuePublicationWithDependencies(request.Spec, request.Graph, env, current)
		if err != nil {
			return functionTypeValueBatch{}, err
		}
		if seen[publication.Key] {
			return functionTypeValueBatch{}, fmt.Errorf("duplicate type value request")
		}
		seen[publication.Key] = true
		bytes := len(publication.Key.Kind) + len(publication.Key.Name) + publication.MaxResultBytes
		for _, input := range publication.Inputs {
			bytes += len(input.Key.Kind) + len(input.Key.Name) + len(input.Value) + 128
		}
		if bytes > (256<<20)-result.AccountedConstructionBytes {
			return functionTypeValueBatch{}, fmt.Errorf("type value batch construction budget exceeded")
		}
		result.AccountedConstructionBytes += bytes
		result.Publications = append(result.Publications, publication)
	}
	sort.Slice(result.Publications, func(i, j int) bool { return result.Publications[i].Key.Name < result.Publications[j].Key.Name })
	result.Declarations = len(dependencies)
	return result, nil
}
