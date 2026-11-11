//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"strconv"
)

func (s *functionState) emitSequenceCountValueFromPatternValue(actualValue llvmIRValueRef, actualType semantic.Type, originExpr ast.Expr, name string) (llvmIRValueRef, error) {
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
	case *semantic.CStrType, *semantic.SViewType:
		if originExpr == nil {
			return nil, fmt.Errorf("count pattern length requires an origin expression for %s", actualType.String())
		}
		return s.emitSequenceCountValue(originExpr, actualType, name)
	default:
		return nil, fmt.Errorf("count pattern length is not implemented for %s", actualType.String())
	}
}
func (s *functionState) emitCountMatchPatternTest(pattern ast.MatchPattern, actualValue llvmIRValueRef, actualType semantic.Type, originExpr ast.Expr, successBB llvmIRBlockRef, failureBB llvmIRBlockRef) (bool, error) {
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
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return true, err
	}
	expectedValue := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(expected), 0)
	countOK := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), countValue, expectedValue, cStringFree("match.count.ok"))
	llvmBuildCondBr(s.builder, countOK, successBB, failureBB)
	return true, nil
}
func (s *functionState) emitListMatchPatternTest(pattern *ast.MatchListPattern, actualValue llvmIRValueRef, actualType semantic.Type, originExpr ast.Expr, successBB llvmIRBlockRef, failureBB llvmIRBlockRef) error {
	if pattern == nil {
		llvmBuildBr(s.builder, successBB)
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
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	hasRest := false
	var restPattern *ast.MatchRestPattern
	expectedCount := len(pattern.Elems)
	if expectedCount != 0 {
		if rest, ok := pattern.Elems[expectedCount-1].(*ast.MatchRestPattern); ok {
			hasRest = true
			restPattern = rest
			expectedCount--
		}
	}
	boundRest := restPattern != nil && restPattern.Name != ""
	expectedLen := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(expectedCount), 0)
	predicate := llvmIRIntPredicate(llvmIntEQ)
	if hasRest {
		predicate = llvmIRIntPredicate(llvmIntUGE)
	}
	lenOK := llvmBuildICmp(s.builder, predicate, countValue, expectedLen, cStringFree("match.list.len.ok"))
	itemsBB := successBB
	if expectedCount != 0 || boundRest {
		itemsBB = llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.list.items"))
	}
	llvmBuildCondBr(s.builder, lenOK, itemsBB, failureBB)
	if expectedCount == 0 && !boundRest {
		return nil
	}
	llvmPositionBuilderAtEnd(s.builder, itemsBB)
	if boundRest {
		// docs/122 §5.3: bind the unmatched tail as a view (`[first, ...rest]`) — the
		// whole-sequence view sliced from the fixed prefix to the runtime count, at the
		// exact type the analyzer declared (RestViewTypeForSequence).
		if err := s.emitListRestBinding(restPattern, actualValue, actualType, elemType, originExpr, expectedCount, countValue, usizeLLVMType); err != nil {
			return err
		}
	}
	if expectedCount == 0 {
		// Bound-rest-only pattern (`[...rest]`): no element tests — close the block.
		llvmBuildBr(s.builder, successBB)
		return nil
	}
	var sourceAlloca llvmIRValueRef
	for i := 0; i < expectedCount; i++ {
		elem := pattern.Elems[i]
		var indexExpr ast.Expr
		var elemValue llvmIRValueRef
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
			indexValue := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(i), 0)
			var err error
			elemValue, _, err = s.emitIterLoopElementValue(nil, sourceAlloca, actualType, indexValue, "match.list")
			if err != nil {
				return err
			}
		}
		nextSuccess := successBB
		if i != expectedCount-1 {
			nextSuccess = llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.list.pattern.next"))
		}
		if _, _, err := s.emitMatchPatternTest(elem, elemValue, nil, elemType, nil, indexExpr, nil, nextSuccess, failureBB); err != nil {
			return err
		}
		if i != expectedCount-1 {
			llvmPositionBuilderAtEnd(s.builder, nextSuccess)
		}
	}
	return nil
}

// emitPatternAsBinding materializes the docs/122 §5.4 as-binding (`Pattern as whole`):
// the entire matched value stored under the given name. Emitted unconditionally at the
// pattern-test site — sound because the binding is only referable from the arm body,
// which only runs when the whole pattern (and guard) matched.
func (s *functionState) emitPatternAsBinding(name string, actualValue llvmIRValueRef, actualType semantic.Type, originExpr ast.Expr) error {
	if name == "" {
		return nil
	}
	if actualValue == nil {
		if originExpr == nil {
			return fmt.Errorf("as-binding %q requires a matched value", name)
		}
		var err error
		actualValue, _, err = s.emitExpr(originExpr, actualType)
		if err != nil {
			return err
		}
	}
	alloca, err := s.createEntryAlloca(name, actualType)
	if err != nil {
		return err
	}
	if err := s.storeValue(alloca, actualValue, actualType, name); err != nil {
		return err
	}
	s.defineBinding(name, valueBinding{ptr: alloca, typ: actualType})
	return nil
}

// emitListRestBinding materializes the docs/122 §5.3 bound list rest: a read-only view
// over the source's tail (`[prefixLen, count)`), stored into a fresh alloca and bound
// under the rest name. Supports darray and view sources (through one non-null ref).
func (s *functionState) emitListRestBinding(rest *ast.MatchRestPattern, actualValue llvmIRValueRef, actualType semantic.Type, elemType semantic.Type, originExpr ast.Expr, prefixLen int, countValue llvmIRValueRef, usizeLLVMType llvmIRTypeRef) error {
	restType, ok := semantic.RestViewTypeForSequence(actualType, elemType)
	if !ok {
		return fmt.Errorf("rest binding requires an array, darray, or view value, got %s", actualType.String())
	}
	baseValue := actualValue
	baseType := semantic.StripAggregateStateType(actualType)
	if baseValue == nil {
		if originExpr == nil {
			return fmt.Errorf("rest binding requires an origin expression or sequence value")
		}
		var err error
		baseValue, _, err = s.emitExpr(originExpr, actualType)
		if err != nil {
			return err
		}
	}
	if refType, isRef := baseType.(*semantic.RefType); isRef && refType.State == semantic.RefStateNonNull {
		inner := semantic.StripAggregateStateType(refType.Elem)
		innerLLVM, err := s.g.lowerType(inner)
		if err != nil {
			return err
		}
		baseValue = llvmBuildLoad2(s.builder, innerLLVM, baseValue, cStringFree("match.rest.deref"))
		baseType = inner
	}
	var wholeView llvmIRValueRef
	switch t := baseType.(type) {
	case *semantic.DArrayType:
		var err error
		wholeView, err = s.buildDynArrayViewValue(baseValue, t, restType, "match.rest")
		if err != nil {
			return err
		}
	case *semantic.ViewType:
		wholeView = baseValue
	default:
		return fmt.Errorf("rest binding is not supported for %s values yet", actualType.String())
	}
	startValue := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(prefixLen), 0)
	restValue, err := s.emitArenaViewSliceValue(wholeView, restType, startValue, countValue, "match.rest.view")
	if err != nil {
		return err
	}
	alloca, err := s.createEntryAlloca(rest.Name, restType)
	if err != nil {
		return err
	}
	llvmBuildStore(s.builder, restValue, alloca)
	s.defineBinding(rest.Name, valueBinding{ptr: alloca, typ: restType})
	return nil
}
