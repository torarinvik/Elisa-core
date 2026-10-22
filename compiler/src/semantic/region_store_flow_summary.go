package semantic

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unicode"

	"elisacore/src/ast"
	"elisacore/src/lexer"
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

// storeFlowKey names the class of n: its pure-source class, or its written-name class.
func (c *storeFlowCtx) storeFlowKey(n string) string {
	if c.isPure(n) {
		return "p:" + c.pu.find(n)
	}
	return "u:" + c.u.find(n)
}

// flow records that the contents of the src names may be stored into the dst names, without
// letting dst's contents reach src: a callee that reads a parameter and writes another.
func (c *storeFlowCtx) flow(src, dst []string) {
	for _, s := range src {
		if c.scalars[storeFlowBaseName(s)] {
			continue
		}
		for _, d := range dst {
			if c.scalars[storeFlowBaseName(d)] || s == d {
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
	scalars := idx.typedNames(storeFlowScalarTypeExpr)
	if decl == nil {
		return scalars
	}
	total, pointerFree := map[string]int{}, map[string]int{}
	for _, binder := range idx.typed {
		total[binder.name]++
		if storeFlowScalarTypeExpr(binder.t) || a.storeFlowPointerFreeTypeExpr(binder.t, map[Type]bool{}) {
			pointerFree[binder.name]++
		}
	}
	for name, count := range total {
		if count > 0 && pointerFree[name] == count && !idx.untyped[name] {
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

// storeFlowSingleNamedTypes maps each name whose every declaration in decl (parameters and
// locals, never a binder) has one and the same plain named type, looking through mutable and
// reference wrappers, to that type name.
func storeFlowSingleNamedTypes(decl *ast.FuncDecl) map[string]string {
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
	candidates := storeFlowTypedNames(decl, func(t ast.TypeExpr) bool { return named(t) != "" })
	result := map[string]string{}
	conflict := map[string]bool{}
	note := func(name string, t ast.TypeExpr) {
		if !candidates[name] {
			return
		}
		n := named(t)
		if prev, ok := result[name]; ok && prev != n {
			conflict[name] = true
		}
		result[name] = n
	}
	for _, param := range decl.Params {
		note(param.Name, param.Type)
	}
	fillMayAdoptWalk(reflect.ValueOf(decl.Body), func(v any) {
		if vd, ok := v.(*ast.VarDeclStmt); ok && vd != nil {
			note(vd.Name, vd.Type)
		}
	})
	for name := range conflict {
		delete(result, name)
	}
	return result
}

// storeFlowRangeIndexType is the type a numeric `for i in a..<b` binder has for the purposes
// of the typed-name predicates: a builtin integer.
var storeFlowRangeIndexType ast.TypeExpr = &ast.BuiltinTypeExpr{Name: "usize"}

// storeFlowValueNamedTypeExpr reports a by-value named type without a region annotation.
func storeFlowValueNamedTypeExpr(t ast.TypeExpr) bool {
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
	allNames map[string]bool
	// Integer classification is queried per expression; callers only read this set.
	integerNames map[string]bool
}

type storeFlowTypedBinder struct {
	name string
	t    ast.TypeExpr
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
	// A false entry is a permanent veto from a conflicting declaration or
	// untyped binder. Keep candidates and vetoes in one pre-sized map rather
	// than growing two maps and then subtracting one from the other.
	names := make(map[string]bool, len(idx.binderNames()))
	note := func(name string, accepted bool) {
		if previous, exists := names[name]; !exists || !accepted || previous {
			names[name] = accepted
		}
	}
	for _, binder := range idx.typed {
		note(binder.name, pred(binder.t))
	}
	if pred(storeFlowRangeIndexType) {
		for _, name := range idx.rangeNames {
			note(name, true)
		}
	} else {
		for _, name := range idx.rangeOther {
			note(name, false)
		}
	}
	for _, name := range idx.other {
		note(name, false)
	}
	for name, accepted := range names {
		if !accepted {
			delete(names, name)
		}
	}
	return names
}

func (idx *storeFlowBinderIndex) integerBinderNames() map[string]bool {
	if idx.integerNames == nil {
		idx.integerNames = idx.typedNames(storeFlowIntegerTypeExpr)
	}
	return idx.integerNames
}

func (idx *storeFlowBinderIndex) binderNames() map[string]bool {
	if idx.allNames != nil {
		return idx.allNames
	}
	scalar, other := idx.sets(func(ast.TypeExpr) bool { return true })
	for n := range other {
		scalar[n] = true
	}
	idx.allNames = scalar
	return scalar
}

// buildStoreFlowBinderIndex walks decl once, collecting its binding sites.
func buildStoreFlowBinderIndex(decl *ast.FuncDecl) *storeFlowBinderIndex {
	idx := &storeFlowBinderIndex{untyped: map[string]bool{}}
	for _, param := range decl.Params {
		idx.typed = append(idx.typed, storeFlowTypedBinder{name: param.Name, t: param.Type})
	}
	fillMayAdoptWalk(reflect.ValueOf(decl.Body), func(v any) {
		if vd, ok := v.(*ast.VarDeclStmt); ok {
			if vd != nil {
				idx.typed = append(idx.typed, storeFlowTypedBinder{name: vd.Name, t: vd.Type})
			}
			return
		}
		rv := reflect.ValueOf(v)
		if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
			return
		}
		elem := rv.Elem()
		out := &idx.other
		if fs, ok := v.(*ast.ForStmt); ok {
			idx.rangeNames = append(idx.rangeNames, fs.Name)
			out = &idx.rangeOther
		}
		if !storeFlowBinderNode(elem.Type().Name()) {
			return
		}
		rfs := reflectFields(elem.Type())
		for i := 0; i < elem.NumField(); i++ {
			f := elem.Field(i)
			if fieldName := rfs[i].Name; fieldName == "Captures" || fieldName == "HeaderCaptures" {
				continue // capture lists name existing bindings; they bind nothing new
			}
			switch {
			case f.Kind() == reflect.String:
				if storeScanQualifierFields[rfs[i].Name] {
					continue // pattern selectors name types/variants, not bindings
				}
				name := f.String()
				*out = append(*out, name)
				if name != "" {
					if _, isRange := v.(*ast.ForStmt); !isRange || rfs[i].Name != "Name" {
						idx.untyped[name] = true
					}
				}
			case f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String:
				for j := 0; j < f.Len(); j++ {
					name := f.Index(j).String()
					*out = append(*out, name)
					if name != "" {
						idx.untyped[name] = true
					}
				}
			case f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.Struct:
				// Pattern argument strings are field labels; the AST walker visits their binders.
				if f.Type().Elem() == storeScanPatternArgType {
					continue
				}
				// value-typed binder records (e.g. []ParamDecl): their string fields bind too
				for j := 0; j < f.Len(); j++ {
					item := f.Index(j)
					for k := 0; k < item.NumField(); k++ {
						if item.Field(k).Kind() == reflect.String {
							name := item.Field(k).String()
							*out = append(*out, name)
							if name != "" {
								idx.untyped[name] = true
							}
						}
					}
				}
			}
		}
	})
	return idx
}

func (c *storeFlowCtx) isPure(name string) bool {
	return name != storeFlowRetName && !c.written[name]
}

func (c *storeFlowCtx) link(names []string) {
	var sinks, sources []string
	for _, n := range names {
		if c.scalars[storeFlowBaseName(n)] {
			continue // a scalar local neither holds nor aliases a region reference
		}
		if c.isPure(n) {
			sources = append(sources, n)
		} else {
			sinks = append(sinks, n)
		}
	}
	c.u.unionAll(sinks)
	c.pu.unionAll(sources)
	if len(sinks) > 0 {
		for _, src := range sources {
			c.edges = append(c.edges, [2]string{src, sinks[0]})
		}
	}
}

// reaches reports whether the contents of src may reach sink along the one-way flows, through
// the classes they pass (a class is reached as a whole: its names exchange contents).
func (c *storeFlowCtx) reaches(anchors map[string][]string, src, sink string) bool {
	target := c.storeFlowKey(sink)
	start := c.storeFlowKey(src)
	seen := map[string]bool{start: true}
	work := []string{start}
	for len(work) > 0 {
		k := work[len(work)-1]
		work = work[:len(work)-1]
		if k == target {
			return true
		}
		for _, n := range anchors[k] {
			nk := c.storeFlowKey(n)
			if !seen[nk] {
				seen[nk] = true
				work = append(work, nk)
			}
		}
	}
	return false
}

// storeFlowSkipFields are string fields that never name a variable.
var storeFlowSkipFields = map[string]bool{
	"Field": true, "Value": true, "Suffix": true, "ArgNames": true, "Keyword": true, "AutovecReason": true,
	"Type": true, "TypeName": true, "EnumName": true, "Variant": true, "WildReason": true,
	"ScopedTheory": true, "PatternFilterSubject": true, "AsKind": true, "Text": true, "Kind": true,
	"Allocator": true, "Fallback": true,
	// Loop and machine header capture lists are a mutation manifest (notation): the writes they
	// license happen in the body statements, which are walked on their own.
	"Captures": true, "HeaderCaptures": true,
}

// storeFlowVariableName reports whether a mentioned string can be a value name: type and
// variant paths are not.
func storeFlowVariableName(name string) bool {
	if name == "" || strings.Contains(name, "::") || strings.Contains(name, ".") {
		return false
	}
	first := []rune(name)[0]
	return !unicode.IsUpper(first)
}

type nameUnion struct{ parent map[string]string }

func (u *nameUnion) find(x string) string {
	if u.parent == nil {
		u.parent = map[string]string{}
	}
	p, ok := u.parent[x]
	if !ok {
		u.parent[x] = x
		return x
	}
	if p == x {
		return x
	}
	root := u.find(p)
	u.parent[x] = root
	return root
}

func (u *nameUnion) union(x, y string) { u.parent[u.find(x)] = u.find(y) }

func (u *nameUnion) unionAll(names []string) {
	for i := 1; i < len(names); i++ {
		u.union(names[0], names[i])
	}
}

// storeFlowSummaryFor returns the (memoized) summary of decl, or nil when none can be trusted.
//
// Recursion is solved per strongly connected component of the call graph (callEdgesOf, by
// name, a superset of the calls storeFlowCallee resolves). Every member of decl's component
// starts from the least summary ("connects nothing") and a worklist recomputes members, growing
// a member's assumption by union and re-queueing its in-component callers whenever it grows,
// until no summary grows. The whole component is then memoized at once. Callees outside the
// component cannot reach back into it, so their summaries settle (and are memoized) on their
// own. Memoizing a member before its component settled made `eb` -> `ea` -> `eb` cache `eb` as
// "never stores r into out" although `ea` does, letting a local reference escape.
func (a *Analyzer) storeFlowSummaryFor(decl *ast.FuncDecl) *storeFlowSummary {
	decl = a.readDecl(decl)
	if !storeFlowSummarizable(decl) {
		return nil
	}
	if s, ok := a.storeFlowSummaries[decl]; ok {
		return s
	}
	if s := a.storeFlowInProgress[decl]; s != nil {
		return s // a member of the component being solved: its current assumption
	}
	if a.storeFlowActive[decl] {
		return nil // defensive: no assumption yet, callers unify conservatively
	}
	if a.storeFlowSummaries == nil {
		a.storeFlowSummaries = map[*ast.FuncDecl]*storeFlowSummary{}
	}
	if a.storeFlowInProgress == nil {
		a.storeFlowInProgress = map[*ast.FuncDecl]*storeFlowSummary{}
	}
	if a.storeFlowActive == nil {
		a.storeFlowActive = map[*ast.FuncDecl]bool{}
	}
	members := a.storeFlowComponent(decl)
	// A nested query can discover an SCC that overlaps one already being solved
	// (for example through worker-local declaration shadows). Starting it would
	// overwrite and then delete the outer solver's in-progress entries. Unknown is
	// conservative for the caller, so leave the active component untouched.
	for _, m := range members {
		if a.storeFlowActive[m] || a.storeFlowInProgress[m] != nil {
			return nil
		}
	}
	inSCC := map[*ast.FuncDecl]bool{}
	for _, m := range members {
		inSCC[m] = true
	}
	callers := map[*ast.FuncDecl][]*ast.FuncDecl{}
	budget := 1
	for _, m := range members {
		a.storeFlowActive[m] = true
		n := len(m.Params)
		a.storeFlowInProgress[m] = newStoreFlowSummary(n)
		budget += n*n + 2*n + 1 // each growth adds at least one bit
		for _, c := range a.callEdgesOf(m) {
			if inSCC[c] {
				callers[c] = append(callers[c], m)
			}
		}
	}
	queued := map[*ast.FuncDecl]bool{}
	work := append([]*ast.FuncDecl(nil), members...)
	for _, m := range members {
		queued[m] = true
	}
	settled := true
	for len(work) > 0 {
		m := work[0]
		work = work[1:]
		queued[m] = false
		next := a.computeStoreFlow(m)
		cur := a.storeFlowInProgress[m]
		if next == nil {
			settled = false
			break
		}
		if cur == nil {
			// A recursive cache alias changed while the component was solving.
			// Do not panic or publish a partial summary; unknown over-approximates.
			settled = false
			break
		}
		if storeFlowSummaryWithin(next, cur) {
			continue
		}
		budget--
		if budget < 0 {
			settled = false
			break
		}
		a.storeFlowInProgress[m] = storeFlowSummaryJoin(cur, next)
		for _, c := range callers[m] {
			if !queued[c] {
				queued[c] = true
				work = append(work, c)
			}
		}
	}
	for _, m := range members {
		s := a.storeFlowInProgress[m]
		delete(a.storeFlowInProgress, m)
		delete(a.storeFlowActive, m)
		if !settled {
			s = nil
		}
		a.storeFlowSummaries[m] = s // nil when it did not settle
	}
	return a.storeFlowSummaries[decl]
}

// invalidateStoreFlowSummaries drops results that predate newly recorded task-group
// proofs. The call-graph SCC cache is independent of those proof facts and remains valid.
func (a *Analyzer) invalidateStoreFlowSummaries() {
	a.storeFlowSummaries = nil
	a.storeFlowInProgress = nil
	a.storeFlowActive = nil
}

func storeFlowSummarizable(decl *ast.FuncDecl) bool {
	if decl == nil || decl.Body == nil || len(decl.TypeParams) != 0 {
		return false
	}
	// Region parameters affect only the lifetime names on references and containers; they do
	// not specialize the function's value-level flow. The body can therefore share one summary
	// across every region instantiation. Type/state/permission/error/value parameters can change
	// the body or stored representation and remain conservative until summaries are specialized.
	for _, param := range decl.GenericParams {
		if param.Kind != ast.GenericParamRegion {
			return false
		}
	}
	return true
}

// storeFlowComponent returns the summarizable members of decl's strongly connected component
// (iterative Tarjan over callEdgesOf). Every component found on the way is cached, so each
// function's component is computed once.
func (a *Analyzer) storeFlowComponent(decl *ast.FuncDecl) []*ast.FuncDecl {
	decl = a.readDecl(decl)
	if comp, ok := a.storeFlowSCC[decl]; ok {
		if mapped, changed := a.parallelReadDeclSlice(comp); changed {
			// Warm SCCs contain canonical declarations. Translate them into this
			// worker's read identities and keep the shadow-bearing slice local.
			a.storeFlowSCC[decl] = mapped
			return mapped
		}
		return comp
	}
	if a.storeFlowSCC == nil {
		a.storeFlowSCC = map[*ast.FuncDecl][]*ast.FuncDecl{}
	}
	index := map[*ast.FuncDecl]int{}
	low := map[*ast.FuncDecl]int{}
	onStack := map[*ast.FuncDecl]bool{}
	var stack []*ast.FuncDecl
	type frame struct {
		fn   *ast.FuncDecl
		next int
	}
	counter := 0
	edges := func(fn *ast.FuncDecl) []*ast.FuncDecl {
		var out []*ast.FuncDecl
		for _, c := range a.callEdgesOf(fn) {
			if !storeFlowSummarizable(c) {
				continue
			}
			if _, done := a.storeFlowSCC[c]; !done {
				out = append(out, c)
			}
		}
		return out
	}
	push := func(fn *ast.FuncDecl) frame {
		index[fn], low[fn] = counter, counter
		counter++
		stack = append(stack, fn)
		onStack[fn] = true
		return frame{fn: fn}
	}
	succ := map[*ast.FuncDecl][]*ast.FuncDecl{decl: edges(decl)}
	frames := []frame{push(decl)}
	for len(frames) > 0 {
		top := &frames[len(frames)-1]
		if top.next < len(succ[top.fn]) {
			c := succ[top.fn][top.next]
			top.next++
			if _, seen := index[c]; !seen {
				succ[c] = edges(c)
				frames = append(frames, push(c))
			} else if onStack[c] && index[c] < low[top.fn] {
				low[top.fn] = index[c]
			}
			continue
		}
		fn := top.fn
		frames = frames[:len(frames)-1]
		if len(frames) > 0 && low[fn] < low[frames[len(frames)-1].fn] {
			low[frames[len(frames)-1].fn] = low[fn]
		}
		if low[fn] == index[fn] {
			var comp []*ast.FuncDecl
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == fn {
					break
				}
			}
			for _, w := range comp {
				a.storeFlowSCC[w] = comp
			}
		}
	}
	return a.storeFlowSCC[decl]
}

// storeFlowBoolMatrix keeps matrix cells in one allocation. Each row has a
// bounded capacity so appending to it cannot overwrite the following row.
func storeFlowBoolMatrix(n int) [][]bool {
	rows := make([][]bool, n)
	cells := make([]bool, n*n)
	for i := range rows {
		start, end := i*n, (i+1)*n
		rows[i] = cells[start:end:end]
	}
	return rows
}

// newStoreFlowSummary is the least summary: each parameter reaches only itself.
func newStoreFlowSummary(n int) *storeFlowSummary {
	s := &storeFlowSummary{conn: storeFlowBoolMatrix(n), reach: storeFlowBoolMatrix(n), retConn: make([]bool, n), written: make([]bool, n)}
	for i := 0; i < n; i++ {
		s.conn[i][i] = true
		s.reach[i][i] = true
	}
	return s
}

// storeFlowSummaryWithin reports that every flow x states, y states too.
func storeFlowSummaryWithin(x, y *storeFlowSummary) bool {
	n := len(y.conn)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if x.conn[i][j] && !y.conn[i][j] || x.reach[i][j] && !y.reach[i][j] {
				return false
			}
		}
		if x.retConn[i] && !y.retConn[i] || x.written[i] && !y.written[i] {
			return false
		}
	}
	return true
}

// storeFlowSummaryJoin is the pointwise union of two summaries of the same function.
func storeFlowSummaryJoin(x, y *storeFlowSummary) *storeFlowSummary {
	n := len(y.conn)
	out := newStoreFlowSummary(n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			out.conn[i][j] = x.conn[i][j] || y.conn[i][j]
			out.reach[i][j] = x.reach[i][j] || y.reach[i][j]
		}
		out.retConn[i] = x.retConn[i] || y.retConn[i]
		out.written[i] = x.written[i] || y.written[i]
	}
	return out
}

func (a *Analyzer) computeStoreFlow(decl *ast.FuncDecl) *storeFlowSummary {
	decl = a.readDecl(decl)
	savedFlowDecl := a.storeFlowCurrentDecl
	a.storeFlowCurrentDecl = decl
	defer func() { a.storeFlowCurrentDecl = savedFlowDecl }()
	savedNamespace, savedUsings := a.currentNamespace, a.currentUsings
	if sym := a.funcDeclSymbols[decl]; sym != nil {
		if index := lastDotIndex(sym.Name); index >= 0 {
			a.currentNamespace = sym.Name[:index]
		} else {
			a.currentNamespace = ""
		}
	}
	a.currentUsings = append([]string(nil), a.funcDeclUsings[decl]...)
	defer func() { a.currentNamespace, a.currentUsings = savedNamespace, savedUsings }()
	savedParams := a.storeFlowParams
	a.storeFlowParams = map[string]bool{}
	for _, param := range decl.Params {
		a.storeFlowParams[param.Name] = true
	}
	savedDarrays := a.storeFlowDarrays
	a.storeFlowDarrays = a.storeFlowBinderIndexFor(decl).typedNames(storeFlowDarrayTypeExpr)
	savedLocals := a.storeFlowLocals
	a.storeFlowLocals = a.storeFlowBinderIndexFor(decl).binderNames()
	defer func() { a.storeFlowLocals = savedLocals }()
	savedViews := a.storeFlowViews
	a.storeFlowViews = a.storeFlowBinderIndexFor(decl).typedNames(storeFlowViewTypeExpr)
	defer func() { a.storeFlowViews = savedViews }()
	savedNamed := a.storeFlowNamedTypes
	a.storeFlowNamedTypes = storeFlowSingleNamedTypes(decl)
	defer func() { a.storeFlowNamedTypes = savedNamed }()
	savedEnv, savedStmtEnv := a.storeFlowEnv, a.storeFlowStmtEnv
	a.storeFlowEnv, a.storeFlowStmtEnv = nil, map[uintptr]map[string]string{}
	defer func() { a.storeFlowEnv, a.storeFlowStmtEnv = savedEnv, savedStmtEnv }()
	defer func() { a.storeFlowParams, a.storeFlowDarrays = savedParams, savedDarrays }()
	c := &storeFlowCtx{written: a.storeFlowWritten(decl), scalars: a.storeFlowScalarNames(decl)}
	for _, param := range decl.Params {
		c.u.find(param.Name)
		c.pu.find(param.Name)
	}
	a.storeFlowNodes(reflect.ValueOf(decl.Body), c, map[uintptr]bool{})
	anchors := map[string][]string{}
	for _, e := range c.edges {
		root := c.storeFlowKey(e[0])
		anchors[root] = append(anchors[root], e[1])
	}
	n := len(decl.Params)
	conn := storeFlowBoolMatrix(n)
	reach := storeFlowBoolMatrix(n)
	retConn := make([]bool, n)
	// A function that yields a value on a path with no `return` (or threads lmut values back)
	// is treated as returning something derived from every parameter.
	returnsValue := decl.ReturnType != nil
	if sym := a.funcDeclSymbols[decl]; sym != nil {
		if ft, ok := sym.Type.(*FuncType); ok && ft != nil && ft.Return != nil && isVoidType(ft.Return) {
			returnsValue = false
		}
	}
	implicit := len(decl.LmutThreadSlots) != 0 || (returnsValue && !storeFlowEndsInReturn(decl.Body))
	// A scalar result carries no reference: nothing a parameter holds reaches the caller through it.
	scalarResult := len(decl.LmutThreadSlots) == 0 && decl.ReturnType != nil && storeFlowScalarTypeExpr(decl.ReturnType)
	if len(decl.LmutThreadSlots) == 0 {
		if sym := a.funcDeclSymbols[decl]; sym != nil {
			if ft, ok := sym.Type.(*FuncType); ok && ft != nil && ft.Return != nil {
				scalarResult = storeFlowPointerFreeReturn(ft.Return, map[Type]bool{})
			}
		}
	}
	for i, pi := range decl.Params {
		iPure := c.isPure(pi.Name)
		for j, pj := range decl.Params {
			jPure := c.isPure(pj.Name)
			switch {
			case i == j:
				reach[i][j] = true
			case !iPure && !jPure:
				reach[i][j] = c.u.find(pi.Name) == c.u.find(pj.Name) || c.reaches(anchors, pi.Name, pj.Name)
			case iPure && jPure:
				reach[i][j] = c.pu.find(pi.Name) == c.pu.find(pj.Name)
			case iPure:
				reach[i][j] = c.reaches(anchors, pi.Name, pj.Name)
			default:
				reach[i][j] = false // a parameter that is never written receives nothing
			}
		}
	}
	for i, pi := range decl.Params {
		for j := range decl.Params {
			conn[i][j] = reach[i][j] || reach[j][i]
		}
		iPure := c.isPure(pi.Name)
		if iPure {
			retConn[i] = implicit || c.reaches(anchors, pi.Name, storeFlowRetName)
		} else {
			retConn[i] = implicit || c.u.find(pi.Name) == c.u.find(storeFlowRetName) || c.reaches(anchors, pi.Name, storeFlowRetName)
		}
		if scalarResult {
			retConn[i] = false
		}
	}
	written := make([]bool, n)
	for i, pi := range decl.Params {
		written[i] = !c.isPure(pi.Name)
	}
	return &storeFlowSummary{conn: conn, reach: reach, retConn: retConn, written: written}
}

const storeFlowRetName = "\x00ret"

func storeFlowEndsInReturn(body []ast.Stmt) bool {
	if len(body) == 0 {
		return false
	}
	switch tail := body[len(body)-1].(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.RegionStmt:
		// Function-body inference wraps allocation-bearing bodies in a
		// synthesized lazy region. The region is transparent to control flow;
		// inspect its tail so an explicit return is not mistaken for an
		// implicit result derived from every parameter.
		return tail != nil && storeFlowEndsInReturn(tail.Body)
	case *ast.CanStmt:
		// A plain permission block executes its body directly. Handler forms can
		// redirect control, so keep those conservative.
		return tail != nil && tail.As == "" && tail.HandlerName == "" && len(tail.HandlerArgs) == 0 && storeFlowEndsInReturn(tail.Body)
	case *ast.ScopeStmt:
		return tail != nil && storeFlowEndsInReturn(tail.Body)
	case *ast.InStoreStmt:
		return tail != nil && storeFlowEndsInReturn(tail.Body)
	case *ast.IfStmt:
		if tail == nil || len(tail.Else) == 0 || !storeFlowEndsInReturn(tail.Then) || !storeFlowEndsInReturn(tail.Else) {
			return false
		}
		for _, clause := range tail.Elifs {
			if !storeFlowEndsInReturn(clause.Body) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

var astStmtType = reflect.TypeOf((*ast.Stmt)(nil)).Elem()
var astTypeExprType = reflect.TypeOf((*ast.TypeExpr)(nil)).Elem()
var astMatchArmType = reflect.TypeOf(ast.MatchArm{})

// storeFlowTypeInfo caches what storeFlowMentions asks of a reflect.Type on every node it
// visits: method-set tests (Implements) and the struct field filter were ~4 s of a stage1
// driver compile when recomputed per node.
type storeFlowTypeInfo struct {
	isStmt     bool  // pointer type implementing ast.Stmt
	isTypeExpr bool  // pointer type implementing ast.TypeExpr
	binding    bool  // pointer to a storeFlowBindingExprs node
	fields     []int // struct: exported fields to walk, in declaration order
}

var storeFlowTypeInfoCache sync.Map // map[reflect.Type]*storeFlowTypeInfo

func storeFlowTypeInfoOf(t reflect.Type) *storeFlowTypeInfo {
	if cached, ok := storeFlowTypeInfoCache.Load(t); ok {
		return cached.(*storeFlowTypeInfo)
	}
	info := &storeFlowTypeInfo{}
	switch t.Kind() {
	case reflect.Pointer:
		info.isStmt = t.Implements(astStmtType)
		info.isTypeExpr = t.Implements(astTypeExprType)
		info.binding = storeFlowBindingExprs[t.Elem().Name()]
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			if storeFlowSkipFields[field.Name] && (field.Type.Kind() == reflect.String || field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.String) {
				continue
			}
			info.fields = append(info.fields, i)
		}
	}
	cached, _ := storeFlowTypeInfoCache.LoadOrStore(t, info)
	return cached.(*storeFlowTypeInfo)
}

// storeFlowStmt applies unit to statement stmt and then to the statements nested in it.
func (a *Analyzer) storeFlowStmt(stmt reflect.Value, seen map[uintptr]bool, unit func(stmt reflect.Value) []reflect.Value) {
	a.storeFlowEnv = a.storeFlowStmtEnv[stmt.Pointer()]
	nested := unit(stmt)
	a.storeFlowEnv = nil
	for _, n := range nested {
		seen[n.Pointer()] = true
		a.storeFlowStmt(n, seen, unit)
	}
}

// storeFlowEach visits v; each statement is a unit whose direct (non-statement) content unit
// handles, nested statements being units of their own.
func (a *Analyzer) storeFlowEach(v reflect.Value, seen map[uintptr]bool, unit func(stmt reflect.Value) []reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			a.storeFlowEach(v.Elem(), seen, unit)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			a.storeFlowEach(v.Index(i), seen, unit)
		}
	case reflect.Pointer:
		if v.IsNil() || !v.CanInterface() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		if v.Type().Implements(astStmtType) {
			a.storeFlowStmt(v, seen, unit)
			return
		}
		a.storeFlowEach(v.Elem(), seen, unit)
	case reflect.Struct:
		rfs := reflectFields(v.Type())
		for i := 0; i < v.NumField(); i++ {
			if rfs[i].IsExported() {
				a.storeFlowEach(v.Field(i), seen, unit)
			}
		}
	}
}

func (a *Analyzer) storeFlowNodes(v reflect.Value, c *storeFlowCtx, seen map[uintptr]bool) {
	a.storeFlowEach(v, seen, func(stmt reflect.Value) []reflect.Value { return a.storeFlowUnit(stmt, c) })
}

// storeFlowUnit links the names a statement mentions outside nested statements, applying call
// summaries to the calls among them, and returns the nested statements.
func (a *Analyzer) storeFlowUnit(stmt reflect.Value, c *storeFlowCtx) []reflect.Value {
	var nested []reflect.Value
	var calls []*ast.CallExpr
	callsOnly := a.storeFlowCondOnly(stmt)
	names := a.storeFlowMentions(stmt.Elem(), stmt.Pointer(), nil, &nested, &calls, callsOnly)
	binds := a.storeFlowBinds
	// A scalar local's initializer carries nothing into it: only calls inside can connect
	// their arguments, and names an expression binds there are scoped to that expression.
	scalarDecl := a.storeFlowScalarDecl(stmt)
	if scalarDecl && !callsOnly {
		callsOnly = true
		nested, calls = nil, nil
		names = a.storeFlowMentions(stmt.Elem(), stmt.Pointer(), nil, &nested, &calls, true)
	}
	if callsOnly && binds && !scalarDecl {
		callsOnly = false
		nested, calls = nil, nil
		names = a.storeFlowMentions(stmt.Elem(), stmt.Pointer(), nil, &nested, &calls, false)
	}
	isReturn := false
	if _, ok := stmt.Interface().(*ast.ReturnStmt); ok {
		isReturn = true
		names = append(names, storeFlowRetName)
	}
	// Names inside calls to summarized callees are linked per the callee's connections instead.
	handled := map[*ast.CallExpr][]string{}
	// Innermost calls first (calls lists a call before the calls in its arguments): an
	// argument that is itself a summarized call contributes only what reaches its result.
	for k := len(calls) - 1; k >= 0; k-- {
		if retNames, ok := a.applyCallStoreFlow(calls[k], c, handled); ok {
			handled[calls[k]] = retNames
		}
	}
	if assign, ok := stmt.Interface().(*ast.AssignStmt); ok && assign != nil && a.storeFlowPointerFreePlace(assign.Target) {
		// A borrow-bearing expression can be used to compute a scalar or another
		// pointer-free field, but that assignment does not retain the expression's
		// storage. Keep any call effects on the RHS, while excluding scalar field
		// reads that would otherwise connect their container roots to the target.
		rhsNames := a.storeFlowMentions(reflect.ValueOf(assign.Value), 0, handled, nil, nil, false)
		c.link(storeFlowRemoveNames(rhsNames, a.storeFlowPointerFreeAccessRoots(assign.Value)))
		return nested
	}
	if binding := a.storeFlowViewTarget(stmt); binding != "" && !binds && len(handled) == len(calls) {
		// A view local (`v: sview = …` / `v <- …`) is never stored through: whatever the
		// initializer mentions may reach v, but nothing flows back into those names, and they
		// are not joined to each other through it. Every call inside must be summarized, so the
		// names in its arguments were already placed by the callee's connections.
		outside := a.storeFlowMentions(stmt.Elem(), stmt.Pointer(), handled, nil, nil, false)
		c.flow(outside, []string{binding})
		c.link([]string{binding})
	} else if isReturn && !binds && len(handled) == len(calls) {
		// `return e` with every call summarized: what e mentions reaches the result, but the
		// return itself stores nothing into those names nor joins them to each other.
		outside := a.storeFlowMentions(stmt.Elem(), stmt.Pointer(), handled, nil, nil, false)
		c.flow(outside, []string{storeFlowRetName})
	} else if len(handled) == 0 {
		c.link(names)
	} else if call, binding := a.storeFlowViewBinding(stmt); call != nil && handled[call] != nil {
		// `v: sview = f(...)`: the result views bytes the arguments hold, so they reach v, but a
		// view binding is never stored through: nothing flows back into the arguments, and the
		// arguments feeding it are not joined to each other through it.
		c.flow(handled[call], []string{binding})
		c.link([]string{binding})
	} else {
		// Names inside a handled call were placed by its summary; the rest of the statement
		// joins with whatever the callee lets reach its result.
		outside := a.storeFlowMentions(stmt.Elem(), stmt.Pointer(), handled, nil, nil, callsOnly)
		if isReturn {
			outside = append(outside, storeFlowRetName)
		}
		c.link(outside)
	}
	return nested
}

// storeFlowPointerFreePlace reports an assignment place whose resolved value cannot
// contain Elisa region storage. A write to such a field/index may mutate its owning
// aggregate, but it cannot store a borrow there.
func (a *Analyzer) storeFlowPointerFreePlace(expr ast.Expr) bool {
	typ := a.exprTypes[expr]
	if typ == nil {
		typ = a.storeFlowPlaceType(expr)
	}
	if typ != nil {
		return storeFlowPointerFreeReturn(typ, map[Type]bool{})
	}
	// During summary convergence, assignment targets are visited before their
	// index-base expression has necessarily been type-checked. Recover the
	// element type from every same-named typed binder rather than treating a
	// cold `values: darray[u32]` base as unknown and linking its scalar store to
	// every name in the assignment. Shadowing and untyped/generic bindings fail
	// closed below.
	index, ok := returnBorrowStripParens(expr).(*ast.IndexExpr)
	return ok && a.storeFlowPointerFreeIndexedElement(index.Object)
}

func (a *Analyzer) storeFlowPointerFreeIndexedElement(base ast.Expr) bool {
	base = returnBorrowStripParens(base)
	id, ok := base.(*ast.Ident)
	if !ok || id == nil || id.Name == "" || a.storeFlowCurrentDecl == nil {
		return false
	}
	idx := a.storeFlowBinderIndexFor(a.storeFlowCurrentDecl)
	if idx.untyped[id.Name] {
		return false
	}
	for _, name := range idx.rangeNames {
		if name == id.Name {
			return false
		}
	}
	found := false
	for _, binder := range idx.typed {
		if binder.name != id.Name {
			continue
		}
		elem, isDarray := storeFlowDarrayElementTypeExpr(binder.t)
		if !isDarray || !a.storeFlowPointerFreeTypeExpr(elem, map[Type]bool{}) {
			return false
		}
		found = true
	}
	return found
}

func storeFlowDarrayElementTypeExpr(t ast.TypeExpr) (ast.TypeExpr, bool) {
	for t != nil {
		switch tt := t.(type) {
		case *ast.MutableType:
			if tt == nil {
				return nil, false
			}
			t = tt.Elem
		case *ast.RefType:
			if tt == nil {
				return nil, false
			}
			t = tt.Elem
		case *ast.BuiltinTypeExpr:
			if tt != nil && tt.Name == "darray" && len(tt.TypeArgs) == 1 && len(tt.ValueArgs) == 0 {
				return tt.TypeArgs[0], true
			}
			return nil, false
		default:
			return nil, false
		}
	}
	return nil, false
}

// storeFlowPointerFreeAccessRoots counts roots named only by pointer-free field or
// index reads. Those reads produce scalar/POD values and must not make their aggregate
// appear to exchange a borrow with another expression in the same assignment.
func (a *Analyzer) storeFlowPointerFreeAccessRoots(expr ast.Expr) map[string]int {
	roots := map[string]int{}
	var walk func(reflect.Value)
	walk = func(value reflect.Value) {
		if !value.IsValid() {
			return
		}
		switch value.Kind() {
		case reflect.Interface:
			if !value.IsNil() {
				walk(value.Elem())
			}
		case reflect.Pointer:
			if value.IsNil() || !value.CanInterface() {
				return
			}
			var place ast.Expr
			switch node := value.Interface().(type) {
			case *ast.FieldExpr:
				place = node
			case *ast.IndexExpr:
				place = node
			}
			if place != nil && a.storeFlowPointerFreePlace(place) {
				if names, ok := storeFlowChainRoots(place); ok {
					for _, name := range names {
						roots[name]++
					}
					return
				}
			}
			walk(value.Elem())
		case reflect.Struct:
			for _, index := range storeFlowTypeInfoOf(value.Type()).fields {
				walk(value.Field(index))
			}
		case reflect.Slice, reflect.Array:
			for index := 0; index < value.Len(); index++ {
				walk(value.Index(index))
			}
		}
	}
	walk(reflect.ValueOf(expr))
	return roots
}

func storeFlowRemoveNames(names []string, removed map[string]int) []string {
	if len(removed) == 0 {
		return names
	}
	kept := make([]string, 0, len(names))
	for _, name := range names {
		base := storeFlowBaseName(name)
		if removed[base] > 0 {
			removed[base]--
			continue
		}
		kept = append(kept, name)
	}
	return kept
}

// storeFlowViewBinding returns the call and binding name of a `name: sview = call(...)`
// declaration whose callee is declared to return a view too.
func (a *Analyzer) storeFlowViewBinding(stmt reflect.Value) (*ast.CallExpr, string) {
	vd, ok := stmt.Interface().(*ast.VarDeclStmt)
	if !ok || vd == nil || vd.Owner != nil || !storeFlowViewTypeExpr(vd.Type) {
		return nil, ""
	}
	call, ok := returnBorrowStripParens(vd.Value).(*ast.CallExpr)
	if !ok || call == nil {
		return nil, ""
	}
	callee := a.storeFlowCallee(call)
	if callee == nil || !storeFlowViewTypeExpr(callee.ReturnType) {
		return nil, ""
	}
	return call, a.storeFlowRenamed(vd.Name)
}

// storeFlowViewTarget returns the renamed local a statement binds or rebinds when that local
// is a plain view in every declaration of the name: a `v: sview = …` declaration, or a
// `v <- …` / `v = …` rebind of such a (non-parameter) local.
func (a *Analyzer) storeFlowViewTarget(stmt reflect.Value) string {
	switch st := stmt.Interface().(type) {
	case *ast.VarDeclStmt:
		if st != nil && st.Owner == nil && st.Value != nil && storeFlowViewTypeExpr(st.Type) && a.storeFlowViews[st.Name] {
			return a.storeFlowRenamed(st.Name)
		}
	case *ast.AssignStmt:
		if st == nil || st.Optional {
			return ""
		}
		if id, ok := st.Target.(*ast.Ident); ok && id != nil && a.storeFlowViews[id.Name] && !a.storeFlowParams[id.Name] {
			return a.storeFlowRenamed(id.Name)
		}
	}
	return ""
}

// storeFlowViewTypeExpr reports a plain immutable byte view type (`sview`, possibly mutable as
// a binding: rebinding a view never writes through it).
func storeFlowViewTypeExpr(t ast.TypeExpr) bool {
	if mt, ok := t.(*ast.MutableType); ok && mt != nil {
		t = mt.Elem
	}
	switch tt := t.(type) {
	case *ast.NamedType:
		return tt != nil && tt.Region == "" && tt.Name == "sview"
	case *ast.BuiltinTypeExpr:
		return tt != nil && len(tt.TypeArgs) == 0 && len(tt.ValueArgs) == 0 && tt.Name == "sview"
	}
	return false
}

// storeFlowCondOnly reports whether only the calls in a statement's header can move values: an
// if/while condition merely tests, so the names it reads must not be linked.
func (a *Analyzer) storeFlowScalarDecl(stmt reflect.Value) bool {
	vd, ok := stmt.Interface().(*ast.VarDeclStmt)
	if !ok || vd == nil || vd.Owner != nil {
		return false
	}
	return storeFlowScalarTypeExpr(vd.Type) || a.storeFlowPointerFreeTypeExpr(vd.Type, map[Type]bool{})
}

func (a *Analyzer) storeFlowCondOnly(stmt reflect.Value) bool {
	switch stmt.Interface().(type) {
	case *ast.IfStmt, *ast.WhileStmt:
		return true
	}
	return false
}

// storeFlowBindingExprs are expressions that bind a name to part of a value they inspect.
var storeFlowBindingExprs = map[string]bool{
	"IsPatternExpr": true, "IsAliasExpr": true, "OptionalBindExpr": true, "MatchExpr": true,
	"CatchExpr": true, "UnwrapElseExpr": true, "TryExpr": true, "GetExpr": true, "QueryExpr": true,
	"FoldExpr": true, "LambdaExpr": true, "VariantTestExpr": true, "StructTestExpr": true,
}

// storeFlowRenamed maps a name through the binder scope of the statement being processed.
func (a *Analyzer) storeFlowRenamed(name string) string {
	if renamed, ok := a.storeFlowEnv[name]; ok {
		return renamed
	}
	return name
}

func (a *Analyzer) storeFlowRenamedAll(names []string) []string {
	for i, name := range names {
		names[i] = a.storeFlowRenamed(name)
	}
	return names
}

// storeFlowPatternBinders lists the variable names a match pattern mentions: its binders. A
// pattern names no outer variable it could alias, so each is scoped to its arm.
func storeFlowPatternBinders(pattern ast.MatchPattern) []string {
	var binders []string
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.String:
			if storeFlowVariableName(v.String()) {
				binders = append(binders, v.String())
			}
		case reflect.Struct:
			rfs := reflectFields(v.Type())
			for i := 0; i < v.NumField(); i++ {
				field := &rfs[i]
				if !field.IsExported() || storeFlowSkipFields[field.Name] && (field.Type.Kind() == reflect.String || field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.String) {
					continue
				}
				walk(v.Field(i))
			}
		}
	}
	walk(reflect.ValueOf(pattern))
	return binders
}

