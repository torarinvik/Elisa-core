package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// gatherLawIsRangeRefinement narrows an immutable integer variable by a law inside the truthy branch
// of `if x is Law:` (docs/85). When the law body is a decidable conjunction of `self OP const`, its
// constraints become an integer range fact on x, so a later refinement obligation on x (another `x
// is OtherLaw`, an `x`-initialized refinement binding, or passing x to a refinement param)
// discharges statically. Handles both bare laws (`is Positive`) and parametric laws with
// compile-time-constant args (`is Bounded[0, 500]`).
func (a *Analyzer) gatherLawIsRangeRefinement(scope *Scope, n *ast.BinaryExpr, truthy bool) {
	if !truthy || scope == nil || n == nil {
		return
	}
	name, ok := immutableIntIdentName(a, scope, n.Left)
	if !ok {
		return
	}
	targets := flattenIsTargetExprs(n.Right)
	if len(targets) != 1 {
		return
	}
	lawName, lawArgs, ok := a.resolveLawIsTarget(targets[0])
	if !ok {
		return
	}
	decl, _, ok := a.lookupLaw(lawName)
	if !ok || decl == nil || len(lawArgs) != len(decl.Params)-1 {
		return
	}
	// Bind the law's static params (decl.Params[1:]) to the constant bracket args, so a body like
	// `self >= lo and self <= hi` is interpreted against the actual bounds.
	paramConsts := map[string]int64{}
	for i, arg := range lawArgs {
		c, ok := a.constIntValue(arg)
		if !ok {
			return // a non-constant arg is not statically interpretable
		}
		paramConsts[decl.Params[i+1].Name] = c
	}
	constraints, ok := a.lawConstraints(decl, paramConsts)
	if !ok || len(constraints) == 0 {
		return
	}
	fact := numRange{}
	for _, k := range constraints {
		fact = fact.intersect(constraintToRange(k))
	}
	if scope.rangeFacts == nil {
		scope.rangeFacts = map[string]numRange{}
	}
	scope.rangeFacts[name] = scope.rangeFacts[name].intersect(fact)
}

// constraintToRange converts one decidable `self OP const` law constraint into the integer range it
// implies.
func constraintToRange(k lawConstraint) numRange {
	switch k.op {
	case lexer.TOKEN_GTEQ:
		return numRange{loKnown: true, lo: k.c}
	case lexer.TOKEN_GT:
		return numRange{loKnown: true, lo: k.c + 1}
	case lexer.TOKEN_LTEQ:
		return numRange{hiKnown: true, hi: k.c}
	case lexer.TOKEN_LT:
		return numRange{hiKnown: true, hi: k.c - 1}
	case lexer.TOKEN_EQEQ:
		return numRange{loKnown: true, lo: k.c, hiKnown: true, hi: k.c}
	default:
		return numRange{}
	}
}

// immutableIntIdentName returns the name of `expr` when it is a bare identifier bound to an
// IMMUTABLE integer-typed variable (so a branch-condition fact about it cannot be invalidated by a
// later mutation). Mutable bindings return false — their facts would be unsound to carry.
func immutableIntIdentName(a *Analyzer, scope *Scope, expr ast.Expr) (string, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil {
		return "", false
	}
	sym, ok := scope.Lookup(ident.Name)
	if !ok || sym == nil || sym.Mutable {
		return "", false
	}
	if !IsNumericType(sym.Type) || IsFloatType(sym.Type) {
		return "", false
	}
	return ident.Name, true
}

