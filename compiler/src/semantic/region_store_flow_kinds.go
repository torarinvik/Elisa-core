package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"os"
)

// storeFlowEdgeKind classifies WHAT of a parameter a store-flow summary edge may carry. It is a
// plain ordered enum (serializable as a byte, e.g. into .elisair region signatures) and may be
// extended with finer kinds between the existing values.
type storeFlowEdgeKind uint8

const (
	// storeFlowEdgeNone: nothing of the parameter flows.
	storeFlowEdgeNone storeFlowEdgeKind = 0
	// storeFlowEdgeElements: only element VALUES copied out of the parameter flow (a fresh
	// container filled with `x[i]` / comprehension values whose type shares no storage: views,
	// scalars). The parameter's own container storage never flows; what the copied views point
	// to still may.
	storeFlowEdgeElements storeFlowEdgeKind = 1
	// storeFlowEdgeHeader: the parameter's storage itself may flow (the default whenever the
	// analysis cannot prove less).
	storeFlowEdgeHeader storeFlowEdgeKind = 2
)

func (k storeFlowEdgeKind) String() string {
	switch k {
	case storeFlowEdgeNone:
		return "none"
	case storeFlowEdgeElements:
		return "elements"
	}
	return "header"
}

func storeFlowKindJoin(x, y storeFlowEdgeKind) storeFlowEdgeKind {
	if x > y {
		return x
	}
	return y
}

// storeFlowKindCompose: what of the caller's source reaches through a callee edge of kind edge
// applied to an argument carrying arg of it -- element values of anything are element values.
func storeFlowKindCompose(edge, arg storeFlowEdgeKind) storeFlowEdgeKind {
	if edge < arg {
		return edge
	}
	return arg
}

// storeFlowRetKind: the kind of the result edge from parameter i (Header when unknown).
func (s *storeFlowSummary) retKindOf(i int) storeFlowEdgeKind {
	if s == nil || i < 0 || i >= len(s.retConn) {
		return storeFlowEdgeHeader
	}
	if !s.retConn[i] {
		return storeFlowEdgeNone
	}
	if i >= len(s.retKind) {
		return storeFlowEdgeHeader
	}
	return s.retKind[i]
}

// reachKindOf: the kind of the edge carrying parameter i's contents into parameter j.
func (s *storeFlowSummary) reachKindOf(i, j int) storeFlowEdgeKind {
	if s == nil || i < 0 || j < 0 || i >= len(s.reach) || j >= len(s.reach) {
		return storeFlowEdgeHeader
	}
	if !s.reach[i][j] {
		return storeFlowEdgeNone
	}
	if i >= len(s.reachKind) || j >= len(s.reachKind[i]) {
		return storeFlowEdgeHeader
	}
	return s.reachKind[i][j]
}

// storeFlowLowerKinds sets every connected edge of s to kind.
func storeFlowLowerKinds(s *storeFlowSummary, kind storeFlowEdgeKind) {
	for i := range s.retKind {
		if s.retConn[i] {
			s.retKind[i] = kind
		}
		for j := range s.reachKind[i] {
			if s.reach[i][j] {
				s.reachKind[i][j] = kind
			}
		}
	}
}

// storeFlowInitKinds sets every connected edge of a settled summary to Header.
func storeFlowInitKinds(s *storeFlowSummary) {
	if s == nil {
		return
	}
	n := len(s.retConn)
	s.retKind = make([]storeFlowEdgeKind, n)
	s.reachKind = make([][]storeFlowEdgeKind, n)
	for i := 0; i < n; i++ {
		if s.retConn[i] {
			s.retKind[i] = storeFlowEdgeHeader
		}
		s.reachKind[i] = make([]storeFlowEdgeKind, n)
		for j := 0; j < n; j++ {
			if s.reach[i][j] {
				s.reachKind[i][j] = storeFlowEdgeHeader
			}
		}
	}
}