// storeFlowReadOnlyMethods are builtin methods that neither mutate their receiver nor move a
// value into it.
var storeFlowReadOnlyMethods = map[string]bool{
	"count": true, "len": true, "is_empty": true, "contains": true, "starts_with": true,
	"ends_with": true, "get": true, "index_of": true, "find": true, "first": true, "last": true,
	"capacity": true, "as_sview": true, "view": true,
}

// storeFlowMentions lists the variable names a statement mentions outside nested statements
// (returned through nested) and reports its calls. A call in handled contributes only the
// names its summary lets reach the result, and its subtree is skipped.
func (a *Analyzer) storeFlowMentions(root reflect.Value, self uintptr, handled map[*ast.CallExpr][]string, nested *[]reflect.Value, calls *[]*ast.CallExpr, callsOnly bool) []string {
	var names []string
	callDepth := 0
	condDepth := 0 // inside a ternary condition: a bool, so its names reach no value
	a.storeFlowBinds = false
	// Scope rename maps are immutable snapshots. Borrow the enclosing environment and
	// clone only when a comprehension or match arm adds binders.
	rename := a.storeFlowEnv
	var walk func(v reflect.Value)
	walkStructFields := func(v reflect.Value) {
		for _, i := range storeFlowTypeInfoOf(v.Type()).fields {
			walk(v.Field(i))
		}
	}
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.String:
			if condDepth == 0 && storeFlowVariableName(v.String()) && (!callsOnly || callDepth > 0) {
				name := v.String()
				if renamed, ok := rename[name]; ok {
					name = renamed
				}
				names = append(names, name)
			}
		case reflect.Pointer:
			if v.IsNil() || !v.CanInterface() {
				return
			}
			info := storeFlowTypeInfoOf(v.Type())
			if info.isStmt && v.Pointer() != self {
				if nested != nil {
					*nested = append(*nested, v)
				}
				if a.storeFlowStmtEnv != nil {
					a.storeFlowStmtEnv[v.Pointer()] = rename
				}
				return
			}
			if info.isTypeExpr {
				return // type syntax names types, not values
			}
			if comp, ok := v.Interface().(*ast.ListComprehensionExpr); ok {
				// A comprehension binder is scoped to it: distinct comprehensions reusing a name
				// must not be unified through it.
				savedRename := rename
				if comp.Name != "" || comp.SecondName != "" {
					scoped := make(map[string]string, len(rename)+2)
					for name, renamed := range rename {
						scoped[name] = renamed
					}
					rename = scoped
				}
				for _, binder := range []string{comp.Name, comp.SecondName} {
					if binder != "" {
						rename[binder] = fmt.Sprintf("%s#%x", binder, v.Pointer())
					}
				}
				walk(v.Elem())
				rename = savedRename
				return
			}
			if tern, ok := v.Interface().(*ast.TernaryExpr); ok && tern != nil && !storeFlowHasBindingExpr(reflect.ValueOf(&tern.Cond).Elem()) {
				// `value if cond else alt`: only the branches become the value. The condition is
				// still walked so its calls (and the statements nested in it) are recorded.
				walk(reflect.ValueOf(&tern.Value).Elem())
				condDepth++
				walk(reflect.ValueOf(&tern.Cond).Elem())
				condDepth--
				walk(reflect.ValueOf(&tern.Alt).Elem())
				return
			}
			if info.binding {
				a.storeFlowBinds = true
			}
			if call, ok := v.Interface().(*ast.CallExpr); ok {
				if callsOnly {
					if fe, isField := call.Func.(*ast.FieldExpr); isField && fe != nil && storeFlowReadOnlyMethods[fe.Field] && !a.isDeclaredFuncName(fe.Field) {
						for _, arg := range call.Args {
							walk(reflect.ValueOf(arg))
						}
						return
					}
				}
				callDepth++
				defer func() { callDepth-- }()
				if calls != nil {
					*calls = append(*calls, call)
				}
				if retNames, isHandled := handled[call]; isHandled {
					if condDepth == 0 {
						names = append(names, retNames...)
					}
					return
				}
				// A declared function's own name is not a variable.
				if ident, ok := call.Func.(*ast.Ident); ok && ident != nil && a.isDeclaredFuncName(ident.Name) && !a.storeFlowParams[ident.Name] {
					for _, arg := range call.Args {
						walk(reflect.ValueOf(arg))
					}
					if call.ResolvedArgsValid {
						for _, arg := range call.ResolvedArgs {
							walk(reflect.ValueOf(arg))
						}
					}
					return
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			// Test the type before boxing: Interface() on a struct value copies it to the heap.
			if v.Type() != astMatchArmType || !v.CanAddr() {
				walkStructFields(v)
				return
			}
			// The arm is addressable: borrow it instead of boxing the entire
			// struct on every traversal of a store-flow summary.
			if arm, ok := v.Addr().Interface().(*ast.MatchArm); ok {
				// A match arm's pattern binders are scoped to that arm (its guard and body):
				// distinct arms and matches reusing a binder name must not be unified through it.
				binders := storeFlowPatternBinders(arm.Pattern)
				savedRename := rename
				if len(binders) != 0 {
					scoped := make(map[string]string, len(rename)+len(binders))
					for name, renamed := range rename {
						scoped[name] = renamed
					}
					for _, binder := range binders {
						scoped[binder] = fmt.Sprintf("%s#%x", binder, v.Addr().Pointer())
					}
					rename = scoped
				}
				walkStructFields(v)
				rename = savedRename
				return
			}
			walkStructFields(v)
		}
	}
	walk(root)
	return names
}

