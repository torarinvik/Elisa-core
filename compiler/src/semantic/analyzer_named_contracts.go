package semantic

import (
	"elisacore/src/ast"
)

// Named composable contracts (docs/97).
//
// A `contract Name(params):` decl bundles requires/ensure/changes/preserves clauses under one name,
// parameterised over its params. A function applies it with a leading `uses Name(args)` clause. This
// pass — run once, before body analysis and contract discharge — resolves each `uses` to its contract,
// substitutes the contract's formals by the application arguments, and folds the resulting clauses
// into the applying function's own Requires / EnsureValues / Changes / Preserves slices. From that
// point on the existing requires-discharge, ensure-proof, and frame checker see the unfolded clauses
// with no further changes, so a `uses`d precondition is automatically checked at every call site of
// the applying function (composition = conjunction of value premises ∪ union of frames; docs/97 §5).

// expandUsesContracts performs the whole-program contract expansion. It builds the contract registry
// from the (already namespace-flattened) decls and then unfolds every function's `uses` clauses.
func (a *Analyzer) expandUsesContracts(decls []scopedDecl) {
	contracts := map[string]*ast.FuncDecl{}
	for _, sd := range decls {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok && fn != nil && fn.IsContract {
			contracts[fn.Name] = fn
			contracts[joinQualifiedName(sd.Namespace, fn.Name)] = fn
		}
	}
	for _, sd := range decls {
		fn, ok := sd.Decl.(*ast.FuncDecl)
		if !ok || fn == nil || len(fn.Uses) == 0 || fn.IsContract {
			continue
		}
		for _, use := range fn.Uses {
			a.expandOneUse(fn, use, contracts)
		}
	}
}

// validateContractDecl checks a `contract` declaration is well-formed: it must bundle at least one
// clause (an empty contract is almost certainly a mistake) and declare at least one parameter (the
// subject every clause is written against).
func (a *Analyzer) validateContractDecl(fn *ast.FuncDecl) {
	if len(fn.Params) == 0 {
		a.errorf(fn.Position, "contract `%s` must declare at least one parameter (the subject its clauses constrain)", fn.Name)
	}
	if len(fn.Requires) == 0 && len(fn.EnsureValues) == 0 && len(fn.Changes) == 0 && len(fn.Preserves) == 0 && len(fn.ContractIncludes) == 0 {
		a.errorf(fn.Position, "contract `%s` is empty: it must bundle at least one requires/ensure/changes/preserves clause", fn.Name)
	}
}

