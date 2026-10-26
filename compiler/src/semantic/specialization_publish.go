package semantic

import (
	"fmt"
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// A published product owns only this closure. Construction-only analyzer maps,
// copy planners and unrelated foreign analysis views are never retained.
type specializationPublication struct {
	context                 *specializationContext
	canonicalMembershipScan bool
	rootNodes               map[ast.Node]bool
	rootSymbols             map[*Symbol]bool
	rootScopes              map[*Scope]bool
	owned                   map[ast.Node]bool
	spanNodes               map[*ast.IndexExpr]bool
	queue                   []ast.Node
	seen                    map[specializationPointer]uint8
	target                  *ast.FuncDecl
	canonical               *ast.FuncDecl
	err                     error
}

type boundSparseSpecializationMap struct {
	dst    reflect.Value
	source reflect.Value
	key    reflect.Type
}

func newSpecializationPublication(c *specializationContext, decl *ast.FuncDecl) *specializationPublication {
	canonical := c.worker.parallel.original[decl]
	var nodes map[ast.Node]bool
	var symbols map[*Symbol]bool
	var scopes map[*Scope]bool
	if c.base.shared == nil {
		// Ordinary serial lowering may update Result fact maps between calls.
		// Preserve its historical complete, current-publication catalogs and
		// direct map answers; only a shared immutable session uses targeting.
		nodes = canonicalSpecializationNodes(c.base)
		symbols, scopes = canonicalSpecializationIdentities(c.base)
	} else {
		nodes, symbols, scopes = targetedSpecializationMembership(c)
	}
	p := &specializationPublication{context: c, rootNodes: nodes, rootSymbols: symbols, rootScopes: scopes, owned: map[ast.Node]bool{}, spanNodes: map[*ast.IndexExpr]bool{}, seen: map[specializationPointer]uint8{}, target: decl, canonical: canonical}
	p.walk(reflect.ValueOf(decl), false)
	// Detached proof nodes are part of the current function's semantic closure,
	// even when folding removed them from the final syntax tree.
	for expr, owner := range c.worker.indexBoundsProofOwners {
		if owner == canonical || owner == decl {
			if p.canonicalNodeMember(expr) {
				p.err = fmt.Errorf("specialization retained canonical syntax in current proof authority")
				continue
			}
			p.walk(reflect.ValueOf(expr), true)
		}
	}
	return p
}

// Canonical syntax membership is shared once per concurrent codegen session.
// It is a reference index over the already-owned program, not another AST copy.
func canonicalSpecializationNodes(root *Result) map[ast.Node]bool {
	var shared *sharedCodegen
	if root.shared != nil {
		shared = root.shared
		shared.canonicalMu.Lock()
		defer shared.canonicalMu.Unlock()
		if shared.canonicalNodes != nil {
			return shared.canonicalNodes
		}
	}
	nodes := map[ast.Node]bool{}
	scan := &specializationPublication{canonicalMembershipScan: true, owned: nodes, rootSymbols: map[*Symbol]bool{}, rootScopes: map[*Scope]bool{}, seen: map[specializationPointer]uint8{}}
	rv := reflect.ValueOf(root).Elem()
	for i := 0; i < rv.NumField(); i++ {
		if rv.Type().Field(i).PkgPath == "" {
			scan.walk(rv.Field(i), false)
		}
	}
	// Analyzer-private AST-keyed tables can hold typed synthesized expressions
	// not otherwise present in the source file or published Result tables.
	for _, path := range specializationASTMapPaths() {
		table := specializationField(reflect.ValueOf(root.analyzer).Elem(), path)
		if !table.IsNil() {
			// Route through walk so its map identity guard skips tables already
			// visited through Result while still scanning private analyzer tables.
			scan.walk(table, false)
		}
	}
	if root.GlobalScope != nil {
		scan.rootScopes[root.GlobalScope] = true
		for _, symbol := range root.GlobalScope.Symbols {
			scan.walk(reflect.ValueOf(symbol), false)
		}
	}
	if shared != nil {
		shared.canonicalNodes = nodes
		shared.canonicalSymbols = scan.rootSymbols
		shared.canonicalScopes = scan.rootScopes
	}
	return nodes
}

// Walk AST-bearing fact/type metadata without traversing canonical nominal
// types, Symbols or Scopes. Their global identity is deliberately borrowed.
func (p *specializationPublication) walk(v reflect.Value, span bool) {
	if !v.IsValid() || p.err != nil {
		return
	}
	dispatch := true
	if p.context == nil {
		policy := canonicalMembershipPolicyFor(v.Type())
		if !policy.relevant {
			return
		}
		dispatch = policy.dispatch
	}
	if p.context != nil {
		if err := validateSparseFactValueType(v); err != nil {
			p.err = err
			return
		}
	}
	if dispatch && v.CanInterface() {
		switch value := v.Interface().(type) {
		case *Symbol:
			if value == nil {
				return
			}
			key := specializationPointer{v.Type(), reflect.ValueOf(value).Pointer()}
			if p.seen[key]&4 != 0 {
				return
			}
			p.seen[key] |= 4
			if p.context == nil {
				p.rootSymbols[value] = true
			} else if p.canonicalSymbolMember(value) {
				return
			}
			p.walk(reflect.ValueOf(value.Node), false)
			p.walk(reflect.ValueOf(value.Type), false)
			p.walk(reflect.ValueOf(value.AliasOf), false)
			return
		case *Scope:
			if value == nil {
				return
			}
			if p.context == nil {
				p.rootScopes[value] = true
				return
			}
			if !p.canonicalScopeMember(value) {
				p.err = fmt.Errorf("specialization fact retains a private semantic scope")
			}
			return
		case *StructType, *EnumType, *ConstEnumType, *OpaqueType, *BitGroupType, *BuiltinType, *BitIntType, *TypeParamType, *ErrorSetType:
			return
		case ast.Node:
			if reflect.ValueOf(value).Kind() == reflect.Pointer && reflect.ValueOf(value).IsNil() {
				return
			}
			if p.rootNodes != nil && p.canonicalNodeMember(value) {
				return
			}
			if decl, ok := value.(ast.Decl); ok && p.rootNodes != nil && decl != p.target {
				// An unrelated declaration is a canonical symbol reference;
				// it is not part of this function's published body closure.
				return
			}
			canonicalNodeWalk := p.canonicalMembershipScan && !span
			if canonicalNodeWalk && p.owned[value] {
				return
			}
			if canonicalNodeWalk && p.walkCanonicalAST(value) {
				return
			}
			if !p.owned[value] {
				// An interface value is unwrapped below. Defer its first visited
				// mark to the concrete node so the node itself still gets walked.
				if !canonicalNodeWalk || v.Kind() != reflect.Interface {
					p.owned[value] = true
				}
				// Canonical scans build only the membership index; publication
				// consumes this worklist only for private fact-held syntax.
				if p.context != nil {
					p.queue = append(p.queue, value)
				}
			}
			if index, ok := value.(*ast.IndexExpr); ok && span {
				p.spanNodes[index] = true
			}
		case Type:
			// Structural type wrappers can carry private default/contract AST.
			// They are kept, but those templates alone are not emitted accesses.
			span = false
		}
	}
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			p.walk(v.Elem(), span)
		}
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		canonicalNodeWalk := p.canonicalMembershipScan && !span && v.CanInterface() && v.Type().Implements(astNodeType)
		if !canonicalNodeWalk {
			key := specializationPointer{v.Type(), v.Pointer()}
			role := uint8(1)
			if span {
				role = 2
			}
			if p.seen[key]&role != 0 {
				return
			}
			p.seen[key] |= role
		}
		if decl, ok := v.Interface().(*ast.FuncDecl); ok && p.context != nil && decl == p.target {
			fields := settable(v.Elem())
			for i := 0; i < fields.NumField(); i++ {
				fieldSpan := span
				// The published target body grants span authority. Walk it in that
				// role on the first pass, leaving defaults/contracts unprivileged.
				if fields.Type().Field(i).Name == "Body" {
					fieldSpan = true
				}
				p.walk(settable(fields.Field(i)), fieldSpan)
			}
			return
		}
		p.walk(settable(v.Elem()), span)
	case reflect.Struct:
		// This walk is read-only. Reuse addressable storage; map/interface
		// values still need a private addressable copy for unexported fields.
		addressable := v
		if !addressable.CanAddr() {
			addressable = reflect.New(v.Type()).Elem()
			addressable.Set(v)
		}
		for i := 0; i < v.NumField(); i++ {
			p.walk(settable(addressable.Field(i)), span)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			p.walk(v.Index(i), span)
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		role := uint8(1)
		if span {
			role = 2
		}
		if p.seen[key]&role != 0 {
			return
		}
		p.seen[key] |= role
		it := v.MapRange()
		if p.context == nil && v.Len() > 1 {
			// Canonical scans retain underlying identities, never reflected cells.
			// Full assignment overwrites each reusable cell before walking it.
			key := reflect.New(v.Type().Key()).Elem()
			value := reflect.New(v.Type().Elem()).Elem()
			for it.Next() {
				key.SetIterKey(it)
				value.SetIterValue(it)
				p.walk(key, span)
				p.walk(value, span)
			}
			return
		}
		for it.Next() {
			p.walk(it.Key(), span)
			p.walk(it.Value(), span)
		}
	}
}

