package semantic

import (
	"sort"

	"elisacore/src/ast"
)

func TreeStoreImplicitParamName(family *TreeType) string {
	if family == nil || family.Name == "" {
		return "__tree_store"
	}
	return "__tree_store_" + sanitizeImplicitTempBase(family.Name)
}

func treeStoreTypeForImplicitContext(t Type) (*TreeStoreType, bool) {
	if t == nil {
		return nil, false
	}
	if ref, ok := t.(*RefType); ok {
		t = ref.Elem
	}
	t = StripAggregateStateType(t)
	var family *TreeType
	switch tt := t.(type) {
	case *TreeNodeType:
		family = tt.Family
	case *TreeCategoryType:
		family = tt.Family
	case *TreeVariantViewType:
		if tt.Category != nil {
			family = tt.Category.Family
		}
	case *TreeBlockType:
		family = tt.Family
	case *TreeStructType:
		family = tt.Family
	}
	if family == nil || family.Layout != TreeLayoutCategoryUnion || family.StoreType == nil {
		return nil, false
	}
	return family.StoreType, true
}

func (a *Analyzer) recordImplicitTreeStoreUse(storeType *TreeStoreType) {
	if a == nil || a.currentFuncType == nil || storeType == nil || storeType.Family == nil {
		return
	}
	if a.currentFunctionUsedTreeStores == nil {
		a.currentFunctionUsedTreeStores = map[string]*TreeStoreType{}
	}
	a.currentFunctionUsedTreeStores[storeType.Family.Name] = storeType
}

func (a *Analyzer) recordImplicitTreeStoreUseForType(t Type) {
	if storeType, ok := treeStoreTypeForImplicitContext(t); ok {
		a.recordImplicitTreeStoreUse(storeType)
	}
}

func (a *Analyzer) recordImplicitTreeStoreUseForFamily(family *TreeType) {
	if family == nil || family.Layout != TreeLayoutCategoryUnion || family.StoreType == nil {
		return
	}
	a.recordImplicitTreeStoreUse(family.StoreType)
}

func (a *Analyzer) recordImplicitTreeStoreUseForTreeAttribute(attr *TreeAttribute) {
	if a == nil || attr == nil {
		return
	}
	recordAttr := func(candidate *TreeAttribute) {
		if candidate == nil {
			return
		}
		root, ok := treeAttributeRootInfoForSemantic(candidate.Receiver)
		if !ok {
			return
		}
		a.recordImplicitTreeStoreUseForFamily(root)
	}
	recordAttr(attr)
	for _, attrs := range a.treeAttributes {
		for _, candidate := range attrs {
			if candidate != nil && candidate.Name == attr.Name {
				recordAttr(candidate)
			}
		}
	}
}

func treeAttributeRootInfoForSemantic(receiver Type) (*TreeType, bool) {
	switch tt := StripAggregateStateType(receiver).(type) {
	case *TreeNodeType:
		if tt != nil {
			return tt.Family, true
		}
	case *TreeCategoryType:
		if tt != nil {
			return tt.Family, true
		}
	case *TreeVariantViewType:
		if tt != nil && tt.Category != nil {
			return tt.Category.Family, true
		}
	case *TreeBlockType:
		if tt != nil {
			return tt.Family, true
		}
	case *TreeStructType:
		if tt != nil {
			return tt.Family, true
		}
	}
	return nil, false
}

func treeStoreImplicitArgExpr(storeType *TreeStoreType) ast.Expr {
	return &ast.Ident{Name: TreeStoreImplicitParamName(storeType.Family)}
}

func funcTypeHasImplicitParam(fnType *FuncType, name string) bool {
	if fnType == nil || name == "" {
		return false
	}
	for _, existing := range fnType.ImplicitParamNames {
		if existing == name {
			return true
		}
	}
	return false
}

func appendInferredTreeStoreParams(fnType *FuncType, stores map[string]*TreeStoreType) {
	if fnType == nil || len(stores) == 0 {
		if fnType == nil {
			return
		}
	}
	exposedFamilies := treeFamiliesExposedByFunctionBoundary(fnType)
	if len(exposedFamilies) == 0 {
		return
	}
	candidates := make(map[string]*TreeStoreType, len(stores)+len(exposedFamilies))
	for familyName, storeType := range exposedFamilies {
		candidates[familyName] = storeType
	}
	for familyName, storeType := range stores {
		candidates[familyName] = storeType
	}
	families := make([]string, 0, len(candidates))
	for familyName := range candidates {
		families = append(families, familyName)
	}
	sort.Strings(families)
	for _, familyName := range families {
		storeType := candidates[familyName]
		if storeType == nil || storeType.Family == nil {
			continue
		}
		name := TreeStoreImplicitParamName(storeType.Family)
		if funcTypeHasImplicitParam(fnType, name) {
			continue
		}
		fnType.Params = append(fnType.Params, storeType)
		fnType.ImplicitParamNames = append(fnType.ImplicitParamNames, name)
	}
}

