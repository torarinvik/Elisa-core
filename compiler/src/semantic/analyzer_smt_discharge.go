package semantic

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"elisacore/src/ast"
	"elisacore/src/smt"
)

// SMT discharge tier (docs/90 brick 2). The bounded-linear prover (docs/86) handles the common
// affine cases cheaply; an obligation it DECLINES — a non-linear product, a richer boolean law body —
// is translated to SMT-LIB2 and handed to a solver. The tier is the LAST prove-step before the
// runtime fallback, so the solver only ever runs on the hard residue, which is exactly what makes its
// cost measurable and bounded.
//
// Soundness: we ask the solver whether `facts ∧ ¬obligation` is satisfiable.
//   - unsat  → no input satisfies the facts yet violates the obligation → the obligation HOLDS → proven.
//   - sat    → a model violates it, but our facts are a SUBSET of what's true (we only model the
//              integer flow facts), so this is "not proven under known facts", NOT a refutation —
//              decline to the runtime check. (Refutation stays with const-eval, which has exact values.)
//   - unknown / no solver → decline.
// Only `unsat` ever concludes anything, so an incomplete translation or a flaky solver can lose a
// proof (runtime check) but never fabricate one.

// smtSolverHandle is the analyzer's view of the solver (an interface so tests can stub it and so the
// analyzer file need not import the smt package directly).
type smtSolverHandle = smt.Solver

// SMTStats is the cost report for the SMT tier (mirrors smt.Stats plus discharge-level counts).
// Exported so the CLI's --explain can render it. Zero-valued (Enabled=false) when the tier is off.
type SMTStats struct {
	Enabled             bool
	Attempts            int // obligations handed to the tier (linear declined)
	Proven              int // unsat → proven
	Declined            int // sat/unknown → fell back to runtime
	SolverProven        int // == Proven, kept for clarity in the report
	PersistentCacheHits int
	CacheHits           int           // obligations answered from the query cache (no solver round-trip)
	SpawnTime           time.Duration // summed solver process startup time
	SolverTime          time.Duration // wall time inside the solver across all queries
	Slowest             time.Duration // slowest single query
}

// String renders the profile for the --explain report (empty when the tier is off).
func (p SMTStats) String() string {
	if !p.Enabled {
		return ""
	}
	report := fmt.Sprintf(
		"SMT tier: %d obligations, %d proven, %d declined, %d cached; solver %.1fms (spawn %.1fms, slowest %.1fms)",
		p.Attempts, p.Proven, p.Declined, p.CacheHits,
		float64(p.SolverTime.Microseconds())/1000.0,
		float64(p.SpawnTime.Microseconds())/1000.0,
		float64(p.Slowest.Microseconds())/1000.0,
	)
	if p.PersistentCacheHits > 0 {
		report += fmt.Sprintf("; %d persistent", p.PersistentCacheHits)
	}
	return report
}

// smtQueryResourceLimit is the deterministic per-(check-sat) z3 budget (see openSMT).
//
// Measured over 73 stage1 files (1480 decided queries): p50 0.6k units, p90 25k, p99 1.1M-1.5M;
// solver time scales with the budget because undecidable queries run to it (6M: 468 s, 2M: 192 s,
// 1M: 110 s, 0.5M: 64 s, versus 291 s under the old 2 s timeout). Box sweep over the full stage1
// probe: 500k 195.6 s user / 1536 errors, 1M 212.7 s / 1535, 2M 256.7 s / 1535, 6M ~390 s / 1534
// (wall-clock 2 s baseline: 215 s). Owner: CPU first, so 500k.
const smtQueryResourceLimit = 500_000

