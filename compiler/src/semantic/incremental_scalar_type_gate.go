package semantic

import (
	"elisacore/src/incremental"
	"encoding/json"
	"fmt"
)

const functionScalarTypeGateKind = "function-scalar-type-gate-v1"

func functionScalarTypeGateKey(spec functionTypeValueSpec) (incremental.Key, error) {
	key, err := functionTypeValueKey(spec)
	key.Kind = functionScalarTypeGateKind
	return key, err
}
func functionScalarTypeFactsInput(key incremental.Key) incremental.Key {
	return incremental.Key{Kind: "stored-function-scalar-facts-v1", Name: key.Name}
}

// This validates ONLY the declaration-Type portion of a retained fact's
// dependencies. Name selection, operators, constants, flow/proof inputs and
// complete body coverage must be validated separately before semantic replay.
func evaluateFunctionScalarTypeGate(r *incremental.Reader, name string) (string, error) {
	var spec functionTypeValueSpec
	if err := decodeFunctionTypeValue(name, &spec); err != nil {
		return "", err
	}
	key, err := functionScalarTypeGateKey(spec)
	if err != nil || key.Name != name {
		return "", fmt.Errorf("invalid scalar type gate")
	}
	encoded, present := r.Input(functionScalarTypeFactsInput(key))
	if !present {
		return "", fmt.Errorf("stored scalar facts unavailable")
	}
	payload, err := decodeScalarFacts(encoded)
	if err != nil {
		return "", err
	}
	if payload.FunctionKey != spec.Site.FunctionKey || payload.Tree != spec.Site.Tree {
		return "", fmt.Errorf("scalar type gate owner mismatch")
	}
	if payload.Types != nil {
		child, _ := functionTypeValueKey(spec)
		current, err := r.Query(child)
		if err != nil {
			return "", err
		}
		expected, err := json.Marshal(payload.Types)
		if err != nil {
			return "", err
		}
		if current != string(expected) {
			return "", fmt.Errorf("scalar facts require fresh typing after declaration change")
		}
	}
	return encoded, nil
}
func functionScalarTypeGatePublication(spec functionTypeValueSpec, facts FunctionScalarFacts) (incremental.Publication, error) {
	key, err := functionScalarTypeGateKey(spec)
	if err != nil {
		return incremental.Publication{}, err
	}
	payload, err := decodeScalarFacts(facts.Encoded())
	if err != nil {
		return incremental.Publication{}, err
	}
	if payload.FunctionKey != spec.Site.FunctionKey || payload.Tree != spec.Site.Tree {
		return incremental.Publication{}, fmt.Errorf("scalar type gate owner mismatch")
	}
	dependencies := 1
	if payload.Types != nil {
		dependencies++
	}
	return incremental.Publication{Key: key, Inputs: []incremental.Change{{Key: functionScalarTypeFactsInput(key), Value: facts.Encoded()}}, MaxDependencies: dependencies, MaxResultBytes: len(facts.Encoded())}, nil
}
