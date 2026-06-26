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
	subst := map[string]ast.Expr{}
	if stmt.Value != nil {
		subst[stmt.Name] = stmt.Value
	}
	a.dischargeWherePredicate(rt.Predicate, subst, stmt.Pos(), "local where refinement")
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
		if counterexample != "" {
			a.proofLint(pos, "%s could not be proven statically; it can fail when %s", subject, counterexample)
		} else {
			a.proofLint(pos, "%s could not be proven statically", subject)
		}
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
