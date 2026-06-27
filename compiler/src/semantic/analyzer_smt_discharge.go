package semantic

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"elisacore/src/ast"
	"elisacore/src/lexer"
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
	Enabled      bool
	Attempts     int           // obligations handed to the tier (linear declined)
	Proven       int           // unsat → proven
	Declined     int           // sat/unknown → fell back to runtime
	SolverProven int           // == Proven, kept for clarity in the report
	CacheHits    int           // obligations answered from the query cache (no solver round-trip)
	SpawnTime    time.Duration // one-time solver process start
	SolverTime   time.Duration // wall time inside the solver across all queries
	Slowest      time.Duration // slowest single query
}

// String renders the profile for the --explain report (empty when the tier is off).
func (p SMTStats) String() string {
	if !p.Enabled {
		return ""
	}
	return fmt.Sprintf(
		"SMT tier: %d obligations, %d proven, %d declined, %d cached; solver %.1fms (spawn %.1fms, slowest %.1fms)",
		p.Attempts, p.Proven, p.Declined, p.CacheHits,
		float64(p.SolverTime.Microseconds())/1000.0,
		float64(p.SpawnTime.Microseconds())/1000.0,
		float64(p.Slowest.Microseconds())/1000.0,
	)
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
		// A generous per-query ceiling: a single obligation that the solver can't crack quickly is
		// not worth stalling the compile — it times out to Unknown and we use the runtime check.
		PerQueryTimeoutMillis: 2000,
	})
	if err != nil || solver == nil {
		a.smtUnavailable = true
		a.smtStats.Enabled = true
		return nil
	}
	a.smtSolver = solver
	a.smtStats.Enabled = true
	a.smtStats.SpawnTime = solver.Stats().SpawnMillis
	return solver
}

// closeSMT shuts down the solver and folds its harness stats into the profile.
func (a *Analyzer) closeSMT() {
	if a.smtSolver == nil {
		return
	}
	st := a.smtSolver.Stats()
	a.smtStats.SolverTime = st.Total
	a.smtStats.Slowest = st.Slowest
	a.smtStats.SpawnTime = st.SpawnMillis
	_ = a.smtSolver.Close()
	a.smtSolver = nil
}

// trySMTProveRefinement attempts to discharge `value is law[predArgs]` with the solver. Returns true
// only on a sound proof (the solver reported unsat on the negated obligation). It is called after the
// linear tier declines, so the subject is genuinely outside the affine fragment (e.g. a var*var
// product) — precisely where the solver earns its keep.
func (a *Analyzer) trySMTProveRefinement(value ast.Expr, decl *ast.FuncDecl, predArgs []ast.Expr) bool {
	solver := a.openSMT()
	if solver == nil || decl == nil || len(decl.Params) == 0 {
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
			tr.paramConsts[pname] = c
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
	if tr.isArrayLike(a.exprTypes[value]) {
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
			if inst == nil {
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
	if t := a.exprTypes[n.Object]; t != nil {
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
	hyps := a.smtRequiresHypotheses(tr) + a.smtImmutableLocalHypotheses(tr) + a.smtAssertHypotheses(tr) + a.smtFlowFactHypotheses(tr) + a.smtProtocolLawHypotheses(tr) + a.smtNonNullRefParamHypotheses(tr) + extraHyps
	for _, obligation := range obligations {
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

// ceOpElisa maps an SMT arithmetic operator to its Elisa infix symbol for readable labels.
func ceOpElisa(op string) string {
	switch op {
	case "div":
		return "/"
	case "mod":
		return "%"
	case "*":
		return "*"
	case "+":
		return "+"
	case "-":
		return "-"
	default:
		return ""
	}
}

// smtCheckGoal lowers a boolean obligation expression into the VC IR, then discharges it via the brick-4
// splitter: a conjunctive goal is proven conjunct-by-conjunct against a shared hypothesis context, and a
// goal that constant-folds to `true` concludes with NO solver call. The third return reports whether the
// goal could be lowered/translated at all (false ⇒ outside the fragment, the caller declines).
func (a *Analyzer) smtCheckGoal(tr *smtTranslator, goalExpr ast.Expr, env map[string]string, extraHyps string) (proven bool, counterexample string, lowered bool) {
	goal, ok := tr.lowerVCFormula(goalExpr, env)
	if !ok {
		return false, "", false
	}
	p, ce := a.smtDischargeFormula(tr, goal, extraHyps)
	if !p {
		if qce := a.quantifierCounterexampleAny(tr, goalExpr, env, extraHyps); qce != "" {
			ce = qce
		}
	}
	return p, ce, true
}

func (a *Analyzer) smtQuantifierCounterexample(tr *smtTranslator, goalExpr ast.Expr, env map[string]string, extraHyps string) string {
	q, ok := stripOptimizationParens(goalExpr).(*ast.QuantifierExpr)
	if !ok || q == nil || q.Exists || q.In != nil || len(q.Vars) == 0 {
		return ""
	}
	qenv := make(map[string]string, len(env)+len(q.Vars))
	for k, v := range env {
		qenv[k] = v
	}
	for _, v := range q.Vars {
		sym := "q_" + v
		qenv[v] = sym
		tr.auxDecls = append(tr.auxDecls, "(declare-const "+sym+" Int)\n")
		tr.ceExprs[v] = sym
	}
	tr.collectQuantifierWitnessSelects(q.Body, qenv)
	body, ok := tr.boolTerm(q.Body, qenv)
	if !ok {
		return ""
	}
	hyps := a.smtRequiresHypotheses(tr) + a.smtImmutableLocalHypotheses(tr) + a.smtAssertHypotheses(tr) + a.smtFlowFactHypotheses(tr) + a.smtProtocolLawHypotheses(tr) + a.smtNonNullRefParamHypotheses(tr) + extraHyps
	proven, ce := a.smtCheckQuery(tr, hyps, body)
	if proven {
		return ""
	}
	return ce
}

// smtDischargeFormulaWithHyps discharges a lowered VC-IR formula against a CALLER-SUPPLIED hypothesis
// block (already built from `tr`), rather than the analyzer's standard set. It is the loop-preservation
// prover's bridge into the central VC IR: that prover deliberately assumes ONLY the loop condition +
// invariants (loop variables free, no ambient assert/flow facts), so it cannot route through smtCheckVC
// (which layers the standard block). Routing through the IR gains the smart-constructor folder — a
// goal that constant-folds to `true` is valid under ANY hypotheses and concludes with NO solver call,
// and structural comparisons/arithmetic are normalized — while keeping emission byte-identical for
// every atom (opaque leaves emit their already-translated string verbatim), so the proof verdict is
// preserved for goals the folder does not touch.
func (a *Analyzer) smtDischargeFormulaWithHyps(tr *smtTranslator, goal vcFormula, hyps string) (bool, string) {
	if isVCTrue(goal) {
		a.smtStats.Attempts++
		a.smtStats.Proven++
		return true, ""
	}
	return a.smtCheckQuery(tr, hyps, emitVCFormula(goal))
}

// smtCheckQuery is the innermost discharge primitive: given the full hypothesis block (already built
// from `tr`) and an `obligation`, it lays out `factPreamble + hyps + (assert (not obligation))`, asks
// the solver, updates the stats, and returns proven (only on `unsat`) plus a counterexample on `sat`.
// smtCheckVC layers the standard hypothesis set on top; the loop-preservation prover uses this directly
// with its OWN hypothesis set (loop variables free, no ambient facts) — so the check protocol itself is
// shared while each site keeps control of which facts it assumes. `factPreamble()` is emitted last so
// every symbol the hypotheses and obligation introduced is declared.
func (a *Analyzer) smtCheckQuery(tr *smtTranslator, hyps string, obligation string) (bool, string) {
	solver := a.openSMT()
	if solver == nil || tr == nil {
		return false, ""
	}
	query := tr.factPreamble() + hyps + "(assert (not " + obligation + "))\n" + tr.counterexampleExprDefinitions()
	a.smtStats.Attempts++
	// Query cache (brick 5): an identical query has the same deterministic verdict, so serve it without a
	// solver call. Attempts and Proven/Declined still count (the obligation was discharged), but no solver
	// time accrues — the cache hit is the whole point. SolverProven is left to mean "proven by an actual
	// solver run", so a cached proof does NOT bump it.
	if hit, ok := a.smtQueryCache[query]; ok {
		a.smtStats.CacheHits++
		if hit.proven {
			a.smtStats.Proven++
		} else {
			a.smtStats.Declined++
		}
		return hit.proven, hit.ce
	}
	res, model, _ := solver.CheckValues(query, tr.declaredSMTVars())
	if res == smt.Unsat {
		a.smtStats.Proven++
		a.smtStats.SolverProven++
		a.cacheSMTQuery(query, smtQueryResult{proven: true})
		return true, ""
	}
	a.smtStats.Declined++
	if res == smt.Sat {
		ce := tr.counterexample(model)
		a.cacheSMTQuery(query, smtQueryResult{proven: false, ce: ce})
		return false, ce
	}
	// `unknown` (timeout / incompleteness) is NOT cached: it is not a stable verdict — a later identical
	// query is free to try again (e.g. the solver's internal state differs), and caching it would lock in
	// a decline that a retry might resolve. Only the definitive unsat/sat answers memoize.
	return false, ""
}

// smtQueryResult is a memoized solver verdict: whether the obligation was proven, plus the
// counterexample text rendered on a `sat` decline (empty otherwise).
type smtQueryResult struct {
	proven bool
	ce     string
}

func (a *Analyzer) cacheSMTQuery(query string, result smtQueryResult) {
	if a.smtQueryCache == nil {
		a.smtQueryCache = map[string]smtQueryResult{}
	}
	a.smtQueryCache[query] = result
}

// trySMTProveRequires discharges a precondition clause with the solver after the linear clause prover
// declined. The clause references the callee's parameters; each is translated to its caller argument
// term (populating the caller's free variables), and the clause obligation is checked against the
// caller's facts. Returns true only on `unsat` of the negation (a sound proof).
func (a *Analyzer) trySMTProveRequires(clause ast.Expr, subst map[string]ast.Expr) (bool, string) {
	solver := a.openSMT()
	if solver == nil || clause == nil {
		return false, ""
	}
	tr := a.newSMTTranslator(nil)
	// When `result` is bound to a struct literal (`return Pair(1, 2)`), it has no scalar SMT term, so
	// drop it from the env subst (which would otherwise decline) and instead resolve each `result.field`
	// read to that field's construction argument — so `ensure result.a == 1` discharges.
	substForEnv := subst
	if res, ok := subst["result"]; ok {
		if sl, ok := stripOptimizationParens(res).(*ast.StructLitExpr); ok {
			tr.resultFields = a.structLitFieldMap(sl)
			substForEnv = map[string]ast.Expr{}
			for k, v := range subst {
				if k != "result" {
					substForEnv[k] = v
				}
			}
		}
	}
	env, ok := a.smtEnvForSubst(tr, substForEnv)
	if !ok {
		return false, ""
	}
	// Register compound substituted expressions as counterexample readbacks so a Sat model reports
	// the CONCRETE VALUE of each callee parameter at the refuting input, not only the free caller
	// variables that compose it. For example, when `result` is substituted with `x + offset` (SMT:
	// the wrapped sum), the diagnostic shows "result=-1, offset=-1, x=0" instead of only
	// "offset=-1, x=0" — naming the callee-parameter expression that actually violates the clause.
	// Simple bindings (where the SMT term is already a declared free variable) are skipped because
	// they duplicate an entry already in tr.decls.
	trRegisterSubstCEExprs(tr, env)
	// Emit a structural null-ness fact for `result` when its null-ness is definitively known from the
	// return expression's type or value. This is EXTRA (not in the standard hypothesis set) because it
	// is substitution-specific: on a literal-null return `isnull_result` is true, on a T& (non-optional
	// ref) return it is false. The standard set does not know which return site is active.
	extra := a.smtResultNullnessHypothesis(tr, substForEnv)
	// Lower the clause to the VC IR and discharge against the standard hypothesis set (the enclosing
	// function's `requires` — docs/90 brick 90-13 — its immutable-local defining equalities, flow
	// assert-facts, and range facts). On sat the returned model is an input permitted by the caller's
	// known facts that violates the precondition — a concrete witness for the diagnostic.
	proven, counterexample, ok := a.smtCheckGoal(tr, clause, env, extra)
	if !ok {
		return false, ""
	}
	return proven, counterexample
}

// trRegisterSubstCEExprs registers env entries whose SMT value is a compound expression (not a plain
// declared free variable) as ceExprs in `tr`, so that a Sat model read-back reports the concrete
// computed value for each substituted callee parameter. This enriches the diagnostic beyond the bare
// caller free-variable assignments: instead of only "x=0, offset=-1", it also shows "result=-1".
// Entries already in tr.decls (plain `v_<name>` symbols) are skipped to avoid duplicate output.
// Only integer-typed entries can be ceExprs (the define-fun emits `() Int`); boolean or array terms
// are silently skipped (they cannot be defined as Int constants).
func trRegisterSubstCEExprs(tr *smtTranslator, env map[string]string) {
	if tr == nil || len(env) == 0 {
		return
	}
	// Sort for deterministic output (env iteration order varies).
	labels := make([]string, 0, len(env))
	for k := range env {
		labels = append(labels, k)
	}
	sort.Strings(labels)
	for _, label := range labels {
		smt := env[label]
		if smt == "" {
			continue
		}
		// Only register compound integer-valued SMT expressions. We must avoid:
		//   - dtToken markers (internal, not valid SMT): start with __dt__
		//   - boolean-valued terms (define-fun uses Int; Bool would be a type error)
		//   - array/set/dict-sorted terms (they are Array-sorted in SMT, not Int)
		//   - simple variable names (already in tr.decls output — would duplicate)
		//   - numeric integer literals (trivial, no added information)
		// The safest positive criterion: the value must start with `(` (is a compound SMT s-expr)
		// and must NOT start with a known boolean-connective or comparison prefix.
		if !strings.HasPrefix(smt, "(") {
			continue // simple var, literal, token — skip
		}
		if strings.HasPrefix(smt, "(and ") || strings.HasPrefix(smt, "(or ") ||
			strings.HasPrefix(smt, "(not ") || strings.HasPrefix(smt, "(= ") ||
			strings.HasPrefix(smt, "(distinct ") || strings.HasPrefix(smt, "(< ") ||
			strings.HasPrefix(smt, "(> ") || strings.HasPrefix(smt, "(<= ") ||
			strings.HasPrefix(smt, "(>= ") || strings.HasPrefix(smt, "(ite ") {
			continue // boolean or conditional — skip (ite can return Int but is too complex to type)
		}
		// Skip array/set/dict terms: they are Array-sorted, not Int.
		isArrayLike := false
		for arrName := range tr.arrayDecls {
			if smt == smtVar(arrName) {
				isArrayLike = true
				break
			}
		}
		if !isArrayLike {
			for sym := range tr.setDecls {
				if smt == sym {
					isArrayLike = true
					break
				}
			}
		}
		if !isArrayLike {
			for sym := range tr.dictDecls {
				if smt == sym || smt == sym+"_vals" {
					isArrayLike = true
					break
				}
			}
		}
		if isArrayLike {
			continue
		}
		// If the term IS the SMT variable for a declared name (already in tr.decls), skip — it
		// would duplicate the decl output in counterexample().
		if tr.counterexampleLabelForTerm(smt) != "" {
			continue
		}
		// Only register when the label is a simple Elisa identifier (no struct-field suffix), to keep
		// the output readable. The __field__ key used for struct-field bindings is not shown.
		if strings.Contains(label, "__field__") || strings.HasPrefix(label, "__") {
			continue
		}
		if _, alreadyPresent := tr.ceExprs[label]; !alreadyPresent {
			tr.ceExprs[label] = smt
		}
	}
}

func (a *Analyzer) trySMTProveEnsureFromReturnCall(clause ast.Expr, call *ast.CallExpr) bool {
	solver := a.openSMT()
	if solver == nil || clause == nil || call == nil {
		return false
	}
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok || decl == nil || len(decl.EnsureValues) == 0 {
		return false
	}
	tr := a.newSMTTranslator(nil)
	retSym := "__return_call_result"
	retTerm := smtVar(retSym)
	tr.decls[retSym] = true
	callerEnv := map[string]string{"result": retTerm}
	obligation, ok := tr.boolTerm(clause, callerEnv)
	if !ok {
		return false
	}
	subst := map[string]ast.Expr{}
	args := proofCallArgs(call)
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	calleeEnv, ok := a.smtEnvForCallSubst(tr, subst)
	if !ok {
		return false
	}
	calleeEnv["result"] = retTerm
	guard := "true"
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		h, ok := tr.boolTerm(req, calleeEnv)
		if !ok {
			return false
		}
		if guard == "true" {
			guard = h
		} else {
			guard = "(and " + guard + " " + h + ")"
		}
	}
	var calleeHyps strings.Builder
	for _, ensure := range decl.EnsureValues {
		if ensure == nil {
			continue
		}
		if h, ok := tr.boolTerm(ensure, calleeEnv); ok {
			if guard == "true" {
				calleeHyps.WriteString("(assert " + h + ")\n")
			} else {
				calleeHyps.WriteString("(assert (=> " + guard + " " + h + "))\n")
			}
		}
	}
	if calleeHyps.Len() == 0 {
		return false
	}
	// The called function's poststates (`calleeHyps`) are the site-specific extra hypotheses on top of
	// the standard set. Discharge through the central chokepoint (the counterexample is unused here).
	proven, _ := a.smtCheckVC(tr, obligation, calleeHyps.String())
	return proven
}

func (a *Analyzer) smtEnvForSubst(tr *smtTranslator, subst map[string]ast.Expr) (map[string]string, bool) {
	env := map[string]string{}
	for name, argExpr := range subst {
		if !a.bindSMTEnvArg(tr, env, name, argExpr, nil, true) {
			return nil, false
		}
	}
	return env, true
}

// isDatatypeValuedType reports whether a type is an enum/struct aggregate value (no scalar SMT term).
// Used to bind datatype substitutions to a projection token rather than a fabricated integer var.
func isDatatypeValuedType(t Type) bool {
	if t == nil {
		return false
	}
	switch stripRefForBounds(t).(type) {
	case *EnumType, *StructType:
		return true
	default:
		return false
	}
}

func (a *Analyzer) smtEnvForCallSubst(tr *smtTranslator, subst map[string]ast.Expr) (map[string]string, bool) {
	return a.smtEnvForCallSubstEnv(tr, subst, nil)
}

func (a *Analyzer) smtEnvForCallSubstEnv(tr *smtTranslator, subst map[string]ast.Expr, argEnv map[string]string) (map[string]string, bool) {
	env := map[string]string{}
	for name, argExpr := range subst {
		if !a.bindSMTEnvArg(tr, env, name, argExpr, argEnv, false) {
			return nil, false
		}
	}
	return env, true
}

func (a *Analyzer) bindSMTEnvArg(tr *smtTranslator, env map[string]string, name string, argExpr ast.Expr, argEnv map[string]string, requireBinding bool) bool {
	// A literal-null substitution (e.g. `result` on a `return null` path) has no scalar SMT term. It is
	// consumed structurally by the null-compare lowering (which keys an `isnull_<name>` predicate), so
	// leave the env entry unbound rather than failing the whole env build.
	if _, ok := stripOptimizationParens(argExpr).(*ast.NullLit); ok {
		return true
	}
	if lit, ok := argExpr.(*ast.BoolLit); ok {
		if lit.Value {
			env[name] = "true"
		} else {
			env[name] = "false"
		}
		return true
	}
	if IsBoolType(a.exprTypes[argExpr]) {
		bterm, ok := tr.boolTerm(argExpr, argEnv)
		if !ok {
			return false
		}
		env[name] = bterm
		return true
	}
	if tr.isArrayLike(a.exprTypes[argExpr]) {
		arr, ok := tr.arrayTermEnv(argExpr, argEnv)
		if !ok {
			return false
		}
		env[name] = arr
		return true
	}
	if a.smtEquationStructType(a.exprTypes[argExpr]) {
		return a.bindSMTStructEnvArg(tr, env, name, argExpr, argEnv, requireBinding)
	}
	// A datatype value (enum, or a struct not modeled field-wise above) has no scalar SMT term; bind a
	// recoverable projection token so a bool predicate over it canonicalizes to the SAME opaque symbol
	// as the un-substituted occurrence (structural induction). Tokens only key opaque predicate symbols;
	// they never enter a numeric SMT term (the type checker forbids datatype operands in arithmetic).
	if isDatatypeValuedType(a.exprTypes[argExpr]) {
		env[name] = dtToken(smtProjectionName(argExpr))
		return true
	}
	// A pointer-like value (nullable optional / reference, e.g. a `void&?` return on a `result == null`
	// disjunct) has no scalar SMT term either. It too is consumed structurally by the null-compare
	// lowering (keyed by the substituted name, not by an env term), so leave it unbound rather than
	// failing the env build. SOUND: an unbound entry contributes no fact; only the null-compare path
	// (which folds against the literal/flow facts) can conclude anything about it.
	if a.exprIsPointerLike(a.exprTypes[argExpr]) {
		return true
	}
	// A bound type-param-typed value (the receiver of a protocol-method call in a behavioral-subtyping
	// contract, e.g. `self`↦`c` where `c: C`) has no scalar SMT term, but it must still bind to a stable
	// projection token so an abstract method call `c.count()` keyed by its receiver canonicalizes the
	// same on both the obligation and the caller-fact side (docs P1). SOUND: the token only ever keys an
	// uninterpreted method-result symbol; it never enters a numeric term.
	if _, ok := stripRefForBounds(a.exprTypes[argExpr]).(*TypeParamType); ok {
		env[name] = dtToken(smtProjectionName(argExpr))
		return true
	}
	// Only early-decline when the type is KNOWN to be non-exact. A substitution expr captured before its
	// body is type-analyzed (e.g. a loop body's `i+1`) has a nil exprTypes entry; falling through to
	// termEnv resolves it via scope, which is what the loop-invariant preserve proof needs.
	if t := a.exprTypes[argExpr]; t != nil && !isSMTExactAssignmentType(t) {
		return !requireBinding
	}
	term, ok := tr.termEnv(argExpr, argEnv)
	if !ok {
		return false
	}
	env[name] = term
	return true
}

func (a *Analyzer) bindSMTStructEnvArg(tr *smtTranslator, env map[string]string, name string, argExpr ast.Expr, argEnv map[string]string, requireBinding bool) bool {
	st, ok := stripRefForBounds(a.exprTypes[argExpr]).(*StructType)
	if !ok || st == nil {
		return !requireBinding
	}
	for _, fieldName := range sortedFieldNames(st.Fields) {
		field := st.Fields[fieldName]
		ft := stripRefForBounds(field.Type)
		fieldExpr := &ast.FieldExpr{Object: argExpr, Field: fieldName}
		key := name + "__field__" + fieldName
		if IsBoolType(ft) {
			bterm, ok := tr.boolTerm(fieldExpr, argEnv)
			if !ok {
				return false
			}
			env[key] = bterm
			continue
		}
		if IsNumericType(ft) && !IsFloatType(ft) {
			term, ok := tr.termEnv(fieldExpr, argEnv)
			if !ok {
				return false
			}
			env[key] = term
			continue
		}
		if requireBinding {
			return false
		}
	}
	return true
}

// dtTokenPrefix marks an env binding as a DATATYPE-projection token (not an SMT term). Only
// boolPredicateArgToken reads these; arithmetic/comparison lowering never sees a datatype-typed operand
// (the type checker forbids it), so the marker can never leak into a numeric SMT term.
const dtTokenPrefix = "__dt__"

func dtToken(projection string) string { return dtTokenPrefix + projection }

// smtRequiresHypotheses translates the enclosing function's `requires` clauses into SMT assertions
// (non-negated — they are assumed), using the given translator so free variables and arrays share the
// obligation's symbols. A clause outside the fragment is silently skipped (sound: fewer assumptions is
// conservative). Returns the concatenated `(assert …)` lines.
func (a *Analyzer) smtRequiresHypotheses(tr *smtTranslator) string {
	if a.currentFuncDecl == nil {
		return ""
	}
	if a.inClosedWorldProof {
		return "" // docs/99: the enclosing `requires` is an ambient fact, walled out of a scoped proof.
	}
	var b strings.Builder
	for _, req := range a.currentFuncDecl.Requires {
		if req == nil {
			continue
		}
		// SOUNDNESS: a `requires` constrains a parameter's ENTRY value. For an immutable variable that
		// stays true everywhere, so it is asserted directly here. But a clause over a MUTABLE root (a
		// mutable param/local, or a field reachable through a `mutable T&` param) is only valid until that
		// root is mutated — re-asserting it unconditionally after `m <- 0` would prove a false postcondition
		// about the new value. Such clauses are seeded as flow assert-facts at function entry
		// (seedRequiresAsAssertFacts) and ride the standard mutation-invalidation instead, so they are
		// skipped here to avoid the stale unconditional copy.
		if a.requiresReferencesMutableRoot(req) {
			continue
		}
		tr.requiresHypMode = true
		h, ok := tr.boolTerm(req, nil)
		tr.requiresHypMode = false
		if ok {
			b.WriteString("(assert " + h + ")\n")
		}
	}
	return b.String()
}

// requiresReferencesMutableRoot reports whether a `requires`/contract clause reads any variable whose
// symbol is MUTABLE (a mutable param/local, or — for a field path like `b.v` — a root bound to a
// `mutable T&` param). Such a clause's entry-value guarantee can be invalidated by a body mutation, so
// it is tracked through the invalidation-aware assert-fact channel rather than asserted unconditionally.
func (a *Analyzer) requiresReferencesMutableRoot(clause ast.Expr) bool {
	if a == nil || a.currentScope == nil || clause == nil {
		return false
	}
	for root := range smtFactDeps(clause) {
		if sym, ok := a.currentScope.Lookup(root); ok && symbolRootMutable(sym) {
			return true
		}
	}
	return false
}

// symbolRootMutable reports whether a symbol's value (or the place it points at) can be changed by a
// body mutation. A by-value mutable local/param qualifies (sym.Mutable). So does a MUTABLE-REFERENCE
// parameter (`s: mutable S&`): paramIsMutable leaves sym.Mutable FALSE there (the `mutable` only allows
// rebinding the ref), but the POINTEE is writable — `s.x <- …` / `bump(&s.x)` change what a `requires`
// over that place reads — so its entry-value guarantee is mutable and must ride the invalidation channel.
func symbolRootMutable(sym *Symbol) bool {
	if sym == nil {
		return false
	}
	if sym.Mutable {
		return true
	}
	if rt, ok := sym.Type.(*RefType); ok && rt != nil && rt.Mutable {
		return true
	}
	return false
}

// seedRequiresAsAssertFacts records every `requires` clause over a MUTABLE root as a flow assert-fact in
// the current (function body) scope, so it is assumed while the root holds its entry value and DROPPED
// by the standard mutation invalidation the moment the root is written — the exact lifetime a precondition
// has. Immutable-root clauses are left to smtRequiresHypotheses (always valid, no invalidation needed).
func (a *Analyzer) seedRequiresAsAssertFacts(fn *ast.FuncDecl) {
	if a == nil || fn == nil || a.currentScope == nil {
		return
	}
	for _, req := range fn.Requires {
		if req != nil && a.requiresReferencesMutableRoot(req) {
			a.recordSMTAssertFact(req)
		}
	}
	a.seedSelfStructInvariantsAsAssertFacts(fn)
	a.checkRequiresVacuity(fn)
}

// checkRequiresVacuity flags a function whose `requires` preconditions are PROVABLY contradictory
// (their conjunction is unsatisfiable). Such a function's body assumes its own `requires` as
// hypotheses, so contradictory preconditions let it vacuously "prove" ANY `ensure` postcondition. This
// is sound today only because every call site must itself discharge the precondition (which it never
// can), but it is a latent leak for exported / entry / uncalled functions where no call site exists to
// catch the impossibility — and it is almost always a real authoring bug regardless.
//
// SOUNDNESS OF THE CHECK ITSELF (mirrors the discharge ladder's "only unsat concludes" discipline):
// we ask the solver whether the CONJUNCTION of the `requires` clauses is satisfiable, and flag ONLY
// when it answers `unsat`. `sat` (a witnessing model exists), `unknown` (timeout/incompleteness), a
// clause outside the SMT fragment, or no solver at all all leave the function unflagged — so a
// tight-but-satisfiable precondition is never a false positive. A clause we cannot translate is simply
// dropped from the conjunction (a weaker assertion can only become MORE satisfiable, never less), so a
// partial translation can lose a true contradiction but can never fabricate one.
func (a *Analyzer) checkRequiresVacuity(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Requires) == 0 {
		return
	}
	solver := a.openSMT()
	if solver == nil {
		return // SMT off / unavailable — decline, exactly like the discharge tier.
	}
	tr := a.newSMTTranslator(nil)
	var b strings.Builder
	asserted := 0
	for _, req := range fn.Requires {
		if req == nil {
			continue
		}
		tr.requiresHypMode = true
		h, ok := tr.boolTerm(req, nil)
		tr.requiresHypMode = false
		if !ok {
			continue // outside the SMT fragment — drop (keeps the conjunction weaker, never stronger).
		}
		b.WriteString("(assert " + h + ")\n")
		asserted++
	}
	if asserted < 1 {
		return // nothing translatable to contradict.
	}
	query := tr.factPreamble() + b.String()
	res, _ := solver.Check(query)
	if res != smt.Unsat {
		return // sat / unknown / error → not proven contradictory → do NOT flag.
	}
	a.proofLint(fn.Position, "the `requires` preconditions of %q are contradictory (their conjunction is unsatisfiable); the function can never be called and its body can vacuously prove any `ensure`", fn.Name)
}

// seedSelfStructInvariantsAsAssertFacts assumes the struct invariants of every struct-typed parameter
// (e.g. `self: Box&`) as flow-assert facts at function entry — the data-structure analogue of a
// `requires` precondition. This is what lets a method `ensure result == self.model` (a refinement of
// the concrete representation onto an abstract GHOST model field) discharge: the invariant
// `self.concrete == self.model` is a well-formedness contract the caller already established, so the
// callee may assume it. Sound: a struct invariant must hold on entry to any method (it is checked at
// construction and after each field store in debug builds), so assuming it here is no stronger than the
// existing runtime contract. Dropped on mutation of the param like any other entry fact.
func (a *Analyzer) seedSelfStructInvariantsAsAssertFacts(fn *ast.FuncDecl) {
	if a == nil || fn == nil {
		return
	}
	for _, param := range a.expandedFuncDeclParams(fn) {
		if param.Name == "" || param.Type == nil {
			continue
		}
		st := structTypeOfParam(a.resolveType(param.Type))
		if st == nil || len(st.Invariants) == 0 {
			continue
		}
		for _, inv := range st.Invariants {
			if inv == nil {
				continue
			}
			// Rewrite `self.<field>` in the invariant onto the actual parameter name so the fact is over
			// the real binding (a no-op when the param is literally `self`).
			a.recordSMTAssertFact(rewriteSelfFieldRoot(inv, param.Name))
		}
	}
}

// structTypeOfParam unwraps a reference/generic-instance parameter type down to its underlying struct.
func structTypeOfParam(t Type) *StructType {
	switch v := t.(type) {
	case *RefType:
		return structTypeOfParam(v.Elem)
	case *StructType:
		return v
	case *GenericInstanceType:
		if st, ok := v.Base.(*StructType); ok {
			return st
		}
	}
	return nil
}

// rewriteSelfFieldRoot clones a struct-invariant expression, replacing the implicit `self` receiver
// root with `paramName`. When paramName == "self" it returns the clause unchanged.
func rewriteSelfFieldRoot(expr ast.Expr, paramName string) ast.Expr {
	if paramName == "self" || paramName == "" {
		return expr
	}
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name == "self" {
			return &ast.Ident{Position: n.Position, Name: paramName}
		}
		return n
	case *ast.FieldExpr:
		return &ast.FieldExpr{Position: n.Position, Object: rewriteSelfFieldRoot(n.Object, paramName), Field: n.Field, Safe: n.Safe}
	case *ast.ParenExpr:
		return &ast.ParenExpr{Position: n.Position, Inner: rewriteSelfFieldRoot(n.Inner, paramName)}
	case *ast.BinaryExpr:
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: rewriteSelfFieldRoot(n.Left, paramName), Right: rewriteSelfFieldRoot(n.Right, paramName)}
	case *ast.UnaryExpr:
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: rewriteSelfFieldRoot(n.Operand, paramName)}
	default:
		return expr
	}
}

// smtFlowFactHypotheses asserts the scope's flow range-facts — branch-derived bounds on
// immutable variables (`if alignment == 0: return` ⟹ `alignment >= 1` afterwards; `if n < cap`
// ⟹ `n <= cap-1` in the then-branch) — as SMT hypotheses. These are already soundly
// flow-scoped and immutable-only (the linear prover uses them at the same program point), so
// surfacing them to the SMT tier lets branchy and loop-exit reasoning discharge (docs/85 gap #3:
// loop-carried/flow facts). A fact with no known bound contributes nothing.
func (a *Analyzer) smtFlowFactHypotheses(tr *smtTranslator) string {
	// Range facts flow through the unified hypothesis renderer (analyzer_fact.go). Emission stays in
	// sorted name order with closer-scope shadowing so the hypothesis text (and thus the cache key —
	// brick 5) is byte-identical to the previous hand-rolled walk.
	return a.renderHypothesisFacts(tr, collectScopeRangeFacts, true)
}

type smtFact struct {
	Expr ast.Expr
	Deps map[string]bool
}

// smtAssertHypotheses translates flow-local proof facts into SMT assumptions. A fact may come from a
// branch guard, a proven invariant/assertion, or an exact assignment equality. Facts carry dependency
// roots and are invalidated on mutation of any root; calls still clear them conservatively.
func (a *Analyzer) smtAssertHypotheses(tr *smtTranslator) string {
	// Assert facts flow through the same unified renderer as range facts (analyzer_fact.go), with no
	// name dedupe (assert facts are emitted per-occurrence). docs/99: a closed-world scope is a wall.
	return a.renderHypothesisFacts(tr, collectScopeAssertFacts, false)
}

func (a *Analyzer) recordSMTAssertFact(expr ast.Expr) {
	if a == nil {
		return
	}
	a.recordSMTAssertFactInScope(a.currentScope, expr)
}

// recordSMTAssertFactInScope records an assumed fact into a SPECIFIC scope, not necessarily the current
// one. This matters for a branch-condition fact: it must live in the BRANCH's scope (so it is gone at
// the fall-through), but it is recorded while `currentScope` is still the parent (the branch scope is
// not made current until the block body is analyzed). Recording into `currentScope` there would leak
// the condition (e.g. `x > 10`) past the `if`, contradicting the negation the fall-through applies.
func (a *Analyzer) recordSMTAssertFactInScope(scope *Scope, expr ast.Expr) {
	if a == nil || scope == nil || expr == nil {
		return
	}
	if bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr); ok && bin.Op == lexer.TOKEN_AND {
		a.recordSMTAssertFactInScope(scope, bin.Left)
		a.recordSMTAssertFactInScope(scope, bin.Right)
		return
	}
	scope.smtAssertFacts = append(scope.smtAssertFacts, smtFact{Expr: expr, Deps: smtFactDeps(expr)})
}

