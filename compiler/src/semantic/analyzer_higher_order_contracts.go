package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Higher-order verification (docs/101): let value contracts compose through first-class functions.
//
// A function-typed parameter may carry a postcondition contract written as a leading
//
//	requires ensures(f, <pred over result>)
//
// clause, where `f` names a function-typed parameter and `<pred>` is a boolean predicate over the
// `result` keyword (the function's return value). The clause is NOT an ordinary runtime precondition —
// `ensures(f, ...)` is a spec-only marker — so expandHigherOrderContracts lifts it out of the
// function's Requires slice into the structured FuncDecl.ParamContracts and removes it before the
// ordinary requires-discharge ever sees it.
//
// The contract is then enforced at the two boundaries it spans:
//
//   - CALL SITE (checkHigherOrderParamContracts): wherever a call passes a concrete NAMED function `g`
//     for a parameter carrying a contract, prove that `g`'s declared `ensure` ENTAILS the contract's
//     predicate. If it does not, error (a definite contract break). A non-named argument (a lambda, a
//     forwarded parameter, an arbitrary expression) is declined soundly — no claim is made, so the
//     parameter's contract simply cannot be relied on inside the body for that call.
//
//   - CALLEE BODY (tryProveEnsureByParamContract): inside the function, a call `f(...)` through the
//     contracted parameter may ASSUME the parameter contract's predicate for its result, exactly as a
//     direct call assumes the callee's own `ensure`. This is what lets `apply`'s `ensure result >= 0`
//     discharge from `return f(x)` when `f` is declared `ensures(f, result >= 0)`.
//
// Soundness: the assumption inside the body is discharged at every call site by the entailment check,
// so it is never a free lunch. The entailment proof reuses the bounded-linear range machinery
// (rangeFromEnsureResult ∪ rangeEntailsConstraint); anything outside that fragment declines (no false
// PROVEN), and a passed function with NO ensure clause yields an empty range that entails nothing,
// correctly erroring on a non-trivial predicate.

// expandHigherOrderContracts lifts recognized `ensures(f, pred)` spec-call clauses out of every
// function's Requires slice into structured ParamContracts. Runs once, after expandUsesContracts and
// before body analysis, so the param contracts are available to both the call-site check and the
// callee-body assumption.
func (a *Analyzer) expandHigherOrderContracts(decls []scopedDecl) {
	for _, sd := range decls {
		fn, ok := sd.Decl.(*ast.FuncDecl)
		if !ok || fn == nil || fn.IsContract || len(fn.Requires) == 0 {
			continue
		}
		a.liftParamContracts(fn)
	}
}

// liftParamContracts scans fn.Requires for the `ensures(<param>, <pred>)` form, moves each matching
// clause into fn.ParamContracts, and rebuilds Requires (and the parallel RequiresProofs) without them.
func (a *Analyzer) liftParamContracts(fn *ast.FuncDecl) {
	keptReq := fn.Requires[:0:0]
	keptProofs := make([]*ast.ProofBlockStmt, 0, len(fn.RequiresProofs))
	for i, req := range fn.Requires {
		paramName, pred, ok := matchEnsuresSpecCall(req)
		if ok {
			if !a.funcParamCarriesFuncType(fn, paramName) {
				a.errorf(req.Pos(), "ensures(%s, ...) names %q, which is not a function-typed parameter of %q", paramName, paramName, fn.Name)
				continue
			}
			if pred == nil {
				a.errorf(req.Pos(), "ensures(%s, ...) needs a predicate over `result`", paramName)
				continue
			}
			fn.ParamContracts = append(fn.ParamContracts, ast.ParamContract{Position: req.Pos(), Param: paramName, Pred: pred})
			continue
		}
		keptReq = append(keptReq, req)
		keptProofs = append(keptProofs, proofAt(fn.RequiresProofs, i))
	}
	fn.Requires = keptReq
	if len(keptProofs) == 0 {
		fn.RequiresProofs = nil
	} else {
		fn.RequiresProofs = keptProofs
	}
}

// matchEnsuresSpecCall recognizes the `ensures(f, pred)` spec-call form and returns the parameter name
// and the predicate expression. Anything else returns ok=false.
func matchEnsuresSpecCall(expr ast.Expr) (param string, pred ast.Expr, ok bool) {
	for {
		paren, isParen := expr.(*ast.ParenExpr)
		if !isParen {
			break
		}
		expr = paren.Inner
	}
	call, isCall := expr.(*ast.CallExpr)
	if !isCall || call == nil {
		return "", nil, false
	}
	ident, isIdent := call.Func.(*ast.Ident)
	if !isIdent || ident == nil || ident.Name != "ensures" {
		return "", nil, false
	}
	if len(call.Args) != 2 {
		return "", nil, false
	}
	subj, isSubjIdent := call.Args[0].(*ast.Ident)
	if !isSubjIdent || subj == nil {
		return "", nil, false
	}
	return subj.Name, call.Args[1], true
}

