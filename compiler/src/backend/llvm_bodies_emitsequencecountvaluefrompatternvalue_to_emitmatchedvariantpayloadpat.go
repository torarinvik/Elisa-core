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
	"elisacore/src/semantic"
	"fmt"
	"strconv"
)

func (s *functionState) emitSequenceCountValueFromPatternValue(actualValue C.LLVMValueRef, actualType semantic.Type, originExpr ast.Expr, name string) (C.LLVMValueRef, error) {
	actualType = semantic.StripAggregateStateType(actualType)
	switch t := actualType.(type) {
	case *semantic.ArrayType:
		return s.safeIndexArrayCountValue(t)
	case *semantic.DArrayType, *semantic.ViewType:
		if actualValue == nil {
			if originExpr == nil {
				return nil, fmt.Errorf("count pattern requires a sequence value")
			}
			return s.emitSequenceCountValue(originExpr, actualType, name)
		}
		sourceAlloca, err := s.emitStackTempValue(actualValue, actualType, name+".source")
		if err != nil {
			return nil, err
		}
		return s.emitContainerCountValue(sourceAlloca, t, name)
	case *semantic.RefType:
		if t.State != semantic.RefStateNonNull {
			return nil, fmt.Errorf("count pattern requires a non-null sequence reference")
		}
		switch elem := semantic.StripAggregateStateType(t.Elem).(type) {
		case *semantic.ArrayType:
			return s.safeIndexArrayCountValue(elem)
		case *semantic.DArrayType, *semantic.ViewType:
			if actualValue == nil {
				return nil, fmt.Errorf("count pattern requires a sequence reference value")
			}
			return s.emitContainerCountValue(actualValue, elem, name)
		default:
			return nil, fmt.Errorf("count pattern length is not implemented for %s", actualType.String())
		}
	case *semantic.DStrType, *semantic.SViewType:
		if originExpr == nil {
			return nil, fmt.Errorf("count pattern length requires an origin expression for %s", actualType.String())
		}
		return s.emitSequenceCountValue(originExpr, actualType, name)
	default:
		return nil, fmt.Errorf("count pattern length is not implemented for %s", actualType.String())
	}
}
func (s *functionState) emitCountMatchPatternTest(pattern ast.MatchPattern, actualValue C.LLVMValueRef, actualType semantic.Type, originExpr ast.Expr, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) (bool, error) {
	expected, ok, err := countMatchPatternExpectedLen(pattern)
	if !ok || err != nil {
		return ok, err
	}
	if _, sequence := semantic.SequenceMatchElementType(actualType); !sequence {
		return true, fmt.Errorf("count pattern requires an array, darray, view, or string-like value, got %s", actualType.String())
	}
	countValue, err := s.emitSequenceCountValueFromPatternValue(actualValue, actualType, originExpr, "match.count")
	if err != nil {
		return true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return true, err
	}
	expectedValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(expected), 0)
	countOK := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), countValue, expectedValue, cStringFree("match.count.ok"))
	C.LLVMBuildCondBr(s.builder, countOK, successBB, failureBB)
	return true, nil
}
func (s *functionState) emitListMatchPatternTest(pattern *ast.MatchListPattern, actualValue C.LLVMValueRef, actualType semantic.Type, originExpr ast.Expr, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) error {
	if pattern == nil {
		C.LLVMBuildBr(s.builder, successBB)
		return nil
	}
	if originExpr == nil && actualValue == nil {
		return fmt.Errorf("list pattern lowering requires an origin expression or sequence value")
	}
	elemType, ok := semantic.SequenceMatchElementType(actualType)
	if !ok {
		return fmt.Errorf("list pattern requires an array, darray, view, or string-like value, got %s", actualType.String())
	}
	countValue, err := s.emitSequenceCountValueFromPatternValue(actualValue, actualType, originExpr, "match.list.len")
	if err != nil {
		return err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	hasRest := false
	expectedCount := len(pattern.Elems)
	if expectedCount != 0 {
		if _, ok := pattern.Elems[expectedCount-1].(*ast.MatchRestPattern); ok {
			hasRest = true
			expectedCount--
		}
	}
	expectedLen := C.LLVMConstInt(usizeLLVMType, C.ulonglong(expectedCount), 0)
	predicate := C.LLVMIntPredicate(C.LLVMIntEQ)
	if hasRest {
		predicate = C.LLVMIntPredicate(C.LLVMIntUGE)
	}
	lenOK := C.LLVMBuildICmp(s.builder, predicate, countValue, expectedLen, cStringFree("match.list.len.ok"))
	itemsBB := successBB
	if expectedCount != 0 {
		itemsBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.list.items"))
	}
	C.LLVMBuildCondBr(s.builder, lenOK, itemsBB, failureBB)
	if expectedCount == 0 {
		return nil
	}
	C.LLVMPositionBuilderAtEnd(s.builder, itemsBB)
	var sourceAlloca C.LLVMValueRef
	for i := 0; i < expectedCount; i++ {
		elem := pattern.Elems[i]
		var indexExpr ast.Expr
		var elemValue C.LLVMValueRef
		if actualValue == nil && originExpr != nil {
			indexExpr = &ast.IndexExpr{
				Position: elem.Pos(),
				Object:   originExpr,
				Index:    &ast.IntLit{Position: elem.Pos(), Value: strconv.Itoa(i), Suffix: "u"},
			}
			var err error
			elemValue, _, err = s.emitExpr(indexExpr, elemType)
			if err != nil {
				return err
			}
		} else {
			if sourceAlloca == nil {
				var err error
				sourceAlloca, err = s.emitStackTempValue(actualValue, actualType, "match.list.source")
				if err != nil {
					return err
				}
			}
			indexValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(i), 0)
			var err error
			elemValue, _, err = s.emitIterLoopElementValue(nil, sourceAlloca, actualType, indexValue, "match.list")
			if err != nil {
				return err
			}
		}
		nextSuccess := successBB
		if i != expectedCount-1 {
			nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.list.pattern.next"))
		}
		if _, _, err := s.emitMatchPatternTest(elem, elemValue, nil, elemType, nil, indexExpr, nil, nextSuccess, failureBB); err != nil {
			return err
		}
		if i != expectedCount-1 {
			C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
		}
	}
	return nil
}
func (s *functionState) emitMatchPatternTest(pattern ast.MatchPattern, actualValue C.LLVMValueRef, decodedActualValue C.LLVMValueRef, actualType semantic.Type, store *packedStoreBinding, originExpr ast.Expr, precomputedTagValue C.LLVMValueRef, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) (C.LLVMValueRef, packedPayloadValueCache, error) {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		C.LLVMBuildBr(s.builder, successBB)
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchBindPattern:
		// docs/77 §2 category arm (`Statement:` / `Statement s:`) on a hierarchy scrutinee:
		// dispatch is the docs/81 range primitive over the category's leaf-tag range; the
		// binder is the same value at the narrowed static type (a no-op re-bind).
		if enumType, ok := semantic.StripAggregateStateType(actualType).(*semantic.EnumType); ok {
			if category, isCategory := s.enumCategoryArm(enumType, p.Name); isCategory {
				if p.Binder != "" {
					alloca, err := s.createEntryAlloca(p.Binder, category)
					if err != nil {
						return nil, packedPayloadValueCache{}, err
					}
					C.LLVMBuildStore(s.builder, actualValue, alloca)
					s.defineBinding(p.Binder, valueBinding{ptr: alloca, typ: category})
					if enumType.Packed {
						s.bindPackedEnumStoreOrigin(p.Binder, enumType, store)
					}
				}
				if semantic.EnumDescendsFrom(enumType, category) {
					C.LLVMBuildBr(s.builder, successBB) // covers the whole scrutinee
					return decodedActualValue, packedPayloadValueCache{}, nil
				}
				tagValue := precomputedTagValue
				if tagValue == nil {
					var err error
					tagValue, err = s.extractEnumTagValue(actualValue, decodedActualValue, enumType, store)
					if err != nil {
						return nil, packedPayloadValueCache{}, err
					}
				}
				cond, _, err := s.emitTagRangeTest(tagValue, enumType, category.LeafTagLo, category.LeafTagCount)
				if err != nil {
					return nil, packedPayloadValueCache{}, err
				}
				C.LLVMBuildCondBr(s.builder, cond, successBB, failureBB)
				return decodedActualValue, packedPayloadValueCache{}, nil
			}
		}
		if handled, err := s.emitPredicateMatchPatternTest(p.Name, actualValue, actualType, successBB, failureBB); handled || err != nil {
			return decodedActualValue, packedPayloadValueCache{}, err
		}
		if p.Name != "" && p.Name != "_" {
			if existing, ok := s.lookupBinding(p.Name); ok && existing.ptr != nil && semantic.SameType(existing.typ, actualType) {
				C.LLVMBuildStore(s.builder, actualValue, existing.ptr)
			} else {
				alloca, err := s.createEntryAlloca(p.Name, actualType)
				if err != nil {
					return nil, packedPayloadValueCache{}, err
				}
				C.LLVMBuildStore(s.builder, actualValue, alloca)
				s.defineBinding(p.Name, valueBinding{ptr: alloca, typ: actualType})
			}
		}
		if enumType, ok := actualType.(*semantic.EnumType); ok && enumType.Packed && decodedActualValue != nil {
			s.bindPackedEnumStorage(p.Name, enumType, decodedActualValue)
		}
		if enumType, ok := actualType.(*semantic.EnumType); ok && enumType.Packed {
			s.bindPackedEnumStoreOrigin(p.Name, enumType, store)
		}
		C.LLVMBuildBr(s.builder, successBB)
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchLiteralPattern:
		if err := s.emitLiteralMatchPatternTest(p.Value, actualValue, actualType, successBB, failureBB); err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchStringLiteralPattern:
		if err := s.emitLiteralMatchPatternTest(&ast.StringLit{Position: p.Position, Value: p.Value}, actualValue, actualType, successBB, failureBB); err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchOrPattern:
		if len(p.Options) == 0 {
			C.LLVMBuildBr(s.builder, failureBB)
			return decodedActualValue, packedPayloadValueCache{}, nil
		}
		bindings, err := s.collectOrMatchPatternBindings(p, actualType)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		for name, typ := range bindings {
			if _, ok := s.lookupBinding(name); ok {
				continue
			}
			alloca, err := s.createEntryAlloca(name+".or", typ)
			if err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			s.defineBinding(name, valueBinding{ptr: alloca, typ: typ, mutable: false})
		}
		for i, option := range p.Options {
			nextFailure := failureBB
			if i != len(p.Options)-1 {
				nextFailure = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.or.next"))
			}
			if _, _, err := s.emitMatchPatternTest(option, actualValue, decodedActualValue, actualType, store, originExpr, precomputedTagValue, successBB, nextFailure); err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			if i != len(p.Options)-1 {
				C.LLVMPositionBuilderAtEnd(s.builder, nextFailure)
			}
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchTuplePattern:
		fields, err := s.resolveTupleMatchPatternElems(p, actualType)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		if len(p.Elems) == 0 {
			C.LLVMBuildBr(s.builder, successBB)
			return decodedActualValue, packedPayloadValueCache{}, nil
		}
		for i, elem := range p.Elems {
			fieldValue := C.LLVMBuildExtractValue(s.builder, actualValue, C.unsigned(fields[i].Index), cStringFree("match.tuple.field"))
			var fieldOrigin ast.Expr
			if originExpr != nil {
				fieldOrigin = &ast.FieldExpr{Position: elem.Pos(), Object: originExpr, Field: fields[i].Decl.Name}
			}
			nextSuccess := successBB
			if i != len(p.Elems)-1 {
				nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.tuple.pattern.next"))
			}
			if _, _, err := s.emitMatchPatternTest(elem, fieldValue, nil, fields[i].Type, nil, fieldOrigin, nil, nextSuccess, failureBB); err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			if i != len(p.Elems)-1 {
				C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
			}
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchListPattern:
		if err := s.emitListMatchPatternTest(p, actualValue, actualType, originExpr, successBB, failureBB); err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchRestPattern:
		C.LLVMBuildBr(s.builder, successBB)
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchStructPattern:
		if handled, err := s.emitCountMatchPatternTest(p, actualValue, actualType, originExpr, successBB, failureBB); handled || err != nil {
			return decodedActualValue, packedPayloadValueCache{}, err
		}
		fields, orderedArgs, err := s.resolveStructMatchPatternArgs(p, actualType)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		matchedIndexes := make([]int, 0, len(orderedArgs))
		for i, arg := range orderedArgs {
			if arg != nil {
				matchedIndexes = append(matchedIndexes, i)
			}
		}
		if len(matchedIndexes) == 0 {
			C.LLVMBuildBr(s.builder, successBB)
			return decodedActualValue, packedPayloadValueCache{}, nil
		}
		for i, fieldIndex := range matchedIndexes {
			arg := orderedArgs[fieldIndex]
			fieldValue, err := s.emitStructMatchFieldValue(actualValue, actualType, fields[fieldIndex], "match.struct.field")
			if err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			var fieldOrigin ast.Expr
			if originExpr != nil {
				fieldOrigin = &ast.FieldExpr{Position: arg.Position, Object: originExpr, Field: fields[fieldIndex].Decl.Name}
			}
			nextSuccess := successBB
			if i != len(matchedIndexes)-1 {
				nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.struct.pattern.next"))
			}
			if _, _, err := s.emitMatchPatternTest(arg.Pattern, fieldValue, nil, fields[fieldIndex].Type, nil, fieldOrigin, nil, nextSuccess, failureBB); err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			if i != len(matchedIndexes)-1 {
				C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
			}
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchVariantPattern:
		if errorSetType, ok := resolveMatchableErrorSetTypeBackend(actualType); ok {
			if p.EnumName != errorSetType.Name {
				return nil, packedPayloadValueCache{}, fmt.Errorf("match arm expects error set %s, got %s", errorSetType.Name, p.EnumName)
			}
			if !errorSetType.HasQualifiedTag(errorSetType.Name, p.Variant) {
				return nil, packedPayloadValueCache{}, fmt.Errorf("error set %s has no tag %s", errorSetType.Name, p.Variant)
			}
			qualifiedTag := semantic.QualifyErrorTag(errorSetType.Name, p.Variant)
			payloadTypes := errorSetType.PayloadForTag(qualifiedTag)
			if len(p.Args) != len(payloadTypes) {
				return nil, packedPayloadValueCache{}, fmt.Errorf("match arm %q expects %d payload patterns, got %d", errorSetType.Name+"."+p.Variant, len(payloadTypes), len(p.Args))
			}
			errorCodeValue, err := s.extractErrorSetCode(actualValue, errorSetType)
			if err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			code, ok := errorSetType.TagCode(qualifiedTag)
			if !ok {
				return nil, packedPayloadValueCache{}, fmt.Errorf("missing error tag %s", qualifiedTag)
			}
			memberValue, err := s.errorCodeConstant(code)
			if err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			pred := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), errorCodeValue, memberValue, cStringFree("match.tag"))
			if len(p.Args) == 0 {
				C.LLVMBuildCondBr(s.builder, pred, successBB, failureBB)
				return decodedActualValue, packedPayloadValueCache{}, nil
			}
			payloadBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.error.payload"))
			C.LLVMBuildCondBr(s.builder, pred, payloadBB, failureBB)
			C.LLVMPositionBuilderAtEnd(s.builder, payloadBB)
			payloadValues, err := s.extractErrorSetPayloadValues(actualValue, errorSetType, qualifiedTag)
			if err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			lastNestedPattern := -1
			for i := range p.Args {
				if p.Args[i].Pattern != nil {
					lastNestedPattern = i
				}
			}
			if lastNestedPattern < 0 {
				C.LLVMBuildBr(s.builder, successBB)
				return decodedActualValue, packedPayloadValueCache{}, nil
			}
			for i := range p.Args {
				arg := &p.Args[i]
				if arg.Name != "" {
					return nil, packedPayloadValueCache{}, fmt.Errorf("match arm %s.%s uses named payload patterns but error payloads are unnamed", p.EnumName, p.Variant)
				}
				if arg.Pattern == nil {
					continue
				}
				nextSuccess := successBB
				if i != lastNestedPattern {
					nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.error.pattern.next"))
				}
				if _, _, err := s.emitMatchPatternTest(arg.Pattern, payloadValues[i], nil, payloadTypes[i], store, nil, nil, nextSuccess, failureBB); err != nil {
					return nil, packedPayloadValueCache{}, err
				}
				if i != lastNestedPattern {
					C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
				}
			}
			return decodedActualValue, packedPayloadValueCache{}, nil
		}
		if constEnumType, ok := resolveMatchableConstEnumType(actualType); ok {
			if p.EnumName != constEnumType.Name {
				return nil, packedPayloadValueCache{}, fmt.Errorf("match arm expects const enum %s, got %s", constEnumType.Name, p.EnumName)
			}
			member, ok := constEnumType.Member(p.Variant)
			if !ok {
				return nil, packedPayloadValueCache{}, fmt.Errorf("const enum %s has no member %s", constEnumType.Name, p.Variant)
			}
			if len(p.Args) != 0 {
				return nil, packedPayloadValueCache{}, fmt.Errorf("match arm %q expects 0 payload patterns, got %d", constEnumType.Name+"."+p.Variant, len(p.Args))
			}
			memberValue, _, err := s.emitConstEnumMemberExpr(constEnumType, member)
			if err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			pred := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), actualValue, memberValue, cStringFree("match.tag"))
			C.LLVMBuildCondBr(s.builder, pred, successBB, failureBB)
			return decodedActualValue, packedPayloadValueCache{}, nil
		}
		enumType, ok := actualType.(*semantic.EnumType)
		if !ok {
			return nil, packedPayloadValueCache{}, fmt.Errorf("variant pattern %s.%s requires enum, const enum, or error set type, got %s", p.EnumName, p.Variant, actualType.String())
		}
		if enumType.Packed && store == nil {
			if b, ok2 := s.lookupPackedStore(enumType); ok2 {
				store = &b
			}
		}
		variant, ok := s.resolveEnumArmVariant(enumType, p)
		if !ok {
			return nil, packedPayloadValueCache{}, fmt.Errorf("enum %s has no variant %s", enumType.Name, p.Variant)
		}
		tagValue := precomputedTagValue
		if tagValue == nil {
			var err error
			tagValue, err = s.extractEnumTagValue(actualValue, decodedActualValue, enumType, store)
			if err != nil {
				return nil, packedPayloadValueCache{}, err
			}
		}
		tagConst, err := s.enumTagConstant(enumType, variant.Tag)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		pred := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), tagValue, tagConst, cStringFree("match.tag"))
		matchedBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.pattern.ok"))
		C.LLVMBuildCondBr(s.builder, pred, matchedBB, failureBB)

		C.LLVMPositionBuilderAtEnd(s.builder, matchedBB)
		return s.emitMatchedVariantPayloadPatternTest(p, actualValue, decodedActualValue, enumType, variant, store, originExpr, successBB, failureBB)
	default:
		return nil, packedPayloadValueCache{}, fmt.Errorf("unsupported match pattern %T", pattern)
	}
}
func (s *functionState) emitStructMatchFieldValue(actualValue C.LLVMValueRef, actualType semantic.Type, field structLiteralField, name string) (C.LLVMValueRef, error) {
	return C.LLVMBuildExtractValue(s.builder, actualValue, C.unsigned(field.Index), cStringFree(name)), nil
}
func (s *functionState) emitLiteralMatchPatternTest(literalExpr ast.Expr, actualValue C.LLVMValueRef, actualType semantic.Type, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) error {
	if literalExpr == nil {
		return fmt.Errorf("missing literal pattern expression")
	}
	actualType = semantic.StripAggregateStateType(actualType)
	literalType := s.exprType(literalExpr)
	if helperName, firstType, secondType, swap, ok := runtimeStringCompareInfo(actualType, literalType); ok {
		cmp, err := s.emitRuntimeStringCompareLiteralValue(actualValue, literalExpr, literalType, helperName, firstType, secondType, swap)
		if err != nil {
			return err
		}
		C.LLVMBuildCondBr(s.builder, cmp, successBB, failureBB)
		return nil
	}
	comparisonType := actualType
	if semantic.IsNumericType(actualType) && semantic.IsNumericType(literalType) {
		comparisonType = semantic.CommonNumericType(actualType, literalType)
	}
	coercedActual := actualValue
	if comparisonType != actualType {
		var err error
		coercedActual, err = s.coerceValue(actualValue, actualType, comparisonType)
		if err != nil {
			return err
		}
	}
	literalValue, _, err := s.emitExpr(literalExpr, comparisonType)
	if err != nil {
		return err
	}
	if isFloatType(comparisonType) {
		cmp := C.LLVMBuildFCmp(s.builder, C.LLVMRealOEQ, coercedActual, literalValue, cStringFree("match.literal.eq"))
		C.LLVMBuildCondBr(s.builder, cmp, successBB, failureBB)
		return nil
	}
	cmp := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), coercedActual, literalValue, cStringFree("match.literal.eq"))
	C.LLVMBuildCondBr(s.builder, cmp, successBB, failureBB)
	return nil
}
func (s *functionState) emitRuntimeStringCompareLiteralValue(actualValue C.LLVMValueRef, literalExpr ast.Expr, literalType semantic.Type, helperName string, firstType semantic.Type, secondType semantic.Type, swap bool) (C.LLVMValueRef, error) {
	firstValue := actualValue
	secondValue, _, err := s.emitExpr(literalExpr, literalType)
	if err != nil {
		return nil, err
	}
	if swap {
		firstValue, secondValue = secondValue, firstValue
	}
	helperReturn := s.g.result.NamedTypes["int"]
	helperType := &semantic.FuncType{Name: helperName, Params: []semantic.Type{firstType, secondType}, Return: helperReturn}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	call := s.buildCall(llvmFnType, callee, []C.LLVMValueRef{firstValue, secondValue}, "match.literal.str")
	helperLLVMType, err := s.g.lowerType(helperReturn)
	if err != nil {
		return nil, err
	}
	zero := C.LLVMConstInt(helperLLVMType, 0, 0)
	return C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), call, zero, cStringFree("match.literal.str.eq")), nil
}
func (s *functionState) emitMatchedVariantPayloadPatternTest(pattern *ast.MatchVariantPattern, actualValue C.LLVMValueRef, decodedActualValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, store *packedStoreBinding, originExpr ast.Expr, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) (C.LLVMValueRef, packedPayloadValueCache, error) {
	matchedDecodedValue := decodedActualValue
	if pattern == nil || variant == nil {
		C.LLVMBuildBr(s.builder, successBB)
		return matchedDecodedValue, packedPayloadValueCache{}, nil
	}
	if len(pattern.Args) == 0 {
		C.LLVMBuildBr(s.builder, successBB)
		return matchedDecodedValue, packedPayloadValueCache{}, nil
	}
	namedCount := 0
	for i := range pattern.Args {
		if pattern.Args[i].Name != "" {
			namedCount++
		}
	}
	var orderedArgs []*ast.MatchPatternArg
	if namedCount == 0 {
		if len(pattern.Args) != len(variant.Payload) {
			return nil, packedPayloadValueCache{}, fmt.Errorf("match arm %s.%s expects %d payload patterns, got %d", pattern.EnumName, pattern.Variant, len(variant.Payload), len(pattern.Args))
		}
		orderedArgs = make([]*ast.MatchPatternArg, len(pattern.Args))
		for i := range pattern.Args {
			orderedArgs[i] = &pattern.Args[i]
		}
	} else {
		if namedCount != len(pattern.Args) {
			return nil, packedPayloadValueCache{}, fmt.Errorf("match arm %s.%s cannot mix positional and named payload patterns", pattern.EnumName, pattern.Variant)
		}
		var err error
		orderedArgs, err = s.resolveMatchPatternArgs(pattern, variant)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
		}
	}
	lastNestedPattern := -1
	for i := range orderedArgs {
		if orderedArgs[i] != nil && orderedArgs[i].Pattern != nil {
			lastNestedPattern = i
		}
	}
	if lastNestedPattern < 0 {
		C.LLVMBuildBr(s.builder, successBB)
		return matchedDecodedValue, packedPayloadValueCache{}, nil
	}
	payloadValues, err := s.extractEnumVariantPayloadValues(actualValue, matchedDecodedValue, enumType, variant, store, originExpr)
	if err != nil {
		return nil, packedPayloadValueCache{}, err
	}
	var cachedPayloads packedPayloadValueCache
	for i, value := range payloadValues {
		if value == nil {
			continue
		}
		cachedPayloads.add(variant.PayloadLabel(i), value)
	}
	for i := range orderedArgs {
		arg := orderedArgs[i]
		if arg == nil || arg.Pattern == nil {
			continue
		}
		nextSuccess := successBB
		if i != lastNestedPattern {
			nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.pattern.next"))
		}
		if _, _, err := s.emitMatchPatternTest(arg.Pattern, payloadValues[i], nil, variant.Payload[i], s.nestedPayloadPatternStore(variant.Payload[i], enumType, store), nil, nil, nextSuccess, failureBB); err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		if i != lastNestedPattern {
			C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
		}
	}
	return matchedDecodedValue, cachedPayloads, nil
}

// nestedPayloadPatternStore picks the store binding to thread into a NESTED payload pattern.
// Stores are per hierarchy ROOT (docs/77): the outer scrutinee's store is only meaningful for a
// payload field handle of the SAME root. A payload of a different packed hierarchy (`Decl.TypeDecl`
// carrying a `Type` handle) must NOT inherit the outer store — reading the inner handle's tag
// against the wrong store yields garbage and a silent false mismatch. Passing nil instead lets the
// nested MatchVariantPattern case resolve the field enum's own store via lookupPackedStore.
func (s *functionState) nestedPayloadPatternStore(fieldType semantic.Type, outerEnum *semantic.EnumType, store *packedStoreBinding) *packedStoreBinding {
	if store == nil {
		return nil
	}
	fieldEnum, ok := semantic.StripAggregateStateType(fieldType).(*semantic.EnumType)
	if !ok || fieldEnum == nil || !fieldEnum.Packed {
		return store
	}
	if outerEnum != nil && fieldEnum.Root() == outerEnum.Root() {
		return store
	}
	return nil
}