// storeFlowHasBindingExpr: a condition that binds (`v is Some(r)`) hands its binders to the
// branches, so its names must stay connected to the value.
func storeFlowHasBindingExpr(v reflect.Value) bool {
	found := false
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if found || !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if storeFlowBindingExprs[v.Type().Elem().Name()] {
				found = true
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for _, i := range fillMayAdoptExportedFields(v.Type()) {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(v)
	return found
}

func (a *Analyzer) isDeclaredFuncName(name string) bool {
	if a.declaredFuncNames == nil {
		a.declaredFuncNames = map[string]bool{}
		for decl := range a.funcDeclSymbols {
			if decl != nil {
				a.declaredFuncNames[fillMayAdoptLastSegment(decl.Name)] = true
			}
		}
	}
	return a.declaredFuncNames[fillMayAdoptLastSegment(name)]
}

// storeFlowArgNames is storeFlowExprNames with the already summarized calls inside the
// expression standing for what reaches their results.
func (a *Analyzer) storeFlowArgNames(expr ast.Expr, handled map[*ast.CallExpr][]string) []string {
	return a.storeFlowMentions(reflect.ValueOf(&expr).Elem(), 0, handled, nil, nil, false)
}

func (a *Analyzer) storeFlowExprNames(expr ast.Expr) []string {
	return a.storeFlowMentions(reflect.ValueOf(&expr).Elem(), 0, nil, nil, nil, false)
}

// storeFlowCallee resolves a call to the single declared function it can only mean, or nil.
// A summary can cover omitted trailing default parameters only when each is scalar: those
// omitted slots cannot carry a borrow or contribute argument flows to the call.
func storeFlowCallFitsParams(decl *ast.FuncDecl, call *ast.CallExpr) bool {
	if decl == nil || call == nil {
		return false
	}
	args := returnBorrowCallArgs(call)
	if len(args) > len(decl.Params) {
		return false
	}
	if call.ResolvedArgsValid {
		return len(args) == len(decl.Params)
	}
	if call.HasArgForward || call.NamedArgCount() != 0 {
		return false
	}
	for _, param := range decl.Params[len(args):] {
		if !storeFlowDefaultCallArgIsIndependent(param) {
			return false
		}
	}
	return true
}

// storeFlowDefaultCallArgIsIndependent reports defaults that cannot introduce a borrow from
// this call's arguments. Scalar defaults are always independent. A string literal used for an
// unregioned sview is also independent: it refers to static literal storage, while an explicitly
// region-bound view remains conservative.
func storeFlowDefaultCallArgIsIndependent(param ast.ParamDecl) bool {
	if param.DefaultValue == nil {
		return false
	}
	if storeFlowScalarTypeExpr(param.Type) {
		return true
	}
	typ, ok := param.Type.(*ast.NamedType)
	if !ok || typ == nil || typ.Name != "sview" || typ.Region != "" {
		return false
	}
	_, ok = param.DefaultValue.(*ast.StringLit)
	return ok
}

func (a *Analyzer) storeFlowCallee(call *ast.CallExpr) *ast.FuncDecl {
	// A call binding only region parameters (`f[r](...)`) runs the same body for every
	// region instantiation, so it shares the callee's summary.
	if spec, ok := call.Func.(*ast.SpecializeExpr); ok && spec != nil {
		plain := *call
		plain.Func = spec.Operand
		decl := a.storeFlowCallee(&plain)
		if decl == nil || len(decl.TypeParams) != 0 || len(decl.GenericParams) != 0 || len(spec.TypeArgs) != len(decl.RegionParams) {
			return nil
		}
		return decl
	}
	ident, ok := call.Func.(*ast.Ident)
	// Locals are a function-wide conservative set. A possible local callable
	// must not borrow a same-named global function's precise store summary.
	if !ok || ident == nil || a.storeFlowParams[ident.Name] || a.storeFlowLocals[ident.Name] {
		return nil
	}
	// The resolver may have qualified the callee (`Module.name`): match by last segment, and
	// by the full name too when that segment is shared.
	short := fillMayAdoptLastSegment(ident.Name)
	a.callEdgesOf(nil) // builds callDeclsByName
	candidates := a.callDeclsByName[short]
	if len(candidates) > 1 && short != ident.Name {
		var exact []*ast.FuncDecl
		for _, decl := range candidates {
			if decl.Name == ident.Name {
				exact = append(exact, decl)
			}
		}
		candidates = exact
	}
	if len(candidates) != 1 || a.funcDeclSymbols[candidates[0]] == nil || len(a.ufcsFunctionsByName[short]) > 1 {
		return nil
	}
	if !storeFlowCallFitsParams(candidates[0], call) {
		return nil
	}
	return candidates[0]
}

// applyCallStoreFlow links the argument groups of a call the callee's summary connects and
// reports whether the call was fully handled (a resolvable, summarized, non-variadic callee).
func (a *Analyzer) applyCallStoreFlow(call *ast.CallExpr, c *storeFlowCtx, handled map[*ast.CallExpr][]string) ([]string, bool) {
	if _, joined := a.taskGroupSubmits[call]; joined {
		// The verified TaskGroup window discards this Task and joins it before the
		// enclosing function can return. Treating pool_submit1's temporary capture
		// as persistent store flow would falsely make worker payload borrows escape
		// through the pool. Argument thread-transfer and worker-capture checks run
		// during ordinary call analysis; this only removes the post-join store edge.
		return nil, true
	}
	if a.storeFlowEnumVariantConstructor(call) {
		// `Enum.Variant(payload...)` creates a fresh value containing exactly its
		// payloads. The payloads reach the expression result, but the constructor
		// does not store them into unrelated state or mutate the enum type.
		var retNames []string
		for _, arg := range returnBorrowCallArgs(call) {
			for _, name := range a.storeFlowArgNames(arg, handled) {
				if !c.scalars[storeFlowBaseName(name)] {
					retNames = append(retNames, name)
				}
			}
		}
		return retNames, true
	}
	if fn, ok := call.Func.(*ast.FieldExpr); ok && fn != nil && call.SafeReceiver == nil && call.NamedArgCount() == 0 && (a.isCompilerBuiltinHelperCall(call, "darray.push") || a.storeFlowBuiltinDarrayGrowth(fn)) {
		// Builtin DArray growth copies element values into the receiver. Preserve only
		// element-to-container flow for borrow-bearing element types; pushing or extending
		// pointer-free values cannot retain the source container or view. The returned
		// DArray still depends on its prior receiver contents for fluent forms.
		argumentIndex := -1
		switch fn.Field {
		case "push", "extend":
			if len(call.Args) == 1 {
				argumentIndex = 0
			}
		}
		if argumentIndex < 0 {
			return nil, false
		}
		receiver := a.storeFlowArgNames(fn.Object, handled)
		c.link(receiver)
		retNames := append([]string(nil), receiver...)
		// main: a pointer-free element type cannot retain the source, so no edge at all.
		if !a.storeFlowDarrayElementPointerFree(fn.Object) {
			group := a.storeFlowArgNames(call.Args[argumentIndex], handled)
			c.link(group)
			if a.storeFlowPushedElemMayShare(fn.Object, call.Args[argumentIndex]) {
				// An element that SHARES indirect storage (a container header, a reference) makes
				// the receiver an alias of the source's storage: writes through the receiver's
				// elements (`holder[0][0] <- v` after `holder.push(t.lists)`) land in the source.
				c.link(append(append([]string(nil), group...), receiver...))
			} else {
				c.flow(group, receiver)
			}
			retNames = append(retNames, group...)
		}
		return retNames, true
	}
	if a.storeFlowBuiltinDarrayTruncate(call) {
		// truncate changes only the receiver's visible length. Its integer count is
		// not stored in the array, and the returned array is the same receiver.
		// Keep the receiver -> result dependency while avoiding a false count ->
		// receiver borrow edge.
		return a.storeFlowArgNames(call.Func.(*ast.FieldExpr).Object, handled), true
	}
	if a.storeFlowBuiltinDarrayReserve(call) {
		// Inner calls are processed first. If one is unresolved, leave this
		// outer call conservative so handling reserve cannot hide its effects.
		var capacityCalls []*ast.CallExpr
		a.storeFlowMentions(reflect.ValueOf(call.Args[0]), 0, nil, nil, &capacityCalls, false)
		for _, inner := range capacityCalls {
			if _, ok := handled[inner]; !ok {
				return nil, false
			}
		}
		// Capacity controls allocation, but is never stored as an element.
		// reserve returns its mutable receiver, including its existing contents.
		return a.storeFlowArgNames(call.Func.(*ast.FieldExpr).Object, handled), true
	}
	callee := a.storeFlowCallee(call)
	if callee == nil {
		if !a.storeFlowOpaqueExternCall(call) {
			return nil, false
		}
		// A native extern over scalars and opaque handles cannot hold an Elisa reference in
		// any parameter: it connects no two arguments, and its result is (conservatively)
		// derived from all of them.
		var retNames []string
		for _, arg := range returnBorrowCallArgs(call) {
			group := a.storeFlowArgNames(arg, handled)
			c.link(group)
			retNames = append(retNames, group...)
		}
		return retNames, true
	}
	args := returnBorrowCallArgs(call)
	summary := a.storeFlowSummaryFor(callee)
	if summary == nil {
		return nil, false
	}
	groupNames := make([][]string, len(args))
	for j, arg := range args {
		groupNames[j] = a.storeFlowArgNames(arg, handled)
		c.link(groupNames[j])
	}
	for i := 0; i < len(args); i++ {
		for j := i + 1; j < len(args); j++ {
			if !summary.connected(i, j) || len(groupNames[i]) == 0 || len(groupNames[j]) == 0 {
				continue
			}
			// A pair flows only into a parameter the callee writes; one that flows both ways
			// exchanges contents. Neither written: the callee stores neither parameter into the
			// other, and whatever both feed (another parameter, the result) is linked through
			// that pair instead.
			ij := summary.writtenParam(j) && summary.reaches(i, j)
			ji := summary.writtenParam(i) && summary.reaches(j, i)
			switch {
			case ij && ji:
				c.link(append(append([]string{}, groupNames[i]...), groupNames[j]...))
			case ij:
				c.flow(groupNames[i], groupNames[j])
			case ji:
				c.flow(groupNames[j], groupNames[i])
			}
		}
	}
	var retNames []string
	for j, connected := range summary.retConn {
		if connected && j < len(groupNames) {
			retNames = append(retNames, groupNames[j]...)
		}
	}
	return retNames, true
}

// storeFlowChainRoots returns the variables an alias-shaped expression (a name, field or index
// path, a reborrow or an aliasing getter) denotes storage of, and whether it has that shape.
func storeFlowChainRoots(expr ast.Expr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n != nil && storeFlowVariableName(n.Name) {
			return []string{n.Name}, true
		}
		return nil, true
	case *ast.ParenExpr:
		return storeFlowChainRoots(n.Inner)
	case *ast.FieldExpr:
		return storeFlowChainRoots(n.Object)
	case *ast.IndexExpr:
		return storeFlowChainRoots(n.Object)
	case *ast.UnaryExpr:
		return storeFlowChainRoots(n.Operand)
	case *ast.MoveExpr:
		return storeFlowChainRoots(n.Operand)
	case *ast.AddrOfExpr:
		return storeFlowChainRoots(n.Operand)
	case *ast.CallExpr:
		if fe, ok := n.Func.(*ast.FieldExpr); ok && fe != nil && storeFlowReadOnlyMethods[fe.Field] {
			return storeFlowChainRoots(fe.Object)
		}
	}
	return nil, false
}

// storeFlowWritten computes the names of decl that may be written: assignment targets, mutated
// receivers, arguments to callees whose summaries show a write (or unresolved callees), and
// everything that aliases one of those. Parameter mutability is only a capability; a settled
// body summary determines whether the capability is exercised. Every other name is a pure source.
func (a *Analyzer) storeFlowWritten(decl *ast.FuncDecl) map[string]bool {
	decl = a.readDecl(decl)
	written := map[string]bool{}
	var ft *FuncType
	if sym := a.funcDeclSymbols[decl]; sym != nil {
		ft, _ = sym.Type.(*FuncType)
	}
	// Hidden trailing parameters (an implicit packed-enum store) follow the declared ones.
	allWritable := len(decl.LmutThreadSlots) != 0 || ft == nil || funcTypeExplicitParamCount(ft) != len(decl.Params)
	for _, param := range decl.Params {
		if allWritable {
			written[param.Name] = true
		}
	}
	markExpr := func(expr ast.Expr) {
		if roots, ok := storeFlowChainRoots(expr); ok {
			for _, r := range roots {
				written[a.storeFlowRenamed(r)] = true
			}
			return
		}
		for _, n := range a.storeFlowExprNames(expr) {
			written[n] = true
		}
	}
	// Rebinding a by-value named local replaces its value; it stores into nothing the local
	// aliases, so the rebind alone does not make it written.
	valueLocals := a.storeFlowBinderIndexFor(decl).typedNames(storeFlowValueNamedTypeExpr)
	for _, param := range decl.Params {
		delete(valueLocals, param.Name)
	}
	var groups [][]string
	a.storeFlowNodes2(decl, func(stmt reflect.Value) []reflect.Value {
		var nested []reflect.Value
		var calls []*ast.CallExpr
		names := a.storeFlowMentions(stmt.Elem(), stmt.Pointer(), nil, &nested, &calls, false)
		binds := a.storeFlowBinds
		for _, call := range calls {
			a.storeFlowMarkCall(call, written, markExpr)
			// A pushed element that shares indirect storage aliases the receiver to it: a write
			// through either is a write of both (applyCallStoreFlow links them the same way).
			if fn, ok := call.Func.(*ast.FieldExpr); ok && fn != nil && fn.Field == "push" && len(call.Args) == 1 {
				if a.storeFlowPushedElemMayShare(fn.Object, call.Args[0]) {
					groups = append(groups, append(a.storeFlowExprNames(fn.Object), a.storeFlowExprNames(call.Args[0])...))
				}
			}
		}
		switch st := stmt.Interface().(type) {
		case *ast.AssignStmt:
			if id, ok := returnBorrowStripParens(st.Target).(*ast.Ident); !ok || id == nil || !valueLocals[id.Name] || st.WriteThrough || st.AsOverlayCall != nil {
				markExpr(st.Target)
			}
			if id, ok := returnBorrowStripParens(st.Target).(*ast.Ident); ok && id != nil {
				if roots, isChain := storeFlowChainRoots(st.Value); isChain {
					groups = append(groups, a.storeFlowRenamedAll(append([]string{id.Name}, roots...)))
				}
			}
		case *ast.AugAssignStmt:
			markExpr(st.Target)
		case *ast.AsRefAssignStmt:
			markExpr(st.Target)
		case *ast.VarDeclStmt:
			if st.Owner != nil {
				markExpr(st.Owner)
			}
			if roots, isChain := storeFlowChainRoots(st.Value); isChain && st.Value != nil {
				groups = append(groups, a.storeFlowRenamedAll(append([]string{st.Name}, roots...)))
			}
		case *ast.ExprStmt, *ast.ReturnStmt, *ast.IfStmt, *ast.WhileStmt, *ast.DiscardStmt:
		case *ast.ForStmt, *ast.IterForStmt, *ast.MatchStmt, *ast.LetDestructureStmt, *ast.TupleBindStmt, *ast.MoveBindStmt:
			groups = append(groups, names)
		default:
			for _, n := range names {
				written[n] = true
			}
		}
		if binds && !a.storeFlowScalarDecl(stmt) {
			groups = append(groups, names)
		}
		return nested
	})
	scalars := a.storeFlowScalarNames(decl)
	for gi, g := range groups {
		var kept []string
		for _, n := range g {
			if !scalars[storeFlowBaseName(n)] {
				kept = append(kept, n)
			}
		}
		groups[gi] = kept
	}
	for changed := true; changed; {
		changed = false
		for _, g := range groups {
			any := false
			for _, n := range g {
				if written[n] {
					any = true
					break
				}
			}
			if !any {
				continue
			}
			for _, n := range g {
				if !written[n] {
					written[n] = true
					changed = true
				}
			}
		}
	}
	return written
}

// storeFlowBaseName strips a scoped binder's "#scope" suffix.
func storeFlowBaseName(name string) string {
	if i := strings.IndexByte(name, '#'); i >= 0 {
		return name[:i]
	}
	return name
}

// storeFlowNodes2 runs unit over every statement of decl.
func (a *Analyzer) storeFlowNodes2(decl *ast.FuncDecl, unit func(stmt reflect.Value) []reflect.Value) {
	decl = a.readDecl(decl)
	a.storeFlowEach(reflect.ValueOf(decl.Body), map[uintptr]bool{}, unit)
}

// storeFlowMarkCall marks what a call may write: a mutated receiver, and the arguments bound to
// writable parameters of the callee (all of them when the callee is not a resolvable declared
// function, except for builtin methods, whose arguments are only read into the receiver).
func (a *Analyzer) storeFlowMarkCall(call *ast.CallExpr, written map[string]bool, mark func(ast.Expr)) {
	if a.storeFlowEnumVariantConstructor(call) {
		return
	}
	args := returnBorrowCallArgs(call)
	switch fn := call.Func.(type) {
	case *ast.FieldExpr:
		if fn == nil {
			return
		}
		if a.storeFlowBuiltinDarrayTruncate(call) || a.storeFlowBuiltinDarrayReserve(call) {
			mark(fn.Object)
			return
		}
		if a.storeFlowBuiltinDarrayGrowth(fn) {
			// The builtin moves its arguments into the receiver: only the receiver is written;
			// the statement link carries the arguments' flow into it.
			mark(fn.Object)
			return
		}
		if a.isDeclaredFuncName(fn.Field) {
			mark(fn.Object)
			for _, arg := range args {
				mark(arg)
			}
			return
		}
		if !storeFlowReadOnlyMethods[fn.Field] {
			mark(fn.Object)
		}
		if call.SafeReceiver != nil {
			mark(call.SafeReceiver)
		}
	case *ast.Ident:
		if callee := a.storeFlowCallee(call); callee != nil {
			var ft *FuncType
			if sym := a.funcDeclSymbols[callee]; sym != nil {
				ft, _ = sym.Type.(*FuncType)
			}
			if ft != nil && funcTypeExplicitParamCount(ft) == len(callee.Params) && len(callee.LmutThreadSlots) == 0 {
				if summary := a.storeFlowSummaryFor(callee); summary != nil {
					for j, arg := range args {
						if summary.writtenParam(j) {
							mark(arg)
						}
					}
					return
				}
				for j, arg := range args {
					if j >= len(callee.Params) || callee.Params[j].Mutable || a.returnBorrowWritableParam(ft.Params[j]) {
						mark(arg)
					}
				}
				return
			}
		}
		for _, arg := range args {
			mark(arg)
		}
	default:
		mark(call.Func)
		for _, arg := range args {
			mark(arg)
		}
	}
}

// storeFlowDarrayGrowthMethods are builtin darray methods that store their arguments into the
// receiver without writing through them.
var storeFlowDarrayGrowthMethods = map[string]bool{"push": true, "insert": true, "extend": true}

// reserve can grow the receiver without retaining its capacity argument. Keep
// this separate from the payload-storing growth methods. Argument type errors
// are diagnosed by builtin call analysis; they cannot turn capacity into payload.
func (a *Analyzer) storeFlowBuiltinDarrayReserve(call *ast.CallExpr) bool {
	if call == nil || call.SafeReceiver != nil || call.HasArgForward || call.NamedArgCount() != 0 || len(call.Args) != 1 {
		return false
	}
	field, ok := call.Func.(*ast.FieldExpr)
	return ok && field != nil && field.Field == "reserve" && a.storeFlowBuiltinDarrayGrowthReceiver(field)
}

// storeFlowBuiltinDarrayTruncate recognizes the builtin DArray truncate operation
// only for a proven DArray receiver and an integer count. In particular, arbitrary
// UFCS methods named truncate stay on the conservative call path.
func (a *Analyzer) storeFlowBuiltinDarrayTruncate(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || field.Field != "truncate" || call.SafeReceiver != nil || call.NamedArgCount() != 0 || len(call.Args) != 1 {
		return false
	}
	if !a.storeFlowBuiltinDarrayGrowthReceiver(field) {
		return false
	}
	return a.storeFlowIntegerExpr(call.Args[0])
}

func (a *Analyzer) storeFlowEnumVariantConstructor(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || field.Object == nil {
		return false
	}
	if ident, isIdent := field.Object.(*ast.Ident); isIdent && ident != nil {
		if a.storeFlowCurrentDecl != nil && a.storeFlowBinderIndexFor(a.storeFlowCurrentDecl).binderNames()[ident.Name] {
			return false
		}
		if a.storeFlowCurrentDecl != nil {
			for _, param := range a.storeFlowCurrentDecl.TypeParams {
				if param == ident.Name {
					return false
				}
			}
			for _, param := range a.storeFlowCurrentDecl.GenericParams {
				if param.Kind == ast.GenericParamType && param.Name == ident.Name {
					return false
				}
			}
		}
		if a.globalScope != nil {
			if sym, found := a.globalScope.Lookup(ident.Name); found && sym != nil && sym.Kind == SymbolGlobal {
				return false
			}
		}
	}
	return a.isEnumVariantConstructorCall(call)
}

func (a *Analyzer) storeFlowIntegerExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.IntLit:
		return e != nil
	case *ast.ParenExpr:
		return e != nil && a.storeFlowIntegerExpr(e.Inner)
	case *ast.UnaryExpr:
		return e != nil && (e.Op == lexer.TOKEN_MINUS || e.Op == lexer.TOKEN_PLUS) && a.storeFlowIntegerExpr(e.Operand)
	case *ast.Ident:
		if e == nil || a.storeFlowCurrentDecl == nil {
			return false
		}
		return a.storeFlowBinderIndexFor(a.storeFlowCurrentDecl).integerBinderNames()[e.Name]
	default:
		return false
	}
}