// storeFlowSolveKinds lowers the edge kinds of a settled component. Every member starts at
// Header on each connected edge; each round recomputes a member's kinds from the current
// kinds of its callees. The transfer is monotone, so the sequence only descends from the safe
// top and every intermediate state over-approximates the flows (a round cut short by the budget
// still leaves a sound answer). Edges the summary does not connect stay None.
// storeFlowKindsFor: decl's settled summary with its edge kinds solved (on first demand, for
// its whole component), or nil.
func (a *Analyzer) storeFlowKindsFor(decl *ast.FuncDecl) *storeFlowSummary {
	decl = a.readDecl(decl)
	s := a.storeFlowSummaryFor(decl)
	if s == nil || a.storeFlowSummaries[decl] != s || s.retKind != nil {
		return s
	}
	a.storeFlowSolveKinds(a.storeFlowComponent(decl))
	return s
}

func (a *Analyzer) storeFlowSolveKinds(members []*ast.FuncDecl) {
	// Ascend from None to the least fixpoint: a descending solve from Header keeps every edge a
	// recursive call re-asserts about itself (`walk(..., xs, ...)` inside walk), which the least
	// fixpoint drops. The transfer is monotone in the callees' kinds and each edge rises at most
	// twice, so the worklist converges; should it exceed its budget the whole component falls
	// back to the safe Header. Every member is initialized first, so a member's query during
	// the solve reads the current assumption instead of starting a nested solve.
	budget := 1
	for _, m := range members {
		if s := a.storeFlowSummaries[m]; s != nil && s.retKind == nil {
			storeFlowInitKinds(s)
			storeFlowLowerKinds(s, storeFlowEdgeNone)
			n := len(s.retConn)
			budget += 2*(n*n+n) + 1
		}
	}
	inSCC := map[*ast.FuncDecl]bool{}
	for _, m := range members {
		inSCC[m] = true
	}
	callers := map[*ast.FuncDecl][]*ast.FuncDecl{}
	for _, m := range members {
		for _, c := range a.callEdgesOf(m) {
			if inSCC[c] && c != m {
				callers[c] = append(callers[c], m)
			}
		}
	}
	queued := map[*ast.FuncDecl]bool{}
	work := append([]*ast.FuncDecl(nil), members...)
	for _, m := range members {
		queued[m] = true
	}
	for len(work) > 0 {
		m := work[0]
		work = work[1:]
		queued[m] = false
		s := a.storeFlowSummaries[m]
		if s == nil {
			continue
		}
		ret, reach := a.computeStoreFlowKinds(m, s)
		changed := false
		for i := range ret {
			// Never above what the summary connects (Init/Lower keep unconnected edges None and
			// the meet with Header caps the rest), never falling.
			if s.retConn[i] {
				if k := storeFlowKindJoin(ret[i], s.retKind[i]); k != s.retKind[i] {
					s.retKind[i] = k
					changed = true
				}
			}
			for j := range reach[i] {
				if s.reach[i][j] {
					if k := storeFlowKindJoin(reach[i][j], s.reachKind[i][j]); k != s.reachKind[i][j] {
						s.reachKind[i][j] = k
						changed = true
					}
				}
			}
		}
		if !changed {
			continue
		}
		budget--
		if budget < 0 {
			for _, member := range members {
				if s := a.storeFlowSummaries[member]; s != nil {
					storeFlowInitKinds(s)
				}
			}
			work = nil
			break
		}
		if !queued[m] {
			// A self-recursive member reads its own kinds.
			queued[m] = true
			work = append(work, m)
		}
		for _, c := range callers[m] {
			if !queued[c] {
				queued[c] = true
				work = append(work, c)
			}
		}
	}
	if storeScanTrace {
		for _, m := range members {
			if s := a.storeFlowSummaries[m]; s != nil {
				fmt.Fprintf(os.Stderr, "FLOWKIND %s ret=%v\n", m.Name, s.retKind)
			}
		}
	}
}

