package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
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
	scheme, ok := a.callRefinementScheme(call)
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
	a.checkCalleeRequires(call, scheme.DeclName, scheme.Requires, scheme.Params, args)
	a.checkCalleeParamWhereRefinements(call, scheme.DeclName, scheme.Params, args)
	// SpecSignature-based where discharge: handles imported/cross-module callees whose AST is
	// unavailable by reading the predicate from the canonical SpecSignature.ParamPredicates.
	if ft, ok := a.callFuncType(call); ok && ft != nil && ft.SpecSignature != nil {
		a.checkCalleeSpecSignatureWherePredicates(call, scheme.DeclName, ft.SpecSignature, scheme.Params, args)
	}
}

// resolveDirectCallExternFuncDecl resolves a direct `name(...)` call to its extern declaration.
func (a *Analyzer) resolveDirectCallExternFuncDecl(call *ast.CallExpr) (*ast.ExternFuncDecl, bool) {
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
func (a *Analyzer) checkCalleeRequires(call *ast.CallExpr, declName string, requires []ast.Expr, params []ast.ParamDecl, args []ast.Expr) {
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
			proven, counterexample := a.trySMTProveRequires(req, subst)
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

// classifyDiffRange decides a comparison `L OP R` from the interval of the difference `L - R`. A bound
// is used only when known; an open bound on the needed side leaves the verdict requiresUnknown
// (fail-closed). Shared by the affine tier and the field-invariant range tier.
func classifyDiffRange(r numRange, op lexer.TokenKind) requiresVerdict {
	switch op {
	case lexer.TOKEN_GT: // L - R > 0
		if r.loKnown && r.lo > 0 {
			return requiresProven
		}
		if r.hiKnown && r.hi <= 0 {
			return requiresRefuted
		}
	case lexer.TOKEN_GTEQ: // L - R >= 0
		if r.loKnown && r.lo >= 0 {
			return requiresProven
		}
		if r.hiKnown && r.hi < 0 {
			return requiresRefuted
		}
	case lexer.TOKEN_LT: // L - R < 0
		if r.hiKnown && r.hi < 0 {
			return requiresProven
		}
		if r.loKnown && r.lo >= 0 {
			return requiresRefuted
		}
	case lexer.TOKEN_LTEQ: // L - R <= 0
		if r.hiKnown && r.hi <= 0 {
			return requiresProven
		}
		if r.loKnown && r.lo > 0 {
			return requiresRefuted
		}
	case lexer.TOKEN_EQEQ: // L - R == 0
		if r.loKnown && r.hiKnown && r.lo == 0 && r.hi == 0 {
			return requiresProven
		}
		if (r.loKnown && r.lo > 0) || (r.hiKnown && r.hi < 0) {
			return requiresRefuted
		}
	case lexer.TOKEN_BANGEQ: // L - R != 0
		if (r.loKnown && r.lo > 0) || (r.hiKnown && r.hi < 0) {
			return requiresProven
		}
		if r.loKnown && r.hiKnown && r.lo == 0 && r.hi == 0 {
			return requiresRefuted
		}
	}
	return requiresUnknown
}

// subNumRange returns the interval of `l - r`: [l.lo - r.hi, l.hi - r.lo]. A bound is known only when
// both contributing bounds are known and the subtraction does not overflow int64 (an overflowing bound
// becomes open, which only declines a proof — never admits an unsound one).
func subNumRange(l, r numRange) numRange {
	out := numRange{}
	if l.loKnown && r.hiKnown {
		if v, ok := subInt64Checked(l.lo, r.hi); ok {
			out.loKnown, out.lo = true, v
		}
	}
	if l.hiKnown && r.loKnown {
		if v, ok := subInt64Checked(l.hi, r.lo); ok {
			out.hiKnown, out.hi = true, v
		}
	}
	return out
}

func subInt64Checked(a, b int64) (int64, bool) {
	if b == -int64(^uint64(0)>>1)-1 { // b == minInt64: -b overflows, so decline (open bound)
		return 0, false
	}
	return addInt64Checked(a, -b)
}

// rangeOfRequiresOperand bounds a precondition operand as an integer interval: first via the affine
// tier (constants, immutable-int params/args, linear combos), then — when that yields no usable bound —
// via the struct invariant of a field-read operand (resolved directly or through a substituted param).
func (a *Analyzer) rangeOfRequiresOperand(expr ast.Expr, subst map[string]ast.Expr) (numRange, bool) {
	if aff, ok := a.substitutedAffine(expr, subst); ok {
		r := a.boundAffine(aff, a.currentScope)
		if r.loKnown || r.hiKnown {
			return r, true
		}
	}
	if fe, ok := a.resolveRequiresFieldOperand(expr, subst); ok {
		if r, ok := a.structInvariantFieldRange(fe); ok {
			return r, true
		}
	}
	return numRange{}, false
}

// resolveRequiresFieldOperand resolves a precondition operand to the caller-side field read it denotes:
// either the operand is itself a field read, or it is a callee parameter whose substituted argument is
// a field read (`requires slot >= 0` called as `f(c.panel_idx)`). Returns the field expression whose
// `Object` type carries the struct invariant.
func (a *Analyzer) resolveRequiresFieldOperand(expr ast.Expr, subst map[string]ast.Expr) (*ast.FieldExpr, bool) {
	switch n := unwrapParen(expr).(type) {
	case *ast.FieldExpr:
		return n, true
	case *ast.Ident:
		if arg, ok := subst[n.Name]; ok {
			if fe, ok := unwrapParen(arg).(*ast.FieldExpr); ok {
				return fe, true
			}
		}
	}
	return nil, false
}

// transitiveRelationalProof attempts to prove or refute a comparison `leftAffine OP rightAffine`
// by transitive closure over the relational-edge graph (requires-clauses and flow-local
// assert-facts between immutable integer idents). Both sides must reduce to a single immutable
// integer variable (affine form with exactly one term of coefficient +1 and constant=0); any
// more complex form is outside this tier and returns requiresUnknown (sound: we only conclude
// when we can chain a witnessed path).
//
// Sound: a path of `<=` edges witnesses `a <= c`; a path with at least one `<` edge witnesses
// `a < c` (which also proves `a <= c`). Refutation of `a >= c` follows from proof of `a < c`.
// A missing link returns requiresUnknown (declines, never fabricates).
func (a *Analyzer) transitiveRelationalProof(leftAff, rightAff affineForm, op lexer.TokenKind) requiresVerdict {
	// Only ident-vs-ident with unit coefficient and zero constant.
	if leftAff.c != 0 || len(leftAff.terms) != 1 {
		return requiresUnknown
	}
	if rightAff.c != 0 || len(rightAff.terms) != 1 {
		return requiresUnknown
	}
	var lName, rName string
	for k, v := range leftAff.terms {
		if v != 1 {
			return requiresUnknown
		}
		lName = k
	}
	for k, v := range rightAff.terms {
		if v != 1 {
			return requiresUnknown
		}
		rName = k
	}
	if lName == "" || rName == "" {
		return requiresUnknown
	}

	// Build the relational-edge graph.
	edges := a.collectRelationalEdges()
	if len(edges) == 0 {
		return requiresUnknown
	}

	// BFS from lName, following edges that give UPPER bounds (lName <= ... <= rName), to see if
	// rName is reachable with a path consisting of <= or < edges. We also track whether any edge
	// on the path is strict (< rather than <=) so we know if we proved `lName < rName` or just
	// `lName <= rName`.
	//
	// Edge direction: for `a <= b` (LTEQ with left=a), following from a reaches b with `<=` weight.
	//                 for `a < b`  (LT   with left=a), following from a reaches b with `<`  weight.
	//                 for `b >= a` (GTEQ with right=a), following from a reaches b with `<=` weight.
	//                 for `b > a`  (GT   with right=a), following from a reaches b with `<`  weight.
	type bfsState struct {
		node   string
		strict bool // at least one < edge on the path so far
		depth  int
	}
	const maxDepth = 8
	// visited tracks (node, strict) — once we've seen a node with strict=false, we need not
	// revisit with strict=true (it's a tighter result either way, and we take the best we find).
	visitedNonStrict := map[string]bool{}
	visitedStrict := map[string]bool{}
	queue := []bfsState{{node: lName, strict: false, depth: 0}}
	foundStrict := false
	foundNonStrict := false
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			continue
		}
		for _, e := range edges {
			var peer string
			var edgeStrict bool
			switch e.op {
			case lexer.TOKEN_LTEQ:
				if e.left == cur.node {
					peer, edgeStrict = e.right, false
				}
			case lexer.TOKEN_LT:
				if e.left == cur.node {
					peer, edgeStrict = e.right, true
				}
			case lexer.TOKEN_GTEQ:
				if e.right == cur.node {
					peer, edgeStrict = e.left, false
				}
			case lexer.TOKEN_GT:
				if e.right == cur.node {
					peer, edgeStrict = e.left, true
				}
			}
			if peer == "" {
				continue
			}
			pathStrict := cur.strict || edgeStrict
			if peer == rName {
				if pathStrict {
					foundStrict = true
				} else {
					foundNonStrict = true
				}
				continue
			}
			// Avoid revisiting a node on a path that is not more informative.
			if pathStrict {
				if visitedStrict[peer] {
					continue
				}
				visitedStrict[peer] = true
			} else {
				if visitedNonStrict[peer] {
					continue
				}
				visitedNonStrict[peer] = true
			}
			queue = append(queue, bfsState{node: peer, strict: pathStrict, depth: cur.depth + 1})
		}
	}

	// Interpret the reachability results against the requested operator.
	// foundStrict implies lName < rName; foundNonStrict implies lName <= rName (not necessarily <).
	provenLT := foundStrict
	provenLTEQ := foundStrict || foundNonStrict
	switch op {
	case lexer.TOKEN_LTEQ: // want lName <= rName
		if provenLTEQ {
			return requiresProven
		}
	case lexer.TOKEN_LT: // want lName < rName
		if provenLT {
			return requiresProven
		}
	case lexer.TOKEN_GTEQ: // want lName >= rName — provably FALSE if lName < rName
		if provenLT {
			return requiresRefuted
		}
	case lexer.TOKEN_GT: // want lName > rName — provably FALSE if lName <= rName
		if provenLTEQ {
			return requiresRefuted
		}
	}
	return requiresUnknown
}

