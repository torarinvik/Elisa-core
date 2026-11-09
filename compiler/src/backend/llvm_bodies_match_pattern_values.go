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
)

func (s *functionState) emitStructMatchFieldValue(actualValue C.LLVMValueRef, actualType semantic.Type, field structLiteralField, name string) (C.LLVMValueRef, error) {
	return C.LLVMBuildExtractValue(s.builder, actualValue, C.unsigned(field.Index), cStringFree(name)), nil
}

// emitRangeMatchPatternTest emits the docs/122 §5.2 range arm (`LO..<HI` / `LO..=HI`):
// scrutinee >= LO chained through an intermediate block into scrutinee </<= HI, with
// signedness taken from the common comparison type — the two-comparison analogue of
// emitLiteralMatchPatternTest's single equality.
func (s *functionState) emitRangeMatchPatternTest(p *ast.MatchRangePattern, actualValue C.LLVMValueRef, actualType semantic.Type, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) error {
	if p.Lo == nil || p.Hi == nil {
		return fmt.Errorf("range pattern is missing a bound")
	}
	actualType = semantic.StripAggregateStateType(actualType)
	comparisonType := actualType
	for _, bound := range []ast.Expr{p.Lo, p.Hi} {
		boundType := s.exprType(bound)
		if semantic.IsNumericType(comparisonType) && semantic.IsNumericType(boundType) {
			comparisonType = semantic.CommonNumericType(comparisonType, boundType)
		}
	}
	if isFloatType(comparisonType) {
		return fmt.Errorf("range patterns require an integer or char scrutinee")
	}
	coercedActual := actualValue
	if comparisonType != actualType {
		var err error
		coercedActual, err = s.coerceValue(actualValue, actualType, comparisonType)
		if err != nil {
			return err
		}
	}
	signed := isSignedIntegerType(comparisonType)
	gePred := C.LLVMIntPredicate(C.LLVMIntUGE)
	hiPred := C.LLVMIntPredicate(C.LLVMIntULT)
	if p.Inclusive {
		hiPred = C.LLVMIntPredicate(C.LLVMIntULE)
	}
	if signed {
		gePred = C.LLVMIntPredicate(C.LLVMIntSGE)
		hiPred = C.LLVMIntPredicate(C.LLVMIntSLT)
		if p.Inclusive {
			hiPred = C.LLVMIntPredicate(C.LLVMIntSLE)
		}
	}
	loValue, _, err := s.emitExpr(p.Lo, comparisonType)
	if err != nil {
		return err
	}
	hiBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.range.hi"))
	loCmp := C.LLVMBuildICmp(s.builder, gePred, coercedActual, loValue, cStringFree("match.range.lo"))
	C.LLVMBuildCondBr(s.builder, loCmp, hiBB, failureBB)
	C.LLVMPositionBuilderAtEnd(s.builder, hiBB)
	hiValue, _, err := s.emitExpr(p.Hi, comparisonType)
	if err != nil {
		return err
	}
	hiCmp := C.LLVMBuildICmp(s.builder, hiPred, coercedActual, hiValue, cStringFree("match.range.hi.cmp"))
	C.LLVMBuildCondBr(s.builder, hiCmp, successBB, failureBB)
	return nil
}

func (s *functionState) emitLiteralMatchPatternTest(literalExpr ast.Expr, actualValue C.LLVMValueRef, actualType semantic.Type, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) error {
	if literalExpr == nil {
		return fmt.Errorf("missing literal pattern expression")
	}
	actualType = semantic.StripAggregateStateType(actualType)
	literalType := s.exprType(literalExpr)
	// A string pattern nested inside a tuple/payload position is a parser/backend-synthesized
	// StringLit with no analyzer type-table entry; without a type it would fall through to the
	// scalar icmp below and compare the view AGGREGATE (invalid IR). Compare it as the
	// scrutinee field's own string-carrier kind instead.
	if literalType == nil {
		if _, isStr := literalExpr.(*ast.StringLit); isStr && classifyRuntimeStringCompareKind(actualType) != runtimeStringCompareNone {
			literalType = actualType
		}
	}
	if literalText, ok := s.staticCStringLiteral(literalExpr); ok && classifyRuntimeStringCompareKind(actualType) == runtimeStringCompareView {
		// `actualValue` is the scrutinee already evaluated by the match lowering.
		// Reusing it preserves exactly-once evaluation while applying the same
		// length/null-guarded tiny-literal path used by view equality.
		if _, isStringLiteral := literalExpr.(*ast.StringLit); isStringLiteral {
			viewType := actualType
			if ref, isReference := actualType.(*semantic.RefType); isReference {
				if _, isView := ref.Elem.(*semantic.SViewType); isView {
					viewType = ref.Elem
					var err error
					actualValue, err = s.loadValue(actualValue, viewType, "match.sview")
					if err != nil {
						return err
					}
				} else {
					viewType = nil
				}
			}
			if viewType != nil {
				cmp, err := s.emitStringViewValueStaticLiteralEqual(actualValue, literalExpr, literalText)
				if err != nil {
					return err
				}
				C.LLVMBuildCondBr(s.builder, cmp, successBB, failureBB)
				return nil
			}
		}
	}
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
	helperReturn := s.g.namedType("int")
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
