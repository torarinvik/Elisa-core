package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

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

// recordRefinementChecks records the discharge obligations for a refinement-typed var declaration
// (docs/85 Stage 1c-2): for each bare predicate `P` in the declared type, the check `P(name)` that
// must hold for the bound value. Codegen emits these as a debug boundary check (trap on
// violation), elided in release. Bare predicates only for now; parametric `P[args]` discharge
// (range/const-arg expansion) is a follow-up.
func (a *Analyzer) recordRefinementChecks(n *ast.VarDeclStmt) {
	if a == nil || n == nil || a.refinementChecks == nil || n.Value == nil {
		return
	}
	rt, ok := n.Type.(*ast.RefinementTypeExpr)
	if !ok || rt == nil {
		return
	}
	var checks []*ast.CallExpr
	for _, pred := range rt.Preds {
		lawDecl, _, ok := a.lookupLaw(pred.Name)
		if !ok {
			continue // not a law — already reported by validateRefinementPreds
		}
		// Try to discharge statically (flow then constant entailment). Proven/refuted → done.
		if a.tryDischargeRefinementStatically(n.Value, "\""+n.Name+"\"", pred, lawDecl, n.Pos()) {
			continue
		}
		// Not statically proven: fall back to a runtime check AND tell the user — a static guarantee
		// was not achieved here (docs/85: the fallback must be KNOWN). Warning by default; hard error
		// under -strict (prove-it-or-fail, the Dafny-like mode).
		a.proofLint(n.Pos(), "refinement %q on %q could not be proven statically; it is checked at runtime (debug) — make the value provable, or accept the runtime check", pred.Name, n.Name)
		call := &ast.CallExpr{
			Position: pred.Position,
			Func:     &ast.Ident{Position: pred.Position, Name: pred.Name},
			Args:     append([]ast.Expr{&ast.Ident{Position: n.Pos(), Name: n.Name}}, pred.Args...),
		}
		a.analyzeExpr(call)
		checks = append(checks, call)
	}
	if len(checks) != 0 {
		a.refinementChecks[n] = checks
	}
}

// tryDischargeRefinementStatically attempts to discharge one refinement obligation `value is
// pred` at compile time, by flow entailment then constant entailment. Returns true when statically
// resolved: PROVEN (nothing emitted) or REFUTED (a compile error is emitted here). Returns false
// when the obligation is unknown — the caller decides the runtime/observability fallback. Shared by
// the var-decl boundary and call-argument boundaries.
func (a *Analyzer) tryDischargeRefinementStatically(value ast.Expr, valueName string, pred ast.RefinementPredExpr, lawDecl *ast.FuncDecl, pos lexer.Pos) bool {
	if a.tryProveRefinementByFlow(value, lawDecl, pred.Args) {
		return true
	}
	if ok, known := a.evalConstBoolExpr(&ast.CallExpr{
		Position: pred.Position,
		Func:     &ast.Ident{Position: pred.Position, Name: pred.Name},
		Args:     append([]ast.Expr{value}, pred.Args...),
	}); known {
		if !ok {
			a.errorf(pos, "refinement %q is violated: %s does not satisfy it", pred.Name, valueName)
		}
		return true
	}
	return false
}

// dischargeCallArgRefinements discharges the refinement obligations on a direct call's arguments
// against the callee's refinement-typed parameters (docs/85: the function-contract boundary). The
// callee's param refinements survive on its decl's parameter type exprs (the resolved FuncType is
// erased), so a direct by-name call can read them and prove/refute/warn each argument with the same
// discharge tiers as a var declaration. Runtime enforcement of an unproven arg at the call site is
// a follow-up; under -strict an unproven arg is already a hard error.
func (a *Analyzer) dischargeCallArgRefinements(call *ast.CallExpr, args []ast.Expr) {
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok {
		return
	}
	for i, param := range decl.Params {
		if i >= len(args) || args[i] == nil {
			break
		}
		rt, ok := param.Type.(*ast.RefinementTypeExpr)
		if !ok || rt == nil {
			continue
		}
		for _, pred := range rt.Preds {
			lawDecl, _, ok := a.lookupLaw(pred.Name)
			if !ok {
				continue
			}
			name := "argument " + itoaParam(i+1)
			if a.tryDischargeRefinementStatically(args[i], name, pred, lawDecl, call.Pos()) {
				continue
			}
			a.proofLint(call.Pos(), "refinement %q on %s of %q could not be proven statically; pass a provable value or accept the runtime check", pred.Name, name, decl.Name)
		}
	}
}

// validateRefinementPreds checks that every predicate in a refinement type `Base is P, …` names a
// law and that the law's subject (first parameter) accepts the base type. Representation is the
// base type (erased); this only validates the predicates so a malformed refinement is a clear
// error rather than a silent no-op. Discharge of the predicate at a binding boundary is separate.
func (a *Analyzer) validateRefinementPreds(n *ast.RefinementTypeExpr, base Type) {
	if a == nil || n == nil {
		return
	}
	for _, pred := range n.Preds {
		decl, ft, ok := a.lookupLaw(pred.Name)
		if !ok {
			a.errorf(pred.Position, "refinement predicate %q is not a law", pred.Name)
			continue
		}
		if ft == nil || len(ft.Params) == 0 {
			continue // signature not built yet, or a subjectless law (reported at its declaration)
		}
		// A generic law (subject is a type parameter) accepts any base via inference. A concrete
		// subject must accept the base type.
		if len(decl.TypeParams) == 0 && base != nil {
			subject := ft.Params[0]
			if !AssignableTo(base, subject) && !AssignableTo(subject, base) {
				a.errorf(pred.Position, "refinement %q expects a subject of type %s, but the refined type is %s", pred.Name, typeString(subject), typeString(base))
			}
		}
	}
}

// lookupLaw resolves a name to a law declaration (and its function type if already built),
// following alias chains. It consults the current scope then the global scope, since refinement
// types are resolved during signature building when the active scope may not yet chain to globals.
// The FuncType may be nil if the law's signature is not built yet; callers that need the subject
// type must tolerate that.
func (a *Analyzer) lookupLaw(name string) (*ast.FuncDecl, *FuncType, bool) {
	scopes := []*Scope{a.currentScope, a.globalScope}
	for _, scope := range scopes {
		if scope == nil {
			continue
		}
		sym, ok := scope.Lookup(name)
		if !ok {
			continue
		}
		for sym != nil {
			if decl, isDecl := sym.Node.(*ast.FuncDecl); isDecl && decl != nil && decl.IsLaw {
				ft, _ := sym.Type.(*FuncType)
				return decl, ft, true
			}
			sym = sym.AliasOf
		}
	}
	return nil, nil, false
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
