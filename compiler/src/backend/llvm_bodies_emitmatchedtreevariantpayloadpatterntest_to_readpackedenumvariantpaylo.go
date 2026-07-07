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
	"fmt"
	"sort"
	"strings"
)

func packedEnumMatchCanUseTagSwitch(enumType *semantic.EnumType, arms []ast.MatchArm) bool {
	if enumType == nil || len(arms) == 0 {
		return false
	}
	// docs/122 §5.1: a failed guard falls through to the NEXT arm, which a
	// one-dispatch-per-tag switch cannot express — guarded matches take the
	// sequential path.
	if matchArmsHaveGuard(arms) {
		return false
	}
	seen := map[string]bool{}
	for i, arm := range arms {
		switch pattern := arm.Pattern.(type) {
		case *ast.MatchVariantPattern:
			if seen[pattern.Variant] {
				return false
			}
			if _, ok := enumType.Variant(pattern.Variant); !ok {
				return false
			}
			// docs/122 §5.4: as-bindings are emitted by emitMatchPatternTest on the
			// sequential path; the tag-switch path would silently drop them.
			if pattern.As != "" {
				return false
			}
			seen[pattern.Variant] = true
		case *ast.MatchWildcardPattern:
			if i != len(arms)-1 {
				return false
			}
		default:
			return false
		}
	}
	return len(seen) >= 3
}
func (s *functionState) emitStringMatchPatternTest(pattern ast.MatchPattern, actualExpr ast.Expr, actualType semantic.Type, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) error {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		C.LLVMBuildBr(s.builder, successBB)
		return nil
	case *ast.MatchStringLiteralPattern:
		literalExpr := &ast.StringLit{Position: p.Pos(), Value: p.Value}
		literalType := runtimeStringLiteralType()
		helperName, firstType, secondType, swap, ok := runtimeStringCompareInfo(actualType, literalType)
		if !ok {
			return fmt.Errorf("string match pattern requires a string value, got %s", actualType.String())
		}
		synthetic := &ast.BinaryExpr{Position: p.Pos(), Op: lexer.TOKEN_EQEQ, Left: actualExpr, Right: literalExpr}
		cmp, _, err := s.emitRuntimeStringCompareExpr(synthetic, helperName, firstType, secondType, swap)
		if err != nil {
			return err
		}
		C.LLVMBuildCondBr(s.builder, cmp, successBB, failureBB)
		return nil
	case *ast.MatchLiteralPattern:
		// Integer match arm (`0xA9:`). emitComparableIsTargetTest unifies the scrutinee and literal
		// operand types (so a bare literal lowers at the scrutinee's width) and emits an ICmp EQ.
		cmp, _, err := s.emitComparableIsTargetTest(actualExpr, p.Value)
		if err != nil {
			return err
		}
		C.LLVMBuildCondBr(s.builder, cmp, successBB, failureBB)
		return nil
	case *ast.MatchRangePattern:
		// docs/122 §5.2 range arm over an integer/char scrutinee: two chained comparisons.
		actualValue, _, err := s.emitExpr(actualExpr, actualType)
		if err != nil {
			return err
		}
		return s.emitRangeMatchPatternTest(p, actualValue, actualType, successBB, failureBB)
	default:
		return fmt.Errorf("unsupported scalar match pattern %T", pattern)
	}
}
func (s *functionState) resolveStructMatchPatternArgs(pattern *ast.MatchStructPattern, actualType semantic.Type) ([]structLiteralField, []*ast.MatchPatternArg, error) {
	if pattern == nil {
		return nil, nil, fmt.Errorf("missing struct match pattern")
	}
	fields, err := s.g.structLiteralFields(actualType)
	if err != nil {
		return nil, nil, err
	}
	switch tt := semantic.StripAggregateStateType(actualType).(type) {
	case *semantic.StructType:
		if tt == nil || (pattern.TypeName != "" && tt.Name != pattern.TypeName) || tt.Decl == nil {
			got := "<invalid>"
			if tt != nil {
				got = tt.Name
			}
			return nil, nil, fmt.Errorf("struct pattern expects struct %s, got %s", pattern.TypeName, got)
		}
	case *semantic.GenericInstanceType:
		base, _ := tt.Base.(*semantic.StructType)
		if base == nil || (pattern.TypeName != "" && base.Name != pattern.TypeName) || base.Decl == nil {
			got := semantic.StripAggregateStateType(actualType).String()
			if base != nil {
				got = base.Name
			}
			return nil, nil, fmt.Errorf("struct pattern expects struct %s, got %s", pattern.TypeName, got)
		}
	default:
		return nil, nil, fmt.Errorf("struct pattern %s requires a concrete struct value, got %s", pattern.TypeName, semantic.StripAggregateStateType(actualType).String())
	}
	if len(pattern.ResolvedArgs) == len(fields) {
		return fields, pattern.ResolvedArgs, nil
	}
	ordered := make([]*ast.MatchPatternArg, len(fields))
	fieldIndexes := make(map[string]int, len(fields))
	for i := range fields {
		fieldIndexes[fields[i].Decl.Name] = i
	}
	seen := make([]bool, len(fields))
	for i := range pattern.Args {
		arg := &pattern.Args[i]
		index, ok := fieldIndexes[arg.Name]
		if !ok {
			return nil, nil, fmt.Errorf("struct %s has no field %s", pattern.TypeName, arg.Name)
		}
		if seen[index] {
			return nil, nil, fmt.Errorf("struct %s matches field %s more than once", pattern.TypeName, arg.Name)
		}
		seen[index] = true
		ordered[index] = arg
	}
	pattern.ResolvedArgs = ordered
	return fields, ordered, nil
}
func (s *functionState) resolveMatchPatternArgs(pattern *ast.MatchVariantPattern, variant *semantic.EnumVariant) ([]*ast.MatchPatternArg, error) {
	if len(pattern.ResolvedArgs) == len(variant.Payload) {
		return pattern.ResolvedArgs, nil
	}
	ordered := make([]*ast.MatchPatternArg, len(variant.Payload))
	if len(pattern.Args) == 0 {
		pattern.ResolvedArgs = ordered
		return ordered, nil
	}
	namedCount := 0
	for i := range pattern.Args {
		if pattern.Args[i].Name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		if len(pattern.Args) != len(variant.Payload) {
			return nil, fmt.Errorf("match arm %s.%s expects %d payload patterns, got %d", pattern.EnumName, pattern.Variant, len(variant.Payload), len(pattern.Args))
		}
		for i := range pattern.Args {
			ordered[i] = &pattern.Args[i]
		}
		pattern.ResolvedArgs = ordered
		return ordered, nil
	}
	if namedCount != len(pattern.Args) {
		return nil, fmt.Errorf("match arm %s.%s cannot mix positional and named payload patterns", pattern.EnumName, pattern.Variant)
	}
	if !variant.HasNamedPayloads() {
		return nil, fmt.Errorf("match arm %s.%s uses named payload patterns but the variant payloads are unnamed", pattern.EnumName, pattern.Variant)
	}
	seen := make([]bool, len(variant.Payload))
	for i := range pattern.Args {
		arg := &pattern.Args[i]
		index, ok := variant.PayloadIndex(arg.Name)
		if !ok {
			return nil, fmt.Errorf("match arm %s.%s has no payload field %q", pattern.EnumName, pattern.Variant, arg.Name)
		}
		if seen[index] {
			return nil, fmt.Errorf("match arm %s.%s matches payload field %q more than once", pattern.EnumName, pattern.Variant, arg.Name)
		}
		seen[index] = true
		ordered[index] = arg
	}
	missing := make([]string, 0)
	for i := range ordered {
		if ordered[i] == nil {
			missing = append(missing, variant.PayloadLabel(i))
		}
	}
	// docs/122 §5.7: an explicit final `_` (pattern.Rest) matches the named fields and
	// ignores the remainder — unmatched positions simply aren't destructured.
	if len(missing) > 0 && !pattern.Rest {
		sort.Strings(missing)
		return nil, fmt.Errorf("match arm %s.%s is missing named payload patterns for: %s", pattern.EnumName, pattern.Variant, strings.Join(missing, ", "))
	}
	pattern.ResolvedArgs = ordered
	return ordered, nil
}
func (s *functionState) resolveTupleMatchPatternElems(pattern *ast.MatchTuplePattern, actualType semantic.Type) ([]structLiteralField, error) {
	if pattern == nil {
		return nil, fmt.Errorf("missing tuple match pattern")
	}
	tupleType, ok := semantic.StripAggregateStateType(actualType).(*semantic.TupleType)
	if !ok || tupleType == nil {
		return nil, fmt.Errorf("tuple pattern requires a tuple value, got %s", semantic.StripAggregateStateType(actualType).String())
	}
	fields, err := s.g.structLiteralFields(actualType)
	if err != nil {
		return nil, err
	}
	if len(pattern.Elems) != len(fields) {
		return nil, fmt.Errorf("tuple pattern expects %d elements, got %d", len(fields), len(pattern.Elems))
	}
	return fields, nil
}
func (s *functionState) extractEnumTagValue(enumValue C.LLVMValueRef, decodedEnumValue C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding) (C.LLVMValueRef, error) {
	if enumType != nil && enumType.Packed {
		return s.loadEnumTag(decodedEnumValue, enumValue, enumType, store)
	}
	if enumIsTagOnly(enumType) {
		return enumValue, nil
	}
	return C.LLVMBuildExtractValue(s.builder, enumValue, 0, cStringFree("match.tag.value")), nil
}
func (s *functionState) extractEnumVariantPayloadValues(enumValue C.LLVMValueRef, decodedEnumValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, store *packedStoreBinding, originExpr ast.Expr) ([]C.LLVMValueRef, error) {
	if variant == nil || len(variant.Payload) == 0 {
		return nil, nil
	}
	origin := packedReadOriginKey{}
	if originExpr != nil {
		resolvedOrigin, ok, err := s.packedReadOriginKey(originExpr)
		if err != nil {
			return nil, err
		}
		if ok {
			origin = resolvedOrigin
		}
	}
	if enumType != nil && enumType.Packed {
		return s.loadEnumVariantPayload(decodedEnumValue, enumValue, enumType, variant, store, origin)
	}
	enumPtr, err := s.emitStackTempValue(enumValue, enumType, "match.payload.tmp")
	if err != nil {
		return nil, err
	}
	return s.loadEnumVariantPayload(nil, enumPtr, enumType, variant, store, packedReadOriginKey{})
}
func (s *functionState) matchIsExhaustive(enumType *semantic.EnumType, arms []ast.MatchArm) bool {
	if enumType == nil {
		return false
	}
	covered := map[string]bool{}          // bare variant names (flat enum)
	coveredQualified := map[string]bool{} // Owner.Variant (hierarchy)
	for _, arm := range arms {
		if arm.Guard != nil {
			// docs/122 §5.1: a guarded arm can fail at runtime, so it never covers.
			// (Semantic already rejects matches relying on guarded coverage, but under
			// -permissive codegen continues — an `unreachable` here would miscompile.)
			continue
		}
		switch pattern := arm.Pattern.(type) {
		case *ast.MatchWildcardPattern:
			return true
		case *ast.MatchVariantPattern:
			covered[pattern.Variant] = true
			coveredQualified[pattern.EnumName+"."+pattern.Variant] = true
		case *ast.MatchBindPattern:
			// docs/77 §2 category arm: covers its category's whole leaf range. A plain
			// (non-category) bind arm catches everything, like a wildcard.
			category, ok := s.enumCategoryArm(enumType, pattern.Name)
			if !ok {
				return true
			}
			var mark func(e *semantic.EnumType)
			mark = func(e *semantic.EnumType) {
				for _, variant := range e.Variants {
					covered[variant.Name] = true
					coveredQualified[e.Name+"."+variant.Name] = true
				}
				for _, child := range e.Children {
					mark(child)
				}
			}
			mark(category)
		}
	}
	// docs/77: a hierarchy match is exhaustive when every leaf across the SCRUTINEE's refinement
	// subtree is covered (qualified Owner.Variant key). The frontier is the scrutinee's static
	// type, not the hierarchy root — matching the analyzer's exhaustiveness scope (a match over
	// `Expr` must cover Expr's leaves, not its sibling categories').
	if enumType.Parent != nil || len(enumType.Children) > 0 {
		total := 0
		allCovered := true
		var visit func(e *semantic.EnumType)
		visit = func(e *semantic.EnumType) {
			for _, variant := range e.Variants {
				total++
				if !coveredQualified[e.Name+"."+variant.Name] {
					allCovered = false
				}
			}
			for _, child := range e.Children {
				visit(child)
			}
		}
		visit(enumType)
		return allCovered && total > 0
	}
	return len(covered) == len(enumType.Variants)
}
func constEnumMatchIsExhaustive(constEnumType *semantic.ConstEnumType, arms []ast.MatchArm) bool {
	if constEnumType == nil {
		return false
	}
	covered := map[string]bool{}
	for _, arm := range arms {
		if arm.Guard != nil {
			// A guarded arm may fail at runtime; it cannot count toward exhaustiveness.
			continue
		}
		switch pattern := arm.Pattern.(type) {
		case *ast.MatchWildcardPattern:
			return true
		case *ast.MatchVariantPattern:
			covered[pattern.Variant] = true
		}
	}
	return len(covered) == len(constEnumType.Members)
}
func (s *functionState) loadEnumTag(decodedEnumPtr C.LLVMValueRef, enumPtr C.LLVMValueRef, enumType *semantic.EnumType, store *packedStoreBinding) (C.LLVMValueRef, error) {
	if enumType != nil && !enumType.Packed && enumIsTagOnly(enumType) {
		tagType, err := s.g.lowerEnumTagType(enumType)
		if err != nil {
			return nil, err
		}
		return C.LLVMBuildLoad2(s.builder, tagType, enumPtr, cStringFree("match.tag.value")), nil
	}
	if enumType != nil && enumType.Packed {
		if decodedEnumPtr != nil {
			enumPtr = decodedEnumPtr
		} else {
			if ops, ok := s.packedStoreOpsFromBinding(store); ok && ops.canDirectTagRead() {
				return ops.storeTagAt(enumPtr, enumType, "packed.tag.store")
			}
			var err error
			enumPtr, err = s.decodePackedEnumHandleWithStore(enumPtr, enumType, store)
			if err != nil {
				return nil, err
			}
		}
	}
	enumLLVMType, err := s.loweredEnumStorageType(enumType)
	if err != nil {
		return nil, err
	}
	tagPtr := C.LLVMBuildStructGEP2(s.builder, enumLLVMType, enumPtr, 0, cStringFree("match.tag.ptr"))
	tagType, err := s.g.lowerEnumTagType(enumType)
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildLoad2(s.builder, tagType, tagPtr, cStringFree("match.tag.value")), nil
}
func (s *functionState) readInlineWordHandlePayload(handleValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant) ([]C.LLVMValueRef, bool, error) {
	if !s.canInlinePackedEnumVariant(enumType, variant) || len(variant.Payload) != 1 {
		return nil, false, nil
	}
	uintptrLLVMType, err := s.g.lowerBuiltin("uintptr")
	if err != nil {
		return nil, false, err
	}
	payloadLLVMType, err := s.g.lowerType(variant.Payload[0])
	if err != nil {
		return nil, false, err
	}
	shifted := C.LLVMBuildLShr(s.builder, handleValue, C.LLVMConstInt(uintptrLLVMType, 1, 0), cStringFree("packed.inline.payload.bits"))
	masked := C.LLVMBuildAnd(s.builder, shifted, C.LLVMConstInt(uintptrLLVMType, C.ulonglong(0x0000ffffffffffff), 0), cStringFree("packed.inline.payload.mask"))
	value := C.LLVMBuildTrunc(s.builder, masked, payloadLLVMType, cStringFree("packed.inline.payload.value"))
	return []C.LLVMValueRef{value}, true, nil
}
func (s *functionState) loadEnumVariantPayload(decodedEnumPtr C.LLVMValueRef, enumPtr C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, store *packedStoreBinding, origin packedReadOriginKey) ([]C.LLVMValueRef, error) {
	if variant == nil || len(variant.Payload) == 0 {
		return nil, nil
	}
	if enumType != nil && enumType.Packed {
		if decodedEnumPtr != nil {
			enumPtr = decodedEnumPtr
		} else {
			values, ok, inlineErr := s.readInlineWordHandlePayload(enumPtr, enumType, variant)
			if inlineErr != nil {
				return nil, inlineErr
			}
			if ok {
				return values, nil
			}
			values, ok, readErr := s.readPackedEnumVariantPayloadWithStore(enumPtr, enumType, variant, store, origin)
			if readErr != nil {
				return nil, readErr
			}
			if ok {
				return values, nil
			}
			var decodeErr error
			enumPtr, decodeErr = s.decodePackedEnumHandleWithStore(enumPtr, enumType, store)
			if decodeErr != nil {
				return nil, decodeErr
			}
		}
	}
	payloadPtr, err := s.enumPayloadPtr(enumPtr, enumType)
	if err != nil {
		return nil, err
	}
	payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
	if err != nil {
		return nil, err
	}
	// docs/76 free-null niche: record slots for optional enum children hold the bare handle;
	// rebuild the generic carrier as each field value crosses the record boundary.
	unpackNiche := func(value C.LLVMValueRef, fieldType semantic.Type) (C.LLVMValueRef, error) {
		nicheEnum, ok := s.g.optionalEnumNicheField(fieldType)
		if !ok {
			return value, nil
		}
		return s.unpackOptionalEnumNicheValue(value, fieldType.(*semantic.OptionalType), nicheEnum, "match.payload")
	}
	if len(variant.Payload) == 1 {
		value := C.LLVMBuildLoad2(s.builder, payloadType, payloadPtr, cStringFree("match.payload"))
		value, err := unpackNiche(value, variant.Payload[0])
		if err != nil {
			return nil, err
		}
		return []C.LLVMValueRef{value}, nil
	}
	aggregate := C.LLVMBuildLoad2(s.builder, payloadType, payloadPtr, cStringFree("match.payload"))
	values := make([]C.LLVMValueRef, 0, len(variant.Payload))
	for i, fieldType := range variant.Payload {
		value := C.LLVMBuildExtractValue(s.builder, aggregate, C.unsigned(i), cStringFree("match.payload.field"))
		value, err := unpackNiche(value, fieldType)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}
func (s *functionState) readPackedEnumVariantPayloadWithStore(handleValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, store *packedStoreBinding, origin packedReadOriginKey) ([]C.LLVMValueRef, bool, error) {
	if enumType == nil || !enumType.Packed || variant == nil || len(variant.Payload) == 0 {
		return nil, false, nil
	}
	if values, ok, err := s.readInlineWordHandlePayload(handleValue, enumType, variant); err != nil {
		return nil, false, err
	} else if ok {
		return values, true, nil
	}
	ops, ok := s.packedStoreOpsFromBinding(store)
	if !ok {
		return nil, false, nil
	}
	var payloadCacheKey packedVariantPayloadReadCacheKey
	cachePayloadValues := false
	if ops.canCacheDirectReadValues(enumType) {
		if s.packedVariantPayloadReads == nil {
			s.packedVariantPayloadReads = map[packedVariantPayloadReadCacheKey][]C.LLVMValueRef{}
		}
		originKey, cacheHandle := ops.directReadCacheIdentity(enumType, origin, handleValue)
		payloadCacheKey = packedVariantPayloadReadCacheKey{
			block:    ops.currentBlock(),
			store:    ops.storeValue,
			enumType: enumType,
			variant:  variant,
			origin:   originKey,
			handle:   cacheHandle,
		}
		if cached, ok := s.packedVariantPayloadReads[payloadCacheKey]; ok && len(cached) == len(variant.Payload) {
			return cached, true, nil
		}
		cachePayloadValues = true
	}
	tailIndex, hasTail := variant.TailPayloadIndex()
	var tailValue C.LLVMValueRef
	if hasTail {
		var err error
		tailValue, ok, err = ops.loadTailView(handleValue, enumType, variant, tailIndex, "packed.payload.tail")
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
	} else if !ops.canDirectWordRead() {
		return nil, false, nil
	}
	values := make([]C.LLVMValueRef, 0, len(variant.Payload))
	for payloadIndex, payloadType := range variant.Payload {
		if hasTail && payloadIndex == tailIndex {
			values = append(values, tailValue)
			continue
		}
		fieldOffsetBytes, ok, err := s.packedEnumVariantPayloadFieldByteOffset(enumType, variant, payloadIndex)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		if nicheEnum, isNiche := s.g.optionalEnumNicheField(payloadType); isNiche {
			// docs/76 free-null niche: the record slot is the bare handle; rebuild the
			// generic {present, handle} carrier at the boundary.
			raw, err := s.emitPackedDirectFieldReadAtOrigin(ops, handleValue, enumType, nicheEnum, fieldOffsetBytes, origin, "packed.payload.niche")
			if err != nil {
				return nil, false, err
			}
			carrier, err := s.unpackOptionalEnumNicheValue(raw, payloadType.(*semantic.OptionalType), nicheEnum, "packed.payload")
			if err != nil {
				return nil, false, err
			}
			values = append(values, carrier)
			continue
		}
		coerced, err := s.emitPackedDirectFieldReadAtOrigin(ops, handleValue, enumType, payloadType, fieldOffsetBytes, origin, "packed.payload")
		if err != nil {
			return nil, false, err
		}
		values = append(values, coerced)
	}
	if cachePayloadValues && len(values) == len(variant.Payload) {
		s.packedVariantPayloadReads[payloadCacheKey] = values
	}
	return values, true, nil
}