// expandOneUse folds a single `uses Name(args)` application into fn.
func (a *Analyzer) expandOneUse(fn *ast.FuncDecl, use *ast.ContractStmt, contracts map[string]*ast.FuncDecl) {
	c, ok := contracts[use.UsesName]
	if !ok || c == nil {
		a.errorf(use.Position, "unknown contract `%s` in `uses` clause (declare it with `contract %s(...)`)", use.UsesName, use.UsesName)
		return
	}
	if len(use.UsesArgs) != len(c.Params) {
		a.errorf(use.Position, "contract `%s` takes %d argument(s), but `uses` supplied %d", use.UsesName, len(c.Params), len(use.UsesArgs))
		return
	}
	cft := a.contractFuncTypeForUse(use, c)
	if cft == nil || !a.validateUsesContractArgumentTypes(fn, use, c, cft) {
		return
	}
	// Bind each contract formal to its application argument.
	subst := make(map[string]ast.Expr, len(c.Params))
	rootRebind := make(map[string]string, len(c.Params))
	for i, p := range c.Params {
		subst[p.Name] = use.UsesArgs[i]
		if root, ok := frameArgRoot(use.UsesArgs[i]); ok {
			rootRebind[p.Name] = root
		}
	}
	// Value contracts: substitute formals → args in the clone, then append.
	for i, req := range c.Requires {
		if rewritten, ok := substituteLemmaEnsure(req, subst); ok {
			fn.Requires = append(fn.Requires, rewritten)
			fn.RequiresProofs = append(fn.RequiresProofs, substituteProofBlock(proofAt(c.RequiresProofs, i), rewritten, subst))
		} else {
			a.errorf(use.Position, "cannot apply `requires` clause of contract `%s` here (unsupported expression form)", use.UsesName)
		}
	}
	for i, ens := range c.EnsureValues {
		if rewritten, ok := substituteLemmaEnsure(ens, subst); ok {
			fn.EnsureValues = append(fn.EnsureValues, rewritten)
			fn.EnsureProofs = append(fn.EnsureProofs, substituteProofBlock(proofAt(c.EnsureProofs, i), rewritten, subst))
		} else {
			a.errorf(use.Position, "cannot apply `ensure` clause of contract `%s` here (unsupported expression form)", use.UsesName)
		}
	}
	// Frame conditions: rebase each path's root formal → argument root, union into fn.
	for _, path := range c.Changes {
		if rebased, ok := rebaseFramePath(path, rootRebind); ok {
			fn.Changes = appendFramePathUnique(fn.Changes, rebased)
		} else {
			a.errorf(use.Position, "cannot apply `changes %s` of contract `%s`: argument is not a place expression", path.Root, use.UsesName)
		}
	}
	for _, path := range c.Preserves {
		if rebased, ok := rebaseFramePath(path, rootRebind); ok {
			fn.Preserves = appendFramePathUnique(fn.Preserves, rebased)
		} else {
			a.errorf(use.Position, "cannot apply `preserves %s` of contract `%s`: argument is not a place expression", path.Root, use.UsesName)
		}
	}
	for _, inc := range c.ContractIncludes {
		a.expandContractInclude(fn, use, inc, subst, rootRebind)
	}
}

func substituteProofBlock(proof *ast.ProofBlockStmt, goal ast.Expr, subst map[string]ast.Expr) *ast.ProofBlockStmt {
	if proof == nil {
		return nil
	}
	stmts, ok := substituteProofStmts(proof.Proof, subst)
	if !ok {
		return nil
	}
	return &ast.ProofBlockStmt{Position: proof.Position, Goal: goal, Proof: stmts}
}

func substituteProofStmts(stmts []ast.Stmt, subst map[string]ast.Expr) ([]ast.Stmt, bool) {
	out := make([]ast.Stmt, 0, len(stmts))
	for _, stmt := range stmts {
		next, ok := substituteProofStmt(stmt, subst)
		if !ok {
			return nil, false
		}
		out = append(out, next)
	}
	return out, true
}

func substituteProofStmt(stmt ast.Stmt, subst map[string]ast.Expr) (ast.Stmt, bool) {
	switch n := stmt.(type) {
	case *ast.PassStmt:
		return &ast.PassStmt{Position: n.Position}, true
	case *ast.ProofUseStmt:
		citations, ok := substituteProofExprs(n.Citations, subst)
		if !ok {
			return nil, false
		}
		return &ast.ProofUseStmt{Position: n.Position, Citations: citations}, true
	case *ast.ExprStmt:
		expr, ok := substituteLemmaEnsure(n.Expr, subst)
		if !ok {
			return nil, false
		}
		return &ast.ExprStmt{Position: n.Position, Expr: expr}, true
	case *ast.AssertByStmt:
		cond, ok := substituteLemmaEnsure(n.Cond, subst)
		if !ok {
			return nil, false
		}
		proof, ok := substituteProofStmts(n.Proof, subst)
		if !ok {
			return nil, false
		}
		return &ast.AssertByStmt{Position: n.Position, Cond: cond, Proof: proof, Scoped: n.Scoped, ScopedTheory: n.ScopedTheory}, true
	case *ast.ProofBlockStmt:
		goal, ok := substituteLemmaEnsure(n.Goal, subst)
		if !ok {
			return nil, false
		}
		proof, ok := substituteProofStmts(n.Proof, subst)
		if !ok {
			return nil, false
		}
		return &ast.ProofBlockStmt{Position: n.Position, Goal: goal, Proof: proof}, true
	case *ast.ContractStmt:
		if n.Kind != ast.ContractInvariant {
			return nil, false
		}
		cond, ok := substituteLemmaEnsure(n.Cond, subst)
		if !ok {
			return nil, false
		}
		return &ast.ContractStmt{Position: n.Position, Kind: n.Kind, Cond: cond}, true
	default:
		return nil, false
	}
}