// fieldPathKey returns a dotted key like "s.n" when `expr` is a field-access expression whose
// object is an immutable local variable and whose resolved integer field type is non-float. This
// lets guard facts like `if s.n <= 103:` be keyed on the path "s.n" and used inside the branch.
// The object must be a bare immutable local so the struct itself cannot be reassigned; only a
// direct mutation of `s.n` or `s` can stale the fact (invalidateRangeFactsForTarget handles both).
// Mutable-root structs are excluded conservatively: any field write would require invalidation of
// all sibling paths, so we only track paths rooted at immutable locals (sound, not complete).
func fieldPathKey(a *Analyzer, scope *Scope, expr ast.Expr) (string, bool) {
	fe, ok := expr.(*ast.FieldExpr)
	if !ok || fe == nil {
		return "", false
	}
	// Only one-level `s.field` where `s` is an immutable local.
	rootIdent, ok := fe.Object.(*ast.Ident)
	if !ok || rootIdent == nil {
		return "", false
	}
	sym, ok := scope.Lookup(rootIdent.Name)
	if !ok || sym == nil || sym.Mutable {
		return "", false
	}
	// The field's resolved type must be an integer (non-float numeric).
	ft := a.exprType(expr)
	if ft == nil || !IsNumericType(ft) || IsFloatType(ft) {
		return "", false
	}
	return rootIdent.Name + "." + fe.Field, true
}

// constIntValue extracts a compile-time integer constant from an expression.
func (a *Analyzer) constIntValue(expr ast.Expr) (int64, bool) {
	if cv, ok := a.evalConstExpr(expr); ok && cv.Kind == ConstInt {
		return cv.Int, true
	}
	// Verification-only fallback: an IMMUTABLE global (`global NAME: T = <literal>`, no `mutable`)
	// with a compile-time-constant initializer folds to its value as a PROOF constant. A non-mutable
	// global is guaranteed never-written — the type checker rejects any store to it ("cannot assign
	// to immutable global") — so its literal initializer is a sound compile-time constant. This is
	// scoped to constIntValue (the prover's constant leaf); it does NOT promote the global to a
	// general `const` (array sizes and other const-only positions are unaffected).
	if ident, isIdent := unwrapParen(expr).(*ast.Ident); isIdent {
		if v, ok := a.immutableGlobalIntValue(ident.Name); ok {
			return v, true
		}
	}
	return 0, false
}

// immutableGlobalIntValue resolves a name that binds a non-mutable global with a constant-foldable
// literal initializer to its integer value. Returns false for a `mutable` global, a non-global, a
// global with no/non-constant initializer, or an unknown name. Sound because immutability is
// enforced by the type system (writes to a non-mutable global are a compile error).
func (a *Analyzer) immutableGlobalIntValue(name string) (int64, bool) {
	if a == nil {
		return 0, false
	}
	sym, _, ok := a.lookupVisibleGlobal(name)
	if !ok || sym == nil || sym.Kind != SymbolGlobal || sym.Mutable {
		return 0, false
	}
	gd, ok := sym.Node.(*ast.GlobalDecl)
	if !ok || gd == nil || gd.Mutable || gd.Value == nil {
		return 0, false
	}
	cv, ok := a.evalConstExpr(gd.Value)
	if !ok || cv.Kind != ConstInt {
		return 0, false
	}
	return cv.Int, true
}

func flipComparison(op lexer.TokenKind) lexer.TokenKind {
	switch op {
	case lexer.TOKEN_GT:
		return lexer.TOKEN_LT
	case lexer.TOKEN_GTEQ:
		return lexer.TOKEN_LTEQ
	case lexer.TOKEN_LT:
		return lexer.TOKEN_GT
	case lexer.TOKEN_LTEQ:
		return lexer.TOKEN_GTEQ
	default:
		return op
	}
}

// negateComparison returns the LOGICAL negation of a comparison operator (`not (a < c)` is `a >= c`),
// used to narrow the falsy branch of a condition. `==`/`!=` negate to each other; neither yields a
// single contiguous integer range, so gatherNumericRangeRefinement's switch ignores them.
func negateComparison(op lexer.TokenKind) lexer.TokenKind {
	switch op {
	case lexer.TOKEN_GT:
		return lexer.TOKEN_LTEQ
	case lexer.TOKEN_GTEQ:
		return lexer.TOKEN_LT
	case lexer.TOKEN_LT:
		return lexer.TOKEN_GTEQ
	case lexer.TOKEN_LTEQ:
		return lexer.TOKEN_GT
	case lexer.TOKEN_EQEQ:
		return lexer.TOKEN_BANGEQ
	case lexer.TOKEN_BANGEQ:
		return lexer.TOKEN_EQEQ
	default:
		return op
	}
}

