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
)

func (s *functionState) conditionTargetPattern(expr ast.Expr) (ast.MatchPattern, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.conditionTargetPattern(n.Inner)
	case *ast.StructTestExpr:
		if n != nil && n.Pattern != nil {
			return n.Pattern, true
		}
	case *ast.VariantTestExpr:
		if n != nil && n.Pattern != nil {
			return n.Pattern, true
		}
	}
	return nil, false
}
func (s *functionState) directConditionPattern(expr ast.Expr) (ast.Expr, ast.MatchPattern, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.directConditionPattern(n.Inner)
	case *ast.BinaryExpr:
		if n.Op != lexer.TOKEN_IS {
			return nil, nil, false
		}
		pattern, ok := s.conditionTargetPattern(n.Right)
		if !ok || pattern == nil {
			return nil, nil, false
		}
		return n.Left, pattern, true
	default:
		return nil, nil, false
	}
}
func (s *functionState) optionalBindSourceType(expr *ast.OptionalBindExpr) semantic.Type {
	if expr == nil {
		return nil
	}
	if s.g != nil && s.g.result != nil && s.g.result.OptionalBindSourceTypes != nil {
		if valueType, ok := s.g.result.OptionalBindSourceTypes[expr]; ok && valueType != nil {
			return valueType
		}
	}
	return s.exprType(expr.Value)
}
func (s *functionState) emitOptionalBindSourceValue(expr ast.Expr, sourceType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || sourceType == nil {
		return nil, nil, fmt.Errorf("invalid let condition source")
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.emitOptionalBindSourceValue(n.Inner, sourceType)
	}
	if ptr, storedType, err := s.emitAddress(expr); err == nil && storedType != nil && semantic.SameType(storedType, sourceType) {
		value, loadErr := s.loadValue(ptr, sourceType, "cond.let.source")
		return value, sourceType, loadErr
	}
	return s.emitExpr(expr, sourceType)
}
func (s *functionState) emitOptionalBindTest(expr *ast.OptionalBindExpr) (C.LLVMValueRef, C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Value == nil {
		return nil, nil, nil, fmt.Errorf("invalid let condition")
	}
	valueType := s.optionalBindSourceType(expr)
	if optionalType, ok := valueType.(*semantic.OptionalType); ok {
		fallibleValue, _, err := s.emitOptionalBindSourceValue(expr.Value, valueType)
		if err != nil {
			return nil, nil, nil, err
		}
		presentValue, err := s.extractOptionalPresent(fallibleValue, optionalType)
		if err != nil {
			return nil, nil, nil, err
		}
		payloadValue, err := s.extractOptionalPayload(fallibleValue, optionalType)
		if err != nil {
			return nil, nil, nil, err
		}
		return presentValue, payloadValue, optionalType.Value, nil
	}
	if refType, ok := valueType.(*semantic.RefType); ok && refType.State == semantic.RefStateNullable {
		refValue, _, err := s.emitExpr(expr.Value, valueType)
		if err != nil {
			return nil, nil, nil, err
		}
		nullValue := C.LLVMConstNull(C.LLVMTypeOf(refValue))
		presentValue := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), refValue, nullValue, cStringFree("cond.let.present"))
		boundType, _ := backendConditionOptionalBindType(valueType)
		return presentValue, refValue, boundType, nil
	}
	return nil, nil, nil, fmt.Errorf("let condition requires an optional or nullable reference")
}
func (s *functionState) emitOptionalBindExpr(expr *ast.OptionalBindExpr) (C.LLVMValueRef, semantic.Type, error) {
	presentValue, _, _, err := s.emitOptionalBindTest(expr)
	if err != nil {
		return nil, nil, err
	}
	return presentValue, s.g.result.NamedTypes["bool"], nil
}
func (s *functionState) emitOptionalBindCondition(expr *ast.OptionalBindExpr, trueBB C.LLVMBasicBlockRef, falseBB C.LLVMBasicBlockRef, hint ast.BranchHint) error {
	presentValue, boundValue, _, err := s.emitOptionalBindTest(expr)
	if err != nil {
		return err
	}
	if expr.Name == "" || expr.Name == "_" {
		s.buildCondBrWithHint(presentValue, trueBB, falseBB, hint)
		return nil
	}
	bindBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cond.let.bind"))
	s.buildCondBrWithHint(presentValue, bindBB, falseBB, hint)
	C.LLVMPositionBuilderAtEnd(s.builder, bindBB)
	if binding, ok := s.lookupBinding(expr.Name); ok && binding.ptr != nil {
		C.LLVMBuildStore(s.builder, boundValue, binding.ptr)
	}
	C.LLVMBuildBr(s.builder, trueBB)
	return nil
}
func stripOptionalAssignTargetExpr(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok || paren == nil {
			return expr
		}
		expr = paren.Inner
	}
}
func (s *functionState) emitOptionalAssignThroughNullableRef(refValue C.LLVMValueRef, elemType semantic.Type, valueExpr ast.Expr, name string) error {
	if refValue == nil || elemType == nil {
		return fmt.Errorf("invalid ?= target")
	}
	nullValue := C.LLVMConstNull(C.LLVMTypeOf(refValue))
	presentValue := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), refValue, nullValue, cStringFree(name+".present"))
	assignBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".assign"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".merge"))
	C.LLVMBuildCondBr(s.builder, presentValue, assignBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, assignBB)
	value, _, err := s.emitExpr(valueExpr, elemType)
	if err != nil {
		return err
	}
	C.LLVMBuildStore(s.builder, value, refValue)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	return nil
}
func (s *functionState) emitOptionalAssignStmt(stmt *ast.AssignStmt) error {
	if stmt == nil || stmt.Target == nil || stmt.Value == nil {
		return fmt.Errorf("invalid ?= statement")
	}
	s.invalidatePackedEnumStorageExpr(stmt.Target)
	s.invalidatePackedEnumStoreOriginExpr(stmt.Target)
	s.invalidatePackedCommonFieldValuesExpr(stmt.Target)
	s.invalidatePackedVariantViewExpr(stmt.Target)
	s.invalidatePackedReadCaches()
	switch target := stripOptionalAssignTargetExpr(stmt.Target).(type) {
	case *ast.Ident:
		targetType := s.exprType(target)
		refType, ok := targetType.(*semantic.RefType)
		if !ok || refType == nil || refType.State != semantic.RefStateNullable {
			return fmt.Errorf("?= requires a nullable reference target")
		}
		refValue, _, err := s.emitExpr(target, targetType)
		if err != nil {
			return err
		}
		return s.emitOptionalAssignThroughNullableRef(refValue, refType.Elem, stmt.Value, "opt.assign.ident")
	case *ast.FieldExpr:
		if !target.Safe {
			return fmt.Errorf("?= requires optional chaining on field targets")
		}
		presentValue, receiverValue, receiverType, err := s.emitSafeChainReceiverValue(target.Object)
		if err != nil {
			return err
		}
		assignBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("opt.assign.field.assign"))
		mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("opt.assign.field.merge"))
		C.LLVMBuildCondBr(s.builder, presentValue, assignBB, mergeBB)

		C.LLVMPositionBuilderAtEnd(s.builder, assignBB)
		fieldPtr, fieldType, err := s.emitFieldAddressFromObjectValue(receiverValue, receiverType, target.Field, "opt.assign.field")
		if err != nil {
			return err
		}
		value, _, err := s.emitExpr(stmt.Value, fieldType)
		if err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, value, fieldPtr)
		C.LLVMBuildBr(s.builder, mergeBB)

		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
		return nil
	default:
		return fmt.Errorf("invalid ?= target")
	}
}
func (s *functionState) collectTruthyConditionBindings(expr ast.Expr) ([]conditionBindingInfo, error) {
	var collectPattern func(pattern ast.MatchPattern, expected semantic.Type, out map[string]semantic.Type) error
	collectPattern = func(pattern ast.MatchPattern, expected semantic.Type, out map[string]semantic.Type) error {
		if pattern == nil || expected == nil || out == nil {
			return nil
		}
		switch p := pattern.(type) {
		case *ast.MatchBindPattern:
			if p.Name == "" || p.Name == "_" {
				return nil
			}
			if s.predicateMatchPatternFuncType(p.Name) != nil {
				return nil
			}
			if prev, ok := out[p.Name]; ok && !semantic.SameType(prev, expected) {
				return fmt.Errorf("condition binding %q has inconsistent types %s and %s", p.Name, prev.String(), expected.String())
			}
			out[p.Name] = expected
			return nil
		case *ast.MatchListPattern:
			elemType, ok := semantic.SequenceMatchElementType(expected)
			if !ok {
				return nil
			}
			for _, elem := range p.Elems {
				if _, ok := elem.(*ast.MatchRestPattern); ok {
					continue
				}
				if err := collectPattern(elem, elemType, out); err != nil {
					return err
				}
			}
			return nil
		case *ast.MatchStructPattern:
			if _, ok, err := countMatchPatternExpectedLen(p); ok || err != nil {
				return err
			}
			fields, orderedArgs, err := s.resolveStructMatchPatternArgs(p, expected)
			if err != nil {
				return err
			}
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				if err := collectPattern(arg.Pattern, fields[i].Type, out); err != nil {
					return err
				}
			}
			return nil
		case *ast.MatchVariantPattern:
			switch base := expected.(type) {
			case *semantic.EnumType:
				variant, ok := base.Variant(p.Variant)
				if !ok {
					return nil
				}
				orderedArgs, err := s.resolveMatchPatternArgs(p, variant)
				if err != nil {
					return err
				}
				for i, arg := range orderedArgs {
					if arg == nil {
						continue
					}
					if err := collectPattern(arg.Pattern, variant.Payload[i], out); err != nil {
						return err
					}
				}
			case *semantic.TreeCategoryType:
				variant, ok := base.Variant(p.Variant)
				if !ok {
					return nil
				}
				orderedArgs, err := s.resolveMatchPatternArgs(p, variant)
				if err != nil {
					return err
				}
				for i, arg := range orderedArgs {
					if arg == nil {
						continue
					}
					if err := collectPattern(arg.Pattern, variant.Payload[i], out); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	var collectExpr func(expr ast.Expr, truthy bool) (map[string]semantic.Type, error)
	collectExpr = func(expr ast.Expr, truthy bool) (map[string]semantic.Type, error) {
		if expr == nil || !truthy {
			return nil, nil
		}
		switch n := expr.(type) {
		case *ast.ParenExpr:
			return collectExpr(n.Inner, truthy)
		case *ast.OptionalBindExpr:
			if n.Name == "" || n.Name == "_" {
				return nil, nil
			}
			boundType, ok := backendConditionOptionalBindType(s.optionalBindSourceType(n))
			if !ok {
				return nil, nil
			}
			return map[string]semantic.Type{n.Name: boundType}, nil
		case *ast.UnaryExpr:
			return nil, nil
		case *ast.BinaryExpr:
			switch n.Op {
			case lexer.TOKEN_AND:
				left, err := collectExpr(n.Left, true)
				if err != nil {
					return nil, err
				}
				right, err := collectExpr(n.Right, true)
				if err != nil {
					return nil, err
				}
				if len(left) == 0 {
					return right, nil
				}
				if len(right) == 0 {
					return left, nil
				}
				out := make(map[string]semantic.Type, len(left)+len(right))
				for name, typ := range left {
					out[name] = typ
				}
				for name, typ := range right {
					if prev, ok := out[name]; ok && !semantic.SameType(prev, typ) {
						return nil, fmt.Errorf("condition binding %q has inconsistent types %s and %s", name, prev.String(), typ.String())
					}
					out[name] = typ
				}
				return out, nil
			case lexer.TOKEN_OR:
				left, err := collectExpr(n.Left, true)
				if err != nil {
					return nil, err
				}
				right, err := collectExpr(n.Right, true)
				if err != nil {
					return nil, err
				}
				if len(left) == 0 || len(right) == 0 {
					return nil, nil
				}
				out := map[string]semantic.Type{}
				for name, leftType := range left {
					rightType, ok := right[name]
					if !ok || !semantic.SameType(leftType, rightType) {
						continue
					}
					out[name] = leftType
				}
				if len(out) == 0 {
					return nil, nil
				}
				return out, nil
			}
		}
		leftExpr, pattern, ok := s.directConditionPattern(expr)
		if !ok || leftExpr == nil || pattern == nil {
			return nil, nil
		}
		out := map[string]semantic.Type{}
		if err := collectPattern(pattern, s.exprType(leftExpr), out); err != nil {
			return nil, err
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	}
	collected, err := collectExpr(expr, true)
	if err != nil {
		return nil, err
	}
	if len(collected) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(collected))
	for name := range collected {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]conditionBindingInfo, 0, len(names))
	for _, name := range names {
		out = append(out, conditionBindingInfo{name: name, typ: collected[name]})
	}
	return out, nil
}
func (s *functionState) createConditionBindingScope(expr ast.Expr) (*codegenScope, bool, error) {
	infos, err := s.collectTruthyConditionBindings(expr)
	if err != nil {
		return nil, false, err
	}
	if len(infos) == 0 && !s.conditionCanBindVariantView(expr) {
		return nil, false, nil
	}
	scope := &codegenScope{parent: s.scope}
	for _, info := range infos {
		alloca, err := s.createEntryAlloca(info.name+".cond", info.typ)
		if err != nil {
			return nil, false, err
		}
		defineBindingInCodegenScope(scope, info.name, valueBinding{ptr: alloca, typ: info.typ, mutable: false})
	}
	return scope, true, nil
}

func (s *functionState) conditionCanBindVariantView(expr ast.Expr) bool {
	switch n := expr.(type) {
	case nil:
		return false
	case *ast.ParenExpr:
		return s.conditionCanBindVariantView(n.Inner)
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			return s.conditionCanBindVariantView(n.Left) || s.conditionCanBindVariantView(n.Right)
		}
		if n.Op != lexer.TOKEN_IS {
			return false
		}
		_, _, ok := s.conditionVariantViewInfo(n)
		return ok
	default:
		return false
	}
}

func (s *functionState) bindConditionVariantViews(expr ast.Expr) error {
	switch n := expr.(type) {
	case nil:
		return nil
	case *ast.ParenExpr:
		return s.bindConditionVariantViews(n.Inner)
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			if err := s.bindConditionVariantViews(n.Left); err != nil {
				return err
			}
			return s.bindConditionVariantViews(n.Right)
		}
		if n.Op != lexer.TOKEN_IS {
			return nil
		}
		name, viewType, ok := s.conditionVariantViewInfo(n)
		if !ok || name == "" || viewType == nil || viewType.Enum == nil {
			return nil
		}
		binding, ok := s.lookupBinding(name)
		if !ok || binding.ptr == nil {
			return nil
		}
		if viewType.Enum.Packed {
			var store packedStoreBinding
			if viewType.Enum.StoreType != nil {
				resolvedStore, ok := s.lookupPackedStore(viewType.Enum)
				if !ok {
					return nil
				}
				store = resolvedStore
			}
			handle, err := s.loadValue(binding.ptr, viewType.Enum, name+".view")
			if err != nil {
				return err
			}
			s.bindPackedVariantView(name, viewType, nil, handle, store, packedPayloadValueCache{})
			return nil
		}
		s.bindPackedVariantView(name, viewType, binding.ptr, nil, packedStoreBinding{}, packedPayloadValueCache{})
	}
	return nil
}

