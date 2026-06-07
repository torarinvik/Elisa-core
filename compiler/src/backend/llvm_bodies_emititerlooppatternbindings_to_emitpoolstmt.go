//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

void elisa_coreSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight);
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"elisacore/src/unparse"
	"fmt"
)

func (s *functionState) emitIterLoopPatternBindings(pattern ast.MoveBindPattern, mode ast.IterBindMode, itemType semantic.Type, itemValue C.LLVMValueRef, itemPtr C.LLVMValueRef) error {
	switch p := pattern.(type) {
	case *ast.MoveBindNamePattern:
		if mode == ast.IterBindValue {
			return s.emitMoveBindLocal(p.Name, itemType, itemValue)
		}
		return s.emitIterLoopRefLocal(p.Name, &semantic.RefType{Elem: itemType, Mutable: mode == ast.IterBindMutableRef, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny}, itemPtr)
	case *ast.MoveBindStructPattern:
		if p.Brace {
			if _, ok := semantic.StripAggregateStateType(itemType).(*semantic.StoreRowViewType); ok {
				if mode != ast.IterBindValue {
					return fmt.Errorf("iterable loop does not support ref binding for %s", itemType.String())
				}
				tempName := s.g.nextSyntheticName("iter.destructure.row.")
				tempAlloca, err := s.createEntryAlloca(tempName, itemType)
				if err != nil {
					return err
				}
				s.defineBinding(tempName, valueBinding{ptr: tempAlloca, typ: itemType, mutable: false})
				C.LLVMBuildStore(s.builder, itemValue, tempAlloca)
				tempIdent := &ast.Ident{Position: p.Position, Name: tempName}
				for _, arg := range p.Args {
					if arg.Name == "_" {
						continue
					}
					fieldExpr := &ast.FieldExpr{Position: arg.Position, Object: tempIdent, Field: moveBindFieldName(arg)}
					fieldValue, fieldType, err := s.emitExpr(fieldExpr, nil)
					if err != nil {
						return err
					}
					if err := s.emitMoveBindLocal(arg.Name, fieldType, fieldValue); err != nil {
						return err
					}
				}
				return nil
			}
		}
		fields, err := s.g.structLiteralFields(itemType)
		if err != nil {
			return err
		}
		if p.Brace {
			if mode == ast.IterBindValue {
				for _, arg := range p.Args {
					if arg.Name == "_" {
						continue
					}
					fieldName := moveBindFieldName(arg)
					field, ok := lookupStructLiteralField(fields, fieldName)
					if !ok {
						return fmt.Errorf("unknown field %q in iterable destructuring", fieldName)
					}
					fieldValue := C.LLVMBuildExtractValue(s.builder, itemValue, C.unsigned(field.Index), cStringFree(arg.Name+".iter.field"))
					if err := s.emitMoveBindLocal(arg.Name, field.Type, fieldValue); err != nil {
						return err
					}
				}
				return nil
			}
			containerLLVMType, err := s.g.lowerType(semantic.StripAggregateStateType(itemType))
			if err != nil {
				return err
			}
			for _, arg := range p.Args {
				if arg.Name == "_" {
					continue
				}
				fieldName := moveBindFieldName(arg)
				field, ok := lookupStructLiteralField(fields, fieldName)
				if !ok {
					return fmt.Errorf("unknown field %q in iterable destructuring", fieldName)
				}
				fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, itemPtr, C.unsigned(field.Index), cStringFree(arg.Name+".iter.field.ptr"))
				refType := &semantic.RefType{Elem: field.Type, Mutable: mode == ast.IterBindMutableRef, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny}
				if err := s.emitIterLoopRefLocal(arg.Name, refType, fieldPtr); err != nil {
					return err
				}
			}
			return nil
		}
		limit := len(p.Args)
		if len(fields) < limit {
			limit = len(fields)
		}
		if mode == ast.IterBindValue {
			for i := 0; i < limit; i++ {
				if p.Args[i].Name == "_" {
					continue
				}
				fieldValue := C.LLVMBuildExtractValue(s.builder, itemValue, C.unsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.field"))
				if err := s.emitMoveBindLocal(p.Args[i].Name, fields[i].Type, fieldValue); err != nil {
					return err
				}
			}
			return nil
		}
		containerLLVMType, err := s.g.lowerType(semantic.StripAggregateStateType(itemType))
		if err != nil {
			return err
		}
		for i := 0; i < limit; i++ {
			if p.Args[i].Name == "_" {
				continue
			}
			fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, itemPtr, C.unsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.field.ptr"))
			refType := &semantic.RefType{Elem: fields[i].Type, Mutable: mode == ast.IterBindMutableRef, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny}
			if err := s.emitIterLoopRefLocal(p.Args[i].Name, refType, fieldPtr); err != nil {
				return err
			}
		}
		return nil
	case *ast.MoveBindTuplePattern:
		fields, err := s.g.structLiteralFields(itemType)
		if err != nil {
			return err
		}
		limit := len(p.Args)
		if len(fields) < limit {
			limit = len(fields)
		}
		if mode == ast.IterBindValue {
			for i := 0; i < limit; i++ {
				if p.Args[i].Name == "_" {
					continue
				}
				fieldValue := C.LLVMBuildExtractValue(s.builder, itemValue, C.unsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.tuple.field"))
				if err := s.emitMoveBindLocal(p.Args[i].Name, fields[i].Type, fieldValue); err != nil {
					return err
				}
			}
			return nil
		}
		containerLLVMType, err := s.g.lowerType(semantic.StripAggregateStateType(itemType))
		if err != nil {
			return err
		}
		for i := 0; i < limit; i++ {
			if p.Args[i].Name == "_" {
				continue
			}
			fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, itemPtr, C.unsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.tuple.field.ptr"))
			refType := &semantic.RefType{Elem: fields[i].Type, Mutable: mode == ast.IterBindMutableRef, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny}
			if err := s.emitIterLoopRefLocal(p.Args[i].Name, refType, fieldPtr); err != nil {
				return err
			}
		}
		return nil
	case *ast.MoveBindVariantPattern:
		return fmt.Errorf("iterable loop lowering requires an irrefutable pattern, got variant pattern %s.%s", p.EnumName, p.Variant)
	default:
		return fmt.Errorf("unsupported iterable loop pattern %T", pattern)
	}
}
func (s *functionState) buildEnumerateItemValue(tupleType semantic.Type, indexValue C.LLVMValueRef, itemValue C.LLVMValueRef, itemActualType semantic.Type, name string) (C.LLVMValueRef, error) {
	tuple, ok := semantic.StripAggregateStateType(tupleType).(*semantic.TupleType)
	if !ok || tuple == nil || len(tuple.Fields) != 2 {
		return nil, fmt.Errorf("enumerate loop item requires a 2-field tuple type")
	}
	tupleLLVMType, err := s.g.lowerType(tuple)
	if err != nil {
		return nil, err
	}
	indexCoerced, err := s.coerceValue(indexValue, s.g.result.NamedTypes["usize"], tuple.Fields[0].Type)
	if err != nil {
		return nil, err
	}
	if itemActualType == nil {
		itemActualType = tuple.Fields[1].Type
	}
	itemCoerced, err := s.coerceValue(itemValue, itemActualType, tuple.Fields[1].Type)
	if err != nil {
		return nil, err
	}
	value := C.LLVMGetUndef(tupleLLVMType)
	value = C.LLVMBuildInsertValue(s.builder, value, indexCoerced, 0, cStringFree(name+".enumerate.item.index.insert"))
	value = C.LLVMBuildInsertValue(s.builder, value, itemCoerced, 1, cStringFree(name+".enumerate.item.value.insert"))
	return value, nil
}

type iterViewTransformKind int

const (
	iterViewTransformEnumerate iterViewTransformKind = iota
	iterViewTransformWhere
	iterViewTransformTreeKind
)

type iterViewTransform struct {
	kind          iterViewTransformKind
	itemType      semantic.Type
	predicate     C.LLVMValueRef
	predicateType *semantic.FuncType
	targetTag     C.LLVMValueRef
	category      *semantic.TreeCategoryType
}

func (s *functionState) peelIterViewTransforms(sourceName string, sourceAlloca C.LLVMValueRef, sourceType semantic.Type, bindMode ast.IterBindMode) (C.LLVMValueRef, semantic.Type, []iterViewTransform, error) {
	iterSourceAlloca := sourceAlloca
	iterSourceType := sourceType
	transforms := []iterViewTransform{}
	for {
		if carrierType, ok := semantic.EnumerateViewInstance(iterSourceType); ok {
			innerSourceType, ok := semantic.EnumerateViewSourceType(carrierType)
			if !ok || innerSourceType == nil {
				return nil, nil, nil, fmt.Errorf("enumerate carrier is missing its source type")
			}
			enumerateItemType, ok := semantic.EnumerateViewItemType(carrierType)
			if !ok || enumerateItemType == nil {
				return nil, nil, nil, fmt.Errorf("enumerate carrier is missing its tuple item type")
			}
			if bindMode != ast.IterBindValue {
				return nil, nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
			}
			carrierValue, err := s.loadValue(iterSourceAlloca, iterSourceType, sourceName+".enumerate.carrier")
			if err != nil {
				return nil, nil, nil, err
			}
			innerSourceAlloca, err := s.createEntryAlloca(sourceName+".enumerate.source", innerSourceType)
			if err != nil {
				return nil, nil, nil, err
			}
			innerSourceValue := C.LLVMBuildExtractValue(s.builder, carrierValue, 0, cStringFree(sourceName+".enumerate.source.extract"))
			C.LLVMBuildStore(s.builder, innerSourceValue, innerSourceAlloca)
			transforms = append(transforms, iterViewTransform{kind: iterViewTransformEnumerate, itemType: enumerateItemType})
			iterSourceAlloca = innerSourceAlloca
			iterSourceType = innerSourceType
			continue
		}
		if carrierType, ok := semantic.FilteredViewInstance(iterSourceType); ok {
			innerSourceType, ok := semantic.FilteredViewSourceType(carrierType)
			if !ok || innerSourceType == nil {
				return nil, nil, nil, fmt.Errorf("where carrier is missing its source type")
			}
			itemType, ok := semantic.FilteredViewItemType(carrierType)
			if !ok || itemType == nil {
				return nil, nil, nil, fmt.Errorf("where carrier is missing its item type")
			}
			predicateType := semantic.FilteredViewPredicateType(itemType)
			if predicateType == nil {
				return nil, nil, nil, fmt.Errorf("where carrier is missing its predicate type")
			}
			if bindMode != ast.IterBindValue {
				return nil, nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
			}
			carrierValue, err := s.loadValue(iterSourceAlloca, iterSourceType, sourceName+".where.carrier")
			if err != nil {
				return nil, nil, nil, err
			}
			innerSourceAlloca, err := s.createEntryAlloca(sourceName+".where.source", innerSourceType)
			if err != nil {
				return nil, nil, nil, err
			}
			innerSourceValue := C.LLVMBuildExtractValue(s.builder, carrierValue, 0, cStringFree(sourceName+".where.source.extract"))
			predicateValue := C.LLVMBuildExtractValue(s.builder, carrierValue, 1, cStringFree(sourceName+".where.predicate.extract"))
			C.LLVMBuildStore(s.builder, innerSourceValue, innerSourceAlloca)
			transforms = append(transforms, iterViewTransform{kind: iterViewTransformWhere, itemType: itemType, predicate: predicateValue, predicateType: predicateType})
			iterSourceAlloca = innerSourceAlloca
			iterSourceType = innerSourceType
			continue
		}
		if carrierType, ok := semantic.TreeKindFilteredViewInstance(iterSourceType); ok {
			innerSourceType, ok := semantic.TreeKindFilteredViewSourceType(carrierType)
			if !ok || innerSourceType == nil {
				return nil, nil, nil, fmt.Errorf("where_kind carrier is missing its source type")
			}
			itemType, ok := semantic.TreeKindFilteredViewItemType(carrierType)
			if !ok || itemType == nil {
				return nil, nil, nil, fmt.Errorf("where_kind carrier is missing its item type")
			}
			category, ok := itemType.(*semantic.TreeCategoryType)
			if !ok || category == nil {
				return nil, nil, nil, fmt.Errorf("where_kind carrier item must be a tree category, got %s", itemType.String())
			}
			if bindMode != ast.IterBindValue {
				return nil, nil, nil, fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
			}
			carrierValue, err := s.loadValue(iterSourceAlloca, iterSourceType, sourceName+".where_kind.carrier")
			if err != nil {
				return nil, nil, nil, err
			}
			innerSourceAlloca, err := s.createEntryAlloca(sourceName+".where_kind.source", innerSourceType)
			if err != nil {
				return nil, nil, nil, err
			}
			innerSourceValue := C.LLVMBuildExtractValue(s.builder, carrierValue, 0, cStringFree(sourceName+".where_kind.source.extract"))
			targetTag := C.LLVMBuildExtractValue(s.builder, carrierValue, 1, cStringFree(sourceName+".where_kind.tag.extract"))
			C.LLVMBuildStore(s.builder, innerSourceValue, innerSourceAlloca)
			transforms = append(transforms, iterViewTransform{kind: iterViewTransformTreeKind, itemType: itemType, targetTag: targetTag, category: category})
			iterSourceAlloca = innerSourceAlloca
			iterSourceType = innerSourceType
			continue
		}
		return iterSourceAlloca, iterSourceType, transforms, nil
	}
}

func iterLoopBaseSourceExpr(expr ast.Expr) ast.Expr {
	for {
		call, ok := expr.(*ast.CallExpr)
		if !ok || call == nil {
			return expr
		}
		switch callIdentName(call) {
		case "enumerate":
			if len(call.Args) != 1 {
				return expr
			}
			expr = call.Args[0]
		case "where":
			if len(call.Args) != 2 {
				return expr
			}
			expr = call.Args[0]
		case "where_kind":
			if len(call.Args) != 2 {
				return expr
			}
			expr = call.Args[0]
		default:
			return expr
		}
	}
}

func (s *functionState) applyIterViewTransforms(sourceName string, indexValue C.LLVMValueRef, stepBB C.LLVMBasicBlockRef, itemValue C.LLVMValueRef, itemType semantic.Type, transforms []iterViewTransform) (C.LLVMValueRef, semantic.Type, error) {
	currentValue := itemValue
	currentType := itemType
	for i := len(transforms) - 1; i >= 0; i-- {
		transform := transforms[i]
		switch transform.kind {
		case iterViewTransformWhere:
			if transform.predicateType == nil || len(transform.predicateType.Params) != 1 {
				return nil, nil, fmt.Errorf("where predicate is missing its parameter type")
			}
			predicateArg, err := s.coerceValue(currentValue, currentType, transform.predicateType.Params[0])
			if err != nil {
				return nil, nil, err
			}
			filterValue, err := s.emitFunctionValueCall(transform.predicate, transform.predicateType, []C.LLVMValueRef{predicateArg}, "where.predicate")
			if err != nil {
				return nil, nil, err
			}
			filterBool, err := s.coerceValue(filterValue, transform.predicateType.Return, s.g.result.NamedTypes["bool"])
			if err != nil {
				return nil, nil, err
			}
			filterBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("where.filter.body"))
			C.LLVMBuildCondBr(s.builder, filterBool, filterBodyBB, stepBB)
			C.LLVMPositionBuilderAtEnd(s.builder, filterBodyBB)
		case iterViewTransformTreeKind:
			if transform.category == nil || transform.targetTag == nil {
				return nil, nil, fmt.Errorf("where_kind transform is missing metadata")
			}
			tagValue, err := s.extractTreeCategoryTagValue(currentValue, transform.category)
			if err != nil {
				return nil, nil, err
			}
			targetTag, err := s.coerceValue(transform.targetTag, s.g.result.NamedTypes["u32"], s.g.result.NamedTypes["u32"])
			if err != nil {
				return nil, nil, err
			}
			filterBool := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), tagValue, targetTag, cStringFree("where_kind.cmp"))
			filterBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("where_kind.filter.body"))
			C.LLVMBuildCondBr(s.builder, filterBool, filterBodyBB, stepBB)
			C.LLVMPositionBuilderAtEnd(s.builder, filterBodyBB)
		case iterViewTransformEnumerate:
			enumeratedValue, err := s.buildEnumerateItemValue(transform.itemType, indexValue, currentValue, currentType, sourceName)
			if err != nil {
				return nil, nil, err
			}
			currentValue = enumeratedValue
			currentType = transform.itemType
		}
	}
	return currentValue, currentType, nil
}

