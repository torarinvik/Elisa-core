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
	if len(fn.Requires) == 0 && len(fn.EnsureValues) == 0 && len(fn.Changes) == 0 && len(fn.Preserves) == 0 {
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
	for _, req := range c.Requires {
		if rewritten, ok := substituteLemmaEnsure(req, subst); ok {
			fn.Requires = append(fn.Requires, rewritten)
		} else {
			a.errorf(use.Position, "cannot apply `requires` clause of contract `%s` here (unsupported expression form)", use.UsesName)
		}
	}
	for _, ens := range c.EnsureValues {
		if rewritten, ok := substituteLemmaEnsure(ens, subst); ok {
			fn.EnsureValues = append(fn.EnsureValues, rewritten)
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
