//go:build cgo

package backend

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

type partitionSpecializationKind uint8

const (
	partitionSpecializationFunction partitionSpecializationKind = iota + 1
	partitionSpecializationImplMethod
)

// partitionSpecializationRequest contains only immutable Go-side semantic
// inputs. In particular, it never retains an LLVM value or generator/context.
type partitionSpecializationRequest struct {
	kind partitionSpecializationKind

	name      string
	ownerPart int

	decl          *ast.FuncDecl
	canonicalDecl *ast.FuncDecl
	base          *semantic.FuncType
	bindings      map[string]semantic.Type

	impl              *semantic.StaticImpl
	implDecl          *ast.ImplDecl
	implTypeParams    []string
	implMethod        string
	implIdentity      string
	baseIdentity      string
	bindingIdentity   string
	signatureIdentity string
}

// partitionSpecializationQueue collects bodies requested while ordinary
// caller modules are lowered. The queue is shared by partition workers and
// later supplemental owner modules; its map provides both deduplication and a
// fail-closed check that one externally named symbol always describes one
// source body, binding map, and signature.
type partitionSpecializationQueue struct {
	mu       sync.Mutex
	requests map[string]partitionSpecializationRequest
}

type partitionIRBuildMode struct {
	queue              *partitionSpecializationQueue
	requests           []partitionSpecializationRequest
	previouslyDefined  map[string]bool
	specializationOnly bool
	cachePart          int
	supplementRound    int
	inputAudit         func(int, irInputReadReport)
}

type partitionIRInputAuditCollector struct {
	mu      sync.Mutex
	reports []irInputReadReport
	present []bool
}

func newPartitionIRInputAuditCollector(parts int) *partitionIRInputAuditCollector {
	return &partitionIRInputAuditCollector{reports: make([]irInputReadReport, parts), present: make([]bool, parts)}
}

func (c *partitionIRInputAuditCollector) record(part int, report irInputReadReport) {
	if c == nil || part < 0 || part >= len(c.reports) {
		return
	}
	c.mu.Lock()
	if !c.present[part] {
		c.reports[part] = report
		c.present[part] = true
	} else {
		c.reports[part] = mergePartitionIRInputReports(c.reports[part], report)
	}
	c.mu.Unlock()
}

func (c *partitionIRInputAuditCollector) flush(callback func(int, irInputReadReport)) {
	if c == nil || callback == nil {
		return
	}
	c.mu.Lock()
	reports := append([]irInputReadReport(nil), c.reports...)
	present := append([]bool(nil), c.present...)
	c.mu.Unlock()
	for part, report := range reports {
		if present[part] {
			callback(part, report)
		}
	}
}

func mergePartitionIRInputReports(left, right irInputReadReport) irInputReadReport {
	left.Semantic = append(left.Semantic, right.Semantic...)
	left.Codegen = append(left.Codegen, right.Codegen...)
	left.Bodies = append(left.Bodies, right.Bodies...)
	left.Queries = append(left.Queries, right.Queries...)
	left.Ranges = append(left.Ranges, cloneIRInputRanges(right.Ranges)...)
	left.Lowered = left.Lowered && right.Lowered
	return left
}

func newPartitionSpecializationQueue() *partitionSpecializationQueue {
	return &partitionSpecializationQueue{requests: make(map[string]partitionSpecializationRequest)}
}

func (q *partitionSpecializationQueue) hasName(name string) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	_, ok := q.requests[name]
	q.mu.Unlock()
	return ok
}

func (q *partitionSpecializationQueue) addFunction(result *semantic.Result, name string, decl *ast.FuncDecl, base, specialized *semantic.FuncType, bindings map[string]semantic.Type, parts int) error {
	return q.add(result, partitionSpecializationRequest{
		kind:     partitionSpecializationFunction,
		name:     name,
		decl:     decl,
		base:     base,
		bindings: bindings,
	}, specialized, parts)
}