// lookupRangeFact walks the current scope chain for a known integer range about `name`.
// After the direct range-fact walk, it attempts one round of transitive-closure over
// relational facts (`smtAssertFacts` of the form `X OP Y` between two immutable integer
// idents) to derive tighter bounds — e.g. given live facts `a <= b` and `b <= c`, it
// can derive that `a` has an upper bound of whatever `c`'s range says, without SMT.
func (a *Analyzer) lookupRangeFact(name string) (numRange, bool) {
	acc := numRange{}
	found := false
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.rangeFacts != nil {
			// A fact of an outer scope about a name a closer binding now shadows is not about it.
			if r, ok := scope.rangeFacts[name]; ok && a.rangeFactBindingCurrent(scope, name) {
				acc = acc.intersect(r)
				found = true
			}
		}
		if scope.closedWorld { // docs/99: stop at a proof wall — outer range facts are out of scope.
			break
		}
	}
	// Transitive-closure enrichment: collect relational facts and try to derive tighter bounds
	// by chaining through intermediate immutable-integer variables. This lets `a <= b, b <= c`
	// prove `a <= c` without SMT. Sound: only adds bounds that follow from valid relational edges;
	// unknown/open sides are left open (fail-closed, sound).
	if tr, trFound := a.transitiveRangeFact(name); trFound {
		acc = acc.intersect(tr)
		found = true
	}
	return acc, found
}

// relationalEdge represents one live relational fact `left OP right` between two immutable
// integer idents (collected from smtAssertFacts for the transitive-closure walk).
type relationalEdge struct {
	left  string
	op    lexer.TokenKind // one of <=, <, >=, >
	right string
}

// collectRelationalEdges gathers all live relational ordering facts (ident OP ident, for
// immutable integer idents) from two sources:
//
//  1. smtAssertFacts in the visible scope chain (flow-local facts seeded from branch conditions,
//     proven assertions, etc.). Respects the closedWorld proof-wall.
//  2. The enclosing function's `requires` clauses that reference only immutable roots — these
//     are always-valid (entry-time) preconditions on the function's immutable parameters, exactly
//     as smtRequiresHypotheses treats them for the SMT tier. Clauses over mutable roots are
//     excluded (they are seeded as smtAssertFacts via seedRequiresAsAssertFacts and tracked
//     through standard mutation-invalidation; picking them up here too would be redundant and
//     potentially stale after a mutation of the root).
//
// Only the four ordering operators (<=, <, >=, >) are admitted; == and != are not monotone
// chains and are skipped (sound).
// relationalEdgesForLookup returns the current relational edges, reusing the batch snapshot
// while one is pinned (withPinnedRelationalEdges) instead of re-walking every scope.
func (a *Analyzer) relationalEdgesForLookup() []relationalEdge {
	if a.pinnedRelationalEdgesValid {
		return a.pinnedRelationalEdges
	}
	return a.collectRelationalEdges()
}

// withPinnedRelationalEdges runs read-only range lookups against one edge snapshot. Callers must
// not change scope facts or bindings inside run.
func (a *Analyzer) withPinnedRelationalEdges(run func()) {
	if a == nil || a.pinnedRelationalEdgesValid {
		run()
		return
	}
	a.pinnedRelationalEdges = a.collectRelationalEdges()
	a.pinnedRelationalEdgesValid = true
	defer func() {
		a.pinnedRelationalEdges = nil
		a.pinnedRelationalEdgesValid = false
	}()
	run()
}

