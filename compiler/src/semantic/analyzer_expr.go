package semantic

import (
	"strconv"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (a *Analyzer) analyzeExpr(expr ast.Expr) (result Type) {
	defer func() {
		if expr != nil {
			a.exprTypes[expr] = result
		}
	}()
	switch n := expr.(type) {
	case *ast.Ident:
		if t, ok := a.lookupRefinedExprType(n); ok {
			result = t
			return
		}
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok {
				result = sym.Type
				return
			}
		}
		if sym, ok := a.globalScope.Lookup(n.Name); ok {
			result = sym.Type
			return
		}
		a.errorf(n.Pos(), "undefined identifier %q", n.Name)
		result = invalidType
		return
	case *ast.IntLit:
		if n.Suffix != "" {
			if t, ok := a.namedTypes[n.Suffix]; ok {
				result = t
				return
			}
			switch n.Suffix {
			case "u":
				result = a.namedTypes["usize"]
				return
			case "i":
				result = a.namedTypes["int"]
				return
			}
		}
		result = a.namedTypes["int"]
		return
	case *ast.StringLit:
		result = &RefType{Elem: a.namedTypes["u8"], State: RefStateNonNull, Storage: RefStorageStatic, ExplicitStorage: true}
		return
	case *ast.BoolLit:
		result = a.namedTypes["bool"]
		return
	case *ast.NullLit:
		result = nullType
		return
	case *ast.ZeroedLit:
		result = invalidType
		return
	case *ast.ListLitExpr:
		result = a.analyzeListLitExprWithExpected(n, nil)
		return
	case *ast.BinaryExpr:
		result = a.analyzeBinaryExpr(n)
		return
	case *ast.UnaryExpr:
		result = a.analyzeUnaryExpr(n)
		return
	case *ast.CallExpr:
		result = a.analyzeCallExpr(n)
		return
	case *ast.FieldExpr:
		if errorType, ok := a.errorTagType(n); ok {
			result = errorType
			return
		}
		if t, ok := a.lookupRefinedExprType(n); ok {
			result = t
			return
		}
		result = a.analyzeFieldExpr(n)
		return
	case *ast.RaiseExpr:
		errorType := a.analyzeExpr(n.Error)
		currentUnion, ok := a.currentReturn.(*ErrorUnionType)
		if !ok {
			a.errorf(n.Pos(), "raise requires the current function to return an error union")
			result = neverType
			return
		}
		if qualifiedTag, ok := a.errorExprTagName(n.Error); ok {
			if _, ok := MatchErrorTag(currentUnion.Errors, qualifiedTag); !ok {
				a.errorf(n.Pos(), "raise cannot propagate tag %q into %s", ErrorTagDiagnosticName(qualifiedTag), ErrorSetDiagnosticName(currentUnion.Errors))
			}
			result = neverType
			return
		}
		if errSet, ok := errorType.(*ErrorSetType); !ok || !ErrorSetAssignable(currentUnion.Errors, errSet) {
			a.errorf(n.Pos(), "raise expects %s, got %s", ErrorSetDiagnosticName(currentUnion.Errors), ErrorTypeDiagnosticName(errorType))
		}
		result = neverType
		return
	case *ast.TryExpr:
		valueType := a.analyzeExpr(n.Value)
		unionType, ok := valueType.(*ErrorUnionType)
		if !ok {
			a.errorf(n.Pos(), "try requires a fallible expression, got %s", valueType.String())
			result = invalidType
			return
		}
		if n.Fallback == nil {
			currentUnion, ok := a.currentReturn.(*ErrorUnionType)
			if !ok {
				a.errorf(n.Pos(), "try without else requires the current function to return an error union")
			} else if !ErrorSetAssignable(currentUnion.Errors, unionType.Errors) {
				a.errorf(n.Pos(), "cannot propagate %s from a function returning %s", ErrorSetDiagnosticName(unionType.Errors), ErrorSetDiagnosticName(currentUnion.Errors))
			}
			result = unionType.Value
			return
		}
		fallbackType := a.analyzeExpr(n.Fallback)
		if !IsNeverType(fallbackType) && !AssignableTo(unionType.Value, fallbackType) {
			a.errorf(n.Pos(), "try fallback expects %s, got %s", unionType.Value.String(), fallbackType.String())
			a.reportShapeMismatchNotes(n.Pos(), unionType.Value, fallbackType)
		}
		result = unionType.Value
		return
	case *ast.UnwrapElseExpr:
		valueType := a.analyzeExpr(n.Value)
		refType, ok := valueType.(*RefType)
		if !ok || refType.State == RefStateNonNull {
			a.errorf(n.Pos(), "else recovery requires a nullable reference, got %s", valueType.String())
			result = invalidType
			return
		}
		resultType := cloneRefTypeWithState(refType, RefStateNonNull)
		fallbackType := a.analyzeExpr(n.Fallback)
		if !IsNeverType(fallbackType) && !AssignableTo(resultType, fallbackType) {
			a.errorf(n.Pos(), "else fallback expects %s, got %s", resultType.String(), fallbackType.String())
			a.reportShapeMismatchNotes(n.Pos(), resultType, fallbackType)
		}
		result = resultType
		return
	case *ast.IndexExpr:
		result = a.analyzeIndexExpr(n)
		return
	case *ast.SliceExpr:
		result = a.analyzeSliceExpr(n)
		return
	case *ast.CastExpr:
		src := a.analyzeExpr(n.Operand)
		dst := a.resolveType(n.Target)
		if !a.validCast(src, dst) {
			a.errorf(n.Pos(), "invalid cast from %s to %s", src.String(), dst.String())
		}
		result = dst
		return
	case *ast.SizeofExpr:
		a.resolveType(n.Type)
		result = a.namedTypes["usize"]
		return
	case *ast.TernaryExpr:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "ternary condition must be bool, got %s", condType.String())
		}
		left := a.analyzeExprInScope(n.Value, a.refinedScopeForCondition(a.currentScope, n.Cond, true))
		right := a.analyzeExprInScope(n.Alt, a.refinedScopeForCondition(a.currentScope, n.Cond, false))
		merged := MergeTypes(left, right)
		if IsInvalidType(merged) {
			a.errorf(n.Pos(), "ternary branches are incompatible: %s and %s", left.String(), right.String())
		}
		result = merged
		return
	case *ast.AddrOfExpr:
		inner := a.analyzeExpr(n.Operand)
		result = &RefType{Elem: inner, State: RefStateNonNull, Storage: a.inferAddrOfStorage(n.Operand), ExplicitStorage: true}
		return
	case *ast.StructLitExpr:
		if t, ok := a.namedTypes[n.Name]; ok {
			switch tt := t.(type) {
			case *StructType:
				a.analyzeStructLiteralArgs(n, tt, nil)
				result = tt
				return
			case *GenericInstanceType:
				if _, ok := tt.Base.(*StructType); ok {
					base := tt.Base.(*StructType)
					bindings := map[string]Type{}
					for i, name := range base.TypeParams {
						if i < len(tt.Args) {
							bindings[name] = tt.Args[i]
						}
					}
					a.analyzeStructLiteralArgs(n, base, bindings)
					result = tt
					return
				}
			}
		}
		a.errorf(n.Pos(), "unknown struct %q", n.Name)
		result = invalidType
		return
	case *ast.ParenExpr:
		result = a.analyzeExpr(n.Inner)
		return
	default:
		result = invalidType
		return
	}
}