func smtFactExprForCondition(expr ast.Expr, truthy bool) ast.Expr {
	if truthy || expr == nil {
		return expr
	}
	return &ast.UnaryExpr{Position: expr.Pos(), Op: lexer.TOKEN_NOT, Operand: expr}
}

func (a *Analyzer) clearSMTAssertFacts() {
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		sc.smtAssertFacts = nil
	}
}

func (a *Analyzer) invalidateSMTAssertFactsForTarget(target ast.Expr) {
	// Drop facts depending on the target's structural root OR any place it aliases (a borrow local
	// mutated through writes the underlying place — audit cluster C). No identifiable root ⇒ clear all.
	roots := a.mutationRootsForTarget(target)
	if len(roots) == 0 {
		a.clearSMTAssertFacts()
		return
	}
	rootSet := make(map[string]bool, len(roots))
	for _, r := range roots {
		rootSet[r] = true
	}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		if len(sc.smtAssertFacts) == 0 {
			continue
		}
		out := sc.smtAssertFacts[:0]
		for _, fact := range sc.smtAssertFacts {
			// Consult the fact's own deps() via the unified predicate, mirroring range-fact invalidation.
			if factInvalidatedBy(assertHypothesisFact{fact: fact}, rootSet) {
				continue
			}
			out = append(out, fact)
		}
		sc.smtAssertFacts = out
	}
}

// depsIntersect reports whether any dependency root is in the given set.
func depsIntersect(deps map[string]bool, set map[string]bool) bool {
	for dep := range deps {
		if set[dep] {
			return true
		}
	}
	return false
}

func (a *Analyzer) invalidateSMTAssertFactsForCall(expr *ast.CallExpr) {
	if a == nil || expr == nil {
		return
	}
	// A resolved call carries its callee frame: drop only the facts the callee can actually falsify,
	// letting facts about provably-untouched places survive (docs/87 frame-aware fact survival).
	if ctx, ok := a.callFrameContexts[expr]; ok {
		delete(a.callFrameContexts, expr)
		a.invalidateSMTAssertFactsFramed(ctx.ft, ctx.args)
		return
	}
	// Unresolved/builtin call: conservative whole-argument invalidation.
	for _, arg := range expr.Args {
		if arg == nil {
			continue
		}
		if rt, ok := a.exprTypes[arg].(*RefType); ok && rt != nil && rt.Mutable {
			a.invalidateSMTAssertFactsForTarget(arg)
		}
	}
}

func (a *Analyzer) recordSMTAssignmentFact(target ast.Expr, value ast.Expr) {
	if a == nil || target == nil || value == nil {
		return
	}
	targetType := a.exprTypes[target]
	if targetType == nil {
		if name, ok := rootIdentName(target); ok && a.currentScope != nil {
			if sym, found := a.currentScope.Lookup(name); found && sym != nil {
				targetType = sym.Type
			}
		}
	}
	valueType := a.exprTypes[value]
	if !isSMTExactAssignmentType(targetType) || !isSMTExactAssignmentType(valueType) {
		return
	}
	// SOUNDNESS: the fact `target == value` models target's NEW value with a single SMT symbol. If
	// `value` reads `target` itself (`y <- y - 1`), the symbol stands for both the old and the new value,
	// so the fact is self-referential and (for any non-identity update) CONTRADICTORY — a contradictory
	// hypothesis set proves every obligation. A reassignment whose RHS reads its own target cannot be
	// captured by an equality over one symbol (that is what weakest-precondition transport is for), so
	// record no fact; the prior `invalidateSMTAssertFactsForTarget` already dropped the stale ones.
	if name, ok := rootIdentName(target); ok && name != "" {
		if smtFactDeps(value)[name] {
			return
		}
	}
	a.recordSMTAssertFact(&ast.BinaryExpr{
		Position: target.Pos(),
		Left:     target,
		Op:       lexer.TOKEN_EQEQ,
		Right:    value,
	})
}

func isSMTExactAssignmentType(t Type) bool {
	t = stripRefForBounds(t)
	return t != nil && (IsNumericType(t) || IsBoolType(t)) && !IsFloatType(t)
}

func smtFactDeps(expr ast.Expr) map[string]bool {
	deps := map[string]bool{}
	collectSMTFactDeps(expr, deps)
	if len(deps) == 0 {
		return nil
	}
	return deps
}

func collectSMTFactDeps(expr ast.Expr, out map[string]bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n != nil {
			out[n.Name] = true
		}
	case *ast.ParenExpr:
		if n != nil {
			collectSMTFactDeps(n.Inner, out)
		}
	case *ast.UnaryExpr:
		if n != nil {
			collectSMTFactDeps(n.Operand, out)
		}
	case *ast.BinaryExpr:
		if n != nil {
			collectSMTFactDeps(n.Left, out)
			collectSMTFactDeps(n.Right, out)
		}
	case *ast.FieldExpr:
		if n != nil {
			collectSMTFactDeps(n.Object, out)
		}
	case *ast.IndexExpr:
		if n != nil {
			collectSMTFactDeps(n.Object, out)
			collectSMTFactDeps(n.Index, out)
		}
	case *ast.CallExpr:
		if n != nil {
			collectSMTFactDeps(n.Func, out)
			for _, arg := range n.Args {
				collectSMTFactDeps(arg, out)
			}
		}
	case *ast.CastExpr:
		if n != nil {
			collectSMTFactDeps(n.Operand, out)
		}
	case *ast.AddrOfExpr:
		if n != nil {
			collectSMTFactDeps(n.Operand, out)
		}
	case *ast.QuantifierExpr:
		// Collect deps from the body, then remove bound variables — they are local to the quantifier and
		// not free names. This is required so `exprReferencesResult` correctly detects a `result`
		// reference inside a quantified body (e.g. `ensure forall k: result[k] == 0` references `result`)
		// and skips the clause at void-exit where `result` is unbound.
		if n != nil {
			collectSMTFactDeps(n.Body, out)
			for _, v := range n.Vars {
				delete(out, v)
			}
		}
	}
}

func (a *Analyzer) canAssumeContractFact(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	if a.proveRequiresClause(expr, nil) == requiresProven {
		return true
	}
	if proven, _ := a.trySMTProveRequires(expr, nil); proven {
		return true
	}
	a.proofLint(expr.Pos(), "invariant could not be proven statically at this point; keeping the debug runtime check but not using it as a release proof fact")
	return false
}