func (a *Analyzer) collectRelationalEdges() []relationalEdge {
	if a == nil || a.currentScope == nil {
		return nil
	}
	var edges []relationalEdge

	addBinaryEdge := func(scope *Scope, expr ast.Expr) {
		bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr)
		if !ok || bin == nil {
			return
		}
		switch bin.Op {
		case lexer.TOKEN_LTEQ, lexer.TOKEN_LT, lexer.TOKEN_GTEQ, lexer.TOKEN_GT:
		default:
			return
		}
		lName, lOk := immutableIntIdentName(a, scope, bin.Left)
		rName, rOk := immutableIntIdentName(a, scope, bin.Right)
		if !lOk || !rOk {
			return
		}
		edges = append(edges, relationalEdge{left: lName, op: bin.Op, right: rName})
	}

	// Source 1: flow-local smtAssertFacts (branch conditions, etc.)
	closedWorld := false
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		for _, fact := range scope.smtAssertFacts {
			if !a.hypothesisFactBindingsCurrent(scope, assertHypothesisFact{fact: fact}) {
				continue
			}
			addBinaryEdge(scope, fact.Expr)
		}
		if scope.closedWorld {
			closedWorld = true
			break
		}
	}

	// Source 2: enclosing function's `requires` over IMMUTABLE roots (always-valid entry facts).
	// Skipped inside a closedWorld proof wall (docs/99: ambient facts are walled out).
	if !closedWorld && a.currentFuncDecl != nil {
		for _, req := range a.currentFuncDecl.Requires {
			if req == nil || a.requiresReferencesMutableRoot(req) {
				continue // mutable-root clauses live in smtAssertFacts already
			}
			if a.mentionsShadowedParam(req) {
				continue
			}
			addBinaryEdge(a.currentScope, req)
		}
	}

	return edges
}