func (s *functionState) conditionVariantViewInfo(expr *ast.BinaryExpr) (string, *semantic.PackedVariantViewType, bool) {
	if expr == nil || expr.Op != lexer.TOKEN_IS {
		return "", nil, false
	}
	left, ok := expr.Left.(*ast.Ident)
	if !ok || left == nil || left.Name == "" {
		return "", nil, false
	}
	targetEnum, targetVariant, ok := s.enumIsTarget(expr.Right)
	if !ok || targetEnum == nil || targetVariant == nil {
		pattern, ok := s.conditionTargetPattern(expr.Right)
		if !ok || pattern == nil {
			return "", nil, false
		}
		variantPattern, ok := pattern.(*ast.MatchVariantPattern)
		if !ok || variantPattern == nil {
			return "", nil, false
		}
		if actualEnum, ok := s.enumTypeForVariantViewExpr(expr.Left); ok && actualEnum != nil {
			targetEnum = actualEnum
			targetVariant, ok = actualEnum.Variant(variantPattern.Variant)
			if !ok || targetVariant == nil || actualEnum.Name != variantPattern.EnumName {
				return "", nil, false
			}
		}
	}
	if targetEnum == nil || targetVariant == nil {
		return "", nil, false
	}
	actualEnum, ok := s.enumTypeForVariantViewExpr(expr.Left)
	if !ok || actualEnum == nil || actualEnum.Name != targetEnum.Name {
		return "", nil, false
	}
	return left.Name, targetVariant.PackedViewType(actualEnum), true
}

