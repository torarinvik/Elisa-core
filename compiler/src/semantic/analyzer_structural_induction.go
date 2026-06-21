package semantic

// Structural induction over recursive datatypes (docs/101 follow-up to bounded numeric unfolding).
//
// The numeric defining-equation machinery (analyzer_recursive_proof_certs.go +
// emitDefiningEquation) proves properties of integer-recursive pure functions by canonicalizing each
// recursive call to a shared SMT symbol and asserting `f(args) == body`, with the body's own recursive
// sub-call standing in as the inductive hypothesis. That machinery is INTEGER-ONLY: a bool-returning
// predicate over a recursive enum (`ghost def all_nonneg(t: Tree) -> bool`) returns bool and is defined
// by a `match`, neither of which the numeric path models — so `ensure all_nonneg(result)` always
// declined.
//
// This file adds the STRUCTURAL-INDUCTION analogue for such predicates:
//
//   (1) CANONICALIZATION. A call `p(arg)` to a bool predicate lowers to ONE opaque SMT Bool symbol
//       keyed by callee + the SYNTACTIC projection of its arguments. Two occurrences of `p(t)` — one in
//       a `requires` hypothesis, one in an `ensure` obligation — therefore share a symbol, so an
//       identity passthrough (`return t` with `requires p(t)` / `ensure p(result)`) discharges trivially.
//       This alone needs NO unfolding and is always sound (a deterministic predicate maps equal
//       syntactic inputs to equal truth values).
//
//   (2) STRUCTURAL DEFINING EQUATION. When the predicate is STRUCTURALLY WELL-FOUNDED — it recurses
//       only on match-bound children of a recursive-plain enum and carries a VERIFIED structural
//       `decreases` measure (reusing structuralTerminationCertificate) — we additionally assert the
//       match-unfolding `p(t) == arm-disjunction`, where each recursive sub-call `p(child)` is itself
//       canonicalized to its own opaque Bool symbol. That sub-symbol IS the inductive hypothesis: the
//       prover may assume the predicate on strictly-smaller substructures (justified by structural
//       decrease) while proving it for the whole. The unfolding is bounded by callEqMaxUnroll, so the
//       asserted axiom set is always finite and the prover never hangs.
//
// SOUNDNESS. Only `unsat` ever concludes a proof. The structural defining equation is emitted ONLY for
// a structurally well-founded predicate; a non-well-founded recursive predicate (recursion not on a
// strictly-smaller child) gets the opaque symbol from (1) but NO equation — exactly as a
// non-terminating numeric recursion is denied its (possibly inconsistent) equation. Bounded unrolling
// guarantees termination of the emission itself. We never assume an unproven inductive step: the IH is
// only the predicate applied to a child the termination certificate proved is structurally smaller.

import (
	"strings"

	"elisacore/src/ast"
)

// boolPredicateCallSym returns the canonical opaque SMT Bool symbol for a call to a bool-returning
// predicate, declaring it as a free Bool const, and (when the predicate is structurally well-founded)
// emitting its bounded structural defining equation. Returns ok=false when the expression is not a
// direct call to a resolvable bool predicate, or its arguments cannot be given a stable canonical key —
// in which case boolTerm falls back to its other cases (declining soundly).
func (tr *smtTranslator) boolPredicateCallSym(call *ast.CallExpr, env map[string]string) (string, bool) {
	if tr == nil || call == nil || tr.a == nil {
		return "", false
	}
	decl, ok := tr.a.resolveDirectCallFuncDecl(call)
	if !ok || decl == nil {
		return "", false
	}
	if !tr.a.boolPredicateEligible(decl) {
		return "", false
	}
	args := proofCallArgs(call)
	key, ok := tr.boolPredicateCanonKey(decl, args, env)
	if !ok {
		return "", false
	}
	if sym, seen := tr.callCanon[key]; seen {
		return sym, true
	}
	sym := tr.freshBoolPredAux()
	tr.callCanon[key] = sym
	// (2) bounded structural defining equation — only for a well-founded recursive (or trivially total
	// non-recursive) predicate. A NO-OP otherwise, leaving `sym` purely opaque (still sound).
	tr.emitStructuralPredicateEquation(call, decl, sym, args, env, key)
	return sym, true
}