func treeFamiliesExposedByFunctionBoundary(fnType *FuncType) map[string]*TreeStoreType {
	out := map[string]*TreeStoreType{}
	if fnType == nil {
		return out
	}
	explicitCount := fnType.ExplicitParamCount
	if explicitCount == 0 && len(fnType.ImplicitParamNames) == 0 {
		explicitCount = len(fnType.Params)
	}
	if explicitCount > len(fnType.Params) {
		explicitCount = len(fnType.Params)
	}
	for i := 0; i < explicitCount; i++ {
		collectTreeFamiliesInType(fnType.Params[i], out, map[*StructType]bool{})
	}
	collectTreeFamiliesInType(fnType.Return, out, map[*StructType]bool{})
	return out
}

func collectTreeFamiliesInType(t Type, out map[string]*TreeStoreType, seenStructs map[*StructType]bool) {
	if t == nil {
		return
	}
	switch tt := t.(type) {
	case *RefType:
		collectTreeFamiliesInType(tt.Elem, out, seenStructs)
	case *ErrorUnionType:
		collectTreeFamiliesInType(tt.Value, out, seenStructs)
	case *OptionalType:
		collectTreeFamiliesInType(tt.Value, out, seenStructs)
	case *TupleType:
		for _, field := range tt.Fields {
			collectTreeFamiliesInType(field.Type, out, seenStructs)
		}
	case *ArrayType:
		collectTreeFamiliesInType(tt.Elem, out, seenStructs)
	case *DArrayType:
		collectTreeFamiliesInType(tt.Elem, out, seenStructs)
	case *ViewType:
		collectTreeFamiliesInType(tt.Elem, out, seenStructs)
	case *DArrayViewType:
		collectTreeFamiliesInType(tt.Elem, out, seenStructs)
	case *DictType:
		collectTreeFamiliesInType(tt.Key, out, seenStructs)
		collectTreeFamiliesInType(tt.Value, out, seenStructs)
	case *DictEntryType:
		if tt.Dict != nil {
			collectTreeFamiliesInType(tt.Dict, out, seenStructs)
		}
	case *GenericInstanceType:
		collectTreeFamiliesInType(tt.Base, out, seenStructs)
		for _, arg := range tt.Args {
			collectTreeFamiliesInType(arg, out, seenStructs)
		}
	case *AggregateStateType:
		collectTreeFamiliesInType(tt.Base, out, seenStructs)
	case *StructType:
		if tt == nil || seenStructs[tt] {
			return
		}
		seenStructs[tt] = true
		for _, field := range tt.Fields {
			collectTreeFamiliesInType(field.Type, out, seenStructs)
		}
	case *TreeNodeType:
		if tt.Family != nil && tt.Family.Layout == TreeLayoutCategoryUnion && tt.Family.StoreType != nil {
			out[tt.Family.Name] = tt.Family.StoreType
		}
	case *TreeCategoryType:
		if tt.Family != nil && tt.Family.Layout == TreeLayoutCategoryUnion && tt.Family.StoreType != nil {
			out[tt.Family.Name] = tt.Family.StoreType
		}
	case *TreeVariantViewType:
		if tt.Category != nil && tt.Category.Family != nil && tt.Category.Family.Layout == TreeLayoutCategoryUnion && tt.Category.Family.StoreType != nil {
			out[tt.Category.Family.Name] = tt.Category.Family.StoreType
		}
	case *TreeBlockType:
		if tt.Family != nil && tt.Family.Layout == TreeLayoutCategoryUnion && tt.Family.StoreType != nil {
			out[tt.Family.Name] = tt.Family.StoreType
		}
	case *TreeStructType:
		if tt.Family != nil && tt.Family.Layout == TreeLayoutCategoryUnion && tt.Family.StoreType != nil {
			out[tt.Family.Name] = tt.Family.StoreType
		}
	case *TreeStoreType:
		if tt.Family != nil && tt.Family.Layout == TreeLayoutCategoryUnion {
			out[tt.Family.Name] = tt
		}
	}
}

func implicitTreeOwnerParamName(name string) bool {
	return name == "owner" || name == "alloc"
}

func (a *Analyzer) inferFunctionTreeAllocOwnerFromParams(params []ast.ParamDecl, fnType *FuncType) (treeAllocOwnerBinding, bool) {
	if a == nil || fnType == nil {
		return treeAllocOwnerBinding{}, false
	}
	arenaType := a.namedTypes["Arena"]
	limit := len(params)
	if len(fnType.Params) < limit {
		limit = len(fnType.Params)
	}
	for i := 0; i < limit; i++ {
		if !implicitTreeOwnerParamName(params[i].Name) {
			continue
		}
		ptype := fnType.Params[i]
		if storeType, ok := ptype.(*TreeStoreType); ok && storeType != nil {
			return treeAllocOwnerBinding{Kind: treeAllocOwnerStore, StoreFamily: storeType.Family}, true
		}
		if arenaType != nil && SameType(ptype, arenaType) {
			return treeAllocOwnerBinding{Kind: treeAllocOwnerArena}, true
		}
		if ref, ok := ptype.(*RefType); ok && ref != nil && arenaType != nil && SameType(ref.Elem, arenaType) {
			return treeAllocOwnerBinding{Kind: treeAllocOwnerArena}, true
		}
	}
	return treeAllocOwnerBinding{}, false
}