// funcParamCarriesFuncType reports whether the named parameter of fn is declared with a function type.
func (a *Analyzer) funcParamCarriesFuncType(fn *ast.FuncDecl, name string) bool {
	for _, p := range fn.Params {
		if p.Name != name {
			continue
		}
		_, ok := p.Type.(*ast.FuncTypeExpr)
		return ok
	}
	return false
}

// paramContractFor returns the contract carried by parameter `name` of fn, if any.
func paramContractFor(fn *ast.FuncDecl, name string) (ast.ParamContract, bool) {
	if fn == nil {
		return ast.ParamContract{}, false
	}
	for _, pc := range fn.ParamContracts {
		if pc.Param == name {
			return pc, true
		}
	}
	return ast.ParamContract{}, false
}

// checkHigherOrderParamContracts enforces, at a call site, that every concrete NAMED function passed
// for a contracted function-typed parameter satisfies that parameter's postcondition contract.
func (a *Analyzer) checkHigherOrderParamContracts(call *ast.CallExpr, args []ast.Expr) {
	// Resolve the call target to an actual named FUNCTION (not a parameter symbol that merely shadows a
	// FuncDecl Node). A call `f(x)` through a function-typed parameter must NOT resolve here — its
	// target is the param, which carries no ParamContracts of its own.
	decl, ok := a.resolveCalledNamedFunc(call)
	if !ok || decl == nil || len(decl.ParamContracts) == 0 {
		return
	}
	for i, param := range decl.Params {
		pc, ok := paramContractFor(decl, param.Name)
		if !ok {
			continue
		}
		if i >= len(args) || args[i] == nil {
			continue
		}
		if passed, ok := a.resolvePassedNamedFunc(args[i]); ok && passed != nil {
			if !a.passedFuncSatisfiesParamContract(passed, pc) {
				a.errorf(args[i].Pos(), "function %q passed for parameter %q of %q does not satisfy its contract `ensures(%s, ...)`: its declared `ensure` does not entail the required postcondition", passed.Name, param.Name, decl.Name, param.Name)
			}
			continue
		}
		if lambda, ok := unwrapLambdaExpr(args[i]); ok && lambda != nil {
			// A lambda argument: infer the postcondition range from its body and prove it entails the
			// parameter contract's predicate, exactly as a named function's declared `ensure` would.
			// If the body's result is outside the decidable fragment, the inference declines (no range)
			// and we error — fail closed, never a false PROVEN.
			if !a.lambdaSatisfiesParamContract(lambda, pc) {
				a.errorf(args[i].Pos(), "lambda passed for parameter %q of %q does not satisfy its contract `ensures(%s, ...)`: its result is not provably within the required postcondition", param.Name, decl.Name, param.Name)
			}
			continue
		}
		// Not a concrete named function or lambda (forwarded param / arbitrary expression): decline
		// soundly. The contract cannot be relied on for this call; the body's assumption is discharged
		// only by call sites that DO pass a checkable function, so an un-checkable site is conservative —
		// it provides no guarantee, and the callee's own `ensure` still gets the debug runtime check.
	}
}

