package semantic

import (
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"elisacore/src/ast"
)

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
		if !storeFlowTypeInfoOf(rv.Type()).binder {
			return
		}
		elem := rv.Elem()
		out := &idx.other
		if fs, ok := v.(*ast.ForStmt); ok {
			idx.rangeNames = append(idx.rangeNames, fs.Name)
			out = &idx.rangeOther
		}
		rfs := reflectFields(elem.Type())
		for _, i := range storeFlowTypeInfoOf(elem.Type()).binderFields {
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
	c.linkPair(names, nil)
}

// linkPair applies one link across both rows without constructing a combined slice.
func (c *storeFlowCtx) linkPair(first, second []string) {
	var sourceBuffer [8]string
	sources := sourceBuffer[:0]
	var firstSink string
	hasSink := false
	for _, names := range [2][]string{first, second} {
		for _, n := range names {
			if c.scalars[storeFlowBaseName(n)] {
				continue // a scalar local neither holds nor aliases a region reference
			}
			if c.isPure(n) {
				if len(sources) != 0 {
					c.pu.union(sources[0], n)
				}
				sources = append(sources, n)
			} else {
				if hasSink {
					c.u.union(firstSink, n)
				} else {
					firstSink, hasSink = n, true
				}
			}
		}
	}
	if hasSink {
		for _, src := range sources {
			c.edges = append(c.edges, [2]string{src, firstSink})
		}
	}
}

// reaches reports whether the contents of src may reach sink along the one-way flows, through
// the classes they pass (a class is reached as a whole: its names exchange contents).
func (c *storeFlowCtx) reaches(anchors map[storeFlowClass][]storeFlowClass, src, sink string) bool {
	target := c.storeFlowKey(sink)
	start := c.storeFlowKey(src)
	seen := map[storeFlowClass]bool{start: true}
	work := []storeFlowClass{start}
	for len(work) > 0 {
		k := work[len(work)-1]
		work = work[:len(work)-1]
		if k == target {
			return true
		}
		for _, next := range anchors[k] {
			if !seen[next] {
				seen[next] = true
				work = append(work, next)
			}
		}
	}
	return false
}

// A settled summary asks reachability from the same parameter to many
// destinations. Compute each source's closure once after all unions are done.
type storeFlowReachMemo struct {
	ctx     *storeFlowCtx
	anchors map[storeFlowClass][]storeFlowClass
	visited map[storeFlowClass]map[storeFlowClass]bool
}

func (m *storeFlowReachMemo) reaches(src, sink string) bool {
	start, target := m.ctx.storeFlowKey(src), m.ctx.storeFlowKey(sink)
	if start == target {
		return true
	}
	if len(m.anchors[start]) == 0 {
		return false
	}
	seen, ok := m.visited[start]
	if !ok {
		seen = map[storeFlowClass]bool{start: true}
		work := []storeFlowClass{start}
		for len(work) != 0 {
			last := len(work) - 1
			key := work[last]
			work = work[:last]
			for _, next := range m.anchors[key] {
				if !seen[next] {
					seen[next] = true
					work = append(work, next)
				}
			}
		}
		if m.visited == nil {
			m.visited = map[storeFlowClass]map[storeFlowClass]bool{}
		}
		m.visited[start] = seen
	}
	return seen[target]
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
	first, _ := utf8.DecodeRuneInString(name)
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
	a.observeSpecializationInputRead(decl, "store-flow-summary-query-unresolved-provenance")
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
	if len(members) == 1 {
		m := members[0]
		selfCall := false
		for _, callee := range a.callEdgesOf(m) {
			if callee == m {
				selfCall = true
				break
			}
		}
		if !selfCall {
			// A non-recursive component has no in-component caller to
			// requeue. Preserve the in-progress assumption used by nested
			// queries, then settle the same one-pass join without work maps.
			a.storeFlowActive[m] = true
			initial := newStoreFlowSummary(len(m.Params))
			a.storeFlowInProgress[m] = initial
			next := a.computeStoreFlow(m)
			cur := a.storeFlowInProgress[m]
			delete(a.storeFlowInProgress, m)
			delete(a.storeFlowActive, m)
			var result *storeFlowSummary
			if next != nil && cur != nil {
				// A computed summary already contains the identity edges in
				// the initial bottom value. If that assumption was untouched,
				// its union is next itself and needs no new matrices.
				if cur == initial {
					result = next
				} else if storeFlowSummaryWithin(next, cur) {
					result = cur
				} else {
					result = storeFlowSummaryJoin(cur, next)
				}
			}
			a.storeFlowSummaries[m] = result
			return a.storeFlowSummaries[decl]
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
		// Most iterations only add facts, so next already is the union. Reuse
		// its matrices when it contains the previous assumption; retain the
		// full union for non-monotone intermediate results.
		if storeFlowSummaryWithin(cur, next) {
			a.storeFlowInProgress[m] = next
		} else {
			a.storeFlowInProgress[m] = storeFlowSummaryJoin(cur, next)
		}
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

// invalidateStoreFlowSummariesFrom drops the summaries of changed functions and of every
// function that reaches one through calls: a summary is a function of its own body, its
// callees' summaries and the types it reads, so every other summary still holds. Call edges of
// unchanged functions are unchanged, so the callers are found from the current edges.
func (a *Analyzer) invalidateStoreFlowSummariesFrom(changed []*ast.FuncDecl) {
	if len(a.storeFlowSummaries) == 0 {
		a.invalidateStoreFlowSummaries()
		return
	}
	callers := map[*ast.FuncDecl][]*ast.FuncDecl{}
	for d := range a.storeFlowSummaries {
		for _, c := range a.callEdgesOf(d) {
			callers[c] = append(callers[c], d)
		}
	}
	dirty := map[*ast.FuncDecl]bool{}
	work := make([]*ast.FuncDecl, 0, len(changed))
	for _, fn := range changed {
		fn = a.readDecl(fn)
		if !dirty[fn] {
			dirty[fn] = true
			work = append(work, fn)
		}
	}
	for len(work) > 0 {
		fn := work[len(work)-1]
		work = work[:len(work)-1]
		for _, c := range callers[fn] {
			if !dirty[c] {
				dirty[c] = true
				work = append(work, c)
			}
		}
	}
	for fn := range dirty {
		delete(a.storeFlowSummaries, fn)
	}
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
