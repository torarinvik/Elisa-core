package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Named refinement aliases (docs/85 "Level 2").
//
// `refine NAME[type-params](value-params) = BASE where PRED` is PURE desugaring: a use of
// `NAME` / `NAME[args]` in a BINDER position (parameter, return, local) is rewritten into the
// equivalent anonymous `BASE where PRED'` (an *ast.WhereRefinementTypeExpr), where PRED' is PRED with
// the value-parameters substituted by the use-site bracket arguments. The bound value is referred to
// inside PRED via `self`, exactly as in an anonymous `where`, so the existing where machinery
// (analyzeParamWhereRefinements / dischargeWhereRefinement / buildSpecSignature) discharges it with NO
// parallel proof path.
//
// Soundness invariants for this first version:
//   - Representation-erased: the rewrite only ever produces a WhereRefinementTypeExpr whose Base is the
//     alias's declared BASE, so SameType / AssignableTo never observe the alias (they erase to BASE).
//   - Binder-position-only: a refine name used as an ordinary type (anywhere resolveType runs that is
//     NOT a pre-rewritten binder) produces a clear diagnostic instead of silently erasing.

// collectRefineAliases records every `refine` declaration by qualified name. It runs alongside the
// other decl-collection passes (collectTypeAliases et al.), before any binder type is resolved.
func (a *Analyzer) collectRefineAliases(decls []scopedDecl) {
	for _, scoped := range decls {
		rd, ok := scoped.Decl.(*ast.RefineDecl)
		if !ok || rd == nil {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			if a.refineAliases == nil {
				a.refineAliases = map[string]*ast.RefineDecl{}
			}
			qualified := joinQualifiedName(scoped.Namespace, rd.Name)
			if _, exists := a.refineAliases[qualified]; exists {
				a.errorf(rd.Pos(), "duplicate refine alias %q", qualified)
				return
			}
			if rd.Base == nil || rd.Predicate == nil {
				// Malformed (parser already reported); skip registration so uses fall back to the
				// ordinary unknown-type path rather than expanding a nil predicate.
				return
			}
			a.refineAliases[qualified] = rd
		})
	}
}

// lookupRefineAlias finds a refine alias by its bare or qualified name, honouring the active
// namespace/using resolution context.
func (a *Analyzer) lookupRefineAlias(name string) (*ast.RefineDecl, bool) {
	if a == nil || a.refineAliases == nil || name == "" {
		return nil, false
	}
	if rd, ok := a.refineAliases[name]; ok {
		return rd, true
	}
	// Try namespace-qualified lookups using the active resolution context, mirroring lookupVisibleType.
	for _, candidate := range a.qualifiedNameCandidates(name) {
		if rd, ok := a.refineAliases[candidate]; ok {
			return rd, true
		}
	}
	return nil, false
}