// boolPredicateCanonKey builds the canonicalization key for a bool-predicate call: the callee identity
// plus a SYNTACTIC projection of each argument (resolved through the current `env` binding so a
// match-bound child `l` keys on the projection it stands for, not the bare name). Datatype arguments
// have no integer SMT term, so — unlike callCanonKey — we key on the projection NAME, which is exactly
// what makes two textual occurrences of `p(t)` collapse to one symbol while keeping `p(l)` and `p(r)`
// distinct.
func (tr *smtTranslator) boolPredicateCanonKey(decl *ast.FuncDecl, args []ast.Expr, env map[string]string) (string, bool) {
	var b strings.Builder
	b.WriteString("boolpred|")
	if sym, ok := tr.a.symbolForFuncDecl(decl); ok && sym != nil {
		b.WriteString(decl.Name)
		_ = sym
	} else {
		b.WriteString(decl.Name)
	}
	b.WriteByte('|')
	for _, arg := range args {
		if arg == nil {
			return "", false
		}
		// Prefer an integer/bool SMT term when the argument lives in that fragment (so `p(n-1)` keys
		// canonically); otherwise fall back to a syntactic projection token (datatype args).
		if proj, ok := tr.boolPredicateArgToken(arg, env); ok {
			b.WriteString(proj)
			b.WriteByte('|')
			continue
		}
		return "", false
	}
	return b.String(), true
}

// boolPredicateArgToken yields a stable token for a predicate argument. For an argument bound in `env`
// (a match-child substitution) it uses the bound token directly; for a plain projection (ident / field
// chain) the syntactic projection name; for integer-fragment expressions the canonical SMT term.
func (tr *smtTranslator) boolPredicateArgToken(arg ast.Expr, env map[string]string) (string, bool) {
	if id, ok := stripOptimizationParens(arg).(*ast.Ident); ok {
		if env != nil {
			if bound, ok := env[id.Name]; ok && bound != "" {
				// A datatype-projection env binding (`result` -> the substituted value's projection) is
				// decoded to that projection, so `p(result)` with `result := t` keys identically to a bare
				// `p(t)`. Any other binding is an SMT term / child token used verbatim.
				if strings.HasPrefix(bound, dtTokenPrefix) {
					return "proj(" + strings.TrimPrefix(bound, dtTokenPrefix) + ")", true
				}
				return "tok(" + bound + ")", true
			}
		}
		return "proj(" + smtProjectionName(id) + ")", true
	}
	if fe, ok := stripOptimizationParens(arg).(*ast.FieldExpr); ok {
		return "proj(" + smtProjectionName(fe) + ")", true
	}
	if t, ok := tr.termEnv(arg, env); ok {
		return "int(" + t + ")", true
	}
	return "proj(" + smtProjectionName(arg) + ")", true
}

// emitStructuralPredicateEquation asserts `sym == <match-unfolding>` for a structurally well-founded
// bool predicate, with recursive sub-calls canonicalized (the IH). Bounded by callEqMaxUnroll. It is a
// sound NO-OP whenever the predicate is recursive but NOT verified structurally decreasing, the body is
// not a single pure `match`/return tree we can lower, or any arm fails to lower to a Bool term.
func (tr *smtTranslator) emitStructuralPredicateEquation(call *ast.CallExpr, decl *ast.FuncDecl, sym string, args []ast.Expr, env map[string]string, key string) {
	if tr == nil || decl == nil {
		return
	}
	// TOTALITY GATE: a recursive predicate must be proven structurally well-founded. A non-recursive
	// predicate is trivially total. Decline (opaque only) otherwise.
	if tr.a.predicateIsRecursive(decl) && !tr.a.predicateStructurallyWellFounded(decl) {
		return
	}
	if tr.callEqInFlight[key] >= tr.callEqMaxUnroll {
		return // bounded unroll reached: leave the symbol opaque (still sound)
	}
	// Map the predicate's parameters to the actual-argument tokens so a recursive sub-call on a child
	// keys on that child's projection (the IH symbol), and integer payload comparisons see the actuals.
	penv := map[string]string{}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		if tok, ok := tr.boolPredicateArgToken(args[i], env); ok {
			penv[param.Name+"$tok"] = tok
		}
		if IsBoolType(tr.a.exprTypes[args[i]]) {
			if bt, ok := tr.boolTerm(args[i], env); ok {
				penv[param.Name] = bt
			}
		} else if t, ok := tr.termEnv(args[i], env); ok {
			penv[param.Name] = t
		}
	}
	tr.callEqInFlight[key]++
	defer func() { tr.callEqInFlight[key]-- }()
	bodyTerm, ok := tr.lowerPredicateBody(decl, penv)
	if !ok {
		return
	}
	tr.auxDecls = append(tr.auxDecls, "(assert (= "+sym+" "+bodyTerm+"))\n")
	if tr.a.predicateIsRecursive(decl) {
		if cert, ok := tr.a.recursiveEquationCertificate(decl); ok {
			tr.a.recordProof(call.Pos(), "structural induction over "+decl.Name+" ("+cert.label()+")", "definition", ProofProvenContract)
		} else {
			tr.a.recordProof(call.Pos(), "structural induction over "+decl.Name+" (verified structural decrease)", "definition", ProofProvenContract)
		}
	} else {
		tr.a.recordProof(call.Pos(), "predicate equation of "+decl.Name, "definition", ProofProvenContract)
	}
}

