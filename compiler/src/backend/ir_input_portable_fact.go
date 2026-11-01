package backend

import (
	"fmt"
	"reflect"
	"sync"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// irObservedTypeGraph captures the type graph synchronously in the diagnostic
// observer. The key and result pointers are still borrowed and are not cache
// authority; callers must authenticate their identity and syntax separately.
type irObservedTypeGraph struct {
	Read   semantic.CodegenFactRead
	Digest [32]byte
	Valid  bool
	Error  string
	// Private result provenance is not portable or authenticated by this flag.
	PrivateResult bool
	Anchor        semantic.FunctionNodeAnchor
	AnchorValid   bool
}

type irTypeGraphObserver struct {
	mu           sync.Mutex
	observations []irObservedTypeGraph
	owners       map[semantic.PositionedFunctionNode]*semantic.FunctionNodeIndex
}

func newTypeGraphObserverForFunctions(functions []*ast.FuncDecl) (*irTypeGraphObserver, error) {
	o := &irTypeGraphObserver{owners: make(map[semantic.PositionedFunctionNode]*semantic.FunctionNodeIndex)}
	for _, fn := range functions {
		index, err := semantic.IndexFunctionNodes(fn)
		if err != nil {
			return nil, err
		}
		nodes, err := index.DiagnosticNodes()
		if err != nil {
			return nil, err
		}
		for _, node := range nodes {
			if _, exists := o.owners[node]; exists {
				o.owners[node] = nil
			} else {
				o.owners[node] = index
			}
		}
	}
	return o, nil
}

func (o *irTypeGraphObserver) observe(read semantic.CodegenFactRead) {
	if o == nil || read.Table != "ExprTypes" || (read.Operation != "lookup" && read.Operation != "value") || !read.Present {
		return
	}
	row := irObservedTypeGraph{Read: read, PrivateResult: read.Result != nil && read.Result.CanonicalCodegenResult() != read.Result}
	if node, ok := read.Key.(semantic.PositionedFunctionNode); ok && node != nil && reflect.ValueOf(node).Kind() == reflect.Pointer && !row.PrivateResult {
		if index := o.owners[node]; index != nil {
			if anchor, err := index.Anchor(node); err == nil {
				row.Anchor = anchor
				row.AnchorValid = true
			}
		}
	}
	if typ, ok := read.Value.(semantic.Type); ok {
		var err error
		row.Digest, err = semantic.DiagnosticCodegenTypeFingerprint(typ, read.Result)
		if err != nil {
			row.Error = err.Error()
		} else {
			row.Valid = true
		}
	} else {
		row.Error = "observation is not a semantic type"
	}
	o.mu.Lock()
	o.observations = append(o.observations, row)
	o.mu.Unlock()
}

func (o *irTypeGraphObserver) snapshot() []irObservedTypeGraph {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]irObservedTypeGraph(nil), o.observations...)
}

// irPortableLocalFactKey identifies an observed key within one exact function
// syntax tree. It is diagnostic evidence only: the observed value, result
// context, query coverage, and all other lowering inputs remain unverified.
type irPortableLocalFactKey struct {
	Table, Operation string
	Anchor           semantic.FunctionNodeAnchor
	Present          bool
}

// irPortableBoolFact snapshots a boolean observation at capture time. Even
// with a portable key and scalar value, this is not a reuse certificate: the
// owning Result, inherited views, query coverage and other inputs are open.
type irPortableBoolFact struct {
	Key   irPortableLocalFactKey
	Value bool
}

// irPortableBuiltinTypeFact captures only a builtin spelling. Nominal types,
// aggregates and target-dependent lowering are explicitly outside this record.
// Even a matching record is diagnostic and never authorizes cached IR.
type irPortableBuiltinTypeFact struct {
	Key  irPortableLocalFactKey
	Name string
}

// irPortableTypeGraphFact records a supported type graph at conversion time,
// not necessarily the earlier observation time: CodegenFactRead.Value is a
// borrowed pointer and may have changed. It does not certify the enclosing
// semantic result, private specialization context or any other dependency.
type irPortableTypeGraphFact struct {
	Key    irPortableLocalFactKey
	Digest [32]byte
}