func (p *specializationPublication) publish() (*Result, error) {
	c := p.context
	if err := validateSpecializationResultMapProjection(); err != nil {
		return nil, err
	}
	if err := validateSparseSpecializationSchema(); err != nil {
		return nil, err
	}
	if err := validateSparseFactSchema(); err != nil {
		return nil, err
	}
	if p.err != nil {
		return nil, p.err
	}
	result := *c.base
	result.analyzer = nil // the complete construction analyzer must die after publication
	result.specializationRoot = c.base
	result.specializationCurrent = p.target
	result.specializationCanonical = map[*ast.FuncDecl]*ast.FuncDecl{p.target: p.canonical}
	result.specializationOwned = p.owned
	result.specializationNodes = map[ast.Node]ast.Node{}
	result.specializationProofMask = map[*ast.IndexExpr]bool{}
	p.projectSparseMaps(&result)
	if p.err != nil {
		return nil, p.err
	}
	for original, private := range c.syntax.nodes {
		if p.owned[private] {
			result.specializationNodes[original] = private
		}
	}
	// Hide every historical original target proof, including detached nodes.
	for expr, owner := range c.base.analyzer.indexBoundsProofOwners {
		if owner == p.canonical {
			result.specializationProofMask[expr] = true
		}
	}
	// ReassignReclaims is computed after body analysis instead of stored in Analyzer.
	result.ReassignReclaims = make(map[*ast.AssignStmt]bool)
	for stmt, value := range c.worker.computeReassignReclaims([]*ast.FuncDecl{p.target}) {
		if p.owned[stmt] {
			result.ReassignReclaims[stmt] = value
		}
	}
	// Scope rebuilt-node authority to current fresh facts. A disagreeing current
	// access at the same span cannot be authorized by another clone's proof.
	blocked := map[lexer.Pos]bool{}
	for index := range p.spanNodes {
		if !c.worker.indexBoundsProven[index] {
			blocked[index.Position] = true
		}
	}
	result.specializationSpanProofs = map[*ast.IndexExpr]bool{}
	for expr, proven := range result.IndexBoundsProven {
		owner := c.worker.indexBoundsProofOwners[expr]
		if proven && (owner == p.canonical || owner == p.target) && !blocked[expr.Position] {
			result.specializationSpanProofs[expr] = true
		}
	}
	helpers := map[*ast.CallExpr]string{}
	for node := range p.owned {
		if call, ok := node.(*ast.CallExpr); ok {
			if name, present := c.worker.lookupCompilerBuiltinHelperCall(call); present && name != "" {
				helpers[call] = name
			}
		}
	}
	result.specializationQueries = newSpecializationQueries(c.worker, c.base, p.target, helpers, p.owned, result.specializationNodes)
	// Public declaration indexes, diagnostics and telemetry remain the canonical
	// program's immutable values. No speculative whole-program report slices or
	// mutable construction caches are retained by the product.
	return &result, nil
}