func substituteProofExprs(exprs []ast.Expr, subst map[string]ast.Expr) ([]ast.Expr, bool) {
	out := make([]ast.Expr, 0, len(exprs))
	for _, expr := range exprs {
		next, ok := substituteLemmaEnsure(expr, subst)
		if !ok {
			return nil, false
		}
		out = append(out, next)
	}
	return out, true
}

// validateUsesContractArgumentTypes enforces docs/97 §6's Tier 1 soundness check: a `uses C(args)`
// application must type-check each actual argument against C's corresponding formal before the
// formals are substituted into value/frame clauses.
func (a *Analyzer) validateUsesContractArgumentTypes(fn *ast.FuncDecl, use *ast.ContractStmt, c *ast.FuncDecl, cft *FuncType) bool {
	if a == nil || fn == nil || use == nil || c == nil || cft == nil {
		return false
	}
	scope := a.usesContractApplicationScope(fn)
	ok := true
	limit := len(use.UsesArgs)
	if len(cft.Params) < limit {
		limit = len(cft.Params)
	}
	for i := 0; i < limit; i++ {
		expected := cft.Params[i]
		actual := a.analyzeValueExprInScope(use.UsesArgs[i], expected, scope)
		if IsInvalidType(expected) || IsInvalidType(actual) {
			ok = false
			continue
		}
		if !AssignableTo(expected, actual) {
			formalName := c.Params[i].Name
			if formalName != "" {
				a.errorf(use.UsesArgs[i].Pos(), "contract `%s` argument %d (%s) expects %s, got %s", use.UsesName, i+1, formalName, expected, actual)
			} else {
				a.errorf(use.UsesArgs[i].Pos(), "contract `%s` argument %d expects %s, got %s", use.UsesName, i+1, expected, actual)
			}
			ok = false
		}
	}
	return ok
}

func (a *Analyzer) contractFuncTypeForUse(use *ast.ContractStmt, c *ast.FuncDecl) *FuncType {
	if a == nil || use == nil || c == nil {
		return nil
	}
	cft := a.funcTypeFromDecl("contract "+c.Name, c.TypeParams, c.GenericParams, c.RegionParams, c.PermissionParams, c.Permissions, nil, c.Params, nil, false)
	if cft == nil {
		return nil
	}
	params := genericParamsForFuncType(cft)
	if len(use.UsesTypeArgs) == 0 {
		if len(params) != 0 {
			a.errorf(use.Position, "contract `%s` expects %d %s, got 0", use.UsesName, len(params), genericArgLabel(params))
			return nil
		}
		return cft
	}
	if len(params) == 0 {
		a.errorf(use.Position, "contract `%s` is not generic", use.UsesName)
		for _, arg := range use.UsesTypeArgs {
			a.resolveType(arg)
		}
		return nil
	}
	if len(use.UsesTypeArgs) != len(params) {
		a.errorf(use.Position, "contract `%s` expects %d %s, got %d", use.UsesName, len(params), genericArgLabel(params), len(use.UsesTypeArgs))
	}
	bindings := make(map[string]Type, len(params))
	limit := len(use.UsesTypeArgs)
	if len(params) < limit {
		limit = len(params)
	}
	for i := 0; i < limit; i++ {
		bindings[params[i].Name] = a.resolveGenericArgForParam(use.UsesTypeArgs[i], params[i])
	}
	for i := limit; i < len(use.UsesTypeArgs); i++ {
		a.resolveType(use.UsesTypeArgs[i])
	}
	specialized, _ := a.substituteType(cft, bindings, nil, nil, nil).(*FuncType)
	if specialized == nil {
		return nil
	}
	specialized.TypeParams = nil
	specialized.GenericParams = nil
	return specialized
}

