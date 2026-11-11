package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type recursionEdge struct {
	Caller *ast.FuncDecl
	Callee *ast.FuncDecl
	Call   *ast.CallExpr
}

// Recursive-function termination via `decreases` (docs/86 brick 86-7).
//
// Dafny's termination story: a function annotated with a `decreases <measure>` clause proves it
// terminates by showing the measure strictly decreases — and stays bounded below — at every
// recursive call. A strictly-decreasing sequence of naturals is finite, so the recursion bottoms
// out. This brick implements that for DIRECT self-recursion, reusing the bounded-linear prover
// (substitutedAffine from brick 86-5): the measure at a recursive call is the measure expression
// with the callee's parameters substituted by the call's arguments, and the obligation is that
// `measure(params) - measure(args)` is a provably-positive constant.
//
// Opt-in and additive: termination is checked ONLY when a `decreases` clause is present, so existing
// recursive code without the clause is unaffected (status quo — no termination guarantee). When the
// clause IS present it is an explicit claim, so an unprovable measure is a hard error, like a
// `requires` under -strict.
//
// Scope: direct self-recursion. Mutual recursion (a calls b calls a) and while-loop variants are
// deferred follow-ups (documented in docs/86 §9). A `decreases` measure may be a lexicographic tuple
// (multiple clauses): the obligation is satisfied at the first component that strictly decreases with
// all earlier components provably unchanged.

// checkTermination verifies the `decreases` measure of a function decreases at every direct
// self-recursive call. Called with the function's parameters in scope (after analyzeRequiresClauses).
func (a *Analyzer) checkTermination(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil {
		return
	}
	// `decreases * "reason"` — explicit opt-out: suppresses the termination obligation entirely.
	// A missing reason string is always a compile error (trust must never be silent).
	// Soundness: DecreasesWild does NOT set verifiedTerminating, so defining-equation axiomatization
	// is NOT enabled — an inconsistency (via non-termination) cannot be introduced.
	if fn.DecreasesWild != "" {
		if fn.DecreasesWild == "*" {
			// Sentinel: `decreases *` was present but no reason string was supplied.
			a.errorf(fn.Pos(), "`decreases *` on %q requires a non-empty reason string: write `decreases * \"reason\"`", fn.Name)
		}
		// Reason present (or error already reported): suppress the termination proof
		// but do NOT mark verifiedTerminating — soundness-critical.
		return
	}
	if len(fn.Decreases) == 0 {
		return
	}
	measures := decreaseMeasureComponents(fn.Decreases)
	if len(measures) == 0 {
		return
	}
	if len(measures) == 1 && a.isStructuralDecreaseMeasure(fn, measures[0]) {
		if a.checkStructuralTermination(fn, measures[0]) {
			a.recordProof(measures[0].Pos(), "structural termination of "+fn.Name, "decreases", ProofProvenLinear)
		} else {
			a.recordProof(measures[0].Pos(), "structural termination of "+fn.Name, "decreases", ProofRefuted)
			a.errorf(measures[0].Pos(), "cannot prove structural `decreases` for %q: recursive calls must pass values bound from a match on the decreasing enum parameter", fn.Name)
		}
		return
	}
	// Type-check the measure components: each must be an integer (the prover reasons over integers).
	for _, m := range measures {
		if m == nil {
			continue
		}
		t := a.analyzeExpr(m)
		if t != nil && (!IsNumericType(t) || IsFloatType(t)) {
			a.errorf(m.Pos(), "decreases measure must be an integer, got %s", t)
		}
	}
	calls := a.collectSelfRecursiveCallSites(fn)
	if len(calls) == 0 {
		edges := a.collectRecursiveSCCEdges(fn)
		if len(edges) > 0 {
			if a.mutualRecursionVerified(fn, edges) {
				if a.terminationVerified == nil {
					a.terminationVerified = map[*ast.FuncDecl]bool{}
				}
				for _, edge := range edges {
					a.terminationVerified[edge.Caller] = true
					a.terminationVerified[edge.Callee] = true
				}
				a.forgetEquationEligibility()
				a.recordProof(measures[0].Pos(), "mutual termination of "+fn.Name, "decreases", ProofProvenLinear)
			} else {
				a.recordProof(measures[0].Pos(), "mutual termination of "+fn.Name, "decreases", ProofRefuted)
				a.errorf(measures[0].Pos(), "cannot prove the `decreases` measure strictly decreases across the mutually-recursive cycle containing %q; the cycle may not terminate", fn.Name)
			}
			return
		}
		// A `decreases` on a non-self-recursive function is harmless but pointless — flag it so the
		// user knows the clause is doing nothing (and isn't masking a typo'd recursive call).
		a.warnf(measures[0].Pos(), "`decreases` on %q, which makes no direct recursive call; the termination clause is unused", fn.Name)
		return
	}
	allProven := true
	defer func() {
		if allProven {
			// Every recursive call's decrease is proven: fn is total, a property of fn itself that
			// callers' defining-equation gate may rely on (measureVerifiedForCalls).
			if a.terminationVerified == nil {
				a.terminationVerified = map[*ast.FuncDecl]bool{}
			}
			a.terminationVerified[fn] = true
			// Callers whose eligibility was decided before this proof may now qualify.
			a.forgetEquationEligibility()
		}
	}()
	for _, site := range calls {
		cert, ok := a.directNumericTerminationCertificateUnderPath(fn, site.Call, site.Path)
		if ok {
			a.recordProof(cert.pos(), "termination of "+fn.Name+" ("+cert.label()+")", "decreases", cert.Outcome)
			continue
		}
		allProven = false
		a.recordProof(cert.pos(), "termination of "+fn.Name+" ("+cert.label()+")", "decreases", ProofRefuted)
		witness := ""
		if cert.Witness != "" {
			witness = " — " + cert.Witness
		}
		a.errorf(site.Call.Pos(), "cannot prove the `decreases` measure strictly decreases at this recursive call to %q; the function may not terminate%s", fn.Name, witness)
	}
	if a.definingEquationCache != nil {
		delete(a.definingEquationCache, fn)
	}
}

