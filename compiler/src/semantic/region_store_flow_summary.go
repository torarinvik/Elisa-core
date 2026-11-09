package semantic

import (
	"maps"
	"strings"
	"sync"

	"elisacore/src/ast"
)

// A store-flow summary answers, for a declared function, which parameters may exchange contents:
// param j and param i are CONNECTED when a value reachable from j can end up reachable from i
// (or the reverse) during a call. It is a flow-insensitive, undirected over-approximation over
// names: every simple statement unifies the names it mentions, and a call to a summarized
// callee unifies only the argument groups the callee connects. Anything the summary cannot see
// (an unresolved or overloaded callee, a callee still being summarized) unifies everything the
// call mentions, so a missing edge is never assumed. The caller-side store check uses it to skip
// argument pairs the callee provably keeps apart.
type storeFlowSummary struct {
	conn    [][]bool // conn[i][j]: parameters i and j may exchange contents
	retConn []bool   // parameter i may reach the call's result
	written []bool   // parameter i may be written by the callee (a store destination)
	reach   [][]bool // reach[i][j]: contents of parameter i may reach parameter j (directed conn)
	// Edge kinds of a settled summary (storeFlowSolveKinds): what of parameter i its result /
	// parameter j may receive. Missing entries read as Header.
	retKind   []storeFlowEdgeKind
	reachKind [][]storeFlowEdgeKind
}

// reaches reports whether the contents of parameter i may reach parameter j; unknown means so.
func (s *storeFlowSummary) reaches(i, j int) bool {
	if s == nil || i < 0 || j < 0 || i >= len(s.reach) || j >= len(s.reach) {
		return s.connected(i, j)
	}
	return s.reach[i][j]
}

// writtenParam reports whether the callee may write parameter i; unknown means it may.
func (s *storeFlowSummary) writtenParam(i int) bool {
	return s == nil || i < 0 || i >= len(s.written) || s.written[i]
}

func (s *storeFlowSummary) connected(i, j int) bool {
	if s == nil || i < 0 || j < 0 || i >= len(s.conn) || j >= len(s.conn) {
		return true
	}
	return s.conn[i][j]
}

// storeFlowCtx is the union state of one summary computation. Names split into two kinds:
// a name that may be WRITTEN (a writable parameter, an assignment target, a mutated receiver, an
// argument bound to a writable parameter, or anything aliasing one) is a node that unions with
// the names it exchanges values with, while a name that is never written is a pure SOURCE: it
// can feed several destinations without connecting them to each other, so it only records
// which written names it reaches. Sources being read-only, `stmt` feeding two different
// containers does not let one container's contents reach the other.
type storeFlowCtx struct {
	u       nameUnion
	pu      nameUnion
	written map[string]bool
	scalars map[string]bool
	edges   [][2]string // (source, written name it feeds): a one-way flow
}

type storeFlowClass struct {
	name string
	pure bool
}

// storeFlowKey names the class of n: its pure-source class, or its written-name class.
func (c *storeFlowCtx) storeFlowKey(n string) storeFlowClass {
	if c.isPure(n) {
		return storeFlowClass{name: c.pu.find(n), pure: true}
	}
	return storeFlowClass{name: c.u.find(n)}
}

// flow records that the contents of the src names may be stored into the dst names, without
// letting dst's contents reach src: a callee that reads a parameter and writes another.
func (c *storeFlowCtx) flow(src, dst []string) {
	// A multi-source flow otherwise repeats the same scalar test for every
	// source-destination pair. Keep destination order and duplicates intact.
	var filteredBuffer [8]string
	filteredDestinations := len(src) > 1 && len(dst) > 1
	if filteredDestinations {
		filtered := filteredBuffer[:0]
		for _, d := range dst {
			if !c.scalars[storeFlowBaseName(d)] {
				filtered = append(filtered, d)
			}
		}
		dst = filtered
	}
	for _, s := range src {
		if c.scalars[storeFlowBaseName(s)] {
			continue
		}
		for _, d := range dst {
			if s == d || (!filteredDestinations && c.scalars[storeFlowBaseName(d)]) {
				continue
			}
			c.edges = append(c.edges, [2]string{s, d})
		}
	}
}

var storeFlowScalarTypeNames = map[string]bool{
	"bool": true, "u8": true, "u16": true, "u32": true, "u64": true, "i8": true, "i16": true,
	"i32": true, "i64": true, "usize": true, "isize": true, "f32": true, "f64": true, "char": true,
}