// smtResourceLimit returns the budget, overridable by ELISA_SMT_RLIMIT (a positive integer) for
// calibration sweeps only. A fixed value is still deterministic; production leaves it unset.
func smtResourceLimit() int64 {
	if v := os.Getenv("ELISA_SMT_RLIMIT"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return smtQueryResourceLimit
}

// openSMT lazily starts the solver on first need. Returns nil if SMT is off or the solver can't be
// started (latched in smtUnavailable so we don't retry per query).
func (a *Analyzer) openSMT() smtSolverHandle {
	if !a.smtEnabled || a.smtUnavailable {
		return nil
	}
	if a.smtSolver != nil {
		return a.smtSolver
	}
	solver, err := smt.Open(smt.Options{
		Binary: a.smtBinary,
		// The per-query budget is z3 RESOURCE UNITS, not wall time, so a verdict never depends on
		// machine load (a 2 s timeout made proofs such as fnv1a's `decreases` flicker between runs).
		// An undecided obligation becomes Unknown and falls back to the runtime check.
		ResourceLimit: smtResourceLimit(),
		// Reset before every check: a verdict is a function of the query alone, so it is the
		// same in every worker, schedule and process. Unknown still needs a certified
		// resource-limit reason before it can be cached.
		Independent: smtIndependent,
		// Hang guard only, far above the time the budget takes even on a loaded machine; it should
		// never be the limit that decides a verdict.
		PerQueryTimeoutMillis: 120000,
	})
	if err != nil || solver == nil {
		markTransientSMT()
		a.smtUnavailable = true
		a.smtSnapshotTainted = true
		a.smtStats.Enabled = true
		return nil
	}
	a.smtSolver = solver
	a.smtStats.Enabled = true
	a.smtStats.SpawnTime += solver.Stats().SpawnMillis
	return solver
}

// closeSMT shuts down the solver and folds its harness stats into the profile.
func (a *Analyzer) closeSMT() {
	if a.smtSolver == nil {
		return
	}
	st := a.smtSolver.Stats()
	a.smtStats.SolverTime += st.Total
	a.smtStats.Slowest = max(a.smtStats.Slowest, st.Slowest)
	_ = a.smtSolver.Close()
	a.smtSolver = nil
}

// resetSMTForRetainedUpdate begins a new analysis pass on a retained Analyzer.
// Query text alone does not bind a verdict to the solver binary or resource
// budget, and the declaration pointers (and therefore smtHistoryFor) can survive
// an edit. Drop per-pass state so changed solver settings cannot reuse old
// resource declines or history.
func (a *Analyzer) resetSMTForRetainedUpdate() {
	a.closeSMT()
	a.smtQueryCache = nil
	a.smtProofCache = nil
	a.smtProofCacheInitialized = false
	a.smtUnavailable = false
	a.smtHistory = nil
	a.smtHistoryFor = nil
}

// trySMTProveRefinement attempts to discharge `value is law[predArgs]` with the solver. Returns true
// only on a sound proof (the solver reported unsat on the negated obligation). It is called after the
// linear tier declines, so the subject is genuinely outside the affine fragment (e.g. a var*var
// product) — precisely where the solver earns its keep.
func (a *Analyzer) trySMTProveRefinement(value ast.Expr, decl *ast.FuncDecl, predArgs []ast.Expr) bool {
	if !a.smtEnabled || a.smtUnavailable || decl == nil || len(decl.Params) == 0 {
		return false
	}
	tr := a.newSMTTranslator(nil)
	// Bind the law's static params to the bracket args. A compile-time-constant arg folds to its value
	// (`Bounded[0, 500]`); a VALUE-DEPENDENT arg (`Index[cap]`, `Bounded[0, n]`, `Aligned[page]`) binds
	// the law param to the argument's SMT term, so the law body proves RELATIONALLY against the runtime
	// value (docs — dependent refinements). An arg outside the SMT fragment declines the whole proof.
	// Sound: the arg term is the same faithful translation used for the subject and hypotheses, so the
	// obligation is exactly the law body evaluated at the actual argument values.
	varParamEnv := map[string]string{}
	for i, arg := range predArgs {
		if i+1 >= len(decl.Params) {
			break
		}
		pname := decl.Params[i+1].Name
		if c, ok := a.constIntValue(arg); ok {
			varParamEnv[pname] = smtInt(c)
			continue
		}
		term, ok := tr.term(arg)
		if !ok {
			return false
		}
		varParamEnv[pname] = term
	}
	// Bind the law's `self` to the subject. An array/darray subject is modeled as an SMT array (so the
	// law body's `self[i]` becomes a select); any other subject is an integer term.
	self := decl.Params[0].Name
	var subjectTerm string
	var ok bool
	if tr.isArrayLike(a.exprType(value)) {
		subjectTerm, ok = tr.arrayTermEnv(value, nil)
	} else {
		subjectTerm, ok = tr.term(value)
	}
	if !ok {
		return false
	}
	env := map[string]string{self: subjectTerm}
	for k, v := range varParamEnv {
		env[k] = v
	}
	// The law body as an SMT boolean, with `self` replaced by the subject term.
	body, ok := a.lawBodyExpr(decl)
	if !ok {
		return false
	}
	// Lower the law body to the VC IR and discharge against the standard hypothesis set (which assumes
	// the enclosing function's preconditions — docs/90 brick 90-6 — so a `requires forall k: 0<=k<n
	// implies xs[k] >= 0` instantiates to prove `return xs[0] is NonNeg`). Contract-sound: the callee may
	// assume its preconditions and an SMT-proven VALUE fact never drives bounds-check elision. On a failed
	// proof, stash z3's satisfying assignment so the refinement diagnostic can show a counterexample.
	// Immutable params carrying their OWN refinements (e.g. `addr: PageAddr` ≡ `Aligned[4096]`) are
	// postconditions the caller proved; assert each as a hypothesis so a return obligation over them
	// (`return addr + 4096 is Aligned[4096]`) can discharge. Non-interval refinements (alignment, mask)
	// have no rangeFact representation, so this is the only channel that carries them into the solver.
	extra := a.smtParamRefinementHypotheses(tr)
	proven, counterexample, ok := a.smtCheckGoal(tr, body, env, extra)
	if !ok {
		return false
	}
	if !proven {
		a.lastSMTCounterexample = counterexample
	}
	return proven
}

// smtParamRefinementHypotheses emits `(assert <law-body>)` for each IMMUTABLE param of the enclosing
// function whose declared (or aliased) type carries a refinement predicate that is a known,
// quantifier-free law. The law body is instantiated at the param's identifier and its static args, then
// lowered in `tr`'s namespace so it shares symbols with the goal. SOUND: a param's refinement is a
// precondition the caller is obligated to satisfy (checked/proven at every call site), so assuming it
// inside the body adds no unproven fact; a param whose body falls outside the SMT fragment is skipped.
func (a *Analyzer) smtParamRefinementHypotheses(tr *smtTranslator) string {
	if a == nil || a.currentFuncDecl == nil || tr == nil {
		return ""
	}
	var b strings.Builder
	for _, param := range a.currentFuncDecl.Params {
		if a.paramIsMutable(param) {
			continue
		}
		rt, ok := a.paramRefinementTypeExpr(param.Type)
		if !ok || rt == nil {
			continue
		}
		for _, pred := range rt.Preds {
			lawDecl, _, ok := a.lookupLaw(pred.Name)
			if !ok || lawDecl == nil || len(lawDecl.Params) == 0 {
				continue
			}
			if a.lawBodyContainsQuantifier(lawDecl) {
				continue
			}
			body, ok := a.lawBodyExpr(lawDecl)
			if !ok || body == nil {
				continue
			}
			subst := map[string]ast.Expr{lawDecl.Params[0].Name: &ast.Ident{Position: param.Position, Name: param.Name}}
			bad := false
			for i, arg := range pred.Args {
				if i+1 >= len(lawDecl.Params) || arg == nil {
					bad = true
					break
				}
				subst[lawDecl.Params[i+1].Name] = arg
			}
			if bad {
				continue
			}
			inst := ast.CloneExprSubst(body, subst)
			if inst == nil || a.mentionsShadowedParam(inst) {
				continue
			}
			f, ok := tr.lowerVCFormula(inst, nil)
			if !ok {
				continue
			}
			b.WriteString("(assert " + emitVCFormula(f) + ")\n")
		}
	}
	return b.String()
}

// smtNonNullRefParamHypotheses asserts `(= isnull_<param> false)` for every immutable param whose
// declared type is a non-nullable reference (`T&`, i.e. `RefType{State: RefStateNonNull}`). A T&
// param is structurally non-null in Elisa's type system: the language prohibits null T& values.
// This fact is NOT derived from flow guards, so it never appears in smtAssertHypotheses; this
// helper is the only channel that makes it available to the solver. The `isnull_` predicate key
// mirrors exactly what nullComparePointer+boolTerm produce for `p == null` / `p != null`, so the
// prover can use it for any goal involving the null-ness of a T& param.
// SOUND: only RefStateNonNull triggers the assertion. Nullable refs (T&?, RefStateNullable),
// null literals (RefStateNull), and T? (OptionalType) are left unconstrained — they fail to prove
// non-null postconditions unless a flow guard surfaces the non-null fact via smtAssertHypotheses.
func (a *Analyzer) smtNonNullRefParamHypotheses(tr *smtTranslator) string {
	if a == nil || a.currentFuncDecl == nil || tr == nil {
		return ""
	}
	var b strings.Builder
	for _, param := range a.currentFuncDecl.Params {
		if a.paramIsMutable(param) {
			continue
		}
		// Resolve the declared type through any alias chain.
		t := a.resolveType(param.Type)
		if t == nil {
			continue
		}
		rt, ok := IsRefType(t)
		if !ok || rt.State != RefStateNonNull {
			// Not a non-optional RefType → may be nullable; skip.
			continue
		}
		if a.shadowedParam(param.Name) {
			continue
		}
		pred := "isnull_" + param.Name
		tr.boolDecls[pred] = true
		b.WriteString("(assert (= " + pred + " false))\n")
	}
	return b.String()
}

// fieldReadResolvedType resolves the type of a struct-field read `obj.field` from the object's type,
// for synthetic/cloned FieldExpr nodes whose per-node exprTypes entry was never populated. It tries
// the object's recorded type, then a scope lookup for a bare identifier, then recurses for a nested
// field path. lookupResolvedFieldType peels refs/aggregates internally.
func (a *Analyzer) fieldReadResolvedType(n *ast.FieldExpr) (Type, bool) {
	if n == nil || n.Object == nil {
		return nil, false
	}
	if t := a.exprType(n.Object); t != nil {
		return a.lookupResolvedFieldType(peelNamedStateRefs(t), n.Field)
	}
	switch obj := n.Object.(type) {
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(obj.Name); ok && sym != nil && sym.Type != nil {
				return a.lookupResolvedFieldType(peelNamedStateRefs(sym.Type), n.Field)
			}
		}
	case *ast.FieldExpr:
		if it, ok := a.fieldReadResolvedType(obj); ok {
			return a.lookupResolvedFieldType(peelNamedStateRefs(it), n.Field)
		}
	}
	return nil, false
}

