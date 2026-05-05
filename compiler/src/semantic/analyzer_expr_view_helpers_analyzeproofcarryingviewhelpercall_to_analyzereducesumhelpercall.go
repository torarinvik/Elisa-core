package semantic

import (
	"elisacore/src/ast"
	"fmt"
)

func (a *Analyzer) analyzeProofCarryingViewHelperCall(expr *ast.CallExpr) (Type, bool) {
	switch callIdentName(expr) {
	case "any":
		return a.analyzeIterableBoolAggregateHelperCall(expr, "any"), true
	case "all":
		return a.analyzeIterableBoolAggregateHelperCall(expr, "all"), true
	case "enumerate":
		return a.analyzeEnumerateHelperCall(expr), true
	case "readonly":
		return a.analyzeReadonlyHelperCall(expr), true
	case "split_at":
		return a.analyzeSplitAtHelperCall(expr), true
	case "chunks_exact":
		return a.analyzeChunksExactHelperCall(expr), true
	case "reduce_sum":
		return a.analyzeReduceSumHelperCall(expr), true
	case "zip_map":
		return a.analyzeZipMapHelperCall(expr), true
	default:
		return nil, false
	}
}
func (a *Analyzer) analyzeIterableBoolAggregateHelperCall(expr *ast.CallExpr, helperName string) Type {
	if len(expr.Args) != 1 {
		a.errorf(expr.Pos(), "%s expects 1 argument, got %d", helperName, len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	sourceType := a.analyzeExpr(expr.Args[0])
	info, ok := a.resolveIterLoopSourceInfo(expr.Args[0], sourceType)
	if !ok {
		a.errorf(expr.Args[0].Pos(), "%s expects an iterable source, got %s", helperName, sourceType)
		return invalidType
	}
	boolType := a.namedTypes["bool"]
	if !AssignableTo(boolType, info.ItemType) {
		a.errorf(expr.Args[0].Pos(), "%s expects an iterable of bool, got %s", helperName, info.ItemType)
		return invalidType
	}
	return boolType
}
func (a *Analyzer) analyzeEnumerateHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) != 1 {
		a.errorf(expr.Pos(), "enumerate expects 1 argument, got %d", len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	sourceType := a.analyzeExpr(expr.Args[0])
	info, ok := a.resolveIterLoopSourceInfo(expr.Args[0], sourceType)
	if !ok {
		a.errorf(expr.Args[0].Pos(), "enumerate expects an iterable source, got %s", sourceType)
		return invalidType
	}
	base, ok := a.namedTypes["EnumerateView"].(*StructType)
	if !ok || base == nil {
		a.errorf(expr.Pos(), "missing builtin EnumerateView carrier type")
		return invalidType
	}
	itemType := EnumerateTupleType(info.ItemType)
	if itemType == nil {
		a.errorf(expr.Pos(), "enumerate requires a concrete iterable item type")
		return invalidType
	}
	return &GenericInstanceType{Name: "EnumerateView", Base: base, Args: []Type{sourceType, itemType}}
}

type treeChildrenSourceKind int

const (
	treeChildrenSourceInvalid treeChildrenSourceKind = iota
	treeChildrenSourceCategory
	treeChildrenSourceExact
	treeChildrenSourceFamily
)

type treeChildrenSource struct {
	Kind     treeChildrenSourceKind
	Category *TreeCategoryType
	Variant  *EnumVariant
	Exact    Type
	Family   *TreeType
}

func resolveTreeChildrenSourceInfo(sourceType Type) (treeChildrenSource, bool) {
	switch tt := StripAggregateStateType(sourceType).(type) {
	case *TreeCategoryType:
		if tt == nil {
			return treeChildrenSource{}, false
		}
		return treeChildrenSource{Kind: treeChildrenSourceCategory, Category: tt, Family: tt.Family}, true
	case *TreeVariantViewType:
		if tt == nil || tt.Category == nil || tt.Variant == nil {
			return treeChildrenSource{}, false
		}
		return treeChildrenSource{Kind: treeChildrenSourceCategory, Category: tt.Category, Variant: tt.Variant, Family: tt.Category.Family}, true
	case *TreeBlockType:
		if tt == nil {
			return treeChildrenSource{}, false
		}
		return treeChildrenSource{Kind: treeChildrenSourceExact, Exact: tt, Family: tt.Family}, true
	case *TreeStructType:
		if tt == nil {
			return treeChildrenSource{}, false
		}
		return treeChildrenSource{Kind: treeChildrenSourceExact, Exact: tt, Family: tt.Family}, true
	case *TreeNodeType:
		if tt == nil {
			return treeChildrenSource{}, false
		}
		return treeChildrenSource{Kind: treeChildrenSourceFamily, Family: tt.Family}, true
	default:
		return treeChildrenSource{}, false
	}
}
func appendTreeStructuralChildCandidates(candidates *[]Type, sourceType Type) {
	switch tt := StripAggregateStateType(sourceType).(type) {
	case *TreeCategoryType:
		if tt == nil {
			return
		}
		for _, variant := range tt.Variants {
			appendTreeVariantStructuralChildCandidates(candidates, variant)
		}
	case *TreeVariantViewType:
		if tt == nil || tt.Variant == nil {
			return
		}
		appendTreeVariantStructuralChildCandidates(candidates, tt.Variant)
	case *TreeBlockType:
		appendTreeExactStructuralChildCandidates(candidates, tt)
	case *TreeStructType:
		appendTreeExactStructuralChildCandidates(candidates, tt)
	case *TreeNodeType:
		if tt == nil || tt.Family == nil {
			return
		}
		for _, member := range TreeFamilyExactMembersInTagOrder(tt.Family) {
			appendTreeStructuralChildCandidates(candidates, member)
		}
	}
}
func appendTreeVariantStructuralChildCandidates(candidates *[]Type, variant *EnumVariant) {
	if variant == nil {
		return
	}
	for payloadIndex, payloadType := range variant.Payload {
		relation := variant.PayloadRelation(payloadIndex)
		if itemType, ok := TreeStructuralChildItemType(payloadType, relation); ok && itemType != nil {
			*candidates = append(*candidates, itemType)
		}
	}
}
func appendTreeExactStructuralChildCandidates(candidates *[]Type, exact Type) {
	family, ok := TreeFamilyForMemberType(exact)
	if !ok || family == nil {
		return
	}
	var decls []ast.FieldDecl
	switch tt := StripAggregateStateType(exact).(type) {
	case *TreeBlockType:
		decls = TreeBlockFieldDeclsWithCommon(tt)
	case *TreeStructType:
		decls = TreeStructFieldDeclsWithCommon(tt)
	default:
		return
	}
	for _, fieldDecl := range decls {
		field, ok := TreeExactFieldInfo(exact, fieldDecl.Name)
		if !ok {
			continue
		}
		relation := TreeFieldStructuralRelation(family, field.Type)
		if itemType, ok := TreeStructuralChildItemType(field.Type, relation); ok && itemType != nil {
			*candidates = append(*candidates, itemType)
		}
	}
}
func treeChildrenCandidateItemType(sourceType Type) (Type, bool) {
	var candidates []Type
	appendTreeStructuralChildCandidates(&candidates, sourceType)
	if len(candidates) == 0 {
		return nil, false
	}
	itemType := candidates[0]
	for _, candidate := range candidates[1:] {
		if !SameType(itemType, candidate) {
			return invalidType, true
		}
	}
	return itemType, true
}
func treeChildrenOverrideItemType(sourceType Type, overrideType Type) (Type, string, bool) {
	if sourceType == nil || overrideType == nil {
		return nil, "", false
	}
	if _, ok := resolveTreeChildrenSourceInfo(overrideType); !ok {
		return nil, "", false
	}
	var candidates []Type
	appendTreeStructuralChildCandidates(&candidates, sourceType)
	if len(candidates) == 0 {
		return nil, fmt.Sprintf("children(...) requires at least one structural child edge on %s", treeChildrenSourceLabel(sourceType)), true
	}
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		if !AssignableTo(overrideType, candidate) {
			return nil, fmt.Sprintf("children(...) override %s is incompatible with structural child %s", overrideType.String(), candidate.String()), true
		}
	}
	return overrideType, "", true
}
func treeChildrenSourceLabel(sourceType Type) string {
	switch tt := StripAggregateStateType(sourceType).(type) {
	case *TreeCategoryType:
		if tt != nil {
			return tt.Name
		}
	case *TreeVariantViewType:
		if tt != nil && tt.Category != nil && tt.Variant != nil {
			return tt.Category.Name + "." + tt.Variant.Name
		}
	case *TreeBlockType:
		if tt != nil {
			return tt.Name
		}
	case *TreeStructType:
		if tt != nil {
			return tt.Name
		}
	case *TreeNodeType:
		if tt != nil {
			return tt.Name
		}
	}
	return sourceType.String()
}
func (a *Analyzer) analyzeTreeTraversalHelperCall(expr *ast.CallExpr) (Type, bool) {
	switch callIdentName(expr) {
	case "children":
		return a.analyzeChildrenHelperCall(expr), true
	default:
		return nil, false
	}
}
func proofCarryingViewType(t Type) bool {
	switch t.(type) {
	case *ViewType, *DArrayViewType, *DStrType, *SViewType:
		return true
	default:
		return false
	}
}
func (a *Analyzer) analyzeChildrenHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) != 1 {
		a.errorf(expr.Pos(), "children expects 1 argument, got %d", len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	sourceExpr := expr.Args[0]
	sourceType := a.analyzeExpr(sourceExpr)
	carrierSourceType := sourceType
	if castExpr, ok := sourceExpr.(*ast.CastExpr); ok {
		actualSourceType := a.exprTypes[castExpr.Operand]
		if actualSourceType == nil {
			actualSourceType = a.analyzeExpr(castExpr.Operand)
		}
		if overrideItemType, diag, handled := treeChildrenOverrideItemType(actualSourceType, sourceType); handled {
			if diag != "" {
				a.errorf(sourceExpr.Pos(), "%s", diag)
				return invalidType
			}
			carrierSourceType = actualSourceType
			base, ok := a.namedTypes["TreeChildren"].(*StructType)
			if !ok || base == nil {
				a.errorf(expr.Pos(), "missing builtin TreeChildren carrier type")
				return invalidType
			}
			return &GenericInstanceType{Name: "TreeChildren", Base: base, Args: []Type{carrierSourceType, overrideItemType}}
		}
	}
	sourceInfo, ok := resolveTreeChildrenSourceInfo(carrierSourceType)
	if !ok {
		a.errorf(expr.Args[0].Pos(), "children expects a tree node, refined tree view, exact tree member, or Family.Node value, got %s", carrierSourceType)
		return invalidType
	}
	itemType, ok := treeChildrenCandidateItemType(carrierSourceType)
	if !ok {
		a.errorf(expr.Args[0].Pos(), "children(...) requires at least one structural child edge on %s", treeChildrenSourceLabel(carrierSourceType))
		return invalidType
	}
	if IsInvalidType(itemType) {
		a.errorf(expr.Args[0].Pos(), "children(...) requires all structural child payloads to have the same item type")
		return invalidType
	}
	base, ok := a.namedTypes["TreeChildren"].(*StructType)
	if !ok || base == nil {
		a.errorf(expr.Pos(), "missing builtin TreeChildren carrier type")
		return invalidType
	}
	_ = sourceInfo
	return &GenericInstanceType{Name: "TreeChildren", Base: base, Args: []Type{carrierSourceType, itemType}}
}
func denseDViewType(t Type) (*DArrayViewType, bool) {
	view, ok := t.(*DArrayViewType)
	if !ok || view == nil {
		return nil, false
	}
	if view.SurfaceName == "packedtags" {
		return nil, false
	}
	return view, true
}
func zipMapDenseViewType(t Type) (Type, Type, bool) {
	switch tt := t.(type) {
	case *ViewType:
		if tt == nil {
			return nil, nil, false
		}
		return tt, tt.Elem, true
	case *DArrayViewType:
		if tt == nil || tt.SurfaceName == "packedtags" {
			return nil, nil, false
		}
		return tt, tt.Elem, true
	default:
		return nil, nil, false
	}
}
func (a *Analyzer) analyzeReadonlyHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) != 1 {
		a.errorf(expr.Pos(), "readonly expects 1 argument, got %d", len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	argType := a.analyzeExpr(expr.Args[0])
	if !proofCarryingViewType(argType) {
		a.errorf(expr.Args[0].Pos(), "readonly expects a view-like argument, got %s", argType)
		return invalidType
	}
	return argType
}
func (a *Analyzer) analyzeSplitAtHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) != 2 {
		a.errorf(expr.Pos(), "split_at expects 2 arguments, got %d", len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	viewType, ok := denseDViewType(a.analyzeExpr(expr.Args[0]))
	if !ok {
		actual := a.exprTypes[expr.Args[0]]
		if actual == nil {
			actual = invalidType
		}
		a.errorf(expr.Args[0].Pos(), "split_at expects a dense dview[T], got %s", actual)
		return invalidType
	}
	indexType := a.analyzeValueExpr(expr.Args[1], a.namedTypes["usize"])
	if !IsNumericType(indexType) {
		a.errorf(expr.Args[1].Pos(), "split_at index must be numeric, got %s", indexType)
	} else if !IsIntegralStorageType(indexType) {
		a.errorf(expr.Args[1].Pos(), "split_at index must be integral, got %s", indexType)
	}
	base, ok := a.namedTypes["SplitView"].(*StructType)
	if !ok || base == nil {
		a.errorf(expr.Pos(), "missing builtin SplitView carrier type")
		return invalidType
	}
	return &GenericInstanceType{Name: "SplitView", Base: base, Args: []Type{viewType.Elem}}
}
func (a *Analyzer) analyzeChunksExactHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) != 2 {
		a.errorf(expr.Pos(), "chunks_exact expects 2 arguments, got %d", len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	viewType, ok := denseDViewType(a.analyzeExpr(expr.Args[0]))
	if !ok {
		actual := a.exprTypes[expr.Args[0]]
		if actual == nil {
			actual = invalidType
		}
		a.errorf(expr.Args[0].Pos(), "chunks_exact expects a dense dview[T], got %s", actual)
		return invalidType
	}
	chunkSizeType := a.analyzeValueExpr(expr.Args[1], a.namedTypes["usize"])
	if !IsNumericType(chunkSizeType) {
		a.errorf(expr.Args[1].Pos(), "chunks_exact chunk size must be numeric, got %s", chunkSizeType)
	} else if !IsIntegralStorageType(chunkSizeType) {
		a.errorf(expr.Args[1].Pos(), "chunks_exact chunk size must be integral, got %s", chunkSizeType)
	}
	if value, ok := a.evalConstExpr(expr.Args[1]); ok && value.Kind == ConstInt && value.Int == 0 {
		a.errorf(expr.Args[1].Pos(), "chunks_exact chunk size cannot be zero")
	}
	base, ok := a.namedTypes["ChunksExactView"].(*StructType)
	if !ok || base == nil {
		a.errorf(expr.Pos(), "missing builtin ChunksExactView carrier type")
		return invalidType
	}
	return &GenericInstanceType{Name: "ChunksExactView", Base: base, Args: []Type{viewType.Elem}}
}
func (a *Analyzer) analyzeReduceSumHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) < 2 {
		a.errorf(expr.Pos(), "reduce_sum expects at least 2 arguments, got %d", len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	srcViewType, srcElemType, srcOK := zipMapDenseViewType(a.analyzeExpr(expr.Args[0]))
	callbackType := a.analyzeExpr(expr.Args[1])
	extraArgTypes := make([]Type, 0, len(expr.Args)-2)
	for _, arg := range expr.Args[2:] {
		extraArgTypes = append(extraArgTypes, a.analyzeExpr(arg))
	}

	if !srcOK {
		actual := a.exprTypes[expr.Args[0]]
		if actual == nil {
			actual = invalidType
		}
		a.errorf(expr.Args[0].Pos(), "reduce_sum source expects a dense view[T], got %s", actual)
		return invalidType
	}
	if !a.exprSupportsReadonlyDenseView(expr.Args[0]) {
		a.errorf(expr.Args[0].Pos(), "reduce_sum requires source to be a readonly contiguous exact-extent view, got %s", srcViewType)
	}

	callbackFn, ok := callbackType.(*FuncType)
	if !ok {
		a.errorf(expr.Args[1].Pos(), "reduce_sum callback expects a function value, got %s", callbackType)
		return invalidType
	}
	if callbackFn.Variadic || len(callbackFn.Params) != len(extraArgTypes)+1 {
		a.errorf(expr.Args[1].Pos(), "reduce_sum callback must accept the source element followed by %d extra arguments", len(extraArgTypes))
		return invalidType
	}
	if len(callbackFn.Permissions) != 0 {
		a.errorf(expr.Args[1].Pos(), "reduce_sum callback must not declare effect permissions")
	}
	if callbackFn.Return == nil || isVoidType(callbackFn.Return) {
		a.errorf(expr.Args[1].Pos(), "reduce_sum callback must return a numeric accumulator")
		return invalidType
	}
	if _, ok := callbackFn.Return.(*ErrorUnionType); ok {
		a.errorf(expr.Args[1].Pos(), "reduce_sum callback must not return an error union")
		return invalidType
	}
	if !IsNumericType(callbackFn.Return) {
		a.errorf(expr.Args[1].Pos(), "reduce_sum callback must return a numeric accumulator, got %s", callbackFn.Return)
		return invalidType
	}
	if !AssignableTo(callbackFn.Params[0], srcElemType) {
		a.errorf(expr.Args[1].Pos(), "reduce_sum callback first parameter expects %s, got %s", callbackFn.Params[0], srcElemType)
	}
	for i, argType := range extraArgTypes {
		if !AssignableTo(callbackFn.Params[i+1], argType) {
			a.errorf(expr.Args[1].Pos(), "reduce_sum callback parameter %d expects %s, got %s", i+2, callbackFn.Params[i+1], argType)
		}
	}
	return callbackFn.Return
}
