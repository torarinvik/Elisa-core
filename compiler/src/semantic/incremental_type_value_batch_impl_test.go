package semantic

import (
	"elisacore/src/incremental"
	"fmt"
	"sort"
)

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
