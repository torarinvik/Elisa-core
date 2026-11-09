package semantic

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"

	"elisacore/src/ast"
)

func (a *Analyzer) trySMTProveRequiresCoreWithCallee(clause ast.Expr, subst map[string]ast.Expr, path ast.Expr, callee *ast.FuncDecl, formalParamSets ...[]ast.ParamDecl) (bool, string, bool) {
	if !a.smtEnabled || a.smtUnavailable || clause == nil {
		return false, "", false
	}
	tr := a.newSMTTranslator(nil)
	tr.bindingCallee = callee
	// Substitution values may be expressions that have not yet reached ordinary body analysis (notably
	// a pure return expression while checking its `ensure`). Type them before translating so arithmetic
	// keeps its machine width and is modeled with wrapping semantics when necessary.
	// `clause` belongs to the callee's formal namespace. Re-analyzing it with the caller's current
	// scope can overwrite its recorded types with same-named caller locals and silently change the
	// width/signedness used by its SMT terms. Its declaration analysis already assigned those types;
	// only the caller-side path and substituted actual expressions need this prepass.
	typedExprs := []ast.Expr{path}
	for _, name := range slices.Sorted(maps.Keys(subst)) {
		typedExprs = append(typedExprs, subst[name])
	}
	if path != nil || len(formalParamSets) != 0 {
		a.typePureTerminationExprsForSMT(typedExprs...)
	}
	// When `result` is bound to a struct literal (`return Pair(1, 2)`), it has no scalar SMT term, so
	// drop it from the env subst (which would otherwise decline) and instead resolve each `result.field`
	// read to that field's construction argument — so `ensure result.a == 1` discharges.
	//
	// The same treatment applies when `result` is a zeroed-then-mutated local: `out: T = zeroed; out.f <- v;
	// return out` — each field resolves to its last written value (from writtenField) or 0 (unwritten fields
	// are still zeroed since the root was not wholesale-replaced, tracked by a.zeroedStructLocals).
	substForEnv := subst
	if res, ok := subst["result"]; ok {
		res = stripOptimizationParens(res)
		var fieldMap map[string]ast.Expr
		if sl, ok := res.(*ast.StructLitExpr); ok {
			fieldMap = a.structLitFieldMap(sl)
		} else if tuple, ok := res.(*ast.TupleExpr); ok {
			fieldMap = a.tupleLiteralFieldMap(tuple)
		} else if id, ok := res.(*ast.Ident); ok {
			fieldMap = a.zeroedStructLocalFieldMap(id)
		}
		if fieldMap != nil {
			tr.resultFields = fieldMap
			substForEnv = map[string]ast.Expr{}
			for k, v := range subst {
				if k != "result" {
					substForEnv[k] = v
				}
			}
		}
	}
	var formalParams []ast.ParamDecl
	if len(formalParamSets) != 0 {
		formalParams = formalParamSets[0]
	}
	var env map[string]string
	var ok bool
	if len(formalParamSets) != 0 {
		env, ok = a.smtEnvForRelevantSubst(tr, substForEnv, clause, formalParams)
	} else {
		// Preserve the established all-arguments translation for ordinary call and contract proofs.
		// Selective binding is reserved for recursive proof obligations that may pass an unrelated
		// machine expression outside the SMT fragment.
		env, ok = a.smtEnvForSubst(tr, substForEnv)
	}
	if !ok {
		if a.smtTraceOn() {
			fmt.Fprintf(os.Stderr, "=== TRACE env-decline (%s) clause=%s\n", a.currentFuncName(), optimizationExprString(clause))
			for _, k := range slices.Sorted(maps.Keys(substForEnv)) {
				v := substForEnv[k]
				fmt.Fprintf(os.Stderr, "    subst %s = %T %s type=%v\n", k, v, optimizationExprString(v), a.exprType(v))
			}
		}
		return false, "", true
	}
	// Register compound substituted expressions as counterexample readbacks so a Sat model reports
	// the CONCRETE VALUE of each callee parameter at the refuting input, not only the free caller
	// variables that compose it. For example, when `result` is substituted with `x + offset` (SMT:
	// the wrapped sum), the diagnostic shows "result=-1, offset=-1, x=0" instead of only
	// "offset=-1, x=0" — naming the callee-parameter expression that actually violates the clause.
	// Simple bindings (where the SMT term is already a declared free variable) are skipped because
	// they duplicate an entry already in tr.decls.
	trRegisterSubstCEExprs(tr, env, substForEnv)
	// Emit a structural null-ness fact for `result` when its null-ness is definitively known from the
	// return expression's type or value. This is EXTRA (not in the standard hypothesis set) because it
	// is substitution-specific: on a literal-null return `isnull_result` is true, on a T& (non-optional
	// ref) return it is false. The standard set does not know which return site is active.
	extra := a.smtResultNullnessHypothesis(tr, substForEnv)
	if path != nil {
		// The path is expressed in the caller's names. Build caller-identity bindings separately:
		// reusing env would substitute a recursive formal such as `index` with the call argument
		// `index + 1`, strengthening the guard and making the proof unsound. These identity bindings
		// also let a guarded sview length resolve through its authorized array symbol.
		pathEnv := make(map[string]string, len(env)+1)
		if a.currentFuncDecl != nil {
			pathEnv = make(map[string]string, len(env)+len(a.currentFuncDecl.Params)+1)
		}
		for name, term := range env {
			pathEnv[name] = term
		}
		if a.currentFuncDecl != nil {
			for _, param := range a.currentFuncDecl.Params {
				ident := &ast.Ident{Position: param.Position, Name: param.Name}
				a.bindSMTEnvArg(tr, pathEnv, param.Name, ident, nil, false)
			}
			if a.currentScope != nil {
				if sym, found := a.currentScope.Lookup("result"); found && sym != nil {
					ident := &ast.Ident{Position: a.currentFuncDecl.Position, Name: "result"}
					a.bindSMTEnvArg(tr, pathEnv, "result", ident, nil, false)
				}
			}
		}
		pathTerm, ok := tr.boolTerm(path, pathEnv)
		if ok {
			extra += "(assert " + pathTerm + ")\n"
		} else {
			// A path is a conjunction of branch guards, each of which holds at the site. Asserting
			// only the conjuncts the translator models drops hypotheses and never adds one, so the
			// query stays sound; with none translatable the old decline is kept.
			kept := 0
			for _, conjunct := range smtPathConjuncts(path) {
				if term, ok := tr.boolTerm(conjunct, pathEnv); ok {
					extra += "(assert " + term + ")\n"
					kept++
				}
			}
			if kept == 0 {
				return false, "", false
			}
		}
	}
	// Lower the clause to the VC IR and discharge against the standard hypothesis set (the enclosing
	// function's `requires` — docs/90 brick 90-13 — its immutable-local defining equalities, flow
	// assert-facts, and range facts). On sat the returned model is an input permitted by the caller's
	// known facts that violates the precondition — a concrete witness for the diagnostic.
	proven, counterexample, ok := a.smtCheckGoal(tr, clause, env, extra)
	if !ok {
		if a.smtTraceOn() {
			fmt.Fprintf(os.Stderr, "=== TRACE goal-decline (%s) clause=%s\n", a.currentFuncName(), optimizationExprString(clause))
		}
		return false, "", false
	}
	return proven, counterexample, false
}

