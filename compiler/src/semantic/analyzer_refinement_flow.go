package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// numRange is a closed integer interval fact: lo <= value <= hi, with either bound optionally
// unknown (open). It is the flow-prover's abstraction of what a branch condition tells us about an
// immutable integer variable (docs/85 1d-2).
type numRange struct {
	loKnown bool
	lo      int64
	hiKnown bool
	hi      int64
}

func cloneNumRangeMap(in map[string]numRange) map[string]numRange {
	if in == nil {
		return nil
	}
	out := make(map[string]numRange, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneNumRangeMapByIndex(in map[int]numRange) map[int]numRange {
	if in == nil {
		return nil
	}
	out := make(map[int]numRange, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneScopeRangeFacts(scope *Scope) map[*Scope]map[string]numRange {
	out := map[*Scope]map[string]numRange{}
	for sc := scope; sc != nil; sc = sc.Parent {
		out[sc] = cloneNumRangeMap(sc.rangeFacts)
	}
	return out
}

func restoreScopeRangeFacts(saved map[*Scope]map[string]numRange) {
	for sc, facts := range saved {
		sc.rangeFacts = cloneNumRangeMap(facts)
	}
}

// join widens a range to cover both itself and another (the union of two branch results): a bound is
// kept only if BOTH ranges bound that side, taking the looser endpoint. Used to combine the per-branch
// result ranges of a conditional into the range guaranteed regardless of which branch is taken.
func (r numRange) join(o numRange) numRange {
	var out numRange
	if r.loKnown && o.loKnown {
		out.loKnown = true
		if o.lo < r.lo {
			out.lo = o.lo
		} else {
			out.lo = r.lo
		}
	}
	if r.hiKnown && o.hiKnown {
		out.hiKnown = true
		if o.hi > r.hi {
			out.hi = o.hi
		} else {
			out.hi = r.hi
		}
	}
	return out
}

// intersect tightens a range with another fact about the same variable (conjunction of conditions).
func (r numRange) intersect(o numRange) numRange {
	out := r
	if o.loKnown && (!out.loKnown || o.lo > out.lo) {
		out.loKnown, out.lo = true, o.lo
	}
	if o.hiKnown && (!out.hiKnown || o.hi < out.hi) {
		out.hiKnown, out.hi = true, o.hi
	}
	return out
}

// gatherNumericRangeRefinement records integer-bound facts for an IMMUTABLE identifier or immutable
// struct field path compared against a compile-time-constant in a (truthy) branch condition:
// `a > 5`, `a >= 0`, `s.n <= 103`, `5 < a`, `a == k`, etc. Immutable-only so the fact holds for
// the whole branch with no invalidation. Field-path keys ("s.n") are invalidated by
// invalidateRangeFactsForTarget on any write to s.n or s. Called from
// applyConditionRefinementsInternal for comparison operators.
func (a *Analyzer) gatherNumericRangeRefinement(scope *Scope, n *ast.BinaryExpr, truthy bool) {
	if scope == nil || n == nil {
		return
	}
	// Normalize to `subject OP const`. If the constant is on the left (`5 < a`), flip the operator.
	// Try bare identifier first; fall back to a one-level field-path subject ("s.n").
	op := n.Op
	name, ok := immutableIntIdentName(a, scope, n.Left)
	identExpr := n.Left
	var c int64
	var cok bool
	if ok {
		c, cok = a.constIntValue(n.Right)
	} else if name, ok = immutableIntIdentName(a, scope, n.Right); ok {
		identExpr = n.Right
		c, cok = a.constIntValue(n.Left)
		op = flipComparison(op)
	} else if name, ok = fieldPathKey(a, scope, n.Left); ok {
		// `s.n OP const`
		c, cok = a.constIntValue(n.Right)
	} else if name, ok = fieldPathKey(a, scope, n.Right); ok {
		// `const OP s.n` — flip so subject is on the left
		identExpr = n.Right
		c, cok = a.constIntValue(n.Left)
		op = flipComparison(op)
	}
	if !ok || !cok {
		return
	}
	// The FALSY branch (the fall-through after `if a < c: return …`, or an else) narrows by the
	// LOGICAL NEGATION of the comparison: `not (a < c)` is `a >= c`. `==` falsy is `!=`, which is not
	// a single contiguous range, so it contributes nothing (default below). This is what makes an
	// early-return guard establish the post-guard bound for the static provers (docs/85 gap #3).
	if !truthy {
		op = negateComparison(op)
	}
	var fact numRange
	switch op {
	case lexer.TOKEN_GT: // a > c  ⇒ a >= c+1
		fact = numRange{loKnown: true, lo: c + 1}
	case lexer.TOKEN_GTEQ: // a >= c
		fact = numRange{loKnown: true, lo: c}
	case lexer.TOKEN_LT: // a < c  ⇒ a <= c-1
		fact = numRange{hiKnown: true, hi: c - 1}
	case lexer.TOKEN_LTEQ: // a <= c
		fact = numRange{hiKnown: true, hi: c}
	case lexer.TOKEN_EQEQ: // a == c
		fact = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}
	case lexer.TOKEN_BANGEQ: // a != c — a single range only for an UNSIGNED zero-check: a != 0 ⇒ a >= 1
		if signed, _, ok := smtIntWidthSign(a.exprTypes[identExpr]); ok && !signed && c == 0 {
			fact = numRange{loKnown: true, lo: 1}
		} else {
			return
		}
	default:
		return
	}
	if scope.rangeFacts == nil {
		scope.rangeFacts = map[string]numRange{}
	}
	scope.rangeFacts[name] = scope.rangeFacts[name].intersect(fact)
}

func (a *Analyzer) applyCountUpWhileExitFacts(stmt *ast.WhileStmt) {
	if a == nil || a.currentScope == nil || stmt == nil {
		return
	}
	name, bound, ok := a.countUpLoopUpperBound(stmt.Cond)
	if !ok || !bodyHasOnlyUnitIncrement(stmt.Body, name) {
		return
	}
	if a.currentScope.rangeFacts == nil {
		a.currentScope.rangeFacts = map[string]numRange{}
	}
	a.currentScope.rangeFacts[name] = a.currentScope.rangeFacts[name].intersect(numRange{hiKnown: true, hi: bound})
}

// countUpExitFactSound reports whether `applyCountUpWhileExitFacts` may soundly record `i <= bound`
// after `while i < bound: i++`. The exit fact is true ONLY when `i <= bound` at ENTRY: a count that
// starts at or below bound reaches exactly bound (the +1 step never overshoots), but a count that
// starts ABOVE bound never runs and keeps its too-large value. MUST be evaluated on the pristine
// pre-loop scope (before the body's own `i <- i + 1` mutates the tracked entry value).
func (a *Analyzer) countUpExitFactSound(stmt *ast.WhileStmt) bool {
	if a == nil || a.currentScope == nil || stmt == nil {
		return false
	}
	name, bound, ok := a.countUpLoopUpperBound(stmt.Cond)
	if !ok || !bodyHasOnlyUnitIncrement(stmt.Body, name) {
		return false
	}
	if r, found := a.lookupRangeFact(name); found && r.hiKnown && r.hi <= bound {
		return true
	}
	if c, known := a.lookupWrittenConst(name); known {
		if v, ok := a.constIntValue(c); ok && v <= bound {
			return true
		}
	}
	// A LIVE entry `requires name <= K` (or `< K`) with K <= bound also makes the exit fact sound: the
	// counter starts within the bound and only counts up to it. "Live" means the seeded requires
	// assert-fact still exists in scope — if `name` was reassigned before the loop, the standard mutation
	// invalidation already dropped it, so a pre-loop reassignment correctly disqualifies the bound. This
	// lets `requires i <= 5; while i < 5: i++` prove `result <= 5` SOUNDLY, without re-asserting the entry
	// precondition for the mutated post-loop value (the requires-as-permanent-axiom hole, audit cluster B).
	if hi, ok := a.liveRequiresUpperBound(name); ok && hi <= bound {
		return true
	}
	return false
}

// liveRequiresUpperBound returns the tightest constant K such that a `requires name <= K` (or `< K+1`)
// clause of the enclosing function is STILL a live assert-fact (not dropped by a mutation of name).
func (a *Analyzer) liveRequiresUpperBound(name string) (int64, bool) {
	if a == nil || a.currentFuncDecl == nil || name == "" {
		return 0, false
	}
	best, found := int64(0), false
	for _, req := range a.currentFuncDecl.Requires {
		hi, ok := a.requiresClauseUpperBound(req, name)
		if !ok || !a.assertFactLive(req) {
			continue
		}
		if !found || hi < best {
			best, found = hi, true
		}
	}
	return best, found
}

// assertFactLive reports whether `expr` is still present as a flow assert-fact in the active scope
// chain (seeded and not since invalidated by a mutation of one of its roots).
func (a *Analyzer) assertFactLive(expr ast.Expr) bool {
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for _, fact := range sc.smtAssertFacts {
			if fact.Expr == expr {
				return true
			}
		}
	}
	return false
}

// requiresClauseUpperBound extracts a constant upper bound on `name` from a `name <= K` / `name < K`
// (or the flipped `K >= name` / `K > name`) clause, or ok=false otherwise.
func (a *Analyzer) requiresClauseUpperBound(expr ast.Expr, name string) (int64, bool) {
	bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr)
	if !ok || bin == nil {
		return 0, false
	}
	leftIsName := isIdentNamed(bin.Left, name)
	rightIsName := isIdentNamed(bin.Right, name)
	switch bin.Op {
	case lexer.TOKEN_LTEQ: // name <= K  /  K <= name is a lower bound (ignored)
		if leftIsName {
			if k, ok := a.constIntValue(bin.Right); ok {
				return k, true
			}
		}
	case lexer.TOKEN_LT: // name < K  ⇒  name <= K-1
		if leftIsName {
			if k, ok := a.constIntValue(bin.Right); ok {
				return k - 1, true
			}
		}
	case lexer.TOKEN_GTEQ: // K >= name  ⇒  name <= K
		if rightIsName {
			if k, ok := a.constIntValue(bin.Left); ok {
				return k, true
			}
		}
	case lexer.TOKEN_GT: // K > name  ⇒  name <= K-1
		if rightIsName {
			if k, ok := a.constIntValue(bin.Left); ok {
				return k - 1, true
			}
		}
	}
	return 0, false
}

func isIdentNamed(expr ast.Expr, name string) bool {
	id, ok := stripOptimizationParens(expr).(*ast.Ident)
	return ok && id != nil && id.Name == name
}

func (a *Analyzer) countUpLoopUpperBound(expr ast.Expr) (string, int64, bool) {
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			if name, bound, ok := a.countUpLoopUpperBound(n.Left); ok {
				return name, bound, true
			}
			return a.countUpLoopUpperBound(n.Right)
		}
		if n.Op != lexer.TOKEN_LT {
			return "", 0, false
		}
		id, ok := stripOptimizationParens(n.Left).(*ast.Ident)
		if !ok || id == nil {
			return "", 0, false
		}
		bound, ok := a.constIntValue(n.Right)
		if !ok {
			return "", 0, false
		}
		return id.Name, bound, true
	default:
		return "", 0, false
	}
}

func bodyHasOnlyUnitIncrement(body []ast.Stmt, name string) bool {
	if name == "" {
		return false
	}
	seenIncrement := false
	for _, stmt := range body {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok {
			if stmtWritesRoot(stmt, name) {
				return false
			}
			continue
		}
		root, ok := rootIdentName(assign.Target)
		if !ok || root != name {
			continue
		}
		if seenIncrement || !isUnitIncrementOf(assign.Value, name) {
			return false
		}
		seenIncrement = true
	}
	return seenIncrement
}

func stmtWritesRoot(stmt ast.Stmt, name string) bool {
	var target ast.Expr
	switch n := stmt.(type) {
	case *ast.AssignStmt:
		target = n.Target
	case *ast.AugAssignStmt:
		target = n.Target
	case *ast.AsRefAssignStmt:
		target = n.Target
	default:
		return false
	}
	root, ok := rootIdentName(target)
	return ok && root == name
}