// smtImmutableLocalHypotheses asserts the defining equality of every immutable integer
// local in scope (`rem: u64 = value % alignment` -> `(assert (= rem (mod value alignment)))`),
// so the prover can reason THROUGH locals instead of treating them as unconstrained free
// variables (docs/85 gap #2). Sound: an immutable local equals its initializer wherever it
// is in scope, and it is never reassigned. A definition outside the integer fragment (a call,
// a float) is skipped — fewer hypotheses only declines a proof, never admits an unsound one.
func (a *Analyzer) smtImmutableLocalHypotheses(tr *smtTranslator) string {
	if a == nil || a.currentScope == nil || tr == nil {
		return ""
	}
	if a.inClosedWorldProof {
		return "" // docs/99: immutable-local defining equalities are ambient; walled out of a scoped proof.
	}
	var b strings.Builder
	seen := map[string]bool{}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		// Sorted name order so the hypothesis text (and the brick-5 cache key) is deterministic — Symbols
		// is a map, and its iteration order would otherwise vary the query string between identical goals.
		names := make([]string, 0, len(sc.Symbols))
		for name := range sc.Symbols {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			sym := sc.Symbols[name]
			if seen[name] || sym == nil || sym.Mutable || sym.Kind != SymbolLocal {
				continue
			}
			seen[name] = true // a closer scope's binding shadows an outer one
			vd, ok := sym.Node.(*ast.VarDeclStmt)
			if !ok || vd == nil || vd.Value == nil || sym.Type == nil || !IsNumericType(sym.Type) {
				continue
			}
			eterm, ok := tr.termEnv(vd.Value, nil)
			if ok {
				tr.decls[name] = true
				b.WriteString("(assert (= " + smtVar(name) + " " + eterm + "))\n")
			}
			// A `name = X % C` local is bounded 0 <= name < C for ANY X when C > 0 and name is
			// unsigned — independent of whether X itself translates to an SMT term. The left operand
			// may carry a width conversion or shift (e.g. `(seed.u64() >> 16) % C`) that termEnv cannot
			// model, which previously dropped the whole local (and with it this bound). Seed the modulo
			// bound directly so a refinement like VmInRange[0, C-1] on the value still discharges. Gated
			// to unsigned `name` (where 0 <= X%C < C holds unconditionally) and a provably-positive C.
			if bin, isBin := stripOptimizationParens(vd.Value).(*ast.BinaryExpr); isBin && bin.Op == lexer.TOKEN_PERCENT {
				if signed, _, sok := smtIntWidthSign(sym.Type); sok && !signed {
					if rterm, rok := tr.termEnv(bin.Right, nil); rok && a.provablyPositive(bin.Right) {
						tr.decls[name] = true
						b.WriteString("(assert (>= " + smtVar(name) + " 0))\n")
						b.WriteString("(assert (< " + smtVar(name) + " " + rterm + "))\n")
					}
				}
			}
		}
	}
	return b.String()
}

// smtIntWidthSign resolves an integer type to (signedness, bit-width) for the value-preserving
// conversion check, including the pointer-width aliases BitIntInfo does not parse (usize/uintptr
// are unsigned 64-bit, isize/int are signed 64-bit on the targets we emit).
func smtIntWidthSign(t Type) (signed bool, bits int, ok bool) {
	t = stripRefForBounds(t)
	if s, b, k := BitIntInfo(t); k {
		return s, b, true
	}
	if bt, isB := t.(*BuiltinType); isB {
		switch bt.Name {
		case "usize", "uintptr":
			return false, 64, true
		case "isize", "int":
			return true, 64, true
		}
	}
	return false, 0, false
}

// castOperandResolvedType returns the operand type of a cast, falling back — when the per-node
// exprTypes entry is unset (a synthetic/cloned node from measure substitution) — to resolving a
// field-read operand from its struct type or a bare-identifier operand from the scope. This mirrors
// the array/scalar field-read fallbacks so a `field.usize()` cast in a cloned measure resolves the
// same way its analyzed twin does.
func (a *Analyzer) castOperandResolvedType(n *ast.CastExpr) Type {
	if n == nil || n.Operand == nil {
		return nil
	}
	if t := a.exprTypes[n.Operand]; t != nil {
		return t
	}
	switch op := n.Operand.(type) {
	case *ast.FieldExpr:
		if t, ok := a.fieldReadResolvedType(op); ok {
			return t
		}
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(op.Name); ok && sym != nil {
				return sym.Type
			}
		}
	case *ast.ParenExpr:
		return a.castOperandResolvedType(&ast.CastExpr{Operand: op.Inner})
	}
	return nil
}

// postfixCastTargetWidthSign derives the destination integer width/sign of a value-form numeric
// postfix cast (`x.usize()`, `x.u32()`) directly from its TARGET NAME, for cloned cast nodes whose
// result type was never recorded. Only the postfix-shorthand origin with a plain integer target name
// qualifies — exactly the family isPureNumericValueCast admits — so this never widens what counts as a
// modelable cast; it only recovers the type the analyzer would have attached. Declines otherwise.
func postfixCastTargetWidthSign(n *ast.CastExpr) (bool, int, bool) {
	if !isPureNumericValueCast(n) {
		return false, 0, false
	}
	named, ok := n.Target.(*ast.NamedType)
	if !ok || named == nil {
		return false, 0, false
	}
	switch named.Name {
	case "i8", "s8":
		return true, 8, true
	case "i16", "s16":
		return true, 16, true
	case "i32", "s32":
		return true, 32, true
	case "i64", "s64", "isize", "int":
		return true, 64, true
	case "u8":
		return false, 8, true
	case "u16":
		return false, 16, true
	case "u32":
		return false, 32, true
	case "u64", "usize", "uintptr":
		return false, 64, true
	}
	return false, 0, false
}

func smtNumericValueType(t Type) (Type, bool) {
	t = stripRefForBounds(t)
	if t == nil || !IsNumericType(t) || IsFloatType(t) {
		return nil, false
	}
	return t, true
}

func smtTypeNonNegative(t Type) bool {
	signed, _, ok := smtIntWidthSign(t)
	return ok && !signed
}

// lawBodyExpr extracts a law's single `return <bool-expr>` body (the decidable shape).
func (a *Analyzer) lawBodyExpr(decl *ast.FuncDecl) (ast.Expr, bool) {
	if decl == nil || len(decl.Body) != 1 {
		return nil, false
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok || ret == nil || ret.Value == nil {
		return nil, false
	}
	return ret.Value, true
}

// smtTranslator lowers the integer/bool expression fragment to SMT-LIB2, collecting the free
// variables it declares so their flow facts can be asserted as hypotheses.
type smtTranslator struct {
	a          *Analyzer
	decls      map[string]bool // Elisa ident -> declared as an SMT Int const
	arrayDecls map[string]bool // Elisa ident -> declared as an SMT (Array Int Int) (docs/90 brick 90-5)
	// setDecls / dictDecls model set[T] / dict[K,V] containers. A set is an (Array <KSort> Bool)
	// membership function (`p in s` = `(select s p)`). A dict is modeled as TWO arrays keyed by the
	// same KSort: a `<sym>_keys` (Array <KSort> Bool) membership predicate plus a `<sym>_vals`
	// (Array <KSort> Int) value map (`k in d` = `(select d_keys k)`, `d[k]` = `(select d_vals k)`).
	// The map value is the SMT sort string for the key/element ("Int" or "Bool"). Only int/bool key
	// and int value types are modeled; floats/structs DECLINE (sound: a declined term forgoes a proof).
	setDecls    map[string]string // set SMT symbol -> key/element SMT sort
	dictDecls   map[string]string // dict SMT base symbol -> key SMT sort
	lenDecls    map[string]bool   // Elisa ident -> declared length Int (its `.count`/`.len`), asserted >= 0
	lenConsts   map[string]int64
	nonNegDecls map[string]bool // SMT Int consts known non-negative by type (e.g. unsigned field projections)
	// unsignedBits / signedBits record the bit-width of each free var known to be unsigned / signed, so
	// factPreamble can assert the true type bound (`[0, 2^w)` unsigned, `[-2^(w-1), 2^(w-1))` signed).
	// This is what makes the wraparound model (wrapMachineArith) PRECISE rather than merely sound: with
	// operands pinned to their representable range an in-range computation does not actually wrap, so
	// divisibility/alignment proofs through `value - value%alignment` survive.
	unsignedBits map[string]int
	signedBits   map[string]int
	boolDecls    map[string]bool     // free bool-typed idents declared as SMT Bool consts
	resultFields map[string]ast.Expr // when `result` is a struct literal, its field -> arg expr
	paramConsts  map[string]int64    // law static params bound to constants
	// auxDecls holds pre-formatted declare/assert lines for fresh under-constrained symbols minted
	// for sub-terms we cannot model precisely yet soundly (e.g. `x % y` with a not-provably-nonzero
	// divisor). Each is a free integer constrained only by what is provably true (never a false
	// relation), so the term stays PRESENT — letting the rest of the clause (a guarding `or`, an
	// outer comparison) still discharge — without the abstraction ever fabricating a proof.
	auxDecls []string // declaration + sound-constraint lines, in mint order
	auxVars  []string // the fresh symbols, for the Sat counterexample query
	auxSeq   int      // monotonic counter → deterministic fresh names
	// ceExprs are diagnostic-only SMT terms worth reading back from a Sat model. Quantified forall
	// failures use these to name the binder and array element/select that falsified the goal.
	ceExprs map[string]string // rendered label -> SMT term
	// oldAsEntry enables `old(expr)` -> the ENTRY-value term (vcOldEntry) in the VC IR. Set ONLY by the
	// WP discharge paths, which thread the bare (exit) value so entry/exit diverge; everywhere else
	// old() must stay a fresh opaque symbol or it would collapse onto the un-threaded bare value.
	oldAsEntry bool
	// callCanon canonicalizes a PURE, TOTAL function call to a single SMT symbol keyed by callee +
	// syntactic args, so two occurrences of `g(n-1)` share one symbol (a function is deterministic, so
	// equal inputs give equal outputs — sound). It is what lets the recursive-function defining
	// equation, and an inductive hypothesis about `g(n-1)`, actually CONNECT to the obligation's
	// `g(n)`/`g(n-1)` terms instead of being unrelated fresh aux symbols.
	callCanon map[string]string
	// callEqInFlight guards recursive instantiation by CANONICAL CALL KEY, not just callee. `g(n)` and
	// `g(n-1)` must remain distinct; only re-entering the exact same ground instance consumes fuel.
	callEqInFlight map[string]int
	// callEqMaxUnroll bounds how deep the defining equation is instantiated (default 1). A finite depth
	// keeps the emitted axiom set finite; soundness does not depend on the depth (only on purity +
	// termination), so this is purely a completeness knob.
	callEqMaxUnroll int
	// callEqDeclDepth bounds defining-equation unfolding PER CALLEE declaration. callEqInFlight keys on
	// the canonical call (decl+args), so genuine recursion f(n)->f(n-1)->f(n-2) mints a fresh key each
	// level and the per-key bound never trips — an infinite unfold. This per-decl counter trips after
	// callEqMaxDeclDepth nested instantiations of the SAME function, leaving the deeper self-call opaque
	// (still sound: a missing equation only weakens the hypothesis set; the recursive IH already carries
	// the inductive content).
	callEqDeclDepth map[*ast.FuncDecl]int
	// callEqMaxDeclDepth bounds callEqDeclDepth.
	callEqMaxDeclDepth int
	// requiresHypMode is set ONLY while emitting an entry `requires` clause as a hypothesis. In that mode
	// an unsigned upper-bound-by-type-max comparison (`a + b <= 2^W-1`) is read as the ℤ no-overflow
	// predicate rather than B2's wrapped bvule (which is vacuously true, since `int2bv` reduces the sum
	// mod 2^W). This makes such a precondition a real constraint — paired with the body's matching `+`
	// being emitted clean only WHEN this same requires proves it no-wrap (provablyNoArithWrap →
	// knownRequiresSumNoWrap). Without the clause, neither the hypothesis nor the clean body term exists,
	// so the genuine overflow counterexample is preserved (the C1 soundness gate).
	requiresHypMode bool
}

// newSMTTranslator builds a translator with all collection maps initialized.
func (a *Analyzer) newSMTTranslator(paramConsts map[string]int64) *smtTranslator {
	if paramConsts == nil {
		paramConsts = map[string]int64{}
	}
	return &smtTranslator{
		a:                  a,
		decls:              map[string]bool{},
		arrayDecls:         map[string]bool{},
		setDecls:           map[string]string{},
		dictDecls:          map[string]string{},
		lenDecls:           map[string]bool{},
		lenConsts:          map[string]int64{},
		nonNegDecls:        map[string]bool{},
		unsignedBits:       map[string]int{},
		signedBits:         map[string]int{},
		boolDecls:          map[string]bool{},
		paramConsts:        paramConsts,
		ceExprs:            map[string]string{},
		callCanon:          map[string]string{},
		callEqInFlight:     map[string]int{},
		callEqMaxUnroll:    1,
		callEqDeclDepth:    map[*ast.FuncDecl]int{},
		callEqMaxDeclDepth: 2,
	}
}

// arrayTermEnv lowers an ARRAY-valued expression to an SMT array symbol: an array/darray identifier
// becomes a `(Array Int Int)` const (declared once), or resolves through `env` (the law's `self`).
// Element-typed quantifiers (docs/90 brick 90-5) model `arr[i]` as `(select <arr> i)`.
func (tr *smtTranslator) arrayTermEnv(expr ast.Expr, env map[string]string) (string, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return tr.arrayTermEnv(n.Inner, env)
	case *ast.Ident:
		if env != nil {
			if bound, ok := env[n.Name]; ok {
				return bound, true
			}
		}
		// Resolve the array's element type from exprTypes (set once the node is analyzed) OR, when that
		// is not yet populated — e.g. loop-invariant proving runs BEFORE the body is type-analyzed — from
		// the symbol's declared type via scope lookup. The declared type is authoritative for a param or
		// local, so this is sound and is what lets `arr[k]` in a quantified loop invariant translate.
		t := tr.a.exprTypes[n]
		if t == nil && tr.a.currentScope != nil {
			if sym, ok := tr.a.currentScope.Lookup(n.Name); ok && sym != nil {
				t = sym.Type
			}
		}
		if tr.isArrayLike(t) {
			tr.arrayDecls[n.Name] = true
			arr := smtVar(n.Name)
			tr.recordFixedArrayLen(arr, t)
			return arr, true
		}
		return "", false
	case *ast.FieldExpr:
		// An array-valued struct field, possibly through a reference (`r.data`, `self.buf`). Model it
		// as a stable array symbol keyed by its syntactic path, so two reads of the same path share the
		// symbol (and thus its `.count`/`.len`). This is what lets `ensure result <= r.data.count;
		// return r.data.count` discharge — both sides resolve to the same length symbol.
		//
		// Check env FIRST: the loop-preservation prover injects a path-keyed (store …) substitution
		// for struct-field array stores (`s.data[i] <- v` → env["s__field__data"] = "(store …)").
		// The env key is the same normalizedFieldPath / smtProjectionName, so picking it up here
		// lets `s.data[k]` in a quantified invariant resolve to the post-body array term — which is
		// exactly what the bare-array Ident case does for `arr[k]`.
		if env != nil {
			if bound, ok := env[smtProjectionName(n)]; ok {
				return bound, true
			}
		}
		// Resolve the field's array type from exprTypes (set once analyzed) OR — when the node is
		// synthetic/cloned (e.g. the loop-termination prover's measure[body], whose field reads were
		// produced by substitution and never analyzed) — from the object's struct type, mirroring the
		// Ident-case scope fallback above. Both readings resolve the SAME syntactic path to the SAME
		// projection symbol, so a field-array length cancels between measure and measure[body].
		ft := tr.a.exprTypes[n]
		if ft == nil {
			if resolved, ok := tr.a.fieldReadResolvedType(n); ok {
				ft = resolved
			}
		}
		if tr.isArrayLike(ft) {
			name := smtProjectionName(n)
			tr.arrayDecls[name] = true
			arr := smtVar(name)
			tr.recordFixedArrayLen(arr, ft)
			return arr, true
		}
		return "", false
	default:
		return "", false
	}
}

// arrayLenSymForAnyElem returns the SMT length symbol (`<base>_len`) for the `.count`/`.len` of an
// array/darray-valued expression of ANY element type — including a darray of structs, which
// arrayTermEnv (integer-element only) declines. It accepts a bare array identifier or a stable
// struct-field path; the base key is the env-bound symbol or the syntactic projection name, so two
// reads of the same path share one length symbol (the cancellation the decrease proof needs). The
// type is read from exprTypes, falling back to the symbol's declared type (Ident) or the resolved
// field type (FieldExpr) for synthetic/cloned nodes. Declines for anything not provably array-like.
func (tr *smtTranslator) arrayLenSymForAnyElem(expr ast.Expr, env map[string]string) (string, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return tr.arrayLenSymForAnyElem(n.Inner, env)
	case *ast.Ident:
		if env != nil {
			if bound, ok := env[n.Name]; ok {
				return bound + "_len", true
			}
		}
		t := tr.a.exprTypes[n]
		if t == nil && tr.a.currentScope != nil {
			if sym, ok := tr.a.currentScope.Lookup(n.Name); ok && sym != nil {
				t = sym.Type
			}
		}
		if isAnyArrayLike(t) {
			return smtVar(n.Name) + "_len", true
		}
	case *ast.FieldExpr:
		t := tr.a.exprTypes[n]
		if t == nil {
			if resolved, ok := tr.a.fieldReadResolvedType(n); ok {
				t = resolved
			}
		}
		if isAnyArrayLike(t) {
			return smtVar(smtProjectionName(n)) + "_len", true
		}
	}
	return "", false
}

// isAnyArrayLike reports whether t is an array or darray of ANY element type (unlike
// smtTranslator.isArrayLike, which restricts to integer elements for the element-select theory). A
// length read needs only that the value is a sized container, so the element type is irrelevant.
func isAnyArrayLike(t Type) bool {
	switch stripRefForBounds(t).(type) {
	case *ArrayType, *DArrayType:
		return true
	}
	return false
}

func (tr *smtTranslator) recordFixedArrayLen(arr string, t Type) {
	at, ok := stripRefForBounds(t).(*ArrayType)
	if !ok || at == nil || !at.HasConstSize {
		return
	}
	tr.lenConsts[arr+"_len"] = at.ConstSize
}

// isArrayLike reports whether a type is an integer-element array/darray we can model as (Array Int
// Int). Non-integer elements decline (sound: we only model integer element theory).
func (tr *smtTranslator) isArrayLike(t Type) bool {
	switch at := stripRefForBounds(t).(type) {
	case *ArrayType:
		return at != nil && IsNumericType(at.Elem) && !IsFloatType(at.Elem)
	case *DArrayType:
		return at != nil && IsNumericType(at.Elem) && !IsFloatType(at.Elem)
	}
	return false
}

// smtKeySort returns the SMT sort string for a set element / dict key type, and whether it is
// soundly modelable. Integer (non-float numeric) -> "Int"; bool -> "Bool". Floats, structs, enums,
// strings, and everything else DECLINE (return ok=false) — modeling them would require theories we
// do not emit, so we forgo the proof rather than risk unsoundness.
func smtKeySort(t Type) (string, bool) {
	t = stripRefForBounds(t)
	if IsBoolType(t) {
		return "Bool", true
	}
	if IsNumericType(t) && !IsFloatType(t) {
		return "Int", true
	}
	return "", false
}

// setTermEnv lowers a set-valued expression to its SMT membership-array symbol (an `(Array <KSort>
// Bool)`), returning the symbol, its key sort, and ok. It resolves a law's `self` / quantifier
// binder through `env`, an identifier/field through its set type. Non-set or non-modelable element
// types decline (sound).
func (tr *smtTranslator) setTermEnv(expr ast.Expr, env map[string]string) (sym string, ksort string, ok bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return tr.setTermEnv(n.Inner, env)
	case *ast.Ident:
		if env != nil {
			if bound, bok := env[n.Name]; bok {
				if ks, kok := tr.setDecls[bound]; kok {
					return bound, ks, true
				}
			}
		}
		st, kok := tr.setElemType(n)
		if !kok {
			return "", "", false
		}
		ks, sok := smtKeySort(st)
		if !sok {
			return "", "", false
		}
		sym = smtVar(n.Name)
		tr.setDecls[sym] = ks
		return sym, ks, true
	case *ast.FieldExpr:
		st, kok := tr.setElemType(n)
		if !kok {
			return "", "", false
		}
		ks, sok := smtKeySort(st)
		if !sok {
			return "", "", false
		}
		sym = smtVar(smtProjectionName(n))
		tr.setDecls[sym] = ks
		return sym, ks, true
	}
	return "", "", false
}

// keyTermEnv lowers a set-element / dict-key expression to a term of the container's key sort:
// an integer term for sort "Int", a boolean term for sort "Bool". This keeps the `(select <arr>
// <key>)` index well-sorted.
func (tr *smtTranslator) keyTermEnv(expr ast.Expr, ksort string, env map[string]string) (string, bool) {
	if ksort == "Bool" {
		return tr.boolTerm(expr, env)
	}
	return tr.termEnv(expr, env)
}

// setElemType resolves the element type of a set-typed expression (from exprTypes, or the symbol's
// declared type when the node predates body analysis — e.g. precondition/law proving).
func (tr *smtTranslator) setElemType(expr ast.Expr) (Type, bool) {
	t := tr.a.exprTypes[expr]
	if t == nil && tr.a.currentScope != nil {
		if id, ok := expr.(*ast.Ident); ok {
			if sym, ok := tr.a.currentScope.Lookup(id.Name); ok && sym != nil {
				t = sym.Type
			}
		}
	}
	if st, ok := stripRefForBounds(t).(*SetType); ok && st != nil {
		return st.Elem, true
	}
	return nil, false
}

// dictTermEnv lowers a dict-valued expression to its SMT base symbol plus the key sort and (modeled)
// value type. The dict is realized as two arrays in factPreamble: `<sym>_keys` (membership) and
// `<sym>_vals` (value map). Only int/bool keys and int values are modeled; otherwise decline.
func (tr *smtTranslator) dictTermEnv(expr ast.Expr, env map[string]string) (sym string, ksort string, ok bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return tr.dictTermEnv(n.Inner, env)
	case *ast.Ident:
		if env != nil {
			if bound, bok := env[n.Name]; bok {
				if ks, kok := tr.dictDecls[bound]; kok {
					return bound, ks, true
				}
			}
		}
		return tr.dictDeclFor(n, smtVar(n.Name))
	case *ast.FieldExpr:
		return tr.dictDeclFor(n, smtVar(smtProjectionName(n)))
	}
	return "", "", false
}

