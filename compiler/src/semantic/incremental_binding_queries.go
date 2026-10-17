package semantic

import (
	"crypto/sha256"
	"elisacore/src/incremental"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
)

const primitiveBindingSourceKind = "primitive-binding-source"
const primitiveLocalTypingKind = "primitive-local-typing"
const primitiveBindingRecipeKind = "binding-source-recipe"

type primitiveBindingDependency struct {
	Identity string
	Value    string `json:",omitempty"`
	Query    string `json:",omitempty"`
}
type primitiveBindingRecipe struct {
	Version      int
	Own          string
	Dependencies []primitiveBindingDependency
}

func primitiveBindingOwn(name, expression string, parameters []primitiveBindingDependency) string {
	data := []byte("binding-own-v1")
	for _, value := range []string{name, expression} {
		data = binary.LittleEndian.AppendUint64(data, uint64(len(value)))
		data = append(data, value...)
	}
	for _, parameter := range parameters {
		data = append(data, parameter.Identity...)
		data = binary.LittleEndian.AppendUint64(data, uint64(len(parameter.Value)))
		data = append(data, parameter.Value...)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func primitiveBindingSource(own string, dependencies []primitiveBindingDependency) string {
	data := []byte("binding-source-v1" + own)
	for _, dependency := range dependencies {
		data = append(data, dependency.Identity...)
		data = append(data, dependency.Value...)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func primitiveQueryEvaluators() map[string]incremental.Evaluator {
	return map[string]incremental.Evaluator{
		"primitive-return": func(reader *incremental.Reader, name string) (string, error) {
			source, ok := reader.Input(primitiveInput("function-source", name))
			if !ok {
				return "", errPrimitiveProductMiss
			}
			return evaluatePrimitiveProduct(reader, name, source, true)
		},
		primitiveLocalTypingKind: func(reader *incremental.Reader, name string) (string, error) {
			source, err := reader.Query(incremental.Key{Kind: primitiveBindingSourceKind, Name: name})
			if err != nil {
				return "", err
			}
			return evaluatePrimitiveProduct(reader, name, source, false)
		},
		primitiveBindingSourceKind: func(reader *incremental.Reader, name string) (string, error) {
			encoded, ok := reader.Input(primitiveInput(primitiveBindingRecipeKind, name))
			if !ok || len(encoded) > 512*257 {
				return "", errPrimitiveProductMiss
			}
			var recipe primitiveBindingRecipe
			if json.Unmarshal([]byte(encoded), &recipe) != nil || recipe.Version != 1 || len(recipe.Own) != 64 || len(recipe.Dependencies) > 256 {
				return "", errPrimitiveProductMiss
			}
			for i := range recipe.Dependencies {
				dependency := &recipe.Dependencies[i]
				if len(dependency.Identity) != 64 {
					return "", errPrimitiveProductMiss
				}
				if dependency.Query != "" {
					value, err := reader.Query(incremental.Key{Kind: primitiveBindingSourceKind, Name: dependency.Query})
					if err != nil {
						return "", err
					}
					if len(value) != 64 {
						return "", errPrimitiveProductMiss
					}
					dependency.Value = value
				} else if len(dependency.Value) != 64 {
					return "", errPrimitiveProductMiss
				}
			}
			return primitiveBindingSource(recipe.Own, recipe.Dependencies), nil
		},
	}
}

func evaluatePrimitiveProduct(reader *incremental.Reader, name, source string, allowPredicate bool) (string, error) {
	config, _ := reader.Input(primitiveConfigKey)
	encoded, ok := reader.Input(primitiveInput("function-product", name))
	if !ok {
		return "", errPrimitiveProductMiss
	}
	var product primitiveBodyProduct
	if json.Unmarshal([]byte(encoded), &product) != nil || product.Source != source || product.Config != config {
		return "", errPrimitiveProductMiss
	}
	safetyKey := primitiveSafetyKey
	expected := primitiveOperatorAbsence
	if product.Class == "predicate-v1" && allowPredicate {
		safetyKey = primitiveInput("predicate-safety", name)
		expected = predicateOperatorAbsence
		if !validPredicateTyping(product.Types) {
			return "", errPrimitiveProductMiss
		}
	} else if product.Class != "" || !validPrimitiveTyping(product.Types) {
		return "", errPrimitiveProductMiss
	}
	safety, _ := reader.Input(safetyKey)
	if safety != expected || product.Safety != safety {
		return "", errPrimitiveProductMiss
	}
	return product.Types, nil
}

func (session *IncrementalAnalysisSession) evictPrimitiveTyping(name string) {
	session.db.EvictQuery(incremental.Key{Kind: "primitive-return", Name: name})
	session.db.EvictQuery(incremental.Key{Kind: primitiveLocalTypingKind, Name: name})
}

func (invocation *incrementalBodyInvocation) registerBindingRecipes() {
	for _, input := range invocation.localInitializers {
		recipe := primitiveBindingRecipe{Version: 1, Own: input.bindingOwn, Dependencies: append([]primitiveBindingDependency(nil), input.dependencies...)}
		for i, binding := range input.localDependencies {
			if producer := invocation.localInitializers[binding]; producer != nil {
				recipe.Dependencies[i].Query = producer.key
				recipe.Dependencies[i].Value = ""
			}
		}
		encoded, _ := json.Marshal(recipe)
		if len(encoded) > input.bindingBytes {
			panic("binding recipe exceeded admitted encoding bound")
		}
		invocation.bindingBytes += len(encoded)
		invocation.bindingEdges += len(recipe.Dependencies)
		invocation.changes = append(invocation.changes, incremental.Change{Key: primitiveInput(primitiveBindingRecipeKind, input.key), Value: string(encoded)})
	}
}

const primitiveSessionMaxBindingBytes = 64 << 20
const primitiveSessionMaxBindingEdges = 65536

func (session *IncrementalAnalysisSession) bindingLimits() (int, int) {
	bytes, edges := primitiveSessionMaxBindingBytes, primitiveSessionMaxBindingEdges
	if session.maxBindingSourceBytes > 0 {
		bytes = session.maxBindingSourceBytes
	}
	if session.maxBindingSourceEdges > 0 {
		edges = session.maxBindingSourceEdges
	}
	return bytes, edges
}

func (invocation *incrementalBodyInvocation) reserveBinding(input *primitiveLocalInitializer) bool {
	// All stored identities/values are 64-byte digests, and query names are
	// bounded owner/name digests. 512 bytes bounds each encoded dependency plus
	// another512 for the recipe header; no raw source names enter recipes.
	bytes, edges := 512*(1+len(input.dependencies)), len(input.dependencies)
	byteLimit, edgeLimit := invocation.session.bindingLimits()
	if invocation.bindingReservedBytes+bytes > byteLimit || invocation.bindingReservedEdges+edges > edgeLimit {
		invocation.session.stats.BindingSpecAdmissionMisses++
		return false
	}
	input.bindingBytes = bytes
	input.bindingEdges = edges
	invocation.bindingReservedBytes += bytes
	invocation.bindingReservedEdges += edges
	return true
}