// lowerPredicateBody lowers a bool predicate's body to a Bool SMT term under the per-call environment
// `penv` (param tokens in `<name>$tok`, integer/bool param substitutions in `<name>`). Supports a body
// that is a single `match <param>:` over a recursive enum, or any pure-return tree boolTerm already
// handles. Returns ok=false for shapes we do not model.
func (tr *smtTranslator) lowerPredicateBody(decl *ast.FuncDecl, penv map[string]string) (string, bool) {
	// Pure-return tree (if/elif/else of bool returns, or a single bool return) — reuse boolTerm.
	if body, ok := pureReturnExpr(decl); ok && body != nil {
		return tr.boolTerm(body, tr.predicateEnvIntBool(penv))
	}
	// Single top-level `match <param>:` returning bool from each arm.
	ms, scrutName, ok := singleMatchReturnBody(decl)
	if !ok {
		return "", false
	}
	return tr.lowerPredicateMatch(decl, ms, scrutName, penv)
}

// predicateEnvIntBool projects penv down to the int/bool substitution entries boolTerm understands
// (dropping the `$tok` projection entries, which are only for canonicalization keys).
func (tr *smtTranslator) predicateEnvIntBool(penv map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range penv {
		if strings.HasSuffix(k, "$tok") {
			continue
		}
		out[k] = v
	}
	return out
}

// lowerPredicateMatch lowers `match <scrut>: <Variant(binds...)>: <bool-body>` to a Bool term. Each arm
// becomes `(and is_<scrut>_<Variant> <arm-body>)` and the whole is the disjunction over arms. The arm
// guard predicates `is_<tok>_<Variant>` are opaque Bool consts keyed by the scrutinee token, plus an
// assertion that exactly one holds (exhaustive + mutually exclusive over the enum's variants), so the
// disjunction is a faithful case split. Child binds are mapped to a projection token so a recursive
// sub-call `p(child)` canonicalizes to its own IH symbol; integer payload binds become fresh Ints.
func (tr *smtTranslator) lowerPredicateMatch(decl *ast.FuncDecl, ms *ast.MatchStmt, scrutName string, penv map[string]string) (string, bool) {
	scrutTok, ok := penv[scrutName+"$tok"]
	if !ok || scrutTok == "" {
		return "", false
	}
	base := "isv_" + smtSanitize(scrutTok)
	var arms []string
	var guards []string
	for _, arm := range ms.Arms {
		vp, ok := arm.Pattern.(*ast.MatchVariantPattern)
		if !ok || vp == nil {
			return "", false
		}
		guard := base + "_" + smtSanitize(vp.Variant)
		tr.boolDecls[guard] = true
		guards = append(guards, guard)
		// Per-arm environment: bind child/payload pattern names.
		armEnv := map[string]string{}
		for k, v := range penv {
			armEnv[k] = v
		}
		tr.bindMatchArmNames(vp, scrutTok, armEnv)
		bodyExpr, ok := singleReturnExpr(arm.Body)
		if !ok {
			return "", false
		}
		bt, ok := tr.boolTerm(bodyExpr, tr.predicateArmEnv(armEnv))
		if !ok {
			return "", false
		}
		arms = append(arms, "(and "+guard+" "+bt+")")
	}
	if len(arms) == 0 || len(guards) == 0 {
		return "", false
	}
	// Exactly-one guard holds: exhaustive (or) + pairwise-exclusive (not both). A faithful, sound case
	// split over the enum's variants (the match was checked exhaustive at type time).
	tr.auxDecls = append(tr.auxDecls, "(assert (or "+strings.Join(guards, " ")+"))\n")
	for i := 0; i < len(guards); i++ {
		for j := i + 1; j < len(guards); j++ {
			tr.auxDecls = append(tr.auxDecls, "(assert (not (and "+guards[i]+" "+guards[j]+")))\n")
		}
	}
	if len(arms) == 1 {
		return arms[0], true
	}
	return "(or " + strings.Join(arms, " ") + ")", true
}