func (a *Analyzer) analyzeStructLiteralArgs(expr *ast.StructLitExpr, base *StructType, bindings map[string]Type) {
	if base == nil || base.Decl == nil {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return
	}
	if len(expr.Args) != len(base.Decl.Fields) {
		a.errorf(expr.Pos(), "struct literal %q expects %d arguments, got %d", expr.Name, len(base.Decl.Fields), len(expr.Args))
	}
	limit := len(expr.Args)
	if len(base.Decl.Fields) < limit {
		limit = len(base.Decl.Fields)
	}
	for i := 0; i < limit; i++ {
		fieldDecl := base.Decl.Fields[i]
		field, ok := base.Fields[fieldDecl.Name]
		if !ok {
			a.analyzeExpr(expr.Args[i])
			continue
		}
		expected := field.Type
		if len(bindings) > 0 {
			expected = a.substituteType(expected, bindings, nil)
		}
		actual := a.analyzeValueExpr(expr.Args[i], expected)
		if !AssignableTo(expected, actual) {
			a.errorf(expr.Args[i].Pos(), "struct literal field %q expects %s, got %s", fieldDecl.Name, expected.String(), actual.String())
		}
	}
	for i := limit; i < len(expr.Args); i++ {
		a.analyzeExpr(expr.Args[i])
	}
}

func (a *Analyzer) errorExprTagName(expr ast.Expr) (string, bool) {
	fieldExpr, ok := expr.(*ast.FieldExpr)
	if !ok {
		return "", false
	}
	ident, ok := fieldExpr.Object.(*ast.Ident)
	if !ok {
		return "", false
	}
	base, ok := a.namedTypes[ident.Name]
	if !ok {
		return "", false
	}
	errSet, ok := base.(*ErrorSetType)
	if !ok || !errSet.HasQualifiedTag(ident.Name, fieldExpr.Field) {
		return "", false
	}
	return QualifyErrorTag(ident.Name, fieldExpr.Field), true
}

