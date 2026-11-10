//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"sort"
)

func (s *functionState) bindMatchedPackedVariantView(name string, pattern ast.MatchPattern, enumValue llvmIRValueRef, decodedValue llvmIRValueRef, enumType *semantic.EnumType, store *packedStoreBinding, payloadValues packedPayloadValueCache) {
	if enumType == nil || !enumType.Packed {
		return
	}
	if name == "" {
		return
	}
	variantPattern, ok := pattern.(*ast.MatchVariantPattern)
	if !ok {
		return
	}
	variant, ok := enumType.Variant(variantPattern.Variant)
	if !ok || variant == nil {
		return
	}
	viewType := s.g.cachedPackedVariantViewType(enumType, variant)
	if decodedValue != nil {
		storeValue := packedStoreBinding{}
		if store != nil {
			storeValue = *store
		}
		s.bindPackedVariantViewOwned(name, viewType, decodedValue, enumValue, storeValue, payloadValues)
		return
	}
	if store != nil {
		s.bindPackedVariantViewOwned(name, viewType, nil, enumValue, *store, payloadValues)
		return
	}
	if s.canInlinePackedEnumVariant(enumType, variant) {
		s.bindPackedVariantViewOwned(name, viewType, nil, enumValue, packedStoreBinding{}, payloadValues)
	}
}
func resolveMatchableEnumType(actual semantic.Type) (*semantic.EnumType, bool) {
	switch tt := actual.(type) {
	case *semantic.EnumType:
		return tt, tt != nil
	case *semantic.PackedVariantViewType:
		if tt == nil || tt.Enum == nil {
			return nil, false
		}
		return tt.Enum, true
	default:
		return nil, false
	}
}
func resolveMatchableConstEnumType(actual semantic.Type) (*semantic.ConstEnumType, bool) {
	constEnumType, ok := semantic.StripAggregateStateType(actual).(*semantic.ConstEnumType)
	return constEnumType, ok && constEnumType != nil
}
func resolveMatchableStructTypeBackend(actual semantic.Type) bool {
	switch tt := semantic.StripAggregateStateType(actual).(type) {
	case *semantic.StructType:
		return tt != nil && tt.Decl != nil
	case *semantic.GenericInstanceType:
		base, _ := tt.Base.(*semantic.StructType)
		return base != nil && base.Decl != nil
	default:
		return false
	}
}
func resolveMatchableTupleTypeBackend(actual semantic.Type) bool {
	tupleType, ok := semantic.StripAggregateStateType(actual).(*semantic.TupleType)
	return ok && tupleType != nil
}
func resolveMatchableSequenceTypeBackend(actual semantic.Type) bool {
	_, ok := semantic.SequenceMatchElementType(actual)
	return ok
}
func runtimeStringLiteralType() semantic.Type {
	return &semantic.RefType{Elem: &semantic.BuiltinType{Name: "u8"}, State: semantic.RefStateNonNull, Storage: semantic.RefStorageStatic, ExplicitStorage: true}
}
func isStringMatchableType(actual semantic.Type) bool {
	_, _, _, _, ok := runtimeStringCompareInfo(actual, runtimeStringLiteralType())
	return ok
}
func resolveMatchableErrorSetTypeBackend(actual semantic.Type) (*semantic.ErrorSetType, bool) {
	errorSetType, ok := semantic.StripAggregateStateType(actual).(*semantic.ErrorSetType)
	return errorSetType, ok && errorSetType != nil
}

// isIntegerMatchableTypeBackend mirrors the analyzer's isIntegerMatchableType. Integer matches lower
// through the same generic scalar-literal drivers as string matches (emitStringMatch/Expr), differing
// only in the per-arm comparison emitted by emitStringMatchPatternTest.
func isIntegerMatchableTypeBackend(actual semantic.Type) bool {
	return actual != nil && semantic.IsIntegralType(semantic.StripAggregateStateType(actual))
}
func (s *functionState) emitMatch(stmt *ast.MatchStmt) error {
	enumType, ok := resolveMatchableEnumType(s.exprType(stmt.Value))
	if ok {
		return s.emitEnumMatch(stmt, enumType)
	}
	constEnumType, ok := resolveMatchableConstEnumType(s.exprType(stmt.Value))
	if ok {
		return s.emitConstEnumMatch(stmt, constEnumType)
	}
	errorSetType, ok := resolveMatchableErrorSetTypeBackend(s.exprType(stmt.Value))
	if ok {
		return s.emitErrorSetMatch(stmt, errorSetType)
	}
	if optionalType, ok := s.exprType(stmt.Value).(*semantic.OptionalType); ok {
		return s.emitOptionalMatch(stmt, optionalType)
	}
	if isIntegerMatchableTypeBackend(s.exprType(stmt.Value)) {
		return s.emitStringMatch(stmt)
	}
	if isStringMatchableType(s.exprType(stmt.Value)) {
		return s.emitStringMatch(stmt)
	}
	if resolveMatchableTupleTypeBackend(s.exprType(stmt.Value)) {
		return s.emitTupleMatch(stmt)
	}
	if resolveMatchableSequenceTypeBackend(s.exprType(stmt.Value)) {
		return s.emitSequenceMatch(stmt)
	}
	if resolveMatchableStructTypeBackend(s.exprType(stmt.Value)) {
		return s.emitStructMatch(stmt)
	}
	return fmt.Errorf("%s: match requires an enum, const enum, error set, optional, tree-category, string, tuple, sequence, or struct value", stmt.Pos())
}
func (s *functionState) emitExpectPatternStmt(stmt *ast.ExpectPatternStmt) error {
	if stmt == nil {
		return nil
	}
	actualType := s.exprType(stmt.Value)
	actualValue, _, err := s.emitExpr(stmt.Value, actualType)
	if err != nil {
		return err
	}
	if len(stmt.Patterns) == 1 {
		bindings := map[string]semantic.Type{}
		if err := s.collectMatchPatternBindings(stmt.Patterns[0], actualType, bindings); err != nil {
			return err
		}
		names := make([]string, 0, len(bindings))
		for name := range bindings {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			alloca, err := s.createEntryAlloca(name+".expect", bindings[name])
			if err != nil {
				return err
			}
			s.defineBinding(name, valueBinding{ptr: alloca, typ: bindings[name], mutable: false})
		}
	}
	successBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("expect.ok"))
	failureBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("expect.fail"))
	if len(stmt.Patterns) == 0 {
		llvmBuildBr(s.builder, successBB)
	} else {
		for i, pattern := range stmt.Patterns {
			nextFailure := failureBB
			if i != len(stmt.Patterns)-1 {
				nextFailure = llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("expect.next"))
			}
			if err := s.emitConditionPatternTestAndBind(pattern, actualValue, actualType, stmt.Value, successBB, nextFailure); err != nil {
				return err
			}
			if i != len(stmt.Patterns)-1 {
				llvmPositionBuilderAtEnd(s.builder, nextFailure)
			}
		}
	}
	llvmPositionBuilderAtEnd(s.builder, failureBB)
	if err := s.emitPanicWithBacktrace(stmt.Position, &ast.StringLit{Position: stmt.Position, Value: "expect pattern failed"}); err != nil {
		return err
	}
	llvmPositionBuilderAtEnd(s.builder, successBB)
	return nil
}