// counterexampleSuffix formats a satisfying-model string as a trailing diagnostic hint, or "" when
// there is none (e.g. the solver returned unknown/timeout rather than a concrete model).
func (a *Analyzer) counterexampleSuffix(ce string) string {
	if ce == "" {
		return ""
	}
	return " — it can fail when " + ce
}

// smtCheckVC is the central verification-condition chokepoint. Every SMT-tier obligation that is
// discharged against the analyzer's STANDARD hypothesis set flows through here, so the negate-and-check
// protocol, query layout, solver stats, and counterexample extraction live in exactly one place — add
// a new hypothesis source or change the proof protocol once and every site benefits.
//
// The standard hypothesis block is: the enclosing function's `requires`, the defining equalities of
// immutable integer locals (so the prover reasons THROUGH `rem = value % alignment`), the accumulated
// flow assert-facts (branch guards, proven invariants), and the scope's range facts. `extraHyps` are
// site-specific assertions already built from `tr` (e.g. a callee's poststates). The obligation is
// negated; only `unsat` concludes (a sound proof). On `sat` the model is rendered as a concrete
// counterexample. `factPreamble()` is emitted LAST — after the hypothesis builders and the caller's
// `obligation`/`extraHyps` have populated the translator's declarations — so all symbols are declared.
func (a *Analyzer) smtCheckVC(tr *smtTranslator, obligation string, extraHyps string) (bool, string) {
	return a.smtCheckVCMulti(tr, []string{obligation}, extraHyps)
}