func storeFlowIntegerTypeExpr(t ast.TypeExpr) bool {
	switch tt := t.(type) {
	case *ast.NamedType:
		return tt != nil && tt.Region == "" && storeFlowIntegerTypeNames[tt.Name]
	case *ast.BuiltinTypeExpr:
		return tt != nil && len(tt.TypeArgs) == 0 && len(tt.ValueArgs) == 0 && storeFlowIntegerTypeNames[tt.Name]
	default:
		return false
	}
}

var storeFlowIntegerTypeNames = map[string]bool{
	"u8": true, "u16": true, "u32": true, "u64": true, "i8": true, "i16": true,
	"i32": true, "i64": true, "usize": true, "isize": true,
}

func (a *Analyzer) storeFlowBuiltinDarrayGrowthReceiver(fn *ast.FieldExpr) bool {
	if fn == nil || !a.storeFlowDarrayPlace(fn.Object) {
		return false
	}
	for _, sym := range a.ufcsFunctionsWithLastSegment(fn.Field) {
		if sym == nil {
			return false
		}
		ft, ok := sym.Type.(*FuncType)
		if !ok || ft == nil {
			return false
		}
		if len(ft.Params) == 0 {
			continue
		}
		if storeFlowUFCSMayAcceptDarrayReceiver(ft.Params[0]) {
			return false
		}
	}
	return true
}