func (a *Analyzer) errorTagType(expr *ast.FieldExpr) (Type, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, false
	}
	base, ok := a.namedTypes[ident.Name]
	if !ok {
		return nil, false
	}
	errSet, ok := base.(*ErrorSetType)
	if !ok {
		return nil, false
	}
	if !errSet.HasQualifiedTag(ident.Name, expr.Field) {
		a.errorf(expr.Pos(), "error set %q has no tag %q", ErrorSetDiagnosticName(errSet), expr.Field)
		return invalidType, true
	}
	return errSet, true
}

func (a *Analyzer) analyzeBinaryExpr(expr *ast.BinaryExpr) Type {
	left := a.analyzeExpr(expr.Left)
	right := a.analyzeExpr(expr.Right)
	switch expr.Op {
	case lexer.TOKEN_AND, lexer.TOKEN_OR:
		if !IsBoolType(left) || !IsBoolType(right) {
			a.errorf(expr.Pos(), "logical operator requires bool operands")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
		if runtimeStringComparable(left, right) {
			return a.namedTypes["bool"]
		}
		if IsNumericType(left) && IsNumericType(right) {
			return a.namedTypes["bool"]
		}
		if !(AssignableTo(left, right) || AssignableTo(right, left) || (IsNullType(left) && isRefLike(right)) || (IsNullType(right) && isRefLike(left))) {
			a.errorf(expr.Pos(), "cannot compare %s and %s", left.String(), right.String())
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "comparison requires numeric operands")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS:
		if lref, ok := left.(*RefType); ok && IsNumericType(right) {
			return lref
		}
		if expr.Op == lexer.TOKEN_PLUS {
			if rref, ok := right.(*RefType); ok && IsNumericType(left) {
				return rref
			}
		}
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "operator requires numeric operands")
			return invalidType
		}
		return CommonNumericType(left, right)
	case lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT,
		lexer.TOKEN_CARET, lexer.TOKEN_PIPE, lexer.TOKEN_AMPERSAND,
		lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "operator requires numeric operands")
			return invalidType
		}
		return CommonNumericType(left, right)
	default:
		return invalidType
	}
}

