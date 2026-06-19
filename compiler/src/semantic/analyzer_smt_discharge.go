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
		"SMT tier: %d obligations, %d proven, %d declined; solver %.1fms (spawn %.1fms, slowest %.1fms)",
		p.Attempts, p.Proven, p.Declined,
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
	obligation, ok := tr.boolTerm(body, env)
	if !ok {
		return false
	}
	// Assume the enclosing function's preconditions (docs/90 brick 90-6). A `requires forall k:
	// 0<=k<n implies xs[k] >= 0` becomes a hypothesis, so `return xs[0] is NonNeg` discharges by
	// quantifier instantiation. Contract-sound: the callee may assume its preconditions (callers must
	// establish them), and an SMT-proven VALUE fact never drives bounds-check elision, so a violated
	// precondition is garbage-in-garbage-out, not memory unsafety. Translated with the SAME translator
	// so param/array symbols unify with the obligation. (factPreamble is built AFTER, once all decls
	// are collected.)
	hyps := a.smtRequiresHypotheses(tr)
	// docs/85 gap #2: assert the defining equality of every immutable integer local in
	// scope, so the prover reasons THROUGH locals (`rem = value % alignment`) rather than
	// treating them as free variables. Must run before factPreamble so the locals and the
	// variables of their defining expressions are declared.
	localHyps := a.smtImmutableLocalHypotheses(tr)
	assertHyps := a.smtAssertHypotheses(tr)
	flowHyps := a.smtFlowFactHypotheses(tr)
	query := tr.factPreamble() + hyps + localHyps + assertHyps + flowHyps + "(assert (not " + obligation + "))\n"
	a.smtStats.Attempts++
	res, _ := solver.Check(query)
	if res == smt.Unsat {
		a.smtStats.Proven++
		a.smtStats.SolverProven++
		return true
	}
	a.smtStats.Declined++
	return false
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
	obligation, ok := tr.boolTerm(clause, env)
	if !ok {
		return false, ""
	}
	// Assume the ENCLOSING (caller) function's own preconditions as hypotheses (docs/90 brick 90-13).
	// This is the dual of brick 90-6 (which lets a callee assume its requires in its body): here a
	// caller that itself carries `requires forall k: 0<=k<n implies data[k] >= 0` can discharge a
	// callee's identical-or-weaker quantified array precondition, because both clauses translate
	// against the SAME array symbol (the caller arg `data` and the caller requires both resolve to
	// smtVar("data")). Contract-sound: the caller's callers must establish the caller's requires, and
	// an SMT-proven precondition never drives bounds-check elision. A caller clause outside the
	// fragment is silently skipped (fewer assumptions is conservative).
	hyps := a.smtRequiresHypotheses(tr)
	// docs/85 gap #2: assert the defining equality of every immutable integer local in
	// scope, so the prover reasons THROUGH locals (`rem = value % alignment`) rather than
	// treating them as free variables. Must run before factPreamble so the locals and the
	// variables of their defining expressions are declared.
	localHyps := a.smtImmutableLocalHypotheses(tr)
	assertHyps := a.smtAssertHypotheses(tr)
	flowHyps := a.smtFlowFactHypotheses(tr)
	query := tr.factPreamble() + hyps + localHyps + assertHyps + flowHyps + "(assert (not " + obligation + "))\n"
	a.smtStats.Attempts++
	res, model, _ := solver.CheckValues(query, tr.declaredSMTVars())
	if res == smt.Unsat {
		a.smtStats.Proven++
		a.smtStats.SolverProven++
		return true, ""
	}
	a.smtStats.Declined++
	// On sat, the model is an input permitted by the caller's known facts that violates the
	// precondition — a concrete witness for the diagnostic (a hint, since our facts are a subset).
	return false, tr.counterexample(model)
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
	calleeEnv, ok := a.smtEnvForSubst(tr, subst)
	if !ok {
		return false
	}
	calleeEnv["result"] = retTerm
	var calleeHyps strings.Builder
	for _, ensure := range decl.EnsureValues {
		if ensure == nil {
			continue
		}
		if h, ok := tr.boolTerm(ensure, calleeEnv); ok {
			calleeHyps.WriteString("(assert " + h + ")\n")
		}
	}
	if calleeHyps.Len() == 0 {
		return false
	}
	hyps := a.smtRequiresHypotheses(tr)
	localHyps := a.smtImmutableLocalHypotheses(tr)
	assertHyps := a.smtAssertHypotheses(tr)
	flowHyps := a.smtFlowFactHypotheses(tr)
	query := tr.factPreamble() + hyps + localHyps + assertHyps + flowHyps + calleeHyps.String() + "(assert (not " + obligation + "))\n"
	a.smtStats.Attempts++
	res, _ := solver.Check(query)
	if res == smt.Unsat {
		a.smtStats.Proven++
		a.smtStats.SolverProven++
		return true
	}
	a.smtStats.Declined++
	return false
}