// storeFlowUFCSMayAcceptDarrayReceiver reports whether a visible UFCS receiver could match a
// builtin DArray. A concrete nominal receiver (including a generic struct instantiation) cannot
// accept a builtin DArray; only DArray formals and unresolved type parameters stay conservative.
func storeFlowUFCSMayAcceptDarrayReceiver(t Type) bool {
	switch receiver := t.(type) {
	case *RefType:
		return receiver != nil && storeFlowUFCSMayAcceptDarrayReceiver(receiver.Elem)
	case *AggregateStateType:
		return receiver != nil && storeFlowUFCSMayAcceptDarrayReceiver(receiver.Base)
	case *DArrayType, *TypeParamType:
		return true
	case *GenericInstanceType:
		// DArray and DynArray are an explicit runtime bridge, so an overload on
		// DynArray[T] can receive a builtin DArray. Other nominal generic structs
		// (for example InlineVec[T, N]) cannot.
		_, ok := dynArrayRuntimeInstance(receiver)
		return ok
	default:
		return false
	}
}

// storeFlowBuiltinDarrayGrowth reports a call of a builtin darray growth method on a local or
// parameter whose every declaration is a darray, where no visible UFCS function of that name
// could accept a darray receiver instead.
func (a *Analyzer) storeFlowBuiltinDarrayGrowth(fn *ast.FieldExpr) bool {
	if !storeFlowDarrayGrowthMethods[fn.Field] {
		return false
	}
	return a.storeFlowBuiltinDarrayGrowthReceiver(fn)
}

