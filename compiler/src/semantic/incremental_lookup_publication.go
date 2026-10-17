package semantic

import (
	"elisacore/src/incremental"
	"encoding/json"
	"fmt"
)

func lookupQueryEvaluators() map[string]incremental.Evaluator {
	return map[string]incremental.Evaluator{lookupSelectionQueryKind: evaluateLookupSelection, lookupAmbiguityQueryKind: evaluateLookupAmbiguity}
}

// Build a complete, bounded storage reservation from freshly prepared lookup
// inputs. This validates recipe/binding ownership before generic publication;
// it does not infer inputs from old traces or bind current Type/Symbol values.
func lookupPublication(key incremental.Key, changes []incremental.Change) (incremental.Publication, error) {
	var spec lookupSelectionSpec
	if err := decodeLookupSelection(key.Name, &spec); err != nil {
		return incremental.Publication{}, err
	}
	expectedKey, err := lookupSelectionKey(spec)
	if key.Kind == lookupAmbiguityQueryKind {
		expectedKey, err = lookupAmbiguityKey(spec)
	}
	if err != nil || expectedKey != key {
		return incremental.Publication{}, fmt.Errorf("invalid lookup publication key")
	}
	if len(changes) > lookupTraceMaxCandidates+2 {
		return incremental.Publication{}, fmt.Errorf("lookup publication input budget exceeded")
	}
	inputs := map[incremental.Key]incremental.Change{}
	for _, change := range changes {
		if change.Remove {
			change.Value = ""
		}
		if old, ok := inputs[change.Key]; ok && (old.Remove != change.Remove || old.Value != change.Value) {
			return incremental.Publication{}, fmt.Errorf("conflicting lookup input")
		}
		inputs[change.Key] = change
	}
	recipe, ok := inputs[lookupRecipeInput(key)]
	if !ok || recipe.Remove {
		return incremental.Publication{}, fmt.Errorf("missing lookup recipe")
	}
	expected := map[incremental.Key]bool{lookupRecipeInput(key): true}
	resultBytes := 64
	if key.Kind == lookupSelectionQueryKind {
		var shape lookupSelectionRecipe
		if err := decodeLookupSelection(recipe.Value, &shape); err != nil {
			return incremental.Publication{}, err
		}
		if err := validateLookupRecipe(spec, shape); err != nil {
			return incremental.Publication{}, err
		}
		canonical, _ := json.Marshal(shape)
		if string(canonical) != recipe.Value {
			return incremental.Publication{}, fmt.Errorf("noncanonical lookup recipe")
		}
		for _, name := range shape.Candidates {
			expected[lookupBindingInput(key, name)] = true
			encoded, _ := json.Marshal(lookupSelectionProduct{Name: name, Found: false})
			resultBytes = max(resultBytes, len(encoded))
		}
	} else {
		if recipe.Value != "ready" {
			return incremental.Publication{}, fmt.Errorf("invalid ambiguity readiness")
		}
		override, imports, err := lookupAmbiguityNames(spec)
		if err != nil {
			return incremental.Publication{}, err
		}
		if override != "" {
			expected[lookupBindingInput(key, override)] = true
		}
		for _, name := range imports {
			expected[lookupBindingInput(key, name)] = true
		}
		encoded, _ := json.Marshal(imports)
		resultBytes = max(resultBytes, len(encoded))
	}
	if len(inputs) != len(expected) || resultBytes > lookupSelectionMaxBytes {
		return incremental.Publication{}, fmt.Errorf("incomplete/oversized lookup publication")
	}
	for input := range expected {
		change, ok := inputs[input]
		if !ok {
			return incremental.Publication{}, fmt.Errorf("unpublished lookup binding")
		}
		if input != lookupRecipeInput(key) && !change.Remove && change.Value != "visible" && change.Value != "hidden" {
			return incremental.Publication{}, fmt.Errorf("invalid lookup binding state")
		}
	}
	owned := make([]incremental.Change, 0, len(inputs))
	for _, change := range inputs {
		owned = append(owned, change)
	}
	return incremental.Publication{Key: key, Inputs: owned, MaxResultBytes: resultBytes, MaxDependencies: len(inputs)}, nil
}