// substitutedAffine builds the affine form of a callee-side expression after replacing each callee
// parameter identifier with the caller's argument affine form. A param identifier resolves through
// `subst` (to the caller's argument, evaluated in the caller scope); any other identifier is treated
// as a potential caller-visible constant (module const) or declines. Mirrors affineOf, plus the
// substitution leaf and a more general product rule (either operand may reduce to a constant form).
func (a *Analyzer) substitutedAffine(expr ast.Expr, subst map[string]ast.Expr) (affineForm, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.substitutedAffine(n.Inner, subst)
	case *ast.IntLit:
		if c, ok := a.constIntValue(n); ok {
			return affineForm{c: c, terms: map[string]int64{}}, true
		}
		return affineForm{}, false
	case *ast.Ident:
		if arg, ok := subst[n.Name]; ok {
			return a.affineOf(arg, a.currentScope)
		}
		// Not a parameter: only admissible if it const-folds (e.g. a module const referenced by the
		// requires clause). A callee local cannot appear in a requires clause, so this is safe.
		if c, ok := a.constIntValue(n); ok {
			return affineForm{c: c, terms: map[string]int64{}}, true
		}
		return affineForm{}, false
	case *ast.UnaryExpr:
		if n.Op != lexer.TOKEN_MINUS {
			return affineForm{}, false
		}
		inner, ok := a.substitutedAffine(n.Operand, subst)
		if !ok {
			return affineForm{}, false
		}
		return negateAffine(inner), true
	case *ast.BinaryExpr:
		l, ok := a.substitutedAffine(n.Left, subst)
		if !ok {
			return affineForm{}, false
		}
		r, ok := a.substitutedAffine(n.Right, subst)
		if !ok {
			return affineForm{}, false
		}
		switch n.Op {
		case lexer.TOKEN_PLUS:
			return addAffine(l, r), true
		case lexer.TOKEN_MINUS:
			return subtractAffine(l, r), true
		case lexer.TOKEN_STAR:
			// Linear only when one operand reduces to a pure constant form.
			if len(l.terms) == 0 {
				return scaleAffineForm(r, l.c), true
			}
			if len(r.terms) == 0 {
				return scaleAffineForm(l, r.c), true
			}
			return affineForm{}, false // variable*variable: non-linear, decline (sound)
		default:
			return affineForm{}, false
		}
	case *ast.FieldExpr:
		// A field place in a requires clause (`requires off + 4 <= v.size`): resolve the formal `v`
		// to its caller argument, then recover that argument's construction-time field constant.
		base, ok := n.Object.(*ast.Ident)
		if !ok || base == nil {
			return affineForm{}, false
		}
		argName := base.Name
		if arg, ok := subst[base.Name]; ok {
			argIdent, ok2 := unwrapParen(arg).(*ast.Ident)
			if !ok2 || argIdent == nil {
				// The caller argument is not a bare ident (e.g. an inline literal).  For the
				// `.count` field specifically, try to evaluate the count directly from the arg.
				if n.Field == "count" {
					if list, ok3 := unwrapParen(arg).(*ast.ListLitExpr); ok3 && list != nil {
						return affineForm{c: int64(len(list.Elems)), terms: map[string]int64{}}, true
					}
				}
				return affineForm{}, false
			}
			argName = argIdent.Name
		}
		// `xs.count` where `xs` is an immutable darray local initialised from a list literal.
		if n.Field == "count" {
			if cnt, ok2 := a.lookupWrittenListCount(argName); ok2 {
				return affineForm{c: cnt, terms: map[string]int64{}}, true
			}
		}
		fieldVal, ok2 := a.lookupWrittenStructField(argName, n.Field)
		if !ok2 {
			return affineForm{}, false
		}
		return a.affineOf(fieldVal, a.currentScope)
	default:
		return affineForm{}, false
	}
}

// --- affineForm value helpers (pure, allocate fresh maps) -------------------------------------

func addAffine(l, r affineForm) affineForm {
	out := affineForm{c: l.c + r.c, terms: map[string]int64{}}
	for k, v := range l.terms {
		out.addTerm(k, v)
	}
	for k, v := range r.terms {
		out.addTerm(k, v)
	}
	return out
}

func subtractAffine(l, r affineForm) affineForm {
	out := affineForm{c: l.c - r.c, terms: map[string]int64{}}
	for k, v := range l.terms {
		out.addTerm(k, v)
	}
	for k, v := range r.terms {
		out.addTerm(k, -v)
	}
	return out
}

func negateAffine(f affineForm) affineForm {
	out := affineForm{c: -f.c, terms: map[string]int64{}}
	for k, v := range f.terms {
		out.addTerm(k, -v)
	}
	return out
}

func scaleAffineForm(f affineForm, k int64) affineForm {
	out := affineForm{c: f.c * k, terms: map[string]int64{}}
	for name, v := range f.terms {
		out.addTerm(name, v*k)
	}
	return out
}