func isUnitIncrementOf(expr ast.Expr, name string) bool {
	bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr)
	if !ok || bin == nil || bin.Op != lexer.TOKEN_PLUS {
		return false
	}
	if id, ok := stripOptimizationParens(bin.Left).(*ast.Ident); ok && id != nil && id.Name == name {
		return isOneLiteral(bin.Right)
	}
	if id, ok := stripOptimizationParens(bin.Right).(*ast.Ident); ok && id != nil && id.Name == name {
		return isOneLiteral(bin.Left)
	}
	return false
}

func isOneLiteral(expr ast.Expr) bool {
	lit, ok := stripOptimizationParens(expr).(*ast.IntLit)
	return ok && lit != nil && lit.Value == "1"
}

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
	ft := a.exprTypes[expr]
	if ft == nil || !IsNumericType(ft) || IsFloatType(ft) {
		return "", false
	}
	return rootIdent.Name + "." + fe.Field, true
}

// constIntValue extracts a compile-time integer constant from an expression.
func (a *Analyzer) constIntValue(expr ast.Expr) (int64, bool) {
	cv, ok := a.evalConstExpr(expr)
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
			if r, ok := scope.rangeFacts[name]; ok {
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
	edges := a.collectRelationalEdges()
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
				if r, ok := scope.rangeFacts[n]; ok {
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
			node string
			adj  int64 // cumulative offset: result_hi = peer_hi + adj
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
	names := map[string]bool{}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		for name := range scope.rangeFacts {
			names[name] = true
		}
		if scope.closedWorld {
			break
		}
	}
	for name := range names {
		if r, ok := a.lookupRangeFact(name); ok {
			out[name] = r
		}
	}
	return out
}

// invalidateRangeFacts drops the known integer range fact about `name` across the active scope chain.
// Called at every mutation site for `name`, mirroring invalidatePredFacts. Unlike predFacts there is
// NO dependent-fact cascade: a range fact is a concrete interval snapshot (even one seeded from another
// variable's range captured that variable's bound at seed time), so it has no live symbolic dependence
// on other variables — only a write to the SUBJECT itself can stale its interval (docs/90 brick 90-11).
func (a *Analyzer) invalidateRangeFacts(name string) {
	if name == "" {
		return
	}
	// Consult each fact's own deps() via the unified predicate (analyzer_fact.go), mirroring assert-fact
	// invalidation. A range fact's sole dep is its subject variable, so this deletes exactly `name`.
	rootSet := map[string]bool{name: true}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.rangeFacts == nil {
			continue
		}
		for n, r := range scope.rangeFacts {
			if factInvalidatedBy(rangeHypothesisFact{name: n, r: r}, rootSet) {
				delete(scope.rangeFacts, n)
			}
		}
	}
}

// invalidateRangeFactsForFieldPath drops any range fact keyed on the exact path `path` (e.g.
// "s.n") across the active scope chain. Used when a direct field mutation `s.n <- …` occurs.
func (a *Analyzer) invalidateRangeFactsForFieldPath(path string) {
	if path == "" {
		return
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.rangeFacts != nil {
			delete(scope.rangeFacts, path)
		}
	}
}

// invalidateRangeFactsWithRootPrefix drops every range fact whose key equals `root` OR starts with
// `root + "."` — covering both a plain-ident fact and any field-path facts whose struct root is
// `root`. Called when the struct variable itself is mutated or reassigned (a write to `s` stales
// all "s.f" facts).
func (a *Analyzer) invalidateRangeFactsWithRootPrefix(root string) {
	if root == "" {
		return
	}
	prefix := root + "."
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.rangeFacts == nil {
			continue
		}
		for k := range scope.rangeFacts {
			if k == root || len(k) > len(prefix) && k[:len(prefix)] == prefix {
				delete(scope.rangeFacts, k)
			}
		}
	}
}

// invalidateRangeFactsForTarget drops the range fact about the root variable of a mutation target
// expression (an identifier, or a field/index path rooted at one), mirroring invalidatePredFactsForTarget.
// For a field-path target `s.n`, it drops the exact path fact "s.n". For any mutation whose root
// is a plain identifier `s`, it drops `s` AND all field-path facts of the form "s.*" (because a
// write to the struct as a whole stales every field fact).
func (a *Analyzer) invalidateRangeFactsForTarget(target ast.Expr) {
	// If the target is a direct field expression `s.field`, drop only that exact path fact as well as
	// the root ident (via the existing plain-ident invalidation below). This is tighter than dropping
	// all "s.*" paths on a field write — only "s.field" is stale.
	if fe, ok := target.(*ast.FieldExpr); ok && fe != nil {
		if rootIdent, ok := fe.Object.(*ast.Ident); ok && rootIdent != nil {
			a.invalidateRangeFactsForFieldPath(rootIdent.Name + "." + fe.Field)
		}
	}
	for _, name := range a.mutationRootsForTarget(target) {
		a.invalidateRangeFacts(name)
		// Also invalidate any field-path facts keyed on this root (e.g. "name.f"), in case the
		// mutation target is the struct itself (not a specific field).
		a.invalidateRangeFactsWithRootPrefix(name)
	}
}

func (a *Analyzer) recordConstAssignmentRangeFact(target ast.Expr, value ast.Expr) {
	if a == nil || a.currentScope == nil {
		return
	}
	name, ok := rootIdentName(target)
	if !ok || name == "" {
		return
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil || !IsNumericType(sym.Type) || IsFloatType(sym.Type) {
		return
	}
	c, ok := a.constIntValue(value)
	if ok {
		if a.currentScope.rangeFacts == nil {
			a.currentScope.rangeFacts = map[string]numRange{}
		}
		a.currentScope.rangeFacts[name] = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}
		return
	}
	// Ternary RHS: seed the join of the two branch ranges so that downstream proofs on `name`
	// can discharge bounds without re-examining the ternary expression structure.
	if tern, isTern := value.(*ast.TernaryExpr); isTern {
		if r, derived := a.tryDeriveTernaryRange(tern); derived {
			if a.currentScope.rangeFacts == nil {
				a.currentScope.rangeFacts = map[string]numRange{}
			}
			a.currentScope.rangeFacts[name] = r
		}
	}
}

// recordCastRangeFact propagates integer range facts from the source of a `.cast[T]` assignment
// `target = src.cast[T]` to `target` when the cast is provably value-preserving.
//
// `.cast[T]` is a bitwise reinterpretation (not a value conversion). For the range of `src` to
// be valid for `target` after the cast, the bit pattern must encode the same numeric value in T.
// This holds in two cases:
//
//   (a) Identity cast (src type == T): the bits are unchanged, so the range carries exactly.
//   (b) Sign-flip cast (same bit width, different sign, e.g. i64↔u64): value-preserving only
//       when the proven range is fully bounded ([lo, hi] both known) and BOTH endpoints fit T.
//       An open bound over a sign-flip means values in the out-of-range half could exist and
//       would reinterpret to negative/positive values, so we decline.
//
// Any other integer cast is rejected by the compiler as a value conversion (must use constructors),
// so this function only needs to handle (a) and (b).
func (a *Analyzer) recordCastRangeFact(target ast.Expr, value ast.Expr) {
	if a == nil || a.currentScope == nil {
		return
	}
	castExpr, ok := unwrapParen(value).(*ast.CastExpr)
	if !ok || castExpr == nil {
		return
	}
	targetName, ok := rootIdentName(target)
	if !ok || targetName == "" {
		return
	}
	sym, ok := a.currentScope.Lookup(targetName)
	if !ok || sym == nil || !IsNumericType(sym.Type) || IsFloatType(sym.Type) {
		return
	}
	// Resolve the source operand to an identifier we can look up.
	srcIdent, ok := unwrapParen(castExpr.Operand).(*ast.Ident)
	if !ok || srcIdent == nil {
		return
	}
	// Look up the source variable's range fact (walks the full scope chain).
	srcRange, found := a.lookupRangeFact(srcIdent.Name)
	if !found || (!srcRange.loKnown && !srcRange.hiKnown) {
		return
	}
	// Resolve the destination type of the cast.
	dstType := a.resolveType(castExpr.Target)
	if dstType == nil {
		return
	}
	dstSigned, dstWidth, dstOk := BitIntInfo(dstType)
	if !dstOk {
		return
	}
	// Resolve the source variable's declared type to check cast kind.
	srcSym, srcFound := a.currentScope.Lookup(srcIdent.Name)
	if !srcFound || srcSym == nil {
		return
	}
	srcSigned, srcWidth, srcOk := BitIntInfo(srcSym.Type)
	if !srcOk {
		return
	}
	var r numRange
	if srcSigned == dstSigned && srcWidth == dstWidth {
		// Case (a): identity cast — bit pattern identical, range carries exactly.
		r = srcRange
	} else if srcWidth == dstWidth {
		// Case (b): sign-flip, same width (e.g. i64↔u64). Value-preserving only when the
		// proven range is fully bounded AND both endpoints fit T (no out-of-range half).
		if !srcRange.loKnown || !srcRange.hiKnown {
			return // open bound over a sign-flip: cannot guarantee fitness
		}
		if !IntegerTypeFitsValue(dstType, srcRange.lo) || !IntegerTypeFitsValue(dstType, srcRange.hi) {
			return // at least one endpoint wraps — range would be unsound
		}
		r = srcRange
	} else {
		// Different width: a narrowing numeric cast would be a value conversion (rejected by the
		// compiler), so this branch is only reachable for exotic non-integer or special casts that
		// are not numeric — decline conservatively.
		return
	}
	// Tighten by the target variable's declared type range (adds non-negativity etc.).
	if tr, ok2 := declaredTypeRange(sym.Type); ok2 {
		r = r.intersect(tr)
	}
	if !r.loKnown && !r.hiKnown {
		return
	}
	_ = dstSigned // both dstSigned and dstWidth were used in the case analysis above
	if a.currentScope.rangeFacts == nil {
		a.currentScope.rangeFacts = map[string]numRange{}
	}
	a.currentScope.rangeFacts[targetName] = r
}

// seedIntegerMatchArmFact seeds the range fact `scrutinee == literal` into an integer match arm's
// scope when the scrutinee is an immutable integer variable and the arm pattern is a literal.
// This lets the flow prover discharge refinement obligations (e.g. `x is Five`) inside the arm
// body — `match k: 5:` knows `k ∈ [5,5]` for the arm body, just like `if k == 5:` does.
// The fact is scoped to the arm scope and therefore cannot leak to other arms or past the match.
func (a *Analyzer) seedIntegerMatchArmFact(scrutineeExpr ast.Expr, pattern ast.MatchPattern, armScope *Scope) {
	if a == nil || armScope == nil {
		return
	}
	litPat, ok := pattern.(*ast.MatchLiteralPattern)
	if !ok {
		return
	}
	name, ok := immutableIntIdentName(a, a.currentScope, scrutineeExpr)
	if !ok {
		return
	}
	c, ok := a.constIntValue(litPat.Value)
	if !ok {
		return
	}
	if armScope.rangeFacts == nil {
		armScope.rangeFacts = map[string]numRange{}
	}
	armScope.rangeFacts[name] = armScope.rangeFacts[name].intersect(numRange{loKnown: true, lo: c, hiKnown: true, hi: c})
}

