package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
	"reflect"
)

// RequiresReportEntry aggregates static-discharge outcomes for one (function, requires-clause) pair
// across every direct call site (docs c3 -requires-report). `Provable` counts the sites the static
// tiers discharge (linear or SMT); `Unprovable` counts the sites that fall back to a runtime check,
// with their source positions in `UnprovableSites`. The flag surfaces the blast radius of adding a
// `requires` to a hot function BEFORE committing (an unprovable call site is a new runtime-check
// obligation the caller did not have).
type RequiresReportEntry struct {
	DeclName        string
	ClauseText      string
	Provable        int
	Unprovable      int
	UnprovableSites []lexer.Pos
}

// requiresReportKey keys the per-clause aggregation by callee name + the rendered clause text, so
// two distinct preconditions on the same function (or the same precondition across many callers) are
// counted separately/together as the user expects.
type requiresReportKey struct {
	declName   string
	clauseText string
}

// recordRequiresReport accumulates one call-site outcome into the requires-report aggregation. Only
// active when the -requires-report flag is on; it changes nothing about errors/lints.
func (a *Analyzer) recordRequiresReport(declName string, req ast.Expr, pos lexer.Pos, provable bool) {
	if !a.requiresReport {
		return
	}
	clauseText := unparse.FormatExpr(req)
	key := requiresReportKey{declName: declName, clauseText: clauseText}
	if a.requiresReportData == nil {
		a.requiresReportData = map[requiresReportKey]*RequiresReportEntry{}
	}
	entry, ok := a.requiresReportData[key]
	if !ok {
		entry = &RequiresReportEntry{DeclName: declName, ClauseText: clauseText}
		a.requiresReportData[key] = entry
		a.requiresReportOrder = append(a.requiresReportOrder, key)
	}
	if provable {
		entry.Provable++
	} else {
		entry.Unprovable++
		entry.UnprovableSites = append(entry.UnprovableSites, pos)
	}
}

// requiresReportEntries returns the aggregated report in deterministic first-seen order.
func (a *Analyzer) requiresReportEntries() []RequiresReportEntry {
	if len(a.requiresReportOrder) == 0 {
		return nil
	}
	out := make([]RequiresReportEntry, 0, len(a.requiresReportOrder))
	for _, key := range a.requiresReportOrder {
		if entry := a.requiresReportData[key]; entry != nil {
			out = append(out, *entry)
		}
	}
	return out
}

// Static precondition discharge (docs/86 brick 86-5).
//
// A callee's `requires <bool-expr>` clauses are runtime debug-checks inside the callee body. This
// pass connects them to the CALLER's static facts: at a direct call site `f(args...)`, each requires
// clause is re-interpreted with the callee's parameters substituted by the actual argument
// expressions, then proven against the caller's range facts with the SAME tier-1/tier-2 linear
// machinery used for value refinements. The result turns two independent runtime asserts (a
// `requires` in the callee, a known fact in the caller) into a real interprocedural contract:
//
//   - PROVEN  — the caller's facts entail the precondition; recorded in the --explain report, and
//     under -strict it is the difference between "checked at runtime" and "guaranteed".
//   - REFUTED — the caller's facts prove the precondition is ALWAYS violated → hard compile error
//     (a definite contract break, caught before the program runs).
//   - UNKNOWN — declined soundly; the callee's existing runtime check stands, a -Wperf-style lint
//     fires, and -strict escalates it to an error (Dafny-like prove-it-or-fail).
//
// Soundness: the prover is the bounded-linear fragment (affineForm + boundAffine), which fails
// closed — any leaf outside the fragment, any open bound on a needed side, makes a clause UNKNOWN,
// never falsely PROVEN or REFUTED. Refutation requires proving the negation holds on the WHOLE
// bounded range, so an over-approximated range can only weaken a refutation to UNKNOWN.