// bindMatchArmNames records each variant-pattern child/payload bind name in armEnv: a recursive-enum
// child gets a projection token derived from the scrutinee token + variant + field index (so its
// predicate self-call canonicalizes to a distinct IH symbol); other binds are left for boolTerm/termEnv
// to mint fresh symbols.
func (tr *smtTranslator) bindMatchArmNames(vp *ast.MatchVariantPattern, scrutTok string, armEnv map[string]string) {
	args := vp.ResolvedArgs
	if len(args) == 0 {
		args = make([]*ast.MatchPatternArg, len(vp.Args))
		for i := range vp.Args {
			args[i] = &vp.Args[i]
		}
	}
	for i, arg := range args {
		if arg == nil {
			continue
		}
		bp, ok := arg.Pattern.(*ast.MatchBindPattern)
		if !ok || bp == nil || bp.Name == "" {
			continue
		}
		childTok := "child(" + scrutTok + "," + vp.Variant + "," + smtSanitize(bp.Name) + "," + itoa(i) + ")"
		armEnv[bp.Name+"$tok"] = childTok
	}
}

// predicateArmEnv exposes the arm env to boolTerm/termEnv: the int/bool substitution entries pass
// through; `$tok` projection entries are dropped (boolTerm keys recursive predicate calls via
// boolPredicateArgToken, which re-reads `$tok` from the env it is handed). To make that work we also
// surface each `<name>$tok` as `<name>` so boolPredicateArgToken's env lookup finds the child token.
func (tr *smtTranslator) predicateArmEnv(armEnv map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range armEnv {
		if strings.HasSuffix(k, "$tok") {
			out[strings.TrimSuffix(k, "$tok")] = v
			continue
		}
		if _, taken := out[k]; !taken {
			out[k] = v
		}
	}
	return out
}

// ---- predicate eligibility / well-foundedness ----------------------------------------------------

// boolPredicateEligible reports whether a function may be treated as a logical predicate the prover can
// canonicalize and (when well-founded) unfold. Conservative: bool return, pure predicate shape (a ghost
// def, or a pure-return / single-match body). Anything unsure is ineligible (forgoes completeness only).
func (a *Analyzer) boolPredicateEligible(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil || decl.IsLemma {
		return false
	}
	sym, ok := a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || fnType.Return == nil || !IsBoolType(fnType.Return) {
		return false
	}
	if _, _, ok := singleMatchReturnBody(decl); ok {
		return true
	}
	if body, ok := pureReturnExpr(decl); ok && body != nil {
		return true
	}
	return false
}

// predicateIsRecursive reports whether the predicate makes any direct self-call.
func (a *Analyzer) predicateIsRecursive(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil {
		return false
	}
	return len(a.collectSelfRecursiveCallsIn(decl)) > 0
}

// predicateStructurallyWellFounded reports whether every direct self-call recurses on a match-bound
// strictly-smaller child of a recursive-plain enum — i.e. the predicate carries a VERIFIED structural
// decrease. Reuses the structural termination certificate so the IH is justified by the same
// well-foundedness the numeric path relies on. A predicate with NO explicit `decreases` but a single
// recursive-plain enum param recursing only on its match children is accepted via the implicit measure.
func (a *Analyzer) predicateStructurallyWellFounded(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil {
		return false
	}
	calls := a.collectSelfRecursiveCallsIn(decl)
	if len(calls) == 0 {
		return true // non-recursive: trivially total
	}
	measure := a.predicateStructuralMeasure(decl)
	if measure == nil {
		return false
	}
	// All recursive calls must land on a structural child of the measured enum param.
	id, ok := measure.(*ast.Ident)
	if !ok || id == nil {
		return false
	}
	measureEnum := a.structuralMeasureEnumForFunc(decl, id.Name)
	if measureEnum == nil {
		return false
	}
	allowed := map[*ast.CallExpr]bool{}
	a.collectStructuralRecursiveCalls(decl.Body, id.Name, measureEnum, map[string]bool{}, allowed)
	for _, c := range calls {
		if !allowed[c] {
			return false
		}
	}
	return true
}