// recordWideningCastRangeFact seeds a range fact for `target` when `value` is a chain of
// value-preserving widening casts from a source variable whose range is already known. For
// example, given `y: u64 = x.u64()` where `x` has proven range [0, 100], this records [0, 100]
// for `y` so that tier-2 proofs over `y` (or further casts of `y`) can carry the range
// through without re-doing the guard analysis.
//
// Sound: the same value-preservation check used by affineOf/CastExpr is applied here —
// IntegerTypeFitsValue on both bounds. A narrowing cast, sign-changing cast, or any cast
// whose source range is not fully known leaves no fact (fail-closed). Only immutable integer
// targets are handled, since a mutable target can be reassigned and would need invalidation.
func (a *Analyzer) recordWideningCastRangeFact(target ast.Expr, value ast.Expr, mutable bool) {
	if a == nil || a.currentScope == nil || mutable {
		return // mutable targets need invalidation; skip to avoid stale facts
	}
	name, ok := rootIdentName(target)
	if !ok || name == "" {
		return
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil {
		return
	}
	if IsFloatType(sym.Type) {
		return
	}
	if _, _, isInt := BitIntInfo(sym.Type); !isInt {
		return
	}
	// Peel the cast chain, collecting each intermediate target type for value-preservation checks.
	expr := value
	var castTypes []Type
	for {
		ce, ok := expr.(*ast.CastExpr)
		if !ok {
			break
		}
		t := a.resolveType(ce.Target)
		if _, _, isInt := BitIntInfo(t); !isInt {
			return // non-integer intermediate — cannot reason about value preservation
		}
		castTypes = append(castTypes, t)
		expr = ce.Operand
	}
	if len(castTypes) == 0 {
		return // value is not a cast at all
	}
	// The innermost expr must be an immutable integer identifier with a known range.
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil {
		return
	}
	r, ok := a.lookupRangeFact(ident.Name)
	if !ok {
		return // source has no known range — nothing to transfer
	}
	if !r.loKnown || !r.hiKnown {
		return // open source range — unsafe to transfer
	}
	// Validate that every cast in the chain is value-preserving (range fits in each target type).
	for _, t := range castTypes {
		if !IntegerTypeFitsValue(t, r.lo) || !IntegerTypeFitsValue(t, r.hi) {
			return // narrowing or sign-changing cast somewhere in the chain — decline
		}
	}
	// All casts are value-preserving: record the source range for the target variable.
	if a.currentScope.rangeFacts == nil {
		a.currentScope.rangeFacts = map[string]numRange{}
	}
	a.currentScope.rangeFacts[name] = r
}

func declaredTypeRange(t Type) (numRange, bool) {
	signed, width, ok := BitIntInfo(t)
	if !ok || width <= 0 || width >= 63 {
		return numRange{}, false
	}
	if signed {
		return numRange{loKnown: true, lo: -(int64(1) << (width - 1)), hiKnown: true, hi: (int64(1) << (width - 1)) - 1}, true
	}
	return numRange{loKnown: true, lo: 0, hiKnown: true, hi: (int64(1) << width) - 1}, true
}

func sameNumRange(a, b numRange) bool {
	return a.loKnown == b.loKnown && a.lo == b.lo && a.hiKnown == b.hiKnown && a.hi == b.hi
}

func (a *Analyzer) mergePostIfRangeFacts(entry map[string]numRange, branches []map[string]numRange) {
	if a == nil || a.currentScope == nil || len(branches) == 0 {
		return
	}
	names := map[string]bool{}
	for name := range entry {
		names[name] = true
	}
	for _, br := range branches {
		for name := range br {
			names[name] = true
		}
	}
	for name := range names {
		base, hadBase := entry[name]
		changed := false
		for _, br := range branches {
			r, ok := br[name]
			if !ok {
				if !hadBase {
					changed = true
					break
				}
				r = base
			}
			if !hadBase || !sameNumRange(r, base) {
				changed = true
				break
			}
		}
		if !changed {
			continue
		}
		var joined numRange
		joinedSet := false
		for _, br := range branches {
			r, ok := br[name]
			if !ok {
				if hadBase {
					r = base
				} else if sym, found := a.currentScope.Lookup(name); found && sym != nil {
					if tr, found := declaredTypeRange(sym.Type); found {
						r = tr
					}
				}
			}
			if !ok && !hadBase {
				if sym, found := a.currentScope.Lookup(name); found && sym != nil {
					if tr, found := declaredTypeRange(sym.Type); found {
						r = tr
						ok = true
					}
				}
			}
			if !ok && !hadBase {
				r = numRange{}
			}
			if !joinedSet {
				joined, joinedSet = r, true
			} else {
				joined = joined.join(r)
			}
		}
		if a.currentScope.rangeFacts == nil {
			a.currentScope.rangeFacts = map[string]numRange{}
		}
		a.currentScope.rangeFacts[name] = joined
	}
}

// writtenConstInt returns the exact integer value of a variable when a live written-constant fact
// pins it to a compile-time integer (an immutable local or a `<- const` write). The bridge between
// the written-const tracker and the interval prover.
func (a *Analyzer) writtenConstInt(name string) (int64, bool) {
	v, ok := a.lookupWrittenConst(name)
	if !ok || v == nil {
		return 0, false
	}
	return a.constIntValue(v)
}

// lawConstraint is one `self OP const` clause of a law body in the decidable fragment.
type lawConstraint struct {
	op lexer.TokenKind
	c  int64
}

// lawConstraints interprets a law body as a conjunction of `self OP const` constraints, or returns
// false if the body is outside the decidable fragment (then the flow prover declines and discharge
// falls back to a runtime check). `self` is the law's first parameter name; `paramConsts` binds the
// law's remaining (static `[..]`) params to the refinement's bracket-arg constants, so a body like
// `self >= lo and self <= hi` is interpreted against the actual bounds.
func (a *Analyzer) lawConstraints(decl *ast.FuncDecl, paramConsts map[string]int64) ([]lawConstraint, bool) {
	return a.lawConstraintsRanged(decl, paramConsts, nil)
}

// lawConstraintsRanged is lawConstraints with an extra `paramRanges` channel (docs/90 brick 90-9):
// a static law param bound to a known INTERVAL (rather than an exact constant) is resolved
// direction-aware — its lower bound for a `self >= param` constraint, its upper bound for a
// `self <= param` constraint — which is the only sound way to use a non-constant bracket argument
// (e.g. `cap_to(k)` with `k ∈ [0, 10]` yields `result <= 10`). paramConsts is consulted first;
// paramRanges (nil for the exact-constant callers) is the fallback.
func (a *Analyzer) lawConstraintsRanged(decl *ast.FuncDecl, paramConsts map[string]int64, paramRanges map[string]numRange) ([]lawConstraint, bool) {
	if decl == nil || len(decl.Params) == 0 || len(decl.Body) != 1 {
		return nil, false
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok || ret == nil || ret.Value == nil {
		return nil, false
	}
	self := decl.Params[0].Name
	var out []lawConstraint
	if !a.collectLawConstraints(ret.Value, self, paramConsts, paramRanges, &out) {
		return nil, false
	}
	return out, true
}

// collectLawConstraints walks a conjunction of `self OP <const>` comparisons, where the operand is
// a literal constant, a static param bound in paramConsts, or (brick 90-9) a static param bound to a
// direction-appropriate interval in paramRanges. Any other shape makes the whole body undecidable
// (returns false) so the prover stays sound by declining.
func (a *Analyzer) collectLawConstraints(expr ast.Expr, self string, paramConsts map[string]int64, paramRanges map[string]numRange, out *[]lawConstraint) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.collectLawConstraints(n.Inner, self, paramConsts, paramRanges, out)
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			return a.collectLawConstraints(n.Left, self, paramConsts, paramRanges, out) && a.collectLawConstraints(n.Right, self, paramConsts, paramRanges, out)
		}
		// Normalize to `self OP operand` (the operand may sit on either side).
		var operand ast.Expr
		var op lexer.TokenKind
		switch {
		case isSelfIdent(n.Left, self):
			operand, op = n.Right, n.Op
		case isSelfIdent(n.Right, self):
			operand, op = n.Left, flipComparison(n.Op)
		default:
			return false
		}
		if c, ok := a.operandConst(operand, paramConsts); ok {
			*out = append(*out, lawConstraint{op: op, c: c})
			return true
		}
		// Ranged fallback: a param bound to an interval contributes the bound that matches the
		// comparison direction. `self >= param` (param ∈ [lo, hi]) ⟹ self >= lo; `self <= param` ⟹
		// self <= hi. `==` against a non-constant interval cannot become a single constraint → decline.
		if c, ok := a.operandRangeBound(operand, op, paramRanges); ok {
			*out = append(*out, lawConstraint{op: op, c: c})
			return true
		}
		return false
	default:
		return false
	}
}

// operandRangeBound resolves a law-body operand that is a static param bound to an interval, picking
// the bound that keeps `self OP operand` sound for the given direction. Returns ok=false when the
// operand is not a ranged param, the needed side of its interval is unknown, or the operator is one
// for which a single constant bound would be unsound (`==`, `!=`).
func (a *Analyzer) operandRangeBound(expr ast.Expr, op lexer.TokenKind, paramRanges map[string]numRange) (int64, bool) {
	if paramRanges == nil {
		return 0, false
	}
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil {
		return 0, false
	}
	r, ok := paramRanges[ident.Name]
	if !ok {
		return 0, false
	}
	switch op {
	case lexer.TOKEN_GTEQ, lexer.TOKEN_GT: // self >= param  ⟹  self >= param.lo
		if r.loKnown {
			return r.lo, true
		}
	case lexer.TOKEN_LTEQ, lexer.TOKEN_LT: // self <= param  ⟹  self <= param.hi
		if r.hiKnown {
			return r.hi, true
		}
	}
	return 0, false
}

// operandConst resolves a law-body comparison operand to a constant: a literal, or a static law
// param bound to a bracket-arg constant.
func (a *Analyzer) operandConst(expr ast.Expr, paramConsts map[string]int64) (int64, bool) {
	if ident, ok := expr.(*ast.Ident); ok && ident != nil {
		if c, bound := paramConsts[ident.Name]; bound {
			return c, true
		}
	}
	return a.constIntValue(expr)
}

func isSelfIdent(expr ast.Expr, self string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident != nil && ident.Name == self
}

// rangeEntailsConstraint reports whether the known range provably satisfies one law constraint.
//
// Sound cases handled:
//   - Interval-implies-comparison: [lo,hi] ⊆ {x | x OP c} for the basic comparison ops.
//   - Equality (degenerate interval): [k,k] entails any comparison that k satisfies, including
//     strict forms (k > k-1, k < k+1) already handled by the lo/hi cases below.
//   - Strict/non-strict duality via off-by-one: [lo,hi] entails x >= lo (already: r.lo >= lo)
//     and also x > lo-1 (already: r.lo > lo-1). Both directions are covered by the GT/GTEQ cases.
//   - Non-equality: [lo,hi] entails x != c when c is strictly outside [lo,hi] — i.e. c < lo
//     (every value in the range is > c, hence != c) or c > hi (every value is < c, hence != c).
func rangeEntailsConstraint(r numRange, k lawConstraint) bool {
	switch k.op {
	case lexer.TOKEN_GTEQ: // self >= c
		return r.loKnown && r.lo >= k.c
	case lexer.TOKEN_GT: // self > c
		return r.loKnown && r.lo > k.c
	case lexer.TOKEN_LTEQ: // self <= c
		return r.hiKnown && r.hi <= k.c
	case lexer.TOKEN_LT: // self < c
		return r.hiKnown && r.hi < k.c
	case lexer.TOKEN_EQEQ: // self == c  (degenerate range [c,c] is the only proof)
		return r.loKnown && r.hiKnown && r.lo == k.c && r.hi == k.c
	case lexer.TOKEN_BANGEQ: // self != c
		// Sound: the range is entirely below c (every value < c, hence != c), or entirely above c.
		// A one-sided open range is insufficient: we need to rule out c being in-range on BOTH sides.
		belowC := r.hiKnown && r.hi < k.c
		aboveC := r.loKnown && r.lo > k.c
		return belowC || aboveC
	default:
		return false
	}
}

// paramRefinementTypeExpr returns the refinement on a param's declared type expr — directly
// (`tx: i32 is Bounded[..]`) or through a type alias (`tx: TileX`, `type TileX = i32 is Bounded[..]`).
// The alias channel is aliasRefinements (namedTypes erases the refinement), keyed by the SAME
// canonical name resolveType uses, so the seed is never confused with a like-named alias elsewhere.
func (a *Analyzer) paramRefinementTypeExpr(te ast.TypeExpr) (*ast.RefinementTypeExpr, bool) {
	switch n := te.(type) {
	case *ast.RefinementTypeExpr:
		return n, n != nil
	case *ast.NamedType:
		if _, canonical, ok := a.lookupVisibleType(n.Name); ok && canonical != "" {
			if rt, found := a.aliasRefinements[canonical]; found {
				return rt, true
			}
		}
		if rt, found := a.aliasRefinements[n.Name]; found {
			return rt, true
		}
	}
	return nil, false
}

