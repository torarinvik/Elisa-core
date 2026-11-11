//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) namedStateIsTarget(expr ast.Expr) (*semantic.StructType, []string, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return s.namedStateIsTarget(paren.Inner)
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil {
		return nil, nil, false
	}
	switch n := typedExpr.Type.(type) {
	case *ast.NamedType:
		base, ok := s.g.lookupNamedType(n.Name)
		if !ok {
			return nil, nil, false
		}
		structType, ok := base.(*semantic.StructType)
		if !ok || structType == nil || len(structType.NamedStateCases) == 0 {
			return nil, nil, false
		}
		return structType, append([]string(nil), structType.NamedStateCases...), true
	case *ast.GenericType:
		base, ok := s.g.lookupNamedType(n.Name)
		if !ok {
			return nil, nil, false
		}
		structType, ok := base.(*semantic.StructType)
		if !ok || structType == nil || len(structType.NamedStateCases) == 0 {
			return nil, nil, false
		}
		params := structGenericParams(structType)
		if len(n.Args) != len(params) {
			return nil, nil, false
		}
		for i, param := range params {
			if param.Kind != ast.GenericParamState {
				continue
			}
			cases, ok := namedStateCasesFromTypeExpr(n.Args[i])
			if !ok {
				return nil, nil, false
			}
			return structType, cases, true
		}
		return nil, nil, false
	default:
		return nil, nil, false
	}
}
func namedStateCasesFromTypeExpr(expr ast.TypeExpr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.NamedType:
		return []string{n.Name}, true
	case *ast.StateSetTypeExpr:
		return append([]string(nil), n.Cases...), true
	default:
		return nil, false
	}
}
func derivedStateSelfFieldPath(expr ast.Expr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name == "self" {
			return nil, true
		}
		return nil, false
	case *ast.FieldExpr:
		path, ok := derivedStateSelfFieldPath(n.Object)
		if !ok {
			return nil, false
		}
		return append(path, n.Field), true
	case *ast.ParenExpr:
		return derivedStateSelfFieldPath(n.Inner)
	default:
		return nil, false
	}
}
func (s *functionState) emitEnumCompareExpr(op lexer.TokenKind, enumType *semantic.EnumType, left llvmIRValueRef, right llvmIRValueRef, resultType semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	if enumType == nil {
		return nil, nil, fmt.Errorf("missing enum type for comparison")
	}
	if enumType.Packed {
		pred := llvmIRIntPredicate(llvmIntEQ)
		if op == lexer.TOKEN_BANGEQ {
			pred = llvmIRIntPredicate(llvmIntNE)
		}
		return llvmBuildICmp(s.builder, pred, left, right, cStringFree("enumcmp.packed")), resultType, nil
	}
	if enumIsTagOnly(enumType) {
		pred := llvmIRIntPredicate(llvmIntEQ)
		if op == lexer.TOKEN_BANGEQ {
			pred = llvmIRIntPredicate(llvmIntNE)
		}
		return llvmBuildICmp(s.builder, pred, left, right, cStringFree("enumcmp.tagonly")), resultType, nil
	}
	leftTag := llvmBuildExtractValue(s.builder, left, 0, cStringFree("enumcmp.left.tag"))
	rightTag := llvmBuildExtractValue(s.builder, right, 0, cStringFree("enumcmp.right.tag"))
	equal := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), leftTag, rightTag, cStringFree("enumcmp.tag.eq"))

	payloadSlots, err := s.enumPayloadWordCount(enumType)
	if err != nil {
		return nil, nil, err
	}
	if payloadSlots > 0 {
		leftPayload := llvmBuildExtractValue(s.builder, left, 1, cStringFree("enumcmp.left.payload"))
		rightPayload := llvmBuildExtractValue(s.builder, right, 1, cStringFree("enumcmp.right.payload"))
		for i := uint64(0); i < payloadSlots; i++ {
			nameSuffix := fmt.Sprintf(".%d", i)
			leftWord := llvmBuildExtractValue(s.builder, leftPayload, llvmIRUnsigned(i), cStringFree("enumcmp.left.word"+nameSuffix))
			rightWord := llvmBuildExtractValue(s.builder, rightPayload, llvmIRUnsigned(i), cStringFree("enumcmp.right.word"+nameSuffix))
			wordEqual := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), leftWord, rightWord, cStringFree("enumcmp.word.eq"+nameSuffix))
			equal = llvmBuildAnd(s.builder, equal, wordEqual, cStringFree("enumcmp.and"+nameSuffix))
		}
	}
	if op == lexer.TOKEN_BANGEQ {
		return llvmBuildNot(s.builder, equal, cStringFree("enumcmp.ne")), resultType, nil
	}
	return equal, resultType, nil
}
func (s *functionState) encodePackedEnumHandle(rowPtr llvmIRValueRef, enumType *semantic.EnumType) (llvmIRValueRef, error) {
	return s.encodePackedEnumHandleWithStore(rowPtr, enumType, nil)
}
func (s *functionState) encodePackedEnumHandleWithStore(rowPtr llvmIRValueRef, enumType *semantic.EnumType, storeValue llvmIRValueRef) (llvmIRValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum handle metadata")
	}
	storeType := enumType.StoreType
	if storeType == nil {
		return nil, fmt.Errorf("packed enum %s is missing store metadata", enumType.Name)
	}
	if storeValue == nil {
		return nil, fmt.Errorf("packed enum %s encode requires store context", enumType.Name)
	}
	return (&packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}).encodeHandle(rowPtr, enumType, "packed.encode.store")
}
func (s *functionState) decodePackedEnumHandle(handleValue llvmIRValueRef, enumType *semantic.EnumType) (llvmIRValueRef, error) {
	return s.decodePackedEnumHandleWithStore(handleValue, enumType, nil)
}
func (s *functionState) decodePackedEnumHandleWithStore(handleValue llvmIRValueRef, enumType *semantic.EnumType, store *packedStoreBinding) (llvmIRValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum handle metadata")
	}
	ops, ok := s.packedStoreOpsFromBinding(store)
	if !ok {
		return nil, fmt.Errorf("packed enum %s decode requires store context", enumType.Name)
	}
	return ops.decodeHandle(handleValue, enumType, "packed.decode.store")
}
func (s *functionState) enumPayloadWordCount(enumType *semantic.EnumType) (uint64, error) {
	if enumType == nil {
		return 0, nil
	}
	maxSlots := uint64(0)
	for _, variant := range enumType.Variants {
		slots, err := s.g.enumVariantPayloadSlots(enumType, variant)
		if err != nil {
			return 0, err
		}
		if slots > maxSlots {
			maxSlots = slots
		}
	}
	return maxSlots, nil
}