// ufcsFunctionsWithLastSegment lists every UFCS function whose registered name ends in segment
// (`push` matches `push` and `Mod.push`). The index is rebuilt only after a new registration;
// scanning every UFCS name at each darray growth call was ~2% of analysis.
func (a *Analyzer) ufcsFunctionsWithLastSegment(segment string) []*Symbol {
	if a.ufcsByLastSegment == nil || a.ufcsByLastSegmentVersion != a.ufcsFunctionsVersion {
		index := make(map[string][]*Symbol, len(a.ufcsFunctionsByName))
		for name, byName := range a.ufcsFunctionsByName {
			last := fillMayAdoptLastSegment(name)
			index[last] = append(index[last], byName...)
		}
		a.ufcsByLastSegment = index
		a.ufcsByLastSegmentVersion = a.ufcsFunctionsVersion
	}
	return a.ufcsByLastSegment[segment]
}

// storeFlowDarrayPlace reports a receiver that is certainly a builtin darray: a local or
// parameter every declaration of which is one, or a field chain from a singly declared local or
// parameter of named struct type whose every same-named struct declares the field as a darray.
func (a *Analyzer) storeFlowDarrayPlace(expr ast.Expr) bool {
	if id, ok := expr.(*ast.Ident); ok && id != nil && a.storeFlowDarrays[id.Name] {
		return true
	}
	t := a.storeFlowPlaceType(expr)
	if ref, ok := t.(*RefType); ok && ref != nil {
		t = ref.Elem
	}
	_, isDarray := t.(*DArrayType)
	return isDarray
}