func (s *functionState) emitEqualityCompare(left C.LLVMValueRef, leftType semantic.Type, right C.LLVMValueRef, rightType semantic.Type, name string) (C.LLVMValueRef, error) {
	rightValue, err := s.coerceValue(right, rightType, leftType)
	if err != nil {
		return nil, err
	}
	if isFloatType(leftType) {
		return C.LLVMBuildFCmp(s.builder, C.LLVMRealPredicate(C.LLVMRealOEQ), left, rightValue, cStringFree(name)), nil
	}
	return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), left, rightValue, cStringFree(name)), nil
}

type frozenTreeRowFieldEqualityFilter struct {
	category  *semantic.TreeCategoryType
	fieldName string
	fieldType semantic.Type
	target    ast.Expr
	negate    bool
}

func frozenTreeRowFieldEqualityFilterFor(pattern ast.MoveBindPattern, sourceType semantic.Type, filter ast.Expr) (frozenTreeRowFieldEqualityFilter, bool) {
	binary, ok := filter.(*ast.BinaryExpr)
	if !ok || binary == nil || (binary.Op != lexer.TOKEN_EQEQ && binary.Op != lexer.TOKEN_BANGEQ) {
		return frozenTreeRowFieldEqualityFilter{}, false
	}
	namePattern, ok := pattern.(*ast.MoveBindNamePattern)
	if !ok || namePattern == nil || namePattern.Name == "_" {
		return frozenTreeRowFieldEqualityFilter{}, false
	}
	rowsType, ok := semantic.StripAggregateStateType(sourceType).(*semantic.FrozenTreeRowsViewType)
	if !ok || rowsType == nil || rowsType.Category == nil {
		return frozenTreeRowFieldEqualityFilter{}, false
	}
	if result, ok := frozenTreeRowFieldEqualityOperand(rowsType.Category, namePattern.Name, binary.Left, binary.Right); ok {
		result.negate = binary.Op == lexer.TOKEN_BANGEQ
		return result, true
	}
	if result, ok := frozenTreeRowFieldEqualityOperand(rowsType.Category, namePattern.Name, binary.Right, binary.Left); ok {
		result.negate = binary.Op == lexer.TOKEN_BANGEQ
		return result, true
	}
	return frozenTreeRowFieldEqualityFilter{}, false
}

