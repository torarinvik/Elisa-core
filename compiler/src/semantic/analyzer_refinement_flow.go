package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// numRange is a closed integer interval fact: lo <= value <= hi, with either bound optionally
// unknown (open). It is the flow-prover's abstraction of what a branch condition tells us about an
// immutable integer variable (docs/85 1d-2).
type numRange struct {
	loKnown bool
	lo      int64
	hiKnown bool
	hi      int64
}

// intersect tightens a range with another fact about the same variable (conjunction of conditions).
func (r numRange) intersect(o numRange) numRange {
	out := r
	if o.loKnown && (!out.loKnown || o.lo > out.lo) {
		out.loKnown, out.lo = true, o.lo
	}
	if o.hiKnown && (!out.hiKnown || o.hi < out.hi) {
		out.hiKnown, out.hi = true, o.hi
	}
	return out
}

// gatherNumericRangeRefinement records integer-bound facts for an IMMUTABLE identifier compared
// against a compile-time-constant in a (truthy) branch condition: `a > 5`, `a >= 0`, `a < n`,
// `5 < a`, `a == k`, etc. Immutable-only so the fact holds for the whole branch with no
// invalidation. Called from applyConditionRefinementsInternal for comparison operators.
func (a *Analyzer) gatherNumericRangeRefinement(scope *Scope, n *ast.BinaryExpr, truthy bool) {
	if !truthy || scope == nil || n == nil {
		return
	}
	// Normalize to `ident OP const`. If the constant is on the left (`5 < a`), flip the operator.
	op := n.Op
	name, ok := immutableIntIdentName(a, scope, n.Left)
	var c int64
	var cok bool
	if ok {
		c, cok = a.constIntValue(n.Right)
	} else if name, ok = immutableIntIdentName(a, scope, n.Right); ok {
		c, cok = a.constIntValue(n.Left)
		op = flipComparison(op)
	}
	if !ok || !cok {
		return
	}
	var fact numRange
	switch op {
	case lexer.TOKEN_GT: // a > c  ⇒ a >= c+1
		fact = numRange{loKnown: true, lo: c + 1}
	case lexer.TOKEN_GTEQ: // a >= c
		fact = numRange{loKnown: true, lo: c}
	case lexer.TOKEN_LT: // a < c  ⇒ a <= c-1
		fact = numRange{hiKnown: true, hi: c - 1}
	case lexer.TOKEN_LTEQ: // a <= c
		fact = numRange{hiKnown: true, hi: c}
	case lexer.TOKEN_EQEQ: // a == c
		fact = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}
	default:
		return
	}
	if scope.rangeFacts == nil {
		scope.rangeFacts = map[string]numRange{}
	}
	scope.rangeFacts[name] = scope.rangeFacts[name].intersect(fact)
}

// immutableIntIdentName returns the name of `expr` when it is a bare identifier bound to an
// IMMUTABLE integer-typed variable (so a branch-condition fact about it cannot be invalidated by a
// later mutation). Mutable bindings return false — their facts would be unsound to carry.
func immutableIntIdentName(a *Analyzer, scope *Scope, expr ast.Expr) (string, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil {
		return "", false
	}
	sym, ok := scope.Lookup(ident.Name)
	if !ok || sym == nil || sym.Mutable {
		return "", false
	}
	if !IsNumericType(sym.Type) || IsFloatType(sym.Type) {
		return "", false
	}
	return ident.Name, true
}

// constIntValue extracts a compile-time integer constant from an expression.
func (a *Analyzer) constIntValue(expr ast.Expr) (int64, bool) {
	cv, ok := a.evalConstExpr(expr)
	if !ok || cv.Kind != ConstInt {
		return 0, false
	}
	return cv.Int, true
}

func flipComparison(op lexer.TokenKind) lexer.TokenKind {
	switch op {
	case lexer.TOKEN_GT:
		return lexer.TOKEN_LT
	case lexer.TOKEN_GTEQ:
		return lexer.TOKEN_LTEQ
	case lexer.TOKEN_LT:
		return lexer.TOKEN_GT
	case lexer.TOKEN_LTEQ:
		return lexer.TOKEN_GTEQ
	default:
		return op
	}
}

// lookupRangeFact walks the current scope chain for a known integer range about `name`.
func (a *Analyzer) lookupRangeFact(name string) (numRange, bool) {
	acc := numRange{}
	found := false
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope.rangeFacts != nil {
			if r, ok := scope.rangeFacts[name]; ok {
				acc = acc.intersect(r)
				found = true
			}
		}
	}
	return acc, found
}

// lawConstraint is one `self OP const` clause of a law body in the decidable fragment.
type lawConstraint struct {
	op lexer.TokenKind
	c  int64
}