func (s *functionState) enumTypeForVariantViewExpr(expr ast.Expr) (*semantic.EnumType, bool) {
	left, ok := expr.(*ast.Ident)
	if !ok || left == nil || left.Name == "" {
		return nil, false
	}
	var actual semantic.Type
	if binding, ok := s.lookupBinding(left.Name); ok && binding.typ != nil {
		actual = binding.typ
	} else {
		actual = s.exprType(expr)
	}
	if refType, ok := actual.(*semantic.RefType); ok && refType != nil {
		actual = refType.Elem
	}
	actual = semantic.StripAggregateStateType(actual)
	enumType, ok := actual.(*semantic.EnumType)
	return enumType, ok && enumType != nil
}

func (s *functionState) collectMatchPatternBindings(pattern ast.MatchPattern, expected semantic.Type, out map[string]semantic.Type) error {
	if pattern == nil || expected == nil || out == nil {
		return nil
	}
	switch p := pattern.(type) {
	case *ast.MatchBindPattern:
		if p.Name == "" || p.Name == "_" || s.predicateMatchPatternFuncType(p.Name) != nil {
			return nil
		}
		if prev, ok := out[p.Name]; ok && !semantic.SameType(prev, expected) {
			return fmt.Errorf("condition binding %q has inconsistent types %s and %s", p.Name, prev.String(), expected.String())
		}
		out[p.Name] = expected
	case *ast.MatchStructPattern:
		if _, ok, err := countMatchPatternExpectedLen(p); ok || err != nil {
			return err
		}
		fields, orderedArgs, err := s.resolveStructMatchPatternArgs(p, expected)
		if err != nil {
			return err
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			if err := s.collectMatchPatternBindings(arg.Pattern, fields[i].Type, out); err != nil {
				return err
			}
		}
	case *ast.MatchListPattern:
		elemType, ok := semantic.SequenceMatchElementType(expected)
		if !ok {
			return nil
		}
		for _, elem := range p.Elems {
			if _, ok := elem.(*ast.MatchRestPattern); ok {
				continue
			}
			if err := s.collectMatchPatternBindings(elem, elemType, out); err != nil {
				return err
			}
		}
	case *ast.MatchVariantPattern:
		switch base := expected.(type) {
		case *semantic.EnumType:
			variant, ok := base.Variant(p.Variant)
			if !ok {
				return nil
			}
			orderedArgs, err := s.resolveMatchPatternArgs(p, variant)
			if err != nil {
				return err
			}
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				if err := s.collectMatchPatternBindings(arg.Pattern, variant.Payload[i], out); err != nil {
					return err
				}
			}
		case *semantic.TreeCategoryType:
			variant, ok := base.Variant(p.Variant)
			if !ok {
				return nil
			}
			orderedArgs, err := s.resolveMatchPatternArgs(p, variant)
			if err != nil {
				return err
			}
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				if err := s.collectMatchPatternBindings(arg.Pattern, variant.Payload[i], out); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (s *functionState) emitConditionPatternTestAndBind(pattern ast.MatchPattern, actualValue C.LLVMValueRef, actualType semantic.Type, actualExpr ast.Expr, successBB C.LLVMBasicBlockRef, failureBB C.LLVMBasicBlockRef) error {
	if pattern == nil || actualType == nil {
		C.LLVMBuildBr(s.builder, successBB)
		return nil
	}
	if actualExpr == nil {
		actualExpr = &ast.Ident{Name: "<cond>"}
	}
	if bindPattern, ok := pattern.(*ast.MatchBindPattern); ok {
		if handled, err := s.emitPredicateMatchPatternTest(bindPattern.Name, actualValue, actualType, successBB, failureBB); handled || err != nil {
			return err
		}
		if bindPattern.Name != "" && bindPattern.Name != "_" {
			if binding, ok := s.lookupBinding(bindPattern.Name); ok && binding.ptr != nil {
				C.LLVMBuildStore(s.builder, actualValue, binding.ptr)
			}
		}
		C.LLVMBuildBr(s.builder, successBB)
		return nil
	}
	if handled, err := s.emitCountMatchPatternTest(pattern, actualValue, actualType, actualExpr, successBB, failureBB); handled || err != nil {
		return err
	}
	if structPattern, ok := pattern.(*ast.MatchStructPattern); ok {
		fields, orderedArgs, err := s.resolveStructMatchPatternArgs(structPattern, actualType)
		if err != nil {
			return err
		}
		matchedIndexes := make([]int, 0, len(orderedArgs))
		for i, arg := range orderedArgs {
			if arg != nil {
				matchedIndexes = append(matchedIndexes, i)
			}
		}
		if len(matchedIndexes) == 0 {
			C.LLVMBuildBr(s.builder, successBB)
			return nil
		}
		for i, fieldIndex := range matchedIndexes {
			arg := orderedArgs[fieldIndex]
			fieldValue := C.LLVMBuildExtractValue(s.builder, actualValue, C.unsigned(fields[fieldIndex].Index), cStringFree("cond.struct.field"))
			nextSuccess := successBB
			if i != len(matchedIndexes)-1 {
				nextSuccess = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cond.struct.next"))
			}
			fieldExpr := ast.Expr(nil)
			if actualExpr != nil {
				fieldExpr = &ast.FieldExpr{Position: arg.Position, Object: actualExpr, Field: fields[fieldIndex].Decl.Name}
			}
			if err := s.emitConditionPatternTestAndBind(arg.Pattern, fieldValue, fields[fieldIndex].Type, fieldExpr, nextSuccess, failureBB); err != nil {
				return err
			}
			if i != len(matchedIndexes)-1 {
				C.LLVMPositionBuilderAtEnd(s.builder, nextSuccess)
			}
		}
		return nil
	}
	if _, _, err := s.emitMatchPatternTest(pattern, actualValue, nil, actualType, nil, actualExpr, nil, successBB, failureBB); err != nil {
		return err
	}
	return nil
}