func frozenTreeRowFieldEqualityOperand(category *semantic.TreeCategoryType, itemName string, fieldExpr ast.Expr, target ast.Expr) (frozenTreeRowFieldEqualityFilter, bool) {
	field, ok := fieldExpr.(*ast.FieldExpr)
	if !ok || field == nil || field.Safe {
		return frozenTreeRowFieldEqualityFilter{}, false
	}
	ident, ok := field.Object.(*ast.Ident)
	if !ok || ident == nil || ident.Name != itemName {
		return frozenTreeRowFieldEqualityFilter{}, false
	}
	fieldInfo, ok := semantic.TreeCategorySurfaceFieldInfo(category, field.Field)
	if !ok || fieldInfo.Type == nil {
		return frozenTreeRowFieldEqualityFilter{}, false
	}
	if field.Field != "kind" && !frozenTreeRowFieldHasFastColumn(category, field.Field) {
		return frozenTreeRowFieldEqualityFilter{}, false
	}
	return frozenTreeRowFieldEqualityFilter{
		category:  category,
		fieldName: field.Field,
		fieldType: fieldInfo.Type,
		target:    target,
	}, true
}

func frozenTreeRowFieldHasFastColumn(category *semantic.TreeCategoryType, fieldName string) bool {
	if category == nil {
		return false
	}
	if category.Layout == semantic.TreeLayoutSOA {
		return true
	}
	for _, index := range category.Indexes {
		if !index.Kind && index.Name == fieldName {
			return true
		}
	}
	return false
}