func (a *Analyzer) smtEnvForSubst(tr *smtTranslator, subst map[string]ast.Expr) (map[string]string, bool) {
	env := map[string]string{}
	for name, argExpr := range subst {
		if lit, ok := argExpr.(*ast.BoolLit); ok {
			if lit.Value {
				env[name] = "true"
			} else {
				env[name] = "false"
			}
			continue
		}
		if IsBoolType(a.exprTypes[argExpr]) {
			bterm, ok := tr.boolTerm(argExpr, nil)
			if !ok {
				return nil, false
			}
			env[name] = bterm
			continue
		}
		if tr.isArrayLike(a.exprTypes[argExpr]) {
			arr, ok := tr.arrayTermEnv(argExpr, nil)
			if !ok {
				return nil, false
			}
			env[name] = arr
			continue
		}
		term, ok := tr.term(argExpr)
		if !ok {
			return nil, false
		}
		env[name] = term
	}
	return env, true
}

// smtRequiresHypotheses translates the enclosing function's `requires` clauses into SMT assertions
// (non-negated — they are assumed), using the given translator so free variables and arrays share the
// obligation's symbols. A clause outside the fragment is silently skipped (sound: fewer assumptions is
// conservative). Returns the concatenated `(assert …)` lines.
func (a *Analyzer) smtRequiresHypotheses(tr *smtTranslator) string {
	if a.currentFuncDecl == nil {
		return ""
	}
	var b strings.Builder
	for _, req := range a.currentFuncDecl.Requires {
		if req == nil {
			continue
		}
		if h, ok := tr.boolTerm(req, nil); ok {
			b.WriteString("(assert " + h + ")\n")
		}
	}
	return b.String()
}

// smtFlowFactHypotheses asserts the scope's flow range-facts — branch-derived bounds on
// immutable variables (`if alignment == 0: return` ⟹ `alignment >= 1` afterwards; `if n < cap`
// ⟹ `n <= cap-1` in the then-branch) — as SMT hypotheses. These are already soundly
// flow-scoped and immutable-only (the linear prover uses them at the same program point), so
// surfacing them to the SMT tier lets branchy and loop-exit reasoning discharge (docs/85 gap #3:
// loop-carried/flow facts). A fact with no known bound contributes nothing.
func (a *Analyzer) smtFlowFactHypotheses(tr *smtTranslator) string {
	if a == nil || a.currentScope == nil || tr == nil {
		return ""
	}
	var b strings.Builder
	seen := map[string]bool{}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for name, r := range sc.rangeFacts {
			if seen[name] {
				continue // a closer scope's fact shadows an outer one
			}
			seen[name] = true
			if !r.loKnown && !r.hiKnown {
				continue
			}
			v := smtVar(name)
			tr.decls[name] = true
			if r.loKnown {
				b.WriteString("(assert (>= " + v + " " + smtInt(r.lo) + "))\n")
			}
			if r.hiKnown {
				b.WriteString("(assert (<= " + v + " " + smtInt(r.hi) + "))\n")
			}
		}
	}
	return b.String()
}

type smtFact struct {
	Expr ast.Expr
	Deps map[string]bool
}

// smtAssertHypotheses translates flow-local proof facts into SMT assumptions. A fact may come from a
// branch guard, a proven invariant/assertion, or an exact assignment equality. Facts carry dependency
// roots and are invalidated on mutation of any root; calls still clear them conservatively.
func (a *Analyzer) smtAssertHypotheses(tr *smtTranslator) string {
	if a == nil || a.currentScope == nil || tr == nil {
		return ""
	}
	var b strings.Builder
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for _, fact := range sc.smtAssertFacts {
			if fact.Expr == nil {
				continue
			}
			if h, ok := tr.boolTerm(fact.Expr, nil); ok {
				b.WriteString("(assert " + h + ")\n")
			}
		}
	}
	return b.String()
}

