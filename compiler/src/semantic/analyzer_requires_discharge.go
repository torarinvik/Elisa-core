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
	if !ok || len(scheme.Requires) == 0 {
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
			// knows a static guarantee fell back, and let -strict escalate. A solver counterexample (an
			// input the caller's facts permit that violates the precondition) sharpens the message.
			a.recordProof(call.Pos(), subject, clauseName, ProofRuntime)
			a.recordRequiresReport(declName, req, call.Pos(), false)
			if counterexample != "" {
				a.proofLint(call.Pos(), "precondition of %q could not be proven statically at this call; it can fail when %s (or accept the runtime check)", declName, counterexample)
			} else {
				a.proofLint(call.Pos(), "precondition of %q could not be proven statically at this call; pass a provable value or accept the runtime check", declName)
			}
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
	left, ok := a.substitutedAffine(n.Left, subst)
	if !ok {
		return requiresUnknown
	}
	right, ok := a.substitutedAffine(n.Right, subst)
	if !ok {
		return requiresUnknown
	}
	diff := subtractAffine(left, right)
	r := a.boundAffine(diff, a.currentScope)
	switch n.Op {
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
				return affineForm{}, false
			}
			argName = argIdent.Name
		}
		fieldVal, ok := a.lookupWrittenStructField(argName, n.Field)
		if !ok {
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