// dictDeclFor resolves a dict-typed expression's key/value types and records the base symbol when
// both key and value are soundly modelable (int/bool key, int value).
func (tr *smtTranslator) dictDeclFor(expr ast.Expr, sym string) (string, string, bool) {
	dt, ok := tr.dictType(expr)
	if !ok {
		return "", "", false
	}
	ks, kok := smtKeySort(dt.Key)
	if !kok {
		return "", "", false
	}
	// Value must be an integer (non-float numeric) — that is the only value theory we model. Bool
	// values are not yet supported (the value array is fixed to (Array <KSort> Int)).
	if !IsNumericType(dt.Value) || IsFloatType(dt.Value) {
		return "", "", false
	}
	tr.dictDecls[sym] = ks
	return sym, ks, true
}

// dictMethodCall recognizes the SURFACE dict-access methods in spec position and maps them onto the
// existing two-array dict model (see dictTermEnv). The surface language has no `d[k]` / `k in d`
// syntax for dicts; membership and lookup are expressed through methods that the analyzer resolves to
// the stdlib free functions `arena_dict_contains` / `arena_dict_get`:
//
//	d.contains(k)  ->  arena_dict_contains(&d, k)  ->  (select <d>_keys <k>)      [Bool: membership]
//	d.get(k)       ->  arena_dict_get(&d, k)       ->  (select <d>_vals <k>)      [Int : value]
//
// Both take the dict as the first (receiver) argument and the key as the second. The dict must be
// soundly modelable (int/bool key, int value); otherwise we decline so the obligation falls back to a
// runtime check rather than fabricating a proof.
//
// SOUNDNESS of the `d.get(k)` -> value-array mapping: `arena_dict_get` returns an OPTIONAL value
// (`T?`); for a key not in the dict it is null. Modeling the result as `(select <d>_vals <k>)` (an
// arbitrary-but-total Int) is only sound for a goal that ALSO carries a membership guard for `k`
// (typically `requires d.contains(k)`), which constrains `(select <d>_keys <k>)` to true and binds
// the goal to the in-domain value the quantifier hypothesis ranges over. Without such a guard the
// value is an unconstrained Int and the goal simply will not discharge — never falsely proven.
//
// `kind` is "keys" for the membership method, "vals" for the lookup method. Returns the select term.
func (tr *smtTranslator) dictMethodCall(call *ast.CallExpr, kind string, env map[string]string) (string, bool) {
	if tr == nil || tr.a == nil || call == nil {
		return "", false
	}
	decl, direct := tr.a.resolveDirectCallFuncDecl(call)
	if !direct || decl == nil {
		return "", false
	}
	var want string
	switch kind {
	case "keys":
		want = "arena_dict_contains"
	case "vals":
		want = "arena_dict_get"
	default:
		return "", false
	}
	if decl.Name != want {
		return "", false
	}
	args := proofCallArgs(call)
	if len(args) != 2 || args[0] == nil || args[1] == nil {
		return "", false
	}
	// UFCS resolution wraps the receiver in synthetic ref CASTs (`(&d).cast[dict[..]&]`); peel addr-of,
	// parens, and casts down to the underlying dict expression the model is keyed on.
	dictArg := args[0]
	for {
		next := stripAddrAndParens(dictArg)
		if c, ok := next.(*ast.CastExpr); ok && c != nil && c.Operand != nil {
			dictArg = c.Operand
			continue
		}
		dictArg = next
		break
	}
	dsym, ksort, ok := tr.dictTermEnv(dictArg, env)
	if !ok {
		return "", false
	}
	key, ok := tr.keyTermEnv(args[1], ksort, env)
	if !ok {
		return "", false
	}
	return "(select " + dsym + "_" + kind + " " + key + ")", true
}

func (tr *smtTranslator) dictType(expr ast.Expr) (*DictType, bool) {
	t := tr.a.exprTypes[expr]
	if t == nil && tr.a.currentScope != nil {
		if id, ok := expr.(*ast.Ident); ok {
			if sym, ok := tr.a.currentScope.Lookup(id.Name); ok && sym != nil {
				t = sym.Type
			}
		}
	}
	if dt, ok := stripRefForBounds(t).(*DictType); ok && dt != nil {
		return dt, true
	}
	return nil, false
}

// term lowers an integer-valued expression. Supports literals, immutable integer identifiers
// (declared as SMT Int consts), parenthesization, unary minus, and +/-/* — including the var*var
// PRODUCT the affine prover cannot handle (the headline reason to call the solver). Division and
// modulo are deliberately omitted for now: SMT-LIB `div`/`mod` are Euclidean and would not match
// Elisa's truncating integer division for negative operands, so translating them could be unsound.
func (tr *smtTranslator) term(expr ast.Expr) (string, bool) {
	return tr.termEnv(expr, nil)
}

func (tr *smtTranslator) termEnv(expr ast.Expr, env map[string]string) (string, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return tr.termEnv(n.Inner, env)
	case *ast.IntLit:
		if c, ok := tr.a.constIntValue(n); ok {
			return smtInt(c), true
		}
		// A u64-range literal (e.g. 0xFFFFFFFFFFFFFFFF) does not fit int64 so constIntValue declines; emit
		// its exact non-negative value as an SMT Int so unsigned upper-bound preconditions translate.
		if v, ok := tr.a.unsignedConstBound(n); ok {
			return v.String(), true
		}
		return "", false
	case *ast.Ident:
		if env != nil {
			if bound, ok := env[n.Name]; ok {
				return bound, true
			}
		}
		if c, ok := tr.paramConsts[n.Name]; ok {
			return smtInt(c), true
		}
		if c, ok := tr.a.constIntValue(n); ok {
			return smtInt(c), true
		}
		if _, ok := immutableIntIdentName(tr.a, tr.a.currentScope, n); ok {
			name := smtVar(n.Name)
			tr.decls[n.Name] = true
			// Emit the identifier's true type bounds (unsigned `[0, 2^w)`, signed `[-2^(w-1), 2^(w-1))`).
			// Without these an immutable unsigned param is an unconstrained Int, so a goal like
			// `a - b <= a` (which needs `b >= 0`) cannot discharge. The bounds are guaranteed by the type
			// and therefore always sound (asserting more true facts only ever proves more, never less).
			if sym, ok := tr.a.currentScope.Lookup(n.Name); ok && sym != nil {
				tr.markIntVar(n.Name, sym.Type)
			}
			return name, true
		}
		if sym, ok := tr.a.currentScope.Lookup(n.Name); ok && sym != nil && (sym.Kind == SymbolLocal || sym.Kind == SymbolParam) {
			if t, ok := smtNumericValueType(sym.Type); ok {
				name := smtVar(n.Name)
				tr.decls[n.Name] = true
				tr.markIntVar(n.Name, t)
				return name, true
			}
		}
		return "", false
	case *ast.CallExpr:
		return tr.callResultTermEnv(n, env)
	case *ast.StructLitExpr:
		if call, ok := tr.a.proofCallExpr(n); ok {
			return tr.callResultTerm(call)
		}
		return "", false
	case *ast.IndexExpr:
		// Array element access `arr[idx]` → `(select <arr> <idx>)` over SMT array theory (docs/90
		// brick 90-5). The element value is an Int; out-of-range indices are an arbitrary-but-total
		// value, which a quantifier's range guard constrains away.
		if arr, ok := tr.arrayTermEnv(n.Object, env); ok {
			idx, ok := tr.termEnv(n.Index, env)
			if !ok {
				return "", false
			}
			return "(select " + arr + " " + idx + ")", true
		}
		// Dict lookup `d[k]` -> `(select <d>_vals <k>)` over the dict's value array (docs/90 set/dict
		// modeling). The key term is modeled in the dict's key sort. Out-of-domain lookups select an
		// arbitrary-but-total value; a `k in d` guard (asserting `(select d_keys k)`) is what constrains
		// the proof to in-domain keys, so e.g. `requires k in d; assert d[k] > 0` discharges.
		if dsym, ksort, ok := tr.dictTermEnv(n.Object, env); ok {
			key, ok := tr.keyTermEnv(n.Index, ksort, env)
			if !ok {
				return "", false
			}
			return "(select " + dsym + "_vals " + key + ")", true
		}
		return "", false
	case *ast.FieldExpr:
		if env != nil {
			if bound, ok := env[smtProjectionName(n)]; ok {
				return bound, true
			}
		}
		// `result.field` where `result` is bound to a struct literal → that field's construction
		// argument, so `ensure result.a == 1` for `return Pair(1, 2)` proves.
		if tr.resultFields != nil {
			if id, ok := stripOptimizationParens(n.Object).(*ast.Ident); ok && id.Name == "result" {
				if arg, ok := tr.resultFields[n.Field]; ok {
					return tr.termEnv(arg, env)
				}
			}
		}
		// `arr.count` / `arr.len` → a per-array length Int symbol (derived from the array's SMT symbol,
		// so it resolves through `env` for `self.count`), asserted >= 0 in the preamble.
		// SPECIAL CASE: if the entire field expression evaluates to a compile-time constant (e.g.
		// `"abc".len == 3`), emit the integer directly so the proof discharges at the const tier
		// rather than introducing an opaque length symbol.
		if n.Field == "count" || n.Field == "len" {
			if c, ok := tr.a.constIntValue(n); ok {
				return smtInt(c), true
			}
			arr, ok := tr.arrayTermEnv(n.Object, env)
			if !ok {
				// `arrayTermEnv` models only INTEGER-element arrays (it lowers `arr[k]` to a `(select …)`),
				// so a darray of STRUCTS (`self.dmem_areas: darray[PhysicalMemoryArea]`) declines there. But
				// the LENGTH `.count`/`.len` is just a `usize` regardless of element type, and a measure /
				// guard only needs that scalar — never the element theory. When the object is provably an
				// array/darray of ANY element type, mint the length symbol keyed by the object's syntactic
				// projection (the same key arrayTermEnv would use for an Ident/Field path), so it resolves
				// to a CONSISTENT symbol between measure and measure[body]. This is what lets the emulator's
				// struct-element field-shift loops (`decreases self.xs.count - j`) discharge.
				lenSym, lok := tr.arrayLenSymForAnyElem(n.Object, env)
				if !lok {
					return "", false
				}
				tr.lenDecls[lenSym] = true
				return lenSym, true
			}
			lenSym := arr + "_len"
			if c, ok := tr.lenConsts[lenSym]; ok {
				return smtInt(c), true
			}
			tr.lenDecls[lenSym] = true
			return lenSym, true
		}
		// Field type from exprTypes (set once analyzed) OR resolved from the object's struct type when
		// the node is synthetic/cloned (e.g. a substituted derived-state condition, whose field reads
		// were never analyzed so their per-node type is unset).
		fieldType := tr.a.exprTypes[n]
		if fieldType == nil {
			if resolved, ok := tr.a.fieldReadResolvedType(n); ok {
				fieldType = resolved
			}
		}
		if t, ok := smtNumericValueType(fieldType); ok && !IsFloatType(t) {
			// Numeric struct-field reads are modeled as fresh-ish projection symbols keyed by
			// their syntactic path (`self.total`, `area.size`). We assert only facts guaranteed
			// by the field type (e.g. unsigned >= 0 in factPreamble), not any relation to other
			// fields or heap state. That is sound and is enough for practical contracts like
			// `ensure result <= self.total` on unsigned storage.
			name := smtProjectionName(n)
			tr.decls[name] = true
			tr.markIntVar(name, t)
			return smtVar(name), true
		}
		return "", false
	case *ast.UnaryExpr:
		if n.Op != lexer.TOKEN_MINUS {
			return "", false
		}
		inner, ok := tr.termEnv(n.Operand, env)
		if !ok {
			return "", false
		}
		return "(- " + inner + ")", true
	case *ast.CastExpr:
		// A value-preserving integer conversion — widening or same-width, SAME signedness
		// (`x.u64()`, `x.usize()`, an i32 used as i64) — is the IDENTITY in the unbounded-Int
		// model, so the prover sees THROUGH the conversion and refinement bounds survive it
		// (docs/85 gap #2). A narrowing or a sign change can wrap, so those are NOT identity
		// and decline here (sound: a declined term only forgoes a proof).
		ssign, sbits, sok := smtIntWidthSign(tr.a.castOperandResolvedType(n))
		dsign, dbits, dok := smtIntWidthSign(tr.a.exprTypes[n])
		if !dok {
			// The result type is unset for a synthetic/cloned cast node (e.g. the loop-termination
			// prover's measure[body], whose `field.usize()` cast was produced by substitution and never
			// analyzed). Resolve the destination width/sign from the postfix cast TARGET NAME — the same
			// information the analyzer would have recorded — so a field-read cast in the measure cancels
			// against its analyzed twin. Declines for any non-integer / compound target (sound).
			dsign, dbits, dok = postfixCastTargetWidthSign(n)
		}
		if !sok || !dok {
			return "", false
		}
		if ssign == dsign && dbits >= sbits {
			return tr.termEnv(n.Operand, env)
		}
		// Unsigned -> strictly wider signed (`(u8 b).i32()`): the source is non-negative and fits the
		// wider signed range, so the conversion is the identity on the value.
		if !ssign && dsign && dbits > sbits {
			return tr.termEnv(n.Operand, env)
		}
		// Same-width sign reinterpretation (`(u32 field).i32()`): a bitcast — the bit pattern is
		// unchanged, only the interpretation flips. Model the destination as the signed/unsigned read
		// of the operand's bits (exact, so a field cast then sign-extended proves its range). Soundness:
		// it is the precise two's-complement relationship, not an approximation.
		if sbits == dbits {
			inner, ok := tr.termEnv(n.Operand, env)
			if !ok {
				return "", false
			}
			bv := "((_ int2bv " + strconv.Itoa(dbits) + ") " + inner + ")"
			return smtBitvectorRead(bv, dsign, dbits), true
		}
		return "", false
	case *ast.TernaryExpr:
		cond, ok := tr.boolTerm(n.Cond, env)
		if !ok {
			return "", false
		}
		if cond == "true" {
			return tr.termEnv(n.Value, env)
		}
		if cond == "false" {
			return tr.termEnv(n.Alt, env)
		}
		v, ok := tr.termEnv(n.Value, env)
		if !ok {
			return "", false
		}
		alt, ok := tr.termEnv(n.Alt, env)
		if !ok {
			return "", false
		}
		return "(ite " + cond + " " + v + " " + alt + ")", true
	case *ast.BinaryExpr:
		var op string
		switch n.Op {
		case lexer.TOKEN_PLUS:
			op = "+"
		case lexer.TOKEN_MINUS:
			op = "-"
		case lexer.TOKEN_STAR:
			op = "*"
		case lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT:
			// SMT-LIB `div`/`mod` are Euclidean, while Elisa integer division truncates toward zero.
			// Model truncating division explicitly from abs/sign, and model remainder as x-y*q. Still
			// require a provably non-zero divisor: SMT-LIB division is total at zero, which could
			// otherwise fabricate proofs for source programs that may divide by zero at runtime.
			// When the divisor is NOT provably non-zero we do not give up on the whole clause —
			// that would lose a guarding `or` (`alignment == 0 or (x % alignment) == 0`) or an
			// outer disjunct that makes the clause hold regardless of this sub-term. Instead we
			// mint a fresh under-constrained symbol: a free integer, asserted `>= 0` only when the
			// dividend is provably non-negative (true of unsigned `%`/`/`). That is sound — it
			// states nothing false — so the result is just opaque, never a fabricated proof.
			if !tr.a.provablyNonZero(n.Right) {
				return tr.freshAux(tr.a.provablyNonNeg(n.Left)), true
			}
			l, ok := tr.termEnv(n.Left, env)
			if !ok {
				return "", false
			}
			r, ok := tr.termEnv(n.Right, env)
			if !ok {
				return "", false
			}
			// Preferred path: a non-negative dividend with a positive divisor (the overwhelmingly
			// common integer case — unsigned `%`/`/`, page/alignment math). Use SMT-LIB's NATIVE
			// `mod`/`div` directly: they are Euclidean, and Euclidean == truncating for non-negative
			// operands, so this is exact (no abs/sign dance). It also engages z3's dedicated div/mod
			// theory, which discharges divisibility goals like `(value - value%alignment) %
			// alignment == 0` instantly where the nested-`div`/fresh-symbol encodings stall. Identical
			// `(mod l r)` sub-terms are syntactically shared, so a `rem = value % alignment`
			// hypothesis and a later `value % alignment` obligation refer to the same term.
			if tr.a.provablyNonNeg(n.Left) && tr.a.provablyPositive(n.Right) {
				if n.Op == lexer.TOKEN_PERCENT {
					return "(mod " + l + " " + r + ")", true
				}
				return "(div " + l + " " + r + ")", true
			}
			// SOUNDNESS: signed `INT_MIN / -1` (and `INT_MIN % -1`) OVERFLOWS — the true result -INT_MIN is
			// not representable. On the arm64 target it wraps to INT_MIN (no hardware trap), so the exact
			// unbounded-ℤ model (smtTruncDiv gives -INT_MIN, positive) would prove a false `result >= 0`.
			// Unless that overflow is provably impossible (divisor cannot be -1, or dividend cannot be
			// INT_MIN), abstract the result as a fresh under-constrained symbol — sound (states nothing
			// false), at worst declines the proof (audit cluster F).
			if !tr.signedDivCannotOverflow(n.Left, n.Right) {
				return tr.freshAux(tr.a.provablyNonNeg(n.Left)), true
			}
			if n.Op == lexer.TOKEN_PERCENT {
				q := smtTruncDiv(l, r)
				return "(- " + l + " (* " + r + " " + q + "))", true
			}
			return smtTruncDiv(l, r), true
		case lexer.TOKEN_AMPERSAND, lexer.TOKEN_PIPE, lexer.TOKEN_CARET,
			lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
			// Bitwise/shift ops are modeled exactly via the SMT bitvector theory (see bitwiseTerm).
			return tr.bitwiseTerm(n, env)
		default:
			return "", false
		}
		l, ok := tr.termEnv(n.Left, env)
		if !ok {
			return "", false
		}
		r, ok := tr.termEnv(n.Right, env)
		if !ok {
			return "", false
		}
		return tr.wrapMachineArith(n, op, l, r), true
	default:
		return "", false
	}
}

// wrapMachineArith models the machine semantics of `+`/`-`/`*` on a fixed-width integer result, which
// WRAP modulo 2^width (two's complement for signed). Modeling them as unbounded-ℤ operations is
// UNSOUND for postconditions a wrap can violate — e.g. `ensure result <= a` for unsigned `a - b` holds
// in ℤ (b ≥ 0) but is FALSE when b > a (underflow), and `ensure result >= a` for signed `a + b` holds
// in ℤ (b ≥ 0) but is FALSE on signed overflow. When the result is PROVABLY in range (affine interval
// within the type, or a no-underflow subtraction) the ℤ term equals the machine value, so we emit it
// clean — keeping divisibility/alignment goals tractable. Otherwise we emit the exact wrapped value:
// `(mod raw 2^W)` for unsigned (Euclidean mod == unsigned wrap), and a recentered mod for signed. The
// prover only ever concludes on `unsat`, so a wrap can never fabricate a proof — at worst it declines.
func (tr *smtTranslator) wrapMachineArith(n *ast.BinaryExpr, op, l, r string) string {
	raw := "(" + op + " " + l + " " + r + ")"
	signed, bits, ok := smtIntWidthSign(tr.a.exprTypes[n])
	if !ok || bits <= 0 || bits > 64 {
		return raw
	}
	if tr.a.provablyNoArithWrap(n, signed, bits) {
		// Provably in range: the ℤ value equals the machine value — emit it clean (keeps divisibility /
		// alignment goals tractable and lets bounded arithmetic prove).
		return raw
	}
	if signed {
		// SOUNDNESS: signed `+`/`-`/`*` that can overflow WRAPS two's-complement at runtime — there is no
		// codegen trap backing the old "assume no overflow" stance, so on the target `i64max + 1` becomes
		// i64min, and the clean ℤ value would prove a false `ensure result > 0`. Model the exact wrapped
		// value: recenter into [0, 2^W), take the unsigned wrap, then shift back into the signed range —
		// `((raw + 2^(W-1)) mod 2^W) - 2^(W-1)`. The prover only concludes on `unsat`, so this can only
		// decline an unprovable goal, never fabricate one.
		return smtSignedWrap(raw, bits)
	}
	// Unsigned wraps modulo 2^W with well-defined two's-complement semantics (and is frequently an
	// intentional idiom), so it is modeled exactly: `(mod raw 2^W)` is the wrapped value. This is what
	// keeps `ensure result <= total; return total - usage` honest (declines without a guard).
	return "(mod " + raw + " " + smtPow2(bits) + ")"
}

// smtSignedWrap renders the two's-complement wrap of a `bits`-wide signed result: the machine value of
// `raw` is `((raw + 2^(W-1)) mod 2^W) - 2^(W-1)`, which maps any ℤ value into [-2^(W-1), 2^(W-1)).
func smtSignedWrap(raw string, bits int) string {
	half := smtPow2(bits - 1)
	full := smtPow2(bits)
	return "(- (mod (+ " + raw + " " + half + ") " + full + ") " + half + ")"
}

