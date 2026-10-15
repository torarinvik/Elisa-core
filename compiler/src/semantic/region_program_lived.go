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