// storeFlowDarrayElementPointerFree proves that a builtin DArray receiver stores
// values whose representation cannot contain an Elisa borrow. A view-bearing or
// unresolved element type stays conservative, since push must propagate its
// source storage into the destination in those cases.
func (a *Analyzer) storeFlowDarrayElementPointerFree(expr ast.Expr) bool {
	if !a.storeFlowDarrayPlace(expr) {
		return false
	}
	if typ := a.storeFlowPlaceType(expr); typ != nil {
		if ref, ok := typ.(*RefType); ok && ref != nil {
			typ = ref.Elem
		}
		if array, ok := typ.(*DArrayType); ok && array != nil {
			return storeFlowPointerFreeReturn(array.Elem, map[Type]bool{})
		}
	}
	id, ok := expr.(*ast.Ident)
	if !ok || id == nil || a.storeFlowCurrentDecl == nil {
		return false
	}
	found := false
	for _, binding := range a.storeFlowBinderIndexFor(a.storeFlowCurrentDecl).typed {
		if binding.name != id.Name {
			continue
		}
		found = true
		elem, isDarray := storeFlowDarrayElementTypeExpr(binding.t)
		if !isDarray || !a.storeFlowPointerFreeTypeExpr(elem, map[Type]bool{}) {
			return false
		}
	}
	return found
}