// storeFlowKindParamProjections: the loop and match binders that only ever name a part of a
// parameter -- every binding of the name iterates or matches a parameter, or a projection of one
// (or of another such binder) -- and that no declaration, assignment or other binder reuses.
func storeFlowKindParamProjections(flat []any, params map[string]int) map[string]bool {
	rooted := func(expr ast.Expr, known map[string]bool) bool {
		for {
			switch e := expr.(type) {
			case *ast.ParenExpr:
				if e == nil {
					return false
				}
				expr = e.Inner
				continue
			case *ast.FieldExpr:
				if e == nil {
					return false
				}
				expr = e.Object
				continue
			case *ast.IndexExpr:
				if e == nil {
					return false
				}
				expr = e.Object
				continue
			case *ast.Ident:
				if e == nil {
					return false
				}
				_, isParam := params[e.Name]
				return isParam || known[e.Name]
			}
			return false
		}
	}
	// A declaration's value must read a part (a projection), never the name itself: `x = p`
	// would make x the parameter's own storage.
	rootedProjection := func(expr ast.Expr, known map[string]bool) bool {
		root, ok := storeFlowKindProjectionRoot(expr)
		if !ok {
			return false
		}
		_, isParam := params[root]
		return isParam || known[root]
	}
	out := map[string]bool{}
	for changed := true; changed; {
		changed = false
		candidate := map[string]bool{}
		bad := map[string]bool{}
		for _, node := range flat {
			var source ast.Expr
			var names []string
			switch n := node.(type) {
			case *ast.IterForStmt:
				if n == nil {
					continue
				}
				source, names = n.Source, stmtBoundNames(n)
			case *ast.MatchStmt:
				if n == nil {
					continue
				}
				source, names = n.Value, stmtBoundNames(n)
			case *ast.VarDeclStmt:
				// `name: T = p[i]` binds a part of a parameter just as a loop binder does.
				if n == nil {
					continue
				}
				if n.Owner == nil && n.Value != nil && rootedProjection(n.Value, out) {
					candidate[n.Name] = true
				} else {
					bad[n.Name] = true
				}
				continue
			case *ast.AssignStmt:
				if n == nil {
					continue
				}
				id, isIdent := returnBorrowStripParens(n.Target).(*ast.Ident)
				if isIdent && id != nil && !n.WriteThrough && n.AsOverlayCall == nil && n.Value != nil && rootedProjection(n.Value, out) {
					candidate[id.Name] = true
				} else if root := rootIdentExpr(stripAddrAndParens(n.Target)); root != nil {
					bad[root.Name] = true
				}
				continue
			case ast.Stmt:
				if n != nil {
					for _, name := range stmtBoundNames(n) {
						bad[name] = true
					}
				}
				continue
			default:
				continue
			}
			ok := rooted(source, out)
			for _, name := range names {
				if ok {
					candidate[name] = true
				} else {
					bad[name] = true
				}
			}
		}
		for name := range candidate {
			if _, isParam := params[name]; !bad[name] && !isParam && !out[name] {
				out[name] = true
				changed = true
			}
		}
	}
	return out
}

// storeFlowKindProjectionRoot: expr reads a value stored inside a named place -- one or more
// field / index projections of a name, with no call, address-of or move that could produce a
// borrow instead of a copy -- and that name. A bare name is not a projection: a view local may
// itself be a borrow of the storage it was taken from.
func storeFlowKindProjectionRoot(expr ast.Expr) (string, bool) {
	projected := false
	for {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			if e == nil {
				return "", false
			}
			expr = e.Inner
			continue
		case *ast.FieldExpr:
			if e == nil {
				return "", false
			}
			expr, projected = e.Object, true
			continue
		case *ast.IndexExpr:
			if e == nil {
				return "", false
			}
			expr, projected = e.Object, true
			continue
		case *ast.Ident:
			return e.Name, e != nil && projected
		}
		return "", false
	}
}

