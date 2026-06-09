package semantic

import (
	"sort"
	"strings"

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
	if a.currentTreeAllocOwner.Kind == treeAllocOwnerStore || a.currentTreeAllocOwner.Kind == treeAllocOwnerPerm {
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

func (a *Analyzer) recoverImplicitTreeStoreOwnerArg(expr *ast.CallExpr, fnType *FuncType, explicitCount int) (ast.Expr, bool) {
	if a == nil || expr == nil || fnType == nil {
		return nil, false
	}
	if a.currentTreeAllocOwner.Kind == treeAllocOwnerStore || a.currentTreeAllocOwner.Kind == treeAllocOwnerPerm {
		return nil, false
	}
	args := expr.Args
	if expr.ResolvedArgsValid && expr.ResolvedCommonArgs == nil {
		args = expr.ResolvedArgs
	}
	if explicitCount > len(fnType.Params) {
		explicitCount = len(fnType.Params)
	}
	if explicitCount > len(args) {
		explicitCount = len(args)
	}
	for i := 0; i < explicitCount; i++ {
		if a.typeCanCreateTreeStore(fnType.Params[i]) {
			return args[i], true
		}
	}
	return nil, false
}

func (a *Analyzer) typeCanCreateTreeStore(t Type) bool {
	if a == nil || t == nil {
		return false
	}
	arenaType := a.namedTypes["Arena"]
	if arenaType == nil {
		return false
	}
	if SameType(t, arenaType) {
		return true
	}
	refType, ok := t.(*RefType)
	return ok && refType != nil && SameType(refType.Elem, arenaType)
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
	if len(exposedFamilies) == 0 && len(stores) == 0 {
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
		if funcTypeHasImplicitParam(fnType, name) || funcTypeHasTreeStoreParamForFamily(fnType, storeType.Family) {
			continue
		}
		fnType.Params = append(fnType.Params, storeType)
		fnType.ImplicitParamNames = append(fnType.ImplicitParamNames, name)
	}
}

func AppendBoundaryTreeStoreParams(fnType *FuncType) {
	appendInferredTreeStoreParams(fnType, nil)
}

func AppendSpecializedBoundaryTreeStoreParams(fnType *FuncType) {
	appendInferredReturnTreeStoreParams(fnType)
}

func appendInferredReturnTreeStoreParams(fnType *FuncType) {
	if fnType == nil || funcTypeSkipsReturnExposedTreeStores(fnType) {
		return
	}
	exposedFamilies := map[string]*TreeStoreType{}
	collectTreeFamiliesInType(fnType.Return, exposedFamilies, map[*StructType]bool{}, map[string]bool{})
	if len(exposedFamilies) == 0 {
		return
	}
	names := make([]string, 0, len(exposedFamilies))
	for name := range exposedFamilies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, familyName := range names {
		storeType := exposedFamilies[familyName]
		if storeType == nil || storeType.Family == nil {
			continue
		}
		name := TreeStoreImplicitParamName(storeType.Family)
		if funcTypeHasImplicitParam(fnType, name) || funcTypeHasTreeStoreParamForFamily(fnType, storeType.Family) {
			continue
		}
		fnType.Params = append(fnType.Params, storeType)
		fnType.ImplicitParamNames = append(fnType.ImplicitParamNames, name)
	}
}

func funcTypeHasTreeStoreParamForFamily(fnType *FuncType, family *TreeType) bool {
	if fnType == nil || family == nil {
		return false
	}
	for _, param := range fnType.Params {
		storeType, ok := param.(*TreeStoreType)
		if ok && storeType != nil && storeType.Family == family {
			return true
		}
	}
	return false
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
		collectTreeFamiliesInType(fnType.Params[i], out, map[*StructType]bool{}, map[string]bool{})
	}
	if !funcTypeSkipsReturnExposedTreeStores(fnType) {
		collectTreeFamiliesInType(fnType.Return, out, map[*StructType]bool{}, map[string]bool{})
	}
	return out
}

func funcTypeSkipsReturnExposedTreeStores(fnType *FuncType) bool {
	if fnType == nil {
		return false
	}
	return fnType.Name == "__cast__" || strings.HasSuffix(fnType.Name, ".__cast__")
}

func collectTreeFamiliesInType(t Type, out map[string]*TreeStoreType, seenStructs map[*StructType]bool, seenTrees map[string]bool) {
	if t == nil {
		return
	}
	switch tt := t.(type) {
	case *RefType:
		collectTreeFamiliesInRefElem(tt.Elem, out, seenStructs, seenTrees)
	case *ErrorUnionType:
		collectTreeFamiliesInType(tt.Value, out, seenStructs, seenTrees)
	case *OptionalType:
		collectTreeFamiliesInType(tt.Value, out, seenStructs, seenTrees)
	case *TupleType:
		for _, field := range tt.Fields {
			collectTreeFamiliesInType(field.Type, out, seenStructs, seenTrees)
		}
	case *ArrayType:
		collectTreeFamiliesInType(tt.Elem, out, seenStructs, seenTrees)
	case *DArrayType:
		collectTreeFamiliesInType(tt.Elem, out, seenStructs, seenTrees)
	case *ViewType:
		collectTreeFamiliesInType(tt.Elem, out, seenStructs, seenTrees)
	case *DictType:
		collectTreeFamiliesInType(tt.Key, out, seenStructs, seenTrees)
		collectTreeFamiliesInType(tt.Value, out, seenStructs, seenTrees)
	case *DictEntryType:
		if tt.Dict != nil {
			collectTreeFamiliesInType(tt.Dict, out, seenStructs, seenTrees)
		}
	case *GenericInstanceType:
		collectTreeFamiliesInType(tt.Base, out, seenStructs, seenTrees)
		for _, arg := range tt.Args {
			collectTreeFamiliesInType(arg, out, seenStructs, seenTrees)
		}
	case *AggregateStateType:
		collectTreeFamiliesInType(tt.Base, out, seenStructs, seenTrees)
	case *StructType:
		if tt == nil || seenStructs[tt] {
			return
		}
		seenStructs[tt] = true
		for _, field := range tt.Fields {
			collectTreeFamiliesInType(field.Type, out, seenStructs, seenTrees)
		}
	case *EnumType:
		if tt == nil {
			return
		}
		key := "enum:" + tt.Name
		if seenTrees[key] {
			return
		}
		seenTrees[key] = true
		for _, field := range tt.Common {
			collectTreeFamiliesInType(field.Type, out, seenStructs, seenTrees)
		}
		for _, variant := range tt.Variants {
			if variant == nil {
				continue
			}
			for _, payloadType := range variant.Payload {
				collectTreeFamiliesInType(payloadType, out, seenStructs, seenTrees)
			}
		}
	case *PackedVariantViewType:
		if tt == nil || tt.Enum == nil || tt.Variant == nil {
			return
		}
		key := "packed-variant:" + tt.Enum.Name + "." + tt.Variant.Name
		if seenTrees[key] {
			return
		}
		seenTrees[key] = true
		for _, field := range tt.Enum.Common {
			collectTreeFamiliesInType(field.Type, out, seenStructs, seenTrees)
		}
		for _, payloadType := range tt.Variant.Payload {
			collectTreeFamiliesInType(payloadType, out, seenStructs, seenTrees)
		}
	case *TreeNodeType:
		if tt.Family != nil && tt.Family.Layout == TreeLayoutCategoryUnion && tt.Family.StoreType != nil {
			out[tt.Family.Name] = tt.Family.StoreType
			collectTreeFamilyPayloadFamilies(tt.Family, out, seenStructs, seenTrees)
		}
	case *TreeCategoryType:
		if tt.Family != nil && tt.Family.Layout == TreeLayoutCategoryUnion && tt.Family.StoreType != nil {
			out[tt.Family.Name] = tt.Family.StoreType
			collectTreeCategoryPayloadFamilies(tt, out, seenStructs, seenTrees)
		}
	case *TreeVariantViewType:
		if tt.Category != nil && tt.Category.Family != nil && tt.Category.Family.Layout == TreeLayoutCategoryUnion && tt.Category.Family.StoreType != nil {
			out[tt.Category.Family.Name] = tt.Category.Family.StoreType
			collectTreeVariantPayloadFamilies(tt, out, seenStructs, seenTrees)
		}
	case *TreeBlockType:
		if tt.Family != nil && tt.Family.Layout == TreeLayoutCategoryUnion && tt.Family.StoreType != nil {
			out[tt.Family.Name] = tt.Family.StoreType
			collectTreeBlockPayloadFamilies(tt, out, seenStructs, seenTrees)
		}
	case *TreeStructType:
		if tt.Family != nil && tt.Family.Layout == TreeLayoutCategoryUnion && tt.Family.StoreType != nil {
			out[tt.Family.Name] = tt.Family.StoreType
			collectTreeStructPayloadFamilies(tt, out, seenStructs, seenTrees)
		}
	case *TreeStoreType:
		if tt.Family != nil && tt.Family.Layout == TreeLayoutCategoryUnion {
			out[tt.Family.Name] = tt
		}
	}
}

func collectTreeFamiliesInRefElem(t Type, out map[string]*TreeStoreType, seenStructs map[*StructType]bool, seenTrees map[string]bool) {
	if t == nil {
		return
	}
	switch StripAggregateStateType(t).(type) {
	case *TreeNodeType, *TreeCategoryType, *TreeVariantViewType, *TreeBlockType, *TreeStructType, *TreeStoreType:
		collectTreeFamiliesInType(t, out, seenStructs, seenTrees)
	case *ArrayType, *DArrayType, *ViewType, *TupleType, *DictType, *DictEntryType, *GenericInstanceType, *AggregateStateType, *StructType, *EnumType, *PackedVariantViewType:
		collectTreeFamiliesInType(t, out, seenStructs, seenTrees)
	}
}

func collectTreeFamilyPayloadFamilies(family *TreeType, out map[string]*TreeStoreType, seenStructs map[*StructType]bool, seenTrees map[string]bool) {
	if family == nil {
		return
	}
	key := "family:" + family.Name
	if seenTrees[key] {
		return
	}
	seenTrees[key] = true
	for _, member := range family.MemberTypes {
		collectTreeFamiliesInType(member, out, seenStructs, seenTrees)
	}
	for _, field := range family.Common {
		collectTreeFamiliesInType(field.Type, out, seenStructs, seenTrees)
	}
}

func collectTreeCategoryPayloadFamilies(category *TreeCategoryType, out map[string]*TreeStoreType, seenStructs map[*StructType]bool, seenTrees map[string]bool) {
	if category == nil {
		return
	}
	key := "category:" + category.Name
	if seenTrees[key] {
		return
	}
	seenTrees[key] = true
	for _, field := range category.Common {
		collectTreeFamiliesInType(field.Type, out, seenStructs, seenTrees)
	}
	for _, variant := range category.Variants {
		collectTreeVariantPayloadFamilies(&TreeVariantViewType{Category: category, Variant: variant}, out, seenStructs, seenTrees)
	}
}

func collectTreeVariantPayloadFamilies(view *TreeVariantViewType, out map[string]*TreeStoreType, seenStructs map[*StructType]bool, seenTrees map[string]bool) {
	if view == nil || view.Variant == nil {
		return
	}
	if view.Category != nil {
		key := "variant:" + view.Category.Name + "." + view.Variant.Name
		if seenTrees[key] {
			return
		}
		seenTrees[key] = true
	}
	for _, fieldType := range view.Variant.Payload {
		collectTreeFamiliesInType(fieldType, out, seenStructs, seenTrees)
	}
	if view.Category != nil {
		for _, field := range view.Category.Common {
			collectTreeFamiliesInType(field.Type, out, seenStructs, seenTrees)
		}
	}
}

func collectTreeBlockPayloadFamilies(block *TreeBlockType, out map[string]*TreeStoreType, seenStructs map[*StructType]bool, seenTrees map[string]bool) {
	if block == nil {
		return
	}
	key := "block:" + block.Name
	if seenTrees[key] {
		return
	}
	seenTrees[key] = true
	for _, field := range block.Fields {
		collectTreeFamiliesInType(field.Type, out, seenStructs, seenTrees)
	}
	if block.Family != nil {
		for _, field := range block.Family.Common {
			collectTreeFamiliesInType(field.Type, out, seenStructs, seenTrees)
		}
	}
}

func collectTreeStructPayloadFamilies(structType *TreeStructType, out map[string]*TreeStoreType, seenStructs map[*StructType]bool, seenTrees map[string]bool) {
	if structType == nil {
		return
	}
	key := "struct:" + structType.Name
	if seenTrees[key] {
		return
	}
	seenTrees[key] = true
	for _, field := range structType.Fields {
		collectTreeFamiliesInType(field.Type, out, seenStructs, seenTrees)
	}
	if structType.Family != nil {
		for _, field := range structType.Family.Common {
			collectTreeFamiliesInType(field.Type, out, seenStructs, seenTrees)
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