// resolveCalledNamedFunc returns the FuncDecl a call directly targets BY NAME, but only when the
// identifier resolves to an actual function symbol (SymbolFunc) — not a function-typed parameter whose
// symbol Node happens to point at the enclosing FuncDecl.
func (a *Analyzer) resolveCalledNamedFunc(call *ast.CallExpr) (*ast.FuncDecl, bool) {
	if call == nil {
		return nil, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil {
		return nil, false
	}
	return a.resolveNamedFuncSymbol(ident.Name)
}

// resolvePassedNamedFunc resolves an argument expression to the FuncDecl it names, for the
// unambiguous direct-by-name form (a bare identifier resolving to a function symbol).
func (a *Analyzer) resolvePassedNamedFunc(arg ast.Expr) (*ast.FuncDecl, bool) {
	for {
		paren, ok := arg.(*ast.ParenExpr)
		if !ok {
			break
		}
		arg = paren.Inner
	}
	ident, ok := arg.(*ast.Ident)
	if !ok || ident == nil {
		return nil, false
	}
	return a.resolveNamedFuncSymbol(ident.Name)
}

// resolveNamedFuncSymbol looks up a name and returns its FuncDecl only when it is a function symbol
// (not a parameter/local binding that shadows or shares a Node with a FuncDecl).
func (a *Analyzer) resolveNamedFuncSymbol(name string) (*ast.FuncDecl, bool) {
	if a == nil || a.currentScope == nil {
		return nil, false
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil || sym.Kind != SymbolFunc {
		return nil, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	return decl, ok && decl != nil
}

// passedFuncSatisfiesParamContract proves that the passed function's declared `ensure` postconditions
// entail the parameter contract's predicate, using the same bounded-linear range machinery as the
// caller-side return-refinement seed. A passed function with no relevant ensure yields an empty range
// that cannot entail a non-trivial predicate — correctly failing.
func (a *Analyzer) passedFuncSatisfiesParamContract(passed *ast.FuncDecl, pc ast.ParamContract) bool {
	if passed == nil || pc.Pred == nil {
		return false
	}
	required := a.paramContractConstraints(pc)
	if len(required) == 0 {
		// The predicate is outside the decidable fragment: we cannot prove the entailment, so we cannot
		// silently accept it. Decline = error (fail closed).
		return false
	}
	provided, ok := a.rangeFromEnsureResult(passed.EnsureValues, nil)
	if !ok {
		return false
	}
	for _, k := range required {
		if !rangeEntailsConstraint(provided, k) {
			return false
		}
	}
	return true
}

// paramContractConstraints reduces a parameter contract's predicate (over `result`) to the set of
// `result OP const` constraints it imposes, in the decidable bounded-linear fragment.
func (a *Analyzer) paramContractConstraints(pc ast.ParamContract) []lawConstraint {
	var constraints []lawConstraint
	a.collectResultConstraints(pc.Pred, nil, &constraints)
	return constraints
}

// tryProveEnsureByParamContract discharges one of the enclosing function's `ensure` clauses from a
// return value of the form `f(...)`, where `f` is a contracted function-typed parameter: the call's
// result is guaranteed (by the param contract, itself discharged at every call site) to satisfy the
// contract predicate, so its implied range entails the ensure clause.
func (a *Analyzer) tryProveEnsureByParamContract(clause ast.Expr, call *ast.CallExpr) bool {
	if a == nil || a.currentFuncDecl == nil || clause == nil || call == nil {
		return false
	}
	pc, ok := a.callTargetParamContract(call)
	if !ok {
		return false
	}
	resultRange := numRange{}
	for _, k := range a.paramContractConstraints(pc) {
		resultRange = resultRange.intersect(constraintToRange(k))
	}
	var goal []lawConstraint
	a.collectResultConstraints(clause, nil, &goal)
	if len(goal) == 0 {
		return false
	}
	for _, k := range goal {
		if !rangeEntailsConstraint(resultRange, k) {
			return false
		}
	}
	return true
}

// lambdaSatisfiesParamContract proves that a lambda argument's inferred postcondition (the integer
// range its result is guaranteed to lie in) entails the parameter contract's predicate. It mirrors
// passedFuncSatisfiesParamContract for the named-function case, but the "provided" range comes from
// analyzing the lambda's own body rather than a declared `ensure`. Fail-closed: if the body result is
// outside the decidable bounded-linear fragment, lambdaResultRange returns ok=false and we decline
// (which the caller turns into an error), so an unverifiable lambda is never silently accepted.
func (a *Analyzer) lambdaSatisfiesParamContract(lambda *ast.LambdaExpr, pc ast.ParamContract) bool {
	if lambda == nil || pc.Pred == nil {
		return false
	}
	required := a.paramContractConstraints(pc)
	if len(required) == 0 {
		// Predicate outside the decidable fragment: cannot prove, must decline.
		return false
	}
	provided, ok := a.lambdaResultRange(lambda)
	if !ok {
		return false
	}
	for _, k := range required {
		if !rangeEntailsConstraint(provided, k) {
			return false
		}
	}
	return true
}

// lambdaResultRange computes the integer interval the lambda's result is guaranteed to lie in, over
// every execution path. For an expression-body lambda it is the range of the body expression; for a
// block-body lambda it is the JOIN (union) of the ranges of every `return` value (a path whose value
// cannot be ranged makes the whole lambda undecidable, since that path could violate the contract).
// Returns ok=false whenever any contributing value is outside the supported fragment — fail-closed.
func (a *Analyzer) lambdaResultRange(lambda *ast.LambdaExpr) (numRange, bool) {
	if lambda == nil {
		return numRange{}, false
	}
	if lambda.BodyExpr != nil {
		return a.exprResultRange(lambda.BodyExpr, nil)
	}
	var returns []ast.Expr
	collectReturnValues(lambda.Body, &returns)
	if len(returns) == 0 {
		return numRange{}, false
	}
	var joined numRange
	for i, rv := range returns {
		r, ok := a.exprResultRange(rv, nil)
		if !ok {
			return numRange{}, false
		}
		if i == 0 {
			joined = r
		} else {
			joined = joined.join(r)
		}
	}
	return joined, true
}

// collectReturnValues gathers the value expressions of every `return EXPR` in a statement block,
// recursing into the branches of if/else and the bodies of nested blocks. A bare `return` (no value)
// or any control construct that could yield a value through an unmodeled path is left for the caller's
// fail-closed handling — an unrecognized statement that produces no return value simply contributes
// nothing, but the conservative result-range join below still requires EVERY collected return to be
// rangeable, so a partially-modeled body declines.
func collectReturnValues(stmts []ast.Stmt, out *[]ast.Expr) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			if s.Value != nil {
				*out = append(*out, s.Value)
			}
		case *ast.IfStmt:
			collectReturnValues(s.Then, out)
			for _, elif := range s.Elifs {
				collectReturnValues(elif.Body, out)
			}
			collectReturnValues(s.Else, out)
		}
	}
}