// transitiveRangeFact attempts to derive a numRange for `name` by chaining relational edges
// (from collectRelationalEdges) through intermediate immutable-integer variables. The walk is
// a BFS that propagates upper-bound and lower-bound information separately:
//
//   - Upper bound: an edge `name <= b` means name <= b's hi; if b has an upper bound via
//     further edges, that propagates. Strict `<` contributes -1 (integers are discrete).
//   - Lower bound: symmetric via >= / >.
//
// The BFS is bounded to maxTransitiveDepth hops to stay O(small) and avoid cycles.
// Only bounds that reach a variable with a known direct range (or written-const) are
// admitted — a chain that terminates in another un-anchored variable stays open (fail-closed).
func (a *Analyzer) transitiveRangeFact(name string) (numRange, bool) {
	edges := a.relationalEdgesForLookup()
	if len(edges) == 0 {
		return numRange{}, false
	}
	const maxTransitiveDepth = 8

	// directRange is the direct (non-transitive) range for a variable: rangeFacts only, no
	// recursion, to avoid infinite recursion from inside lookupRangeFact.
	directRange := func(n string) (numRange, bool) {
		acc := numRange{}
		found := false
		for scope := a.currentScope; scope != nil; scope = scope.Parent {
			if scope.rangeFacts != nil {
				if r, ok := scope.rangeFacts[n]; ok && a.rangeFactBindingCurrent(scope, n) {
					acc = acc.intersect(r)
					found = true
				}
			}
			if scope.closedWorld {
				break
			}
		}
		if !found {
			if c, known := a.writtenConstInt(n); known {
				return numRange{loKnown: true, lo: c, hiKnown: true, hi: c}, true
			}
		}
		return acc, found
	}

	result := numRange{}
	improved := false

	// BFS for upper-bound derivation: follow edges that carry upper-bound constraints on name.
	{
		type bfsState struct {
			node  string
			adj   int64 // cumulative offset: result_hi = peer_hi + adj
			depth int
		}
		visited := map[string]bool{name: true}
		queue := []bfsState{{node: name}}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if cur.depth >= maxTransitiveDepth {
				continue
			}
			for _, e := range edges {
				// An edge that gives an UPPER bound on cur.node:
				//   cur.node <= e.right  (LTEQ)  →  peer=e.right, edgeAdj=0
				//   cur.node <  e.right  (LT)    →  peer=e.right, edgeAdj=-1 (a<b ⟹ a<=b-1)
				//   e.right >= cur.node  (GTEQ)  →  peer=e.right, edgeAdj=0
				//   e.right >  cur.node  (GT)    →  peer=e.right, edgeAdj=-1
				var peer string
				var edgeAdj int64
				switch e.op {
				case lexer.TOKEN_LTEQ:
					if e.left == cur.node {
						peer, edgeAdj = e.right, 0
					}
				case lexer.TOKEN_LT:
					if e.left == cur.node {
						peer, edgeAdj = e.right, -1
					}
				case lexer.TOKEN_GTEQ:
					if e.right == cur.node {
						peer, edgeAdj = e.left, 0
					}
				case lexer.TOKEN_GT:
					if e.right == cur.node {
						peer, edgeAdj = e.left, -1
					}
				}
				if peer == "" || visited[peer] {
					continue
				}
				totalAdj := cur.adj + edgeAdj
				if pr, ok := directRange(peer); ok && pr.hiKnown {
					derivedHi := pr.hi + totalAdj
					if !result.hiKnown || derivedHi < result.hi {
						result.hi = derivedHi
						result.hiKnown = true
						improved = true
					}
				}
				visited[peer] = true
				queue = append(queue, bfsState{node: peer, adj: totalAdj, depth: cur.depth + 1})
			}
		}
	}

	// BFS for lower-bound derivation: symmetric.
	{
		type bfsState struct {
			node  string
			adj   int64 // cumulative offset: result_lo = peer_lo + adj
			depth int
		}
		visited := map[string]bool{name: true}
		queue := []bfsState{{node: name}}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if cur.depth >= maxTransitiveDepth {
				continue
			}
			for _, e := range edges {
				// An edge that gives a LOWER bound on cur.node:
				//   cur.node >= e.right  (GTEQ)  →  peer=e.right, edgeAdj=0
				//   cur.node >  e.right  (GT)    →  peer=e.right, edgeAdj=+1 (a>b ⟹ a>=b+1)
				//   e.right <= cur.node  (LTEQ)  →  peer=e.right, edgeAdj=0
				//   e.right <  cur.node  (LT)    →  peer=e.right, edgeAdj=+1
				var peer string
				var edgeAdj int64
				switch e.op {
				case lexer.TOKEN_GTEQ:
					if e.left == cur.node {
						peer, edgeAdj = e.right, 0
					}
				case lexer.TOKEN_GT:
					if e.left == cur.node {
						peer, edgeAdj = e.right, 1
					}
				case lexer.TOKEN_LTEQ:
					if e.right == cur.node {
						peer, edgeAdj = e.left, 0
					}
				case lexer.TOKEN_LT:
					if e.right == cur.node {
						peer, edgeAdj = e.left, 1
					}
				}
				if peer == "" || visited[peer] {
					continue
				}
				totalAdj := cur.adj + edgeAdj
				if pr, ok := directRange(peer); ok && pr.loKnown {
					derivedLo := pr.lo + totalAdj
					if !result.loKnown || derivedLo > result.lo {
						result.lo = derivedLo
						result.loKnown = true
						improved = true
					}
				}
				visited[peer] = true
				queue = append(queue, bfsState{node: peer, adj: totalAdj, depth: cur.depth + 1})
			}
		}
	}

	return result, improved
}

func (a *Analyzer) visibleRangeFacts() map[string]numRange {
	out := map[string]numRange{}
	hasFacts := false
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if len(scope.rangeFacts) != 0 {
			hasFacts = true
			break
		}
		if scope.closedWorld {
			break
		}
	}
	// No scope facts or bindings change during these lookups, so collect the
	// relational graph once instead of walking it again for every name.
	if hasFacts {
		a.withPinnedRelationalEdges(func() {
			for scope := a.currentScope; scope != nil; scope = scope.Parent {
				for name := range scope.rangeFacts {
					if _, recorded := out[name]; recorded {
						continue
					}
					if r, ok := a.lookupRangeFact(name); ok {
						out[name] = r
					}
				}
				if scope.closedWorld {
					break
				}
			}
		})
	}
	return out
}