// projectSparseMaps imports facts by owned syntax key. Bind the reflection
// fields once, then probe only tables whose key type accepts each concrete node
// type. Keeping the cached candidate slice in source-table order preserves the
// original projection and the fact-held syntax closure walk.
func (p *specializationPublication) projectSparseMaps(result *Result) {
	c := p.context
	rv := reflect.ValueOf(result).Elem()
	av := reflect.ValueOf(c.worker).Elem()
	rootv := reflect.ValueOf(c.base).Elem()
	bindings := make([]boundSparseSpecializationMap, 0, len(sparseSpecializationMaps))
	// Seed only empty shapes. Borrowing a whole node-fact map here would put the
	// whole-program multiplier back into every memoized product.
	for _, entry := range sparseSpecializationMaps {
		dst := rv.FieldByName(entry.result)
		if entry.analyzer == "" {
			continue
		}
		source := settable(av.FieldByName(entry.analyzer))
		if !rootv.FieldByName(entry.result).IsNil() || !source.IsNil() {
			dst.Set(reflect.MakeMap(dst.Type()))
		} else {
			dst.Set(reflect.Zero(dst.Type()))
		}
		if !source.IsNil() {
			bindings = append(bindings, boundSparseSpecializationMap{dst: dst, source: source, key: source.Type().Key()})
		}
	}
	eligibleByType := make(map[reflect.Type][]boundSparseSpecializationMap)
	// Probe by owned keys, including newly discovered fact-held syntax, until
	// closure. No per-function full scan of every source fact table is needed.
	for cursor := 0; cursor < len(p.queue); cursor++ {
		node := p.queue[cursor]
		nodeValue := reflect.ValueOf(node)
		nodeType := nodeValue.Type()
		eligible, cached := eligibleByType[nodeType]
		if !cached {
			for _, binding := range bindings {
				if nodeType.AssignableTo(binding.key) {
					eligible = append(eligible, binding)
				}
			}
			eligibleByType[nodeType] = eligible
		}
		for _, binding := range eligible {
			value := binding.source.MapIndex(nodeValue)
			if !value.IsValid() {
				continue
			}
			if binding.dst.IsNil() {
				binding.dst.Set(reflect.MakeMap(binding.dst.Type()))
			}
			binding.dst.SetMapIndex(nodeValue, value)
			p.walk(value, true)
		}
	}
}

