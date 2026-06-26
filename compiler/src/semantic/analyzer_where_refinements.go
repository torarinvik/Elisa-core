package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func whereRefinementTypeExpr(te ast.TypeExpr) (*ast.WhereRefinementTypeExpr, bool) {
	switch n := te.(type) {
	case *ast.WhereRefinementTypeExpr:
		return n, true
	case *ast.OwnedType:
		return whereRefinementTypeExpr(n.Elem)
	}
	return nil, false
}

func (a *Analyzer) analyzeParamWhereRefinements(fn *ast.FuncDecl) {
	if a == nil || fn == nil {
		return
	}
	params := a.expandedFuncDeclParams(fn)
	a.ghostReadAllowed++
	defer func() { a.ghostReadAllowed-- }()
	for i, param := range params {
		rt, ok := whereRefinementTypeExpr(param.Type)
		if !ok || rt == nil || rt.Predicate == nil {
			continue
		}
		a.validateWhereParamReferences(rt.Predicate, param.Name, params[:i])
		a.analyzeWhereBoolPredicate(rt.Predicate)
		a.seedRangeFactsFromCondition(rt.Predicate)
		a.collectBoundEqualitiesForCondition(rt.Predicate, true)
	}
}

func (a *Analyzer) analyzeReturnWhereRefinement(fn *ast.FuncDecl, fnType *FuncType) {
	if a == nil || fn == nil {
		return
	}
	rt, ok := whereRefinementTypeExpr(fn.ReturnType)
	if !ok || rt == nil || rt.Predicate == nil {
		return
	}
	saved := a.currentScope
	scope := NewScope(saved)
	if fnType != nil && fnType.Return != nil && !isVoidType(fnType.Return) {
		scope.Define(&Symbol{Name: "result", Kind: SymbolLocal, Type: fnType.Return})
	}
	a.currentScope = scope
	a.inEnsureContext = true
	a.ghostReadAllowed++
	defer func() {
		a.currentScope = saved
		a.inEnsureContext = false
		a.ghostReadAllowed--
	}()
	a.validateWhereResultReferences(rt.Predicate, fn.Params)
	a.analyzeWhereBoolPredicate(rt.Predicate)
}

func (a *Analyzer) dischargeReturnWhereRefinement(stmt *ast.ReturnStmt) {
	if a == nil || a.currentFuncDecl == nil || stmt == nil || stmt.Value == nil {
		return
	}
	rt, ok := whereRefinementTypeExpr(a.currentFuncDecl.ReturnType)
	if !ok || rt == nil || rt.Predicate == nil {
		return
	}
	subst := map[string]ast.Expr{"result": stmt.Value}
	a.dischargeWherePredicate(rt.Predicate, subst, stmt.Pos(), "return where refinement")
}

func (a *Analyzer) dischargeLocalWhereRefinement(stmt *ast.VarDeclStmt) {
	if a == nil || stmt == nil || stmt.Type == nil {
		return
	}
	rt, ok := whereRefinementTypeExpr(stmt.Type)
	if !ok || rt == nil || rt.Predicate == nil {
		return
	}
	a.ghostReadAllowed++
	a.validateWhereLocalReferences(rt.Predicate, stmt.Name)
	a.analyzeWhereBoolPredicate(rt.Predicate)
	a.ghostReadAllowed--
	// Route the discharge through dischargeWhereRefinement so all local binding-site
	// obligations use the shared prover ladder (linear → SMT → runtime).
	if !a.dischargeWhereRefinement(rt, stmt.Name, stmt.Value, stmt.Pos(), "local where refinement") {
		// Obligation unknown — record as runtime and emit the rich proof-lint diagnostic.
		a.recordProof(stmt.Pos(), "local where refinement", "where", ProofRuntime)
		subst := map[string]ast.Expr{}
		if stmt.Value != nil {
			subst[stmt.Name] = stmt.Value
		}
		diag := a.buildRequiresFailureDiagnostic(rt.Predicate, subst, "")
		a.proofLint(stmt.Pos(), "%s", diag.Format("local where refinement"))
	}
	a.seedRangeFactsFromCondition(rt.Predicate)
	a.collectBoundEqualitiesForCondition(rt.Predicate, true)
}

func (a *Analyzer) checkCalleeParamWhereRefinements(call *ast.CallExpr, declName string, params []ast.ParamDecl, args []ast.Expr) {
	if a == nil || call == nil {
		return
	}
	subst := map[string]ast.Expr{}
	for i, param := range params {
		if i < len(args) && args[i] != nil {
			subst[param.Name] = args[i]
		}
	}
	for i, param := range params {
		rt, ok := whereRefinementTypeExpr(param.Type)
		if !ok || rt == nil || rt.Predicate == nil {
			continue
		}
		a.validateWhereParamReferences(rt.Predicate, param.Name, params[:i])
		a.dischargeWherePredicate(rt.Predicate, subst, call.Pos(), "where precondition of "+declName)
	}
}

