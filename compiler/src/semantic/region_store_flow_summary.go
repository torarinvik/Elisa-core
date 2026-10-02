package semantic

import (
	"fmt"
	"reflect"
	"strings"
	"unicode"

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
	return a.storeFlowBinderIndexFor(decl).typedNames(storeFlowScalarTypeExpr)
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
	scalar, other := idx.sets(pred)
	for n := range other {
		delete(scalar, n)
	}
	return scalar
}

func (idx *storeFlowBinderIndex) binderNames() map[string]bool {
	scalar, other := idx.sets(func(ast.TypeExpr) bool { return true })
	for n := range other {
		scalar[n] = true
	}
	return scalar
}

// buildStoreFlowBinderIndex walks decl once, collecting its binding sites.
func buildStoreFlowBinderIndex(decl *ast.FuncDecl) *storeFlowBinderIndex {
	idx := &storeFlowBinderIndex{}
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
		for i := 0; i < elem.NumField(); i++ {
			f := elem.Field(i)
			if fieldName := elem.Type().Field(i).Name; fieldName == "Captures" || fieldName == "HeaderCaptures" {
				continue // capture lists name existing bindings; they bind nothing new
			}
			switch {
			case f.Kind() == reflect.String:
				*out = append(*out, f.String())
			case f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String:
				for j := 0; j < f.Len(); j++ {
					*out = append(*out, f.Index(j).String())
				}
			case f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.Struct:
				// value-typed binder records (e.g. []ParamDecl): their string fields bind too
				for j := 0; j < f.Len(); j++ {
					item := f.Index(j)
					for k := 0; k < item.NumField(); k++ {
						if item.Field(k).Kind() == reflect.String {
							*out = append(*out, item.Field(k).String())
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
		a.storeFlowInProgress = map[*ast.FuncDecl]*storeFlowSummary{}
		a.storeFlowActive = map[*ast.FuncDecl]bool{}
	}
	members := a.storeFlowComponent(decl)
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

func storeFlowSummarizable(decl *ast.FuncDecl) bool {
	return decl != nil && decl.Body != nil && len(decl.TypeParams) == 0 && len(decl.GenericParams) == 0
}

// storeFlowComponent returns the summarizable members of decl's strongly connected component
// (iterative Tarjan over callEdgesOf). Every component found on the way is cached, so each
// function's component is computed once.
func (a *Analyzer) storeFlowComponent(decl *ast.FuncDecl) []*ast.FuncDecl {
	if comp, ok := a.storeFlowSCC[decl]; ok {
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

// newStoreFlowSummary is the least summary: each parameter reaches only itself.
func newStoreFlowSummary(n int) *storeFlowSummary {
	s := &storeFlowSummary{conn: make([][]bool, n), reach: make([][]bool, n), retConn: make([]bool, n), written: make([]bool, n)}
	for i := 0; i < n; i++ {
		s.conn[i] = make([]bool, n)
		s.reach[i] = make([]bool, n)
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
	conn := make([][]bool, n)
	reach := make([][]bool, n)
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
	for i, pi := range decl.Params {
		reach[i] = make([]bool, n)
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
		conn[i] = make([]bool, n)
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
	_, ok := body[len(body)-1].(*ast.ReturnStmt)
	return ok
}

var astStmtType = reflect.TypeOf((*ast.Stmt)(nil)).Elem()
var astTypeExprType = reflect.TypeOf((*ast.TypeExpr)(nil)).Elem()

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
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
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
	scalarDecl := storeFlowScalarDecl(stmt)
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
	if binding := a.storeFlowViewTarget(stmt); binding != "" && !binds && len(handled) == len(calls) {
		// A view local (`v: sview = …` / `v <- …`) is never stored through: whatever the
		// initializer mentions may reach v, but nothing flows back into those names, and they
		// are not joined to each other through it. Every call inside must be summarized, so the
		// names in its arguments were already placed by the callee's connections.
		var ignoreNested []reflect.Value
		var ignoreCalls []*ast.CallExpr
		outside := a.storeFlowMentions(stmt.Elem(), stmt.Pointer(), handled, &ignoreNested, &ignoreCalls, false)
		c.flow(outside, []string{binding})
		c.link([]string{binding})
	} else if isReturn && !binds && len(handled) == len(calls) {
		// `return e` with every call summarized: what e mentions reaches the result, but the
		// return itself stores nothing into those names nor joins them to each other.
		var ignoreNested []reflect.Value
		var ignoreCalls []*ast.CallExpr
		outside := a.storeFlowMentions(stmt.Elem(), stmt.Pointer(), handled, &ignoreNested, &ignoreCalls, false)
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
		var ignoreNested []reflect.Value
		var ignoreCalls []*ast.CallExpr
		outside := a.storeFlowMentions(stmt.Elem(), stmt.Pointer(), handled, &ignoreNested, &ignoreCalls, callsOnly)
		if isReturn {
			outside = append(outside, storeFlowRetName)
		}
		c.link(outside)
	}
	return nested
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
func storeFlowScalarDecl(stmt reflect.Value) bool {
	vd, ok := stmt.Interface().(*ast.VarDeclStmt)
	return ok && vd != nil && vd.Owner == nil && storeFlowScalarTypeExpr(vd.Type)
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
			for i := 0; i < v.NumField(); i++ {
				field := v.Type().Field(i)
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
	rename := map[string]string{}
	for name, renamed := range a.storeFlowEnv {
		rename[name] = renamed
	}
	var walk func(v reflect.Value)
	walkStructFields := func(v reflect.Value) {
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			if storeFlowSkipFields[field.Name] && (field.Type.Kind() == reflect.String || field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.String) {
				continue
			}
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
			if v.Type().Implements(astStmtType) && v.Pointer() != self {
				*nested = append(*nested, v)
				if a.storeFlowStmtEnv != nil {
					env := make(map[string]string, len(rename))
					for name, renamed := range rename {
						env[name] = renamed
					}
					a.storeFlowStmtEnv[v.Pointer()] = env
				}
				return
			}
			if v.Type().Implements(astTypeExprType) {
				return // type syntax names types, not values
			}
			if comp, ok := v.Interface().(*ast.ListComprehensionExpr); ok {
				// A comprehension binder is scoped to it: distinct comprehensions reusing a name
				// must not be unified through it.
				saved := map[string]string{}
				for _, binder := range []string{comp.Name, comp.SecondName} {
					if binder != "" {
						saved[binder] = rename[binder]
						rename[binder] = fmt.Sprintf("%s#%x", binder, v.Pointer())
					}
				}
				walk(v.Elem())
				for binder, prev := range saved {
					if prev == "" {
						delete(rename, binder)
					} else {
						rename[binder] = prev
					}
				}
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
			if storeFlowBindingExprs[v.Type().Elem().Name()] {
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
				*calls = append(*calls, call)
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
			if arm, ok := v.Interface().(ast.MatchArm); ok && v.CanAddr() {
				// A match arm's pattern binders are scoped to that arm (its guard and body):
				// distinct arms and matches reusing a binder name must not be unified through it.
				saved := map[string]string{}
				had := map[string]bool{}
				for _, binder := range storeFlowPatternBinders(arm.Pattern) {
					if _, done := saved[binder]; done || had[binder] {
						continue
					}
					prev, ok := rename[binder]
					saved[binder], had[binder] = prev, ok
					rename[binder] = fmt.Sprintf("%s#%x", binder, v.Addr().Pointer())
				}
				walkStructFields(v)
				for binder, prev := range saved {
					if had[binder] {
						rename[binder] = prev
					} else {
						delete(rename, binder)
					}
				}
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
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
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
	var nested []reflect.Value
	var calls []*ast.CallExpr
	return a.storeFlowMentions(reflect.ValueOf(&expr).Elem(), 0, handled, &nested, &calls, false)
}

func (a *Analyzer) storeFlowExprNames(expr ast.Expr) []string {
	var nested []reflect.Value
	var calls []*ast.CallExpr
	return a.storeFlowMentions(reflect.ValueOf(&expr).Elem(), 0, nil, &nested, &calls, false)
}

// storeFlowCallee resolves a call to the single declared function it can only mean, or nil.
func (a *Analyzer) storeFlowCallee(call *ast.CallExpr) *ast.FuncDecl {
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil || a.storeFlowParams[ident.Name] {
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
	if len(candidates[0].Params) != len(returnBorrowCallArgs(call)) {
		return nil
	}
	return candidates[0]
}

// applyCallStoreFlow links the argument groups of a call the callee's summary connects and
// reports whether the call was fully handled (a resolvable, summarized, non-variadic callee).
func (a *Analyzer) applyCallStoreFlow(call *ast.CallExpr, c *storeFlowCtx, handled map[*ast.CallExpr][]string) ([]string, bool) {
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
		if connected {
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

// storeFlowWritten computes the names of decl that may be written: writable parameters,
// assignment targets, mutated receivers, arguments bound to writable (or unknown) parameters,
// and everything that aliases one of those. Every other name is a pure source.
func (a *Analyzer) storeFlowWritten(decl *ast.FuncDecl) map[string]bool {
	written := map[string]bool{}
	var ft *FuncType
	if sym := a.funcDeclSymbols[decl]; sym != nil {
		ft, _ = sym.Type.(*FuncType)
	}
	// Hidden trailing parameters (an implicit packed-enum store) follow the declared ones.
	allWritable := len(decl.LmutThreadSlots) != 0 || ft == nil || funcTypeExplicitParamCount(ft) != len(decl.Params)
	for i, param := range decl.Params {
		if allWritable || param.Mutable || a.returnBorrowWritableParam(ft.Params[i]) {
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
		if binds && !storeFlowScalarDecl(stmt) {
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
	a.storeFlowEach(reflect.ValueOf(decl.Body), map[uintptr]bool{}, unit)
}

// storeFlowMarkCall marks what a call may write: a mutated receiver, and the arguments bound to
// writable parameters of the callee (all of them when the callee is not a resolvable declared
// function, except for builtin methods, whose arguments are only read into the receiver).
func (a *Analyzer) storeFlowMarkCall(call *ast.CallExpr, written map[string]bool, mark func(ast.Expr)) {
	args := returnBorrowCallArgs(call)
	switch fn := call.Func.(type) {
	case *ast.FieldExpr:
		if fn == nil {
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
				for j, arg := range args {
					if callee.Params[j].Mutable || a.returnBorrowWritableParam(ft.Params[j]) {
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

// storeFlowBuiltinDarrayGrowth reports a call of a builtin darray growth method on a local or
// parameter whose every declaration is a darray, where no visible UFCS function of that name
// could accept a darray receiver instead.
func (a *Analyzer) storeFlowBuiltinDarrayGrowth(fn *ast.FieldExpr) bool {
	if !storeFlowDarrayGrowthMethods[fn.Field] {
		return false
	}
	if !a.storeFlowDarrayPlace(fn.Object) {
		return false
	}
	var syms []*Symbol
	for name, byName := range a.ufcsFunctionsByName {
		if fillMayAdoptLastSegment(name) == fn.Field {
			syms = append(syms, byName...)
		}
	}
	for _, sym := range syms {
		if sym == nil {
			return false
		}
		ft, ok := sym.Type.(*FuncType)
		if !ok || ft == nil || len(ft.Params) == 0 {
			return false
		}
		recv := ft.Params[0]
		if ref, isRef := recv.(*RefType); isRef && ref != nil {
			recv = ref.Elem
		}
		switch recv.(type) {
		case *StructType, *GenericInstanceType:
		default:
			return false
		}
	}
	return true
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
	if !ok || decl == nil || len(decl.Params) != len(returnBorrowCallArgs(call)) || funcHasArenaParam(decl) {
		return false
	}
	summary := a.storeFlowSummaryFor(decl)
	return summary != nil && !summary.connected(index, other)
}

// callArgNeverReaches reports that the callee is summarized and the contents of argument other
// never reach parameter index (directed: other is never stored into index).
func (a *Analyzer) callArgNeverReaches(call *ast.CallExpr, other, index int) bool {
	decl, ok := a.resolveReadOnlyScanCallee(call)
	if !ok || decl == nil || len(decl.Params) != len(returnBorrowCallArgs(call)) || funcHasArenaParam(decl) {
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
