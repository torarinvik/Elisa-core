package semantic

import "elisacore/src/ast"

// tryAnalyzeLawIsExpr handles `subject is Law` as predicate application (docs/85 §2): `is` is
// UFCS first-arg binding, so `x is P` ≡ `P(x)`. When the (single) target resolves to a law, this
// builds the synthetic call `P(subject)`, analyzes it (which type-checks the subject against the
// law's first parameter and yields bool), and records it for codegen. Returns false when the
// target is not a bare law, leaving the existing `is` handling (variants, patterns, comparisons)
// untouched. The parametric `is P[args]` form is handled later with refinement types (Stage 1c).
func (a *Analyzer) tryAnalyzeLawIsExpr(expr *ast.BinaryExpr) bool {
	if a == nil || expr == nil || a.lawIsCalls == nil {
		return false
	}
	targets := flattenIsTargetExprs(expr.Right)
	if len(targets) != 1 {
		return false
	}
	lawName, ok := a.resolveBareLawIsTarget(targets[0])
	if !ok {
		return false
	}
	call := &ast.CallExpr{
		Position: expr.Pos(),
		Func:     &ast.Ident{Position: expr.Right.Pos(), Name: lawName},
		Args:     []ast.Expr{expr.Left},
	}
	a.analyzeExpr(call)
	a.lawIsCalls[expr] = call
	return true
}

// resolveBareLawIsTarget returns the law name when `target` is a bare reference (an identifier or
// plain named type, no bracket/value args) to a `law` declaration. Parametric targets
// (`Bounded[0..500]`) and non-laws return false.
func (a *Analyzer) resolveBareLawIsTarget(target ast.Expr) (string, bool) {
	name, ok := bareTargetName(target)
	if !ok || name == "" {
		return "", false
	}
	if a.currentScope == nil {
		return "", false
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil {
		return "", false
	}
	for sym != nil {
		if decl, ok := sym.Node.(*ast.FuncDecl); ok && decl != nil && decl.IsLaw {
			return name, true
		}
		sym = sym.AliasOf
	}
	return "", false
}

// bareTargetName extracts a plain name from an `is` target that is a bare identifier or named
// type (unwrapping parens). Anything compound (generic args, dotted, value args) returns false.
func bareTargetName(target ast.Expr) (string, bool) {
	switch n := target.(type) {
	case *ast.ParenExpr:
		if n == nil {
			return "", false
		}
		return bareTargetName(n.Inner)
	case *ast.Ident:
		if n == nil {
			return "", false
		}
		return n.Name, true
	case *ast.TypeExprExpr:
		if n == nil {
			return "", false
		}
		named, ok := n.Type.(*ast.NamedType)
		if !ok || named == nil || indexOfByte(named.Name, '.') >= 0 {
			return "", false
		}
		return named.Name, true
	default:
		return "", false
	}
}