func storeFlowScalarTypeExpr(t ast.TypeExpr) bool {
	switch tt := t.(type) {
	case *ast.NamedType:
		return tt != nil && tt.Region == "" && storeFlowScalarTypeNames[tt.Name]
	case *ast.BuiltinTypeExpr:
		return tt != nil && len(tt.TypeArgs) == 0 && len(tt.ValueArgs) == 0 && storeFlowScalarTypeNames[tt.Name]
	case *ast.TupleTypeExpr:
		// A tuple of scalars carries no reference either.
		if tt == nil || tt.Region != "" || len(tt.Fields) == 0 {
			return false
		}
		for _, field := range tt.Fields {
			if !storeFlowScalarTypeExpr(field.Type) {
				return false
			}
		}
		return true
	}
	return false
}

// storeFlowBinderNodes are node kinds whose own string fields may introduce a binding of
// unknown type; a name bound there is never treated as scalar.
func storeFlowBinderNode(typeName string) bool {
	if storeFlowBindingExprs[typeName] {
		return true
	}
	for _, part := range []string{"Pattern", "Arm", "Bind", "Destructure", "For", "Catch", "Lambda", "Comprehension", "Block", "Machine", "Param"} {
		if strings.Contains(typeName, part) {
			return true
		}
	}
	return false
}

// storeFlowScalarNames returns the names every declaration of which, in decl, has a scalar
// declared type (a parameter or local of bool/integer/float type) and that no pattern, loop,
// lambda or other binder rebinds. Such a name can carry no region reference, so it links nothing.
func (a *Analyzer) storeFlowScalarNames(decl *ast.FuncDecl) map[string]bool {
	decl = a.readDecl(decl)
	idx := a.storeFlowBinderIndexFor(decl)
	idx.classify()
	scalars := maps.Clone(idx.scalarNames)
	if decl == nil {
		return scalars
	}
	pointerFree := make(map[string]bool, len(idx.typed))
	for _, binder := range idx.typed {
		good := storeFlowScalarTypeExpr(binder.t) || a.storeFlowPointerFreeTypeExpr(binder.t, map[Type]bool{})
		prior, exists := pointerFree[binder.name]
		pointerFree[binder.name] = good && (!exists || prior)
	}
	for name, good := range pointerFree {
		if good && !idx.untyped[name] {
			scalars[name] = true
		}
	}
	return scalars
}

// storeFlowDarrayTypeExpr reports a builtin darray type, possibly behind a reference.
func storeFlowDarrayTypeExpr(t ast.TypeExpr) bool {
	if mt, ok := t.(*ast.MutableType); ok && mt != nil {
		t = mt.Elem
	}
	if ref, ok := t.(*ast.RefType); ok && ref != nil {
		t = ref.Elem
	}
	bt, ok := t.(*ast.BuiltinTypeExpr)
	return ok && bt != nil && bt.Name == "darray"
}

// storeFlowPointerFreeTypeExpr resolves the narrowly-supported by-value forms
// whose representation cannot contain an Elisa borrow. It intentionally rejects
// containers: their headers may alias backing storage even when their elements
// are pointer-free.
func (a *Analyzer) storeFlowPointerFreeTypeExpr(t ast.TypeExpr, active map[Type]bool) bool {
	if storeFlowScalarTypeExpr(t) {
		return true
	}
	switch tt := t.(type) {
	case *ast.MutableType:
		return tt != nil && a.storeFlowPointerFreeTypeExpr(tt.Elem, active)
	case *ast.OptionalTypeExpr:
		return tt != nil && a.storeFlowPointerFreeTypeExpr(tt.Value, active)
	case *ast.TupleTypeExpr:
		if tt == nil || tt.Region != "" || len(tt.Fields) == 0 {
			return false
		}
		for _, field := range tt.Fields {
			if !a.storeFlowPointerFreeTypeExpr(field.Type, active) {
				return false
			}
		}
		return true
	case *ast.NamedType:
		if tt == nil || tt.Region != "" {
			return false
		}
		name := tt.Name
		if canonical := a.resolvedTypeName(tt); canonical != "" {
			name = canonical
		}
		return storeFlowPointerFreeReturn(a.storeFlowNamedType(name), active)
	default:
		return false
	}
}

// storeFlowNamedType resolves a name using the same namespace/import context as
// semantic analysis. A suffix match against the global type table would let an
// unrelated module change a StoreFlow result, so unresolved names stay unknown.
func (a *Analyzer) storeFlowNamedType(name string) Type {
	if a == nil {
		return nil
	}
	if a.storeFlowCurrentDecl != nil {
		for _, param := range a.storeFlowCurrentDecl.TypeParams {
			if param == name {
				return nil
			}
		}
		for _, param := range a.storeFlowCurrentDecl.GenericParams {
			if param.Kind == ast.GenericParamType && param.Name == name {
				return nil
			}
		}
	}
	typ, _, ok := a.lookupVisibleType(name)
	if !ok {
		return nil
	}
	return typ
}