func (a *Analyzer) dischargeWherePredicate(pred ast.Expr, subst map[string]ast.Expr, pos lexer.Pos, subject string) {
	switch a.proveRequiresClause(pred, subst) {
	case requiresProven:
		a.recordProof(pos, subject, "where", ProofProvenLinear)
	case requiresRefuted:
		a.recordProof(pos, subject, "where", ProofRefuted)
		a.errorf(pos, "%s is violated", subject)
	default:
		proven, counterexample := a.trySMTProveRequires(pred, subst)
		if proven {
			a.recordProof(pos, subject, "where", ProofProvenSMT)
			return
		}
		a.recordProof(pos, subject, "where", ProofRuntime)
		diag := a.buildRequiresFailureDiagnostic(pred, subst, counterexample)
		a.proofLint(pos, "%s", diag.Format(subject))
	}
}

func (a *Analyzer) analyzeWhereBoolPredicate(pred ast.Expr) {
	if pred == nil {
		return
	}
	if !wherePredicateIsSideEffectFree(pred) {
		a.errorf(pred.Pos(), "where refinement predicate must be pure and side-effect-free")
	}
	t := a.analyzeExpr(pred)
	if t != nil && !IsBoolType(t) {
		a.errorf(pred.Pos(), "where refinement predicate must be bool, got %s", typeString(t))
	}
}

func (a *Analyzer) validateWhereParamReferences(pred ast.Expr, self string, earlier []ast.ParamDecl) {
	allowed := map[string]bool{self: true}
	for _, p := range earlier {
		allowed[p.Name] = true
	}
	a.validateWhereReferences(pred, allowed, "parameter where refinement may only reference its parameter and earlier parameters")
}

func (a *Analyzer) validateWhereResultReferences(pred ast.Expr, params []ast.ParamDecl) {
	allowed := map[string]bool{"result": true}
	for _, p := range params {
		allowed[p.Name] = true
	}
	a.validateWhereReferences(pred, allowed, "return where refinement may only reference result and parameters")
}

func (a *Analyzer) validateWhereLocalReferences(pred ast.Expr, self string) {
	allowed := map[string]bool{self: true}
	for _, name := range exprIdentNames(pred) {
		if allowed[name] {
			continue
		}
		if a.currentScope != nil {
			if _, ok := a.currentScope.Lookup(name); ok {
				allowed[name] = true
			}
		}
	}
	a.validateWhereReferences(pred, allowed, "local where refinement may only reference the local and values in scope")
}

func (a *Analyzer) validateWhereReferences(pred ast.Expr, allowed map[string]bool, msg string) {
	for _, name := range exprIdentNames(pred) {
		if allowed[name] || name == "true" || name == "false" {
			continue
		}
		if a != nil {
			if _, ok := a.namedTypes[name]; ok {
				continue
			}
		}
		if allowed[name] {
			continue
		}
		a.errorf(pred.Pos(), "%s: %q is not available here", msg, name)
	}
}

func wherePredicateIsSideEffectFree(expr ast.Expr) bool {
	switch n := expr.(type) {
	case nil, *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.CharLit:
		return true
	case *ast.ParenExpr:
		return wherePredicateIsSideEffectFree(n.Inner)
	case *ast.UnaryExpr:
		return wherePredicateIsSideEffectFree(n.Operand)
	case *ast.BinaryExpr:
		return wherePredicateIsSideEffectFree(n.Left) && wherePredicateIsSideEffectFree(n.Right)
	case *ast.FieldExpr:
		return wherePredicateIsSideEffectFree(n.Object)
	case *ast.IndexExpr:
		return wherePredicateIsSideEffectFree(n.Object) && wherePredicateIsSideEffectFree(n.Index) && wherePredicateIsSideEffectFree(n.Index2) && wherePredicateIsSideEffectFree(n.Fallback)
	case *ast.SliceExpr:
		return wherePredicateIsSideEffectFree(n.Object) && wherePredicateIsSideEffectFree(n.Start) && wherePredicateIsSideEffectFree(n.End)
	}
	return false
}

func exprIdentNames(expr ast.Expr) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch n := e.(type) {
		case nil:
		case *ast.Ident:
			if !seen[n.Name] {
				seen[n.Name] = true
				out = append(out, n.Name)
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
			for _, arg := range n.Args {
				walk(arg)
			}
		}
	}
	walk(expr)
	return out
}