func (q *partitionSpecializationQueue) addImplMethod(result *semantic.Result, name string, decl *ast.FuncDecl, base, specialized *semantic.FuncType, implTypeParams []string, impl *semantic.StaticImpl, method string, bindings map[string]semantic.Type, parts int) error {
	return q.add(result, partitionSpecializationRequest{
		kind:     partitionSpecializationImplMethod,
		name:     name,
		decl:     decl,
		base:     base,
		bindings: bindings,
		impl:     impl,
		implDecl: func() *ast.ImplDecl {
			if impl != nil {
				return impl.Decl
			}
			return nil
		}(),
		implTypeParams: append([]string(nil), implTypeParams...),
		implMethod:     method,
	}, specialized, parts)
}

func (q *partitionSpecializationQueue) add(result *semantic.Result, request partitionSpecializationRequest, specialized *semantic.FuncType, parts int) error {
	if q == nil || result == nil || request.name == "" || request.decl == nil || request.base == nil || specialized == nil || parts < 1 {
		return errPartitionDeclined
	}
	request.canonicalDecl = result.CanonicalCodegenFunction(request.decl)
	if request.canonicalDecl == nil {
		request.canonicalDecl = request.decl
	}
	// A canonical alias can carry a different semantic body view from the
	// source node requested by this generator. Until those views can be proven
	// interchangeable for every codegen fact, let the existing path handle it.
	if request.decl != request.canonicalDecl {
		return errPartitionDeclined
	}
	var err error
	request.baseIdentity, err = semantic.TypeSymbolKey(request.base)
	if err != nil {
		return errPartitionDeclined
	}
	request.bindingIdentity, err = partitionSpecializationBindingsIdentity(request.bindings)
	if err != nil {
		return errPartitionDeclined
	}
	request.signatureIdentity, err = semantic.TypeSymbolKey(specialized)
	if err != nil {
		return errPartitionDeclined
	}
	if request.kind == partitionSpecializationImplMethod {
		if request.decl.Name != request.implMethod {
			return errPartitionDeclined
		}
		request.implIdentity, err = partitionSpecializationImplIdentity(request.impl, request.implDecl, request.implTypeParams, request.implMethod)
		if err != nil {
			return errPartitionDeclined
		}
	} else if request.kind != partitionSpecializationFunction {
		return errPartitionDeclined
	}
	request.ownerPart = partitionOwner(request.name, parts)
	request.bindings = clonePartitionSpecializationBindings(request.bindings)

	q.mu.Lock()
	defer q.mu.Unlock()
	if previous, ok := q.requests[request.name]; ok {
		if !samePartitionSpecializationIdentity(previous, request) {
			return errPartitionDeclined
		}
		return nil
	}
	q.requests[request.name] = request
	return nil
}

func (q *partitionSpecializationQueue) snapshot() []partitionSpecializationRequest {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	requests := make([]partitionSpecializationRequest, 0, len(q.requests))
	for _, request := range q.requests {
		request.bindings = clonePartitionSpecializationBindings(request.bindings)
		request.implTypeParams = append([]string(nil), request.implTypeParams...)
		requests = append(requests, request)
	}
	q.mu.Unlock()
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].ownerPart != requests[j].ownerPart {
			return requests[i].ownerPart < requests[j].ownerPart
		}
		return requests[i].name < requests[j].name
	})
	return requests
}

func missingPartitionSpecializations(requests []partitionSpecializationRequest, definitionsByPart []map[string]bool) []partitionSpecializationRequest {
	var missing []partitionSpecializationRequest
	for _, request := range requests {
		if request.ownerPart < 0 || request.ownerPart >= len(definitionsByPart) || !definitionsByPart[request.ownerPart][request.name] {
			missing = append(missing, request)
		}
	}
	return missing
}

func samePartitionSpecializationIdentity(left, right partitionSpecializationRequest) bool {
	return left.kind == right.kind &&
		left.canonicalDecl == right.canonicalDecl &&
		left.impl == right.impl &&
		left.implDecl == right.implDecl &&
		left.implIdentity == right.implIdentity &&
		left.implMethod == right.implMethod &&
		left.baseIdentity == right.baseIdentity &&
		left.bindingIdentity == right.bindingIdentity &&
		left.signatureIdentity == right.signatureIdentity
}