func (a *Analyzer) analyzeUnaryExpr(expr *ast.UnaryExpr) Type {
	operand := a.analyzeExpr(expr.Operand)
	switch expr.Op {
	case lexer.TOKEN_NOT:
		if !IsBoolType(operand) {
			a.errorf(expr.Pos(), "not operator requires bool operand")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_MINUS, lexer.TOKEN_TILDE:
		if !IsNumericType(operand) {
			a.errorf(expr.Pos(), "unary operator requires numeric operand")
		}
		return operand
	default:
		return invalidType
	}
}

func (a *Analyzer) analyzeCallExpr(expr *ast.CallExpr) Type {
	fnType := a.analyzeExpr(expr.Func)
	ft, ok := fnType.(*FuncType)
	if !ok {
		a.errorf(expr.Pos(), "cannot call non-function value of type %s", fnType.String())
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	if !ft.Variadic && len(expr.Args) != len(ft.Params) {
		a.errorf(expr.Pos(), "function %q expects %d arguments, got %d", ft.Name, len(ft.Params), len(expr.Args))
	}
	if ft.Variadic && len(expr.Args) < len(ft.Params) {
		a.errorf(expr.Pos(), "variadic function %q expects at least %d arguments, got %d", ft.Name, len(ft.Params), len(expr.Args))
	}
	bindings := map[string]Type{}
	shapeBindings := map[string]Shape{}
	limit := len(ft.Params)
	if len(expr.Args) < limit {
		limit = len(expr.Args)
	}
	for i := 0; i < len(expr.Args); i++ {
		var argType Type
		if i < limit {
			expectedType := a.substituteType(ft.Params[i], bindings, shapeBindings)
			argType = a.analyzeValueExpr(expr.Args[i], expectedType)
			a.collectTypeBindings(ft.Params[i], argType, bindings, shapeBindings)
			expectedType = a.substituteType(ft.Params[i], bindings, shapeBindings)
			if !AssignableTo(expectedType, argType) {
				a.errorf(expr.Args[i].Pos(), "argument %d to %q expects %s, got %s", i+1, ft.Name, expectedType.String(), argType.String())
				a.reportShapeMismatchNotes(expr.Args[i].Pos(), expectedType, argType)
			}
		} else {
			argType = a.analyzeExpr(expr.Args[i])
		}
	}
	if ft.Return == nil {
		return a.namedTypes["void"]
	}
	a.bindFreshReturnShapes(ft, shapeBindings)
	return a.substituteType(ft.Return, bindings, shapeBindings)
}

func (a *Analyzer) collectRuntimeBridgeBindings(pattern, actual Type, bindings map[string]Type, shapeBindings map[string]Shape) bool {
	bridge, ok := classifyRuntimeBridge(pattern, actual)
	if !ok {
		return false
	}
	switch bridge.Kind {
	case runtimeBridgeDArrayDynArray:
		if patternDArray, ok := pattern.(*DArrayType); ok {
			a.collectTypeBindings(patternDArray.Elem, bridge.DynArray.Args[0], bindings, shapeBindings)
			return true
		}
		if patternDynArray, ok := dynArrayRuntimeInstance(pattern); ok {
			a.collectTypeBindings(patternDynArray.Args[0], bridge.DArray.Elem, bindings, shapeBindings)
			return true
		}
		return true
	case runtimeBridgeDArrayViewDynArrayView, runtimeBridgeDListCtxList, runtimeBridgeDListViewCtxListView, runtimeBridgeDArrayCtxList, runtimeBridgeDStrU8Ref:
		return true
	default:
		return false
	}
}

func (a *Analyzer) collectTypeBindings(pattern, actual Type, bindings map[string]Type, shapeBindings map[string]Shape) {
	if pattern == nil || actual == nil {
		return
	}
	if a.collectRuntimeBridgeBindings(pattern, actual, bindings, shapeBindings) {
		return
	}
	switch p := pattern.(type) {
	case *TypeParamType:
		if _, exists := bindings[p.Name]; !exists {
			bindings[p.Name] = actual
		}
	case *ErrorUnionType:
		if act, ok := actual.(*ErrorUnionType); ok {
			a.collectTypeBindings(p.Value, act.Value, bindings, shapeBindings)
			return
		}
		a.collectTypeBindings(p.Value, actual, bindings, shapeBindings)
	case *RefType:
		if act, ok := actual.(*RefType); ok {
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings)
		}
	case *ArrayType:
		if act, ok := actual.(*ArrayType); ok {
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings)
		}
	case *DArrayType:
		if act, ok := actual.(*DArrayType); ok {
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings)
			a.collectShapeBinding(p.Shape, act.Shape, shapeBindings)
		}
	case *DArrayViewType:
		if act, ok := actual.(*DArrayViewType); ok {
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings)
		}
	case *DListType:
		if act, ok := actual.(*DListType); ok {
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings)
			a.collectShapeBinding(p.Shape, act.Shape, shapeBindings)
		}
	case *DListViewType:
		if act, ok := actual.(*DListViewType); ok {
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings)
		}
	case *DStrType:
		if act, ok := actual.(*DStrType); ok {
			a.collectShapeBinding(p.Shape, act.Shape, shapeBindings)
		}
	case *SViewType:
		_, _ = actual.(*SViewType)
	case *GenericInstanceType:
		if act, ok := actual.(*GenericInstanceType); ok && p.Name == act.Name && len(p.Args) == len(act.Args) {
			for i := range p.Args {
				a.collectTypeBindings(p.Args[i], act.Args[i], bindings, shapeBindings)
			}
		}
	case *FuncType:
		if act, ok := actual.(*FuncType); ok {
			limit := len(p.Params)
			if len(act.Params) < limit {
				limit = len(act.Params)
			}
			for i := 0; i < limit; i++ {
				a.collectTypeBindings(p.Params[i], act.Params[i], bindings, shapeBindings)
			}
			a.collectTypeBindings(p.Return, act.Return, bindings, shapeBindings)
		}
	}
}

func (a *Analyzer) collectShapeBinding(pattern, actual Shape, bindings map[string]Shape) {
	param, ok := pattern.(*ShapeParam)
	if !ok {
		return
	}
	if _, exists := bindings[param.Name]; !exists {
		bindings[param.Name] = actual
	}
}

func (a *Analyzer) matchReturnType(actual Type) Type {
	if a.currentReturn == nil || actual == nil {
		return a.currentReturn
	}
	bindings := map[string]Type{}
	shapeBindings := map[string]Shape{}
	a.collectTypeBindings(a.currentReturn, actual, bindings, shapeBindings)
	return a.substituteType(a.currentReturn, bindings, shapeBindings)
}

func (a *Analyzer) bindFreshReturnShapes(fn *FuncType, bindings map[string]Shape) {
	if fn == nil || fn.Return == nil {
		return
	}
	for _, name := range fn.FreshReturnShapeParams {
		a.bindFreshShape(&ShapeParam{Name: name}, fn.Name, bindings)
	}
}

