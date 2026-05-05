package semantic

import (
	"strconv"

	"elisacore/src/ast"
)

func (a *Analyzer) analyzeScopedPackedAllocExpr(expr *ast.AllocExpr) Type {
	enumType, variant, ok := a.packedAllocConstructorInfo(expr.Value)
	if !ok || enumType == nil || variant == nil || !enumType.Packed {
		valueType := a.analyzeExpr(expr.Value)
		a.errorf(expr.Pos(), "new without [...] expects a packed enum constructor inside an in-store block, got %s", valueType)
		return invalidType
	}
	storeType, ok := a.lookupPackedStore(enumType)
	if !ok {
		a.errorf(expr.Pos(), "packed enum constructor %q requires an active in %s: scope or explicit new[%s]", enumType.Name+"."+variant.Name, packedEnumStoreTypeName(enumType.Name), packedEnumStoreTypeName(enumType.Name))
		if enumType.StoreType != nil {
			return a.analyzePackedAllocExpr(expr, PackedEnumStoreWithState(enumType.StoreType, a.namedTypes["Local"]))
		}
		return enumType
	}
	return a.analyzePackedAllocExpr(expr, storeType)
}

func (a *Analyzer) analyzePackedAllocExpr(expr *ast.AllocExpr, storeType *PackedEnumStoreType) Type {
	if storeType == nil {
		a.errorf(expr.Pos(), "missing packed enum store type")
		return invalidType
	}
	if !IsLocalPackedEnumStoreType(storeType) {
		a.errorf(allocOwnerPos(expr), "packed enum allocation requires local store type %q, got %s", PackedEnumStoreWithState(storeType, a.namedTypes["Local"]), storeType)
	}
	if fieldExpr, ok := expr.Value.(*ast.FieldExpr); ok {
		ident, ok := fieldExpr.Object.(*ast.Ident)
		if ok {
			base, _, ok := a.lookupVisibleType(ident.Name)
			if ok {
				enumType, ok := base.(*EnumType)
				if ok {
					variant, ok := enumType.Variant(fieldExpr.Field)
					if ok && enumType.Packed && len(variant.Payload) == 0 {
						if storeType.Enum != enumType {
							a.errorf(allocOwnerPos(expr), "packed enum constructor %q requires store %q, got %q", enumType.Name+"."+variant.Name, packedEnumStoreTypeName(enumType.Name), storeType)
						}
						return enumType
					}
				}
			}
		}
	}
	callExpr, ok := expr.Value.(*ast.CallExpr)
	if !ok {
		valueType := a.analyzeExpr(expr.Value)
		a.errorf(expr.Value.Pos(), "new[%s] expects a packed enum constructor call, got %s", storeType, valueType)
		return invalidType
	}
	enumType, variant, ok := a.enumConstructorCall(callExpr)
	if !ok || enumType == nil || variant == nil || !enumType.Packed {
		valueType := a.analyzeExpr(expr.Value)
		a.errorf(expr.Value.Pos(), "new[%s] expects a packed enum constructor call, got %s", storeType, valueType)
		return invalidType
	}
	if storeType.Enum != enumType {
		a.errorf(allocOwnerPos(expr), "packed enum constructor %q requires store %q, got %q", enumType.Name+"."+variant.Name, packedEnumStoreTypeName(enumType.Name), storeType)
	}
	orderedArgs, commonArgs, ok := a.resolvePackedEnumConstructorArgs(callExpr, enumType, variant)
	if !ok {
		return enumType
	}
	if len(orderedArgs) != len(variant.Payload) {
		a.errorf(callExpr.Pos(), "enum constructor %q expects %d arguments, got %d", enumType.Name+"."+variant.Name, len(variant.Payload), len(callExpr.Args))
	}
	limit := len(orderedArgs)
	if len(variant.Payload) < limit {
		limit = len(variant.Payload)
	}
	for i := 0; i < len(orderedArgs); i++ {
		if i < limit {
			var actual Type
			orderedArgs[i], actual, ok = a.analyzePackedEnumConstructorArg(orderedArgs[i], variant, i, enumType.Name)
			if !ok {
				label := variant.PayloadLabel(i)
				if label != "" {
					a.errorf(orderedArgs[i].Pos(), "enum constructor argument %d (%s) to %q expects %s, got %s", i+1, label, enumType.Name+"."+variant.Name, variant.Payload[i], actual)
				} else {
					a.errorf(orderedArgs[i].Pos(), "enum constructor argument %d to %q expects %s, got %s", i+1, enumType.Name+"."+variant.Name, variant.Payload[i], actual)
				}
			}
		} else {
			a.analyzeExpr(orderedArgs[i])
		}
	}
	for _, commonDecl := range enumType.Decl.Common {
		arg, ok := commonArgs[commonDecl.Name]
		if !ok {
			continue
		}
		field, ok := enumType.Common[commonDecl.Name]
		if !ok {
			a.analyzeExpr(arg)
			continue
		}
		var actual Type
		commonArgs[commonDecl.Name], actual = a.analyzeCallLikeValueExpr(arg, field.Type)
		if !AssignableTo(field.Type, actual) {
			a.errorf(commonArgs[commonDecl.Name].Pos(), "packed enum common field %q for %q expects %s, got %s", commonDecl.Name, enumType.Name+"."+variant.Name, field.Type, actual)
		}
		a.consumeAffineValueExpr(commonArgs[commonDecl.Name], field.Type, "move into enum common field "+strconv.Quote(commonDecl.Name))
	}
	return enumType
}