func (a *Analyzer) recordSMTAssertFact(expr ast.Expr) {
	if a == nil || a.currentScope == nil || expr == nil {
		return
	}
	if bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr); ok && bin.Op == lexer.TOKEN_AND {
		a.recordSMTAssertFact(bin.Left)
		a.recordSMTAssertFact(bin.Right)
		return
	}
	a.currentScope.smtAssertFacts = append(a.currentScope.smtAssertFacts, smtFact{Expr: expr, Deps: smtFactDeps(expr)})
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
	name, ok := rootIdentName(target)
	if !ok || name == "" {
		a.clearSMTAssertFacts()
		return
	}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		if len(sc.smtAssertFacts) == 0 {
			continue
		}
		out := sc.smtAssertFacts[:0]
		for _, fact := range sc.smtAssertFacts {
			if fact.Deps != nil && fact.Deps[name] {
				continue
			}
			out = append(out, fact)
		}
		sc.smtAssertFacts = out
	}
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
	var b strings.Builder
	seen := map[string]bool{}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for name, sym := range sc.Symbols {
			if seen[name] || sym == nil || sym.Mutable || sym.Kind != SymbolLocal {
				continue
			}
			seen[name] = true // a closer scope's binding shadows an outer one
			vd, ok := sym.Node.(*ast.VarDeclStmt)
			if !ok || vd == nil || vd.Value == nil || sym.Type == nil || !IsNumericType(sym.Type) {
				continue
			}
			eterm, ok := tr.termEnv(vd.Value, nil)
			if !ok {
				continue
			}
			tr.decls[name] = true
			b.WriteString("(assert (= " + smtVar(name) + " " + eterm + "))\n")
			if bin, ok := stripOptimizationParens(vd.Value).(*ast.BinaryExpr); ok && bin.Op == lexer.TOKEN_PERCENT {
				rterm, rok := tr.termEnv(bin.Right, nil)
				if rok && a.provablyPositive(bin.Right) {
					b.WriteString("(assert (>= " + smtVar(name) + " 0))\n")
					b.WriteString("(assert (< " + smtVar(name) + " " + rterm + "))\n")
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
	a           *Analyzer
	decls       map[string]bool // Elisa ident -> declared as an SMT Int const
	arrayDecls  map[string]bool // Elisa ident -> declared as an SMT (Array Int Int) (docs/90 brick 90-5)
	lenDecls    map[string]bool // Elisa ident -> declared length Int (its `.count`/`.len`), asserted >= 0
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
}

// newSMTTranslator builds a translator with all collection maps initialized.
func (a *Analyzer) newSMTTranslator(paramConsts map[string]int64) *smtTranslator {
	if paramConsts == nil {
		paramConsts = map[string]int64{}
	}
	return &smtTranslator{
		a:            a,
		decls:        map[string]bool{},
		arrayDecls:   map[string]bool{},
		lenDecls:     map[string]bool{},
		nonNegDecls:  map[string]bool{},
		unsignedBits: map[string]int{},
		signedBits:   map[string]int{},
		boolDecls:    map[string]bool{},
		paramConsts:  paramConsts,
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
		if tr.isArrayLike(tr.a.exprTypes[n]) {
			tr.arrayDecls[n.Name] = true
			return smtVar(n.Name), true
		}
		return "", false
	case *ast.FieldExpr:
		// An array-valued struct field, possibly through a reference (`r.data`, `self.buf`). Model it
		// as a stable array symbol keyed by its syntactic path, so two reads of the same path share the
		// symbol (and thus its `.count`/`.len`). This is what lets `ensure result <= r.data.count;
		// return r.data.count` discharge — both sides resolve to the same length symbol.
		if tr.isArrayLike(tr.a.exprTypes[n]) {
			name := smtProjectionName(n)
			tr.arrayDecls[name] = true
			return smtVar(name), true
		}
		return "", false
	default:
		return "", false
	}
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
		return tr.callResultTerm(n)
	case *ast.StructLitExpr:
		if call, ok := tr.a.proofCallExpr(n); ok {
			return tr.callResultTerm(call)
		}
		return "", false
	case *ast.IndexExpr:
		// Array element access `arr[idx]` → `(select <arr> <idx>)` over SMT array theory (docs/90
		// brick 90-5). The element value is an Int; out-of-range indices are an arbitrary-but-total
		// value, which a quantifier's range guard constrains away.
		arr, ok := tr.arrayTermEnv(n.Object, env)
		if !ok {
			return "", false
		}
		idx, ok := tr.termEnv(n.Index, env)
		if !ok {
			return "", false
		}
		return "(select " + arr + " " + idx + ")", true
	case *ast.FieldExpr:
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
		if n.Field == "count" || n.Field == "len" {
			arr, ok := tr.arrayTermEnv(n.Object, env)
			if !ok {
				return "", false
			}
			lenSym := arr + "_len"
			tr.lenDecls[lenSym] = true
			return lenSym, true
		}
		if t, ok := smtNumericValueType(tr.a.exprTypes[n]); ok && !IsFloatType(t) {
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
		ssign, sbits, sok := smtIntWidthSign(tr.a.exprTypes[n.Operand])
		dsign, dbits, dok := smtIntWidthSign(tr.a.exprTypes[n])
		if sok && dok && ssign == dsign && dbits >= sbits {
			return tr.termEnv(n.Operand, env)
		}
		return "", false
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
	if signed {
		// Signed overflow TRAPS in debug builds (where contracts are checked), so reaching a return
		// without trapping implies no overflow — the prover may soundly assume the ℤ value. Release
		// wraps for perf, but contracts are off there, so nothing relies on the wrap. Hence: no wrap
		// modeling for signed; the debug trap backs the assumption (see codegen signed-overflow check).
		return raw
	}
	if tr.a.provablyNoArithWrap(n, signed, bits) {
		return raw
	}
	// Unsigned wraps modulo 2^W with well-defined two's-complement semantics (and is frequently an
	// intentional idiom), so it is modeled exactly: `(mod raw 2^W)` is the wrapped value. This is what
	// keeps `ensure result <= total; return total - usage` honest (declines without a guard).
	return "(mod " + raw + " " + smtPow2(bits) + ")"
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
//   - Result must be UNSIGNED, width 1..64 (the `bv2nat` read is the unsigned value; a signed result
//     would need the signed read and is left for a follow-up).
//   - Operands must share the result width (Elisa bitwise is same-type), so int2bv at W is the exact
//     bit pattern with no narrowing/sign surprise.
//   - Shift amounts must be a compile-time constant in [0, W): the machine leaves shifts ≥ width
//     undefined (LLVM poison), so assuming the bitvector "shift past width ⇒ 0" rule would be unsound.
//
// Bridging the Int and BV theories can be costly for the solver on relational goals; the per-query
// timeout turns any hard case into Unknown → runtime fallback, so soundness holds regardless of cost.
func (tr *smtTranslator) bitwiseTerm(n *ast.BinaryExpr, env map[string]string) (string, bool) {
	signed, bits, ok := smtIntWidthSign(tr.a.exprTypes[n])
	if !ok || signed || bits <= 0 || bits > 64 {
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
		} else {
			bvop = "bvlshr" // unsigned result ⇒ logical right shift
		}
	default:
		return "", false
	}
	l, ok := tr.termEnv(n.Left, env)
	if !ok {
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
			return "", false
		}
		rbv = "((_ int2bv " + width + ") " + r + ")"
	}
	return "(bv2nat (" + bvop + " " + lbv + " " + rbv + "))", true
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
	// The affine interval prover computes in int64, so a 64-bit type's range (or a wide intermediate
	// sum) can overflow it. Restrict the interval-based in-range gate to widths ≤ 32, where the bounds
	// and any sum/product of them sit comfortably inside int64 with no overflow risk. 64-bit results
	// that are not a no-underflow subtraction take the (sound, exact) wrap path.
	if bits > 32 {
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
		case lexer.TOKEN_GT, lexer.TOKEN_GTEQ, lexer.TOKEN_LT, lexer.TOKEN_LTEQ:
			l, ok := tr.termEnv(n.Left, env)
			if !ok {
				return "", false
			}
			r, ok := tr.termEnv(n.Right, env)
			if !ok {
				return "", false
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
					return smtCompare(n.Op, l, r), true
				}
			}
			// Boolean equality: both sides are bool-valued (e.g. `result == ((x % m) == 0)`). SMT `=`
			// is polymorphic, so equate the two Bool terms directly (negated for `!=`).
			if bl, blok := tr.boolTerm(n.Left, env); blok {
				if br, brok := tr.boolTerm(n.Right, env); brok {
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
	default:
		return "", false
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

func (tr *smtTranslator) callResultTerm(call *ast.CallExpr) (string, bool) {
	if tr == nil || tr.a == nil || call == nil {
		return "", false
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
	if !isSMTExactAssignmentType(resultType) || IsBoolType(resultType) {
		return "", false
	}
	ret := tr.freshAux(smtTypeNonNegative(resultType))
	if !direct || decl == nil || len(decl.EnsureValues) == 0 {
		return ret, true
	}
	args := proofCallArgs(call)
	subst := map[string]ast.Expr{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		subst[param.Name] = args[i]
	}
	env, ok := tr.a.smtEnvForSubst(tr, subst)
	if !ok {
		return ret, true
	}
	env["result"] = ret
	for _, ensure := range decl.EnsureValues {
		if ensure == nil {
			continue
		}
		if h, ok := tr.boolTerm(ensure, env); ok {
			tr.auxDecls = append(tr.auxDecls, "(assert "+h+")\n")
		}
	}
	return ret, true
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
	return out
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
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
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