// irLocatedTypeGraphFact records a source-graph locator as diagnostic
// provenance. The locator alone is not authenticated loader provenance.
type irLocatedTypeGraphFact struct {
	Function semantic.DiagnosticFunctionLocator
	Fact     irPortableTypeGraphFact
}

func portableLocatedTypeGraphFacts(file *ast.File, fn *ast.FuncDecl, observations []irObservedTypeGraph) ([]irLocatedTypeGraphFact, error) {
	locators, err := semantic.DiagnosticFunctionLocators(file)
	if err != nil {
		return nil, err
	}
	locator, ok := locators[fn]
	if !ok {
		return nil, fmt.Errorf("function absent from diagnostic source graph")
	}
	facts, err := portableObservedTypeGraphFacts(fn, observations)
	if err != nil {
		return nil, err
	}
	out := make([]irLocatedTypeGraphFact, len(facts))
	for i, fact := range facts {
		out[i] = irLocatedTypeGraphFact{Function: locator, Fact: fact}
	}
	return out, nil
}

func compareLocatedTypeGraphFacts(file *ast.File, fn *ast.FuncDecl, result *semantic.Result, facts []irLocatedTypeGraphFact) (bool, error) {
	locators, err := semantic.DiagnosticFunctionLocators(file)
	if err != nil {
		return false, err
	}
	locator, ok := locators[fn]
	if !ok {
		return false, fmt.Errorf("function absent from diagnostic source graph")
	}
	local := make([]irPortableTypeGraphFact, len(facts))
	for i, fact := range facts {
		if fact.Function != locator {
			return false, nil
		}
		local[i] = fact.Fact
	}
	return compareLocalTypeGraphFacts(fn, result, local)
}

// portableObservedTypeGraphFacts preserves the observation-time digest while
// resolving only canonical local keys. Private views remain unsupported: a
// matching source node cannot authenticate their specialization or ownership.
// Both type digest and anchor must have been captured at observation time;
// this output remains diagnostic rather than authority to reuse IR.
func portableObservedTypeGraphFacts(fn *ast.FuncDecl, observations []irObservedTypeGraph) ([]irPortableTypeGraphFact, error) {
	reads := make([]semantic.CodegenFactRead, len(observations))
	for i, observation := range observations {
		if !observation.Valid || observation.PrivateResult || !observation.AnchorValid {
			return nil, fmt.Errorf("unverified type observation or private result")
		}
		reads[i] = observation.Read
	}
	keys, err := portableLocalFactKeys(fn, reads)
	if err != nil {
		return nil, err
	}
	out := make([]irPortableTypeGraphFact, len(observations))
	for i, observation := range observations {
		if keys[i].Anchor != observation.Anchor {
			return nil, fmt.Errorf("function syntax changed since observation")
		}
		out[i] = irPortableTypeGraphFact{Key: keys[i], Digest: observation.Digest}
	}
	return out, nil
}

func portableLocalTypeGraphFacts(fn *ast.FuncDecl, reads []semantic.CodegenFactRead) ([]irPortableTypeGraphFact, error) {
	keys, err := portableLocalFactKeys(fn, reads)
	if err != nil {
		return nil, err
	}
	out := make([]irPortableTypeGraphFact, len(reads))
	for i, read := range reads {
		if read.Table != "ExprTypes" || !read.Present {
			return nil, fmt.Errorf("unsupported type graph observation")
		}
		typ, ok := read.Value.(semantic.Type)
		if !ok {
			return nil, fmt.Errorf("type graph observation is not a semantic type")
		}
		digest, err := semantic.DiagnosticCodegenTypeFingerprint(typ, read.Result)
		if err != nil {
			return nil, err
		}
		out[i] = irPortableTypeGraphFact{Key: keys[i], Digest: digest}
	}
	return out, nil
}