// forgetEquationEligibility drops every memoized equation-eligibility verdict. Eligibility of a
// function depends on the verified termination of the functions it calls, so a verdict computed
// before one of those proofs ran may be a stale "no".
func (a *Analyzer) forgetEquationEligibility() {
	clear(a.definingEquationCache)
	clear(a.viewEquationCache)
	clear(a.aggregateEquationEligibleMemo)
}

// typeLoopSubstCastsForSMT quietly populates exprTypes for any value-form numeric CastExpr appearing
// in a loop-body substitution value, so the SMT cast translator (which keys off operand/result types)
// can model it. It analyzes only the cast subexpressions, under diagnostic suppression, on the current
// (pristine pre-loop) scope — the same scope the loop variables and measure already resolve in. It is
// purely a type-recording side effect: it never changes which substitutions exist or what they mean.
func (a *Analyzer) typeLoopSubstCastsForSMT(subst map[string]ast.Expr) {
	proofProbe := a.beginProofReportProbe()
	defer proofProbe.restore()
	if a == nil || len(subst) == 0 {
		return
	}
	saved := a.suppressDiagnostics
	a.suppressDiagnostics = true
	defer func() { a.suppressDiagnostics = saved }()
	for _, v := range subst {
		a.typeCastNodesInExpr(v)
		a.typeArithNodesForSMT(v)
	}
}

// typeArithNodesForSMT gives the identifiers and arithmetic nodes of a captured loop-body value the
// types the SMT term translator keys on (a `%` or `&` over usize needs its width), WITHOUT analyzing
// them: the body has not been analyzed yet, and a speculative analysis against the pre-loop facts
// would record optimization facts on body nodes. An identifier takes its symbol's type; an
// arithmetic node takes its typed operand's type. Only missing entries are filled, and the real body
// analysis later overwrites them.
func (a *Analyzer) typeArithNodesForSMT(expr ast.Expr) Type {
	if a == nil || expr == nil {
		return nil
	}
	if t := a.exprType(expr); t != nil {
		return t
	}
	var t Type
	switch n := expr.(type) {
	case *ast.ParenExpr:
		t = a.typeArithNodesForSMT(n.Inner)
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok && sym != nil && (sym.Kind == SymbolLocal || sym.Kind == SymbolParam) {
				t = sym.Type
			}
		}
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_MINUS {
			t = a.typeArithNodesForSMT(n.Operand)
		}
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT,
			lexer.TOKEN_AMPERSAND, lexer.TOKEN_PIPE, lexer.TOKEN_CARET, lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
			l := a.typeArithNodesForSMT(n.Left)
			r := a.typeArithNodesForSMT(n.Right)
			if isSMTIntWidthType(l) {
				t = stripRefForBounds(l)
			} else if n.Op != lexer.TOKEN_LSHIFT && n.Op != lexer.TOKEN_RSHIFT && isSMTIntWidthType(r) {
				t = stripRefForBounds(r)
			}
		}
	}
	if t != nil {
		a.exprTypes[expr] = t
	}
	return t
}