func (a *Analyzer) paramRefinementRangeFacts(params []ast.ParamDecl) map[int]numRange {
	var out map[int]numRange
	for i, param := range params {
		fact, any := a.paramRefinementRangeFact(param.Type, param.Name)
		if !any {
			continue
		}
		if out == nil {
			out = map[int]numRange{}
		}
		out[i] = fact
	}
	return out
}

func (a *Analyzer) paramRefinementRangeFact(te ast.TypeExpr, subjectName string) (numRange, bool) {
	switch n := te.(type) {
	case *ast.RefType:
		if n == nil {
			return numRange{}, false
		}
		return a.paramRefinementRangeFact(n.Elem, subjectName)
	case *ast.MutableType:
		if n == nil {
			return numRange{}, false
		}
		return a.paramRefinementRangeFact(n.Elem, subjectName)
	case *ast.OwnedType:
		if n == nil {
			return numRange{}, false
		}
		return a.paramRefinementRangeFact(n.Elem, subjectName)
	}
	if lo, hi, ok := a.refinementIntervalOfTypeExpr(te, subjectName); ok {
		return numRange{loKnown: true, lo: lo, hiKnown: true, hi: hi}, true
	}
	rt, ok := a.paramRefinementTypeExpr(te)
	if !ok {
		return numRange{}, false
	}
	return a.rangeFromRefinementTypeExpr(rt, nil)
}

// seedParamRefinementFacts records, on the function entry scope, the integer range implied by each
// IMMUTABLE integer param's declared refinement (docs/86 brick 86-2). This is what lets the docs/85
// §13 form `def tile_index(tx: TileX, ty: TileY) -> usize is Bounded[..]` prove with NO body guard:
// the params carry their bounds on entry, and tier-1/tier-2 read them from rangeFacts as usual.
// Mutable params are skipped (a fact could be invalidated mid-body) — sound, just no seed.
func (a *Analyzer) seedParamRefinementFacts(params []ast.ParamDecl) {
	if a.currentScope == nil {
		return
	}
	for _, param := range params {
		if a.paramIsMutable(param) {
			continue
		}
		rt, ok := a.paramRefinementTypeExpr(param.Type)
		if !ok {
			continue
		}
		sym, ok := a.currentScope.Lookup(param.Name)
		if !ok || sym == nil || sym.Mutable || !IsNumericType(sym.Type) || IsFloatType(sym.Type) {
			continue
		}
		fact, any := a.rangeFromRefinementTypeExpr(rt, nil)
		if !any {
			continue
		}
		if a.currentScope.rangeFacts == nil {
			a.currentScope.rangeFacts = map[string]numRange{}
		}
		a.currentScope.rangeFacts[param.Name] = a.currentScope.rangeFacts[param.Name].intersect(fact)
	}
}

func (a *Analyzer) seedMutableRefParamRefinementRangeFact(arg ast.Expr, fact numRange) {
	a.seedMutableRefParamRefinementRangeFactInScope(a.currentScope, arg, fact)
}

func (a *Analyzer) seedMutableRefParamRefinementRangeFactInScope(scope *Scope, arg ast.Expr, fact numRange) {
	if scope == nil || arg == nil {
		return
	}
	name, ok := directIdentName(arg)
	if !ok {
		return
	}
	sym, ok := scope.Lookup(name)
	if !ok || sym == nil || !IsNumericType(sym.Type) || IsFloatType(sym.Type) {
		return
	}
	if scope.rangeFacts == nil {
		scope.rangeFacts = map[string]numRange{}
	}
	scope.rangeFacts[name] = scope.rangeFacts[name].intersect(fact)
}

func directIdentName(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil {
			return "", false
		}
		return n.Name, true
	case *ast.ParenExpr:
		if n == nil {
			return "", false
		}
		return directIdentName(n.Inner)
	case *ast.UnaryExpr:
		if n == nil || n.Op != lexer.TOKEN_AMPERSAND {
			return "", false
		}
		return directIdentName(n.Operand)
	case *ast.AddrOfExpr:
		if n == nil {
			return "", false
		}
		return directIdentName(n.Operand)
	default:
		return "", false
	}
}

// rangeFromRefinementTypeExpr computes the integer interval implied by a refinement type expression
// whose law predicates reduce to compile-time-constant arguments (e.g. `i64 is Bounded[0, 100]`). It
// is the shared kernel behind both the param-entry seed (seedParamRefinementFacts) and the
// caller-side return-refinement seed (seedReturnRefinementFacts): a refinement on a function's return
// type IS its postcondition, so binding its result lets the caller assume the bound.
//
// `subst` (nil for the param-entry case) maps callee parameter names to the CALLER's argument
// expressions, so a parametric return refinement like `-> i64 is Bounded[0, n]` called as `f(100)`
// substitutes `n` → `100` and yields `[0, 100]` in the caller. A law arg that does not reduce to a
// constant after substitution drops that predicate — fails closed, never widens.
func (a *Analyzer) rangeFromRefinementTypeExpr(rt *ast.RefinementTypeExpr, subst map[string]ast.Expr) (numRange, bool) {
	if rt == nil {
		return numRange{}, false
	}
	return a.rangeFromRefinementPreds(rt.Preds, subst)
}

func (a *Analyzer) rangeFromRefinementPreds(preds []ast.RefinementPredExpr, subst map[string]ast.Expr) (numRange, bool) {
	fact := numRange{}
	any := false
	for _, pred := range preds {
		r, ok := a.rangeFromLawApplication(pred.Name, pred.Args, subst)
		if !ok {
			continue
		}
		fact = fact.intersect(r)
		any = true
	}
	return fact, any
}

// rangeFromLawApplication computes the integer interval implied by ONE law application `Law[args...]`
// (the subject being the value the law constrains). It is the per-predicate kernel shared by the
// return-type seed (rangeFromRefinementTypeExpr) and the `ensures <param> is Law` post-call seed
// (seedEnsuresParamRangeFacts, brick 90-11). `subst` maps callee params to caller arguments for parametric bounds
// (nil = exact-constant args). Returns ok=false when the args/law leave the decidable fragment.
func (a *Analyzer) rangeFromLawApplication(lawName string, lawArgs []ast.Expr, subst map[string]ast.Expr) (numRange, bool) {
	decl, _, ok := a.lookupLaw(lawName)
	if !ok || decl == nil || len(lawArgs) != len(decl.Params)-1 {
		return numRange{}, false
	}
	paramConsts := map[string]int64{}
	var paramRanges map[string]numRange
	for i, arg := range lawArgs {
		name := decl.Params[i+1].Name
		if c, ok := a.substConstInt(arg, subst); ok {
			paramConsts[name] = c
			continue
		}
		// Not an exact constant: try a known interval for the (substituted) argument, used
		// direction-aware in collectLawConstraints (brick 90-9). nil subst (param-entry seed) never
		// reaches here with a range, so that path is unchanged.
		if r, ok := a.substArgRange(arg, subst); ok {
			if paramRanges == nil {
				paramRanges = map[string]numRange{}
			}
			paramRanges[name] = r
		}
		// Either way leave the param out of paramConsts; collectLawConstraints declines any
		// constraint whose operand it cannot resolve, dropping the whole predicate (sound).
	}
	constraints, ok := a.lawConstraintsRanged(decl, paramConsts, paramRanges)
	if !ok || len(constraints) == 0 {
		return numRange{}, false
	}
	fact := numRange{}
	for _, k := range constraints {
		fact = fact.intersect(constraintToRange(k))
	}
	return fact, true
}

// substConstInt const-evaluates an integer expression, first replacing any identifier named in
// `subst` (a callee parameter) with the caller's argument expression (evaluated in the caller scope).
// With subst==nil it is exactly constIntValue. Covers the small arithmetic fragment a refinement's
// bracket arguments can use (`Bounded[0, n]`, `Bounded[0, n - 1]`, `Bounded[0, n * 2]`). Anything
// outside the fragment, or a substituted argument that is not itself constant, returns ok=false.
func (a *Analyzer) substConstInt(expr ast.Expr, subst map[string]ast.Expr) (int64, bool) {
	if subst == nil {
		return a.constIntValue(expr)
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.substConstInt(n.Inner, subst)
	case *ast.Ident:
		if arg, ok := subst[n.Name]; ok {
			// The callee parameter is bound to the caller's argument; that argument must const-fold in
			// the caller scope for the bracket value to be statically known.
			return a.constIntValue(arg)
		}
		return a.constIntValue(n)
	case *ast.UnaryExpr:
		if n.Op != lexer.TOKEN_MINUS {
			return 0, false
		}
		v, ok := a.substConstInt(n.Operand, subst)
		if !ok {
			return 0, false
		}
		return -v, true
	case *ast.BinaryExpr:
		l, ok := a.substConstInt(n.Left, subst)
		if !ok {
			return 0, false
		}
		r, ok := a.substConstInt(n.Right, subst)
		if !ok {
			return 0, false
		}
		switch n.Op {
		case lexer.TOKEN_PLUS:
			return l + r, true
		case lexer.TOKEN_MINUS:
			return l - r, true
		case lexer.TOKEN_STAR:
			return l * r, true
		case lexer.TOKEN_SLASH:
			if r == 0 {
				return 0, false
			}
			return l / r, true
		default:
			return 0, false
		}
	default:
		return a.constIntValue(expr)
	}
}

// substArgRange bounds a refinement bracket argument as an interval after substituting callee params
// with caller arguments (docs/90 brick 90-9). It reuses the bounded-linear machinery: the substituted
// expression's affine form, bounded over the caller's range facts. Used only as the fallback when the
// argument is not an exact constant (e.g. `cap_to(k)` with `k ∈ [0, 10]` makes the bracket arg `n`
// resolve to `[0, 10]`). Returns ok=false outside the linear fragment.
func (a *Analyzer) substArgRange(arg ast.Expr, subst map[string]ast.Expr) (numRange, bool) {
	if subst == nil {
		return numRange{}, false
	}
	af, ok := a.substitutedAffine(arg, subst)
	if !ok {
		return numRange{}, false
	}
	r := a.boundAffine(af, a.currentScope)
	if !r.loKnown && !r.hiKnown {
		return numRange{}, false
	}
	return r, true
}

// seedReturnRefinementFacts records the integer range implied by a callee's REFINED return type onto
// an immutable integer binding `name = f(args)` (docs/90 brick 90-7). The return refinement is the
// function's postcondition; a caller that binds the result may assume it. This closes the modular
// loop: a function PROVES its return refinement (dischargeReturnRefinements), and every caller then
// USES it as a fact — without re-deriving it from the body.
//
// Sound and conservative:
//   - Immutable bindings only (the caller passes n.Mutable==false); a mutable binding could be
//     reassigned, invalidating the fact.
//   - Only direct calls to a resolvable FuncDecl with a constant-argument return refinement; anything
//     else simply seeds nothing.
//   - The seed only NARROWS (intersect), so it can never widen an existing fact unsoundly.
//   - Like all SMT/refinement VALUE facts, this never drives bounds-check elision (that is the
//     separate syntactic indexBoundsProven system), so even a buggy callee contract cannot create
//     memory unsafety — it is garbage-in-garbage-out at worst.
func (a *Analyzer) seedReturnRefinementFacts(name string, value ast.Expr, bindingType Type) {
	if a.currentScope == nil || value == nil {
		return
	}
	if !IsNumericType(bindingType) || IsFloatType(bindingType) {
		return
	}
	call, ok := a.proofCallExpr(value)
	if !ok {
		return
	}
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok || decl == nil {
		return
	}
	// Bind each callee parameter to the caller's argument, so a parametric postcondition
	// (`-> i64 is Bounded[0, n]`, `ensure result <= n`) is resolved in the caller's terms. Absent args
	// (variadic/defaulted) are simply not bound — the dependent clause then drops, never widens.
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(call.Args) || call.Args[i] == nil {
			continue
		}
		subst[param.Name] = call.Args[i]
	}
	fact := numRange{}
	any := false
	// (1) The refined return type (`-> i64 is Bounded[..]`) — bricks 90-7/8/9.
	if s, ok := a.valueRefinementSchemeFromTypeExpr(decl.ReturnType); ok {
		if r, found := a.rangeFromRefinementPreds(s.Preds, subst); found {
			fact = fact.intersect(r)
			any = true
		}
	}
	// (2) Value-contract postconditions over `result` (`ensure result >= 0`, `ensure result <= n`) —
	// brick 90-10. These constrain the returned value, which IS this (immutable) binding, so the caller
	// may assume them just like the return refinement. Reuses the same direction-aware bracket machinery
	// with `result` as the subject.
	if r, found := a.rangeFromEnsureResult(decl.EnsureValues, subst); found {
		fact = fact.intersect(r)
		any = true
	}
	if !any {
		return
	}
	if a.currentScope.rangeFacts == nil {
		a.currentScope.rangeFacts = map[string]numRange{}
	}
	a.currentScope.rangeFacts[name] = a.currentScope.rangeFacts[name].intersect(fact)
}

