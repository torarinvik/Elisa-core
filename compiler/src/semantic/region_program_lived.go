package semantic

import "elisacore/src/ast"

// Program-lived named regions (docs/128): `global region NAME`. Such a region dies at
// program end, so it outlives every region (rule 1), program-lived regions outlive each
// other (rule 2), and nothing else outlives one (rule 3). The rules are cases of
// regionOutlives / regionStoreEscapes; no separate diagnostic exists.

func (a *Analyzer) defineProgramRegion(n *ast.GlobalRegionDecl) {
	arenaType, ok := a.namedTypes["Arena"]
	if !ok {
		arenaType = invalidType
	}
	a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolRegion, Type: arenaType, Node: n, Mutable: false}, n.Pos())
}

func (a *Analyzer) isProgramRegion(name string) bool {
	return a != nil && name != "" && a.programRegions[name]
}

// checkProgramRegionShadow rejects a binder (region parameter or `region r:` block) that
// reuses a program-lived region's name: the lattice keys regions by name, so the two would
// be indistinguishable.
func (a *Analyzer) checkProgramRegionShadow(name string, pos ast.Node) {
	if a.isProgramRegion(name) {
		a.errorf(pos.Pos(), "region %q shadows the program-lived region of the same name; rename it", name)
	}
}

// collectProgramRegions records every program-lived region before any signature resolves,
// so `Node[source]` and `sview @source` resolve regardless of declaration order.
func (a *Analyzer) collectProgramRegions(decls []scopedDecl) {
	for _, scoped := range decls {
		if n, ok := scoped.Decl.(*ast.GlobalRegionDecl); ok {
			if a.programRegions == nil {
				a.programRegions = map[string]bool{}
			}
			a.programRegions[n.Name] = true
		}
	}
}

// IsProgramRegion reports whether name is a program-lived region, for consumers (the
// backend's type resolution) that resolve region arguments outside the analyzer.
func (r *Result) IsProgramRegion(name string) bool {
	return r != nil && r.analyzer.isProgramRegion(name)
}

// checkProgramRegionSlotStore checks a value written into a slot whose type names a
// program-lived region (an enum constructor payload, a tail payload element): only a
// program-lived value may be stored there (docs/128 rule 3).
func (a *Analyzer) checkProgramRegionSlotStore(arg ast.Expr, expected, actual Type) {
	if len(a.programRegions) == 0 || arg == nil {
		return
	}
	if !a.isProgramRegion(containerOrEntryRegion(expected)) {
		return
	}
	a.checkNestedRegionStoreEscape(arg, arg, expected, actual)
}

// analyzeRegionOwnerExpr analyzes an expression in region-owner position (`new[X]`,
// `in X:`, a literal's owner), the one place a program-lived region may be named as a value.
func (a *Analyzer) analyzeRegionOwnerExpr(expr ast.Expr) Type {
	if _, bare := stripParenExpr(expr).(*ast.Ident); !bare {
		return a.analyzeExpr(expr) // only a bare name is exempt, never `&source` inside a call
	}
	a.programRegionNameUse++
	t := a.analyzeExpr(expr)
	a.programRegionNameUse--
	return t
}

// checkDeclaredStoreRegions validates `enum Root layout(...) @R:` (docs/128 §8): R must be a
// program-lived region, the enum a store-backed root.
func (a *Analyzer) checkDeclaredStoreRegions(decls []scopedDecl) {
	for _, scoped := range decls {
		n, ok := scoped.Decl.(*ast.EnumDecl)
		if !ok || n.StoreRegion == "" {
			continue
		}
		if n.Parent != "" {
			a.errorf(n.Pos(), "enum %q: a store region is declared on the hierarchy root only; refinements inherit it", n.Name)
			continue
		}
		if !a.isProgramRegion(n.StoreRegion) {
			a.errorf(n.Pos(), "enum %q: store region %q must be a program-lived region (`global region %s`)", n.Name, n.StoreRegion, n.StoreRegion)
			continue
		}
		if !n.Packed {
			a.errorf(n.Pos(), "enum %q: a store region applies only to a store-backed enum (recursive, or a hierarchy root)", n.Name)
		}
	}
}

// checkDeclaredStorePayload: a node of a declared program-lived store (docs/128 §8) lives
// until exit, so every region-carrying payload is a store into that region, annotated or
// not. Without this a view of a caller's region would sit in a node that outlives it.
func (a *Analyzer) checkDeclaredStorePayload(enumType *EnumType, arg ast.Expr, payload, actual Type) {
	if enumType == nil || arg == nil {
		return
	}
	region := enumType.DeclaredStoreRegion()
	if region == "" {
		return
	}
	stamped, carries, conflicts := stampAggregateRegion(payload, region)
	if !carries || conflicts {
		return // no region-carrying slot of its own, or already typed with a region (checked by rule 3)
	}
	a.checkProgramRegionSlotStore(arg, stamped, actual)
}