// typeCastNodesInExpr walks a pure-arith substitution value and analyzes each CastExpr it contains
// (recording exprTypes for the cast and, transitively, its operand). Non-cast arithmetic nodes need no
// per-node type for the SMT translator, so only casts are analyzed.
func (a *Analyzer) typeCastNodesInExpr(expr ast.Expr) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		if n != nil {
			a.typeCastNodesInExpr(n.Inner)
		}
	case *ast.UnaryExpr:
		if n != nil {
			a.typeCastNodesInExpr(n.Operand)
		}
	case *ast.BinaryExpr:
		if n != nil {
			a.typeCastNodesInExpr(n.Left)
			a.typeCastNodesInExpr(n.Right)
		}
	case *ast.CastExpr:
		if n != nil {
			analyzeExprForTypeOrProof(a, n) // records exprTypes for n and its operand on the current scope
		}
	}
}

// collectSelfRecursiveCalls returns every direct call whose resolved declaration is fn.
// Comparing declaration identity, rather than source spelling, also handles module-local functions
// whose calls are bare identifiers but whose declaration names are qualified during flattening.
// Indirect and method calls remain outside this direct-recursion check.
func (a *Analyzer) collectSelfRecursiveCalls(fn *ast.FuncDecl) []*ast.CallExpr {
	if fn == nil {
		return nil
	}
	var calls []*ast.CallExpr
	a.walkStaticStmts(a.readDecl(fn).Body, func(expr ast.Expr) bool {
		if call, ok := expr.(*ast.CallExpr); ok && call != nil {
			if decl, ok := a.resolveDirectCallFuncDecl(call); ok && a.canonicalFuncDecl(decl) == a.canonicalFuncDecl(fn) {
				calls = append(calls, call)
			}
		}
		return false // visit every node; never short-circuit
	})
	return calls
}

type recursiveCallSite struct {
	Call *ast.CallExpr
	Path ast.Expr
}

// collectSelfRecursiveCallSites preserves simple if/elif path guards for the termination SMT tier.
// Calls in a branch receive that branch's condition; statements after an early-return guard receive
// the negated guard. Other statement forms retain the existing conservative path context.
func (a *Analyzer) collectSelfRecursiveCallSites(fn *ast.FuncDecl) []recursiveCallSite {
	if a == nil || fn == nil {
		return nil
	}
	var sites []recursiveCallSite
	visit := func(path ast.Expr) func(ast.Expr) bool {
		return func(expr ast.Expr) bool {
			call, ok := expr.(*ast.CallExpr)
			if !ok || call == nil {
				return false
			}
			if decl, ok := a.resolveDirectCallFuncDecl(call); ok && a.canonicalFuncDecl(decl) == a.canonicalFuncDecl(fn) {
				sites = append(sites, recursiveCallSite{Call: call, Path: path})
			}
			return false
		}
	}
	var walk func([]ast.Stmt, ast.Expr)
	walk = func(stmts []ast.Stmt, path ast.Expr) {
		activePath := path
		for _, stmt := range stmts {
			if stmt == nil {
				continue
			}
			if branch, ok := stmt.(*ast.IfStmt); ok && branch != nil && branch.Cond != nil {
				a.walkStaticExpr(branch.Cond, visit(activePath))
				thenPath := combinePathCond(activePath, branch.Cond)
				walk(branch.Then, thenPath)
				negatedPrior := ast.Expr(&ast.UnaryExpr{Position: branch.Cond.Pos(), Op: lexer.TOKEN_NOT, Operand: branch.Cond})
				allArmsExit := blockDefinitelyExits(branch.Then)
				for _, elif := range branch.Elifs {
					if elif.Cond == nil {
						allArmsExit = false
						continue
					}
					a.walkStaticExpr(elif.Cond, visit(activePath))
					elifPath := combinePathCond(activePath, &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: elif.Cond})
					walk(elif.Body, elifPath)
					allArmsExit = allArmsExit && blockDefinitelyExits(elif.Body)
					negatedPrior = &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: &ast.UnaryExpr{Position: elif.Cond.Pos(), Op: lexer.TOKEN_NOT, Operand: elif.Cond}}
				}
				if len(branch.Else) != 0 {
					walk(branch.Else, combinePathCond(activePath, negatedPrior))
					allArmsExit = allArmsExit && blockDefinitelyExits(branch.Else)
					if allArmsExit {
						break
					}
				} else if allArmsExit {
					activePath = combinePathCond(activePath, negatedPrior)
				}
				continue
			}
			a.walkStaticStmt(stmt, visit(activePath))
			if blockDefinitelyExits([]ast.Stmt{stmt}) {
				break
			}
		}
	}
	walk(a.readDecl(fn).Body, nil)
	return sites
}

