package semantic

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// FunctionIdentityContext must come from resolved declarations and loader
// provenance, not from a function's leaf spelling or a source-written #line.
// Specialization entries are canonical semantic type/constant identities, in
// binder order. Captured lexical environments need their own tracked input.
type FunctionIdentityContext struct {
	Origin         string
	QualifiedName  string
	ImplKey        string
	Specialization []string
}

type FunctionIdentity struct {
	Context   FunctionIdentityContext
	Signature [32]byte
}

// NewFunctionIdentity operates on declaration syntax before semantic mutation.
// A body edit preserves identity; a changed contract/signature does not.
func NewFunctionIdentity(fn *ast.FuncDecl, scope FunctionIdentityContext) (FunctionIdentity, error) {
	if fn == nil || strings.TrimSpace(scope.Origin) == "" || strings.TrimSpace(scope.QualifiedName) == "" {
		return FunctionIdentity{}, fmt.Errorf("function identity requires declaration, resolved name and source origin")
	}
	signature := *fn
	signature.Body = nil
	digest, _, err := functionSyntaxFingerprint(&signature)
	if err != nil {
		return FunctionIdentity{}, err
	}
	scope.Specialization = append([]string{}, scope.Specialization...)
	return FunctionIdentity{scope, digest}, nil
}
func (id FunctionIdentity) Key() string {
	encoded, _ := json.Marshal(id)
	return fmt.Sprintf("function-v1:%x", sha256.Sum256(encoded))
}

// FunctionNodeAnchor contains no pointer or previous source position. It is
// valid only for an exactly matching position-independent syntax graph.
// Structural compatibility does not authorize semantic reuse: the enclosing
// product must also validate function identity, dependencies and configuration.
type FunctionNodeAnchor struct {
	Tree    [32]byte
	Ordinal uint32
	Kind    string
}
type PositionedFunctionNode interface{ Pos() lexer.Pos }
type FunctionNodeIndex struct {
	root   *ast.FuncDecl
	digest [32]byte
}

// IndexFunctionNodes indexes the actual tree supplied (parsed or lowered).
// A lowered product must restore its lowered tree before rebinding its metadata;
// a parsed tree with different synthesized nodes is deliberately rejected.
func IndexFunctionNodes(fn *ast.FuncDecl) (*FunctionNodeIndex, error) {
	if _, err := FunctionProductRequirements(); err != nil {
		return nil, err
	}
	if fn == nil {
		return nil, fmt.Errorf("missing function")
	}
	digest, _, err := functionSyntaxFingerprint(fn)
	if err != nil {
		return nil, err
	}
	return &FunctionNodeIndex{root: fn, digest: digest}, nil
}
func (index *FunctionNodeIndex) Anchor(node PositionedFunctionNode) (FunctionNodeAnchor, error) {
	anchors, err := index.Anchors([]PositionedFunctionNode{node})
	if err != nil {
		return FunctionNodeAnchor{}, err
	}
	return anchors[0], nil
}

// Anchors validates the source graph once before exporting product references.
// RebindAll performs the corresponding validation on the destination graph.
func (index *FunctionNodeIndex) Anchors(nodes []PositionedFunctionNode) ([]FunctionNodeAnchor, error) {
	if index == nil {
		return nil, fmt.Errorf("missing function node index")
	}
	digest, current, err := functionSyntaxFingerprint(index.root)
	if err != nil || digest != index.digest {
		return nil, fmt.Errorf("indexed function tree changed before capture")
	}
	ordinals := make(map[PositionedFunctionNode]uint32, len(current))
	for i, node := range current {
		ordinals[node] = uint32(i)
	}
	output := make([]FunctionNodeAnchor, 0, len(nodes))
	for _, node := range nodes {
		if node == nil || reflect.ValueOf(node).Kind() != reflect.Pointer {
			return nil, fmt.Errorf("invalid function node")
		}
		ordinal, ok := ordinals[node]
		if !ok {
			return nil, fmt.Errorf("node does not belong to current function tree")
		}
		output = append(output, FunctionNodeAnchor{index.digest, ordinal, reflect.TypeOf(node).String()})
	}
	return output, nil
}

func (index *FunctionNodeIndex) Rebind(anchor FunctionNodeAnchor) (PositionedFunctionNode, error) {
	nodes, err := index.RebindAll([]FunctionNodeAnchor{anchor})
	if err != nil {
		return nil, err
	}
	return nodes[0], nil
}

// RebindAll validates the current tree once for the complete product. Rebuilding
// its node list also handles equivalent nodes replaced since index creation.
func (index *FunctionNodeIndex) RebindAll(anchors []FunctionNodeAnchor) ([]PositionedFunctionNode, error) {
	if index == nil {
		return nil, fmt.Errorf("missing function node index")
	}
	digest, current, err := functionSyntaxFingerprint(index.root)
	if err != nil || digest != index.digest {
		return nil, fmt.Errorf("indexed function tree changed")
	}
	output := make([]PositionedFunctionNode, 0, len(anchors))
	for _, anchor := range anchors {
		if digest != anchor.Tree || uint64(anchor.Ordinal) >= uint64(len(current)) {
			return nil, fmt.Errorf("function node anchor does not match current tree")
		}
		node := current[anchor.Ordinal]
		if reflect.TypeOf(node).String() != anchor.Kind {
			return nil, fmt.Errorf("function node anchor kind mismatch")
		}
		output = append(output, node)
	}
	return output, nil
}
func (index *FunctionNodeIndex) Fingerprint() [32]byte { return index.digest }