// seedEnsuresParamRangeFacts records, at a call site, the integer interval implied by a callee
// postcondition `ensures <param> is Law` onto the (mutable) caller variable bound to that argument
// (docs/90 brick 90-11). It complements the predicate-fact gain in the same call loop: where the
// predFact lets a later `x is Law` discharge by factset identity, this lets the flow/interval prover
// use `x`'s numeric bound directly (as a bracket argument, an array index, a comparison operand).
//
// Caller is responsible for restricting this to MUTABLE-REF params, where the postcondition genuinely
// constrains the caller's variable (for a by-value or immutable-ref param the postcondition is about
// the callee's local copy and says nothing about the caller's binding). Sound and conservative:
//   - Applied AFTER the call-site mutable-ref invalidation, and `x`'s range fact is dropped again at the
//     next mutation of `x` (invalidateRangeFactsForTarget at every assignment + every ref-arg call), so
//     the snapshot interval can never outlive a write — the same envelope that makes the predFact gain
//     sound, extended to the interval store.
//   - The interval is a concrete snapshot (the callee guaranteed `x ∈ [..]` at return) with no live
//     dependence on other variables, so no dependent-fact cascade is needed.
//   - `ensures` law args are validated compile-time constants (resolveRefinementEnsures), so no
//     caller-substitution is required; a non-decidable law simply seeds nothing.
//   - Like all refinement value facts it only NARROWS (intersect) and never drives bounds-check elision,
//     so even a buggy callee contract is garbage-in-garbage-out, never memory unsafety.
func (a *Analyzer) seedEnsuresParamRangeFacts(arg ast.Expr, lawName string, lawArgs []ast.Expr) {
	if a.currentScope == nil {
		return
	}
	name, ok := rootIdentName(arg)
	if !ok {
		return
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil || !IsNumericType(sym.Type) || IsFloatType(sym.Type) {
		return
	}
	fact, found := a.rangeFromLawApplication(lawName, lawArgs, nil)
	if !found {
		return
	}
	if a.currentScope.rangeFacts == nil {
		a.currentScope.rangeFacts = map[string]numRange{}
	}
	a.currentScope.rangeFacts[name] = a.currentScope.rangeFacts[name].intersect(fact)
}

// rangeFromEnsureResult computes the integer interval that a callee's value-contract postconditions
// place on its returned value (docs/90 brick 90-10). Each `ensure` clause is an independent boolean;
// this collects every `result OP operand` comparison appearing in conjunction position across all
// clauses (a clause or sub-term outside the fragment is simply skipped — unlike a law body, partial
// information is sound here because the clauses are independently true). The operand is resolved with
// the same caller-substitution + direction-aware bounding used for refinement bracket args:
// a constant after substitution, or a known interval (`>=`/`>` uses its lower bound, `<=`/`<` its
// upper). `result` is the subject keyword bound in analyzeEnsureClauses.
func (a *Analyzer) rangeFromEnsureResult(clauses []ast.Expr, subst map[string]ast.Expr) (numRange, bool) {
	var constraints []lawConstraint
	for _, clause := range clauses {
		if clause == nil {
			continue
		}
		a.collectResultConstraints(clause, subst, &constraints)
	}
	if len(constraints) == 0 {
		return numRange{}, false
	}
	fact := numRange{}
	for _, k := range constraints {
		fact = fact.intersect(constraintToRange(k))
	}
	return fact, true
}

func (a *Analyzer) tryProveEnsureByReturnCallRange(clause ast.Expr, call *ast.CallExpr) bool {
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok || decl == nil || len(decl.EnsureValues) == 0 {
		return false
	}
	args := proofCallArgs(call)
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	resultRange, ok := a.rangeFromEnsureResult(decl.EnsureValues, subst)
	if !ok {
		return false
	}
	var constraints []lawConstraint
	a.collectResultConstraints(clause, nil, &constraints)
	if len(constraints) == 0 {
		return false
	}
	for _, constraint := range constraints {
		if !rangeEntailsConstraint(resultRange, constraint) {
			return false
		}
	}
	return true
}

func (a *Analyzer) proofCallExpr(expr ast.Expr) (*ast.CallExpr, bool) {
	expr = stripOptimizationParens(expr)
	switch n := expr.(type) {
	case *ast.CallExpr:
		return n, n != nil
	case *ast.StructLitExpr:
		if a != nil && a.loweredInitCalls != nil {
			if call := a.loweredInitCalls[n]; call != nil {
				return call, true
			}
		}
	}
	return nil, false
}

func proofCallArgs(call *ast.CallExpr) []ast.Expr {
	if call == nil {
		return nil
	}
	if call.ResolvedArgsValid && call.ResolvedCommonArgs == nil {
		return call.ResolvedArgs
	}
	return call.Args
}

// collectResultConstraints gathers `result OP operand` comparisons from an `ensure` clause, recursing
// through parentheses and `and`. Unlike collectLawConstraints it never fails: a comparison it cannot
// resolve (operand not constant/ranged, or not about `result`) is skipped, since each conjunct that
// IS resolvable is an independent sound fact about the result.
func (a *Analyzer) collectResultConstraints(expr ast.Expr, subst map[string]ast.Expr, out *[]lawConstraint) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		a.collectResultConstraints(n.Inner, subst, out)
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			a.collectResultConstraints(n.Left, subst, out)
			a.collectResultConstraints(n.Right, subst, out)
			return
		}
		// Normalize to `result OP operand`.
		var operand ast.Expr
		var op lexer.TokenKind
		switch {
		case isSelfIdent(n.Left, "result"):
			operand, op = n.Right, n.Op
		case isSelfIdent(n.Right, "result"):
			operand, op = n.Left, flipComparison(n.Op)
		default:
			return
		}
		switch op {
		case lexer.TOKEN_GT, lexer.TOKEN_GTEQ, lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_EQEQ:
		default:
			return // `!=` gives no interval bound; skip
		}
		if c, ok := a.substConstInt(operand, subst); ok {
			*out = append(*out, lawConstraint{op: op, c: c})
			return
		}
		// Ranged operand (`ensure result <= n` with n ∈ [.., hi]): direction-aware, like bracket args.
		if r, ok := a.substArgRange(operand, subst); ok {
			switch op {
			case lexer.TOKEN_GTEQ, lexer.TOKEN_GT:
				if r.loKnown {
					*out = append(*out, lawConstraint{op: op, c: r.lo})
				}
			case lexer.TOKEN_LTEQ, lexer.TOKEN_LT:
				if r.hiKnown {
					*out = append(*out, lawConstraint{op: op, c: r.hi})
				}
			}
		}
	}
}

// --- tier-2: bounded linear arithmetic (docs/86) ---------------------------------------------
//
// The tier-1 flow prover (tryProveRefinementByFlow) discharges an obligation only when the subject
// is a BARE immutable integer identifier with a range fact. Tier-2 generalizes the subject to an
// affine form `c0 + sum(ci*xi)` over immutable integer variables, bounds it by interval arithmetic
// over the same range facts, and checks the result entails the law's `self OP const` constraints. It
// is the only tier that can prove a DERIVED index such as `tx*MAPHEIGHT + ty is Bounded[0..<4096]`
// (docs/85 §3 tier 2). The law side is reused verbatim (lawConstraints); only the subject is richer.

// affineForm is c0 + sum over terms of (coeff * variable), all integer. An empty terms map with a
// nonzero const is a literal; a single {x:1} term is a bare variable. Coefficients are exact int64.
type affineForm struct {
	c     int64
	terms map[string]int64
}

func (f affineForm) addTerm(name string, coeff int64) {
	if coeff == 0 {
		return
	}
	f.terms[name] += coeff
	if f.terms[name] == 0 {
		delete(f.terms, name)
	}
}

// affineOf builds the affine form of an integer expression, or returns ok=false when the expression
// leaves the linear-arithmetic fragment (non-linear product, unknown leaf, value-changing cast).
// Only IMMUTABLE integer identifiers are admitted as variables, so a mutable binding can never enter
// a form — the dependence-freeze (docs/85 §5.3) holds for free, same gate tier-1 uses.
func (a *Analyzer) affineOf(expr ast.Expr, scope *Scope) (affineForm, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.affineOf(n.Inner, scope)
	case *ast.IntLit:
		if c, ok := a.constIntValue(n); ok {
			return affineForm{c: c, terms: map[string]int64{}}, true
		}
		return affineForm{}, false
	case *ast.Ident:
		// A const-evaluable identifier (e.g. a module const like MAPHEIGHT) folds to its value.
		if c, ok := a.constIntValue(n); ok {
			return affineForm{c: c, terms: map[string]int64{}}, true
		}
		if name, ok := immutableIntIdentName(a, scope, n); ok {
			return affineForm{c: 0, terms: map[string]int64{name: 1}}, true
		}
		return affineForm{}, false
	case *ast.UnaryExpr:
		if n.Op != lexer.TOKEN_MINUS {
			return affineForm{}, false
		}
		inner, ok := a.affineOf(n.Operand, scope)
		if !ok {
			return affineForm{}, false
		}
		out := affineForm{c: -inner.c, terms: map[string]int64{}}
		for k, v := range inner.terms {
			out.terms[k] = -v
		}
		return out, true
	case *ast.BinaryExpr:
		return a.affineOfBinary(n, scope)
	case *ast.CastExpr:
		// A numeric-to-integer cast is value-preserving only when the target type can represent the
		// subject's whole proven range; a narrowing cast wraps and would make the bound unsound.
		inner, ok := a.affineOf(n.Operand, scope)
		if !ok {
			return affineForm{}, false
		}
		target := a.resolveType(n.Target)
		if _, _, isInt := BitIntInfo(target); !isInt {
			return affineForm{}, false
		}
		r := a.boundAffine(inner, scope)
		if !r.loKnown || !r.hiKnown {
			return affineForm{}, false // unbounded subject: cannot prove the cast is value-preserving
		}
		if !IntegerTypeFitsValue(target, r.lo) || !IntegerTypeFitsValue(target, r.hi) {
			return affineForm{}, false // narrowing/wrapping cast: bound would be unsound
		}
		return inner, true
	case *ast.FieldExpr:
		// `v.size` where `v` is a local known to hold a struct literal with a constant `size` field.
		base, ok := n.Object.(*ast.Ident)
		if !ok || base == nil {
			return affineForm{}, false
		}
		// `xs.count` where `xs` is an immutable darray local initialised from a list literal.
		if n.Field == "count" {
			if cnt, ok2 := a.lookupWrittenListCount(base.Name); ok2 {
				return affineForm{c: cnt, terms: map[string]int64{}}, true
			}
		}
		fieldVal, ok2 := a.lookupWrittenStructField(base.Name, n.Field)
		if !ok2 {
			// Fall back to the written-field channel: a direct `s.f <- <const>` assignment on a straight-line
			// path records a compile-time constant in writtenField keyed by smtProjectionName(s.f). Consult
			// that fact here so `s.f <- 5; check(s.f)` proves `requires x >= 5` from the written constant.
			// Sound: writtenField is invalidated on every write to s.f, to s, or on a mutable-ref escape of s
			// (see recordWrittenFieldForTarget / invalidateWrittenFieldsForRoot).
			key := smtProjectionName(n)
			if isPureProjectionKey(key) {
				if wfVal, ok3 := a.lookupWrittenField(key); ok3 && wfVal != nil {
					fieldVal, ok2 = wfVal, true
				}
			}
		}
		if !ok2 {
			return affineForm{}, false
		}
		return a.affineOf(fieldVal, scope)
	default:
		return affineForm{}, false
	}
}