func (a *Analyzer) analyzePackedEnumConstructorArg(expr ast.Expr, variant *EnumVariant, index int, enumName string) (ast.Expr, Type, bool) {
	if variant == nil || index < 0 || index >= len(variant.Payload) {
		actual := a.analyzeExpr(expr)
		return expr, actual, false
	}
	expected := variant.Payload[index]
	if tailIndex, ok := variant.TailPayloadIndex(); ok && tailIndex == index {
		if expectedView, ok := expected.(*DArrayViewType); ok {
			actual, ok := a.analyzePackedEnumTailPayloadArg(expr, expectedView, a.enumConstructorMoveReason(enumName, variant, index))
			return expr, actual, ok
		}
	}
	rewritten, actual := a.analyzeCallLikeValueExpr(expr, expected)
	ok := AssignableTo(expected, actual)
	a.consumeAffineValueExpr(rewritten, expected, a.enumConstructorMoveReason(enumName, variant, index))
	return rewritten, actual, ok
}

func (a *Analyzer) analyzePackedEnumTailPayloadArg(expr ast.Expr, expected *DArrayViewType, moveReason string) (Type, bool) {
	if expected == nil {
		actual := a.analyzeExpr(expr)
		return actual, false
	}
	if list, ok := expr.(*ast.ListLitExpr); ok {
		for _, elem := range list.Elems {
			actualElem := a.analyzeValueExpr(elem, expected.Elem)
			if !AssignableTo(expected.Elem, actualElem) {
				a.errorf(elem.Pos(), "tail payload element expects %s, got %s", expected.Elem, actualElem)
			}
			a.consumeAffineValueExpr(elem, expected.Elem, moveReason)
		}
		arrayType := &ArrayType{Elem: expected.Elem, Size: strconv.Itoa(len(list.Elems)), HasConstSize: true, ConstSize: int64(len(list.Elems))}
		a.exprTypes[list] = arrayType
		return arrayType, true
	}
	actual := a.analyzeValueExpr(expr, expected)
	ok := AssignableTo(expected, actual) || packedEnumTailPayloadSourceCompatible(expected, actual)
	a.consumeAffineValueExpr(expr, expected, moveReason)
	return actual, ok
}

func packedEnumTailPayloadSourceCompatible(expected *DArrayViewType, actual Type) bool {
	if expected == nil || actual == nil {
		return false
	}
	if actualView, ok := actual.(*DArrayViewType); ok {
		return SameType(expected.Elem, actualView.Elem)
	}
	if actualView, ok := actual.(*ViewType); ok {
		return SameType(expected.Elem, actualView.Elem)
	}
	return false
}