// smtEnvForRelevantSubst binds only callee parameters that occur in this obligation. A recursive
// call may pass a rich expression for an unrelated parameter (for example, a wrapped u64 update in
// `state`) that lies outside the current SMT fragment. Requiring that unused argument to translate
// would discard a valid proof of a requirement over the other parameters. Every referenced formal
// still must bind successfully; otherwise the solver declines rather than treating it as a caller
// variable.
func (a *Analyzer) smtEnvForRelevantSubst(tr *smtTranslator, subst map[string]ast.Expr, obligation ast.Expr, formalParams []ast.ParamDecl) (map[string]string, bool) {
	used, supported := smtFreeIdentifierSet(obligation)
	if !supported {
		// Selective binding depends on complete identifier collection. Decline unsupported AST
		// forms rather than risk leaving a callee formal free in the caller's namespace.
		return nil, false
	}
	env := map[string]string{}
	for _, param := range formalParams {
		if !used[param.Name] {
			continue
		}
		argExpr, ok := subst[param.Name]
		if !ok || argExpr == nil {
			return nil, false
		}
		if !a.bindSMTEnvArg(tr, env, param.Name, argExpr, nil, true) {
			return nil, false
		}
	}
	// Sorted: binding order names fresh symbols (svarg$N) and decides which binding declines
	// first, so map order made the emitted query set differ run to run.
	for _, name := range slices.Sorted(maps.Keys(subst)) {
		argExpr := subst[name]
		if !used[name] || env[name] != "" {
			continue
		}
		if !a.bindSMTEnvArg(tr, env, name, argExpr, nil, true) {
			return nil, false
		}
	}
	return env, true
}