func (a *Analyzer) bindFreshShape(shape Shape, origin string, bindings map[string]Shape) {
	param, ok := shape.(*ShapeParam)
	if !ok {
		return
	}
	if _, exists := bindings[param.Name]; exists {
		return
	}
	a.freshShapeCounter++
	bindings[param.Name] = &FreshShape{ID: a.freshShapeCounter, Label: param.Name, Origin: origin}
}

func (a *Analyzer) analyzeFieldExpr(expr *ast.FieldExpr) Type {
	if field, ok := dstrSyntheticField(a.analyzeExpr(expr.Object), expr.Field); ok {
		return field.Type
	}
	field, ok := a.lookupField(a.analyzeExpr(expr.Object), expr.Field, expr.Pos())
	if !ok {
		return invalidType
	}
	return field.Type
}

func (a *Analyzer) analyzeIndexExpr(expr *ast.IndexExpr) Type {
	objType := a.analyzeExpr(expr.Object)
	indexType := a.analyzeExpr(expr.Index)
	if !IsNumericType(indexType) {
		a.errorf(expr.Index.Pos(), "index must be numeric, got %s", indexType.String())
	}
	if arr, ok := objType.(*ArrayType); ok {
		a.checkConstantArrayIndexBounds(arr, expr.Index)
		if isStringArrayType(arr) {
			return a.namedTypes["char"]
		}
		return arr.Elem
	}
	if darray, ok := objType.(*DArrayType); ok {
		return darray.Elem
	}
	if view, ok := objType.(*DArrayViewType); ok {
		return view.Elem
	}
	if dlist, ok := objType.(*DListType); ok {
		return dlist.Elem
	}
	if view, ok := objType.(*DListViewType); ok {
		return view.Elem
	}
	if _, ok := objType.(*DStrType); ok {
		return a.namedTypes["char"]
	}
	if isStringViewType(objType) {
		return a.namedTypes["char"]
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			a.errorf(expr.Pos(), "indexing requires proven non-null reference, got %s", objType.String())
			return invalidType
		}
		if arr, ok := ref.Elem.(*ArrayType); ok {
			a.checkConstantArrayIndexBounds(arr, expr.Index)
			if isStringArrayType(arr) {
				return a.namedTypes["char"]
			}
			return arr.Elem
		}
		if darray, ok := ref.Elem.(*DArrayType); ok {
			return darray.Elem
		}
		if view, ok := ref.Elem.(*DArrayViewType); ok {
			return view.Elem
		}
		if dlist, ok := ref.Elem.(*DListType); ok {
			return dlist.Elem
		}
		if view, ok := ref.Elem.(*DListViewType); ok {
			return view.Elem
		}
		if _, ok := ref.Elem.(*DStrType); ok {
			return a.namedTypes["char"]
		}
		if isStringViewType(ref.Elem) {
			return a.namedTypes["char"]
		}
		return ref.Elem
	}
	a.errorf(expr.Pos(), "indexing requires array or reference type, got %s", objType.String())
	return invalidType
}