// exprResultRange computes the integer interval an expression evaluates within, given the guard facts
// in scope (a map of immutable-identifier name -> narrowed range, accumulated from enclosing
// conditional tests). Supports the bounded fragment: integer literals, guarded identifiers, and
// conditionals (whose value is the JOIN of the two arms, each evaluated under the guard the test
// establishes). Returns ok=false for anything outside the fragment — fail-closed.
func (a *Analyzer) exprResultRange(expr ast.Expr, guards map[string]numRange) (numRange, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.exprResultRange(n.Inner, guards)
	case *ast.Ident:
		if guards != nil {
			if r, ok := guards[n.Name]; ok {
				return r, true
			}
		}
		return numRange{}, false
	case *ast.TernaryExpr:
		// `Value if Cond else Alt`: the Value arm is taken when Cond is truthy (narrow by the positive
		// guard), the Alt arm when falsy (narrow by the negated guard). The result is the join of both.
		thenName, thenFact := a.condGuardFact(n.Cond, true)
		elseName, elseFact := a.condGuardFact(n.Cond, false)
		thenGuards := mergeGuard(guards, thenName, thenFact)
		elseGuards := mergeGuard(guards, elseName, elseFact)
		vr, vok := a.exprResultRange(n.Value, thenGuards)
		ar, aok := a.exprResultRange(n.Alt, elseGuards)
		if !vok || !aok {
			return numRange{}, false
		}
		return vr.join(ar), true
	default:
		if c, ok := a.constIntValue(expr); ok {
			return numRange{loKnown: true, lo: c, hiKnown: true, hi: c}, true
		}
		return numRange{}, false
	}
}

// condGuardFact reduces a single `ident OP const` comparison condition to the (name, range) fact it
// establishes on its truthy or falsy branch. Returns name="" when the condition is not of that shape.
func (a *Analyzer) condGuardFact(cond ast.Expr, truthy bool) (string, numRange) {
	for {
		paren, ok := cond.(*ast.ParenExpr)
		if !ok {
			break
		}
		cond = paren.Inner
	}
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin == nil {
		return "", numRange{}
	}
	op := bin.Op
	var name string
	var operand ast.Expr
	if id, isID := bin.Left.(*ast.Ident); isID && id != nil {
		name, operand = id.Name, bin.Right
	} else if id, isID := bin.Right.(*ast.Ident); isID && id != nil {
		name, operand, op = id.Name, bin.Left, flipComparison(op)
	} else {
		return "", numRange{}
	}
	c, ok := a.constIntValue(operand)
	if !ok {
		return "", numRange{}
	}
	if !truthy {
		op = negateComparison(op)
	}
	switch op {
	case lexer.TOKEN_GT:
		return name, numRange{loKnown: true, lo: c + 1}
	case lexer.TOKEN_GTEQ:
		return name, numRange{loKnown: true, lo: c}
	case lexer.TOKEN_LT:
		return name, numRange{hiKnown: true, hi: c - 1}
	case lexer.TOKEN_LTEQ:
		return name, numRange{hiKnown: true, hi: c}
	case lexer.TOKEN_EQEQ:
		return name, numRange{loKnown: true, lo: c, hiKnown: true, hi: c}
	default:
		return "", numRange{}
	}
}

// mergeGuard returns a copy of base with the (name,fact) intersected in. A name="" fact (the condition
// was not a recognized comparison) leaves the guards unchanged.
func mergeGuard(base map[string]numRange, name string, fact numRange) map[string]numRange {
	if name == "" {
		return base
	}
	out := make(map[string]numRange, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	out[name] = out[name].intersect(fact)
	return out
}

// callTargetParamContract returns the parameter contract for a call whose target is a contracted
// function-typed parameter of the function currently being analyzed.
func (a *Analyzer) callTargetParamContract(call *ast.CallExpr) (ast.ParamContract, bool) {
	if a == nil || a.currentFuncDecl == nil || call == nil {
		return ast.ParamContract{}, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil {
		return ast.ParamContract{}, false
	}
	return paramContractFor(a.currentFuncDecl, ident.Name)
}
