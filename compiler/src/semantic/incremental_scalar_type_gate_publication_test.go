package semantic

import (
	"elisacore/src/incremental"
	"fmt"
)

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