func compareLocalTypeGraphFacts(fn *ast.FuncDecl, result *semantic.Result, facts []irPortableTypeGraphFact) (bool, error) {
	if result == nil {
		return false, fmt.Errorf("missing codegen result")
	}
	index, err := semantic.IndexFunctionNodes(fn)
	if err != nil {
		return false, err
	}
	anchors := make([]semantic.FunctionNodeAnchor, len(facts))
	for i, fact := range facts {
		if fact.Key.Table != "ExprTypes" || !fact.Key.Present || (fact.Key.Operation != "lookup" && fact.Key.Operation != "value") {
			return false, fmt.Errorf("unsupported type graph query")
		}
		anchors[i] = fact.Key.Anchor
	}
	nodes, err := index.RebindAll(anchors)
	if err != nil {
		return false, err
	}
	for i, node := range nodes {
		expr, ok := node.(ast.Expr)
		if !ok {
			return false, fmt.Errorf("type graph key is not an expression")
		}
		typ, present := result.LookupExprTypes(expr)
		if !present {
			return false, nil
		}
		digest, err := semantic.DiagnosticCodegenTypeFingerprint(typ, result)
		if err != nil {
			return false, err
		}
		if digest != facts[i].Digest {
			return false, nil
		}
	}
	return true, nil
}

func portableLocalBuiltinTypeFacts(fn *ast.FuncDecl, reads []semantic.CodegenFactRead) ([]irPortableBuiltinTypeFact, error) {
	keys, err := portableLocalFactKeys(fn, reads)
	if err != nil {
		return nil, err
	}
	out := make([]irPortableBuiltinTypeFact, len(reads))
	for i, read := range reads {
		if read.Table != "ExprTypes" {
			return nil, fmt.Errorf("unsupported type fact table")
		}
		name := ""
		if read.Present {
			typ, ok := read.Value.(*semantic.BuiltinType)
			if !ok || typ == nil || typ.Name == "" {
				return nil, fmt.Errorf("type fact is not a named builtin")
			}
			name = typ.Name
		} else if read.Value != nil {
			return nil, fmt.Errorf("absent type fact has a value")
		}
		out[i] = irPortableBuiltinTypeFact{Key: keys[i], Name: name}
	}
	return out, nil
}

func compareLocalBuiltinTypeFacts(fn *ast.FuncDecl, result *semantic.Result, facts []irPortableBuiltinTypeFact) (bool, error) {
	if result == nil {
		return false, fmt.Errorf("missing codegen result")
	}
	index, err := semantic.IndexFunctionNodes(fn)
	if err != nil {
		return false, err
	}
	anchors := make([]semantic.FunctionNodeAnchor, len(facts))
	for i, fact := range facts {
		if fact.Key.Table != "ExprTypes" || (fact.Key.Operation != "lookup" && fact.Key.Operation != "value") || (fact.Key.Present && fact.Name == "") || (!fact.Key.Present && fact.Name != "") {
			return false, fmt.Errorf("unsupported builtin type query")
		}
		anchors[i] = fact.Key.Anchor
	}
	nodes, err := index.RebindAll(anchors)
	if err != nil {
		return false, err
	}
	for i, node := range nodes {
		expr, ok := node.(ast.Expr)
		if !ok {
			return false, fmt.Errorf("type fact key is not an expression")
		}
		value, present := result.LookupExprTypes(expr)
		if present != facts[i].Key.Present {
			return false, nil
		}
		if present {
			typ, ok := value.(*semantic.BuiltinType)
			if !ok || typ == nil || typ.Name != facts[i].Name {
				return false, nil
			}
		} else if value != nil {
			return false, fmt.Errorf("absent type fact has a value")
		}
	}
	return true, nil
}

// irPortableMapPresence records a physical map-presence observation. Result
// identity is intentionally external to this record and must be authenticated
// by any future caller before these observations could contribute to reuse.
type irPortableMapPresence struct {
	Table   string
	Present bool
}