// dischargeCallRequires proves a direct call's arguments against the callee's `requires` clauses.
// Mirrors dischargeCallArgRefinements (refinement-typed params); this covers arbitrary boolean
// preconditions in the bounded-linear fragment.
func (a *Analyzer) dischargeCallRequires(call *ast.CallExpr, args []ast.Expr) {
	scheme, ok := a.callRequiresScheme(call)
	if !ok {
		return
	}
	if scheme.IsLemma {
		// A lemma's preconditions are handled by assumeLemmaEnsures with hard-error semantics (a
		// lemma is erased, so an unmet `requires` has no runtime check and cannot be tolerated as a
		// lint). Don't run the ordinary lint/strict path for it.
		return
	}
	// Boundary preconditions, including extern FFI contracts, are checked at every call site so the
	// caller cannot hand a callee an out-of-domain argument.
	callee, _ := a.resolveDirectCallFuncDecl(call)
	a.checkCalleeRequires(call, scheme.DeclName, scheme.Requires, scheme.Params, args, callee)
	a.checkCalleeParamWhereRefinements(call, scheme.DeclName, scheme.Params, args)
	// SpecSignature-based where discharge: handles imported/cross-module callees whose AST is
	// unavailable by reading the predicate from the canonical SpecSignature.ParamPredicates.
	if ft, ok := a.callFuncType(call); ok && ft != nil && ft.SpecSignature != nil {
		a.checkCalleeSpecSignatureWherePredicates(call, scheme.DeclName, ft.SpecSignature, scheme.Params, args)
	}
}

// callRequiresScheme borrows declaration headers for the duration of
// discharge. The checks below only read those headers; building the owned
// multi-channel refinement scheme would copy every parameter on every call.
func (a *Analyzer) callRequiresScheme(call *ast.CallExpr) (RefinementScheme, bool) {
	if decl, ok := a.resolveDirectCallFuncDecl(call); ok && decl != nil {
		return RefinementScheme{DeclName: decl.Name, Params: decl.Params, Requires: decl.Requires, IsLemma: decl.IsLemma}, true
	}
	if ext, ok := a.resolveDirectCallExternFuncDecl(call); ok && ext != nil {
		return RefinementScheme{DeclName: ext.Name, Params: ext.Params, Requires: ext.Requires}, true
	}
	if ft, ok := a.callFuncType(call); ok && ft.SpecSignature != nil {
		return a.refinementSchemeFromSpecSignature(ft.SpecSignature, ft.RefinementEnsures), true
	}
	return RefinementScheme{}, false
}

// resolveDirectCallExternFuncDecl resolves a direct `name(...)` call to its extern declaration.
func (a *Analyzer) resolveDirectCallExternFuncDecl(call *ast.CallExpr) (*ast.ExternFuncDecl, bool) {
	decl, ok := a.resolveDirectCallExternFuncDeclForReport(call)
	if a != nil {
		a.addProofReportOrigin(decl)
	}
	return decl, ok
}