// singleNamedTypes maps each name whose every declaration in decl (parameters and
// locals, never a binder) has one and the same plain named type, looking through mutable and
// reference wrappers, to that type name.
func (idx *storeFlowBinderIndex) singleNamedTypes() map[string]string {
	named := func(t ast.TypeExpr) string {
		if mt, ok := t.(*ast.MutableType); ok && mt != nil {
			t = mt.Elem
		}
		if ref, ok := t.(*ast.RefType); ok && ref != nil {
			t = ref.Elem
		}
		if nt, ok := t.(*ast.NamedType); ok && nt != nil && nt.Region == "" {
			return nt.Name
		}
		return ""
	}
	var result map[string]string
	var rejected map[string]bool
	reject := func(name string) {
		if rejected == nil {
			rejected = map[string]bool{}
		}
		rejected[name] = true
		delete(result, name)
	}
	note := func(name string, t ast.TypeExpr) {
		if rejected[name] {
			return
		}
		n := named(t)
		if n == "" {
			reject(name)
			return
		}
		if prev, ok := result[name]; ok && prev != n {
			reject(name)
			return
		}
		if result == nil {
			result = make(map[string]string, len(idx.typed))
		}
		result[name] = n
	}
	for _, binder := range idx.typed {
		note(binder.name, binder.t)
	}
	for _, name := range idx.rangeOther {
		delete(result, name)
	}
	for _, name := range idx.other {
		delete(result, name)
	}
	if result == nil {
		return map[string]string{}
	}
	return result
}

// storeFlowRangeIndexType is the type a numeric `for i in a..<b` binder has for the purposes
// of the typed-name predicates: a builtin integer.
var storeFlowRangeIndexType ast.TypeExpr = &ast.BuiltinTypeExpr{Name: "usize"}

// storeFlowValueNamedTypeExpr reports a by-value named type without a region annotation, an
// optional of one, or a view: rebinding such a local replaces its value and stores into nothing
// it aliased.
func storeFlowValueNamedTypeExpr(t ast.TypeExpr) bool {
	if opt, ok := t.(*ast.OptionalTypeExpr); ok && opt != nil {
		t = opt.Value
	}
	if storeFlowViewTypeExpr(t) {
		return true
	}
	nt, ok := t.(*ast.NamedType)
	return ok && nt != nil && nt.Region == ""
}

// storeFlowTypedNames returns the names every declaration of which, in decl, has a declared
// type satisfying pred and that no pattern, loop, lambda or other binder rebinds.
func storeFlowTypedNames(decl *ast.FuncDecl, pred func(ast.TypeExpr) bool) map[string]bool {
	return buildStoreFlowBinderIndex(decl).typedNames(pred)
}

// storeFlowBinderNames returns every name a function's parameters and body bind.
func storeFlowBinderNames(decl *ast.FuncDecl) map[string]bool {
	return buildStoreFlowBinderIndex(decl).binderNames()
}

// storeFlowBinderIndexFor returns decl's binder index, built once per analyzer: the summaries
// query it with several predicates, and each query used to re-walk the whole body.
func (a *Analyzer) storeFlowBinderIndexFor(decl *ast.FuncDecl) *storeFlowBinderIndex {
	decl = a.readDecl(decl)
	if a.storeFlowBinderIndexes == nil {
		a.storeFlowBinderIndexes = map[*ast.FuncDecl]*storeFlowBinderIndex{}
	}
	if idx, ok := a.storeFlowBinderIndexes[decl]; ok {
		return idx
	}
	idx := buildStoreFlowBinderIndex(decl)
	a.storeFlowBinderIndexes[decl] = idx
	return idx
}

// storeFlowBinderIndex records every binding site of a function: declarations with their
// declared type (parameters and locals), numeric range binders, and untyped binders.
type storeFlowBinderIndex struct {
	typed []storeFlowTypedBinder
	// rangeNames are `for i in a..b` binders; when the predicate rejects an integer, the
	// names in rangeOther (the same statement's string fields) are untyped binders instead.
	rangeNames []string
	rangeOther []string
	other      []string
	untyped    map[string]bool
	// allNames memoizes binderNames: the index is immutable once built, and the store-flow scan
	// asks for the full binder set per identifier (~2 s of map building in the q7 stage1 profile).
	// Callers only read it.
	allNamesOnce sync.Once
	allNames     map[string]bool
	// Integer classification is queried per expression; callers only read this set.
	integerNamesOnce                                     sync.Once
	integerNames                                         map[string]bool
	typedByNameOnce                                      sync.Once
	typedByName                                          map[string][]storeFlowTypedBinder
	rangeNameSetOnce                                     sync.Once
	rangeNameSet                                         map[string]bool
	classificationOnce                                   sync.Once
	darrayNames, viewNames, valueNamedNames, scalarNames map[string]bool
	namedTypes                                           map[string]string
}