// smtFreeIdentifierSet reports free identifiers in a contract obligation. Quantifier ranges
// are evaluated outside the binder scope, while quantifier bodies inherit the declared binders.
// The ordinary exprIdentNames helper intentionally reports every spelling for scope validation;
// using it here would try to substitute a shadowed callee parameter and can decline a proof just
// because that unused argument lies outside the SMT fragment.
func smtFreeIdentifierSet(expr ast.Expr) (map[string]bool, bool) {
	free := map[string]bool{}
	bound := map[string]int{}
	supported := true
	patternSupported := true
	var walk func(ast.Expr)
	var walkPattern func(ast.MatchPattern)
	walkPattern = func(pattern ast.MatchPattern) {
		switch p := pattern.(type) {
		case *ast.MatchWildcardPattern, *ast.MatchBindPattern, *ast.MatchStringLiteralPattern, *ast.MatchRestPattern:
			// These patterns contain no expression reads; binders are scoped by the arm below.
		case *ast.MatchLiteralPattern:
			walk(p.Value)
		case *ast.MatchRangePattern:
			walk(p.Lo)
			walk(p.Hi)
		case *ast.MatchTuplePattern:
			for _, elem := range p.Elems {
				walkPattern(elem)
			}
		case *ast.MatchListPattern:
			for _, elem := range p.Elems {
				walkPattern(elem)
			}
		case *ast.MatchOrPattern:
			for _, option := range p.Options {
				walkPattern(option)
			}
		case *ast.MatchStructPattern:
			for _, arg := range p.Args {
				walkPattern(arg.Pattern)
			}
		case *ast.MatchVariantPattern:
			for _, arg := range p.Args {
				walkPattern(arg.Pattern)
			}
		default:
			patternSupported = false
		}
	}
	walk = func(e ast.Expr) {
		switch n := e.(type) {
		case nil:
		case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.CharLit, *ast.BoolLit, *ast.NullLit, *ast.ZeroedLit:
			// Literal leaves contain no identifiers and are part of the SMT fragment.
		case *ast.Ident:
			if bound[n.Name] == 0 {
				free[n.Name] = true
			}
		case *ast.ParenExpr:
			walk(n.Inner)
		case *ast.UnaryExpr:
			walk(n.Operand)
		case *ast.BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *ast.FieldExpr:
			walk(n.Object)
		case *ast.IndexExpr:
			walk(n.Object)
			walk(n.Index)
			walk(n.Index2)
			walk(n.Fallback)
		case *ast.SliceExpr:
			walk(n.Object)
			walk(n.Start)
			walk(n.End)
		case *ast.CallExpr:
			walk(n.Func)
			walk(n.SafeReceiver)
			// Match the SMT call translator's argument selection so synthesized defaults and
			// reordered named arguments cannot hide a referenced callee formal.
			for _, arg := range proofCallArgs(n) {
				walk(arg)
			}
		case *ast.TernaryExpr:
			walk(n.Cond)
			walk(n.Value)
			walk(n.Alt)
		case *ast.TupleExpr:
			for _, elem := range n.Elems {
				walk(elem)
			}
		case *ast.ListLitExpr:
			for _, elem := range n.Elems {
				walk(elem)
			}
			for _, key := range n.Keys {
				walk(key)
			}
			walk(n.Owner)
		case *ast.CastExpr:
			walk(n.Operand)
		case *ast.AddrOfExpr:
			walk(n.Operand)
		case *ast.SpecializeExpr:
			walk(n.Operand)
		case *ast.MatchExpr:
			walk(n.Value)
			walk(n.Store)
			for _, arm := range n.Arms {
				walkPattern(arm.Pattern)
				binders := map[string]bool{}
				collectSMTMatchBinders(arm.Pattern, binders, map[string]bool{})
				for name := range binders {
					bound[name]++
				}
				walk(arm.Guard)
				// Scalar SMT match lowering accepts exactly one expression statement per arm.
				if len(arm.Body) != 1 {
					supported = false
				} else if exprStmt, ok := arm.Body[0].(*ast.ExprStmt); ok {
					walk(exprStmt.Expr)
				} else {
					supported = false
				}
				for name := range binders {
					bound[name]--
					if bound[name] == 0 {
						delete(bound, name)
					}
				}
			}
		case *ast.QuantifierExpr:
			walk(n.In)
			for _, name := range n.Vars {
				bound[name]++
			}
			walk(n.Body)
			for _, name := range n.Vars {
				bound[name]--
				if bound[name] == 0 {
					delete(bound, name)
				}
			}
		case *ast.MembershipRangeExpr:
			walk(n.Start)
			walk(n.End)
		case *ast.RecordUpdateExpr:
			walk(n.Base)
			for _, arg := range n.LoweredArgs() {
				walk(arg)
			}
		case *ast.StructLitExpr:
			args := n.Args
			if n.ResolvedArgsValid {
				args = n.ResolvedArgs
			}
			for _, arg := range args {
				walk(arg)
			}
		case *ast.UnwrapElseExpr:
			walk(n.Value)
			walk(n.Fallback)
		case *ast.GetExpr:
			walk(n.Value)
			walk(n.Fallback)
		default:
			supported = false
		}
	}
	walk(expr)
	return free, supported && patternSupported
}

