package semantic

import (
	"fmt"
	"reflect"

	"elisacore/src/ast"
)

// FunctionSourceAnchor refers to a parser-produced node. Generated or replaced
// nodes require separate reconstruction recipes; they cannot borrow a nearby
// source node's identity. Semantic dependencies remain the product's obligation.
type FunctionSourceAnchor struct {
	FunctionKey string
	Tree        [32]byte
	Ordinal     uint32
	Kind        string
}

// FunctionSourceIndex is invocation-local. Build it before analysis, and discard
// it when the invocation ends. Only FunctionSourceAnchor values may be persisted.
// Source edits require a new index; semantic analysis may mutate the indexed AST.
type FunctionSourceIndex struct {
	root     *ast.FuncDecl
	key      string
	digest   [32]byte
	ordinals map[PositionedFunctionNode]uint32
}

func IndexFunctionSource(fn *ast.FuncDecl, scope FunctionIdentityContext) (*FunctionSourceIndex, error) {
	if _, err := FunctionProductRequirements(); err != nil {
		return nil, err
	}
	identity, err := NewFunctionIdentity(fn, scope)
	if err != nil {
		return nil, err
	}
	digest, nodes, err := functionSyntaxFingerprint(fn)
	if err != nil {
		return nil, err
	}
	ordinals := make(map[PositionedFunctionNode]uint32, len(nodes))
	for i, node := range nodes {
		ordinals[node] = uint32(i)
	}
	return &FunctionSourceIndex{fn, identity.Key(), digest, ordinals}, nil
}

// Capture survives semantic annotations and added lowering nodes, but admits
// only original source pointers still present in the analyzed declaration.
// Default-argument clones from another declaration deliberately fail here.
func (index *FunctionSourceIndex) Capture(nodes []PositionedFunctionNode) ([]FunctionSourceAnchor, error) {
	live, err := index.liveSourceNodes()
	if err != nil {
		return nil, err
	}
	anchors := make([]FunctionSourceAnchor, 0, len(nodes))
	for _, node := range nodes {
		value := reflect.ValueOf(node)
		if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
			return nil, fmt.Errorf("invalid function source node")
		}
		ordinal, ok := index.ordinals[node]
		if !ok || !live[node] {
			return nil, fmt.Errorf("node lacks a surviving function source anchor")
		}
		anchors = append(anchors, FunctionSourceAnchor{index.key, index.digest, ordinal, reflect.TypeOf(node).String()})
	}
	return anchors, nil
}

// liveSourceNodes is shared by strict capture and partial query publication.
// Failure never grants authority to a subset of an unvalidated source graph.
func (index *FunctionSourceIndex) liveSourceNodes() (map[PositionedFunctionNode]bool, error) {
	if index == nil {
		return nil, fmt.Errorf("missing function source index")
	}
	_, current, err := functionSyntaxFingerprint(index.root)
	if err != nil {
		return nil, err
	}
	live := make(map[PositionedFunctionNode]bool, len(current))
	for _, node := range current {
		live[node] = true
	}
	return live, nil
}

// Rebind returns fresh parsed nodes, including their current exact spans. The
// target must still be the pristine tree indexed before analysis. This handles
// independent spacing edits inside a body, without guessed offset adjustments.
func (index *FunctionSourceIndex) Rebind(anchors []FunctionSourceAnchor) ([]PositionedFunctionNode, error) {
	if index == nil {
		return nil, fmt.Errorf("missing function source index")
	}
	digest, current, err := functionSyntaxFingerprint(index.root)
	if err != nil {
		return nil, err
	}
	if digest != index.digest {
		return nil, fmt.Errorf("source anchor target is no longer pristine")
	}
	nodes := make([]PositionedFunctionNode, 0, len(anchors))
	for _, anchor := range anchors {
		if anchor.FunctionKey != index.key || anchor.Tree != digest || uint64(anchor.Ordinal) >= uint64(len(current)) {
			return nil, fmt.Errorf("source anchor does not match function inputs")
		}
		node := current[anchor.Ordinal]
		if reflect.TypeOf(node).String() != anchor.Kind {
			return nil, fmt.Errorf("source anchor kind mismatch")
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}