// substForSelfCall maps each parameter name to the corresponding argument expression of a recursive
// call. A parameter without a positional argument is absent — a measure mentioning it then leaves the
// fragment and the decrease cannot be proven (sound: the call is reported as non-decreasing).
func (a *Analyzer) substForSelfCall(fn *ast.FuncDecl, call *ast.CallExpr) map[string]ast.Expr {
	subst := map[string]ast.Expr{}
	for i, param := range fn.Params {
		if i >= len(call.Args) || call.Args[i] == nil {
			continue
		}
		subst[param.Name] = call.Args[i]
	}
	return subst
}

// decreaseMeasureComponents normalizes both accepted spellings of a lexicographic measure:
// repeated clauses (`decreases a` / `decreases b`) and tuple syntax (`decreases (a, b)`).
func decreaseMeasureComponents(measures []ast.Expr) []ast.Expr {
	out := make([]ast.Expr, 0, len(measures))
	for _, measure := range measures {
		appendDecreaseMeasureComponents(&out, measure)
	}
	return out
}

func appendDecreaseMeasureComponents(out *[]ast.Expr, measure ast.Expr) {
	switch m := measure.(type) {
	case *ast.ParenExpr:
		if m != nil {
			appendDecreaseMeasureComponents(out, m.Inner)
		}
	case *ast.TupleExpr:
		if m != nil {
			for _, elem := range m.Elems {
				appendDecreaseMeasureComponents(out, elem)
			}
		}
	default:
		if measure != nil {
			*out = append(*out, measure)
		}
	}
}

// proveMeasureDecreases proves the lexicographic measure tuple strictly decreases from entry
// (parameters symbolic) to a recursive call (parameters substituted by arguments). The tuple
// decreases iff some component k strictly decreases while every earlier component is provably
// unchanged. The deciding component must also be bounded below by 0 — otherwise an ever-decreasing
// measure would never bottom out.
func (a *Analyzer) proveMeasureDecreases(measures []ast.Expr, subst map[string]ast.Expr) bool {
	_, proven := a.proveMeasureDecreasesOutcome(measures, subst)
	return proven
}

// proveMeasureDecreasesOutcome returns the strongest proof tier used for a valid
// lexicographic decrease. The affine arithmetic is the fast path; SMT may additionally
// establish the lower bound from relational preconditions such as `index <= text.len`.
func (a *Analyzer) proveMeasureDecreasesOutcome(measures []ast.Expr, subst map[string]ast.Expr) (ProofOutcome, bool) {
	return a.proveMeasureDecreasesOutcomeUnderPath(measures, subst, nil)
}