// qualifiedNameCandidates returns the active-namespace-qualified spellings of a bare name, most
// specific first, so a refine alias declared inside a module resolves from within that module.
func (a *Analyzer) qualifiedNameCandidates(name string) []string {
	var out []string
	ns := a.currentNamespace
	for ns != "" {
		out = append(out, joinQualifiedName(ns, name))
		idx := -1
		for i := len(ns) - 1; i >= 0; i-- {
			if ns[i] == '.' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		ns = ns[:idx]
	}
	return out
}

// refineAliasName extracts the alias name and use-site bracket argument expressions from a binder
// TypeExpr, if it denotes a refine alias use. Returns ok=false for any non-alias type. A bare name
// (`Positive`) is a NamedType; a parametric use (`IndexOf[xs]`) is a GenericType whose Args are the
// value arguments.
func refineAliasNameAndArgs(te ast.TypeExpr) (string, []ast.TypeExpr, bool) {
	switch n := te.(type) {
	case *ast.NamedType:
		return n.Name, nil, true
	case *ast.GenericType:
		return n.Name, n.Args, true
	}
	return "", nil, false
}

// expandRefineAliasType returns the WhereRefinementTypeExpr equivalent of a binder-position refine
// alias use, or (nil,false) when te is not a refine alias. It performs the value-parameter
// substitution into the predicate; `self` is left intact (the where machinery binds it to the bound
// value's name at the discharge site, exactly like an anonymous `where`).
//
// Two enhancements over the initial version:
//  1. Richer use-site args: bracket args may be arbitrary pure value expressions (dotted paths,
//     GenericValueArgTypeExpr wrappers). Impure args emit a clear diagnostic and are skipped.
//  2. Alias-composing-alias: if the alias's Base names another refine alias, resolveRefineAliasFully
//     flattens it transitively (cycle guard included).
func (a *Analyzer) expandRefineAliasType(te ast.TypeExpr, selfName string) (*ast.WhereRefinementTypeExpr, bool) {
	name, args, ok := refineAliasNameAndArgs(te)
	if !ok {
		return nil, false
	}
	rd, found := a.lookupRefineAlias(name)
	if !found || rd == nil || rd.Base == nil || rd.Predicate == nil {
		return nil, false
	}
	// Transitively resolve if the base itself is a refine alias (alias-composing-alias).
	base, pred := a.resolveRefineAliasFully(rd, nil)
	// Map the alias's value parameters onto the use-site bracket arguments positionally. Type-params
	// (if any) precede value-params in the bracket list; they only affect Base resolution (handled by
	// the surrounding scope) and never appear in the predicate, so they are skipped for substitution.
	// `self` (the refined value inside the predicate) is substituted by the binder name, so the
	// produced predicate references the bound name directly — exactly an anonymous `where` does.
	subst := map[string]ast.Expr{}
	if selfName != "" {
		subst["self"] = &ast.Ident{Position: te.Pos(), Name: selfName}
	}
	valueArgs := args
	if len(rd.TypeParams) > 0 && len(valueArgs) >= len(rd.TypeParams) {
		valueArgs = valueArgs[len(rd.TypeParams):]
	}
	for i, param := range rd.Params {
		if i >= len(valueArgs) {
			break
		}
		argExpr, okArg := typeExprAsValueExpr(valueArgs[i])
		if !okArg {
			a.errorf(valueArgs[i].Pos(), "refine alias argument must be a pure value expression (identifier or field path), not an impure or complex type expression")
			continue
		}
		subst[param.Name] = argExpr
	}
	if len(subst) > 0 {
		pred = substituteIdents(pred, subst)
	}
	return &ast.WhereRefinementTypeExpr{
		Position:  te.Pos(),
		Base:      base,
		Predicate: pred,
	}, true
}

// resolveRefineAliasFully flattens a refine-alias chain: if rd.Base names another refine alias,
// it recurses into that alias and AND-combines the predicates. visiting tracks aliases in the current
// chain to detect cycles.
//
// Cycle detection: if we reach an alias already in visiting, we emit a diagnostic and stop recursion,
// returning the innermost non-recursive base/predicate seen so far.
func (a *Analyzer) resolveRefineAliasFully(rd *ast.RefineDecl, visiting map[string]bool) (ast.TypeExpr, ast.Expr) {
	if visiting == nil {
		visiting = map[string]bool{}
	}
	visiting[rd.Name] = true
	base := rd.Base
	pred := rd.Predicate
	// Check if the base type itself is a refine alias.
	if baseName, _, baseIsAlias := refineAliasNameAndArgs(base); baseIsAlias {
		innerRD, found := a.lookupRefineAlias(baseName)
		if found && innerRD != nil && innerRD.Base != nil && innerRD.Predicate != nil {
			if visiting[innerRD.Name] {
				a.errorf(rd.Pos(), "refine alias %q has a recursive or mutually-recursive base (cycle through %q)", rd.Name, innerRD.Name)
				// Return what we have rather than looping.
				return base, pred
			}
			innerBase, innerPred := a.resolveRefineAliasFully(innerRD, visiting)
			base = innerBase
			// Combine predicates: outer AND inner (both must hold).
			pred = &ast.BinaryExpr{
				Position: rd.Predicate.Pos(),
				Op:       lexer.TOKEN_AND,
				Left:     pred,
				Right:    innerPred,
			}
		}
	}
	delete(visiting, rd.Name)
	return base, pred
}

// typeExprAsValueExpr converts a refine use-site bracket argument (a TypeExpr) into the value
// expression it denotes. It accepts any pure value expression encoded as a TypeExpr:
//   - a bare name (`xs`) → Ident
//   - a dotted path (`xs.count`, `a.b`) → FieldExpr chain (via dottedNameAsValueExpr)
//   - a GenericValueArgTypeExpr wrapping an arbitrary pure Expr → the inner Expr
//
// Returns (nil, false) for real generic instantiations (e.g. `Foo[i64]`) or expressions
// that fail the side-effect-free check. The caller emits the diagnostic.
func typeExprAsValueExpr(te ast.TypeExpr) (ast.Expr, bool) {
	// Use the richer converter already present in the law-is machinery.
	expr, ok := typeArgAsValueExpr(te)
	if !ok {
		return nil, false
	}
	// Purity gate: reuse the same predicate-purity checker used by anonymous where refinements.
	if !wherePredicateIsSideEffectFree(expr) {
		return nil, false
	}
	return expr, true
}

// rewriteBinderRefineAliases expands refine-alias uses in every binder position (function parameter
// types and return types) in place, so all downstream where machinery sees a normal anonymous
// WhereRefinementTypeExpr. Local var-decl binders are handled lazily at their flow site (see
// rewriteLocalRefineAlias), because their use-site bracket args reference in-scope locals.
func (a *Analyzer) rewriteBinderRefineAliases(decls []scopedDecl) {
	if a == nil || len(a.refineAliases) == 0 {
		return
	}
	for _, scoped := range decls {
		fn, ok := scoped.Decl.(*ast.FuncDecl)
		if !ok || fn == nil {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			for i := range fn.Params {
				if wt, ok := a.expandRefineAliasType(fn.Params[i].Type, fn.Params[i].Name); ok {
					fn.Params[i].Type = wt
				}
			}
			// A return refinement binds the refined value as `result`, mirroring anonymous return
			// where refinements (`-> i64 where result >= 0`).
			if wt, ok := a.expandRefineAliasType(fn.ReturnType, "result"); ok {
				fn.ReturnType = wt
			}
		})
	}
}

