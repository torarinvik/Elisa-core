package semantic

import (
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type smtStringParameterKey struct {
	function   *ast.FuncDecl
	paramIndex int
}

func pureClassifierPattern(pattern ast.MatchPattern) bool {
	if pattern == nil {
		return false
	}
	switch n := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return n != nil
	case *ast.MatchStringLiteralPattern:
		return n != nil && !strings.ContainsRune(n.Value, 0)
	case *ast.MatchLiteralPattern:
		if n == nil || n.Pinned {
			return false
		}
		switch stripOptimizationParens(n.Value).(type) {
		case *ast.StringLit, *ast.BoolLit, *ast.IntLit:
			return true
		}
	}
	return false
}

// Only expression-only classifiers qualify: an immutable view binding does
// not by itself guarantee that its backing bytes survive mutations or calls.
func pureClassifierExpr(e ast.Expr) bool {
	switch n := e.(type) {
	case *ast.Ident, *ast.StringLit, *ast.BoolLit, *ast.IntLit:
		return true
	case *ast.ParenExpr:
		return pureClassifierExpr(n.Inner)
	case *ast.BinaryExpr:
		return n.LoweredCall == nil && pureClassifierExpr(n.Left) && pureClassifierExpr(n.Right)
	case *ast.MatchExpr:
		if n.Store != nil || !pureClassifierExpr(n.Value) {
			return false
		}
		for _, arm := range n.Arms {
			if arm.Guard != nil || !pureClassifierPattern(arm.Pattern) || len(arm.Body) != 1 {
				return false
			}
			tail, ok := arm.Body[0].(*ast.ExprStmt)
			if !ok || !pureClassifierExpr(tail.Expr) {
				return false
			}
		}
		return true
	}
	return false
}

func pureClassifierBody(body []ast.Stmt) bool {
	if len(body) != 1 {
		return false
	}
	switch n := body[0].(type) {
	case *ast.ReturnStmt:
		return pureClassifierExpr(n.Value)
	case *ast.CanStmt:
		return n.HandlerName == "" && len(n.HandlerArgs) == 0 && pureClassifierBody(n.Body)
	}
	return false
}