// smtCheckVCMulti discharges SEVERAL obligations that share ONE hypothesis context — multi-goal
// batching (VC IR brick 4). The standard hypothesis block is built ONCE from `tr` and reused for every
// obligation, rather than re-derived per goal; this is the win when a conjunctive postcondition is
// split into its independent conjuncts (vcConjuncts), each a smaller, separately-decidable query. The
// batch holds only if EVERY obligation proves (each negation `unsat`); the first that fails short-
// circuits and returns its counterexample, so a failed conjunction is pinned to the exact conjunct
// that broke. With a single obligation this is byte-identical to the old smtCheckVC, so non-
// conjunctive goals stay behavior-preserving. factPreamble() is emitted inside each smtCheckQuery from
// the now-complete tr.decls (the obligations were already lowered, so all their symbols are declared),
// so every query is self-contained.
func (a *Analyzer) smtCheckVCMulti(tr *smtTranslator, obligations []string, extraHyps string) (bool, string) {
	requires := a.smtRequiresHypotheses(tr)
	locals := a.smtImmutableLocalHypotheses(tr)
	others := a.smtAssertHypotheses(tr) + a.smtFlowFactHypotheses(tr) + a.smtProtocolLawHypotheses(tr) + a.smtNonNullRefParamHypotheses(tr) + extraHyps
	for _, obligation := range obligations {
		preamble := tr.factPreamble()
		context := smtPreambleContext(preamble) + requires + others + obligation
		queryLocals := locals
		if len(tr.boolLocalRefs) == 0 {
			// Nothing below mutates tr or the analyzer's facts before smtCheckQuery renders the
			// preamble again, so hand it the text already built (it is GBs of allocation per seed).
			tr.reusePreamble, tr.reusePreambleOK = preamble, true
		} else {
			context = smtBoolMacroContext(tr, context)
			boolDemandContext := context + sliceImmutableLocalHypotheses(locals, context)
			queryLocals += a.smtImmutableBoolLocalHypotheses(tr, boolDemandContext)
			context = smtPreambleContext(tr.factPreamble()) + requires + others + obligation
			context = smtBoolMacroContext(tr, context)
		}
		hyps := requires + sliceImmutableLocalHypotheses(queryLocals, context) + others
		if proven, ce := a.smtCheckQuery(tr, hyps, obligation); !proven {
			return false, ce
		}
	}
	return true, ""
}