func (a *Analyzer) resolveDirectCallExternFuncDeclForReport(call *ast.CallExpr) (*ast.ExternFuncDecl, bool) {
	if call == nil || a.currentScope == nil {
		return nil, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil {
		return nil, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return nil, false
	}
	ext, ok := sym.Node.(*ast.ExternFuncDecl)
	return ext, ok && ext != nil
}

// checkCalleeRequires discharges/checks a callee's precondition clauses against the caller's
// arguments — shared by ordinary and extern (FFI boundary) calls.
func (a *Analyzer) checkCalleeRequires(call *ast.CallExpr, declName string, requires []ast.Expr, params []ast.ParamDecl, args []ast.Expr, callee *ast.FuncDecl) {
	a.addProofReportOrigin(callee)
	if len(requires) == 0 {
		return
	}
	// Map each callee parameter name to the caller's argument expression. A param with no
	// corresponding argument (variadic tail, defaulted) is simply absent — a clause that mentions it
	// then leaves the fragment and declines.
	subst := map[string]ast.Expr{}
	for i, param := range params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	a.qualifyCalleeClauseConsts(call, requires, subst)
	// Calls inside a guarded specification only need their contract on paths where the
	// expression is evaluated. Preserve that logical path for the SMT fallback (for example,
	// `ensure i < n implies result == recursive(i + 1)`).
	callPath := a.contractCallPath(call)
	subject := "precondition of " + declName
	for _, req := range requires {
		if req == nil {
			continue
		}
		clauseName := "requires"
		switch a.proveRequiresClause(req, subst) {
		case requiresProven:
			a.recordProof(call.Pos(), subject, clauseName, ProofProvenLinear)
			a.recordRequiresReport(declName, req, call.Pos(), true)
		case requiresRefuted:
			a.recordProof(call.Pos(), subject, clauseName, ProofRefuted)
			a.errorf(call.Pos(), "precondition of %q is violated: the argument provably does not satisfy `requires`", declName)
		default:
			// SMT fallback (docs/90 brick 3): the linear clause prover declined, but the solver may
			// still discharge a non-linear precondition (e.g. `requires lo * 2 <= cap`) under the
			// caller's facts. Only `unsat` of the negation concludes — sound, off unless -smt.
			proven, counterexample := a.trySMTProveCallRequires(req, subst, callPath, params, callee)
			if !proven {
				// Reported below when it stays unproven: worth the larger budget.
				a.withSMTEscalation(func() bool {
					proven, counterexample = a.trySMTProveCallRequires(req, subst, callPath, params, callee)
					return proven
				})
			}
			if proven {
				a.recordProof(call.Pos(), subject, clauseName, ProofProvenSMT)
				a.recordRequiresReport(declName, req, call.Pos(), true)
				continue
			}
			// Unknown: the callee still checks this at runtime (debug builds). Surface it so the user
			// knows a static guarantee fell back, and let -strict escalate. Build a rich diagnostic
			// (goal text, relevant scope facts, actionable suggestion) via
			// buildRequiresFailureDiagnosticWithProvenance so each known fact is annotated with its
			// origin (requires hypothesis, where refinement, branch guard, etc.).
			a.recordProof(call.Pos(), subject, clauseName, ProofRuntime)
			a.recordRequiresReport(declName, req, call.Pos(), false)
			diag := a.buildRequiresFailureDiagnosticWithProvenance(req, subst, counterexample)
			a.proofLint(call.Pos(), "%s", diag.Format(declName))
		}
	}
}

// contractCallPath finds a pure guard that is required for a call expression inside a contract.
// Implication is represented in the AST as `not guard or consequent`, so its consequent inherits
// the guard. Short-circuit conjunction/disjunction and ternary branches contribute their normal
// evaluation conditions as well. The path is used only as an SMT hypothesis for this specific call.
func (a *Analyzer) contractCallPath(call *ast.CallExpr) ast.Expr {
	if a == nil || a.currentFuncDecl == nil || call == nil {
		return nil
	}
	clauses := append([]ast.Expr{}, a.currentFuncDecl.Requires...)
	clauses = append(clauses, a.currentFuncDecl.EnsureValues...)
	for _, clause := range clauses {
		if path, found := a.findContractCallPath(clause, call, nil); found {
			return path
		}
	}
	return nil
}

func (a *Analyzer) findContractCallPath(expr ast.Expr, target *ast.CallExpr, path ast.Expr) (ast.Expr, bool) {
	if expr == nil || target == nil {
		return nil, false
	}
	if call, ok := expr.(*ast.CallExpr); ok && contractCallsMatch(call, target) {
		return path, true
	}
	search := func(child ast.Expr, childPath ast.Expr) (ast.Expr, bool) {
		return a.findContractCallPath(child, target, childPath)
	}
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		if found, ok := search(n.Left, path); ok {
			return found, true
		}
		rightPath := path
		switch n.Op {
		case lexer.TOKEN_AND:
			rightPath = contractPathAnd(path, n.Left)
		case lexer.TOKEN_OR:
			// `guard implies body` desugars to `not guard or body`.
			if not, ok := stripOptimizationParens(n.Left).(*ast.UnaryExpr); ok && not.Op == lexer.TOKEN_NOT {
				rightPath = contractPathAnd(path, not.Operand)
			} else {
				rightPath = contractPathAnd(path, &ast.UnaryExpr{Position: n.Left.Pos(), Op: lexer.TOKEN_NOT, Operand: n.Left})
			}
		}
		return search(n.Right, rightPath)
	case *ast.UnaryExpr:
		return search(n.Operand, path)
	case *ast.MoveExpr:
		return search(n.Operand, path)
	case *ast.ParenExpr:
		return search(n.Inner, path)
	case *ast.CastExpr:
		return search(n.Operand, path)
	case *ast.TernaryExpr:
		if found, ok := search(n.Cond, path); ok {
			return found, true
		}
		if found, ok := search(n.Value, contractPathAnd(path, n.Cond)); ok {
			return found, true
		}
		return search(n.Alt, contractPathAnd(path, &ast.UnaryExpr{Position: n.Cond.Pos(), Op: lexer.TOKEN_NOT, Operand: n.Cond}))
	case *ast.FieldExpr:
		return search(n.Object, path)
	case *ast.IndexExpr:
		for _, child := range []ast.Expr{n.Object, n.Index, n.Index2, n.Fallback} {
			if found, ok := search(child, path); ok {
				return found, true
			}
		}
	case *ast.SliceExpr:
		for _, child := range []ast.Expr{n.Object, n.Start, n.End} {
			if found, ok := search(child, path); ok {
				return found, true
			}
		}
	case *ast.TupleExpr:
		for _, child := range n.Elems {
			if found, ok := search(child, path); ok {
				return found, true
			}
		}
	case *ast.CallExpr:
		if found, ok := search(n.Func, path); ok {
			return found, true
		}
		if found, ok := search(n.SafeReceiver, path); ok {
			return found, true
		}
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		for _, child := range args {
			if found, ok := search(child, path); ok {
				return found, true
			}
		}
	case *ast.ListLitExpr:
		for _, children := range [][]ast.Expr{n.Elems, n.Keys} {
			for _, child := range children {
				if found, ok := search(child, path); ok {
					return found, true
				}
			}
		}
		return search(n.Owner, path)
	case *ast.StructLitExpr:
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		for _, child := range args {
			if found, ok := search(child, path); ok {
				return found, true
			}
		}
		for _, child := range n.Spreads {
			if found, ok := search(child, path); ok {
				return found, true
			}
		}
	case *ast.MembershipRangeExpr:
		if found, ok := search(n.Start, path); ok {
			return found, true
		}
		return search(n.End, path)
	case *ast.QuantifierExpr:
		if found, ok := search(n.In, path); ok {
			return found, true
		}
		return search(n.Body, path)
	}
	// Less common expression containers still need termination checking, but they do not add a
	// Boolean evaluation guard. Search them with the nearest known path using the shared AST walker.
	foundCall := false
	a.walkStaticExpr(expr, func(node ast.Expr) bool {
		if candidate, ok := node.(*ast.CallExpr); ok && contractCallsMatch(candidate, target) {
			foundCall = true
			return true
		}
		return false
	})
	return path, foundCall
}