// storeFlowPlaceType returns the type of a field-chain place, or nil when it is not certain.
func (a *Analyzer) storeFlowPlaceType(expr ast.Expr) Type {
	switch e := expr.(type) {
	case *ast.Ident:
		if e == nil {
			return nil
		}
		nt, ok := a.storeFlowNamedTypes[e.Name]
		if !ok {
			return nil
		}
		return a.storeFlowStructNamed(nt)
	case *ast.FieldExpr:
		if e == nil {
			return nil
		}
		base := a.storeFlowPlaceType(e.Object)
		if ref, ok := base.(*RefType); ok && ref != nil {
			base = ref.Elem
		}
		st, ok := base.(*StructType)
		if !ok || st == nil {
			return nil
		}
		field, ok := st.Fields[e.Field]
		if !ok {
			return nil
		}
		return field.Type
	case *ast.IndexExpr:
		if e == nil {
			return nil
		}
		// Assignment targets are analyzed through analyzeIndexExpr directly, so the
		// target itself may not have an exprTypes entry even though its base does.
		// Recover the element type from that resolved base to distinguish scalar or
		// pointer-free indexed writes from stores that can retain a borrow.
		base := a.exprTypes[e.Object]
		if base == nil {
			base = a.storeFlowPlaceType(e.Object)
		}
		if ref, ok := base.(*RefType); ok && ref != nil {
			base = ref.Elem
		}
		switch indexed := base.(type) {
		case *DArrayType:
			if indexed != nil {
				return indexed.Elem
			}
		case *ArrayType:
			if indexed != nil {
				return indexed.Elem
			}
		case *ViewType:
			if indexed != nil {
				return indexed.Elem
			}
		}
	}
	return nil
}

// storeFlowStructNamed returns the struct type spelled name when every named type whose last
// segment matches it is that one struct type; otherwise nil.
func (a *Analyzer) storeFlowStructNamed(name string) Type {
	short := fillMayAdoptLastSegment(name)
	var found *StructType
	for typeName, named := range a.namedTypes {
		if fillMayAdoptLastSegment(typeName) != short {
			continue
		}
		st, ok := named.(*StructType)
		if !ok || st == nil || (found != nil && found != st) {
			return nil
		}
		found = st
	}
	if found == nil {
		return nil
	}
	return found
}

// callArgsKeptApart reports that the callee is summarized and never lets argument other reach
// the container bound to parameter index.
func (a *Analyzer) callArgsKeptApart(call *ast.CallExpr, index, other int) bool {
	decl, ok := a.resolveReadOnlyScanCallee(call)
	if !ok || decl == nil || !storeFlowCallFitsParams(decl, call) || funcHasArenaParam(decl) {
		return false
	}
	summary := a.storeFlowSummaryFor(decl)
	return summary != nil && !summary.connected(index, other)
}

// callArgNeverReaches reports that the callee is summarized and the contents of argument other
// never reach parameter index (directed: other is never stored into index).
func (a *Analyzer) callArgNeverReaches(call *ast.CallExpr, other, index int) bool {
	decl, ok := a.resolveReadOnlyScanCallee(call)
	if !ok || decl == nil || !storeFlowCallFitsParams(decl, call) || funcHasArenaParam(decl) {
		return false
	}
	summary := a.storeFlowSummaryFor(decl)
	return summary != nil && !summary.reaches(other, index)
}

// storeFlowOpaqueExternCall reports a direct call of a native, non-generic `extern` whose every
// parameter is a scalar or an opaque foreign handle: no parameter can carry an Elisa reference.
func (a *Analyzer) storeFlowOpaqueExternCall(call *ast.CallExpr) bool {
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil || a.storeFlowParams[ident.Name] || a.storeFlowLocals[ident.Name] || a.globalScope == nil {
		return false
	}
	sym, ok := a.globalScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || !fnType.IsNativeExtern || len(fnType.Params) != len(returnBorrowCallArgs(call)) {
		return false
	}
	for _, param := range fnType.Params {
		switch pt := param.(type) {
		case *OpaqueType:
		case *BitIntType:
		case *BuiltinType:
			if pt == nil || !storeFlowScalarTypeNames[pt.Name] {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// storeFlowPushedElemMayShare: an element pushed into receiver may share indirect storage. The
// element is stored as the receiver's element type: a receiver whose element type shares none
// (`darray[u8]`) stores no alias, whatever (untyped literal) expression produced it.
func (a *Analyzer) storeFlowPushedElemMayShare(receiver, elem ast.Expr) bool {
	if rt := a.exprTypes[receiver]; rt != nil && !containsTypeParam(rt) {
		if d, ok := StripAggregateStateType(stripRefForBounds(rt)).(*DArrayType); ok && d != nil && d.Elem != nil && !containsTypeParam(d.Elem) && !a.storeScanSharesCached(d.Elem) {
			return false
		}
	}
	t := a.exprTypes[elem]
	return t == nil || containsTypeParam(t) || a.storeScanSharesCached(t)
}