// bitwiseTerm models a fixed-width bitwise/shift operator (`&`, `|`, `^`, `<<`, `>>`) by bridging the
// unbounded-Int operand terms into SMT bitvectors at the result's machine width, applying the matching
// bitvector operator, and reading the result back as an unsigned integer. `(_ int2bv W)` reduces each
// operand modulo 2^W — exactly the machine bit pattern (two's complement for any sign) — and `bv2nat`
// reads the result in [0, 2^W), exactly the unsigned machine value. The produced Int term therefore
// EQUALS the machine result, so it composes soundly with further Int reasoning (a mask `x & 0xFFF` is
// provably `< 0x1000`, a shift `x >> 12` provably `< 2^(W-12)`, etc.).
//
// Soundness gates — anything outside them declines (returns ok=false), which only forgoes a proof,
// never fabricates one:
//   - Result width 1..64. An unsigned result reads back via `bv2nat`; a signed result reads back via
//     the exact two's-complement value (smtBitvectorRead), and a signed `>>` uses `bvashr` (arithmetic)
//     to match the LLVM AShr codegen — so sign-extension idioms `(v << k) >> k` prove their signed range.
//   - Operands must share the result width (Elisa bitwise is same-type), so int2bv at W is the exact
//     bit pattern with no narrowing/sign surprise.
//   - Shift amounts must be a compile-time constant in [0, W): the machine leaves shifts ≥ width
//     undefined (LLVM poison), so assuming the bitvector "shift past width ⇒ 0" rule would be unsound.
//
// Bridging the Int and BV theories can be costly for the solver on relational goals; the per-query
// timeout turns any hard case into Unknown → runtime fallback, so soundness holds regardless of cost.
func (tr *smtTranslator) bitwiseTerm(n *ast.BinaryExpr, env map[string]string) (string, bool) {
	signed, bits, ok := smtIntWidthSign(tr.a.exprTypes[n])
	if !ok || bits <= 0 || bits > 64 {
		return "", false
	}
	width := strconv.Itoa(bits)
	// An operand is bit-faithful at the result width when it is a same-width integer (Elisa bitwise is
	// same-type) or a compile-time constant (`int2bv` reduces it modulo 2^W exactly).
	sameWidthOrConst := func(e ast.Expr) bool {
		if _, ebits, ok := smtIntWidthSign(tr.a.exprTypes[e]); ok && ebits == bits {
			return true
		}
		_, isConst := tr.a.constIntValue(e)
		return isConst
	}
	if !sameWidthOrConst(n.Left) {
		return "", false
	}
	var bvop, rbv string
	switch n.Op {
	case lexer.TOKEN_AMPERSAND:
		bvop = "bvand"
	case lexer.TOKEN_PIPE:
		bvop = "bvor"
	case lexer.TOKEN_CARET:
		bvop = "bvxor"
	case lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
		c, isConst := tr.a.constIntValue(n.Right)
		if !isConst || c < 0 || c >= int64(bits) {
			return "", false
		}
		rbv = "((_ int2bv " + width + ") " + strconv.FormatInt(c, 10) + ")"
		if n.Op == lexer.TOKEN_LSHIFT {
			bvop = "bvshl"
		} else if signed {
			bvop = "bvashr" // signed result ⇒ arithmetic right shift (matches LLVM AShr codegen)
		} else {
			bvop = "bvlshr" // unsigned result ⇒ logical right shift
		}
	default:
		return "", false
	}
	l, ok := tr.termEnv(n.Left, env)
	if !ok {
		// Operand-independent mask bound (see maskBoundAux): `X & C` ∈ [0, C] for a non-negative
		// constant C, regardless of X — so a masked extraction proves even when X (e.g.
		// `word >> runtime_shift`) is outside the modelable bitvector fragment.
		if n.Op == lexer.TOKEN_AMPERSAND {
			if c, isC := tr.a.constIntValue(n.Right); isC && c >= 0 {
				return tr.maskBoundAux(c), true
			}
		}
		// Operand-independent shift bound (see shiftRightBoundAux): `X >> k` (unsigned, constant k)
		// ∈ [0, 2^(bits-k)-1] regardless of X — so a goal like `unknownExpr >> 4 < 2^(bits-4)` proves
		// on the pure Int side even when X is outside the bitvector fragment.
		if n.Op == lexer.TOKEN_RSHIFT && !signed {
			if c, isC := tr.a.constIntValue(n.Right); isC && c >= 0 && c < int64(bits) {
				return tr.shiftRightBoundAux(bits, int(c)), true
			}
		}
		return "", false
	}
	lbv := "((_ int2bv " + width + ") " + l + ")"
	if rbv == "" {
		// Binary bitwise: the right operand must also be a same-width integer (or a constant).
		if !sameWidthOrConst(n.Right) {
			return "", false
		}
		r, ok := tr.termEnv(n.Right, env)
		if !ok {
			// Symmetric mask bound: `C & X` ∈ [0, C] for a non-negative constant C, regardless of X.
			if n.Op == lexer.TOKEN_AMPERSAND {
				if c, isC := tr.a.constIntValue(n.Left); isC && c >= 0 {
					return tr.maskBoundAux(c), true
				}
			}
			return "", false
		}
		rbv = "((_ int2bv " + width + ") " + r + ")"
	}
	return smtBitvectorRead("("+bvop+" "+lbv+" "+rbv+")", signed, bits), true
}

// signedDivCannotOverflow reports whether signed `left / right` (or `%`) provably avoids the one
// overflowing case INT_MIN / -1. Safe when the divisor cannot be -1 (a constant other than -1, or
// provably non-negative) OR the dividend cannot be INT_MIN (provably non-negative, or a known lower
// bound above the type minimum). An unsigned operand has no -1 and never overflows. When neither can be
// shown, the caller abstracts the result so the exact (overflowing) model is never trusted.
func (tr *smtTranslator) signedDivCannotOverflow(left, right ast.Expr) bool {
	signed, bits, ok := smtIntWidthSign(tr.a.exprTypes[left])
	if ok && !signed {
		return true // unsigned: no -1 divisor, cannot overflow
	}
	if c, isConst := tr.a.constIntValue(right); isConst && c != -1 {
		return true // divisor is a constant other than -1
	}
	if tr.a.provablyNonNeg(right) {
		return true // divisor >= 0 (and nonzero is already required) ⇒ != -1
	}
	if tr.a.provablyNonNeg(left) {
		return true // dividend >= 0 ⇒ != INT_MIN
	}
	if ok && signed && bits > 0 && bits <= 64 {
		minVal := -(int64(1) << (bits - 1))
		if f, aok := tr.a.affineOf(left, tr.a.currentScope); aok {
			if rng := tr.a.boundAffine(f, tr.a.currentScope); rng.loKnown && rng.lo > minVal {
				return true // dividend's lower bound is above the type minimum ⇒ != INT_MIN
			}
		}
	}
	return false
}

// provablyNoArithWrap reports whether `n` (an int `+`/`-`/`*`) cannot wrap — its true result is within
// the representable range of its width. It recognizes the no-underflow subtraction shapes (unsigned)
// and, for any width/signedness, an affine result whose interval lies inside [min, max]. Type-level
// reasoning alone is NOT sufficient (a wrapped result is still type-valid), so this is a VALUE check.
func (a *Analyzer) provablyNoArithWrap(n *ast.BinaryExpr, signed bool, bits int) bool {
	// No-underflow subtraction (e.g. `X - X%m`) is exact at any width and is the shape alignment math
	// relies on; it does not depend on the int64 interval prover, so it is safe for 64-bit too.
	if n.Op == lexer.TOKEN_MINUS && !signed && a.provablyNoUnsignedUnderflow(n.Left, n.Right) {
		return true
	}
	// A `requires`-supplied UPPER bound on this exact addition (`requires value + alignment <= 2^W-1`)
	// proves the unsigned sum cannot wrap — the affine interval prover cannot represent the 2^64-1 bound
	// (it computes in int64), so it is recognized symbolically here. This lets an overflow-guarded
	// postcondition (e.g. an AlignUp `ensure result >= value`) discharge from its entry precondition:
	// without the clean term the inner `+` would be emitted as `(mod … 2^W)`, which z3 cannot relate to
	// the surrounding div/mod/* goals. SOUNDNESS: this never makes the conclusion unconditional — it
	// requires an actual `requires` clause bounding the sum below the type maximum, so dropping the guard
	// (the C1 soundness gate) restores the exact-wrap model and the genuine overflow counterexample.
	if n.Op == lexer.TOKEN_PLUS && !signed && a.knownRequiresSumNoWrap(n, bits) {
		return true
	}
	// The affine interval prover now computes in OVERFLOW-CHECKED int64 (boundAffine via
	// mulInt64Checked/addInt64Checked, audit cluster E): a computation that would overflow int64 yields
	// an UNKNOWN (open) bound rather than a wrong one, so the in-range gate below is sound up to 64-bit
	// SIGNED — any int64-representable interval lies within [i64min, i64max]. UNSIGNED 64-bit stays on the
	// exact wrap path: its upper bound 2^64-1 is not int64-representable.
	if bits > 64 || (!signed && bits >= 64) {
		return false
	}
	f, ok := a.affineOf(n, a.currentScope)
	if !ok {
		return false
	}
	r := a.boundAffine(f, a.currentScope)
	if !r.loKnown || !r.hiKnown {
		return false
	}
	var lo, hi int64
	if signed {
		lo, hi = -(int64(1) << (bits - 1)), (int64(1)<<(bits-1))-1
	} else {
		lo, hi = 0, (int64(1)<<bits)-1
	}
	return r.lo >= lo && r.hi <= hi
}

// provablyNoUnsignedUnderflow reports whether `left - right` cannot underflow, i.e. `right <= left`
// holds for all admissible values. It recognizes the canonical alignment shape `X - (X % m)` (a
// remainder is always ≤ its nonnegative dividend) and, more generally, an affine difference whose
// interval lower bound is ≥ 0. Type-level non-negativity is NOT sufficient (an underflowed unsigned
// result is still nonnegative), so this is deliberately a VALUE-level check.
func (a *Analyzer) provablyNoUnsignedUnderflow(left, right ast.Expr) bool {
	l := stripOptimizationParens(left)
	if rem, ok := stripOptimizationParens(right).(*ast.BinaryExpr); ok && rem.Op == lexer.TOKEN_PERCENT {
		// `X - (X % m)`: the remainder of a non-negative dividend is ≤ the dividend.
		if a.provablyNonNeg(rem.Left) && exprsSyntacticallyEqual(l, stripOptimizationParens(rem.Left)) {
			return true
		}
	}
	diff := &ast.BinaryExpr{Position: left.Pos(), Left: left, Op: lexer.TOKEN_MINUS, Right: right}
	if f, ok := a.affineOf(diff, a.currentScope); ok {
		if r := a.boundAffine(f, a.currentScope); r.loKnown && r.lo >= 0 {
			return true
		}
	}
	// A `requires`-supplied relational bound (`requires b <= a`, `requires a >= b`, …) establishes
	// `left >= right` symbolically, which the constant-interval prover above cannot see (it relates two
	// variables, not a variable to a constant). Recognizing it here emits the CLEAN `left - right` term
	// — which the solver discharges trivially — instead of the wrapped `(mod … 2^W)` form that z3 stalls
	// on. This is what lets the natural precondition pattern prove `ensure result <= a; return a - b`.
	if a.knownRequiresGE(left, right) {
		return true
	}
	return false
}

// knownRequiresGE reports whether the enclosing function's `requires` clauses imply `left >= right`.
// Only IMMUTABLE integer identifiers qualify: a `requires` constrains a parameter's ENTRY value, so it
// stays valid for a later subtraction only if neither operand has been reassigned since entry. A clause
// is read as a conjunction; any conjunct of the form `left >= right`, `left > right`, `right <= left`,
// or `right < left` suffices (each implies `left - right` cannot underflow for unsigned operands).
func (a *Analyzer) knownRequiresGE(left, right ast.Expr) bool {
	if a == nil || a.currentFuncDecl == nil || a.currentScope == nil {
		return false
	}
	ln, lok := immutableIntIdentName(a, a.currentScope, stripOptimizationParens(left))
	rn, rok := immutableIntIdentName(a, a.currentScope, stripOptimizationParens(right))
	if !lok || !rok {
		return false
	}
	for _, req := range a.currentFuncDecl.Requires {
		if requiresConjunctImpliesGE(req, ln, rn) {
			return true
		}
	}
	return false
}

// knownRequiresSumNoWrap reports whether the enclosing function's `requires` clauses prove the unsigned
// addition `n` (`a + b`) cannot wrap at `bits` width — i.e. some conjunct `a + b <= C` (or `C >= a + b`)
// with a constant `C <= 2^bits - 1` and `n`'s SAME operands. Only IMMUTABLE-rooted addends qualify: a
// `requires` constrains entry values, so it stays valid only if neither operand was reassigned since
// entry. The bound is read structurally (the requires LHS sum must be syntactically the same two
// addends as `n`, order-insensitive), so this can only conclude no-wrap for a sum the precondition
// actually guards — making the conclusion conditional, never unconditional.
func (a *Analyzer) knownRequiresSumNoWrap(n *ast.BinaryExpr, bits int) bool {
	if a == nil || a.currentFuncDecl == nil || a.currentScope == nil || bits <= 0 || bits > 64 {
		return false
	}
	// Both addends must be immutable since entry for the requires fact to remain valid here.
	if _, ok := immutableIntIdentName(a, a.currentScope, stripOptimizationParens(n.Left)); !ok {
		return false
	}
	if _, ok := immutableIntIdentName(a, a.currentScope, stripOptimizationParens(n.Right)); !ok {
		return false
	}
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bits)), big.NewInt(1))
	for _, req := range a.currentFuncDecl.Requires {
		if a.requiresConjunctBoundsSum(req, n, max) {
			return true
		}
	}
	return false
}

// requiresConjunctBoundsSum reports whether a `requires` conjunct establishes `sum <= max` where `sum`
// is the same two-addend addition as `n` (`a + b`, order-insensitive). Accepts `sum <= C` and `C >= sum`.
func (a *Analyzer) requiresConjunctBoundsSum(e ast.Expr, n *ast.BinaryExpr, max *big.Int) bool {
	bin, ok := stripOptimizationParens(e).(*ast.BinaryExpr)
	if !ok {
		return false
	}
	if bin.Op == lexer.TOKEN_AND {
		return a.requiresConjunctBoundsSum(bin.Left, n, max) || a.requiresConjunctBoundsSum(bin.Right, n, max)
	}
	var sumSide, constSide ast.Expr
	switch bin.Op {
	case lexer.TOKEN_LTEQ: // sum <= C
		sumSide, constSide = bin.Left, bin.Right
	case lexer.TOKEN_GTEQ: // C >= sum
		sumSide, constSide = bin.Right, bin.Left
	default:
		return false
	}
	cBig, ok := a.unsignedConstBound(constSide)
	if !ok {
		return false
	}
	// cBig <= max (= 2^bits-1) proves the user-supplied sum bound forbids the wrap.
	if cBig.Cmp(max) > 0 {
		return false
	}
	sum, ok := stripOptimizationParens(sumSide).(*ast.BinaryExpr)
	if !ok || sum.Op != lexer.TOKEN_PLUS {
		return false
	}
	return additionsSameOperands(sum, n)
}

// unsignedConstBound returns the non-negative big.Int value of a constant `+`-bound expression. It
// handles a u64-max literal like 0xFFFFFFFFFFFFFFFF (which does not fit int64, so the const folder reads
// it as -1): the raw IntLit string is parsed directly as a big.Int. Other forms fall back to the int64
// const evaluator; a negative result is rejected (a sum upper bound below zero is meaningless here).
func (a *Analyzer) unsignedConstBound(e ast.Expr) (*big.Int, bool) {
	if lit, ok := stripOptimizationParens(e).(*ast.IntLit); ok && lit != nil {
		base := 10
		s := lit.Value
		if lit.IsHex {
			base = 16
			s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
		}
		s = strings.ReplaceAll(s, "_", "")
		if v, ok := new(big.Int).SetString(s, base); ok && v.Sign() >= 0 {
			return v, true
		}
	}
	if c, ok := a.constIntValue(e); ok && c >= 0 {
		return big.NewInt(c), true
	}
	return nil, false
}

// additionsSameOperands reports whether two `+` expressions add the same pair of lvalue operands
// (order-insensitive), using the path-stable SMT projection name for each addend.
func additionsSameOperands(x, y *ast.BinaryExpr) bool {
	xl, xr := smtProjectionName(stripOptimizationParens(x.Left)), smtProjectionName(stripOptimizationParens(x.Right))
	yl, yr := smtProjectionName(stripOptimizationParens(y.Left)), smtProjectionName(stripOptimizationParens(y.Right))
	if xl == "" || xr == "" || yl == "" || yr == "" {
		return false
	}
	return (xl == yl && xr == yr) || (xl == yr && xr == yl)
}

func requiresConjunctImpliesGE(e ast.Expr, geName, leName string) bool {
	bin, ok := stripOptimizationParens(e).(*ast.BinaryExpr)
	if !ok {
		return false
	}
	if bin.Op == lexer.TOKEN_AND {
		return requiresConjunctImpliesGE(bin.Left, geName, leName) || requiresConjunctImpliesGE(bin.Right, geName, leName)
	}
	li, lok := stripOptimizationParens(bin.Left).(*ast.Ident)
	ri, rok := stripOptimizationParens(bin.Right).(*ast.Ident)
	if !lok || !rok || li == nil || ri == nil {
		return false
	}
	switch bin.Op {
	case lexer.TOKEN_GTEQ, lexer.TOKEN_GT: // geName >= leName  /  geName > leName
		return li.Name == geName && ri.Name == leName
	case lexer.TOKEN_LTEQ, lexer.TOKEN_LT: // leName <= geName  /  leName < geName
		return li.Name == leName && ri.Name == geName
	}
	return false
}

// exprsSyntacticallyEqual reports whether two expressions are the same lvalue path (ident or nested
// field access). It is intentionally conservative — only the shapes the underflow check needs — and
// uses the SMT projection name (already path-stable for idents/fields) for the comparison.
func exprsSyntacticallyEqual(x, y ast.Expr) bool {
	switch x.(type) {
	case *ast.Ident, *ast.FieldExpr, *ast.ParenExpr:
		switch y.(type) {
		case *ast.Ident, *ast.FieldExpr, *ast.ParenExpr:
			return smtProjectionName(x) == smtProjectionName(y)
		}
	}
	return false
}

// smtPow2 returns 2^bits as a decimal literal (bits in [1, 64]).
func smtPow2(bits int) string {
	return new(big.Int).Lsh(big.NewInt(1), uint(bits)).String()
}

// markIntVar records a free var's integer type so factPreamble can assert its true representable
// range — `[0, 2^width)` for unsigned, `[-2^(width-1), 2^(width-1))` for signed. These bounds are
// guaranteed by the type and therefore always sound; they make the wraparound model precise.
func (tr *smtTranslator) markIntVar(name string, t Type) {
	signed, bits, ok := smtIntWidthSign(t)
	if !ok || bits <= 0 || bits > 64 {
		// Width unknown (e.g. an abstract numeric) — fall back to the non-negativity flag alone.
		if smtTypeNonNegative(t) {
			tr.nonNegDecls[name] = true
		}
		return
	}
	if signed {
		tr.signedBits[name] = bits
	} else {
		tr.nonNegDecls[name] = true
		tr.unsignedBits[name] = bits
	}
}

// provablyNonNeg reports whether an expression is provably ≥ 0 — by an unsigned type, or by the
// interval prover's lower bound. Used to gate sound division/modulo translation.
func (a *Analyzer) provablyNonNeg(expr ast.Expr) bool {
	if t := a.exprTypes[expr]; t != nil && indexTypeGuaranteedNonNegative(t) {
		return true
	}
	if f, ok := a.affineOf(expr, a.currentScope); ok {
		r := a.boundAffine(f, a.currentScope)
		if r.loKnown && r.lo >= 0 {
			return true
		}
	}
	return false
}

// provablyPositive reports whether an expression is provably ≥ 1 — a constant ≥ 1, or an interval
// lower bound ≥ 1. (An unsigned type alone only gives ≥ 0, so it does not qualify.)
func (a *Analyzer) provablyPositive(expr ast.Expr) bool {
	if c, ok := a.constIntValue(expr); ok {
		return c >= 1
	}
	if f, ok := a.affineOf(expr, a.currentScope); ok {
		r := a.boundAffine(f, a.currentScope)
		if r.loKnown && r.lo >= 1 {
			return true
		}
	}
	return false
}

// provablyNegative reports whether an expression is provably <= -1.
func (a *Analyzer) provablyNegative(expr ast.Expr) bool {
	if c, ok := a.constIntValue(expr); ok {
		return c <= -1
	}
	if f, ok := a.affineOf(expr, a.currentScope); ok {
		r := a.boundAffine(f, a.currentScope)
		if r.hiKnown && r.hi <= -1 {
			return true
		}
	}
	return false
}

// provablyNonZero reports whether an expression is provably outside zero. This is the soundness gate
// for SMT division/modulo because SMT-LIB's arithmetic is total at zero but Elisa division is not.
func (a *Analyzer) provablyNonZero(expr ast.Expr) bool {
	return a.provablyPositive(expr) || a.provablyNegative(expr)
}