func (a *Analyzer) proveMeasureDecreasesOutcomeUnderPath(measures []ast.Expr, subst map[string]ast.Expr, path ast.Expr) (ProofOutcome, bool) {
	measures = decreaseMeasureComponents(measures)
	for k, measure := range measures {
		earlierUnchanged := true
		for j := 0; j < k; j++ {
			if !a.measureDiffIsZero(measures[j], subst) {
				earlierUnchanged = false
				break
			}
		}
		if !earlierUnchanged {
			continue
		}
		strictly, strictViaSMT := a.measureStrictlyDecreasesUnderPath(measure, subst, path)
		if !strictly {
			continue
		}
		bounded, viaSMT := a.measureBoundedBelowWithTier(measure)
		if bounded {
			if viaSMT || strictViaSMT {
				return ProofProvenSMT, true
			}
			return ProofProvenLinear, true
		}
	}
	return ProofRefuted, false
}

// measureDiff returns the affine form of `measure(params) - measure(args)` — the amount the measure
// drops from entry to the recursive call. The entry side treats parameters as symbolic variables
// (affineOf admits immutable integer params); the call side substitutes arguments (substitutedAffine).
func (a *Analyzer) measureDiff(measure ast.Expr, subst map[string]ast.Expr) (affineForm, bool) {
	entry, ok := a.affineOf(measure, a.currentScope)
	if !ok {
		return affineForm{}, false
	}
	call, ok := a.substitutedAffine(measure, subst)
	if !ok {
		return affineForm{}, false
	}
	return subtractAffine(entry, call), true
}

// measureStrictlyDecreases reports whether the measure provably drops by a positive amount: the
// difference `entry - call` has a provably-positive lower bound. A constant positive difference
// (the `decreases n` + `f(n-1)` case → +1) is the common, always-sound result.
func (a *Analyzer) measureStrictlyDecreases(measure ast.Expr, subst map[string]ast.Expr) bool {
	proven, _ := a.measureStrictlyDecreasesUnderPath(measure, subst, nil)
	return proven
}

func (a *Analyzer) measureStrictlyDecreasesUnderPath(measure ast.Expr, subst map[string]ast.Expr, path ast.Expr) (bool, bool) {
	diff, ok := a.measureDiff(measure, subst)
	if ok {
		r := a.boundAffine(diff, a.currentScope)
		if r.loKnown && r.lo > 0 {
			return true, false
		}
	}
	post, ok := substituteLemmaEnsure(measure, subst)
	if !ok {
		return false, false
	}
	goal := &ast.BinaryExpr{Position: measure.Pos(), Op: lexer.TOKEN_GT, Left: measure, Right: post}
	a.typePureTerminationExprsForSMT(goal, path)
	if proven, _ := a.trySMTProveRequiresWithPath(goal, nil, path); proven {
		return true, true
	}
	return false, false
}

// typePureTerminationExprsForSMT records widths for a pure measure and its branch path before
// translating machine arithmetic. It deliberately declines to type arbitrary call expressions here;
// the ordinary body pass remains responsible for their full analysis.
func (a *Analyzer) typePureTerminationExprsForSMT(exprs ...ast.Expr) {
	proofProbe := a.beginProofReportProbe()
	defer proofProbe.restore()
	if a == nil {
		return
	}
	savedDiagnostics, savedFacts := a.suppressDiagnostics, a.suppressOptimizationFacts
	a.suppressDiagnostics = true
	a.suppressOptimizationFacts = true
	defer func() {
		a.suppressDiagnostics, a.suppressOptimizationFacts = savedDiagnostics, savedFacts
	}()
	for _, expr := range exprs {
		if expr == nil || !wherePredicateIsSideEffectFree(expr) {
			continue
		}
		a.typeCastNodesInExpr(expr)
		analyzeExprForTypeOrProof(a, expr)
	}
}

// measureDiffIsZero reports whether an (earlier, lexicographic) measure component is provably
// unchanged across the recursive call — the difference is exactly the constant 0.
func (a *Analyzer) measureDiffIsZero(measure ast.Expr, subst map[string]ast.Expr) bool {
	diff, ok := a.measureDiff(measure, subst)
	if !ok {
		return false
	}
	r := a.boundAffine(diff, a.currentScope)
	return r.loKnown && r.hiKnown && r.lo == 0 && r.hi == 0
}