// smtDischargeFormula discharges a lowered VC-IR formula, splitting a top-level conjunction into
// independent sub-goals (vcConjuncts) batched over a shared hypothesis context (brick 4). A formula
// that folds entirely to `true` is valid under any hypotheses and concludes with NO solver call.
// Before the solver query, we register the comparison sub-terms of each conjunct as ce-expressions so
// that a refuting model shows concrete values for compound sub-expressions (e.g. "x/2=-1") in addition
// to the free variables already shown via tr.decls ("x=-2"). This is additive: free variables remain
// the primary counterexample content; the sub-term values appear only when they differ from what the
// free-variable assignments already make obvious.
func (a *Analyzer) smtDischargeFormula(tr *smtTranslator, goal vcFormula, extraHyps string) (bool, string) {
	conjuncts := vcConjuncts(goal)
	if len(conjuncts) == 0 { // the whole obligation folded to true
		a.smtStats.Attempts++
		a.smtStats.Proven++
		return true, ""
	}
	// Register compound sub-terms from every conjunct as counterexample expressions BEFORE the query,
	// so their values are read back from a Sat model and appear in the diagnostic alongside the free
	// variables. A term that is already a simple variable (already in tr.decls) is skipped to avoid
	// duplicate output; only non-trivial computed sub-expressions add new information.
	for _, c := range conjuncts {
		vcRegisterCompareSubtermsAsCE(tr, c)
	}
	obligations := make([]string, len(conjuncts))
	for i, c := range conjuncts {
		obligations[i] = emitVCFormula(c)
	}
	return a.smtCheckVCMulti(tr, obligations, extraHyps)
}

// vcRegisterCompareSubtermsAsCE walks a vcFormula looking for vcCompare nodes and registers each
// non-trivial operand (not a literal, not a plain free variable) as a ceExpr in the translator so the
// Sat model read-back can report the concrete computed value. For example, a conjunct `x/2 >= 0`
// registers the LHS `(div v_x 2)` under the label "x/2" so the counterexample prints "x/2=-1, x=-2"
// rather than only "x=-2". Literals and simple variables are skipped (they add no new information).
func vcRegisterCompareSubtermsAsCE(tr *smtTranslator, f vcFormula) {
	switch ff := f.(type) {
	case vcCompare:
		vcRegisterTermAsCE(tr, ff.L)
		vcRegisterTermAsCE(tr, ff.R)
	case vcAnd:
		vcRegisterCompareSubtermsAsCE(tr, ff.L)
		vcRegisterCompareSubtermsAsCE(tr, ff.R)
	case vcNot:
		vcRegisterCompareSubtermsAsCE(tr, ff.Arg)
	}
}