func (a *Analyzer) affineOfBinary(n *ast.BinaryExpr, scope *Scope) (affineForm, bool) {
	switch n.Op {
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS:
		l, ok := a.affineOf(n.Left, scope)
		if !ok {
			return affineForm{}, false
		}
		r, ok := a.affineOf(n.Right, scope)
		if !ok {
			return affineForm{}, false
		}
		sign := int64(1)
		if n.Op == lexer.TOKEN_MINUS {
			sign = -1
		}
		out := affineForm{c: l.c + sign*r.c, terms: map[string]int64{}}
		for k, v := range l.terms {
			out.addTerm(k, v)
		}
		for k, v := range r.terms {
			out.addTerm(k, sign*v)
		}
		return out, true
	case lexer.TOKEN_STAR:
		// Linear only when at least one side is a compile-time constant.
		if c, ok := a.constIntValue(n.Right); ok {
			return a.scaleAffine(n.Left, c, scope)
		}
		if c, ok := a.constIntValue(n.Left); ok {
			return a.scaleAffine(n.Right, c, scope)
		}
		return affineForm{}, false // variable*variable: non-linear, decline (sound)
	default:
		return affineForm{}, false
	}
}

func (a *Analyzer) scaleAffine(expr ast.Expr, k int64, scope *Scope) (affineForm, bool) {
	inner, ok := a.affineOf(expr, scope)
	if !ok {
		return affineForm{}, false
	}
	out := affineForm{c: inner.c * k, terms: map[string]int64{}}
	for name, v := range inner.terms {
		out.addTerm(name, v*k)
	}
	return out, true
}

// boundAffine interval-evaluates an affine form by substituting each variable's known range. A
// nonnegative coefficient keeps the bound orientation; a negative one swaps lo/hi. A variable with
// no range fact (or an open bound on the needed side) makes that side of the result open, so a later
// entailment on that side fails and the prover declines (fail-closed, docs/85 §9.2).
func (a *Analyzer) boundAffine(f affineForm, scope *Scope) numRange {
	out := numRange{loKnown: true, lo: f.c, hiKnown: true, hi: f.c}
	for name, coeff := range f.terms {
		r, ok := a.lookupRangeFact(name)
		if !ok {
			// No branch-derived range, but a live written-constant fact (e.g. an immutable local
			// `k: i32 = 5`) pins the variable to an exact value — a tight point range. Sound: the
			// written-const fact is a proven exact value, invalidated on any mutation.
			if c, known := a.writtenConstInt(name); known {
				r, ok = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}, true
			}
		}
		if !ok {
			if sym, found := a.currentScope.Lookup(name); found && sym != nil && smtTypeNonNegative(sym.Type) {
				r, ok = numRange{loKnown: true, lo: 0}, true
			}
		}
		if !ok {
			return numRange{} // unknown variable: fully open, declines
		}
		var lo, hi int64
		var loK, hiK bool
		if coeff >= 0 {
			lo, loK = r.lo, r.loKnown
			hi, hiK = r.hi, r.hiKnown
		} else {
			lo, loK = r.hi, r.hiKnown
			hi, hiK = r.lo, r.loKnown
		}
		// SOUNDNESS: do the interval arithmetic with int64-OVERFLOW detection. A wide product such as a
		// u32 `a*3000000000` with a in [0, 4e9] computes `3e9 * 4e9 = 1.2e19`, which overflows int64 and
		// wraps to a small/negative value — making the interval look in-range so provablyNoArithWrap
		// wrongly skips the wrap model and proves a false `result >= a`. On any overflow the bound becomes
		// unknown (open), which only declines a proof, never admits an unsound one (audit cluster E).
		if out.loKnown && loK {
			if p, ok := mulInt64Checked(coeff, lo); ok {
				if s, ok := addInt64Checked(out.lo, p); ok {
					out.lo = s
				} else {
					out.loKnown = false
				}
			} else {
				out.loKnown = false
			}
		} else {
			out.loKnown = false
		}
		if out.hiKnown && hiK {
			if p, ok := mulInt64Checked(coeff, hi); ok {
				if s, ok := addInt64Checked(out.hi, p); ok {
					out.hi = s
				} else {
					out.hiKnown = false
				}
			} else {
				out.hiKnown = false
			}
		} else {
			out.hiKnown = false
		}
	}
	return out
}

// mulInt64Checked multiplies with overflow detection (ok=false on overflow).
// Special-cases: minInt64 * -1 overflows (result would be maxInt64+1) but the division
// check p/b==a wraps back to minInt64 in two's complement and gives a false "ok" result,
// so we guard that explicitly.
func mulInt64Checked(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	const minI64 = -int64(^uint64(0)>>1) - 1
	// minInt64 * -1 would be maxInt64+1 which does not fit in int64.
	if (a == minI64 && b == -1) || (b == minI64 && a == -1) {
		return 0, false
	}
	p := a * b
	if p/b != a {
		return 0, false
	}
	return p, true
}

// addInt64Checked adds with overflow detection (ok=false on overflow).
func addInt64Checked(a, b int64) (int64, bool) {
	s := a + b
	if (s > a) != (b > 0) {
		return 0, false
	}
	return s, true
}

// shiftRange shifts a known integer range [lo, hi] by a constant c, producing [lo+c, hi+c].
// A bound is preserved only when the addition does not overflow int64; an overflowing bound is
// dropped (open), so the result is always sound — never wider than the true shifted interval.
// Used by tryProveRefinementByFlow to handle additive-shift subjects `x + c`.
func shiftRange(r numRange, c int64) (numRange, bool) {
	out := numRange{}
	anyKnown := false
	if r.loKnown {
		if s, ok := addInt64Checked(r.lo, c); ok {
			out.loKnown, out.lo = true, s
			anyKnown = true
		}
	}
	if r.hiKnown {
		if s, ok := addInt64Checked(r.hi, c); ok {
			out.hiKnown, out.hi = true, s
			anyKnown = true
		}
	}
	return out, anyKnown
}

// scaleRangePositive scales a known integer range [lo, hi] (with lo >= 0) by a positive constant k > 0,
// producing [lo*k, hi*k]. Guard: lo must be >= 0 (monotonic scaling only applies to non-negative ranges),
// and k must be > 0. Either bound that would overflow int64 is dropped (open), keeping the result sound.
// Used by tryProveRefinementByFlow to handle scaling subjects `x * k` when x >= 0.
func scaleRangePositive(r numRange, k int64) (numRange, bool) {
	if k <= 0 {
		return numRange{}, false
	}
	if !r.loKnown || r.lo < 0 {
		return numRange{}, false // monotonic scaling requires a known non-negative lower bound
	}
	out := numRange{}
	anyKnown := false
	if lo, ok := mulInt64Checked(r.lo, k); ok {
		out.loKnown, out.lo = true, lo
		anyKnown = true
	}
	if r.hiKnown {
		if hi, ok := mulInt64Checked(r.hi, k); ok {
			out.hiKnown, out.hi = true, hi
			anyKnown = true
		}
	}
	return out, anyKnown
}

// scaleRangeNegative scales a known integer range [lo, hi] by a negative constant k < 0,
// producing [hi*k, lo*k]. Multiplication by a negative number reverses order: the new lower
// bound is the old hi scaled by k, and the new upper bound is the old lo scaled by k.
// Either bound that would overflow int64 is dropped (open), keeping the result sound (never
// asserts a tighter bound than the true interval). k must be < 0.
// Used by tryDeriveShiftOrScaleRange to handle scaling subjects `x * k` when k < 0.
func scaleRangeNegative(r numRange, k int64) (numRange, bool) {
	if k >= 0 {
		return numRange{}, false
	}
	out := numRange{}
	anyKnown := false
	// new lo = hi * k  (hi is the largest; multiplied by negative k gives the smallest product)
	if r.hiKnown {
		if lo, ok := mulInt64Checked(r.hi, k); ok {
			out.loKnown, out.lo = true, lo
			anyKnown = true
		}
	}
	// new hi = lo * k  (lo is the smallest; multiplied by negative k gives the largest product)
	if r.loKnown {
		if hi, ok := mulInt64Checked(r.lo, k); ok {
			out.hiKnown, out.hi = true, hi
			anyKnown = true
		}
	}
	return out, anyKnown
}

// tryProveRefinementByLinear discharges `value is law[args]` when `value` is an affine form over
// immutable integer variables whose bounded range entails every law constraint. Reuses lawConstraints
// (the law side is unchanged) and rangeEntailsConstraint (the entailment check). Sound: any leaf
// outside the fragment, or any open bound on a needed side, makes it decline to a runtime check.
func (a *Analyzer) tryProveRefinementByLinear(value ast.Expr, decl *ast.FuncDecl, predArgs []ast.Expr, scope *Scope) bool {
	// A bare identifier is already tier-1's job; tier-2 only earns its keep on derived forms.
	if _, isIdent := value.(*ast.Ident); isIdent {
		return false
	}
	if decl == nil || len(predArgs) != len(decl.Params)-1 {
		return false
	}
	form, ok := a.affineOf(value, scope)
	if !ok {
		return false
	}
	// A constant-only form (no variable terms) is const-eval's job, not tier-2's — declining keeps
	// the discharge recorded as proven (const) and tier-2 scoped to genuinely derived subjects.
	if len(form.terms) == 0 {
		return false
	}
	paramConsts := map[string]int64{}
	for i, arg := range predArgs {
		c, ok := a.constIntValue(arg)
		if !ok {
			return false
		}
		paramConsts[decl.Params[i+1].Name] = c
	}
	constraints, ok := a.lawConstraints(decl, paramConsts)
	if !ok || len(constraints) == 0 {
		return false
	}
	r := a.boundAffine(form, scope)
	for _, k := range constraints {
		if !rangeEntailsConstraint(r, k) {
			return false
		}
	}
	return true
}