func contractCallsMatch(left, right *ast.CallExpr) bool {
	if left == nil || right == nil {
		return false
	}
	if left == right {
		return true
	}
	// Contract analysis may clone call nodes while preserving source positions. This match controls
	// which branch guard can justify a proof, so compare the complete AST node: the optimization
	// printer is intentionally lossy for some expressions and omits call metadata such as safe-call
	// receivers. Deep equality fails closed for unsupported or newly added AST fields.
	return left.Pos() == right.Pos() && reflect.DeepEqual(left, right)
}

func contractPathAnd(path ast.Expr, guard ast.Expr) ast.Expr {
	if guard == nil {
		return path
	}
	if path == nil {
		return guard
	}
	return &ast.BinaryExpr{Position: guard.Pos(), Op: lexer.TOKEN_AND, Left: path, Right: guard}
}

// requiresVerdict is the tri-state result of proving one precondition clause under the caller facts.
type requiresVerdict int

const (
	requiresUnknown requiresVerdict = iota // outside the fragment, or bounds insufficient
	requiresProven                         // entailed on the whole bounded range
	requiresRefuted                        // negation entailed on the whole bounded range
)

// proveRequiresClause classifies a precondition clause after substituting callee params with caller
// args. Handles conjunctions structurally (`A and B`) and comparison leaves via bounded linear
// arithmetic; everything else declines (requiresUnknown), keeping the pass sound.
func (a *Analyzer) proveRequiresClause(expr ast.Expr, subst map[string]ast.Expr) requiresVerdict {
	// A proven assertion is itself a complete proof of the same instantiated goal.  This fast path is
	// particularly important for field-place assertions: affineOf intentionally does not model an
	// arbitrary `object.field` as an immutable scalar, while the assertion fact is nevertheless sound
	// and is invalidated whenever that place (or one of its roots) may be mutated.
	instantiated := substituteIdents(expr, subst)
	goalKey := optimizationExprString(stripOptimizationParens(instantiated))
	if goalKey != "" {
		for sc := a.currentScope; sc != nil; sc = sc.Parent {
			for _, fact := range sc.smtAssertFacts {
				if optimizationExprString(stripOptimizationParens(fact.Expr)) == goalKey {
					return requiresProven
				}
			}
		}
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.proveRequiresClause(n.Inner, subst)
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			l := a.proveRequiresClause(n.Left, subst)
			r := a.proveRequiresClause(n.Right, subst)
			// `A and B` is refuted if EITHER conjunct is always-false; proven only if BOTH are proven.
			if l == requiresRefuted || r == requiresRefuted {
				return requiresRefuted
			}
			if l == requiresProven && r == requiresProven {
				return requiresProven
			}
			return requiresUnknown
		}
		return a.proveRequiresComparison(n, subst)
	default:
		return requiresUnknown
	}
}