// vcRegisterTermAsCE registers a single vcTerm as a ceExpr when it carries new information:
// literals and plain free variables already appear in the model (as declared SMT vars), so they are
// skipped. Compound terms (arithmetic, opaque sub-expressions) are registered under a readable label
// derived from the Elisa-level names of the operands. vcArith is handled structurally (before
// emitting the potentially-wrapped SMT form) so we can derive a clean "x+offset" label even when the
// emitted SMT is a complex wrapped expression.
func vcRegisterTermAsCE(tr *smtTranslator, t vcTerm) {
	switch tt := t.(type) {
	case vcIntLit:
		return // constant — no new information
	case vcVar:
		return // plain free variable — already in tr.decls, already rendered
	case vcOldEntry:
		return // entry-value snapshot — not a computed result
	case vcArith:
		// Derive a readable label from the Elisa names of the operands (before the wrap is applied).
		lName := vcTermElisaName(tr, tt.L)
		rName := vcTermElisaName(tr, tt.R)
		elisa := ceOpElisa(tt.Op)
		if elisa != "" && (lName != "" || rName != "") {
			if lName == "" {
				lName = emitVCTerm(tt.L) // fall back to the SMT literal (e.g. "3")
			}
			if rName == "" {
				rName = emitVCTerm(tt.R)
			}
			label := lName + elisa + rName
			smt := emitVCTerm(t)
			if tr.ceExprs[label] == "" {
				tr.ceExprs[label] = smt
			}
		}
	case vcOpaque:
		smt := tt.SMT
		if tr.counterexampleLabelForTerm(smt) != "" {
			return // the opaque term IS a known decl — skip to avoid duplication
		}
		label := ceTermLabel(smt, tr)
		if label != "" && tr.ceExprs[label] == "" {
			tr.ceExprs[label] = smt
		}
	default:
		smt := emitVCTerm(t)
		if tr.counterexampleLabelForTerm(smt) != "" {
			return
		}
		label := ceTermLabel(smt, tr)
		if label != "" && tr.ceExprs[label] == "" {
			tr.ceExprs[label] = smt
		}
	}
}

// vcTermElisaName returns the Elisa source name of a term when it is a simple variable (vcVar) or an
// opaque term that maps back to a known decl, or "" when the term is composite/unknown. Used to
// construct readable "x+offset" labels for vcArith sub-expressions.
func vcTermElisaName(tr *smtTranslator, t vcTerm) string {
	switch tt := t.(type) {
	case vcVar:
		return tt.Name
	case vcOpaque:
		return tr.counterexampleLabelForTerm(tt.SMT)
	default:
		return ""
	}
}

// ceTermLabel derives a short, readable label for an SMT sub-term to use as a counterexample key.
// We look for the pattern "(<op> <var> <const>)" or "(div v_x 2)" and produce "x/2", "x*3", etc.
// For unrecognized patterns we fall back to a short prefix of the SMT text (capped to keep the
// diagnostic compact). The label is sanitised so it round-trips through smtCounterexampleExprVar.
// Returns "" when the term is too short/simple to warrant a separate label.
func ceTermLabel(smt string, tr *smtTranslator) string {
	if len(smt) <= 3 || smt == "0" || smt == "1" {
		return "" // too trivial
	}
	// Try "(op L R)" where one operand is a simple SMT variable that maps back to an Elisa name.
	if len(smt) > 2 && smt[0] == '(' && smt[len(smt)-1] == ')' {
		inner := smt[1 : len(smt)-1]
		fields := strings.Fields(inner)
		if len(fields) == 3 {
			op, l, r := fields[0], fields[1], fields[2]
			lLabel := tr.counterexampleLabelForTerm(l)
			rLabel := tr.counterexampleLabelForTerm(r)
			elisa := ceOpElisa(op)
			if elisa != "" {
				lName := lLabel
				if lName == "" {
					lName = l // keep raw (e.g. a literal constant)
				}
				rName := rLabel
				if rName == "" {
					rName = r
				}
				// Only emit a label when at least one operand maps to an Elisa name, so the label
				// is meaningful rather than a raw SMT term ("v_x+v_offset" is useful; "3+5" is not).
				if lLabel != "" || rLabel != "" {
					return lName + elisa + rName
				}
			}
		}
	}
	// The term is a complex expression we cannot label readably — skip it.
	// Only readable labels (derived from known Elisa variable names) add diagnostic value;
	// a raw SMT string as a label would confuse rather than help.
	return ""
}