func (a *Analyzer) analyzeSliceExpr(expr *ast.SliceExpr) Type {
	objType := a.analyzeExpr(expr.Object)
	startType := a.analyzeExpr(expr.Start)
	endType := a.analyzeExpr(expr.End)
	if !IsNumericType(startType) {
		a.errorf(expr.Start.Pos(), "slice start must be numeric, got %s", startType.String())
	}
	if !IsNumericType(endType) {
		a.errorf(expr.End.Pos(), "slice end must be numeric, got %s", endType.String())
	}
	if array, ok := objType.(*ArrayType); ok {
		if isStringArrayType(array) {
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
		return &DArrayViewType{Elem: array.Elem}
	}
	if view, ok := objType.(*DArrayType); ok {
		return &DArrayViewType{Elem: view.Elem}
	}
	if view, ok := objType.(*DArrayViewType); ok {
		return &DArrayViewType{Elem: view.Elem}
	}
	if dstr, ok := objType.(*DStrType); ok {
		_ = dstr
		return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
	}
	if view, ok := objType.(*DListType); ok {
		return &DListViewType{Elem: view.Elem}
	}
	if view, ok := objType.(*DListViewType); ok {
		return &DListViewType{Elem: view.Elem}
	}
	if isStringViewType(objType) {
		return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			a.errorf(expr.Pos(), "slicing requires proven non-null reference, got %s", objType.String())
			return invalidType
		}
		if array, ok := ref.Elem.(*ArrayType); ok {
			if isStringArrayType(array) {
				return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
			}
			return &DArrayViewType{Elem: array.Elem}
		}
		if view, ok := ref.Elem.(*DArrayType); ok {
			return &DArrayViewType{Elem: view.Elem}
		}
		if view, ok := ref.Elem.(*DArrayViewType); ok {
			return &DArrayViewType{Elem: view.Elem}
		}
		if _, ok := ref.Elem.(*DStrType); ok {
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
		if view, ok := ref.Elem.(*DListType); ok {
			return &DListViewType{Elem: view.Elem}
		}
		if view, ok := ref.Elem.(*DListViewType); ok {
			return &DListViewType{Elem: view.Elem}
		}
		if isStringViewType(ref.Elem) {
			return &SViewType{Begin: a.exprSummary(expr.Start), End: a.exprSummary(expr.End)}
		}
	}
	a.errorf(expr.Pos(), "slicing requires string, array, list, or view type, got %s", objType.String())
	return invalidType
}

func (a *Analyzer) analyzeValueExpr(expr ast.Expr, expected Type) Type {
	if list, ok := expr.(*ast.ListLitExpr); ok {
		return a.analyzeListLitExprWithExpected(list, expected)
	}
	return a.analyzeExpr(expr)
}

func (a *Analyzer) analyzeListLitExprWithExpected(expr *ast.ListLitExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	expectedArray, useExpected := contextualArrayLiteralType(expected)
	if len(expr.Elems) == 0 {
		if useExpected {
			if expectedArray.HasConstSize && expectedArray.ConstSize != 0 {
				a.errorf(expr.Pos(), "array literal expects %d elements, got 0", expectedArray.ConstSize)
			}
			a.exprTypes[expr] = expectedArray
			return expectedArray
		}
		a.errorf(expr.Pos(), "empty array literal requires an expected array type")
		a.exprTypes[expr] = invalidType
		return invalidType
	}

	var elemType Type
	if useExpected {
		elemType = expectedArray.Elem
		if expectedArray.HasConstSize && expectedArray.ConstSize != int64(len(expr.Elems)) {
			a.errorf(expr.Pos(), "array literal expects %d elements, got %d", expectedArray.ConstSize, len(expr.Elems))
		}
	}

	for _, elem := range expr.Elems {
		itemType := a.analyzeValueExpr(elem, elemType)
		if useExpected {
			if !AssignableTo(expectedArray.Elem, itemType) {
				a.errorf(elem.Pos(), "array literal element expects %s, got %s", expectedArray.Elem.String(), itemType.String())
			}
			continue
		}
		if elemType == nil {
			elemType = itemType
			continue
		}
		merged := MergeTypes(elemType, itemType)
		if IsInvalidType(merged) {
			a.errorf(elem.Pos(), "array literal elements are incompatible: %s and %s", elemType.String(), itemType.String())
			a.exprTypes[expr] = invalidType
			return invalidType
		}
		elemType = merged
	}

	if useExpected {
		a.exprTypes[expr] = expectedArray
		return expectedArray
	}
	if elemType == nil || IsInvalidType(elemType) {
		a.exprTypes[expr] = invalidType
		return invalidType
	}
	result := &ArrayType{Elem: elemType, Size: strconv.Itoa(len(expr.Elems)), HasConstSize: true, ConstSize: int64(len(expr.Elems))}
	a.exprTypes[expr] = result
	return result
}

func contextualArrayLiteralType(expected Type) (*ArrayType, bool) {
	arrayType, ok := expected.(*ArrayType)
	if !ok {
		return nil, false
	}
	return arrayType, true
}

func containsTypeParam(t Type) bool {
	switch n := t.(type) {
	case nil:
		return false
	case *TypeParamType:
		return true
	case *ErrorUnionType:
		return containsTypeParam(n.Value)
	case *RefType:
		return containsTypeParam(n.Elem)
	case *ArrayType:
		return containsTypeParam(n.Elem)
	case *DArrayType:
		return containsTypeParam(n.Elem)
	case *DArrayViewType:
		return containsTypeParam(n.Elem)
	case *DListType:
		return containsTypeParam(n.Elem)
	case *DListViewType:
		return containsTypeParam(n.Elem)
	case *GenericInstanceType:
		for _, arg := range n.Args {
			if containsTypeParam(arg) {
				return true
			}
		}
		return containsTypeParam(n.Base)
	case *FuncType:
		for _, param := range n.Params {
			if containsTypeParam(param) {
				return true
			}
		}
		return containsTypeParam(n.Return)
	default:
		return false
	}
}

func (a *Analyzer) assignmentTargetType(expr ast.Expr) Type {
	switch n := expr.(type) {
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			if sym, ok = a.globalScope.Lookup(n.Name); !ok {
				a.errorf(n.Pos(), "undefined assignment target %q", n.Name)
				return invalidType
			}
		}
		if !sym.Mutable {
			if ref, ok := sym.Type.(*RefType); ok {
				return ref.Elem
			}
			a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
			return sym.Type
		}
		if a.currentScope != nil {
			if current, exists := a.currentScope.Symbols[n.Name]; exists && current == sym && a.currentScope.Parent != nil {
				if parent, ok := a.currentScope.Parent.Lookup(n.Name); ok && parent.Node == sym.Node && parent.Kind == sym.Kind && parent.Mutable {
					return parent.Type
				}
			}
		}
		return sym.Type
	case *ast.FieldExpr:
		field, ok := a.lookupField(a.analyzeExpr(n.Object), n.Field, n.Pos())
		if !ok {
			return invalidType
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q is immutable", n.Field)
		}
		return field.Type
	case *ast.IndexExpr:
		targetType := a.analyzeIndexExpr(n)
		if kind, ok := valueOnlyIndexKind(a.exprTypes[n.Object]); ok {
			a.errorf(n.Pos(), "cannot assign to %s", kind)
			return invalidType
		}
		return targetType
	default:
		a.errorf(expr.Pos(), "invalid assignment target")
		return invalidType
	}
}

