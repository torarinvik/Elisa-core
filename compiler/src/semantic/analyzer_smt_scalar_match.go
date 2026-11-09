package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

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
		if i != len(n.Arms)-1 && !lemmaScalarMatchPattern(arm.Pattern) && !scalarCharOrRangePattern(arm.Pattern) {
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
		if !lemmaScalarMatchPattern(p) && !scalarCharOrRangePattern(p) {
			return "", false
		}
		return tr.boolTerm(&ast.BinaryExpr{Position: value.Pos(), Op: lexer.TOKEN_EQEQ, Left: value, Right: p.Value}, env)
	case *ast.MatchStringLiteralPattern:
		return tr.classifierStringEquality(value, &ast.StringLit{Value: p.Value}, env)
	case *ast.MatchRangePattern:
		// `lo..=hi` / `lo..<hi` over integer or char literal bounds (scalarCharOrRangePattern).
		if !scalarCharOrRangePattern(p) {
			return "", false
		}
		v, ok := tr.termEnv(value, env)
		if !ok {
			return "", false
		}
		lo, ok := tr.termEnv(p.Lo, env)
		if !ok {
			return "", false
		}
		hi, ok := tr.termEnv(p.Hi, env)
		if !ok {
			return "", false
		}
		if p.Inclusive {
			return "(and (<= " + lo + " " + v + ") (<= " + v + " " + hi + "))", true
		}
		return "(and (<= " + lo + " " + v + ") (< " + v + " " + hi + "))", true
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
	case *ast.MembershipRangeExpr:
		return scalarMatchProofExpr(n.Start) && scalarMatchProofExpr(n.End)
	case *ast.ListLitExpr:
		// Membership normalizes its candidates to a list, including a single range.
		// No owner, dictionary keys, spreads, or calls may enter a match equation.
		if n.Keys != nil || n.Owner != nil {
			return false
		}
		for _, spread := range n.Spreads {
			if spread {
				return false
			}
		}
		for _, elem := range n.Elems {
			if !scalarMatchProofExpr(elem) {
				return false
			}
		}
		return true
	case *ast.Ident, *ast.IntLit, *ast.BoolLit, *ast.StringLit, *ast.CharLit:
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

// scalarCharOrRangePattern accepts the scalar arm patterns lemmaScalarMatchPattern leaves out: a char
// literal (`'e'`), an integer/char range (`'0'..='9'`, `0..<10`), and or-patterns of them.
func scalarCharOrRangePattern(pattern ast.MatchPattern) bool {
	literalBound := func(e ast.Expr) bool {
		switch stripOptimizationParens(e).(type) {
		case *ast.IntLit, *ast.CharLit:
			return true
		}
		return false
	}
	switch p := pattern.(type) {
	case *ast.MatchLiteralPattern:
		return p != nil && !p.Pinned && literalBound(p.Value)
	case *ast.MatchRangePattern:
		return p != nil && p.Lo != nil && p.Hi != nil && literalBound(p.Lo) && literalBound(p.Hi)
	case *ast.MatchOrPattern:
		if p == nil || len(p.Options) == 0 {
			return false
		}
		for _, option := range p.Options {
			if !lemmaScalarMatchPattern(option) && !scalarCharOrRangePattern(option) {
				return false
			}
		}
		return true
	}
	return false
}

// matchArmValueReadOnly accepts an arm value that reads but never writes: names, literals,
// non-overloaded operators, ternaries, non-optional field reads, plain indexing, numeric casts,
// nested guard-free matches over such values, and calls to callees classified write-free. Such a
// value may be translated for every arm (only the selected arm's term survives the ite fold), since
// translating it neither duplicates an effect nor reads state a sibling arm could change.
func (a *Analyzer) matchArmValueReadOnly(e ast.Expr, depth int) bool {
	if a == nil || e == nil || depth > 64 {
		return false
	}
	switch n := e.(type) {
	case *ast.Ident, *ast.IntLit, *ast.BoolLit, *ast.CharLit, *ast.StringLit:
		return true
	case *ast.ParenExpr:
		return a.matchArmValueReadOnly(n.Inner, depth+1)
	case *ast.UnaryExpr:
		return n.LoweredCall == nil && a.matchArmValueReadOnly(n.Operand, depth+1)
	case *ast.BinaryExpr:
		return n.LoweredCall == nil && a.matchArmValueReadOnly(n.Left, depth+1) && a.matchArmValueReadOnly(n.Right, depth+1)
	case *ast.TernaryExpr:
		return a.matchArmValueReadOnly(n.Cond, depth+1) && a.matchArmValueReadOnly(n.Value, depth+1) && a.matchArmValueReadOnly(n.Alt, depth+1)
	case *ast.FieldExpr:
		return !n.Safe && a.matchArmValueReadOnly(n.Object, depth+1)
	case *ast.IndexExpr:
		return n.Index2 == nil && n.Fallback == nil && n.AsOverlayCall == nil && a.matchArmValueReadOnly(n.Object, depth+1) && a.matchArmValueReadOnly(n.Index, depth+1)
	case *ast.CastExpr:
		return isPureNumericValueCast(n) && a.matchArmValueReadOnly(n.Operand, depth+1)
	case *ast.MatchExpr:
		if n.Store != nil || !a.matchArmValueReadOnly(n.Value, depth+1) {
			return false
		}
		for _, arm := range n.Arms {
			if arm.Guard != nil || len(arm.Body) != 1 {
				return false
			}
			tail, ok := arm.Body[0].(*ast.ExprStmt)
			if !ok || !a.matchArmValueReadOnly(tail.Expr, depth+1) {
				return false
			}
		}
		return true
	case *ast.CallExpr:
		callee, ok := a.classifierFrameCallDecl(n)
		if !ok || callee == nil || !(a.functionPureEquationShape(callee) || a.classifierCalleeWriteFree(callee)) {
			return false
		}
		for _, arg := range n.Args {
			if !a.matchArmValueReadOnly(arg, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}
