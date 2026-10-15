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

// typeIsProgramLived reports that every byte a value of type t can reach is program-lived
// (docs/128 §8): scalars; handles into a declared program-lived store whose payloads are all
// program-lived; and region-carrying types whose own region is program-lived, with
// program-lived elements. Anything unannotated keeps its normal (unrecorded) treatment.
func (a *Analyzer) typeIsProgramLived(t Type, seen map[Type]bool) bool {
	if t == nil {
		return false
	}
	if seen[t] {
		return true // a recursive reference is judged by the rest of the walk
	}
	switch tt := t.(type) {
	case *EnumType:
		if tt.DeclaredStoreRegion() == "" {
			return !a.typeCanContainRegionRefs(t, map[string]bool{})
		}
		seen[t] = true
		root := tt.Root()
		for _, field := range root.Common {
			if !a.typeIsProgramLived(field.Type, seen) {
				return false
			}
		}
		return a.enumPayloadsProgramLived(root, seen)
	case *OptionalType:
		return a.typeIsProgramLived(tt.Value, seen)
	case *RefType:
		return a.isProgramRegion(tt.Region) && a.typeIsProgramLived(tt.Elem, seen)
	case *SViewType:
		return a.isProgramRegion(tt.Region)
	case *DArrayType:
		return a.isProgramRegion(tt.Region) && a.typeIsProgramLived(tt.Elem, seen)
	case *StructType:
		seen[t] = true
		for _, field := range tt.Fields {
			if !a.typeIsProgramLived(field.Type, seen) {
				return false
			}
		}
		return true
	}
	return !a.typeCanContainRegionRefs(t, map[string]bool{})
}

// enumPayloadsProgramLived walks every variant of a hierarchy root and its refinements.
func (a *Analyzer) enumPayloadsProgramLived(root *EnumType, seen map[Type]bool) bool {
	for _, named := range a.namedTypes {
		enum, ok := named.(*EnumType)
		if !ok || enum.Root() != root {
			continue
		}
		for _, variant := range enum.Variants {
			for _, payload := range variant.Payload {
				if !a.typeIsProgramLived(payload, seen) {
					return false
				}
			}
		}
	}
	return true
}

// typeViewsProgramLived: every VIEW reachable inside a value of t (sviews, refs, payload views)
// points at program-lived bytes. Unlike typeIsProgramLived it ignores where t's own container
// storage lives: it answers what a view-only value read out of t can point at (storeViews).
func (a *Analyzer) typeViewsProgramLived(t Type, seen map[Type]bool) bool {
	if t == nil {
		return false
	}
	if seen[t] {
		return true
	}
	switch tt := t.(type) {
	case *DArrayType:
		seen[t] = true
		return a.typeViewsProgramLived(tt.Elem, seen)
	case *ArrayType:
		seen[t] = true
		return a.typeViewsProgramLived(tt.Elem, seen)
	case *OptionalType:
		return a.typeViewsProgramLived(tt.Value, seen)
	case *TupleType:
		seen[t] = true
		for _, field := range tt.Fields {
			if !a.typeViewsProgramLived(field.Type, seen) {
				return false
			}
		}
		return true
	case *StructType:
		seen[t] = true
		for _, field := range tt.Fields {
			if !a.typeViewsProgramLived(field.Type, seen) {
				return false
			}
		}
		return true
	}
	return a.typeIsProgramLived(t, map[Type]bool{})
}

// typeIsProgramLivedCached memoizes typeIsProgramLived per type (types are immutable once
// declarations are collected).
func (a *Analyzer) typeIsProgramLivedCached(t Type) bool {
	if verdict, ok := a.programLivedTypes[t]; ok {
		return verdict
	}
	verdict := a.typeIsProgramLived(t, map[Type]bool{})
	if a.programLivedTypes == nil {
		a.programLivedTypes = map[Type]bool{}
	}
	a.programLivedTypes[t] = verdict
	return verdict
}

// darrayElemsProgramLived: t is (a reference to) a darray whose element type is program-lived
// and can hold region references -- its element VALUES reach only program-region storage.
func (a *Analyzer) darrayElemsProgramLived(t Type) bool {
	da, ok := stripRefForBounds(t).(*DArrayType)
	if !ok || da == nil || da.Elem == nil || containsTypeParam(da.Elem) {
		return false
	}
	return a.typeCanContainRegionRefs(da.Elem, map[string]bool{}) && a.typeIsProgramLivedCached(da.Elem)
}