func classifierStringControlFlowBody(body []ast.Stmt, allowEmpty bool) bool {
	if len(body) == 0 {
		return allowEmpty
	}
	for _, stmt := range body {
		switch n := stmt.(type) {
		case *ast.ReturnStmt:
			if _, ok := stripOptimizationParens(n.Value).(*ast.BoolLit); !ok {
				return false
			}
		case *ast.IfStmt:
			if !pureClassifierExpr(n.Cond) || !classifierStringControlFlowBody(n.Then, false) || !classifierStringControlFlowBody(n.Else, true) {
				return false
			}
			for _, clause := range n.Elifs {
				if !pureClassifierExpr(clause.Cond) || !classifierStringControlFlowBody(clause.Body, false) {
					return false
				}
			}
		case *ast.CanStmt:
			if n.HandlerName != "" || len(n.HandlerArgs) != 0 || n.SuppressPermissionInference || n.As != "" || len(n.Permissions) == 0 || !classifierStringControlFlowBody(n.Body, false) {
				return false
			}
			for _, permission := range n.Permissions {
				if permission.Name != "Abort" || permission.Member != "Panic" || len(permission.TypeArgs) != 0 || len(permission.Via) != 0 {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

// classifierStringFrameStable permits literal string facts to cross multiple
// return paths when the function cannot mutate the borrowed string's backing
// storage. The fallback accepts only Abort.Panic: functions with any other
// capability may write through aliases or mutate shared state between reads.
func classifierStringFrameStable(a *Analyzer, decl *ast.FuncDecl) bool {
	if decl == nil {
		return false
	}
	if pureClassifierBody(decl.Body) {
		return true
	}
	if !classifierStringControlFlowBody(decl.Body, false) {
		return false
	}
	isPanicOnly := func(permissions []ast.PermissionRef) bool {
		for _, permission := range permissions {
			if permission.Name != "Abort" || permission.Member != "Panic" || len(permission.TypeArgs) != 0 || len(permission.Via) != 0 {
				return false
			}
		}
		return true
	}
	if !isPanicOnly(decl.Permissions) {
		return false
	}
	return a == nil || isPanicOnly(a.currentFunctionUsedPermissionRefs)
}

func (tr *smtTranslator) classifierStringParameter(expr ast.Expr, env map[string]string) (*ast.Ident, smtStringParameterKey, bool) {
	id, ok := stripOptimizationParens(expr).(*ast.Ident)
	if !ok || tr.a == nil || tr.a.currentScope == nil || tr.a.currentFuncDecl == nil || tr.calleeBodyDepth != 0 {
		return nil, smtStringParameterKey{}, false
	}
	if !classifierStringFrameStable(tr.a, tr.a.currentFuncDecl) {
		return nil, smtStringParameterKey{}, false
	}
	if _, bound := env[id.Name]; bound {
		return nil, smtStringParameterKey{}, false
	}
	if _, bound := env["\x00string:"+id.Name]; bound {
		return nil, smtStringParameterKey{}, false
	}
	paramIndex := -1
	for index, param := range tr.a.currentFuncDecl.Params {
		if param.Name == id.Name {
			if tr.a.paramIsMutable(param) {
				return nil, smtStringParameterKey{}, false
			}
			paramIndex = index
			break
		}
	}
	if paramIndex < 0 {
		return nil, smtStringParameterKey{}, false
	}
	sym, found := tr.a.currentScope.Lookup(id.Name)
	if !found || sym == nil || sym.Mutable || sym.Kind != SymbolParam || sym.Node != tr.a.currentFuncDecl || sym.ParamIndex != paramIndex || !isStringViewType(sym.Type) {
		return nil, smtStringParameterKey{}, false
	}
	return id, smtStringParameterKey{function: tr.a.currentFuncDecl, paramIndex: paramIndex}, true
}

func (tr *smtTranslator) classifierStringParameterInt(key smtStringParameterKey) string {
	if tr.stringParamInts == nil {
		tr.stringParamInts = map[smtStringParameterKey]string{}
	}
	if term, ok := tr.stringParamInts[key]; ok {
		return term
	}
	term := tr.freshAux(false)
	tr.stringParamInts[key] = term
	return term
}

func (tr *smtTranslator) classifierStringLiteralInt(value string) int64 {
	if tr.stringLiteralInts == nil {
		tr.stringLiteralInts = map[string]int64{}
	}
	if id, ok := tr.stringLiteralInts[value]; ok {
		return id
	}
	tr.nextStringLiteralInt++
	id := tr.nextStringLiteralInt
	tr.stringLiteralInts[value] = id
	return id
}

// String equality against a literal uses an injective query-local integer ID.
// The parameter's free Int overapproximates all possible string values: every
// known literal has its own ID, while unknown strings may use any other value.
// This preserves exact known-literal exclusivity without pairwise constraints.
func (tr *smtTranslator) classifierStringEquality(left, right ast.Expr, env map[string]string) (string, bool) {
	left = stripOptimizationParens(left)
	right = stripOptimizationParens(right)
	if _, ok := right.(*ast.StringLit); !ok {
		if _, ok := left.(*ast.StringLit); ok {
			left, right = right, left
		} else {
			return "", false
		}
	}
	lit, ok := right.(*ast.StringLit)
	if !ok || strings.ContainsRune(lit.Value, 0) {
		return "", false
	}
	_, key, ok := tr.classifierStringParameter(left, env)
	if !ok {
		return "", false
	}
	param := tr.classifierStringParameterInt(key)
	literal := tr.classifierStringLiteralInt(lit.Value)
	return "(= " + param + " " + smtInt(literal) + ")", true
}

func (tr *smtTranslator) booleanMatchTerm(n *ast.MatchExpr, env map[string]string) (string, bool) {
	if term, ok := tr.scalarMatchTerm(n, env, tr.boolTerm); ok {
		return term, true
	}

	if n == nil || n.Store != nil || len(n.Arms) == 0 {
		return "", false
	}
	if _, _, ok := tr.classifierStringParameter(n.Value, env); !ok {
		return "", false
	}
	if _, ok := n.Arms[len(n.Arms)-1].Pattern.(*ast.MatchWildcardPattern); !ok {
		return "", false
	}
	// Validate the full shape before creating any SMT symbols. Restrict the arm
	// values to literals so translation cannot duplicate calls or mutable reads.
	for index, arm := range n.Arms {
		if arm.Guard != nil || len(arm.Body) != 1 {
			return "", false
		}
		tail, ok := arm.Body[0].(*ast.ExprStmt)
		if !ok {
			return "", false
		}
		if _, ok := stripOptimizationParens(tail.Expr).(*ast.BoolLit); !ok {
			return "", false
		}
		if index == len(n.Arms)-1 {
			continue
		}
		pattern, ok := arm.Pattern.(*ast.MatchStringLiteralPattern)
		if !ok || strings.ContainsRune(pattern.Value, 0) {
			return "", false
		}
	}
	result := ""
	for index := len(n.Arms) - 1; index >= 0; index-- {
		arm := n.Arms[index]
		literal := stripOptimizationParens(arm.Body[0].(*ast.ExprStmt).Expr).(*ast.BoolLit)
		branch := "false"
		if literal.Value {
			branch = "true"
		}
		if index == len(n.Arms)-1 {
			result = branch
			continue
		}
		pattern := arm.Pattern.(*ast.MatchStringLiteralPattern)
		guard, ok := tr.classifierStringEquality(n.Value, &ast.StringLit{Value: pattern.Value}, env)
		if !ok {
			return "", false
		}
		result = "(ite " + guard + " " + branch + " " + result + ")"
	}
	return result, true
}

// scalarMatchTerm preserves ordered dispatch by folding arms into nested ite terms.
// Require an explicit default so an untranslated/missing case can never become a proof.
// Pure scalar matches share the translator's complete preflight, so equation
// eligibility cannot admit branch calls or side effects the SMT lowering declines.
func pureScalarMatchEquation(n *ast.MatchExpr) bool {
	if n == nil || n.Store != nil || len(n.Arms) == 0 {
		return false
	}
	if _, ok := n.Arms[len(n.Arms)-1].Pattern.(*ast.MatchWildcardPattern); !ok {
		return false
	}
	// Preflight the whole expression before lowering any branch. In particular,
	// callee contract axioms cannot be asserted outside their dispatch guard.
	if !scalarMatchProofExpr(n.Value) {
		return false
	}
	for i, arm := range n.Arms {
		if arm.Guard != nil || len(arm.Body) != 1 {
			return false
		}
		tail, ok := arm.Body[0].(*ast.ExprStmt)
		if !ok || !scalarMatchProofExpr(tail.Expr) {
			return false
		}
		if i != len(n.Arms)-1 && !lemmaScalarMatchPattern(arm.Pattern) {
			return false
		}
	}
	return true
}

func (tr *smtTranslator) scalarMatchTerm(n *ast.MatchExpr, env map[string]string, lower func(ast.Expr, map[string]string) (string, bool)) (string, bool) {
	if !pureScalarMatchEquation(n) {
		return "", false
	}
	result := ""
	for i := len(n.Arms) - 1; i >= 0; i-- {
		arm := n.Arms[i]
		if arm.Guard != nil || len(arm.Body) != 1 {
			return "", false
		}
		tail, ok := arm.Body[0].(*ast.ExprStmt)
		if !ok {
			return "", false
		}
		branch, ok := lower(tail.Expr, env)
		if !ok {
			return "", false
		}
		if i == len(n.Arms)-1 {
			result = branch
			continue
		}
		guard, ok := tr.scalarMatchGuard(n.Value, arm.Pattern, env)
		if !ok {
			return "", false
		}
		result = "(ite " + guard + " " + branch + " " + result + ")"
	}
	return result, true
}

func (tr *smtTranslator) scalarMatchGuard(value ast.Expr, pattern ast.MatchPattern, env map[string]string) (string, bool) {
	switch p := pattern.(type) {
	case *ast.MatchLiteralPattern:
		if p.Pinned {
			return "", false
		}
		if !lemmaScalarMatchPattern(p) {
			return "", false
		}
		return tr.boolTerm(&ast.BinaryExpr{Position: value.Pos(), Op: lexer.TOKEN_EQEQ, Left: value, Right: p.Value}, env)
	case *ast.MatchStringLiteralPattern:
		return tr.classifierStringEquality(value, &ast.StringLit{Value: p.Value}, env)
	case *ast.MatchOrPattern:
		var guards []string
		for _, option := range p.Options {
			guard, ok := tr.scalarMatchGuard(value, option, env)
			if !ok {
				return "", false
			}
			guards = append(guards, guard)
		}
		if len(guards) == 0 {
			return "", false
		}
		return "(or " + strings.Join(guards, " ") + ")", true
	}
	return "", false
}

func scalarMatchProofExpr(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.Ident, *ast.IntLit, *ast.BoolLit, *ast.StringLit:
		return true
	case *ast.ParenExpr:
		return scalarMatchProofExpr(n.Inner)
	case *ast.UnaryExpr:
		return scalarMatchProofExpr(n.Operand)
	case *ast.BinaryExpr:
		return n.LoweredCall == nil && scalarMatchProofExpr(n.Left) && scalarMatchProofExpr(n.Right)
	case *ast.TernaryExpr:
		return scalarMatchProofExpr(n.Cond) && scalarMatchProofExpr(n.Value) && scalarMatchProofExpr(n.Alt)
	}
	return false
}