// predicateStructuralMeasure returns the structural measure expression: an explicit single structural
// `decreases`, or — when absent — the predicate's sole recursive-plain enum parameter (the canonical
// implicit measure for a structural predicate). Returns nil when no well-founded structural measure
// exists, which makes predicateStructurallyWellFounded decline (sound).
func (a *Analyzer) predicateStructuralMeasure(decl *ast.FuncDecl) ast.Expr {
	if len(decl.Decreases) == 1 && a.isStructuralDecreaseMeasure(decl, decl.Decreases[0]) {
		return decl.Decreases[0]
	}
	if len(decl.Decreases) != 0 {
		return nil // an explicit non-structural / multi measure: not handled here
	}
	if decl.DecreasesWild != "" {
		return nil // wildcard opt-out cannot grant well-foundedness
	}
	// Implicit measure: a single recursive-plain enum parameter.
	var found ast.Expr
	for _, p := range decl.Params {
		if et := a.structuralMeasureEnumForFunc(decl, p.Name); et != nil {
			if found != nil {
				return nil // ambiguous: more than one recursive-enum param
			}
			found = &ast.Ident{Position: decl.Pos(), Name: p.Name}
		}
	}
	return found
}

// collectSelfRecursiveCallsIn gathers direct self-recursive CallExprs in a decl's body. Mirrors
// collectSelfRecursiveCalls but does not assume decl is the currently-analyzed function.
func (a *Analyzer) collectSelfRecursiveCallsIn(decl *ast.FuncDecl) []*ast.CallExpr {
	if a == nil || decl == nil {
		return nil
	}
	var out []*ast.CallExpr
	var walkStmt func(stmts []ast.Stmt)
	walkExpr := func(e ast.Expr) {
		a.walkStaticExpr(e, func(x ast.Expr) bool {
			if call, ok := x.(*ast.CallExpr); ok {
				if d, ok := a.resolveDirectCallFuncDecl(call); ok && d == decl {
					out = append(out, call)
				}
			}
			return false
		})
	}
	walkStmt = func(stmts []ast.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ast.ReturnStmt:
				if n.Value != nil {
					walkExpr(n.Value)
				}
			case *ast.ExprStmt:
				walkExpr(n.Expr)
			case *ast.IfStmt:
				walkStmt(n.Then)
				walkStmt(n.Else)
				for _, e := range n.Elifs {
					walkStmt(e.Body)
				}
			case *ast.MatchStmt:
				for _, arm := range n.Arms {
					walkStmt(arm.Body)
				}
			}
		}
	}
	walkStmt(decl.Body)
	return out
}

// ---- small AST helpers ---------------------------------------------------------------------------

// singleMatchReturnBody returns the body's single top-level `match <param>:` statement (every arm a
// single `return <expr>`) and the matched parameter name. ok=false for any other shape.
func singleMatchReturnBody(decl *ast.FuncDecl) (*ast.MatchStmt, string, bool) {
	if decl == nil || len(decl.Body) != 1 {
		return nil, "", false
	}
	ms, ok := decl.Body[0].(*ast.MatchStmt)
	if !ok || ms == nil || len(ms.Arms) == 0 {
		return nil, "", false
	}
	id, ok := stripOptimizationParens(ms.Value).(*ast.Ident)
	if !ok || id == nil {
		return nil, "", false
	}
	for _, arm := range ms.Arms {
		if _, ok := singleReturnExpr(arm.Body); !ok {
			return nil, "", false
		}
	}
	return ms, id.Name, true
}

// singleReturnExpr extracts the single `return <expr>` from an arm body.
func singleReturnExpr(stmts []ast.Stmt) (ast.Expr, bool) {
	if len(stmts) != 1 {
		return nil, false
	}
	rs, ok := stmts[0].(*ast.ReturnStmt)
	if !ok || rs == nil || rs.Value == nil {
		return nil, false
	}
	return rs.Value, true
}

// freshBoolPredAux mints a fresh opaque SMT Bool symbol. It registers the name in boolDecls (factPreamble
// emits its declaration, deterministically, alongside the other free Bool consts) and in auxVars (for
// the Sat counterexample query). It does NOT add its own declare line — that would double-declare.
func (tr *smtTranslator) freshBoolPredAux() string {
	tr.auxSeq++
	name := "bpred_" + itoa(tr.auxSeq)
	tr.auxVars = append(tr.auxVars, name)
	tr.boolDecls[name] = true
	return name
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