func (s *functionState) emitFrozenTreeRowFieldEqualityFilter(rowValue C.LLVMValueRef, filter frozenTreeRowFieldEqualityFilter, name string) (C.LLVMValueRef, error) {
	var fieldValue C.LLVMValueRef
	var err error
	if filter.fieldName == "kind" {
		fieldValue, err = s.extractTreeCategoryTagValue(rowValue, filter.category)
	} else {
		fieldValue, err = s.emitTreeFrozenColumnFieldFilterValue(rowValue, filter.category, filter.fieldName, filter.fieldType, name)
	}
	if err != nil {
		return nil, err
	}
	targetValue, targetType, err := s.emitExpr(filter.target, filter.fieldType)
	if err != nil {
		return nil, err
	}
	filterBool, err := s.emitEqualityCompare(fieldValue, filter.fieldType, targetValue, targetType, name+".cmp")
	if err != nil {
		return nil, err
	}
	if filter.negate {
		filterBool = C.LLVMBuildNot(s.builder, filterBool, cStringFree(name+".not"))
	}
	return filterBool, nil
}

func (s *functionState) emitIterFilterBranch(pattern ast.MoveBindPattern, sourceType semantic.Type, rowValue C.LLVMValueRef, filter ast.Expr, passBB C.LLVMBasicBlockRef, failBB C.LLVMBasicBlockRef, name string) error {
	if filter == nil {
		C.LLVMBuildBr(s.builder, passBB)
		return nil
	}
	if binary, ok := filter.(*ast.BinaryExpr); ok && binary != nil {
		switch binary.Op {
		case lexer.TOKEN_AND:
			rightBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".and.rhs"))
			if err := s.emitIterFilterBranch(pattern, sourceType, rowValue, binary.Left, rightBB, failBB, name+".and.left"); err != nil {
				return err
			}
			C.LLVMPositionBuilderAtEnd(s.builder, rightBB)
			return s.emitIterFilterBranch(pattern, sourceType, rowValue, binary.Right, passBB, failBB, name+".and.right")
		case lexer.TOKEN_OR:
			rightBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".or.rhs"))
			if err := s.emitIterFilterBranch(pattern, sourceType, rowValue, binary.Left, passBB, rightBB, name+".or.left"); err != nil {
				return err
			}
			C.LLVMPositionBuilderAtEnd(s.builder, rightBB)
			return s.emitIterFilterBranch(pattern, sourceType, rowValue, binary.Right, passBB, failBB, name+".or.right")
		}
	}
	if rowValue != nil {
		if rowFilter, ok := frozenTreeRowFieldEqualityFilterFor(pattern, sourceType, filter); ok {
			filterBool, err := s.emitFrozenTreeRowFieldEqualityFilter(rowValue, rowFilter, name+".field")
			if err != nil {
				return err
			}
			C.LLVMBuildCondBr(s.builder, filterBool, passBB, failBB)
			return nil
		}
	}
	filterValue, filterType, err := s.emitExpr(filter, nil)
	if err != nil {
		return fmt.Errorf("emit iterable filter at %s (%T %q): %w", filter.Pos(), filter, unparse.FormatExpr(filter), err)
	}
	filterBool, err := s.coerceValue(filterValue, filterType, s.g.result.NamedTypes["bool"])
	if err != nil {
		return err
	}
	C.LLVMBuildCondBr(s.builder, filterBool, passBB, failBB)
	return nil
}