func partitionSpecializationBindingsIdentity(bindings map[string]semantic.Type) (string, error) {
	names := make([]string, 0, len(bindings))
	for name := range bindings {
		names = append(names, name)
	}
	sort.Strings(names)
	var key strings.Builder
	fmt.Fprintf(&key, "%d|", len(names))
	for _, name := range names {
		typ := bindings[name]
		if typ == nil {
			return "", fmt.Errorf("nil specialization binding %q", name)
		}
		typeKey, err := semantic.TypeSymbolKey(typ)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&key, "%d:%s|%d:%s|", len(name), name, len(typeKey), typeKey)
	}
	return key.String(), nil
}

func partitionSpecializationImplIdentity(impl *semantic.StaticImpl, implDecl *ast.ImplDecl, params []string, method string) (string, error) {
	if impl == nil || implDecl == nil || impl.Decl != implDecl || method == "" || len(params) == 0 || len(impl.TypeParams) != len(params) || len(implDecl.GenericParams) != len(params) {
		return "", fmt.Errorf("incomplete parametric impl specialization identity")
	}
	for index, param := range params {
		if param == "" || impl.TypeParams[index] != param || implDecl.GenericParams[index].Name != param {
			return "", fmt.Errorf("parametric impl type-parameter order differs from its source declaration")
		}
	}
	receiver, err := semantic.TypeSymbolKey(impl.Receiver)
	if err != nil {
		return "", err
	}
	var key strings.Builder
	fmt.Fprintf(&key, "%d:%s|%d:%s|%d:%s|%d:", len(impl.InterfaceName), impl.InterfaceName, len(receiver), receiver, len(method), method, len(params))
	for _, param := range params {
		fmt.Fprintf(&key, "%d:%s|", len(param), param)
	}
	associatedNames := make([]string, 0, len(impl.AssociatedTypes))
	for name := range impl.AssociatedTypes {
		associatedNames = append(associatedNames, name)
	}
	sort.Strings(associatedNames)
	fmt.Fprintf(&key, "associated:%d|", len(associatedNames))
	for _, name := range associatedNames {
		typeKey, err := semantic.TypeSymbolKey(impl.AssociatedTypes[name])
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&key, "%d:%s|%d:%s|", len(name), name, len(typeKey), typeKey)
	}
	boundNames := make([]string, 0, len(impl.TypeParamInterfaceBounds))
	for name := range impl.TypeParamInterfaceBounds {
		boundNames = append(boundNames, name)
	}
	sort.Strings(boundNames)
	fmt.Fprintf(&key, "bounds:%d|", len(boundNames))
	for _, name := range boundNames {
		bound := impl.TypeParamInterfaceBounds[name]
		fmt.Fprintf(&key, "%d:%s|%d:%s|", len(name), name, len(bound), bound)
	}
	if impl.InvalidTypeParamBounds {
		key.WriteString("invalid-bounds|1|")
	} else {
		key.WriteString("invalid-bounds|0|")
	}
	return key.String(), nil
}

func clonePartitionSpecializationBindings(bindings map[string]semantic.Type) map[string]semantic.Type {
	if bindings == nil {
		return nil
	}
	copy := make(map[string]semantic.Type, len(bindings))
	for name, typ := range bindings {
		copy[name] = typ
	}
	return copy
}

func sameSpecializedFunctionType(left, right *semantic.FuncType) bool {
	if left == nil || right == nil {
		return left == right
	}
	leftKey, leftErr := semantic.TypeSymbolKey(left)
	rightKey, rightErr := semantic.TypeSymbolKey(right)
	return leftErr == nil && rightErr == nil && leftKey == rightKey
}

func (g *llvmGenerator) markPartitionSpecializationDefined(name string) {
	if g == nil || g.partitionSpecializationQueue == nil || name == "" {
		return
	}
	if g.partSpecializationDefinitions == nil {
		g.partSpecializationDefinitions = make(map[string]bool)
	}
	g.partSpecializationDefinitions[name] = true
}