func validateSparseSpecializationSchema() error {
	typ := reflect.TypeOf(Result{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		expected, ok := sparseSpecializationSchema[field.Name]
		if !ok || expected != field.Type {
			return fmt.Errorf("sparse specialization has no exact schema policy for Result.%s", field.Name)
		}
	}
	return nil
}

func canonicalSpecializationIdentities(root *Result) (map[*Symbol]bool, map[*Scope]bool) {
	scan := &specializationPublication{owned: map[ast.Node]bool{}, rootSymbols: map[*Symbol]bool{}, rootScopes: map[*Scope]bool{}, seen: map[specializationPointer]uint8{}}
	rv := reflect.ValueOf(root).Elem()
	for i := 0; i < rv.NumField(); i++ {
		if rv.Type().Field(i).PkgPath == "" {
			scan.walk(rv.Field(i), false)
		}
	}
	if root.GlobalScope != nil {
		scan.rootScopes[root.GlobalScope] = true
		for _, symbol := range root.GlobalScope.Symbols {
			scan.walk(reflect.ValueOf(symbol), false)
		}
	}
	for _, path := range specializationASTMapPaths() {
		table := specializationField(reflect.ValueOf(root.analyzer).Elem(), path)
		if !table.IsNil() {
			// Route through walk so its map identity guard skips tables already
			// visited through Result while still scanning private analyzer tables.
			scan.walk(table, false)
		}
	}
	return scan.rootSymbols, scan.rootScopes
}