// lawConstraints interprets a law body as a conjunction of `self OP const` constraints, or returns
// false if the body is outside the decidable fragment (then the flow prover declines and discharge
// falls back to a runtime check). `self` is the law's first parameter name; `paramConsts` binds the
// law's remaining (static `[..]`) params to the refinement's bracket-arg constants, so a body like
// `self >= lo and self <= hi` is interpreted against the actual bounds.
func (a *Analyzer) lawConstraints(decl *ast.FuncDecl, paramConsts map[string]int64) ([]lawConstraint, bool) {
	if decl == nil || len(decl.Params) == 0 || len(decl.Body) != 1 {
		return nil, false
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok || ret == nil || ret.Value == nil {
		return nil, false
	}
	self := decl.Params[0].Name
	var out []lawConstraint
	if !a.collectLawConstraints(ret.Value, self, paramConsts, &out) {
		return nil, false
	}
	return out, true
}

// collectLawConstraints walks a conjunction of `self OP <const>` comparisons, where the operand is
// a literal constant or a static param bound in paramConsts. Any other shape makes the whole body
// undecidable (returns false) so the prover stays sound by declining.
func (a *Analyzer) collectLawConstraints(expr ast.Expr, self string, paramConsts map[string]int64, out *[]lawConstraint) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.collectLawConstraints(n.Inner, self, paramConsts, out)
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			return a.collectLawConstraints(n.Left, self, paramConsts, out) && a.collectLawConstraints(n.Right, self, paramConsts, out)
		}
		// `self OP <const>` (or the constant on the left).
		if isSelfIdent(n.Left, self) {
			if c, ok := a.operandConst(n.Right, paramConsts); ok {
				*out = append(*out, lawConstraint{op: n.Op, c: c})
				return true
			}
		}
		if isSelfIdent(n.Right, self) {
			if c, ok := a.operandConst(n.Left, paramConsts); ok {
				*out = append(*out, lawConstraint{op: flipComparison(n.Op), c: c})
				return true
			}
		}
		return false
	default:
		return false
	}
}

// operandConst resolves a law-body comparison operand to a constant: a literal, or a static law
// param bound to a bracket-arg constant.
func (a *Analyzer) operandConst(expr ast.Expr, paramConsts map[string]int64) (int64, bool) {
	if ident, ok := expr.(*ast.Ident); ok && ident != nil {
		if c, bound := paramConsts[ident.Name]; bound {
			return c, true
		}
	}
	return a.constIntValue(expr)
}

func isSelfIdent(expr ast.Expr, self string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident != nil && ident.Name == self
}

// rangeEntailsConstraint reports whether the known range provably satisfies one law constraint.
func rangeEntailsConstraint(r numRange, k lawConstraint) bool {
	switch k.op {
	case lexer.TOKEN_GTEQ: // self >= c
		return r.loKnown && r.lo >= k.c
	case lexer.TOKEN_GT: // self > c
		return r.loKnown && r.lo > k.c
	case lexer.TOKEN_LTEQ: // self <= c
		return r.hiKnown && r.hi <= k.c
	case lexer.TOKEN_LT: // self < c
		return r.hiKnown && r.hi < k.c
	case lexer.TOKEN_EQEQ: // self == c
		return r.loKnown && r.hiKnown && r.lo == k.c && r.hi == k.c
	default:
		return false
	}
}

// tryProveRefinementByFlow attempts a flow-sensitive static proof of `value is law`: when `value`
// is a bare immutable integer identifier with a known range fact, and the law body is a decidable
// conjunction of `self OP const` constraints, it checks the range entails every constraint. Returns
// true only on a sound proof (docs/85 1d-2).
func (a *Analyzer) tryProveRefinementByFlow(value ast.Expr, decl *ast.FuncDecl, predArgs []ast.Expr) bool {
	ident, ok := value.(*ast.Ident)
	if !ok || ident == nil {
		return false
	}
	r, ok := a.lookupRangeFact(ident.Name)
	if !ok {
		return false
	}
	// Bind the law's static params (decl.Params[1:]) to the refinement's bracket-arg constants.
	if decl == nil || len(predArgs) != len(decl.Params)-1 {
		return false
	}
	paramConsts := map[string]int64{}
	for i, arg := range predArgs {
		c, ok := a.constIntValue(arg)
		if !ok {
			return false // a non-constant bracket arg is not statically interpretable
		}
		paramConsts[decl.Params[i+1].Name] = c
	}
	constraints, ok := a.lawConstraints(decl, paramConsts)
	if !ok || len(constraints) == 0 {
		return false
	}
	for _, k := range constraints {
		if !rangeEntailsConstraint(r, k) {
			return false
		}
	}
	return true
}
