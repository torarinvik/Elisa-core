package semantic

import (
	"elisacore/src/ast"
	"strings"
)

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
	if a == nil || decl == nil || decl.IsLemma || !a.callResultStateIndependent(decl) {
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
	if _, _, ok := singleMatchReturnBody(a.readDecl(decl)); ok {
		return true
	}
	if body, ok := a.typedPureReturnExpr(decl); ok && body != nil {
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
	a.collectStructuralRecursiveCalls(a.readDecl(decl).Body, id.Name, measureEnum, map[string]bool{}, allowed)
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
				if d, ok := a.resolveDirectCallFuncDecl(call); ok && a.canonicalFuncDecl(d) == a.canonicalFuncDecl(decl) {
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
	walkStmt(a.readDecl(decl).Body)
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