func (s *functionState) emitIterForStmt(stmt *ast.IterForStmt) error {
	// Auto-reservation (region inference, Phase A): presize the fill target before the loop.
	if stmt.PreReserve != nil {
		if err := s.emitStmt(stmt.PreReserve); err != nil {
			return err
		}
	}
	for _, preReserve := range stmt.PreReserves {
		if err := s.emitStmt(preReserve); err != nil {
			return err
		}
	}
	sourceType := s.exprType(stmt.Source)
	if sourceType == nil {
		return fmt.Errorf("missing semantic type for iterable loop source")
	}
	sourceName := s.g.nextSyntheticName("iter.src.")
	sourceAlloca, err := s.createEntryAlloca(sourceName, sourceType)
	if err != nil {
		return err
	}
	sourceValue, _, err := s.emitExpr(stmt.Source, sourceType)
	if err != nil {
		return err
	}
	C.LLVMBuildStore(s.builder, sourceValue, sourceAlloca)

	iterSourceAlloca, iterSourceType, transforms, err := s.peelIterViewTransforms(sourceName, sourceAlloca, sourceType, stmt.Mode)
	if err != nil {
		return err
	}
	iterSourceExpr := iterLoopBaseSourceExpr(stmt.Source)
	countValue, err := s.emitIterLoopCount(iterSourceExpr, iterSourceAlloca, iterSourceType, sourceName)
	if err != nil {
		return err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	indexAlloca, err := s.createEntryAlloca(sourceName+".index", usizeType)
	if err != nil {
		return err
	}
	zeroValue := C.LLVMConstInt(usizeLLVMType, 0, 0)
	C.LLVMBuildStore(s.builder, zeroValue, indexAlloca)

	condBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.cond"))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.body"))
	stepBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.step"))
	exitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.end"))
	C.LLVMBuildBr(s.builder, condBB)

	C.LLVMPositionBuilderAtEnd(s.builder, condBB)
	indexValue, err := s.loadValue(indexAlloca, usizeType, sourceName+".index")
	if err != nil {
		return err
	}
	condValue := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, countValue, cStringFree("iter.cmp"))
	C.LLVMBuildCondBr(s.builder, condValue, bodyBB, exitBB)

	itemType, ok := iterLoopItemTypeBackend(iterSourceType)
	if !ok && stmt.Mode != ast.IterBindValue {
		return fmt.Errorf("iterable loop ref binding requires an addressable array-like source, got %s", iterSourceType.String())
	}
	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	s.pushScope()
	iterIndexValue := indexValue
	if stmt.Reverse {
		lastIndex := C.LLVMBuildSub(s.builder, countValue, C.LLVMConstInt(usizeLLVMType, 1, 0), cStringFree("iter.rev.last"))
		iterIndexValue = C.LLVMBuildSub(s.builder, lastIndex, indexValue, cStringFree("iter.rev.index"))
	}
	var boundItemValue C.LLVMValueRef
	var boundItemPtr C.LLVMValueRef
	if stmt.Mode == ast.IterBindValue {
		itemValue, resolvedItemType, err := s.emitIterLoopElementValue(iterSourceExpr, iterSourceAlloca, iterSourceType, iterIndexValue, sourceName)
		if err != nil {
			s.popScope()
			return err
		}
		itemValue, resolvedItemType, err = s.applyIterViewTransforms(sourceName, iterIndexValue, stepBB, itemValue, resolvedItemType, transforms)
		if err != nil {
			s.popScope()
			return err
		}
		if itemType == nil {
			itemType = resolvedItemType
		} else {
			itemType = resolvedItemType
		}
		boundItemValue = itemValue
		if err := s.emitIterLoopPatternBindings(stmt.Pattern, stmt.Mode, itemType, itemValue, nil); err != nil {
			s.popScope()
			return err
		}
	} else {
		itemPtr, resolvedItemType, err := s.emitIterLoopElementAddress(iterSourceAlloca, iterSourceType, iterIndexValue, sourceName)
		if err != nil {
			s.popScope()
			return err
		}
		if itemType == nil {
			itemType = resolvedItemType
		}
		boundItemPtr = itemPtr
		if err := s.emitIterLoopPatternBindings(stmt.Pattern, stmt.Mode, itemType, nil, itemPtr); err != nil {
			s.popScope()
			return err
		}
	}
	if stmt.PatternFilter != nil {
		filterValue := boundItemValue
		filterType := itemType
		if stmt.PatternFilterSubject != "" {
			subject := &ast.Ident{Position: stmt.PatternFilter.Pos(), Name: stmt.PatternFilterSubject}
			filterValue, filterType, err = s.emitIdent(subject)
			if err != nil {
				s.popScope()
				return err
			}
		}
		if filterValue == nil {
			filterValue, err = s.loadValue(boundItemPtr, itemType, "iter.pattern.value")
			if err != nil {
				s.popScope()
				return err
			}
		}
		filterBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.pattern.filter.body"))
		if _, _, err := s.emitMatchPatternTest(stmt.PatternFilter, filterValue, nil, filterType, nil, nil, nil, filterBodyBB, stepBB); err != nil {
			s.popScope()
			return err
		}
		C.LLVMPositionBuilderAtEnd(s.builder, filterBodyBB)
	}
	if stmt.WhereFilter != nil {
		filterBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.where.filter.body"))
		if err := s.emitIterFilterBranch(stmt.Pattern, iterSourceType, boundItemValue, stmt.WhereFilter, filterBodyBB, stepBB, "iter.where.filter"); err != nil {
			s.popScope()
			return err
		}
		C.LLVMPositionBuilderAtEnd(s.builder, filterBodyBB)
	}
	if stmt.Filter != nil {
		filterBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.filter.body"))
		if err := s.emitIterFilterBranch(stmt.Pattern, iterSourceType, boundItemValue, stmt.Filter, filterBodyBB, stepBB, "iter.filter"); err != nil {
			s.popScope()
			return err
		}
		C.LLVMPositionBuilderAtEnd(s.builder, filterBodyBB)
	}
	s.pushLoopTargets(exitBB, stepBB)
	if err := s.emitBlock(stmt.Body, true); err != nil {
		s.popLoopTargets()
		s.popScope()
		return err
	}
	s.popLoopTargets()
	s.popScope()
	if !s.currentBlockTerminated() {
		C.LLVMBuildBr(s.builder, stepBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, stepBB)
	if !s.currentBlockTerminated() {
		nextValue := C.LLVMBuildAdd(s.builder, indexValue, C.LLVMConstInt(usizeLLVMType, 1, 0), cStringFree("iter.next"))
		C.LLVMBuildStore(s.builder, nextValue, indexAlloca)
		C.LLVMBuildBr(s.builder, condBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, exitBB)
	return nil
}
func unwrapPackedStoreOriginExpr(expr ast.Expr) ast.Expr {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return unwrapPackedStoreOriginExpr(n.Inner)
	case *ast.CastExpr:
		return unwrapPackedStoreOriginExpr(n.Operand)
	case *ast.MoveExpr:
		return unwrapPackedStoreOriginExpr(n.Operand)
	case *ast.CanExpr:
		return unwrapPackedStoreOriginExpr(n.Expr)
	default:
		return expr
	}
}
func (s *functionState) bindPackedStoreOriginsForExprPath(path string, expr ast.Expr, typ semantic.Type) error {
	if path == "" || expr == nil || typ == nil {
		return nil
	}
	stripped := semantic.StripAggregateStateType(typ)
	if enumType, ok := stripped.(*semantic.EnumType); ok {
		if !enumType.Packed {
			return nil
		}
		origin, ok, err := s.resolvePackedNodeStoreBinding(expr, enumType)
		if err != nil {
			return err
		}
		if ok {
			s.bindPackedEnumStoreOrigin(path, enumType, origin)
		}
		return nil
	}
	switch stripped.(type) {
	case *semantic.StructType, *semantic.GenericInstanceType:
	default:
		return nil
	}
	fields, err := s.g.structLiteralFields(stripped)
	if err != nil {
		return err
	}
	sourceExpr := unwrapPackedStoreOriginExpr(expr)
	if lit, ok := sourceExpr.(*ast.StructLitExpr); ok {
		args := lit.LoweredArgs()
		limit := len(fields)
		if len(args) < limit {
			limit = len(args)
		}
		for i := 0; i < limit; i++ {
			if args[i] == nil {
				continue
			}
			childPath := path + "." + fields[i].Decl.Name
			if err := s.bindPackedStoreOriginsForExprPath(childPath, args[i], fields[i].Type); err != nil {
				return err
			}
		}
		return nil
	}
	for _, field := range fields {
		childPath := path + "." + field.Decl.Name
		childExpr := &ast.FieldExpr{Position: expr.Pos(), Object: expr, Field: field.Decl.Name}
		if err := s.bindPackedStoreOriginsForExprPath(childPath, childExpr, field.Type); err != nil {
			return err
		}
	}
	return nil
}
func (s *functionState) emitInStore(stmt *ast.InStoreStmt) error {
	savedTreeOwner := s.treeAllocOwner
	if owner, ok, err := s.classifyTreeAllocOwnerExpr(stmt.Store); err != nil {
		return err
	} else if ok {
		s.treeAllocOwner = owner
		defer func() {
			s.treeAllocOwner = savedTreeOwner
		}()
		return s.emitBlock(stmt.Body, true)
	}
	storeValue, actualType, err := s.emitExpr(stmt.Store, nil)
	if err != nil {
		return err
	}
	savedStores := s.packedStores
	if treeStore, ok := actualType.(*semantic.TreeStoreType); ok {
		s.treeAllocOwner = treeAllocOwnerBinding{storeValue: storeValue, storeType: treeStore}
		defer func() {
			s.treeAllocOwner = savedTreeOwner
			s.packedStores = savedStores
		}()
		return s.emitBlock(stmt.Body, true)
	}
	storeType, ok := actualType.(*semantic.PackedEnumStoreType)
	if !ok {
		return fmt.Errorf("in-block requires a tree store, packed enum store, perm, an Arena value, or an Arena reference, got %s", actualType.String())
	}
	s.packedStores = s.clonePackedStores()
	if s.packedStores == nil {
		s.packedStores = map[string]packedStoreBinding{}
	}
	s.packedStores[storeType.Enum.Name] = packedStoreBinding{value: storeValue, typ: storeType}
	defer func() {
		s.treeAllocOwner = savedTreeOwner
		s.packedStores = savedStores
	}()
	return s.emitBlock(stmt.Body, true)
}
func (s *functionState) emitPoolStmt(stmt *ast.PoolStmt) error {
	poolType := s.g.result.NamedTypes["ThreadPool"]
	usizeType := s.g.result.NamedTypes["usize"]
	workersValue, _, err := s.emitExpr(stmt.Workers, usizeType)
	if err != nil {
		return err
	}
	poolNewType := &semantic.FuncType{Name: "pool_new", Params: []semantic.Type{usizeType}, Return: poolType}
	poolNew, err := s.g.ensureFunctionDeclared("pool_new", poolNewType)
	if err != nil {
		return err
	}
	poolNewLLVMType, err := s.g.lowerFunctionType(poolNewType)
	if err != nil {
		return err
	}
	poolValue := s.buildCall(poolNewLLVMType, poolNew, []C.LLVMValueRef{workersValue}, "pool.new")
	poolAlloca, err := s.createEntryAlloca(stmt.Name, poolType)
	if err != nil {
		return err
	}
	s.pushScope()
	defer s.popScope()
	s.defineBinding(stmt.Name, valueBinding{ptr: poolAlloca, typ: poolType})
	C.LLVMBuildStore(s.builder, poolValue, poolAlloca)
	pool := scopedCleanupBinding{kind: scopedCleanupThreadPool, name: stmt.Name, ptr: poolAlloca, typ: poolType}
	s.registerScopedCleanup(pool)
	s.poolScopes = append(s.poolScopes, activePoolBinding{name: stmt.Name, ptr: poolAlloca, typ: poolType, workers: workersValue})
	defer func() {
		s.poolScopes = s.poolScopes[:len(s.poolScopes)-1]
	}()
	return s.emitBlockInCurrentScope(stmt.Body)
}