// proveRequiresComparison proves a single `L OP R` precondition comparison by forming the affine
// difference `L - R` over the caller's immutable integer variables and bounding it: the clause holds
// iff the difference's whole range satisfies `OP 0`, and is refuted iff the whole range satisfies the
// negation. Cross-variable preconditions (`lo <= hi`) fall out for free when both sides bound to
// constants/known ranges.
func (a *Analyzer) proveRequiresComparison(n *ast.BinaryExpr, subst map[string]ast.Expr) requiresVerdict {
	switch n.Op {
	case lexer.TOKEN_GT, lexer.TOKEN_GTEQ, lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
	default:
		return requiresUnknown
	}
	left, lok := a.substitutedAffine(n.Left, subst)
	right, rok := a.substitutedAffine(n.Right, subst)
	if lok && rok {
		diff := subtractAffine(left, right)
		r := a.boundAffine(diff, a.currentScope)
		if v := classifyDiffRange(r, n.Op); v != requiresUnknown {
			return v
		}
		// Transitive-relational fallback: when the affine-bound tier cannot resolve the goal via
		// constant ranges, try to prove/refute a simple `L OP R` comparison (where both L and R
		// reduce to a single immutable-integer variable) by checking reachability in the relational-
		// edge graph (collectRelationalEdges). E.g. `a <= b, b <= c` can prove `a <= c` and refute
		// `a > c`. Only ident-vs-ident goals with a single term on each side are admitted (sound:
		// a more complex affine expression would require the general arithmetic path above).
		if v := a.transitiveRelationalProof(left, right, n.Op); v != requiresUnknown {
			return v
		}
	}
	// Field-invariant range tier: an operand that is a struct-field read (directly, or a callee param
	// that substitutes to a caller field read) carries no affine form, so the tiers above decline. But
	// the field's enclosing struct invariant pins its range — e.g. `invariant self.panel_idx >= 0`
	// bounds `c.panel_idx` to [0, +inf) at every read. Bound each side as an interval (its affine range
	// OR its invariant range) and decide `L OP R` by interval subtraction. This is what lets
	// `cc_blit_panel(c, ..., c.panel_idx)` discharge `requires slot >= 0` even across an intervening
	// mutation or mutable-ref call that dropped the flow fact. Sound: struct invariants hold at every
	// field read (established at construction, re-checked after each field store) — the same basis as
	// structInvariantEntailsFieldRefinement and the method-entry invariant seed.
	if lr, ok := a.rangeOfRequiresOperand(n.Left, subst); ok {
		if rr, ok := a.rangeOfRequiresOperand(n.Right, subst); ok {
			if v := classifyDiffRange(subNumRange(lr, rr), n.Op); v != requiresUnknown {
				return v
			}
		}
	}
	return requiresUnknown
}