// rewriteLocalRefineAlias expands a refine-alias use in a local var-decl binder in place, returning
// true when a rewrite occurred. Called from the var-decl flow handling so that the existing local
// where discharge (dischargeLocalWhereRefinement / seedWhereRefinementFact) sees a normal anonymous
// where.
func (a *Analyzer) rewriteLocalRefineAlias(stmt *ast.VarDeclStmt) {
	if a == nil || stmt == nil || stmt.Type == nil || len(a.refineAliases) == 0 {
		return
	}
	if wt, ok := a.expandRefineAliasType(stmt.Type, stmt.Name); ok {
		stmt.Type = wt
	}
}

// substituteIdents returns a deep copy of expr with each Ident whose name is a key in subst replaced
// by the mapped expression. It is a structural rewrite over the pure predicate expression grammar
// (the same grammar wherePredicateIsSideEffectFree accepts), which is all a refine predicate may use.
func substituteIdents(expr ast.Expr, subst map[string]ast.Expr) ast.Expr {
	switch n := expr.(type) {
	case nil:
		return nil
	case *ast.Ident:
		if repl, ok := subst[n.Name]; ok {
			return repl
		}
		return n
	case *ast.ParenExpr:
		return &ast.ParenExpr{Position: n.Position, Inner: substituteIdents(n.Inner, subst)}
	case *ast.UnaryExpr:
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: substituteIdents(n.Operand, subst)}
	case *ast.BinaryExpr:
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: substituteIdents(n.Left, subst), Right: substituteIdents(n.Right, subst)}
	case *ast.FieldExpr:
		out := *n
		out.Object = substituteIdents(n.Object, subst)
		return &out
	case *ast.IndexExpr:
		out := *n
		out.Object = substituteIdents(n.Object, subst)
		out.Index = substituteIdents(n.Index, subst)
		out.Index2 = substituteIdents(n.Index2, subst)
		out.Fallback = substituteIdents(n.Fallback, subst)
		return &out
	case *ast.SliceExpr:
		out := *n
		out.Object = substituteIdents(n.Object, subst)
		out.Start = substituteIdents(n.Start, subst)
		out.End = substituteIdents(n.End, subst)
		return &out
	case *ast.CallExpr:
		out := *n
		out.Func = substituteIdents(n.Func, subst)
		out.Args = make([]ast.Expr, len(n.Args))
		for i, arg := range n.Args {
			out.Args[i] = substituteIdents(arg, subst)
		}
		return &out
	}
	return expr
}

// rejectRefineAliasAsOrdinaryType emits the binder-position-only diagnostic. It is called from the
// type-resolution fall-through for a name that resolves to a refine alias but was NOT pre-rewritten as
// a binder — i.e. an attempt to use the alias as an ordinary type.
func (a *Analyzer) rejectRefineAliasAsOrdinaryType(pos lexer.Pos, name string) bool {
	if _, ok := a.lookupRefineAlias(name); !ok {
		return false
	}
	a.errorf(pos, "refine alias %q may only be used in a binder position (parameter, return, or local declaration), not as an ordinary type", name)
	return true
}
