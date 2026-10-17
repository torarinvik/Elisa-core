package semantic

import (
	"fmt"
	"reflect"
)

// Invocation-only liveness batching. All requested nodes for an owner are
// captured together, so a large body is traversed once, not once per lookup.
// The batch is never retained by Session or returned in a persistent product.
type lexicalAnchorBatch struct {
	nodes     map[*FunctionSourceIndex]map[PositionedFunctionNode]FunctionSourceAnchor
	validated bool
	scans     int
}

func (b *lexicalAnchorBatch) add(index *FunctionSourceIndex, node PositionedFunctionNode) (FunctionSourceAnchor, error) {
	if b.validated {
		return FunctionSourceAnchor{}, fmt.Errorf("lexical anchor batch is sealed")
	}
	value := reflect.ValueOf(node)
	if index == nil || !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return FunctionSourceAnchor{}, fmt.Errorf("missing lexical source node")
	}
	ordinal, ok := index.ordinals[node]
	if !ok {
		return FunctionSourceAnchor{}, fmt.Errorf("lexical node is generated/foreign")
	}
	if b.nodes == nil {
		b.nodes = map[*FunctionSourceIndex]map[PositionedFunctionNode]FunctionSourceAnchor{}
	}
	if b.nodes[index] == nil {
		b.nodes[index] = map[PositionedFunctionNode]FunctionSourceAnchor{}
	}
	anchor := FunctionSourceAnchor{FunctionKey: index.key, Tree: index.digest, Ordinal: ordinal, Kind: value.Type().String()}
	b.nodes[index][node] = anchor
	return anchor, nil
}
func (b *lexicalAnchorBatch) validate() error {
	b.validated = false
	for index, entries := range b.nodes {
		nodes := make([]PositionedFunctionNode, 0, len(entries))
		for node := range entries {
			nodes = append(nodes, node)
		}
		anchors, err := index.Capture(nodes)
		b.scans++
		if err != nil {
			return err
		}
		for i, node := range nodes {
			entries[node] = anchors[i]
		}
	}
	b.validated = true
	return nil
}
func (b *lexicalAnchorBatch) anchor(index *FunctionSourceIndex, node PositionedFunctionNode) (FunctionSourceAnchor, error) {
	if !b.validated {
		return FunctionSourceAnchor{}, fmt.Errorf("lexical anchor batch not validated")
	}
	anchor, ok := b.nodes[index][node]
	if !ok {
		return FunctionSourceAnchor{}, fmt.Errorf("uncaptured lexical node")
	}
	return anchor, nil
}