// boolTerm lowers a boolean-valued expression: comparisons, and/or/not, parens, and bool literals.
// `env` maps an Elisa identifier (notably the law's `self`) to a pre-built SMT term.
func (tr *smtTranslator) boolTerm(expr ast.Expr, env map[string]string) (string, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return tr.boolTerm(n.Inner, env)
	case *ast.QuantifierExpr:
		if n.In != nil {
			// `forall x in s` / `exists x in s` over a SET: bind `x` to a fresh key symbol of the set's
			// key sort, guard membership with `(select s x)`, and emit a `:pattern` on that select so z3
			// has a deterministic E-matching trigger (mirrors the array path; MBQI completes the rest).
			if ssym, ksort, ok := tr.setTermEnv(n.In, env); ok {
				if len(n.Vars) != 1 {
					return "", false
				}
				kSym := "q_" + n.Vars[0] + "_k"
				qenv := make(map[string]string, len(env)+1)
				for k, v := range env {
					qenv[k] = v
				}
				qenv[n.Vars[0]] = kSym
				body, ok := tr.boolTerm(n.Body, qenv)
				if !ok {
					return "", false
				}
				mem := "(select " + ssym + " " + kSym + ")"
				if n.Exists {
					return "(exists ((" + kSym + " " + ksort + ")) (! (and " + mem + " " + body + ") :pattern (" + mem + ")))", true
				}
				return "(forall ((" + kSym + " " + ksort + ")) (! (or (not " + mem + ") " + body + ") :pattern (" + mem + ")))", true
			}
			// `forall (key,v) in d` / `forall key in d` over a DICT: bind the key to a fresh key symbol
			// and (when a value binder is present) the value to `(select d_vals key)`. Membership
			// `(select d_keys key)` guards the body, and is the `:pattern` trigger.
			if dsym, ksort, ok := tr.dictTermEnv(n.In, env); ok {
				if len(n.Vars) != 1 && len(n.Vars) != 2 {
					return "", false
				}
				kSym := "q_" + n.Vars[0] + "_k"
				qenv := make(map[string]string, len(env)+2)
				for k, v := range env {
					qenv[k] = v
				}
				qenv[n.Vars[0]] = kSym
				if len(n.Vars) == 2 {
					qenv[n.Vars[1]] = "(select " + dsym + "_vals " + kSym + ")"
				}
				body, ok := tr.boolTerm(n.Body, qenv)
				if !ok {
					return "", false
				}
				mem := "(select " + dsym + "_keys " + kSym + ")"
				if n.Exists {
					return "(exists ((" + kSym + " " + ksort + ")) (! (and " + mem + " " + body + ") :pattern (" + mem + ")))", true
				}
				return "(forall ((" + kSym + " " + ksort + ")) (! (or (not " + mem + ") " + body + ") :pattern (" + mem + ")))", true
			}
			if len(n.Vars) != 1 {
				return "", false
			}
			arr, ok := tr.arrayTermEnv(n.In, env)
			if !ok {
				return "", false
			}
			idxSym := "q_" + n.Vars[0] + "_idx"
			qenv := make(map[string]string, len(env)+1)
			for k, v := range env {
				qenv[k] = v
			}
			qenv[n.Vars[0]] = "(select " + arr + " " + idxSym + ")"
			body, ok := tr.boolTerm(n.Body, qenv)
			if !ok {
				return "", false
			}
			lenSym := arr + "_len"
			tr.lenDecls[lenSym] = true
			guard := "(and (<= 0 " + idxSym + ") (< " + idxSym + " " + lenSym + "))"
			// E-matching trigger (docs/90 brick 90-16): a `forall x in xs` / `forall i in xs.indices`
			// quantifier binds the index `idxSym` and models the element as `(select arr idxSym)`. That
			// select term is the canonical instantiation pattern, so emit it as `:pattern`. Previously this
			// `In`-source path went out as a BARE quantifier, leaving z3 to auto-pattern infer (or loop on a
			// bad inferred trigger). Soundness/completeness hold: the trigger only guides E-matching, and
			// MBQI (preamble-forced on) still completes any goal the trigger alone would miss.
			elemSel := "(select " + arr + " " + idxSym + ")"
			if n.Exists {
				return "(exists ((" + idxSym + " Int)) (! (and " + guard + " " + body + ") :pattern (" + elemSel + ")))", true
			}
			return "(forall ((" + idxSym + " Int)) (! (or (not " + guard + ") " + body + ") :pattern (" + elemSel + ")))", true
		}
		// Bind each quantifier variable to a fresh SMT Int symbol (prefix "q_" so it never collides
		// with a free variable's "v_" symbol), then translate the body under the extended environment.
		qenv := make(map[string]string, len(env)+len(n.Vars))
		for k, v := range env {
			qenv[k] = v
		}
		decls := make([]string, 0, len(n.Vars))
		for _, v := range n.Vars {
			sym := "q_" + v
			qenv[v] = sym
			decls = append(decls, "("+sym+" Int)")
		}
		body, ok := tr.boolTerm(n.Body, qenv)
		if !ok {
			return "", false
		}
		q := "forall"
		if n.Exists {
			q = "exists"
		}
		// Attach an E-matching trigger (docs/90 brick 90-16): for a quantifier over array contents, the
		// `(select <arr> <idx>)` subterms whose index mentions a bound variable are the canonical
		// instantiation pattern. Emitting them as `(! body :pattern (...))` gives z3 a deterministic,
		// cheap instantiation strategy instead of relying on auto-pattern inference. Soundness/
		// completeness are preserved: triggers only guide E-matching, and z3's MBQI (on by default)
		// still completes any goal the trigger alone would miss. A purely arithmetic quantifier (no
		// select term mentioning a binder) gets no pattern — there is no good ground trigger, so it is
		// left to MBQI exactly as before.
		if triggers := tr.collectSelectTriggers(n.Body, qenv, n.Vars); len(triggers) > 0 {
			body = "(! " + body + " :pattern (" + strings.Join(triggers, " ") + "))"
		}
		return "(" + q + " (" + strings.Join(decls, " ") + ") " + body + ")", true
	case *ast.BoolLit:
		if n.Value {
			return "true", true
		}
		return "false", true
	case *ast.Ident:
		if env != nil {
			if bound, ok := env[n.Name]; ok {
				return bound, true
			}
		}
		if c, ok := tr.a.evalConstBoolExpr(n); ok {
			if c {
				return "true", true
			}
			return "false", true
		}
		// A free IMMUTABLE bool-typed identifier (a bool param/immutable local) → a stable SMT Bool
		// const, so a boolean postcondition over it (`ensure result == (p != q)`) can discharge. Only
		// immutable bindings qualify (a reassigned bool would make the const stale).
		if sym, ok := tr.a.currentScope.Lookup(n.Name); ok && sym != nil && !sym.Mutable && IsBoolType(sym.Type) {
			name := smtBoolVar(n.Name)
			tr.boolDecls[name] = true
			return name, true
		}
		return "", false
	case *ast.FieldExpr:
		if env != nil {
			if bound, ok := env[smtProjectionName(n)]; ok {
				return bound, true
			}
		}
		fieldType := tr.a.exprTypes[n]
		if fieldType == nil {
			if resolved, ok := tr.a.fieldReadResolvedType(n); ok {
				fieldType = resolved
			}
		}
		if IsBoolType(fieldType) {
			pred := smtBoolVar(smtProjectionName(n))
			tr.boolDecls[pred] = true
			return pred, true
		}
		return "", false
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			inner, ok := tr.boolTerm(n.Operand, env)
			if !ok {
				return "", false
			}
			if inner == "true" {
				return "false", true
			}
			if inner == "false" {
				return "true", true
			}
			return "(not " + inner + ")", true
		}
		return "", false
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND, lexer.TOKEN_OR:
			l, ok := tr.boolTerm(n.Left, env)
			if !ok {
				return "", false
			}
			if n.Op == lexer.TOKEN_OR && l == "true" {
				return "true", true
			}
			if n.Op == lexer.TOKEN_AND && l == "false" {
				return "false", true
			}
			r, ok := tr.boolTerm(n.Right, env)
			if !ok {
				return "", false
			}
			if n.Op == lexer.TOKEN_OR {
				if r == "true" {
					return "true", true
				}
				if l == "false" {
					return r, true
				}
			}
			if n.Op == lexer.TOKEN_AND {
				if r == "false" {
					return "false", true
				}
				if l == "true" {
					return r, true
				}
			}
			conn := "and"
			if n.Op == lexer.TOKEN_OR {
				conn = "or"
			}
			return "(" + conn + " " + l + " " + r + ")", true
		case lexer.TOKEN_IN:
			// Membership `p in s` / `k in d` (docs/90 set/dict modeling). A set is its membership
			// predicate directly; a dict's membership is its `<d>_keys` array. `not in` already lowers to
			// `not (x in y)` at parse time, so only the positive form is handled here.
			if ssym, ksort, ok := tr.setTermEnv(n.Right, env); ok {
				elem, ok := tr.keyTermEnv(n.Left, ksort, env)
				if !ok {
					return "", false
				}
				return "(select " + ssym + " " + elem + ")", true
			}
			if dsym, ksort, ok := tr.dictTermEnv(n.Right, env); ok {
				key, ok := tr.keyTermEnv(n.Left, ksort, env)
				if !ok {
					return "", false
				}
				return "(select " + dsym + "_keys " + key + ")", true
			}
			return "", false
		case lexer.TOKEN_GT, lexer.TOKEN_GTEQ, lexer.TOKEN_LT, lexer.TOKEN_LTEQ:
			l, ok := tr.termEnv(n.Left, env)
			if !ok {
				return "", false
			}
			r, ok := tr.termEnv(n.Right, env)
			if !ok {
				return "", false
			}
			if cmp, ok := tr.bitvectorCompare(n, l, r, env); ok {
				return cmp, true
			}
			return smtCompare(n.Op, l, r), true
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
			// Pointer-null test (`p == null` / `p != null`). Model null-ness as a Bool predicate keyed by
			// the pointer's syntactic path, so the same pointer's null-ness is consistent between a branch
			// guard and the obligation. This lets `ensure (p != null) or (result == false)` discharge: the
			// fall-through guard `p != null` and the disjunct refer to the same predicate.
			if ptr := nullComparePointer(n); ptr != nil {
				pred := "isnull_" + smtProjectionName(stripOptimizationParens(ptr))
				tr.boolDecls[pred] = true
				if n.Op == lexer.TOKEN_EQEQ {
					return pred, true
				}
				return "(not " + pred + ")", true
			}
			// Numeric equality is the common case; try integer terms first.
			if l, lok := tr.termEnv(n.Left, env); lok {
				if r, rok := tr.termEnv(n.Right, env); rok {
					if l == r {
						if n.Op == lexer.TOKEN_EQEQ {
							return "true", true
						}
						return "false", true
					}
					return smtCompare(n.Op, l, r), true
				}
			}
			// Boolean equality: both sides are bool-valued (e.g. `result == ((x % m) == 0)`). SMT `=`
			// is polymorphic, so equate the two Bool terms directly (negated for `!=`).
			if bl, blok := tr.boolTerm(n.Left, env); blok {
				if br, brok := tr.boolTerm(n.Right, env); brok {
					if bl == br {
						if n.Op == lexer.TOKEN_EQEQ {
							return "true", true
						}
						return "false", true
					}
					eq := "(= " + bl + " " + br + ")"
					if n.Op == lexer.TOKEN_BANGEQ {
						return "(not " + eq + ")", true
					}
					return eq, true
				}
			}
			return "", false
		default:
			return "", false
		}
	case *ast.CallExpr:
		// SURFACE dict membership `d.contains(k)` maps to the dict key array. This must run BEFORE the
		// structural-predicate path, which would otherwise canonicalize `arena_dict_contains` into an
		// opaque predicate + defining equation, severing it from the dict model (so a `requires
		// d.contains(k)` guard would no longer constrain the modeled key set).
		if sel, ok := tr.dictMethodCall(n, "keys", env); ok {
			return sel, true
		}
		// Try structural-predicate canonicalization first (a `ghost def ... -> bool` over a recursive
		// datatype canonicalizes to one opaque Bool symbol + its bounded structural defining equation —
		// see analyzer_structural_induction.go); fall back to the general ghost bool-call path for
		// non-predicate bool-returning calls so the docs/101 ghost-beyond-int behavior is preserved.
		if sym, ok := tr.boolPredicateCallSym(n, env); ok {
			return sym, true
		}
		return tr.callResultBoolTermEnv(n, env)
	default:
		return "", false
	}
}

// bitvectorCompare lowers fixed-width integer ordering to SMT bitvector ordering. The integer term
// lane already models machine arithmetic by wrapping results back into the source type's value range;
// comparing the corresponding bit patterns preserves the same source-level signed/unsigned ordering
// for overflow-sensitive guards such as `addr + len >= addr`.
func (tr *smtTranslator) bitvectorCompare(n *ast.BinaryExpr, l, r string, env map[string]string) (string, bool) {
	if smtEnvHasQuantifierBinder(env) {
		return "", false
	}
	// In requires-hypothesis mode an unsigned `sum <= 2^W-1` (a no-overflow precondition) must be read as
	// the ℤ bound, not the wrapped bvule (which `int2bv` makes vacuously true). Decline here so boolTerm
	// falls through to the clean ℤ comparison. Gated to the exact overflow-guard shape the body's clean
	// term is justified by (provablyNoArithWrap), so it cannot over-assume on any other comparison.
	if tr.requiresHypMode && tr.isUnsignedSumTypeMaxGuard(n) {
		return "", false
	}
	if !tr.containsFixedWidthMachineArith(n.Left) && !tr.containsFixedWidthMachineArith(n.Right) {
		return "", false
	}
	signed, bits, ok := tr.compareWidthSign(n.Left, n.Right)
	if !ok || signed || bits <= 0 || bits > 64 {
		return "", false
	}
	// Both operands provably non-negative ⟹ their ℤ values lie in [0, 2^bits): the term lane wraps every
	// machine `+`/`-`/`*` modulo 2^bits and asserts the [0,2^bits) range of each unsigned variable / length
	// symbol, so a non-negative such term cannot exceed the width. Over that range the unsigned bitvector
	// ordering is IDENTICAL to the clean ℤ ordering, so decline the `int2bv` encoding and let boolTerm emit
	// the plain ℤ comparison. This is not just an optimization: the `(_ int2bv …)`/`bvult` mix forces a
	// bitvector+int query that z3 IN INCREMENTAL MODE (push/pop, mbqi) can answer `unknown` on even when it
	// is valid — which silently dropped the loop guard `j + 1 < self.xs.count` and blocked the field-shift
	// termination proofs. SOUNDNESS: restricted to provably-non-negative operands, where ℤ `<` is exactly
	// the unsigned `<`; a comparison that could observe wrap (operand not provably non-negative) keeps the
	// exact bitvector model below.
	if tr.a.provablyNonNeg(n.Left) && tr.a.provablyNonNeg(n.Right) {
		return "", false
	}
	op := ""
	switch n.Op {
	case lexer.TOKEN_GT:
		op = "bvugt"
	case lexer.TOKEN_GTEQ:
		op = "bvuge"
	case lexer.TOKEN_LT:
		op = "bvult"
	case lexer.TOKEN_LTEQ:
		op = "bvule"
	default:
		return "", false
	}
	width := strconv.Itoa(bits)
	return "(" + op + " ((_ int2bv " + width + ") " + l + ") ((_ int2bv " + width + ") " + r + "))", true
}

// isUnsignedSumTypeMaxGuard reports whether `n` is an upper-bound comparison of an unsigned addition by
// a constant within its width — `a + b <= C` or `C >= a + b` with `C <= 2^W-1` (W the sum's width). This
// is the no-overflow precondition shape; in requiresHypMode it is read in ℤ (declining bitvectorCompare).
func (tr *smtTranslator) isUnsignedSumTypeMaxGuard(n *ast.BinaryExpr) bool {
	var sumSide, constSide ast.Expr
	switch n.Op {
	case lexer.TOKEN_LTEQ:
		sumSide, constSide = n.Left, n.Right
	case lexer.TOKEN_GTEQ:
		sumSide, constSide = n.Right, n.Left
	default:
		return false
	}
	sum, ok := stripOptimizationParens(sumSide).(*ast.BinaryExpr)
	if !ok || sum.Op != lexer.TOKEN_PLUS {
		return false
	}
	signed, bits, ok := smtIntWidthSign(tr.a.exprTypes[sum])
	if !ok || signed || bits <= 0 || bits > 64 {
		return false
	}
	c, ok := tr.a.unsignedConstBound(constSide)
	if !ok {
		return false
	}
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bits)), big.NewInt(1))
	return c.Cmp(max) <= 0
}

func smtEnvHasQuantifierBinder(env map[string]string) bool {
	for _, term := range env {
		if strings.HasPrefix(term, "q_") || strings.Contains(term, " q_") {
			return true
		}
	}
	return false
}

func (tr *smtTranslator) compareWidthSign(left, right ast.Expr) (signed bool, bits int, ok bool) {
	ls, lb, lok := smtIntWidthSign(tr.a.exprTypes[left])
	rs, rb, rok := smtIntWidthSign(tr.a.exprTypes[right])
	if lok && rok && ls == rs && lb == rb {
		return ls, lb, true
	}
	if lok && isIntegerLiteralExpr(right) {
		return ls, lb, true
	}
	if rok && isIntegerLiteralExpr(left) {
		return rs, rb, true
	}
	return false, 0, false
}

func (tr *smtTranslator) containsFixedWidthMachineArith(expr ast.Expr) bool {
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR:
			if _, bits, ok := smtIntWidthSign(tr.a.exprTypes[n]); ok && bits > 0 && bits <= 64 {
				return true
			}
		}
		return tr.containsFixedWidthMachineArith(n.Left) || tr.containsFixedWidthMachineArith(n.Right)
	case *ast.UnaryExpr:
		return tr.containsFixedWidthMachineArith(n.Operand)
	case *ast.CastExpr:
		return tr.containsFixedWidthMachineArith(n.Operand)
	case *ast.TernaryExpr:
		return tr.containsFixedWidthMachineArith(n.Cond) ||
			tr.containsFixedWidthMachineArith(n.Value) ||
			tr.containsFixedWidthMachineArith(n.Alt)
	default:
		return false
	}
}

func isIntegerLiteralExpr(expr ast.Expr) bool {
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.IntLit:
		return true
	case *ast.UnaryExpr:
		return n.Op == lexer.TOKEN_MINUS && isIntegerLiteralExpr(n.Operand)
	default:
		return false
	}
}

// collectSelectTriggers gathers the `(select <arr> <idx>)` SMT terms in a quantifier body whose index
// mentions one of the quantifier's bound variables — the canonical E-matching trigger for an
// array-element quantifier (docs/90 brick 90-16). It walks the AST body for IndexExpr nodes, lowers
// each through the same `qenv` (so the array/index symbols match the body), and keeps the distinct
// ones referencing a binder, in stable order. Returns nil when there is no array indexing on a binder
// (a purely arithmetic quantifier), leaving that quantifier patternless for MBQI.
func (tr *smtTranslator) collectSelectTriggers(body ast.Expr, qenv map[string]string, vars []string) []string {
	bound := make(map[string]bool, len(vars))
	for _, v := range vars {
		bound["q_"+v] = true
	}
	seen := map[string]bool{}
	var triggers []string
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch n := e.(type) {
		case *ast.ParenExpr:
			walk(n.Inner)
		case *ast.UnaryExpr:
			walk(n.Operand)
		case *ast.BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *ast.QuantifierExpr:
			walk(n.Body) // nested quantifier: its own binders may still mention ours
		case *ast.IndexExpr:
			if term, ok := tr.termEnv(n, qenv); ok && termMentionsAnyBinder(term, bound) && !seen[term] {
				seen[term] = true
				triggers = append(triggers, term)
			}
			walk(n.Object)
			walk(n.Index)
		}
	}
	walk(body)
	return triggers
}

func (tr *smtTranslator) collectQuantifierWitnessSelects(body ast.Expr, qenv map[string]string) {
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch n := e.(type) {
		case *ast.ParenExpr:
			walk(n.Inner)
		case *ast.UnaryExpr:
			walk(n.Operand)
		case *ast.BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *ast.TernaryExpr:
			walk(n.Cond)
			walk(n.Value)
			walk(n.Alt)
		case *ast.QuantifierExpr:
			return
		case *ast.IndexExpr:
			if term, ok := tr.termEnv(n, qenv); ok {
				if label := tr.witnessSelectLabel(n, qenv); label != "" {
					tr.ceExprs[label] = term
				}
			}
			walk(n.Object)
			walk(n.Index)
		}
	}
	walk(body)
}

func (tr *smtTranslator) witnessSelectLabel(n *ast.IndexExpr, env map[string]string) string {
	if n == nil {
		return ""
	}
	obj := tr.witnessExprLabel(n.Object, env)
	idx := tr.witnessExprLabel(n.Index, env)
	if obj == "" || idx == "" {
		return ""
	}
	return obj + "[" + idx + "]"
}

func (tr *smtTranslator) witnessExprLabel(expr ast.Expr, env map[string]string) string {
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.Ident:
		if env != nil {
			if bound, ok := env[n.Name]; ok {
				if label := tr.counterexampleLabelForTerm(bound); label != "" {
					return label
				}
			}
		}
		return n.Name
	case *ast.IntLit:
		if c, ok := tr.a.constIntValue(n); ok {
			return strconv.FormatInt(c, 10)
		}
	case *ast.FieldExpr:
		base := tr.witnessExprLabel(n.Object, env)
		if base == "" {
			return ""
		}
		return base + "." + n.Field
	case *ast.BinaryExpr:
		l := tr.witnessExprLabel(n.Left, env)
		r := tr.witnessExprLabel(n.Right, env)
		if l == "" || r == "" {
			return ""
		}
		switch n.Op {
		case lexer.TOKEN_PLUS:
			return l + "+" + r
		case lexer.TOKEN_MINUS:
			return l + "-" + r
		}
	}
	return ""
}

func (tr *smtTranslator) counterexampleLabelForTerm(term string) string {
	for name := range tr.arrayDecls {
		if smtVar(name) == term {
			return name
		}
	}
	for name := range tr.decls {
		if smtVar(name) == term {
			return name
		}
	}
	return ""
}