func (a *Analyzer) asRefTargetType(expr ast.Expr, asKind string) Type {
	switch n := expr.(type) {
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			if sym, ok = a.globalScope.Lookup(n.Name); !ok {
				a.errorf(n.Pos(), "undefined assignment target %q", n.Name)
				return invalidType
			}
		}
		if !sym.Mutable {
			a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
		}
		return a.refTypeWithAsKind(sym.Type, asKind)
	case *ast.FieldExpr:
		field, ok := a.lookupField(a.analyzeExpr(n.Object), n.Field, n.Pos())
		if !ok {
			return invalidType
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q is immutable", n.Field)
		}
		return a.refTypeWithAsKind(field.Type, asKind)
	case *ast.IndexExpr:
		targetType := a.analyzeIndexExpr(n)
		if kind, ok := valueOnlyIndexKind(a.exprTypes[n.Object]); ok {
			a.errorf(n.Pos(), "cannot take a reference to %s", kind)
			return invalidType
		}
		return a.refTypeWithAsKind(targetType, asKind)
	default:
		a.errorf(expr.Pos(), "invalid assignment target")
		return invalidType
	}
}

func (a *Analyzer) refTypeWithAsKind(t Type, asKind string) Type {
	ref, ok := t.(*RefType)
	if !ok {
		return t
	}
	switch asKind {
	case "&":
		return cloneRefTypeWithState(ref, RefStateNonNull)
	case "!":
		return cloneRefTypeWithState(ref, RefStateNull)
	default:
		return t
	}
}

func (a *Analyzer) inferAddrOfStorage(expr ast.Expr) RefStorage {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.inferAddrOfStorage(n.Inner)
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok {
				switch sym.Kind {
				case SymbolLocal, SymbolParam:
					return RefStorageStack
				case SymbolGlobal:
					return RefStorageStatic
				}
			}
		}
		if sym, ok := a.globalScope.Lookup(n.Name); ok && sym.Kind == SymbolGlobal {
			return RefStorageStatic
		}
	case *ast.FieldExpr:
		if objType, ok := a.exprTypes[n.Object].(*RefType); ok {
			return objType.Storage
		}
		return a.inferAddrOfStorage(n.Object)
	case *ast.IndexExpr:
		switch objType := a.exprTypes[n.Object].(type) {
		case *RefType:
			if _, ok := objType.Elem.(*ArrayType); ok {
				return objType.Storage
			}
			return RefStorageAny
		case *ArrayType:
			return a.inferAddrOfStorage(n.Object)
		}
	}
	return RefStorageAny
}

func (a *Analyzer) lookupField(objType Type, fieldName string, pos lexer.Pos) (Field, bool) {
	if field, ok := dstrSyntheticField(objType, fieldName); ok {
		return field, true
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			a.errorf(pos, "field access requires proven non-null reference, got %s", objType.String())
			return Field{}, false
		}
		objType = ref.Elem
	}
	if runtimeBacked := a.runtimeBackedStructType(objType); runtimeBacked != nil {
		objType = runtimeBacked
	}
	switch t := objType.(type) {
	case *StructType:
		field, ok := t.Fields[fieldName]
		if !ok {
			a.errorf(pos, "struct %q has no field %q", t.Name, fieldName)
			return Field{}, false
		}
		return field, true
	case *GenericInstanceType:
		baseStruct, ok := t.Base.(*StructType)
		if !ok {
			a.errorf(pos, "field access requires struct type, got %s", objType.String())
			return Field{}, false
		}
		field, ok := baseStruct.Fields[fieldName]
		if !ok {
			a.errorf(pos, "struct %q has no field %q", baseStruct.Name, fieldName)
			return Field{}, false
		}
		bindings := map[string]Type{}
		for i, name := range baseStruct.TypeParams {
			if i < len(t.Args) {
				bindings[name] = t.Args[i]
			}
		}
		field.Type = a.substituteType(field.Type, bindings, nil)
		return field, true
	default:
		a.errorf(pos, "field access requires struct type, got %s", objType.String())
		return Field{}, false
	}
}