func portableOwnMapPresence(read semantic.CodegenFactRead) (irPortableMapPresence, error) {
	value, ok := read.Value.(bool)
	if read.Table != "PermGrowthOps" || read.Operation != "own-map-presence" || read.Key != nil || !ok || value != read.Present {
		return irPortableMapPresence{}, fmt.Errorf("unsupported map presence observation")
	}
	return irPortableMapPresence{Table: read.Table, Present: value}, nil
}

// compareOwnMapPresence checks only the observed physical map presence. A
// match does not validate the semantic view, its values or any other inputs.
func compareOwnMapPresence(result *semantic.Result, fact irPortableMapPresence) (bool, error) {
	if result == nil || fact.Table != "PermGrowthOps" {
		return false, fmt.Errorf("unsupported map presence query")
	}
	return (result.PermGrowthOps != nil) == fact.Present, nil
}

// portableLocalBoolFacts deliberately rejects every value except a concrete
// boolean. Types, pointers, slices and even nil are not encoded by identity
// or formatting; their complete meaning requires separate validation.
func portableLocalBoolFacts(fn *ast.FuncDecl, reads []semantic.CodegenFactRead) ([]irPortableBoolFact, error) {
	keys, err := portableLocalFactKeys(fn, reads)
	if err != nil {
		return nil, err
	}
	out := make([]irPortableBoolFact, len(reads))
	for i, read := range reads {
		value, ok := read.Value.(bool)
		if !ok {
			return nil, fmt.Errorf("codegen fact value is not a boolean")
		}
		out[i] = irPortableBoolFact{Key: keys[i], Value: value}
	}
	return out, nil
}

// compareLocalBoolFacts rebinds a narrow class of diagnostic observations to
// an independently constructed function and Result. A match is evidence only
// for these individual reads; it must never authorize cached IR on its own.
func compareLocalBoolFacts(fn *ast.FuncDecl, result *semantic.Result, facts []irPortableBoolFact) (bool, error) {
	if result == nil {
		return false, fmt.Errorf("missing codegen result")
	}
	index, err := semantic.IndexFunctionNodes(fn)
	if err != nil {
		return false, err
	}
	anchors := make([]semantic.FunctionNodeAnchor, len(facts))
	for i, fact := range facts {
		if fact.Key.Table != "PermGrowthOps" || (fact.Key.Operation != "lookup" && fact.Key.Operation != "value") {
			return false, fmt.Errorf("unsupported boolean fact query")
		}
		anchors[i] = fact.Key.Anchor
	}
	nodes, err := index.RebindAll(anchors)
	if err != nil {
		return false, err
	}
	for i, node := range nodes {
		expr, ok := node.(ast.Expr)
		if !ok {
			return false, fmt.Errorf("boolean fact key is not an expression")
		}
		value, present := result.LookupPermGrowthOps(expr)
		if present != facts[i].Key.Present || value != facts[i].Value {
			return false, nil
		}
	}
	return true, nil
}

// portableLocalFactKeys fails closed for unsupported operations and for keys
// outside the supplied function. In particular it does not translate private
// specializations into their source templates or authorize IR reuse.
func portableLocalFactKeys(fn *ast.FuncDecl, reads []semantic.CodegenFactRead) ([]irPortableLocalFactKey, error) {
	index, err := semantic.IndexFunctionNodes(fn)
	if err != nil {
		return nil, err
	}
	nodes := make([]semantic.PositionedFunctionNode, 0, len(reads))
	for _, read := range reads {
		if read.Table == "" || (read.Operation != "lookup" && read.Operation != "value") {
			return nil, fmt.Errorf("unsupported codegen fact observation")
		}
		node, ok := read.Key.(semantic.PositionedFunctionNode)
		if !ok || node == nil {
			return nil, fmt.Errorf("codegen fact key is not a function node")
		}
		nodes = append(nodes, node)
	}
	anchors, err := index.Anchors(nodes)
	if err != nil {
		return nil, err
	}
	out := make([]irPortableLocalFactKey, len(reads))
	for i, read := range reads {
		out[i] = irPortableLocalFactKey{read.Table, read.Operation, anchors[i], read.Present}
	}
	return out, nil
}