// termMentionsAnyBinder reports whether an SMT term string contains any of the bound `q_*` symbols as
// a whole token (so `q_i` does not spuriously match `q_index`).
func termMentionsAnyBinder(term string, bound map[string]bool) bool {
	for _, tok := range strings.FieldsFunc(term, func(r rune) bool {
		return r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	}) {
		if bound[tok] {
			return true
		}
	}
	return false
}

// freshAux mints a new under-constrained integer symbol for a sub-term we cannot model precisely but
// must keep PRESENT (so the surrounding clause can still discharge). It is constrained only by what
// provably holds — `>= 0` when nonNeg — never by a false relation, so it can never fabricate a proof.
func (tr *smtTranslator) freshAux(nonNeg bool) string {
	tr.auxSeq++
	v := "aux_" + smtInt(int64(tr.auxSeq))
	tr.auxVars = append(tr.auxVars, v)
	tr.auxDecls = append(tr.auxDecls, "(declare-const "+v+" Int)\n")
	if nonNeg {
		tr.auxDecls = append(tr.auxDecls, "(assert (>= "+v+" 0))\n")
	}
	return v
}

func (tr *smtTranslator) freshBoolAux() string {
	tr.auxSeq++
	v := "aux_bool_" + smtInt(int64(tr.auxSeq))
	tr.auxVars = append(tr.auxVars, v)
	tr.auxDecls = append(tr.auxDecls, "(declare-const "+v+" Bool)\n")
	return v
}

// maskBoundAux models `X & C` for a non-negative constant C when X's exact bitvector term is
// unavailable (e.g. X = `word >> runtime_shift`, outside the const-shift fragment). The result is
// ALWAYS in [0, C] regardless of X — its set bits are a subset of C's — so a fresh integer bounded
// to [0, C] is a sound OVER-approximation of the exact value. Soundness: the prover concludes only
// on `unsat` of the negated goal; widening the value's feasible region to all of [0, C] can only
// make that unsat harder to reach (decline), never fabricate one. This lets a masked extraction
// like `(word >> sh) & 0xF` prove `< 16` even though the runtime shift is unmodelable.
func (tr *smtTranslator) maskBoundAux(c int64) string {
	tr.auxSeq++
	v := "aux_" + smtInt(int64(tr.auxSeq))
	tr.auxVars = append(tr.auxVars, v)
	tr.auxDecls = append(tr.auxDecls, "(declare-const "+v+" Int)\n")
	tr.auxDecls = append(tr.auxDecls, "(assert (>= "+v+" 0))\n")
	tr.auxDecls = append(tr.auxDecls, "(assert (<= "+v+" "+smtInt(c)+"))\n")
	return v
}

// shiftRightBoundAux models `X >> k` for an unsigned W-bit value X and a constant k when X's exact
// bitvector term is unavailable. A logical right shift by k can only produce values in [0, 2^(W-k)-1]:
// it discards the low k bits and shifts in zeros at the top, so the result fits in W-k bits. A fresh
// integer bounded to [0, 2^(W-k)-1] is a sound OVER-approximation. Soundness: the prover concludes
// only on `unsat` of the negated goal; widening to all of [0, 2^(W-k)-1] can only make unsat harder
// (decline), never fabricate one. This lets goals like `unknownExpr >> 12 < 2^52` prove on the pure
// Int side without bitvector theory, avoiding the BV timeout on relational goals.
func (tr *smtTranslator) shiftRightBoundAux(bits, k int) string {
	tr.auxSeq++
	v := "aux_" + smtInt(int64(tr.auxSeq))
	tr.auxVars = append(tr.auxVars, v)
	hi := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bits-k)), big.NewInt(1)) // 2^(bits-k) - 1
	tr.auxDecls = append(tr.auxDecls, "(declare-const "+v+" Int)\n")
	tr.auxDecls = append(tr.auxDecls, "(assert (>= "+v+" 0))\n")
	tr.auxDecls = append(tr.auxDecls, "(assert (<= "+v+" "+hi.String()+"))\n")
	return v
}

// smtBitvectorRead reads a W-bit bitvector result back as its machine integer value. For an unsigned
// result that is `bv2nat(b)` ∈ [0, 2^W). For a signed result it is the exact two's-complement value
// `bv2nat(b) - 2^W * topbit(b)` ∈ [-2^(W-1), 2^(W-1)) — so the produced Int EQUALS the machine value
// and composes soundly with further Int reasoning (e.g. a sign-extension `(v << k) >> k` proves its
// signed range). The top bit is read via a 1-bit extract.
func smtBitvectorRead(bvExpr string, signed bool, bits int) string {
	if !signed {
		return "(bv2nat " + bvExpr + ")"
	}
	hi := strconv.Itoa(bits - 1)
	signBit := "(bv2nat ((_ extract " + hi + " " + hi + ") " + bvExpr + "))"
	return "(- (bv2nat " + bvExpr + ") (* " + smtPow2(bits) + " " + signBit + "))"
}

func (tr *smtTranslator) callResultTerm(call *ast.CallExpr) (string, bool) {
	return tr.callResultTermEnv(call, nil)
}

func (tr *smtTranslator) callResultTermEnv(call *ast.CallExpr, argEnv map[string]string) (string, bool) {
	if tr == nil || tr.a == nil || call == nil {
		return "", false
	}
	// SURFACE dict lookup `d.get(k)` (resolved to `arena_dict_get`) maps to the dict value array, so a
	// goal like `d.get(k) > 0` under a `d.contains(k)` guard reaches the dict quantifier model.
	if sel, ok := tr.dictMethodCall(call, "vals", argEnv); ok {
		return sel, true
	}
	resultType := tr.a.exprTypes[call]
	decl, direct := tr.a.resolveDirectCallFuncDecl(call)
	if resultType == nil && direct && decl != nil {
		if sym, ok := tr.a.symbolForFuncDecl(decl); ok {
			if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
				resultType = fnType.Return
			}
		}
	}
	if resultType == nil && !direct {
		// Abstract protocol-method call inside a contract (docs P1): its node was never analyzed in
		// this caller context (the predicate lives on the protocol decl), so recover the return type
		// from the bound protocol's method signature.
		resultType = tr.abstractMethodReturnType(call)
	}
	if !isSMTExactAssignmentType(resultType) || IsBoolType(resultType) {
		return "", false
	}
	// CANONICALIZATION: a function is deterministic, so two calls with syntactically-equal arguments
	// denote the same value and must share one SMT symbol. Without this, `g(n-1)` in a hypothesis and
	// `g(n-1)` in the obligation would be unrelated fresh symbols and nothing about a recursive function
	// could be transported between them. The key is the callee identity plus the canonical SMT terms of
	// the arguments (so `g(n - 1)` and `g(n-1)` match, and `g(x)` vs `g(y)` do not). If any argument is
	// outside the integer fragment we cannot form a stable key, so we fall back to a fresh aux (sound:
	// just less sharing). Only DIRECT calls to a known decl are canonicalized.
	args := proofCallArgs(call)
	var canonKey string
	if direct && decl != nil {
		if key, ok := tr.callCanonKeyEnv(decl, args, tr.canonKeyEnv(decl, argEnv)); ok {
			if sym, seen := tr.callCanon[key]; seen {
				return sym, true
			}
			canonKey = key
		}
	}
	// ABSTRACT protocol-method canonicalization (docs P1 behavioral subtyping): a call to a protocol
	// method through a bounded type param (`self.count()` in a contract, `c.count()` in a caller fact)
	// has no resolvable decl, but is still DETERMINISTIC — the same method on the same receiver denotes
	// one value. Key it by (method name, receiver SMT term, arg SMT terms) so the protocol-side
	// obligation and the caller-side hypothesis share a symbol and facts transport. This treats the
	// abstract method as an uninterpreted function, which is sound: we assert nothing about its result.
	if !direct || decl == nil {
		if key, ok := tr.abstractMethodCanonKey(call, argEnv); ok {
			if sym, seen := tr.callCanon[key]; seen {
				return sym, true
			}
			canonKey = key
		}
	}
	ret := tr.freshAux(smtTypeNonNegative(resultType))
	if canonKey != "" {
		tr.callCanon[canonKey] = ret
	}
	if !direct || decl == nil {
		return ret, true
	}
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	env, ok := tr.a.smtEnvForCallSubstEnv(tr, subst, argEnv)
	if !ok {
		return ret, true
	}
	env["result"] = ret
	guard := "true"
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		h, ok := tr.boolTerm(req, env)
		if !ok {
			return ret, true
		}
		if guard == "true" {
			guard = h
		} else {
			guard = "(and " + guard + " " + h + ")"
		}
	}
	// (1) Assume the callee's already-verified `ensure` clauses at the call (existing behavior): a
	// contract-sound postcondition holds for the actual arguments.
	recursiveProofCall := tr.a.isRecursiveProofCall(decl)
	var recursiveCert recursiveProofCertificate
	if recursiveProofCall {
		var ok bool
		recursiveCert, ok = tr.a.recursiveCallCertificate(tr.a.currentFuncDecl, decl, call)
		if !ok {
			recursiveProofCall = false
		}
	}
	if recursiveProofCall && !tr.a.recursiveCallRequiresProven(decl, call) {
		tr.a.recordProof(call.Pos(), "recursive proof declined for "+decl.Name, "callee requires", ProofRuntime)
		tr.a.proofLint(call.Pos(), "recursive proof declined: callee requires for %q could not be proven at this recursive call", decl.Name)
		recursiveProofCall = false
	}
	if recursiveProofCall && !tr.a.recursiveCallHasVerifiedDecrease(decl, call) {
		recursiveProofCall = false
	}
	for _, ensure := range decl.EnsureValues {
		if ensure == nil {
			continue
		}
		if tr.a.isRecursiveProofCall(decl) && !recursiveProofCall {
			continue
		}
		if h, ok := tr.boolTerm(ensure, env); ok {
			if guard == "true" {
				tr.auxDecls = append(tr.auxDecls, "(assert "+h+")\n")
			} else {
				tr.auxDecls = append(tr.auxDecls, "(assert (=> "+guard+" "+h+"))\n")
			}
			if recursiveProofCall {
				tr.a.recordProof(ensure.Pos(), "recursive IH of "+decl.Name+" ("+recursiveCert.label()+")", "ensure", ProofProvenContract)
			}
		}
	}
	// (2) DEFINING EQUATION for a PURE, TOTAL function: assert `f(args) == body[params:=args]`, so the
	// prover can reason THROUGH the function (and, for a terminating recursive function, perform
	// induction — the body's own recursive sub-call canonicalizes to a symbol the IH/equation can
	// constrain). SOUNDNESS GATES (tr.callEquationEligible): the function must be pure (a single
	// integer-returning `return <pure-expr>` over integer params, no mutation/loops/effects) AND, if it
	// is recursive, have a VERIFIED `decreases` measure. A non-terminating function's equation is
	// inconsistent (it would let `f(n) == f(n) + 1` style nonsense be assumed); the termination gate is
	// exactly what forbids that. The instantiation is bounded by callEqMaxUnroll so the axiom set stays
	// finite.
	tr.emitDefiningEquationWithEnv(call, decl, ret, subst, canonKey, guard, recursiveProofCall, env)
	return ret, true
}