type functionSyntaxPointer struct {
	typ     reflect.Type
	address uintptr
}

const functionSyntaxMaxDepth = 2048
const functionSyntaxMaxWork = 1_000_000
const functionSyntaxMaxBytes = 16 * 1024 * 1024

var ErrFunctionSyntaxBudget = errors.New("function product syntax budget exceeded")

type functionSyntaxEncoder struct {
	bytes.Buffer
	depth, work int
	failure     error
	seen        map[functionSyntaxPointer]uint64
	nodes       []PositionedFunctionNode
	// Optional explicit derived-cache exclusions for nominal fingerprints.
	// Source syntax fingerprints leave this nil and remain fully structural.
	skipFields               map[reflect.Type]map[string]bool
	semanticBitSets          bool
	validateSemanticFunction func(*FuncType) error
}

var functionPositionType = reflect.TypeOf(lexer.Pos{})

func functionSyntaxFingerprint(value any) ([32]byte, []PositionedFunctionNode, error) {
	encoder := &functionSyntaxEncoder{seen: map[functionSyntaxPointer]uint64{}}
	encoder.WriteString("elisa-function-syntax-v1\x00")
	if err := encoder.encode(reflect.ValueOf(value)); err != nil {
		return [32]byte{}, nil, err
	}
	return sha256.Sum256(encoder.Bytes()), encoder.nodes, nil
}
func (e *functionSyntaxEncoder) number(value uint64) {
	if e.failure != nil {
		return
	}
	if e.Len() > functionSyntaxMaxBytes-8 {
		e.failure = ErrFunctionSyntaxBudget
		return
	}
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], value)
	e.Write(data[:])
}
func (e *functionSyntaxEncoder) text(value string) {
	if e.failure != nil {
		return
	}
	if len(value) > functionSyntaxMaxBytes-e.Len()-8 {
		e.failure = ErrFunctionSyntaxBudget
		return
	}
	e.number(uint64(len(value)))
	e.WriteString(value)
}
func (e *functionSyntaxEncoder) encode(value reflect.Value) error {
	if e.failure != nil {
		return e.failure
	}
	if e.depth >= functionSyntaxMaxDepth || e.work >= functionSyntaxMaxWork || e.Len() >= functionSyntaxMaxBytes {
		return ErrFunctionSyntaxBudget
	}
	e.depth++
	e.work++
	defer func() { e.depth-- }()
	if !value.IsValid() {
		e.WriteByte(0)
		return nil
	}
	// Position changes are rebound inputs, not declaration or body identity.
	if value.Type() == functionPositionType {
		e.WriteByte(1)
		return nil
	}
	e.text(value.Type().String())
	if e.failure != nil {
		return e.failure
	}
	if e.validateSemanticFunction != nil && value.Type() == reflect.TypeOf((*FuncType)(nil)) && !value.IsNil() {
		if err := e.validateSemanticFunction(value.Interface().(*FuncType)); err != nil {
			return err
		}
	}
	if e.semanticBitSets && value.Type() == reflect.TypeOf(IntBitSet{}) {
		return e.encodeSemanticIntBitSet(value.Interface().(IntBitSet))
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			e.WriteByte(0)
			return nil
		}
		e.WriteByte(1)
		return e.encode(value.Elem())
	case reflect.Pointer:
		if value.IsNil() {
			e.WriteByte(0)
			return nil
		}
		key := functionSyntaxPointer{value.Type(), value.Pointer()}
		if ordinal, ok := e.seen[key]; ok {
			e.WriteByte(2)
			e.number(ordinal)
			return nil
		}
		e.WriteByte(1)
		e.seen[key] = uint64(len(e.seen))
		if node, ok := value.Interface().(PositionedFunctionNode); ok {
			e.nodes = append(e.nodes, node)
		}
		return e.encode(value.Elem())
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if e.skipFields[value.Type()][value.Type().Field(i).Name] {
				continue
			}
			if !value.Field(i).CanInterface() {
				return fmt.Errorf("unpublished function syntax field %s.%s", value.Type(), value.Type().Field(i).Name)
			}
			e.text(value.Type().Field(i).Name)
			if err := e.encode(value.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if value.IsNil() {
			e.WriteByte(0)
			return nil
		}
		e.WriteByte(1)
		fallthrough
	case reflect.Array:
		if value.Len() > functionSyntaxMaxWork-e.work {
			return ErrFunctionSyntaxBudget
		}
		e.number(uint64(value.Len()))
		for i := 0; i < value.Len(); i++ {
			if err := e.encode(value.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		if value.IsNil() {
			e.WriteByte(0)
			return nil
		}
		e.WriteByte(1)
		if value.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("unsupported function syntax map key %s", value.Type().Key())
		}
		if value.Len() > functionSyntaxMaxWork-e.work {
			return ErrFunctionSyntaxBudget
		}
		keys := value.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		e.number(uint64(len(keys)))
		for _, key := range keys {
			e.text(key.String())
			if err := e.encode(value.MapIndex(key)); err != nil {
				return err
			}
		}
	case reflect.String:
		e.text(value.String())
	case reflect.Bool:
		if value.Bool() {
			e.WriteByte(1)
		} else {
			e.WriteByte(0)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.number(uint64(value.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		e.number(value.Uint())
	case reflect.Float32, reflect.Float64:
		e.number(math.Float64bits(value.Float()))
	default:
		return fmt.Errorf("unsupported function syntax kind %s", value.Kind())
	}
	return e.failure
}