// tryProveRefinementByFlow attempts a flow-sensitive static proof of `value is law`: when `value`
// is a bare immutable integer identifier with a known range fact, and the law body is a decidable
// conjunction of `self OP const` constraints, it checks the range entails every constraint. Returns
// true only on a sound proof (docs/85 1d-2).
//
// Strengthened fallbacks (sound, narrowly scoped):
//  1. Written-constant fact: if no rangeFact exists but the variable is pinned to an exact compile-time
//     constant by a live written-const fact (an immutable local assigned a literal), we construct the
//     degenerate interval [c,c] and check against it. This is the equality-fact → comparison case:
//     a variable known to be exactly 5 satisfies any comparison or interval containing 5.
//  2. Declared-type non-negativity: if no rangeFact or written-const is available, but the variable's
//     declared type is unsigned (e.g. u8, u16, u32, u64, usize), we seed a [0, typeMax] range from
//     the declared type — which already provides non-negativity. This is sound because unsigned types
//     cannot hold negative values; the type system enforces the lower bound at construction time.
//  3. Additive shift: if `value` is `x + c` (or `x - c`) for an immutable integer `x` with a known
//     range [lo, hi] and constant c, the shifted range [lo+c, hi+c] is derived (shiftRange) and
//     checked against the law constraints. Overflow-safe: either bound that would overflow int64 is
//     dropped (open), and the check then fails (sound). Integers only.
//  4. Monotonic scaling: if `value` is `x * k` for an immutable integer `x` with known range
//     [lo, hi] where lo >= 0, and constant k > 0, the scaled range [lo*k, hi*k] is derived
//     (scaleRangePositive) and checked. Overflow-safe: overflowing bounds become open. Integers only.
//  4a. Negating scale: if `value` is `x * k` with k < 0, the range flips to [hi*k, lo*k]
//     (scaleRangeNegative). Overflow-safe: overflowing bounds become open. Integers only.
//  4b. Unary negation: if `value` is `-x`, treated as x * -1 via scaleRangeNegative, yielding
//     range [-hi, -lo]. Overflow-safe: overflowing bounds become open. Integers only.
func (a *Analyzer) tryProveRefinementByFlow(value ast.Expr, decl *ast.FuncDecl, predArgs []ast.Expr) bool {
	ident, ok := value.(*ast.Ident)
	if !ok || ident == nil {
		// Field-path case: `s.n` where a guard established a range fact keyed on "s.n".
		if fe, isFE := value.(*ast.FieldExpr); isFE && fe != nil && a.currentScope != nil {
			if pathKey, pkOk := fieldPathKey(a, a.currentScope, fe); pkOk {
				if r, rOk := a.lookupRangeFact(pathKey); rOk {
					// Also intersect with the declared type bounds (unsigned non-negativity, etc.),
					// mirroring the bare-ident path below.
					if ft := a.exprTypes[value]; ft != nil {
						if tr, trOk := declaredTypeRange(ft); trOk {
							r = r.intersect(tr)
						}
					}
					return a.proveConstraintsFromRange(r, decl, predArgs)
				}
			}
		}
		// Cases 3 & 4: additive shift or monotonic scaling of an immutable integer variable.
		if r, derived := a.tryDeriveShiftOrScaleRange(value); derived {
			return a.proveConstraintsFromRange(r, decl, predArgs)
		}
		// Case 5: ternary/if-expression — join the branch ranges and check the union.
		if tern, isTern := value.(*ast.TernaryExpr); isTern {
			if r, derived := a.tryDeriveTernaryRange(tern); derived {
				return a.proveConstraintsFromRange(r, decl, predArgs)
			}
		}
		return false
	}
	r, ok := a.lookupRangeFact(ident.Name)
	if !ok {
		// Fallback 1: written-constant fact → degenerate [c,c] range.
		if c, known := a.writtenConstInt(ident.Name); known {
			r = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}
			ok = true
		}
	}
	// Declared-type bounds (e.g. unsigned non-negativity) always apply and only tighten the range,
	// so merge them in whether or not a range fact already exists. A guard/assert like `x <= 103`
	// records hi=103 but leaves the lower bound open; for an unsigned `x` the declared type supplies
	// lo=0, which the law's `self >= 0` conjunct needs. Without this merge, `if x <= 103: return x`
	// fails to discharge `is InRange[0, 103]` because non-negativity was only consulted as a fallback
	// for a missing range fact (Fallback 2), never combined with a present one.
	if sym, found := a.currentScope.Lookup(ident.Name); found && sym != nil {
		if tr, found := declaredTypeRange(sym.Type); found {
			if ok {
				r = r.intersect(tr)
			} else {
				r = tr
				ok = true
			}
		}
	}
	if !ok {
		// Fallback 3 — in-body monotonic counter bound: for a MUTABLE integer identifier, harvest
		// constant-comparison facts from live smtAssertFacts. When the analyzer enters
		// `while i < n:` (or any branch), it seeds the raw condition as an smtAssertFact in the
		// body scope, and drops that fact whenever `i` is mutated
		// (invalidateSMTAssertFactsForTarget). Reading those facts here is therefore as sound as
		// reading rangeFacts for an immutable identifier: the invalidation guarantee is the same.
		// This lets the tier-1 flow prover handle simple laws like `i is Nat` inside
		// `while i >= 0:`, or `i is Bounded[0..=9]` inside `while i < 10:`, without SMT.
		if sym, found := a.currentScope.Lookup(ident.Name); found && sym != nil &&
			sym.Mutable && IsNumericType(sym.Type) && !IsFloatType(sym.Type) {
			if mr, mok := a.rangeFromSMTAssertFacts(ident.Name); mok {
				if tr, found := declaredTypeRange(sym.Type); found {
					mr = mr.intersect(tr)
				}
				return a.proveConstraintsFromRange(mr, decl, predArgs)
			}
		}
		return false
	}
	return a.proveConstraintsFromRange(r, decl, predArgs)
}

// rangeFromSMTAssertFacts scans the live smtAssertFacts in the current scope chain for
// constant-comparison facts about a named variable and returns their conjunction as a numRange.
// Used by tryProveRefinementByFlow Fallback 3 to derive a provable range for MUTABLE integer
// identifiers that are excluded from the immutable-only rangeFacts map. Soundness holds because
// smtAssertFacts is invalidated at every mutation of the variable — the same guarantee that
// makes rangeFacts sound for immutables (docs/85 §5.3 monotonic counter bound).
// Only constant-RHS (or constant-LHS) comparisons contribute; variable-bound facts are skipped.
func (a *Analyzer) rangeFromSMTAssertFacts(name string) (numRange, bool) {
	r := numRange{}
	found := false
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for _, fact := range sc.smtAssertFacts {
			bin, ok := stripOptimizationParens(fact.Expr).(*ast.BinaryExpr)
			if !ok || bin == nil {
				continue
			}
			op := bin.Op
			var c int64
			var cok bool
			leftIsName := isIdentNamed(bin.Left, name)
			if leftIsName {
				// name OP const
				c, cok = a.constIntValue(bin.Right)
			} else if isIdentNamed(bin.Right, name) {
				// const OP name — flip operator to normalize to name flipOp const
				c, cok = a.constIntValue(bin.Left)
				op = flipComparison(op)
			} else {
				continue
			}
			if !cok {
				continue
			}
			var fact numRange
			switch op {
			case lexer.TOKEN_GT:
				fact = numRange{loKnown: true, lo: c + 1}
			case lexer.TOKEN_GTEQ:
				fact = numRange{loKnown: true, lo: c}
			case lexer.TOKEN_LT:
				fact = numRange{hiKnown: true, hi: c - 1}
			case lexer.TOKEN_LTEQ:
				fact = numRange{hiKnown: true, hi: c}
			case lexer.TOKEN_EQEQ:
				fact = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}
			default:
				continue
			}
			r = r.intersect(fact)
			found = true
		}
	}
	return r, found
}

// proveConstraintsFromRange checks whether the given known range entails all law constraints.
// Shared by tryProveRefinementByFlow (bare identifier and derived-range cases).
func (a *Analyzer) proveConstraintsFromRange(r numRange, decl *ast.FuncDecl, predArgs []ast.Expr) bool {
	if decl == nil || len(predArgs) != len(decl.Params)-1 {
		return false
	}
	paramConsts := map[string]int64{}
	for i, arg := range predArgs {
		c, ok := a.constIntValue(arg)
		if !ok {
			return false // a non-constant bracket arg is not statically interpretable
		}
		paramConsts[decl.Params[i+1].Name] = c
	}
	constraints, ok := a.lawConstraints(decl, paramConsts)
	if !ok || len(constraints) == 0 {
		return false
	}
	for _, k := range constraints {
		if !rangeEntailsConstraint(r, k) {
			return false
		}
	}
	return true
}

// tryProveRefinementByRelational discharges `subject is law[args]` when one or more bracket args are
// non-constant immutable-integer identifiers whose relationship to the subject is directly stated by
// a `requires` clause of the enclosing function (docs/85 dependent-arg extension).
//
// Example: `raw is InRange[0, cap]` with law `self >= 0 and self < cap` proves when:
//   - `raw` is an immutable integer param of unsigned type (giving self >= 0 via declared-type range), AND
//   - `requires raw < cap` is a precondition of the enclosing function (giving self < cap relationally).
//
// Sound design: BOTH the subject and every non-const bracket arg must be immutable integer
// identifiers (sym.Mutable == false). Immutable params cannot be reassigned, so the `requires` fact
// holds throughout the entire body — no mutation-invalidation tracking is needed. Any mutable
// subject or mutable bracket arg causes an immediate decline. A constant bracket arg is resolved
// through the normal paramConsts path; only non-const bracket args use the relational channel.
func (a *Analyzer) tryProveRefinementByRelational(value ast.Expr, decl *ast.FuncDecl, predArgs []ast.Expr) bool {
	if a == nil || decl == nil || a.currentFuncDecl == nil || a.currentScope == nil {
		return false
	}
	if len(predArgs) != len(decl.Params)-1 {
		return false
	}
	// Subject must be an immutable integer identifier.
	subjectName, subjectOk := immutableIntIdentName(a, a.currentScope, value)
	if !subjectOk || subjectName == "" {
		return false
	}
	// Partition bracket args into constants and relational (immutable-ident) entries.
	paramConsts := map[string]int64{}
	paramRelNames := map[string]string{} // law-param-name → caller-side variable name
	anyRelational := false
	for i, arg := range predArgs {
		lawParamName := decl.Params[i+1].Name
		if c, cok := a.constIntValue(arg); cok {
			paramConsts[lawParamName] = c
			continue
		}
		argName, argOk := immutableIntIdentName(a, a.currentScope, arg)
		if !argOk || argName == "" {
			return false // outside the relational fragment — decline soundly
		}
		paramRelNames[lawParamName] = argName
		anyRelational = true
	}
	if !anyRelational {
		// All args were constant: the existing proveConstraintsFromRange handles this.
		return false
	}
	// Build the subject's range from live range facts and declared-type bounds (for channel A).
	// We use smtIntWidthSign (rather than BitIntInfo/declaredTypeRange) so that wide unsigned types
	// such as usize/uintptr (64-bit, excluded by BitIntInfo's width>=63 guard) also contribute their
	// non-negativity lower bound.
	subjectRange := numRange{}
	subjectRangeOk := false
	if r, found := a.lookupRangeFact(subjectName); found {
		subjectRange = r
		subjectRangeOk = true
	}
	if sym, found := a.currentScope.Lookup(subjectName); found && sym != nil {
		// First, try declaredTypeRange for exact-width types (u8/u16/u32, i8/…).
		if tr, found := declaredTypeRange(sym.Type); found {
			if subjectRangeOk {
				subjectRange = subjectRange.intersect(tr)
			} else {
				subjectRange = tr
				subjectRangeOk = true
			}
		} else if signed, _, ok := smtIntWidthSign(sym.Type); ok && !signed {
			// Wide unsigned (usize/uintptr/u64): declaredTypeRange declined (width>=63), but we
			// can still supply the non-negativity lower bound lo=0 soundly. The upper bound is
			// left open (unknown) — we only assert what we know for certain.
			unsignedNonneg := numRange{loKnown: true, lo: 0}
			if subjectRangeOk {
				subjectRange = subjectRange.intersect(unsignedNonneg)
			} else {
				subjectRange = unsignedNonneg
				subjectRangeOk = true
			}
		}
	}
	// Walk the law body and prove each atom via either:
	//   A) constant operand (paramConsts or literal) → range entailment on subjectRange
	//   B) relational operand (paramRelNames → callerVarName) → live requires clause
	return a.proveRelationalLawBody(decl, subjectName, paramConsts, paramRelNames, subjectRange, subjectRangeOk)
}

// proveRelationalLawBody extracts the single-return law body and walks it as a conjunction.
func (a *Analyzer) proveRelationalLawBody(decl *ast.FuncDecl, subjectName string, paramConsts map[string]int64, paramRelNames map[string]string, subjectRange numRange, subjectRangeOk bool) bool {
	if decl == nil || len(decl.Params) == 0 || len(decl.Body) != 1 {
		return false
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok || ret == nil || ret.Value == nil {
		return false
	}
	self := decl.Params[0].Name
	return a.proveRelationalLawExpr(ret.Value, self, subjectName, paramConsts, paramRelNames, subjectRange, subjectRangeOk)
}

// proveRelationalLawExpr proves a single law body expression using the mixed const+relational
// strategy. Returns false (decline) if any atom is undecidable or unprovable.
func (a *Analyzer) proveRelationalLawExpr(expr ast.Expr, self string, subjectName string, paramConsts map[string]int64, paramRelNames map[string]string, subjectRange numRange, subjectRangeOk bool) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.proveRelationalLawExpr(n.Inner, self, subjectName, paramConsts, paramRelNames, subjectRange, subjectRangeOk)
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			return a.proveRelationalLawExpr(n.Left, self, subjectName, paramConsts, paramRelNames, subjectRange, subjectRangeOk) &&
				a.proveRelationalLawExpr(n.Right, self, subjectName, paramConsts, paramRelNames, subjectRange, subjectRangeOk)
		}
		// Normalize to `self OP operand`.
		var operand ast.Expr
		var op lexer.TokenKind
		switch {
		case isSelfIdent(n.Left, self):
			operand, op = n.Right, n.Op
		case isSelfIdent(n.Right, self):
			operand, op = n.Left, flipComparison(n.Op)
		default:
			return false // non-self atom — undecidable in this tier, decline
		}
		// Channel A: operand resolves to a constant (literal or paramConsts).
		if c, cok := a.operandConst(operand, paramConsts); cok {
			if !subjectRangeOk {
				return false
			}
			return rangeEntailsConstraint(subjectRange, lawConstraint{op: op, c: c})
		}
		// Channel B: operand is a law param bound to a caller-side variable via paramRelNames.
		opIdent, identOk := operand.(*ast.Ident)
		if !identOk || opIdent == nil {
			return false
		}
		callerVarName, relOk := paramRelNames[opIdent.Name]
		if !relOk {
			return false // operand is not a known law param — undecidable, decline
		}
		return a.requiresClauseProves(subjectName, op, callerVarName)
	default:
		return false
	}
}