type storeFlowTypedBinder struct {
	name string
	t    ast.TypeExpr
}

// These classifications depend only on the immutable binder index, not on the
// current convergence pass or callee summaries. Callers treat the maps as read-only.
func (idx *storeFlowBinderIndex) classify() {
	idx.classificationOnce.Do(func() {
		idx.darrayNames = idx.typedNames(storeFlowDarrayTypeExpr)
		idx.viewNames = idx.typedNames(storeFlowViewTypeExpr)
		idx.valueNamedNames = idx.typedNames(storeFlowValueNamedTypeExpr)
		idx.scalarNames = idx.typedNames(storeFlowScalarTypeExpr)
		idx.namedTypes = idx.singleNamedTypes()
	})
}

// Repeated place queries need only bindings for one name. The index keeps all
// same-named declarations, including shadowed ones, so their vetoes remain.
func (idx *storeFlowBinderIndex) typedBindings(name string) []storeFlowTypedBinder {
	idx.typedByNameOnce.Do(func() {
		idx.typedByName = make(map[string][]storeFlowTypedBinder, len(idx.typed))
		for _, binder := range idx.typed {
			idx.typedByName[binder.name] = append(idx.typedByName[binder.name], binder)
		}
	})
	return idx.typedByName[name]
}

func (idx *storeFlowBinderIndex) hasRangeName(name string) bool {
	idx.rangeNameSetOnce.Do(func() {
		idx.rangeNameSet = make(map[string]bool, len(idx.rangeNames))
		for _, n := range idx.rangeNames {
			idx.rangeNameSet[n] = true
		}
	})
	return idx.rangeNameSet[name]
}

func (idx *storeFlowBinderIndex) sets(pred func(ast.TypeExpr) bool) (map[string]bool, map[string]bool) {
	scalar, other := map[string]bool{}, map[string]bool{}
	for _, b := range idx.typed {
		if pred(b.t) {
			scalar[b.name] = true
		} else {
			other[b.name] = true
		}
	}
	if pred(storeFlowRangeIndexType) {
		for _, n := range idx.rangeNames {
			scalar[n] = true // a numeric range binder is an integer
		}
	} else {
		for _, n := range idx.rangeOther {
			other[n] = true
		}
	}
	for _, n := range idx.other {
		other[n] = true
	}
	return scalar, other
}

func (idx *storeFlowBinderIndex) typedNames(pred func(ast.TypeExpr) bool) map[string]bool {
	// First count accepted declarations to size the map to possible members,
	// not all binders. A second pass applies every veto after candidates have
	// been added, preserving shadowing regardless of declaration order.
	var inline [64]bool
	var matches []bool
	count := 0
	for i, binder := range idx.typed {
		if pred(binder.t) {
			if matches == nil {
				if len(idx.typed) > len(inline) {
					matches = make([]bool, len(idx.typed))
				} else {
					matches = inline[:len(idx.typed)]
				}
			}
			matches[i] = true
			count++
		}
	}
	rangeAccepted := pred(storeFlowRangeIndexType)
	if rangeAccepted {
		count += len(idx.rangeNames)
	}
	if count == 0 {
		return map[string]bool{}
	}
	names := make(map[string]bool, count)
	for i, binder := range idx.typed {
		if matches != nil && matches[i] {
			names[binder.name] = true
		}
	}
	if rangeAccepted {
		for _, name := range idx.rangeNames {
			names[name] = true
		}
	}
	for i, binder := range idx.typed {
		if matches == nil || !matches[i] {
			delete(names, binder.name)
		}
	}
	if !rangeAccepted {
		for _, name := range idx.rangeOther {
			delete(names, name)
		}
	}
	for _, name := range idx.other {
		delete(names, name)
	}
	return names
}

func (idx *storeFlowBinderIndex) integerBinderNames() map[string]bool {
	idx.integerNamesOnce.Do(func() {
		idx.integerNames = idx.typedNames(storeFlowIntegerTypeExpr)
	})
	return idx.integerNames
}

func (idx *storeFlowBinderIndex) binderNames() map[string]bool {
	idx.allNamesOnce.Do(func() {
		scalar, other := idx.sets(func(ast.TypeExpr) bool { return true })
		for n := range other {
			scalar[n] = true
		}
		idx.allNames = scalar
	})
	return idx.allNames
}
