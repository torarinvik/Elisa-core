package semantic

import (
	"elisacore/src/ast"
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

func proofCarryingViewType(t Type) bool {
	switch t.(type) {
	case *ViewType, *DStrType, *SViewType:
		return true
	default:
		return false
	}
}
func denseDViewType(t Type) (*ViewType, bool) {
	view, ok := t.(*ViewType)
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
		a.errorf(expr.Args[0].Pos(), "split_at expects a dense view[T], got %s", actual)
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
		a.errorf(expr.Args[0].Pos(), "chunks_exact expects a dense view[T], got %s", actual)
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