// requiresClauseProves reports whether the enclosing function's `requires` clauses contain a
// precondition that entails `subjectName OP argName`. Both variables must be immutable integer
// identifiers (enforced by the caller); since they cannot be reassigned, the requires fact is valid
// throughout the entire body without mutation-invalidation tracking.
//
// Sound: we only conclude when the requires clause's normalized operator is at least as strong as
// the wanted operator (see relationalOpProves). We scan ALL requires clauses and return true on the
// first match; if none match, we decline (return false).
func (a *Analyzer) requiresClauseProves(subjectName string, op lexer.TokenKind, argName string) bool {
	if a == nil || a.currentFuncDecl == nil || subjectName == "" || argName == "" {
		return false
	}
	for _, req := range a.currentFuncDecl.Requires {
		if a.requiresClauseMatchesRelational(req, subjectName, op, argName) {
			return true
		}
	}
	return false
}

// requiresClauseMatchesRelational reports whether the clause `expr` normalizes to a fact
// `subjectName clauseOp argName` where clauseOp proves wantOp (see relationalOpProves).
// The normalization flips the operator when identifiers appear in reversed order.
func (a *Analyzer) requiresClauseMatchesRelational(expr ast.Expr, subjectName string, wantOp lexer.TokenKind, argName string) bool {
	bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr)
	if !ok || bin == nil {
		return false
	}
	var clauseOp lexer.TokenKind
	switch {
	case isIdentNamed(bin.Left, subjectName) && isIdentNamed(bin.Right, argName):
		clauseOp = bin.Op
	case isIdentNamed(bin.Right, subjectName) && isIdentNamed(bin.Left, argName):
		clauseOp = flipComparison(bin.Op)
	default:
		return false
	}
	return relationalOpProves(clauseOp, wantOp)
}

// relationalOpProves reports whether having a `requires` clause with operator `have` (in normalized
// `subject OP arg` form) is sufficient to prove a law constraint with operator `want`.
//
// Sound entailment rules:
//
//	have=LT  proves want=LT (exact) and want=LTEQ  (x<y ⇒ x≤y, integer semantics)
//	have=LTEQ proves want=LTEQ only
//	have=GT  proves want=GT and want=GTEQ
//	have=GTEQ proves want=GTEQ only
//	have=EQEQ proves want=EQEQ, LTEQ, GTEQ
//	anything else: decline
func relationalOpProves(have, want lexer.TokenKind) bool {
	if have == want {
		return true
	}
	switch have {
	case lexer.TOKEN_LT:
		return want == lexer.TOKEN_LTEQ
	case lexer.TOKEN_GT:
		return want == lexer.TOKEN_GTEQ
	case lexer.TOKEN_EQEQ:
		return want == lexer.TOKEN_LTEQ || want == lexer.TOKEN_GTEQ
	}
	return false
}

// tryDeriveShiftOrScaleRange attempts to derive a numRange for a compound expression that is either:
//   - an additive shift `x + c` or `x - c` (case 3): range [lo+c, hi+c] derived via shiftRange.
//   - a monotonic scaling `x * k` with k > 0 and x >= 0 (case 4): range [lo*k, hi*k] via scaleRangePositive.
//   - a negating scale `x * k` with k < 0 (case 4a): range [hi*k, lo*k] via scaleRangeNegative.
//   - a unary negation `-x` (case 4b): range [-hi, -lo] (equivalent to x * -1 via scaleRangeNegative).
//
// `x` must be an immutable integer identifier with a known range fact (or written-const/declared-type
// fallback). Returns ok=false for anything outside these forms, or when overflow would occur on both
// bounds, keeping the result sound (abstain rather than approximate unsoundly).
func (a *Analyzer) tryDeriveShiftOrScaleRange(value ast.Expr) (numRange, bool) {
	stripped := stripOptimizationParens(value)

	// Case 4b: unary negation `-x` — treat as x * -1.
	if unary, ok := stripped.(*ast.UnaryExpr); ok && unary != nil && unary.Op == lexer.TOKEN_MINUS {
		name, xOk := a.immutableIntIdentNameFromScope(unary.Operand)
		if !xOk {
			return numRange{}, false
		}
		r, rOk := a.lookupRangeFact(name)
		if !rOk {
			if c, known := a.writtenConstInt(name); known {
				r, rOk = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}, true
			}
		}
		if !rOk {
			return numRange{}, false
		}
		return scaleRangeNegative(r, -1)
	}

	bin, ok := stripped.(*ast.BinaryExpr)
	if !ok || bin == nil {
		return numRange{}, false
	}
	switch bin.Op {
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS:
		// x + c  or  x - c: the constant may be on either side.
		name, xOk := a.immutableIntIdentNameFromScope(bin.Left)
		var shift int64
		if xOk {
			c, cok := a.constIntValue(bin.Right)
			if !cok {
				return numRange{}, false
			}
			if bin.Op == lexer.TOKEN_MINUS {
				c = -c
			}
			shift = c
		} else {
			name, xOk = a.immutableIntIdentNameFromScope(bin.Right)
			if !xOk || bin.Op == lexer.TOKEN_MINUS {
				// `c - x` is not a shift of x (it's a negation + shift); decline.
				return numRange{}, false
			}
			c, cok := a.constIntValue(bin.Left)
			if !cok {
				return numRange{}, false
			}
			shift = c
		}
		r, rOk := a.lookupRangeFact(name)
		if !rOk {
			if c, known := a.writtenConstInt(name); known {
				r, rOk = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}, true
			}
		}
		if !rOk {
			return numRange{}, false
		}
		return shiftRange(r, shift)
	case lexer.TOKEN_STAR:
		// x * k  or  k * x: one side must be a non-zero constant; the other an immutable int identifier.
		// For k > 0 and x >= 0: monotonic scaling via scaleRangePositive.
		// For k < 0: range-flipping via scaleRangeNegative (overflow-safe, always sound).
		name, xOk := a.immutableIntIdentNameFromScope(bin.Left)
		var k int64
		if xOk {
			c, cok := a.constIntValue(bin.Right)
			if !cok {
				return numRange{}, false
			}
			k = c
		} else {
			name, xOk = a.immutableIntIdentNameFromScope(bin.Right)
			if !xOk {
				return numRange{}, false
			}
			c, cok := a.constIntValue(bin.Left)
			if !cok {
				return numRange{}, false
			}
			k = c
		}
		if k == 0 {
			return numRange{}, false // zero scale collapses to a constant; not a range-form
		}
		r, rOk := a.lookupRangeFact(name)
		if !rOk {
			if c, known := a.writtenConstInt(name); known {
				r, rOk = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}, true
			}
		}
		if !rOk {
			return numRange{}, false
		}
		if k > 0 {
			return scaleRangePositive(r, k)
		}
		// k < 0: flip the range via scaleRangeNegative.
		return scaleRangeNegative(r, k)
	case lexer.TOKEN_PERCENT:
		// x % m  (unsigned dividend, positive constant modulus): range is [0, m-1].
		//
		// Soundness conditions (all must hold; any failure → decline, not unsound):
		//  1. Modulus `m` must be a compile-time positive constant (variable modulus: decline).
		//  2. The dividend must be unsigned (non-negative by declared type); signed remainder
		//     can be negative in C-style truncating semantics, so [0, m-1] would be unsound.
		//  3. m must be > 0 (zero modulus is UB; decline to avoid div-by-zero assumptions).
		//
		// Result: hi = m-1 (not m-2). `x % m` can equal m-1 when x ≡ m-1 (mod m), so
		// this proves `<= m-1` but NOT `< m-1` — the required soundness-negative distinction.
		m, mok := a.constIntValue(bin.Right)
		if !mok || m <= 0 {
			return numRange{}, false // non-const or non-positive modulus: decline (sound)
		}
		// Dividend must have a non-negative declared type (unsigned). We check the left
		// operand's type via the immutable-ident lookup (covers the common `x % m` form).
		// Complex sub-expressions that aren't bare identifiers are declined conservatively.
		name, xOk := a.immutableIntIdentNameFromScope(bin.Left)
		if !xOk {
			return numRange{}, false // non-identifier dividend: decline conservatively
		}
		sym, found := a.currentScope.Lookup(name)
		if !found || sym == nil || !smtTypeNonNegative(sym.Type) {
			return numRange{}, false // signed or unknown type: decline (sound)
		}
		return numRange{loKnown: true, lo: 0, hiKnown: true, hi: m - 1}, true
	default:
		return numRange{}, false
	}
}

// immutableIntIdentNameFromScope is a scope-aware wrapper for tryDeriveShiftOrScaleRange: looks up
// `expr` as an immutable integer identifier in the current scope chain.
func (a *Analyzer) immutableIntIdentNameFromScope(expr ast.Expr) (string, bool) {
	if a == nil || a.currentScope == nil {
		return "", false
	}
	return immutableIntIdentName(a, a.currentScope, expr)
}

// tryDeriveTernaryRange attempts to derive a numRange for a ternary expression `value if cond else alt`
// by computing the range of each branch in its condition-refined scope and joining them (union).
// This is the range-merge rule for conditional expressions: a value produced by a ternary is bounded
// by the union of the two branch bounds, which is sound because exactly one branch executes.
// Returns ok=false when no useful range can be derived from either branch.
func (a *Analyzer) tryDeriveTernaryRange(expr *ast.TernaryExpr) (numRange, bool) {
	if a == nil || a.currentScope == nil || expr == nil {
		return numRange{}, false
	}
	savedScope := a.currentScope

	// Compute range of the truthy branch (expr.Value) in the truthy refined scope.
	truthyScope := a.refinedScopeForCondition(savedScope, expr.Cond, true)
	a.currentScope = truthyScope
	leftRange, leftOk := a.rangeForBranchExpr(expr.Value)
	a.currentScope = savedScope

	// Compute range of the falsy branch (expr.Alt) in the falsy refined scope.
	falsyScope := a.refinedScopeForCondition(savedScope, expr.Cond, false)
	a.currentScope = falsyScope
	rightRange, rightOk := a.rangeForBranchExpr(expr.Alt)
	a.currentScope = savedScope

	if !leftOk && !rightOk {
		return numRange{}, false
	}
	if !leftOk {
		return rightRange, true
	}
	if !rightOk {
		return leftRange, true
	}
	return leftRange.join(rightRange), true
}

// rangeForBranchExpr attempts to derive a numRange for an expression appearing as a ternary branch.
// It supports:
//   - bare immutable integer identifiers (via lookupRangeFact + written-const + declared-type)
//   - additive shift / monotonic scaling (via tryDeriveShiftOrScaleRange)
//
// Called with a.currentScope already set to the condition-refined branch scope.
func (a *Analyzer) rangeForBranchExpr(expr ast.Expr) (numRange, bool) {
	if expr == nil {
		return numRange{}, false
	}
	ident, ok := expr.(*ast.Ident)
	if ok && ident != nil {
		r, found := a.lookupRangeFact(ident.Name)
		if !found {
			if c, known := a.writtenConstInt(ident.Name); known {
				r, found = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}, true
			}
		}
		if sym, symFound := a.currentScope.Lookup(ident.Name); symFound && sym != nil {
			if tr, typeFound := declaredTypeRange(sym.Type); typeFound {
				if found {
					r = r.intersect(tr)
				} else {
					r, found = tr, true
				}
			}
		}
		return r, found
	}
	// Additive shift / scaling.
	if r, derived := a.tryDeriveShiftOrScaleRange(expr); derived {
		return r, true
	}
	return numRange{}, false
}