// abstractMethodReturnType recovers the return type of an abstract protocol-method call (one with no
// resolvable concrete decl) from the bound protocol's method signature. It handles both the method
// form (`recv.m(...)`, receiver typed as a bound type param) and the static type-path form
// (`T.m(...)`). Returns nil when the protocol/method cannot be identified.
func (tr *smtTranslator) abstractMethodReturnType(call *ast.CallExpr) Type {
	if tr == nil || tr.a == nil || call == nil {
		return nil
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field == "" {
		return nil
	}
	var iface *StaticInterface
	if obj, ok := fieldExpr.Object.(*ast.Ident); ok {
		// Static type-path form: object is a bound type param `T` with interface bound.
		if bound, isTypeParam := tr.a.lookupTypeParamInterface(obj.Name); isTypeParam {
			iface = bound
		}
	}
	if iface == nil {
		// Method form: the receiver expression's resolved type is a bound type param.
		if recvType := tr.a.exprTypes[fieldExpr.Object]; recvType != nil {
			if tp, ok := stripRefForBounds(recvType).(*TypeParamType); ok {
				if bound, isTypeParam := tr.a.lookupTypeParamInterface(tp.Name); isTypeParam {
					iface = bound
				}
			}
		}
	}
	if iface == nil {
		// The receiver is an unresolved contract identifier (`self` on a protocol-decl predicate that
		// was never analyzed in this caller context). Fall back to the in-scope bound type-param
		// interfaces: find the unique one declaring a method of this name.
		for i := len(tr.a.typeParamInterfaceScopes) - 1; i >= 0 && iface == nil; i-- {
			for _, bound := range tr.a.typeParamInterfaceScopes[i] {
				if bound == nil {
					continue
				}
				if _, ok := bound.Methods[fieldExpr.Field]; ok {
					iface = bound
					break
				}
			}
		}
	}
	if iface == nil {
		return nil
	}
	method, ok := iface.Methods[fieldExpr.Field]
	if !ok || method == nil || method.Signature == nil {
		return nil
	}
	return method.Signature.Return
}

// abstractMethodCanonKey builds a stable canonicalization key for a deterministic protocol-method
// call that does not resolve to a concrete decl. It normalizes BOTH surface forms to the same key:
//
//   - method form  `recv.m(a, b)`   → participants = [recv, a, b]
//   - static form  `T.m(recv, a, b)` (the bound-type-param rewrite) → participants = [recv, a, b]
//
// The key is `method-name | term(p0) | term(p1) | …`, so `self.count()` (with `self`↦c) and the
// caller's `c.count()` produce the same key and share one SMT symbol. Returns false when the call is
// not a recognizable method/static-method form or any participant is outside the integer fragment.
func (tr *smtTranslator) abstractMethodCanonKey(call *ast.CallExpr, argEnv map[string]string) (string, bool) {
	if tr == nil || call == nil {
		return "", false
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field == "" {
		return "", false
	}
	args := proofCallArgs(call)
	var participants []ast.Expr
	// Static type-path form `T.m(...)`: the object is a bare type-param / type name, and the receiver
	// is the first argument. Detect it by the object being an Ident bound to a type-param interface.
	if obj, ok := fieldExpr.Object.(*ast.Ident); ok && tr.a != nil {
		if _, isTypeParam := tr.a.lookupTypeParamInterface(obj.Name); isTypeParam {
			participants = args
		}
	}
	if participants == nil {
		// Method form `recv.m(args...)`: receiver is the object, followed by the explicit args.
		participants = append(participants, fieldExpr.Object)
		participants = append(participants, args...)
	}
	var b strings.Builder
	b.WriteString("absmethod|")
	b.WriteString(fieldExpr.Field)
	b.WriteByte('|')
	for _, p := range participants {
		t, ok := tr.callCanonArgKeyEnv(p, argEnv)
		if !ok {
			return "", false
		}
		b.WriteString(t)
		b.WriteByte('|')
	}
	return b.String(), true
}

func (tr *smtTranslator) callResultBoolTerm(call *ast.CallExpr) (string, bool) {
	return tr.callResultBoolTermEnv(call, nil)
}

func (tr *smtTranslator) callResultBoolTermEnv(call *ast.CallExpr, argEnv map[string]string) (string, bool) {
	if tr == nil || tr.a == nil || call == nil {
		return "", false
	}
	// SURFACE dict membership `d.contains(k)` (resolved to `arena_dict_contains`) maps to the dict key
	// array, so it can serve as the in-domain GUARD for a value-lookup goal.
	if sel, ok := tr.dictMethodCall(call, "keys", argEnv); ok {
		return sel, true
	}
	// A PROTOCOL-METHOD bool call (`x.le(y)` where le is an interface method, docs/85 P3) is modeled as
	// an UNINTERPRETED predicate: a stable Bool symbol keyed by (Interface.method, argument idents). The
	// same `x.le(y)` therefore denotes one symbol everywhere — so an injected protocol-law hypothesis
	// (`(=> (and le_xy le_yz) le_xz)`) shares symbols with the goal and transports the fact. Modeling a
	// total method as an arbitrary predicate is sound: it constrains nothing beyond what the laws assert.
	if sym, ok := tr.interfaceMethodBoolSym(call, argEnv); ok {
		return sym, true
	}
	resultType := tr.a.exprTypes[call]
	decl, direct := tr.a.resolveDirectCallFuncDecl(call)
	if resultType == nil && direct && decl != nil {
		if sym, ok := tr.a.symbolForFuncDecl(decl); ok {
			if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
				resultType = fnType.Return
			}
		}
	}
	if !IsBoolType(resultType) {
		return "", false
	}
	args := proofCallArgs(call)
	var canonKey string
	if direct && decl != nil {
		if key, ok := tr.callCanonKeyEnv(decl, args, tr.canonKeyEnv(decl, argEnv)); ok {
			if sym, seen := tr.callCanon[key]; seen {
				return sym, true
			}
			canonKey = key
		}
	}
	ret := tr.freshBoolAux()
	if canonKey != "" {
		tr.callCanon[canonKey] = ret
	}
	if !direct || decl == nil {
		return ret, true
	}
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	env, ok := tr.a.smtEnvForCallSubstEnv(tr, subst, argEnv)
	if !ok {
		return ret, true
	}
	env["result"] = ret
	guard := "true"
	for _, req := range decl.Requires {
		if req == nil {
			continue
		}
		h, ok := tr.boolTerm(req, env)
		if !ok {
			return ret, true
		}
		if guard == "true" {
			guard = h
		} else {
			guard = "(and " + guard + " " + h + ")"
		}
	}
	tr.emitDefiningEquationWithEnv(call, decl, ret, subst, canonKey, guard, false, env)
	return ret, true
}

// callCanonKey builds the canonicalization key for a direct call: the callee's identity plus the
// canonical SMT term of each argument. Returns ok=false if any argument is outside the integer term
// fragment (so no stable key exists and the caller mints a fresh aux instead).
func (tr *smtTranslator) callCanonKey(decl *ast.FuncDecl, args []ast.Expr) (string, bool) {
	return tr.callCanonKeyEnv(decl, args, nil)
}

// canonKeyEnv chooses the env used to CANONICALIZE a call (not to lower its body). For a recursive
// self-call the key MUST be syntactic (nil env): the function's own defining-equation body is
// translated under a substitution env, and we need the body's recursive sub-call to canonicalize to
// the SAME symbol as the outer call so the equation becomes the self-referential fixpoint
// `aux == body(aux)` that, together with the `ensure` IH, lets z3 perform the induction. Threading the
// substitution env here would give the sub-call a distinct key, mint a fresh symbol every level, and
// both lose the fixpoint AND defeat the bounded-unroll guard (unbounded recursion). Non-recursive
// calls keep the env so struct/bool/forwarded args lower precisely.
func (tr *smtTranslator) canonKeyEnv(decl *ast.FuncDecl, argEnv map[string]string) map[string]string {
	if tr != nil && tr.a != nil && tr.a.isRecursiveProofCall(decl) {
		return nil
	}
	return argEnv
}

// interfaceMethodBoolSym models a bool-returning PROTOCOL-method call over a type-parameter receiver
// (`x.le(y)` inside `def sort[T: Ord]`) as an uninterpreted predicate: a stable Bool symbol keyed by the
// protocol+method and the canonical names of its receiver/argument idents. It applies ONLY when every
// operand is a plain identifier whose type is a protocol-bounded type parameter — exactly the shape the
// law-fact injection and the generic goal share — so it never perturbs concrete-type proofs. Returns
// ok=false otherwise (the caller then takes the normal path).
func (tr *smtTranslator) interfaceMethodBoolSym(call *ast.CallExpr, env map[string]string) (string, bool) {
	if tr == nil || tr.a == nil || call == nil {
		return "", false
	}
	sel, ok := call.Func.(*ast.FieldExpr)
	if !ok || sel == nil {
		return "", false
	}
	// Two shapes reach here: the UFCS form `x.le(y)` (receiver = sel.Object, args = call.Args) and the
	// qualified static form `T.le(x, y)` — the analyzer rewrites a bound type-param method call to this,
	// so sel.Object is the type-param name `T` and ALL value operands are call.Args. Detect the latter by
	// sel.Object naming the protocol-bounded type parameter itself.
	var ifaceName string
	var operands []ast.Expr
	if ifn, ok := tr.a.qualifiedTypeParamMethodInterface(sel.Object, sel.Field); ok {
		ifaceName = ifn
		operands = call.Args
	} else if ifn, ok := tr.a.protocolMethodReceiverInterface(sel.Object, sel.Field); ok {
		ifaceName = ifn
		operands = append([]ast.Expr{sel.Object}, call.Args...)
	} else {
		return "", false
	}
	parts := make([]string, 0, len(operands))
	for _, op := range operands {
		name, ok := lawOperandIdentName(op, env)
		if !ok {
			return "", false
		}
		parts = append(parts, name)
	}
	sym := smtBoolVar("__iface_" + sanitizeSMTFragment(ifaceName) + "_" + sel.Field + "_" + strings.Join(parts, "_"))
	tr.boolDecls[sym] = true
	return sym, true
}

// lawOperandIdentName returns a stable canonical name for an operand that is a plain identifier,
// resolving through the quantifier/substitution env so a bound `a` and the param it maps to share a key.
func lawOperandIdentName(expr ast.Expr, env map[string]string) (string, bool) {
	id, ok := stripOptimizationParens(expr).(*ast.Ident)
	if !ok || id == nil {
		return "", false
	}
	if env != nil {
		if bound, ok := env[id.Name]; ok {
			// A `T`-typed operand resolves through the env to a DATATYPE-projection token (`__dt__a`), not
			// an SMT term. The law-hypothesis side instantiates the law over the bare parameter idents
			// (env-free ⇒ raw name `a`). Both denote the SAME place, so normalize the dt-token to its bare
			// projection — otherwise the goal symbol (`le___dt__a_a`) and the law symbol (`le_a_a`) diverge
			// and the injected law fact never transports to the goal. This is the chokepoint that connects
			// a `return a` ensure (`result` ↦ `a`) to the law instantiated at `a`.
			return strings.TrimPrefix(bound, dtTokenPrefix), true
		}
	}
	return id.Name, true
}

// protocolMethodReceiverInterface reports the protocol name whose method `method` applies to `recv` when
// recv is an identifier typed as a protocol-bounded type parameter. This is the gate that restricts the
// uninterpreted-predicate model to the generic protocol-law setting.
func (a *Analyzer) protocolMethodReceiverInterface(recv ast.Expr, method string) (string, bool) {
	id, ok := stripOptimizationParens(recv).(*ast.Ident)
	if !ok || id == nil || a.currentScope == nil {
		return "", false
	}
	sym, ok := a.currentScope.Lookup(id.Name)
	if !ok || sym == nil {
		return "", false
	}
	tp, ok := sym.Type.(*TypeParamType)
	if !ok {
		return "", false
	}
	iface, ok := a.lookupTypeParamInterface(tp.Name)
	if !ok || iface == nil {
		return "", false
	}
	if _, ok := iface.Methods[method]; !ok {
		return "", false
	}
	return iface.Name, true
}

// qualifiedTypeParamMethodInterface reports the protocol name for a qualified static method call
// `T.method(...)` where `T` is a protocol-bounded type parameter in scope and the protocol declares
// `method`. This is the canonical lowered shape of a generic `value.method(...)` UFCS call.
func (a *Analyzer) qualifiedTypeParamMethodInterface(owner ast.Expr, method string) (string, bool) {
	id, ok := stripOptimizationParens(owner).(*ast.Ident)
	if !ok || id == nil {
		return "", false
	}
	if _, ok := a.lookupTypeParam(id.Name); !ok {
		return "", false
	}
	iface, ok := a.lookupTypeParamInterface(id.Name)
	if !ok || iface == nil {
		return "", false
	}
	if _, ok := iface.Methods[method]; !ok {
		return "", false
	}
	return iface.Name, true
}

// sanitizeSMTFragment maps a (possibly qualified) name to an SMT-symbol-safe fragment.
func sanitizeSMTFragment(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func (tr *smtTranslator) callCanonKeyEnv(decl *ast.FuncDecl, args []ast.Expr, env map[string]string) (string, bool) {
	var b strings.Builder
	fmt.Fprintf(&b, "%p|", decl)
	for _, arg := range args {
		if arg == nil {
			return "", false
		}
		t, ok := tr.callCanonArgKeyEnv(arg, env)
		if !ok {
			return "", false
		}
		b.WriteString(t)
		b.WriteByte('|')
	}
	return b.String(), true
}

func (tr *smtTranslator) callCanonArgKey(arg ast.Expr) (string, bool) {
	return tr.callCanonArgKeyEnv(arg, nil)
}

func (tr *smtTranslator) callCanonArgKeyEnv(arg ast.Expr, env map[string]string) (string, bool) {
	if arg == nil {
		return "", false
	}
	if IsBoolType(tr.a.exprTypes[arg]) {
		return tr.boolTerm(arg, env)
	}
	// A bound type-param-typed receiver (`c: C`) has no scalar term; key it by its env binding (a
	// dtToken, when substituted) or its projection token. This is what makes an abstract method call
	// keyed by its receiver canonicalize identically on the obligation and caller-fact sides (docs P1).
	if _, ok := stripRefForBounds(tr.a.exprTypes[arg]).(*TypeParamType); ok {
		if id, ok := arg.(*ast.Ident); ok && env != nil {
			if bound, ok := env[id.Name]; ok {
				return bound, true
			}
		}
		return dtToken(smtProjectionName(arg)), true
	}
	if tr.a.smtEquationStructType(tr.a.exprTypes[arg]) {
		st, _ := stripRefForBounds(tr.a.exprTypes[arg]).(*StructType)
		var parts []string
		for _, fieldName := range sortedStructFieldNames(st) {
			field := st.Fields[fieldName]
			fieldExpr := &ast.FieldExpr{Object: arg, Field: fieldName}
			ft := stripRefForBounds(field.Type)
			if IsBoolType(ft) {
				t, ok := tr.boolTerm(fieldExpr, env)
				if !ok {
					return "", false
				}
				parts = append(parts, fieldName+"="+t)
				continue
			}
			t, ok := tr.termEnv(fieldExpr, env)
			if !ok {
				return "", false
			}
			parts = append(parts, fieldName+"="+t)
		}
		return "struct{" + strings.Join(parts, ",") + "}", true
	}
	return tr.termEnv(arg, env)
}

// emitDefiningEquation asserts `ret == body[params:=args]` for a pure, total function, with the body's
// own recursive calls bounded-unrolled (callEqMaxUnroll). It is a NO-OP (sound decline) whenever the
// purity/termination gate fails, the body is not a single pure return, or the body cannot be lowered
// to an integer term.
func (tr *smtTranslator) emitDefiningEquation(call *ast.CallExpr, decl *ast.FuncDecl, ret string, subst map[string]ast.Expr, canonKey, guard string, recursiveCallDomainProven bool) {
	tr.emitDefiningEquationWithEnv(call, decl, ret, subst, canonKey, guard, recursiveCallDomainProven, nil)
}

func (tr *smtTranslator) emitDefiningEquationWithEnv(call *ast.CallExpr, decl *ast.FuncDecl, ret string, subst map[string]ast.Expr, canonKey, guard string, recursiveCallDomainProven bool, envOverride map[string]string) {
	if tr == nil || decl == nil {
		return
	}
	if tr.a.isRecursiveProofCall(decl) && !recursiveCallDomainProven {
		return
	}
	if canonKey == "" {
		var ok bool
		canonKey, ok = tr.callCanonKey(decl, proofCallArgs(call))
		if !ok {
			return
		}
	}
	if tr.callEqInFlight[canonKey] >= tr.callEqMaxUnroll {
		return // bounded unroll reached: leave the symbol opaque (still sound)
	}
	// Per-callee depth cap. canonKey embeds the arguments, so genuine recursion f(n)->f(n-1)->... mints a
	// fresh key at every level and callEqInFlight never trips. This per-decl counter bounds how many
	// nested instantiations of the SAME function may be unfolded, so the unfold terminates. Leaving the
	// deeper self-call opaque is sound (a missing defining equation only removes a hypothesis).
	if tr.callEqDeclDepth[decl] >= tr.callEqMaxDeclDepth {
		return
	}
	if !tr.callEquationEligible(decl) {
		return
	}
	body, ok := pureReturnBody(decl)
	if !ok {
		return
	}
	env := envOverride
	if env == nil {
		var ok bool
		env, ok = tr.a.smtEnvForSubst(tr, subst)
		if !ok {
			return
		}
	}
	tr.callEqInFlight[canonKey]++
	defer func() { tr.callEqInFlight[canonKey]-- }()
	tr.callEqDeclDepth[decl]++
	defer func() { tr.callEqDeclDepth[decl]-- }()
	bodyTerm, ok := tr.termEnv(body, env)
	if !ok {
		bodyTerm, ok = tr.boolTerm(body, env)
		if !ok {
			return
		}
	}
	eq := "(= " + ret + " " + bodyTerm + ")"
	if guard != "" && guard != "true" {
		eq = "(=> " + guard + " " + eq + ")"
	}
	tr.auxDecls = append(tr.auxDecls, "(assert "+eq+")\n")
	if tr.a.isRecursiveProofCall(decl) {
		if cert, ok := tr.a.recursiveEquationCertificate(decl); ok {
			tr.a.recordProof(call.Pos(), "recursive equation of "+decl.Name+" ("+cert.label()+")", "definition", ProofProvenContract)
		} else {
			tr.a.recordProof(call.Pos(), "recursive equation of "+decl.Name+" (verified termination)", "definition", ProofProvenContract)
		}
	} else {
		tr.a.recordProof(call.Pos(), "defining equation of "+decl.Name, "definition", ProofProvenContract)
	}
}

func (a *Analyzer) isRecursiveProofCall(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil || a.currentFuncDecl == nil {
		return false
	}
	if decl == a.currentFuncDecl {
		return true
	}
	for _, edge := range a.collectRecursiveSCCEdges(a.currentFuncDecl) {
		if edge.Caller == decl || edge.Callee == decl {
			return true
		}
	}
	return false
}

func (a *Analyzer) recursiveCallHasVerifiedDecrease(callee *ast.FuncDecl, call *ast.CallExpr) bool {
	if a == nil || a.currentFuncDecl == nil || callee == nil || call == nil {
		return false
	}
	caller := a.currentFuncDecl
	_, ok := a.recursiveCallCertificate(caller, callee, call)
	return ok
}

func (a *Analyzer) recursiveCallRequiresProven(callee *ast.FuncDecl, call *ast.CallExpr) bool {
	if a == nil || callee == nil || call == nil {
		return false
	}
	if len(callee.Requires) == 0 {
		return true
	}
	subst := map[string]ast.Expr{}
	args := proofCallArgs(call)
	for i, param := range callee.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	for _, req := range callee.Requires {
		if req == nil {
			continue
		}
		switch a.proveRequiresClause(req, subst) {
		case requiresProven:
			continue
		case requiresRefuted:
			return false
		default:
			proven, _ := a.trySMTProveRequires(req, subst)
			if !proven {
				return false
			}
		}
	}
	return true
}

// callEquationEligible reports whether a function's defining equation may be soundly assumed:
//   - PURE: a single `return <pure-expr>` body (after leading contracts) over integer params, with an
//     integer return. Such a body has no mutation, loops, or effects, so `f(args)` equals the body
//     evaluated at the arguments — a true mathematical equation.
//   - TOTAL: if the function makes ANY direct self-recursive call it MUST carry a VERIFIED `decreases`
//     measure. A non-terminating recursive function does not denote a value, and assuming its
//     "equation" can be inconsistent (`f(n) == 1 + f(n)`), which would prove anything. The termination
//     gate makes the recursion well-founded, so the equation has a unique fixed point and is sound.
//
// A non-recursive pure function needs no measure (it is trivially total). Eligibility is cached via the
// analyzer's defining-equation cache to avoid re-deriving purity per call.
func (tr *smtTranslator) callEquationEligible(decl *ast.FuncDecl) bool {
	return tr.a.functionDefiningEquationEligible(decl)
}

// factPreamble emits the declarations for every free variable the translation touched, plus the
// integer flow facts known about them (range bounds, written-constant equalities) as hypotheses. The
// facts are a SOUND SUBSET of what holds, which is why only an `unsat` result concludes a proof.
func (tr *smtTranslator) factPreamble() string {
	names := make([]string, 0, len(tr.decls))
	for name := range tr.decls {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic query text (stable across runs / cache-friendly)
	var b strings.Builder
	if tr.a != nil && tr.a.currentClosedWorldTheory != "" {
		b.WriteString("; closed-world theory: " + tr.a.currentClosedWorldTheory + "\n")
	}
	for _, name := range names {
		b.WriteString("(declare-const " + smtVar(name) + " Int)\n")
	}
	for _, name := range names {
		v := smtVar(name)
		if r, ok := tr.a.lookupRangeFact(name); ok {
			if r.loKnown {
				b.WriteString("(assert (>= " + v + " " + smtInt(r.lo) + "))\n")
			}
			if r.hiKnown {
				b.WriteString("(assert (<= " + v + " " + smtInt(r.hi) + "))\n")
			}
		}
		if c, ok := tr.a.writtenConstInt(name); ok {
			b.WriteString("(assert (= " + v + " " + smtInt(c) + "))\n")
		}
		if tr.nonNegDecls[name] {
			b.WriteString("(assert (>= " + v + " 0))\n")
		}
		if bits, ok := tr.unsignedBits[name]; ok && bits > 0 && bits <= 64 {
			// True type bound: an unsigned value lives in [0, 2^width). Pinning the upper bound makes
			// the wraparound model exact for in-range computations (so divisibility through
			// `value - value%alignment` proves) and still sound (it can only remove counterexamples).
			b.WriteString("(assert (< " + v + " " + smtPow2(bits) + "))\n")
		}
		if bits, ok := tr.signedBits[name]; ok && bits > 0 && bits <= 64 {
			// True type bound: a signed value lives in [-2^(width-1), 2^(width-1)).
			bias := smtPow2(bits - 1)
			b.WriteString("(assert (>= " + v + " (- " + bias + ")))\n")
			b.WriteString("(assert (< " + v + " " + bias + "))\n")
		}
	}
	// Array declarations (docs/90 brick 90-5): each integer-element array/darray modeled as an SMT
	// (Array Int Int). Deterministic order.
	arrays := make([]string, 0, len(tr.arrayDecls))
	for name := range tr.arrayDecls {
		arrays = append(arrays, name)
	}
	sort.Strings(arrays)
	for _, name := range arrays {
		b.WriteString("(declare-const " + smtVar(name) + " (Array Int Int))\n")
	}
	// Set declarations: an `(Array <KSort> Bool)` membership function. Deterministic order.
	sets := make([]string, 0, len(tr.setDecls))
	for sym := range tr.setDecls {
		sets = append(sets, sym)
	}
	sort.Strings(sets)
	for _, sym := range sets {
		b.WriteString("(declare-const " + sym + " (Array " + tr.setDecls[sym] + " Bool))\n")
	}
	// Dict declarations: a `<sym>_keys` membership array plus a `<sym>_vals` value array, sharing the
	// key sort. The value array is fixed to Int (only integer-valued dicts are modeled). Deterministic.
	dicts := make([]string, 0, len(tr.dictDecls))
	for sym := range tr.dictDecls {
		dicts = append(dicts, sym)
	}
	sort.Strings(dicts)
	for _, sym := range dicts {
		ks := tr.dictDecls[sym]
		b.WriteString("(declare-const " + sym + "_keys (Array " + ks + " Bool))\n")
		b.WriteString("(declare-const " + sym + "_vals (Array " + ks + " Int))\n")
	}
	// Free bool consts (bool params/locals appearing in a boolean postcondition). Deterministic order.
	bools := make([]string, 0, len(tr.boolDecls))
	for name := range tr.boolDecls {
		bools = append(bools, name)
	}
	sort.Strings(bools)
	for _, name := range bools {
		b.WriteString("(declare-const " + name + " Bool)\n")
	}
	// Length symbols (`arr.count`/`.len`), each a non-negative Int.
	lens := make([]string, 0, len(tr.lenDecls))
	for sym := range tr.lenDecls {
		lens = append(lens, sym)
	}
	sort.Strings(lens)
	for _, sym := range lens {
		b.WriteString("(declare-const " + sym + " Int)\n")
		b.WriteString("(assert (>= " + sym + " 0))\n")
		// A container length is a `usize`, so it is bounded above by 2^64 by construction. Asserting this
		// (always-true) upper bound lets an UNSIGNED loop guard `i < arr.count` — lowered as a bitvector
		// `bvult` over `(int2bv 64 …)` — bridge back to Int reasoning: without `arr.count < 2^64` the
		// `int2bv` of an unbounded length could wrap, so z3 cannot conclude the Int relation the
		// termination/preservation goal needs. Sound: it only ever asserts a fact the type guarantees.
		b.WriteString("(assert (< " + sym + " 18446744073709551616))\n")
	}
	// Fresh under-constrained symbols (modulo/div with a not-provably-nonzero divisor). Emitted in
	// mint order — deterministic because the counter is monotonic over a single translation.
	for _, line := range tr.auxDecls {
		b.WriteString(line)
	}
	return b.String()
}

// declaredSMTVars returns the SMT symbols for every free variable the translation declared, so the
// solver can be asked for their values on a Sat (counterexample) result.
func (tr *smtTranslator) declaredSMTVars() []string {
	out := make([]string, 0, len(tr.decls))
	for name := range tr.decls {
		out = append(out, smtVar(name))
	}
	sort.Strings(out)
	out = append(out, tr.auxVars...)
	labels := make([]string, 0, len(tr.ceExprs))
	for label := range tr.ceExprs {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		out = append(out, smtCounterexampleExprVar(label))
	}
	return out
}

func (tr *smtTranslator) counterexampleExprDefinitions() string {
	if len(tr.ceExprs) == 0 {
		return ""
	}
	labels := make([]string, 0, len(tr.ceExprs))
	for label := range tr.ceExprs {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	var b strings.Builder
	for _, label := range labels {
		b.WriteString("(define-fun ")
		b.WriteString(smtCounterexampleExprVar(label))
		b.WriteString(" () Int ")
		b.WriteString(tr.ceExprs[label])
		b.WriteString(")\n")
	}
	return b.String()
}

// counterexample renders a model (SMT-var → value) as a readable "a=5, b=20" hint using the original
// Elisa identifier names. Empty when no model is available.
func (tr *smtTranslator) counterexample(model map[string]string) string {
	if len(model) == 0 {
		return ""
	}
	names := make([]string, 0, len(tr.decls))
	for name := range tr.decls {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		if v, ok := model[smtVar(name)]; ok {
			parts = append(parts, name+"="+v)
		}
	}
	labels := make([]string, 0, len(tr.ceExprs))
	for label := range tr.ceExprs {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		if v, ok := model[smtCounterexampleExprVar(label)]; ok {
			parts = append(parts, label+"="+v)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

func smtCounterexampleExprVar(label string) string {
	var b strings.Builder
	b.WriteString("ce_")
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func smtCompare(op lexer.TokenKind, l, r string) string {
	switch op {
	case lexer.TOKEN_GT:
		return "(> " + l + " " + r + ")"
	case lexer.TOKEN_GTEQ:
		return "(>= " + l + " " + r + ")"
	case lexer.TOKEN_LT:
		return "(< " + l + " " + r + ")"
	case lexer.TOKEN_LTEQ:
		return "(<= " + l + " " + r + ")"
	case lexer.TOKEN_EQEQ:
		return "(= " + l + " " + r + ")"
	case lexer.TOKEN_BANGEQ:
		return "(distinct " + l + " " + r + ")"
	}
	return "false"
}

// smtInt renders an integer literal, parenthesizing negatives as SMT-LIB requires `(- n)`.
func smtInt(v int64) string {
	if v < 0 {
		return "(- " + strconv.FormatInt(-v, 10) + ")"
	}
	return strconv.FormatInt(v, 10)
}

// smtAbs renders integer absolute value. The caller is responsible for keeping terms in the integer
// fragment; SMT-LIB Int arithmetic is total.
func smtAbs(term string) string {
	return "(ite (< " + term + " 0) (- " + term + ") " + term + ")"
}

// smtTruncDiv renders Elisa/C-style integer division, which truncates toward zero. SMT-LIB `div` is
// Euclidean, so for negative operands we divide absolute values and restore the quotient sign.
func smtTruncDiv(left, right string) string {
	absLeft := smtAbs(left)
	absRight := smtAbs(right)
	quot := "(div " + absLeft + " " + absRight + ")"
	sameSign := "(= (< " + left + " 0) (< " + right + " 0))"
	return "(ite " + sameSign + " " + quot + " (- " + quot + "))"
}

// smtVar maps an Elisa identifier to a collision-free SMT symbol.
func smtVar(name string) string {
	return "v_" + name
}

// smtBoolVar names an SMT Bool const for a free bool identifier — a distinct namespace from the
// integer `v_` consts so an int and a bool of the same Elisa name never collide.
func smtBoolVar(name string) string {
	return "b_" + name
}

// structLitFieldMap maps each field name of a struct literal to its construction argument, handling
// both named args and positional args (resolved against the struct declaration's field order). Returns
// nil if the field order cannot be resolved.
func (a *Analyzer) structLitFieldMap(sl *ast.StructLitExpr) map[string]ast.Expr {
	if sl == nil {
		return nil
	}
	args := sl.LoweredArgs()
	var ordered []string
	if st, ok := stripRefForBounds(a.exprTypes[sl]).(*StructType); ok && st != nil && st.Decl != nil {
		for _, fd := range st.Decl.Fields {
			ordered = append(ordered, fd.Name)
		}
	}
	out := map[string]ast.Expr{}
	for i, arg := range args {
		name := sl.ArgName(i)
		if name == "" {
			if i >= len(ordered) {
				continue
			}
			name = ordered[i]
		}
		if name != "" && arg != nil {
			out[name] = arg
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// nullComparePointer returns the non-null operand of a `p == null` / `p != null` comparison, or nil if
// neither operand is the null literal.
func nullComparePointer(n *ast.BinaryExpr) ast.Expr {
	if _, ok := stripOptimizationParens(n.Right).(*ast.NullLit); ok {
		return n.Left
	}
	if _, ok := stripOptimizationParens(n.Left).(*ast.NullLit); ok {
		return n.Right
	}
	return nil
}

func smtProjectionName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.FieldExpr:
		return smtProjectionName(n.Object) + "__field__" + n.Field
	case *ast.ParenExpr:
		return smtProjectionName(n.Inner)
	default:
		return "expr__" + smtSanitize(fmt.Sprintf("%T_%s", expr, expr.Pos().String()))
	}
}

func sortedStructFieldNames(st *StructType) []string {
	if st == nil {
		return nil
	}
	names := make([]string, 0, len(st.Fields))
	for name := range st.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func smtSanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