func (a *Analyzer) runtimeBackedStructType(t Type) Type {
	if dav, ok := t.(*DArrayViewType); ok {
		base, ok := a.namedTypes["DynArrayView"]
		if !ok {
			return nil
		}
		_ = dav
		return base
	}
	if _, ok := t.(*DListType); ok {
		base, ok := a.namedTypes["CtxList"]
		if !ok {
			return nil
		}
		return base
	}
	if _, ok := t.(*DListViewType); ok {
		base, ok := a.namedTypes["CtxListView"]
		if !ok {
			return nil
		}
		return base
	}
	if _, ok := t.(*SViewType); ok {
		base, ok := a.namedTypes["CtxStringView"]
		if !ok {
			return nil
		}
		return base
	}
	darray, ok := t.(*DArrayType)
	if !ok {
		return nil
	}
	base, ok := a.namedTypes["DynArray"]
	if !ok {
		return nil
	}
	return &GenericInstanceType{Name: "DynArray", Base: base, Args: []Type{darray.Elem}}
}

func valueOnlyIndexKind(t Type) (string, bool) {
	if _, ok := t.(*DStrType); ok {
		return "string index", true
	}
	if array, ok := t.(*ArrayType); ok && isStringArrayType(array) {
		return "string index", true
	}
	if isStringViewType(t) {
		return "string view index", true
	}
	ref, ok := t.(*RefType)
	if !ok {
		return "", false
	}
	if _, ok := ref.Elem.(*DStrType); ok {
		return "string index", true
	}
	if array, ok := ref.Elem.(*ArrayType); ok && isStringArrayType(array) {
		return "string index", true
	}
	if isStringViewType(ref.Elem) {
		return "string view index", true
	}
	return "", false
}

func isStringArrayType(t *ArrayType) bool {
	return t != nil && (t.SurfaceName == "str" || t.SurfaceName == "string")
}

func isStringViewType(t Type) bool {
	if _, ok := t.(*SViewType); ok {
		return true
	}
	return isCtxStringViewType(t)
}

func isCtxStringViewType(t Type) bool {
	st, ok := t.(*StructType)
	return ok && st.Name == "CtxStringView"
}

func dstrSyntheticField(t Type, fieldName string) (Field, bool) {
	if fieldName != "len" {
		return Field{}, false
	}
	if _, ok := t.(*DStrType); ok {
		return Field{Name: "len", Type: builtinI64Type(), Mutable: false}, true
	}
	ref, ok := t.(*RefType)
	if !ok {
		return Field{}, false
	}
	if _, ok := ref.Elem.(*DStrType); ok {
		return Field{Name: "len", Type: builtinI64Type(), Mutable: false}, true
	}
	return Field{}, false
}

func builtinI64Type() Type {
	return &BuiltinType{Name: "i64"}
}

type runtimeStringKind int

const (
	runtimeStringNone runtimeStringKind = iota
	runtimeStringDStr
	runtimeStringView
	runtimeStringRaw
)

func runtimeStringComparable(left Type, right Type) bool {
	leftKind := runtimeStringKindOf(left)
	rightKind := runtimeStringKindOf(right)
	if leftKind == runtimeStringNone || rightKind == runtimeStringNone {
		return false
	}
	if leftKind == runtimeStringRaw && rightKind == runtimeStringRaw {
		return false
	}
	return leftKind != runtimeStringNone && rightKind != runtimeStringNone
}

func runtimeStringKindOf(t Type) runtimeStringKind {
	if t == nil {
		return runtimeStringNone
	}
	if _, ok := t.(*DStrType); ok {
		return runtimeStringDStr
	}
	if isStringViewType(t) {
		return runtimeStringView
	}
	ref, ok := t.(*RefType)
	if !ok {
		return runtimeStringNone
	}
	if builtin, ok := ref.Elem.(*BuiltinType); ok && builtin.Name == "u8" {
		return runtimeStringRaw
	}
	if isStringViewType(ref.Elem) {
		return runtimeStringView
	}
	return runtimeStringNone
}