func (a *Analyzer) expandContractInclude(fn *ast.FuncDecl, use *ast.ContractStmt, inc ast.ContractInclude, contractSubst map[string]ast.Expr, rootRebind map[string]string) {
	if isBuiltinShapeLaw(inc.Law) {
		if len(inc.Args) != 0 {
			a.errorf(inc.Position, "function-level law %q takes no argument(s) in contract `%s`", inc.Law, use.UsesName)
			return
		}
		fn.Fulfills = append(fn.Fulfills, ast.FulfillsClause{Position: inc.Position, Law: inc.Law})
		return
	}
	decl, ft, ok := a.lookupLaw(inc.Law)
	if !ok || decl == nil {
		a.errorf(inc.Position, "contract `%s` includes %q, which is not a law", use.UsesName, inc.Law)
		return
	}
	args := make([]ast.Expr, 0, len(inc.Args))
	for _, arg := range inc.Args {
		rewritten, ok := substituteLemmaEnsure(arg, contractSubst)
		if !ok {
			a.errorf(inc.Position, "cannot apply `includes %s(...)` of contract `%s` here (unsupported argument form)", inc.Law, use.UsesName)
			return
		}
		args = append(args, rewritten)
	}
	if isFrameLaw(decl) {
		a.expandFrameLawInclude(fn, use, inc, decl, args, rootRebind)
		return
	}
	if isEffectLaw(decl) || isCompositeLaw(decl) {
		if len(args) != 0 {
			a.errorf(inc.Position, "function-level law %q takes no argument(s) in contract `%s`", inc.Law, use.UsesName)
			return
		}
		fn.Fulfills = append(fn.Fulfills, ast.FulfillsClause{Position: inc.Position, Law: inc.Law})
		return
	}
	if ft == nil {
		ft = a.funcTypeFromDecl("law "+decl.Name, decl.TypeParams, decl.GenericParams, decl.RegionParams, decl.PermissionParams, decl.Permissions, nil, decl.Params, decl.ReturnType, false)
	}
	if len(args) != len(decl.Params) {
		a.errorf(inc.Position, "law `%s` takes %d argument(s), but contract `%s` includes supplied %d", inc.Law, len(decl.Params), use.UsesName, len(args))
		return
	}
	if !a.validateIncludedLawArgumentTypes(fn, use, inc, ft, args) {
		return
	}
	predicate := lawPredicateExpr(decl)
	if predicate == nil {
		a.errorf(inc.Position, "cannot include law `%s` in contract `%s`: missing predicate body", inc.Law, use.UsesName)
		return
	}
	lawSubst := make(map[string]ast.Expr, len(decl.Params))
	for i, p := range decl.Params {
		lawSubst[p.Name] = args[i]
	}
	rewritten, ok := substituteLemmaEnsure(predicate, lawSubst)
	if !ok {
		a.errorf(inc.Position, "cannot apply included law `%s` of contract `%s` here (unsupported expression form)", inc.Law, use.UsesName)
		return
	}
	fn.Requires = append(fn.Requires, rewritten)
}

func (a *Analyzer) validateIncludedLawArgumentTypes(fn *ast.FuncDecl, use *ast.ContractStmt, inc ast.ContractInclude, ft *FuncType, args []ast.Expr) bool {
	if ft == nil {
		return true
	}
	scope := a.usesContractApplicationScope(fn)
	ok := true
	limit := len(args)
	if len(ft.Params) < limit {
		limit = len(ft.Params)
	}
	for i := 0; i < limit; i++ {
		expected := ft.Params[i]
		actual := a.analyzeValueExprInScope(args[i], expected, scope)
		if IsInvalidType(expected) || IsInvalidType(actual) {
			ok = false
			continue
		}
		if !AssignableTo(expected, actual) {
			a.errorf(args[i].Pos(), "contract `%s` included law `%s` argument %d expects %s, got %s", use.UsesName, inc.Law, i+1, expected, actual)
			ok = false
		}
	}
	return ok
}

