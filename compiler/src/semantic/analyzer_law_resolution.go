package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

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
			// During eager resolution the law may simply not be registered yet (a struct field type is
			// resolved before law decls). Defer the whole refinement's validation to the post-law pass
			// rather than falsely reporting "not a law"; only the final pass errors.
			if !a.finalizingRefinements {
				a.deferredAliasRefinements = append(a.deferredAliasRefinements, deferredAliasRefinement{
					rt:        n,
					base:      base,
					namespace: a.currentNamespace,
					usings:    append([]string(nil), a.currentUsings...),
				})
				return
			}
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
	decl, typ, ok := a.lookupLawForReport(name)
	if a != nil {
		a.addProofReportOrigin(decl)
	}
	return decl, typ, ok
}

func (a *Analyzer) lookupLawForReport(name string) (*ast.FuncDecl, *FuncType, bool) {
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
	// Built-in modular-arithmetic predicates (Aligned/Divides/Fits/MaskedZero) have no user `law`
	// decl: synthesize one on demand so every law consumer (discharge, flow facts, validation) treats
	// them uniformly. The FuncType is left nil — refinement discharge does not need it, and the
	// builtin's subject accepts any integer width.
	if decl := a.lookupBuiltinModularLaw(name); decl != nil {
		return decl, nil, true
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

// gatherLawIsSMTFact records the LAW BODY (instantiated for the subject expression) as an assumable
// SMT flow fact for an `if EXPR is Law:` branch condition (docs/85: refinement flow-narrowing). Inside
// the THEN branch the predicate holds, so the body — e.g. `self >= 0` for `law NonNeg(self:i64) = self
// >= 0`, with `self` replaced by EXPR — becomes a hypothesis available to discharge a downstream
// obligation such as `assert EXPR >= 0`. In the ELSE branch the NEGATION `not(body)` is recorded ONLY
// when the law is decidable/total (a quantifier-free, clonable boolean body): a partial/quantified law
// has no usable runtime negation, so the else records nothing (always sound — fewer facts only declines
// a proof, never fabricates one). This complements gatherLawIsPredFact (which tracks the `EXPR is Law`
// fact itself for a later `is`-obligation) by feeding the comparison/arithmetic prover.
//
// SOUNDNESS: the substituted body is the law's own definition evaluated at EXPR, recorded only in the
// branch where the condition's truth value matches. smtFactDeps over the substituted body names EXPR's
// roots, so the fact is invalidated automatically when EXPR is mutated (the standard fact-drop path).
func (a *Analyzer) gatherLawIsSMTFact(scope *Scope, n *ast.BinaryExpr, truthy bool) {
	if a == nil || scope == nil || n == nil || n.Left == nil {
		return
	}
	targets := flattenIsTargetExprs(n.Right)
	if len(targets) != 1 {
		return
	}
	lawName, lawArgs, ok := a.resolveLawIsTarget(targets[0])
	if !ok {
		return
	}
	lawDecl, _, ok := a.lookupLaw(lawName)
	if !ok || lawDecl == nil || len(lawDecl.Params) == 0 {
		return
	}
	body, ok := a.lawBodyExpr(lawDecl)
	if !ok || body == nil {
		return
	}
	// A quantified body is spec-only (no executable/negatable boolean) — never record it as a flow
	// hypothesis here; the dedicated refinement-discharge tiers handle quantified laws.
	if a.lawBodyContainsQuantifier(lawDecl) {
		return
	}
	// Build the subject/param substitution: `self` -> EXPR, each law value-param -> the bracket arg.
	subst := map[string]ast.Expr{lawDecl.Params[0].Name: n.Left}
	for i, arg := range lawArgs {
		if i+1 >= len(lawDecl.Params) || arg == nil {
			return // arity mismatch or a missing arg ⇒ cannot instantiate soundly
		}
		subst[lawDecl.Params[i+1].Name] = arg
	}
	instantiated := ast.CloneExprSubst(body, subst)
	if instantiated == nil {
		return // body or a substituted arg fell outside the clonable fragment ⇒ decline (sound)
	}
	if truthy {
		a.recordSMTAssertFactInScope(scope, instantiated)
		return
	}
	// ELSE branch: the law is decidable (quantifier-free, clonable), so its negation is a sound fact.
	a.recordSMTAssertFactInScope(scope, &ast.UnaryExpr{Position: n.Pos(), Op: lexer.TOKEN_NOT, Operand: instantiated})
}

// returnSubjectLocalBindingWouldProve reports whether binding the return subject to a local variable
// first would allow the flow tier to prove the refinement `pred` statically. This is true when:
//
//  1. The subject is a field access (`value.f`) or index access (`arr[i]`) — bare locals are already
//     handled by the flow tier and would not benefit from re-binding.
//  2. The resolved type of the expression has a numeric range (from primitive width, e.g. u8→[0,255],
//     or from a declared refinement on the field's type expr) that entails all the law's constraints.
//
// The soundness guarantee: we ONLY return true when `proveConstraintsFromRange` succeeds, which means
// the proof would genuinely discharge for a fresh local of that type. No false positives.
func (a *Analyzer) returnSubjectLocalBindingWouldProve(value ast.Expr, lawDecl *ast.FuncDecl, pred ast.RefinementPredExpr) bool {
	stripped := stripOptimizationParens(value)
	switch stripped.(type) {
	case *ast.FieldExpr, *ast.IndexExpr:
		// subject is a field or index read — proceed
	default:
		return false
	}
	// Derive the best available range for the expression's resolved type.
	// First try: primitive-width range (e.g. u8 → [0, 255], i16 → [-32768, 32767]).
	resolvedType := a.exprType(stripped)
	r, ok := declaredTypeRange(resolvedType)
	// Second try: if the field has a declared refinement (e.g. `u32 is InRange[0,127]`), extract the
	// implied range from the refinement's law constraints and intersect with any primitive-width range.
	if fe, isFE := stripped.(*ast.FieldExpr); isFE && fe != nil {
		st, stOk := stripRefForBounds(a.exprType(fe.Object)).(*StructType)
		if stOk && st != nil && st.Decl != nil {
			for _, fd := range st.Decl.Fields {
				if fd.Name != fe.Field {
					continue
				}
				ft := fd.Type
				if mt, mok := ft.(*ast.MutableType); mok && mt != nil {
					ft = mt.Elem
				}
				if rt, rtOk := a.paramRefinementTypeExpr(ft); rtOk {
					if fact, fok := a.rangeFromRefinementTypeExpr(rt, nil); fok {
						if ok {
							r = r.intersect(fact)
						} else {
							r = fact
							ok = true
						}
					}
				}
				break
			}
		}
	}
	if !ok {
		return false
	}
	return a.proveConstraintsFromRange(r, lawDecl, pred.Args)
}

// returnSubjectDescription returns a short human-readable description of the return subject expression
// for use in lint messages. For field/index accesses it returns the sub-expression text; otherwise it
// returns a generic placeholder.
func (a *Analyzer) returnSubjectDescription(value ast.Expr) string {
	stripped := stripOptimizationParens(value)
	switch n := stripped.(type) {
	case *ast.FieldExpr:
		if n != nil {
			if base, ok := n.Object.(*ast.Ident); ok && base != nil {
				return "\"" + base.Name + "." + n.Field + "\""
			}
			return "\"<expr>." + n.Field + "\""
		}
	case *ast.IndexExpr:
		if n != nil {
			if base, ok := n.Object.(*ast.Ident); ok && base != nil {
				return "\"" + base.Name + "[…]\""
			}
			return "\"<expr>[…]\""
		}
	}
	return "the returned value"
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
