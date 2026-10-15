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
	return pureClassifierExprCalls(e, nil)
}

// pureClassifierExprCalls is pureClassifierExpr that also admits the calls pureCall accepts.
func pureClassifierExprCalls(e ast.Expr, pureCall func(*ast.CallExpr) bool) bool {
	switch n := e.(type) {
	case *ast.Ident, *ast.StringLit, *ast.BoolLit, *ast.IntLit:
		return true
	case *ast.CallExpr:
		return n != nil && pureCall != nil && pureCall(n)
	case *ast.ParenExpr:
		return pureClassifierExprCalls(n.Inner, pureCall)
	case *ast.BinaryExpr:
		return n.LoweredCall == nil && pureClassifierExprCalls(n.Left, pureCall) && pureClassifierExprCalls(n.Right, pureCall)
	case *ast.MatchExpr:
		if n.Store != nil || !pureClassifierExprCalls(n.Value, pureCall) {
			return false
		}
		for _, arm := range n.Arms {
			if arm.Guard != nil || !pureClassifierPattern(arm.Pattern) || len(arm.Body) != 1 {
				return false
			}
			tail, ok := arm.Body[0].(*ast.ExprStmt)
			if !ok || !pureClassifierExprCalls(tail.Expr, pureCall) {
				return false
			}
		}
		return true
	}
	return false
}

// pureClassifierReturnValue accepts a write-free return value: a pure classifier
// expression, or a tuple of them (`("string", "i32")`).
func pureClassifierReturnValue(value ast.Expr, pureCall func(*ast.CallExpr) bool) bool {
	if tuple, ok := stripOptimizationParens(value).(*ast.TupleExpr); ok {
		if len(tuple.Elems) == 0 {
			return false
		}
		for _, elem := range tuple.Elems {
			if !pureClassifierExprCalls(elem, pureCall) {
				return false
			}
		}
		return true
	}
	return value != nil && pureClassifierExprCalls(value, pureCall)
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

func classifierStringControlFlowBody(body []ast.Stmt, allowEmpty bool, pureCall func(*ast.CallExpr) bool) bool {
	if len(body) == 0 {
		return allowEmpty
	}
	for _, stmt := range body {
		switch n := stmt.(type) {
		case *ast.ReturnStmt:
			// The returned value only has to be write-free: frame stability is about
			// what runs BETWEEN the guards and the return, not about the result's shape.
			if !pureClassifierReturnValue(n.Value, pureCall) {
				return false
			}
		case *ast.VarDeclStmt:
			// An immutable local bound to a write-free value writes nothing.
			if n.Mutable || n.Owner != nil || n.Value == nil || !pureClassifierExprCalls(n.Value, pureCall) {
				return false
			}
		case *ast.IfStmt:
			if !pureClassifierExprCalls(n.Cond, pureCall) || !classifierStringControlFlowBody(n.Then, false, pureCall) || !classifierStringControlFlowBody(n.Else, true, pureCall) {
				return false
			}
			for _, clause := range n.Elifs {
				if !pureClassifierExprCalls(clause.Cond, pureCall) || !classifierStringControlFlowBody(clause.Body, false, pureCall) {
					return false
				}
			}
		case *ast.CanStmt:
			if n.HandlerName != "" || len(n.HandlerArgs) != 0 || n.SuppressPermissionInference || n.As != "" || len(n.Permissions) == 0 || !classifierStringControlFlowBody(n.Body, false, pureCall) {
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
	// A call keeps the frame stable only when the callee is a transitively pure single-return
	// function over immutable scalar/view formals (functionPureEquationShape): it cannot write.
	pureCall := func(call *ast.CallExpr) bool {
		if a == nil {
			return false
		}
		callee, ok := a.classifierFrameCallDecl(call)
		if !ok || callee == nil || !(a.functionPureEquationShape(callee) || a.classifierCalleeWriteFree(callee)) {
			return false
		}
		for _, arg := range call.Args {
			if !pureClassifierExpr(arg) {
				return false
			}
		}
		return true
	}
	if !classifierStringControlFlowBody(decl.Body, false, pureCall) {
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
	param, ok := tr.stringIDTerm(left, env)
	if !ok {
		return "", false
	}
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
	// Symbol-free precheck: the subject must be a name. Its identity term (a formal, a bound
	// callee formal, or a call-bound local) is created only after the arm shape validates.
	if _, ok := stripOptimizationParens(n.Value).(*ast.Ident); !ok {
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

// classifierCalleeWriteFree reports a callee that cannot write anything a caller's
// borrowed view could observe: its analysis has finished (effect summary final),
// every formal is an immutable scalar or view value (no mutable binding, no mutable
// reference, no pointer or function value through which to reach caller memory),
// it has no type/region/permission parameters, and its declared plus inferred
// effects are Abort.Panic at most (no Global.Write, Memory.*, Unsafe.*).
func (a *Analyzer) classifierCalleeWriteFree(decl *ast.FuncDecl) bool {
	if a == nil || decl == nil || decl.IsLemma {
		return false
	}
	sym, ok := a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return false
	}
	fn, ok := sym.Type.(*FuncType)
	if !ok || fn == nil || !fn.ReturnBorrowedOwnerRefsKnown || len(fn.Params) != len(decl.Params) {
		return false
	}
	if len(fn.TypeParams) != 0 || len(fn.RegionParams) != 0 || len(fn.PermissionParams) != 0 || len(fn.GenericParams) != 0 ||
		len(fn.MutableGlobalPermissionRefs) != 0 || len(fn.MutableGlobalSourceNames) != 0 {
		return false
	}
	for _, permission := range fn.PermissionRefs {
		if permission.Name != "Abort" || permission.Member != "Panic" || len(permission.TypeArgs) != 0 || len(permission.Via) != 0 {
			return false
		}
	}
	for i, param := range decl.Params {
		if param.Mutable || a.paramIsMutable(param) {
			return false
		}
		t := fn.Params[i]
		if ref, isRef := t.(*RefType); isRef {
			if ref == nil || ref.Mutable {
				return false
			}
			t = ref.Elem
		}
		switch t.(type) {
		case *SViewType:
			continue
		}
		if !((IsNumericType(t) && !IsFloatType(t)) || IsBoolType(t)) {
			return false
		}
	}
	return true
}

// classifierFrameCallDecl resolves a call's direct callee WITHOUT relying on the analyzer having
// already visited the call: the frame gate inspects the whole body, including statements after the
// proof point whose qualified (`Mod::f`) or namespace-relative callees are rewritten to canonical
// identifiers only when analyzed. Read-only: the call node is not modified.
func (a *Analyzer) classifierFrameCallDecl(call *ast.CallExpr) (*ast.FuncDecl, bool) {
	if a == nil || call == nil {
		return nil, false
	}
	if decl, ok := a.resolveDirectCallFuncDecl(call); ok && decl != nil {
		return decl, true
	}
	var sym *Symbol
	if ident, isIdent := call.Func.(*ast.Ident); isIdent && ident != nil {
		if member, _, ok := a.enclosingNamespaceMember(ident.Name); ok {
			sym = member
		}
	} else if name, ok := qualifiedTypePathFromExpr(call.Func); ok && name != "" && a.globalScope != nil {
		if global, _, ok := a.lookupVisibleGlobal(name); ok {
			sym = global
		}
	}
	if sym == nil {
		return nil, false
	}
	if _, isFunc := sym.Type.(*FuncType); !isFunc {
		return nil, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	return decl, ok && decl != nil
}