func (a *Analyzer) expandFrameLawInclude(fn *ast.FuncDecl, use *ast.ContractStmt, inc ast.ContractInclude, decl *ast.FuncDecl, args []ast.Expr, rootRebind map[string]string) {
	if len(args) != 1 {
		a.errorf(inc.Position, "frame law `%s` takes 1 subject argument in contract `%s`, got %d", inc.Law, use.UsesName, len(args))
		return
	}
	root, ok := frameArgRoot(args[0])
	if !ok {
		a.errorf(inc.Position, "cannot apply included frame law `%s` of contract `%s`: argument is not a place expression", inc.Law, use.UsesName)
		return
	}
	subject := decl.Params[0].Name
	localRebind := map[string]string{subject: root}
	for _, p := range decl.Changes {
		if rebased, ok := rebaseFramePath(p, localRebind); ok {
			fn.Changes = appendFramePathUnique(fn.Changes, rebased)
		}
	}
	for _, p := range decl.Preserves {
		if rebased, ok := rebaseFramePath(p, localRebind); ok {
			fn.Preserves = appendFramePathUnique(fn.Preserves, rebased)
		}
	}
	_ = rootRebind
}

func lawPredicateExpr(decl *ast.FuncDecl) ast.Expr {
	if decl == nil || len(decl.Body) != 1 {
		return nil
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		return nil
	}
	return ret.Value
}

func (a *Analyzer) usesContractApplicationScope(fn *ast.FuncDecl) *Scope {
	scope := NewScope(a.globalScope)
	sym, ok := a.symbolForFuncDecl(fn)
	if !ok || sym == nil {
		return scope
	}
	ft, _ := sym.Type.(*FuncType)
	if ft == nil {
		return scope
	}
	params := a.expandedFuncDeclParams(fn)
	for i, param := range params {
		var ptype Type = invalidType
		if i < len(ft.Params) {
			ptype = ft.Params[i]
		}
		symType := ptype
		if ref, ok := ptype.(*RefType); ok && !ref.Mutable && isLegacyOutParamName(param.Name) {
			cloned := cloneRefType(ref)
			cloned.Mutable = true
			symType = cloned
		}
		scope.Define(&Symbol{Name: param.Name, Kind: SymbolParam, Type: symType, Node: fn, ParamIndex: i, Mutable: a.paramIsMutable(param)})
	}
	return scope
}

// frameArgRoot returns the root identifier name of a place expression used as a `uses` argument that
// binds a frame-path root (e.g. `out`, or `obj` in `obj.field`). Only a bare identifier qualifies as a
// rebindable root; anything else (a literal, a call) cannot anchor a frame path.
func frameArgRoot(arg ast.Expr) (string, bool) {
	switch n := arg.(type) {
	case *ast.Ident:
		return n.Name, true
	case *ast.ParenExpr:
		return frameArgRoot(n.Inner)
	default:
		return "", false
	}
}

// rebaseFramePath rewrites a contract frame path's root (a contract formal) to the application
// argument's root identifier. Returns false if the formal root has no rebindable argument root.
func rebaseFramePath(path ast.EnsuresPath, rootRebind map[string]string) (ast.EnsuresPath, bool) {
	newRoot, ok := rootRebind[path.Root]
	if !ok {
		// Root is not a contract formal — it refers to a global/const place with the same meaning in
		// the applying function; keep it verbatim.
		return path, true
	}
	return ast.EnsuresPath{Position: path.Position, Root: newRoot, Fields: append([]string(nil), path.Fields...)}, true
}

// appendFramePathUnique unions a frame path into a slice, skipping an exact (root+fields) duplicate so
// that two `uses` of overlapping contracts do not double-list the same place.
func appendFramePathUnique(paths []ast.EnsuresPath, p ast.EnsuresPath) []ast.EnsuresPath {
	for _, existing := range paths {
		if existing.Root == p.Root && len(existing.Fields) == len(p.Fields) {
			same := true
			for i := range existing.Fields {
				if existing.Fields[i] != p.Fields[i] {
					same = false
					break
				}
			}
			if same {
				return paths
			}
		}
	}
	return append(paths, p)
}