func storeFlowKindMeet(x, y storeFlowEdgeKind) storeFlowEdgeKind {
	if x < y {
		return x
	}
	return y
}

// storeFlowKindValue is what an expression may evaluate to: the storage of any name in hdr
// (or a part of it), plus element values copied out of the parameters in el.
type storeFlowKindValue struct {
	hdr map[string]bool
	el  map[int]bool
}

// computeStoreFlowKinds: one round of the kind analysis of decl under its callees' current
// kinds. Names that may share storage are unioned into classes (a local bound to, or holding, a
// part of another name's storage joins its class: a store into either is a store into both).
// Each class also records the parameters whose copied ELEMENT values it may hold. It is
// flow-insensitive and iterated to a local fixpoint over every node.
func (a *Analyzer) computeStoreFlowKinds(decl *ast.FuncDecl, s *storeFlowSummary) ([]storeFlowEdgeKind, [][]storeFlowEdgeKind) {
	decl = a.readDecl(decl)
	n := len(decl.Params)
	top := func() ([]storeFlowEdgeKind, [][]storeFlowEdgeKind) {
		ret := make([]storeFlowEdgeKind, n)
		reach := make([][]storeFlowEdgeKind, n)
		for i := range ret {
			ret[i] = storeFlowEdgeHeader
			reach[i] = make([]storeFlowEdgeKind, n)
			for j := range reach[i] {
				reach[i][j] = storeFlowEdgeHeader
			}
		}
		return ret, reach
	}
	// Shapes the local analysis does not model keep every edge at Header.
	if len(decl.LmutThreadSlots) != 0 || (decl.ReturnType != nil && !a.declReturnsVoid(decl) && !storeFlowEndsInReturn(decl.Body)) {
		return top()
	}
	idx := a.storeScanIndex(decl)
	for _, node := range idx.flat {
		switch node.(type) {
		case *ast.LambdaExpr, *ast.DeferStmt, *ast.AdoptStmt, *ast.PromoteStmt, *ast.ParallelForStmt, *ast.PoolStmt, *ast.LockStmt:
			return top()
		}
	}
	savedFlowDecl := a.storeFlowCurrentDecl
	a.storeFlowCurrentDecl = decl
	defer func() { a.storeFlowCurrentDecl = savedFlowDecl }()
	// Resolve the body's calls in decl's own module (as computeStoreFlow does).
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

	scalars := a.storeScanScalarLocals(decl)
	const retName = "\x00ret"
	known := map[string]bool{retName: true}
	for _, p := range decl.Params {
		known[p.Name] = true
	}
	for name := range idx.locals {
		known[name] = true
	}
	parent := map[string]string{}
	var find func(x string) string
	find = func(x string) string {
		p, ok := parent[x]
		if !ok || p == x {
			return x
		}
		r := find(p)
		parent[x] = r
		return r
	}
	elem := map[string]map[int]bool{} // class root -> parameters whose copied elements it holds
	changed := false
	union := func(x, y string) {
		rx, ry := find(x), find(y)
		if rx == ry {
			return
		}
		parent[rx] = ry
		if e := elem[rx]; len(e) > 0 {
			if elem[ry] == nil {
				elem[ry] = map[int]bool{}
			}
			for i := range e {
				elem[ry][i] = true
			}
		}
		delete(elem, rx)
		changed = true
	}
	paramIndex := map[string]int{}
	for i, p := range decl.Params {
		paramIndex[p.Name] = i
	}
	empty := func() storeFlowKindValue { return storeFlowKindValue{} }
	merge := func(dst *storeFlowKindValue, src storeFlowKindValue) {
		for h := range src.hdr {
			if dst.hdr == nil {
				dst.hdr = map[string]bool{}
			}
			dst.hdr[h] = true
		}
		for i := range src.el {
			if dst.el == nil {
				dst.el = map[int]bool{}
			}
			dst.el[i] = true
		}
	}
	// lower: what reading element / field values whose type shares no storage out of v yields.
	lower := func(v storeFlowKindValue, to storeFlowEdgeKind) storeFlowKindValue {
		if to == storeFlowEdgeHeader {
			return v
		}
		out := storeFlowKindValue{el: map[int]bool{}}
		for i := range v.el {
			out.el[i] = true
		}
		for h := range v.hdr {
			root := find(h)
			for i, p := range decl.Params {
				if find(p.Name) == root {
					out.el[i] = true
				}
			}
			for i := range elem[root] {
				out.el[i] = true
			}
		}
		return out
	}
	put := func(name string, v storeFlowKindValue) {
		if name == "" || scalars[name] || !known[name] {
			return
		}
		for h := range v.hdr {
			union(name, h)
		}
		if len(v.el) > 0 {
			root := find(name)
			if elem[root] == nil {
				elem[root] = map[int]bool{}
			}
			for i := range v.el {
				if !elem[root][i] {
					elem[root][i] = true
					changed = true
				}
			}
		}
	}
	headerOf := func(tokens []string) storeFlowKindValue {
		out := empty()
		for _, t := range tokens {
			if known[t] && !scalars[t] && t != retName {
				if out.hdr == nil {
					out.hdr = map[string]bool{}
				}
				out.hdr[t] = true
			}
		}
		return out
	}
	mentioned := func(expr ast.Expr) storeFlowKindValue {
		if expr == nil {
			return empty()
		}
		return headerOf(storeScanMentionTokens(expr))
	}
	// elemKind: the kind of an element / field value read out of a container: Elements when its
	// static type can share no storage (views, scalars), else Header.
	elemKind := func(expr ast.Expr, container ast.Expr) storeFlowEdgeKind {
		t, ok := a.storeScanStaticType(decl, storeScanUnwrap(expr))
		if id, isIdent := storeScanUnwrap(expr).(*ast.Ident); (!ok || t == nil) && isIdent && id != nil {
			// A binder in a body not type-checked yet: its declared pattern or loop type.
			if t, ok = a.storeScanPatternBinderType(decl, id.Name); !ok || t == nil {
				t, ok = a.storeScanLoopBinderElemType(decl, id)
			}
		}
		if id, isIdent := storeScanUnwrap(expr).(*ast.Ident); (!ok || t == nil) && isIdent && id != nil && a.storeScanPatternBindersShareNot(decl, id.Name) {
			return storeFlowEdgeElements
		}
		if ok && t != nil && !containsTypeParam(t) {
			if a.storeScanSharesCached(t) {
				return storeFlowEdgeHeader
			}
			return storeFlowEdgeElements
		}
		if container != nil {
			if _, isIdent := storeScanUnwrap(container).(*ast.Ident); isIdent && !a.storeScanContainerElemsMayShare(decl, container) {
				return storeFlowEdgeElements
			}
		}
		return storeFlowEdgeHeader
	}
	// through: v passed through a callee edge of the given kind.
	through := func(v storeFlowKindValue, kind storeFlowEdgeKind) storeFlowKindValue {
		switch kind {
		case storeFlowEdgeNone:
			return empty()
		case storeFlowEdgeElements:
			return lower(v, storeFlowEdgeElements)
		}
		return v
	}
	calleeOf := func(call *ast.CallExpr) (*ast.FuncDecl, *storeFlowSummary) {
		callee, ok := a.resolveReadOnlyScanCallee(call)
		if !ok || callee == nil || !storeFlowCallFitsParams(callee, call) || funcHasArenaParam(callee) {
			return nil, nil
		}
		sum := a.storeFlowKindsFor(callee)
		if sum == nil || len(sum.retKind) != len(callee.Params) {
			return nil, nil
		}
		return callee, sum
	}
	// memo: per-round results (the state only grows within a round; a round that changes
	// nothing recomputes everything against the final state, so the fixpoint reads nothing stale).
	memo := map[ast.Expr]storeFlowKindValue{}
	var evalRaw func(expr ast.Expr) storeFlowKindValue
	eval := func(expr ast.Expr) storeFlowKindValue {
		if v, ok := memo[expr]; ok {
			return v
		}
		v := evalRaw(expr)
		memo[expr] = v
		return v
	}
	evalRaw = func(expr ast.Expr) storeFlowKindValue {
		out := empty()
		inner := storeScanUnwrap(expr)
		if inner == nil {
			return out
		}
		if t, ok := a.storeScanStaticType(decl, inner); ok && t != nil && !containsTypeParam(t) && a.storeScanHoldsNoRegionStorage(t) {
			return out
		}
		switch e := inner.(type) {
		case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.CharLit, *ast.StringLit:
			return out
		case *ast.Ident:
			if e == nil || scalars[e.Name] || !known[e.Name] {
				return out
			}
			return storeFlowKindValue{hdr: map[string]bool{e.Name: true}}
		case *ast.IndexExpr:
			if e == nil || e.Index2 != nil {
				return mentioned(inner)
			}
			return lower(eval(e.Object), elemKind(e, e.Object))
		case *ast.FieldExpr:
			if e == nil {
				return out
			}
			// A field of a container is part of its storage unless its value shares none.
			return lower(eval(e.Object), elemKind(e, nil))
		case *ast.StructLitExpr:
			if e == nil || e.CopyBase != nil {
				return mentioned(inner)
			}
			for _, arg := range e.Args {
				merge(&out, eval(arg))
			}
			for _, spread := range e.Spreads {
				merge(&out, mentioned(spread))
			}
			return out
		case *ast.TernaryExpr:
			// `v if c else w`: either branch (the condition is only tested).
			if e == nil {
				return out
			}
			merge(&out, eval(e.Value))
			merge(&out, eval(e.Alt))
			return out
		case *ast.TupleExpr:
			for _, elem := range e.Elems {
				merge(&out, eval(elem))
			}
			return out
		case *ast.ListLitExpr:
			if e == nil || len(e.Keys) != 0 {
				return mentioned(inner)
			}
			for i, item := range e.Elems {
				if i < len(e.Spreads) && e.Spreads[i] {
					merge(&out, mentioned(item))
					continue
				}
				merge(&out, eval(item))
			}
			return out
		case *ast.ListComprehensionExpr:
			if e == nil {
				return out
			}
			// Each produced element is the value expression over a binder; only a value whose
			// type shares no storage is a copied element.
			return lower(mentioned(inner), elemKind(e.Value, nil))
		case *ast.CallExpr:
			if e == nil {
				return out
			}
			if field, isMethod := e.Func.(*ast.FieldExpr); isMethod && field != nil && len(returnBorrowCallArgs(e)) == len(e.Args) {
				if a.storeScanReceiverIsBuiltinDArray(decl, field.Object) && containerGrowthMethods[field.Field] {
					merge(&out, eval(field.Object))
					for _, arg := range e.Args {
						if field.Field == "extend" {
							merge(&out, lower(eval(arg), elemKind(&ast.IndexExpr{Object: arg}, arg)))
						} else {
							merge(&out, eval(arg))
						}
					}
					return out
				}
				return mentioned(inner)
			}
			if _, sum := calleeOf(e); sum != nil {
				for i, arg := range returnBorrowCallArgs(e) {
					merge(&out, through(eval(arg), sum.retKindOf(i)))
				}
				return out
			}
			return mentioned(inner)
		}
		return mentioned(inner)
	}
	// copied: the value of expr where it is stored. Reading a place (a name, field or index
	// chain, no call) whose type shares no storage copies a value out: the destination then
	// holds an element value of the place's class (one way), not a part of its storage. Any
	// other expression may produce a borrow (`s.view()`) and keeps its header.
	paramProjections := storeFlowKindParamProjections(idx.flat, paramIndex)
	var copied func(expr ast.Expr) storeFlowKindValue
	copied = func(expr ast.Expr) storeFlowKindValue {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			if e != nil {
				return copied(e.Inner)
			}
		case *ast.TernaryExpr:
			// `v if c else w`: either branch (the condition is only tested).
			if e != nil && e.Alt != nil {
				out := copied(e.Value)
				merge(&out, copied(e.Alt))
				return out
			}
		case *ast.MatchExpr:
			// Each arm yields its tail value; an arm of any other shape keeps the whole match.
			if e == nil || len(e.Arms) == 0 {
				break
			}
			out := empty()
			for _, arm := range e.Arms {
				if len(arm.Body) == 0 {
					return eval(expr)
				}
				switch tail := arm.Body[len(arm.Body)-1].(type) {
				case *ast.ExprStmt:
					if tail == nil {
						return eval(expr)
					}
					merge(&out, copied(tail.Expr))
				case *ast.ReturnStmt:
					// Leaves the function; the return statement is applied on its own.
				default:
					return eval(expr)
				}
			}
			return out
		}
		if root, ok := storeFlowKindProjectionRoot(expr); ok {
			if _, isParam := paramIndex[root]; isParam || paramProjections[root] {
				return lower(eval(expr), elemKind(expr, nil))
			}
		}
		if id, ok := expr.(*ast.Ident); ok && id != nil && paramProjections[id.Name] {
			return lower(eval(expr), elemKind(expr, nil))
		}
		return eval(expr)
	}
	// callWrites applies what a call may store into its arguments.
	callWrites := func(call *ast.CallExpr) {
		args := returnBorrowCallArgs(call)
		if field, isMethod := call.Func.(*ast.FieldExpr); isMethod && field != nil && len(args) == len(call.Args) {
			root := rootIdentExpr(stripAddrAndParens(field.Object))
			if storeScanReadOnlyMethods[field.Field] {
				return
			}
			if a.storeScanReceiverIsBuiltinDArray(decl, field.Object) && containerGrowthMethods[field.Field] {
				if root == nil {
					return
				}
				for _, arg := range call.Args {
					if field.Field == "extend" {
						put(root.Name, lower(eval(arg), elemKind(&ast.IndexExpr{Object: arg}, arg)))
					} else {
						put(root.Name, copied(arg))
					}
				}
				return
			}
		}
		if callee, sum := calleeOf(call); sum != nil {
			for m, target := range args {
				root := rootIdentExpr(stripAddrAndParens(target))
				if root == nil || !a.declParamIsWritableRef(callee, m) {
					continue
				}
				for k, arg := range args {
					if k == m {
						continue
					}
					if kind := sum.reachKindOf(k, m); kind != storeFlowEdgeNone {
						put(root.Name, through(eval(arg), kind))
					}
				}
			}
			return
		}
		// Unknown: any argument (or receiver) may receive everything the others hold.
		all := empty()
		var roots []string
		visit := func(e ast.Expr) {
			merge(&all, mentioned(e))
			if root := rootIdentExpr(stripAddrAndParens(e)); root != nil {
				roots = append(roots, root.Name)
			}
		}
		if field, isMethod := call.Func.(*ast.FieldExpr); isMethod && field != nil {
			visit(field.Object)
		}
		for _, arg := range call.Args {
			visit(arg)
		}
		for _, r := range roots {
			put(r, all)
		}
	}
	for rounds := 0; rounds < 64; rounds++ {
		changed = false
		memo = map[ast.Expr]storeFlowKindValue{}
		for _, node := range idx.flat {
			switch nd := node.(type) {
			case *ast.VarDeclStmt:
				if nd != nil && nd.Value != nil {
					put(nd.Name, copied(nd.Value))
				}
			case *ast.AssignStmt:
				if nd == nil {
					continue
				}
				if root := rootIdentExpr(stripAddrAndParens(nd.Target)); root != nil {
					put(root.Name, copied(nd.Value))
				}
			case *ast.AugAssignStmt:
				// `xs += ys` may store the value into the target.
				if nd == nil {
					continue
				}
				if root := rootIdentExpr(stripAddrAndParens(nd.Target)); root != nil {
					put(root.Name, eval(nd.Value))
				}
				if nd.CollectionAppend != nil {
					callWrites(nd.CollectionAppend)
				}
			case *ast.AsRefAssignStmt:
				if nd == nil {
					continue
				}
				if root := rootIdentExpr(stripAddrAndParens(nd.Target)); root != nil {
					put(root.Name, eval(nd.Value))
				}
			case *ast.ReturnStmt:
				if nd != nil && nd.Value != nil {
					put(retName, copied(nd.Value))
				}
			case *ast.CallExpr:
				if nd != nil {
					callWrites(nd)
				}
			case *ast.TupleBindStmt:
				// `a, b = f(..)`: each name may hold anything the value holds. An arg-manifest
				// `a, b <- f(a, b)` binds nothing; the call's writes are applied with the call.
				if nd == nil || nd.ArgManifest {
					continue
				}
				value := eval(nd.Value)
				for _, name := range nd.Names {
					put(name.Name, value)
				}
			case *ast.ForStmt:
				// an integer binder
			case *ast.IterForStmt:
				if nd == nil {
					continue
				}
				bound := lower(eval(nd.Source), elemKind(&ast.IndexExpr{Object: nd.Source}, nd.Source))
				for _, name := range idx.boundNames(nd) {
					put(name, bound)
				}
			case ast.Stmt:
				if nd == nil {
					continue
				}
				names := idx.boundNames(nd)
				if len(names) == 0 {
					continue
				}
				// Any other binder (match / is patterns, catch, ...): the whole header, at Header
				// -- except a binder that only ever names a part of a parameter, at a type that
				// shares no storage: it holds a copied element value of the header.
				all := headerOf(storeScanHeaderTokens(nd))
				for _, name := range names {
					if paramProjections[name] && elemKind(&ast.Ident{Name: name}, nil) == storeFlowEdgeElements {
						put(name, lower(all, storeFlowEdgeElements))
						continue
					}
					put(name, all)
				}
			}
		}
		if !changed {
			outRet := make([]storeFlowEdgeKind, n)
			outReach := make([][]storeFlowEdgeKind, n)
			retRoot := find(retName)
			for i, pi := range decl.Params {
				ri := find(pi.Name)
				switch {
				case ri == retRoot:
					outRet[i] = storeFlowEdgeHeader
				case elem[retRoot][i]:
					outRet[i] = storeFlowEdgeElements
				}
				outReach[i] = make([]storeFlowEdgeKind, n)
				for j, pj := range decl.Params {
					rj := find(pj.Name)
					switch {
					case i == j || ri == rj:
						outReach[i][j] = storeFlowEdgeHeader
					case elem[rj][i]:
						outReach[i][j] = storeFlowEdgeElements
					}
				}
			}
			return outRet, outReach
		}
	}
	return top()
}

// storeFlowRetKind / storeFlowReachKind read an edge kind for a consumer. storeFlowKindsOff
// (tests only) reads every edge as Header: the monotonicity check compares verdicts with and
// without the element refinement.
func (a *Analyzer) storeFlowRetKind(sum *storeFlowSummary, i int) storeFlowEdgeKind {
	if a.storeFlowKindsOff {
		return storeFlowEdgeHeader
	}
	return sum.retKindOf(i)
}

func (a *Analyzer) storeFlowReachKind(sum *storeFlowSummary, other, index int) storeFlowEdgeKind {
	if a.storeFlowKindsOff {
		return storeFlowEdgeHeader
	}
	return sum.reachKindOf(other, index)
}
