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
	members := a.storeFlowComponent(decl)
	if a.parallel != nil && a.parallel.specialization != nil {
		detachStoreFlowKindsForWrite(a.storeFlowSummaries, members)
		s = a.storeFlowSummaries[decl]
	}
	a.storeFlowSolveKinds(members)
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
func storeFlowKindParamProjections(flat []any, params map[string]int, sc *storeFlowKindScopes) map[string]bool {
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
				return isParam || known[sc.key(e)]
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
		return isParam || known[storeFlowKindProjectionKey(expr, root, sc)]
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
				source = n.Source
				for _, name := range stmtBoundNames(n) {
					names = append(names, sc.bound(n, name))
				}
			case *ast.MatchStmt:
				if n == nil {
					continue
				}
				source = n.Value
				for _, name := range stmtBoundNames(n) {
					names = append(names, sc.boundAll(n, name)...)
				}
			case *ast.VarDeclStmt:
				// `name: T = p[i]` binds a part of a parameter just as a loop binder does.
				if n == nil {
					continue
				}
				if key := sc.bound(n, n.Name); n.Owner == nil && n.Value != nil && rootedProjection(n.Value, out) {
					candidate[key] = true
				} else {
					bad[key] = true
				}
				continue
			case *ast.AssignStmt:
				if n == nil {
					continue
				}
				id, isIdent := returnBorrowStripParens(n.Target).(*ast.Ident)
				if isIdent && id != nil && !n.WriteThrough && n.AsOverlayCall == nil && n.Value != nil && rootedProjection(n.Value, out) {
					candidate[sc.key(id)] = true
				} else if root := rootIdentExpr(stripAddrAndParens(n.Target)); root != nil {
					bad[sc.key(root)] = true
				}
				continue
			case ast.Stmt:
				if n != nil {
					for _, name := range stmtBoundNames(n) {
						for _, key := range sc.boundAll(n, name) {
							bad[key] = true
						}
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
		// An expression binder (a comprehension's, a match-expression arm's) reads its source.
		for key, source := range sc.sources {
			if rooted(source, out) {
				candidate[key] = true
			} else {
				bad[key] = true
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

// storeFlowKindProjectionKey: the scoped key of root, the name expr projects out of.
func storeFlowKindProjectionKey(expr ast.Expr, root string, sc *storeFlowKindScopes) string {
	if id := rootIdentExpr(stripAddrAndParens(expr)); id != nil && id.Name == root {
		return sc.key(id)
	}
	return root
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