// seedWhereRefinementFact records the where predicate of a local binding as an SMT assert-fact so
// later obligations in the same scope can consume it. Only fires when the declared type is a
// WhereRefinementTypeExpr; no-ops otherwise, so non-where locals are unaffected.
//
// The predicate is recorded as-is: it already references the bound name (e.g. `x > 0` for
// `x: i64 where x > 0`), so smtFactDeps will include `x` and invalidateSMTAssertFactsForTarget will
// drop the fact the moment `x` is mutated — precisely the invalidation-on-mutation behaviour required.
// An immutable local is never mutated, so the fact lives for the binding's whole scope.
func (a *Analyzer) seedWhereRefinementFact(n *ast.VarDeclStmt) {
	if a == nil || n == nil || a.currentScope == nil {
		return
	}
	wt, ok := n.Type.(*ast.WhereRefinementTypeExpr)
	if !ok || wt == nil || wt.Predicate == nil {
		return
	}
	// Record the predicate as an SMT assert-fact. The predicate already carries the bound variable's
	// name as an identifier; smtFactDeps extracts it as a dependency root, and
	// invalidateSMTAssertFactsForTarget drops the fact when that root is mutated.
	a.recordSMTAssertFact(wt.Predicate)
}

// seedParamWhereRefinementFacts records the where-predicate of each immutable param as an SMT
// assert-fact on function entry. This is the param-boundary analogue of seedWhereRefinementFact for
// locals: parameters declared as `p: T where P(p)` are assumed to satisfy P on entry (the CALLER is
// responsible for the discharge at the call site), so the body may use P as a proven hypothesis.
//
// Mutable params are skipped: a mutation of `p` inside the body would invalidate the fact, but we
// must not confuse "invalidated by body mutation" with "the fact was never true". Skipping is always
// sound (just means the body re-proves P via SMT if needed), and matches the conservatism of
// seedParamRefinementFacts for the range-fact channel.
func (a *Analyzer) seedParamWhereRefinementFacts(params []ast.ParamDecl) {
	if a == nil || a.currentScope == nil {
		return
	}
	for _, param := range params {
		if a.paramIsMutable(param) {
			continue
		}
		wt, ok := param.Type.(*ast.WhereRefinementTypeExpr)
		if !ok || wt == nil || wt.Predicate == nil {
			continue
		}
		a.recordSMTAssertFact(wt.Predicate)
	}
}

// dischargeWhereRefinement checks that the value assigned to a where-typed local binding satisfies
// the where predicate at the binding site. It routes through the shared proof ladder
// (proveRequiresClause → trySMTProveRequires) rather than a parallel discharge path, so where
// behaves like requires/ensure/is Law at the proof engine level.
//
// pos and subject are used for diagnostics and proof recording. When initExpr is nil (no
// initializer), a no-op substitution is used and the obligation is still attempted.
//
// The predicate is substituted: `x: i64 where P` at `x = expr` becomes `P[x ↦ expr]` as the goal.
// Substitution is performed via the standard proveRequiresClause substitution map so the prover
// never sees the binding name — only the initializer expression.
//
// Returns true when the obligation was statically discharged (proved or refuted — refuted also emits
// an error). Returns false when the obligation is unknown and a runtime check is warranted.
func (a *Analyzer) dischargeWhereRefinement(wt *ast.WhereRefinementTypeExpr, bindingName string, initExpr ast.Expr, pos lexer.Pos, subject string) bool {
	if a == nil || wt == nil || wt.Predicate == nil {
		return true // nothing to prove
	}
	// Build a substitution: replace the binding name with the initializer expression so the
	// predicate becomes a pure constraint over the initializer values. This is the same
	// substitution that proveRequiresClause uses for `requires` clauses at call sites.
	subst := map[string]ast.Expr{}
	if initExpr != nil {
		subst[bindingName] = initExpr
	}
	// Tier 1: linear clause prover (interval arithmetic over known ranges / written-const facts).
	switch a.proveRequiresClause(wt.Predicate, subst) {
	case requiresProven:
		a.recordProof(pos, subject, "where", ProofProvenLinear)
		return true
	case requiresRefuted:
		// Statically refuted — emit a hard error; do not fall through to a runtime check.
		a.recordProof(pos, subject, "where", ProofRefuted)
		a.errorf(pos, "%s is violated", subject)
		return true
	}
	// Tier 2: SMT solver (Z3 via SMT-LIB2 subprocess, only when -smt is active).
	if proven, _ := a.trySMTProveRequires(wt.Predicate, subst); proven {
		a.recordProof(pos, subject, "where", ProofProvenSMT)
		return true
	}
	// Unknown — caller should emit a runtime check diagnostic.
	return false
}