// trRegisterSubstCEExprs registers env entries whose SMT value is a compound expression (not a plain
// declared free variable) as ceExprs in `tr`, so that a Sat model read-back reports the concrete
// computed value for each substituted callee parameter. This enriches the diagnostic beyond the bare
// caller free-variable assignments: instead of only "x=0, offset=-1", it also shows "result=-1".
// Entries already in tr.decls (plain `v_<name>` symbols) are skipped to avoid duplicate output.
// Only integer-typed entries can be ceExprs (the define-fun emits `() Int`); boolean or array terms
// are silently skipped (they cannot be defined as Int constants).
func trRegisterSubstCEExprs(tr *smtTranslator, env map[string]string, subst map[string]ast.Expr) {
	if tr == nil || tr.a == nil || len(env) == 0 {
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
		// A Boolean UF application looks like a numeric application syntactically.
		// Readbacks are declared as Int, so require the actual source value to
		// have an integer type rather than guessing its sort from the SMT text.
		if _, _, integer := smtIntWidthSign(tr.a.exprType(subst[label])); !integer {
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
		// the output readable. Dotted keys used for struct-field bindings are not shown.
		if strings.Contains(label, smtFieldSeparator) || strings.HasPrefix(label, "__") {
			continue
		}
		if _, alreadyPresent := tr.ceExprs[label]; !alreadyPresent {
			tr.ceExprs[label] = smt
		}
	}
}
