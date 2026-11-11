//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"maps"
	"slices"
)

func (s *functionState) emitMatchPatternTest(pattern ast.MatchPattern, actualValue llvmIRValueRef, decodedActualValue llvmIRValueRef, actualType semantic.Type, store *packedStoreBinding, originExpr ast.Expr, precomputedTagValue llvmIRValueRef, successBB llvmIRBlockRef, failureBB llvmIRBlockRef) (llvmIRValueRef, packedPayloadValueCache, error) {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		llvmBuildBr(s.builder, successBB)
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
					if err := s.storeValue(alloca, actualValue, category, p.Binder); err != nil {
						return nil, packedPayloadValueCache{}, err
					}
					s.defineBinding(p.Binder, valueBinding{ptr: alloca, typ: category})
					if enumType.Packed {
						s.bindPackedEnumStoreOrigin(p.Binder, enumType, store)
					}
				}
				if semantic.EnumDescendsFrom(enumType, category) {
					llvmBuildBr(s.builder, successBB) // covers the whole scrutinee
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
				llvmBuildCondBr(s.builder, cond, successBB, failureBB)
				return decodedActualValue, packedPayloadValueCache{}, nil
			}
		}
		if handled, err := s.emitPredicateMatchPatternTest(p.Name, actualValue, actualType, successBB, failureBB); handled || err != nil {
			return decodedActualValue, packedPayloadValueCache{}, err
		}
		if p.Name != "" && p.Name != "_" {
			if existing, ok := s.lookupBinding(p.Name); ok && existing.ptr != nil && semantic.SameType(existing.typ, actualType) {
				if err := s.storeValue(existing.ptr, actualValue, actualType, p.Name); err != nil {
					return nil, packedPayloadValueCache{}, err
				}
			} else {
				alloca, err := s.createEntryAlloca(p.Name, actualType)
				if err != nil {
					return nil, packedPayloadValueCache{}, err
				}
				if err := s.storeValue(alloca, actualValue, actualType, p.Name); err != nil {
					return nil, packedPayloadValueCache{}, err
				}
				s.defineBinding(p.Name, valueBinding{ptr: alloca, typ: actualType})
			}
		}
		if enumType, ok := actualType.(*semantic.EnumType); ok && enumType.Packed && decodedActualValue != nil {
			s.bindPackedEnumStorage(p.Name, enumType, decodedActualValue)
		}
		if enumType, ok := actualType.(*semantic.EnumType); ok && enumType.Packed {
			s.bindPackedEnumStoreOrigin(p.Name, enumType, store)
		}
		llvmBuildBr(s.builder, successBB)
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchLiteralPattern:
		if err := s.emitLiteralMatchPatternTest(p.Value, actualValue, actualType, successBB, failureBB); err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchRangePattern:
		if err := s.emitRangeMatchPatternTest(p, actualValue, actualType, successBB, failureBB); err != nil {
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
			llvmBuildBr(s.builder, failureBB)
			return decodedActualValue, packedPayloadValueCache{}, nil
		}
		bindings, err := s.collectOrMatchPatternBindings(p, actualType)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		// Materialize the scrutinee copy HERE, before any option block exists, so the
		// store that initializes it dominates every option. nonPackedEnumMatchTemps
		// caches the temp POINTER but not its initializing store, so without this the
		// FIRST option's lazy spill lands inside that option's own block and every
		// later option reads an uninitialized slot -- binding garbage instead of the
		// payload. The top-level match pre-seeds for exactly this reason; a nested
		// or-pattern (`Expr.Leaf(Token.Ident(v) | Token.Keyword(v))`) is the case that
		// had no pre-seeding of its own.
		if orEnumType, ok := actualType.(*semantic.EnumType); ok && !orEnumType.Packed {
			if _, err := s.prepareNonPackedEnumMatchTemp(actualValue, orEnumType); err != nil {
				return nil, packedPayloadValueCache{}, err
			}
		}
		for _, name := range slices.Sorted(maps.Keys(bindings)) { // map: sort so the allocas land in a stable order
			typ := bindings[name]
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
				nextFailure = llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.or.next"))
			}
			if _, _, err := s.emitMatchPatternTest(option, actualValue, decodedActualValue, actualType, store, originExpr, precomputedTagValue, successBB, nextFailure); err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			if i != len(p.Options)-1 {
				llvmPositionBuilderAtEnd(s.builder, nextFailure)
			}
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchTuplePattern:
		fields, err := s.resolveTupleMatchPatternElems(p, actualType)
		if err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		if len(p.Elems) == 0 {
			llvmBuildBr(s.builder, successBB)
			return decodedActualValue, packedPayloadValueCache{}, nil
		}
		for i, elem := range p.Elems {
			fieldValue := llvmBuildExtractValue(s.builder, actualValue, llvmIRUnsigned(fields[i].Index), cStringFree("match.tuple.field"))
			var fieldOrigin ast.Expr
			if originExpr != nil {
				fieldOrigin = &ast.FieldExpr{Position: elem.Pos(), Object: originExpr, Field: fields[i].Decl.Name}
			}
			nextSuccess := successBB
			if i != len(p.Elems)-1 {
				nextSuccess = llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.tuple.pattern.next"))
			}
			if _, _, err := s.emitMatchPatternTest(elem, fieldValue, nil, fields[i].Type, nil, fieldOrigin, nil, nextSuccess, failureBB); err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			if i != len(p.Elems)-1 {
				llvmPositionBuilderAtEnd(s.builder, nextSuccess)
			}
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchListPattern:
		if err := s.emitListMatchPatternTest(p, actualValue, actualType, originExpr, successBB, failureBB); err != nil {
			return nil, packedPayloadValueCache{}, err
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchRestPattern:
		llvmBuildBr(s.builder, successBB)
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchStructPattern:
		if handled, err := s.emitCountMatchPatternTest(p, actualValue, actualType, originExpr, successBB, failureBB); handled || err != nil {
			return decodedActualValue, packedPayloadValueCache{}, err
		}
		// docs/122 §5.4: bind the whole matched struct value alongside the destructure.
		if err := s.emitPatternAsBinding(p.As, actualValue, actualType, originExpr); err != nil {
			return nil, packedPayloadValueCache{}, err
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
			llvmBuildBr(s.builder, successBB)
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
				nextSuccess = llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.struct.pattern.next"))
			}
			if _, _, err := s.emitMatchPatternTest(arg.Pattern, fieldValue, nil, fields[fieldIndex].Type, nil, fieldOrigin, nil, nextSuccess, failureBB); err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			if i != len(matchedIndexes)-1 {
				llvmPositionBuilderAtEnd(s.builder, nextSuccess)
			}
		}
		return decodedActualValue, packedPayloadValueCache{}, nil
	case *ast.MatchVariantPattern:
		// A scrutinee narrowed to one variant's packed view (inside a packed enum's match
		// arm) is still a value of that enum: `if e is E.Other(x, _)` tests the handle's
		// tag like any enum value, so lower the view back to its enum handle first.
		if viewType, ok := actualType.(*semantic.PackedVariantViewType); ok && viewType != nil && viewType.Enum != nil {
			// The view carries its own store; prefer it over any enclosing lookup so the tag
			// and payload decode against the store the handle belongs to.
			view, err := s.unpackPackedVariantViewValue(actualValue, viewType)
			if err != nil {
				return nil, packedPayloadValueCache{}, err
			}
			if view.store.value != nil {
				viewStore := view.store
				store = &viewStore
			}
			actualValue, decodedActualValue, actualType = view.handle, nil, viewType.Enum
			precomputedTagValue = nil
		}
		// docs/122 §5.4: bind the whole matched value alongside the destructure.
		if err := s.emitPatternAsBinding(p.As, actualValue, actualType, originExpr); err != nil {
			return nil, packedPayloadValueCache{}, err
		}
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
			pred := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), errorCodeValue, memberValue, cStringFree("match.tag"))
			if len(p.Args) == 0 {
				llvmBuildCondBr(s.builder, pred, successBB, failureBB)
				return decodedActualValue, packedPayloadValueCache{}, nil
			}
			payloadBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.error.payload"))
			llvmBuildCondBr(s.builder, pred, payloadBB, failureBB)
			llvmPositionBuilderAtEnd(s.builder, payloadBB)
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
				llvmBuildBr(s.builder, successBB)
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
					nextSuccess = llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.error.pattern.next"))
				}
				if _, _, err := s.emitMatchPatternTest(arg.Pattern, payloadValues[i], nil, payloadTypes[i], store, nil, nil, nextSuccess, failureBB); err != nil {
					return nil, packedPayloadValueCache{}, err
				}
				if i != lastNestedPattern {
					llvmPositionBuilderAtEnd(s.builder, nextSuccess)
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
			pred := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), actualValue, memberValue, cStringFree("match.tag"))
			llvmBuildCondBr(s.builder, pred, successBB, failureBB)
			return decodedActualValue, packedPayloadValueCache{}, nil
		}
		enumType, ok := actualType.(*semantic.EnumType)
		if !ok {
			return nil, packedPayloadValueCache{}, fmt.Errorf("%s: variant pattern %s.%s requires enum, const enum, or error set type, got %s", p.Pos(), p.EnumName, p.Variant, actualType.String())
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
		pred := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), tagValue, tagConst, cStringFree("match.tag"))
		matchedBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.pattern.ok"))
		llvmBuildCondBr(s.builder, pred, matchedBB, failureBB)

		llvmPositionBuilderAtEnd(s.builder, matchedBB)
		return s.emitMatchedVariantPayloadPatternTest(p, actualValue, decodedActualValue, enumType, variant, store, originExpr, successBB, failureBB)
	default:
		return nil, packedPayloadValueCache{}, fmt.Errorf("unsupported match pattern %T", pattern)
	}
}
