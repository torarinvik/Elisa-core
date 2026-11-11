//go:build cgo

package backend

import (
	"fmt"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

type sequenceRewriteCodegenContext struct {
	outPtr  llvmIRValueRef
	outType *semantic.DArrayType
}

type sequenceRewriteArmInfoBackend struct {
	bindType      semantic.Type
	bindName      string
	alwaysMatches bool
	memberType    semantic.Type
}

func sequenceRewriteTargetTypeExprBackend(root ast.TypeExpr) (ast.TypeExpr, bool) {
	generic, ok := root.(*ast.GenericType)
	if !ok || generic == nil || generic.Name != "sequence" || len(generic.Args) != 1 {
		return nil, false
	}
	return generic.Args[0], true
}

func sequenceRewriteCarrierElemTypeBackend(t semantic.Type) (semantic.Type, bool) {
	switch tt := semantic.StripAggregateStateType(t).(type) {
	case *semantic.DArrayType:
		return tt.Elem, true
	case *semantic.ViewType:
		if tt.SurfaceName != "" && tt.SurfaceName != "view" {
			return nil, false
		}
		return tt.Elem, true
	default:
		return nil, false
	}
}

func sequenceRewriteArmBindNameBackend(arm ast.VisitArm) (string, bool) {
	if arm.Wildcard {
		return "", true
	}
	if arm.TargetName != "" && !strings.Contains(arm.TargetName, ".") && arm.BindName == "" && arm.ChildResultsName == "" && len(arm.ChildBindings) == 0 {
		return arm.TargetName, true
	}
	return "", false
}

func (s *functionState) resolveSequenceRewriteArmInfoBackend(elemType semantic.Type, arm ast.VisitArm) (sequenceRewriteArmInfoBackend, error) {
	if arm.Wildcard {
		return sequenceRewriteArmInfoBackend{alwaysMatches: true}, nil
	}
	if bindName, ok := sequenceRewriteArmBindNameBackend(arm); ok {
		return sequenceRewriteArmInfoBackend{bindType: elemType, bindName: bindName, alwaysMatches: true}, nil
	}
	return sequenceRewriteArmInfoBackend{}, fmt.Errorf("sequence rewrite arms currently support only `_` or a bare element binding name")
}

func (s *functionState) emitSequenceRewriteEmitExpr(expr *ast.EmitExpr) (llvmIRValueRef, semantic.Type, error) {
	if s.currentSequenceRewrite == nil || s.currentSequenceRewrite.outType == nil {
		return nil, nil, fmt.Errorf("emit is only allowed inside sequence rewrite arms")
	}
	voidType := s.g.namedType("void")
	if expr == nil || expr.Nothing || expr.Value == nil {
		return nil, voidType, nil
	}
	if expr.All {
		sequenceValue, actualType, err := s.emitExpr(expr.Value, nil)
		if err != nil {
			return nil, nil, err
		}
		sequenceElemType, ok := sequenceRewriteCarrierElemTypeBackend(actualType)
		if !ok || sequenceElemType == nil {
			return nil, nil, fmt.Errorf("emit all expects a darray or view source, got %s", actualType.String())
		}
		sequenceViewType := &semantic.ViewType{Elem: sequenceElemType, SurfaceName: "view"}
		sequenceViewValue, err := s.coerceValue(sequenceValue, actualType, sequenceViewType)
		if err != nil {
			return nil, nil, err
		}
		if err := s.emitSequenceRewriteAppendView(s.currentSequenceRewrite.outPtr, s.currentSequenceRewrite.outType, sequenceViewValue, sequenceViewType, "sequence.emit.all"); err != nil {
			return nil, nil, err
		}
		return nil, voidType, nil
	}
	itemValue, _, err := s.emitExpr(expr.Value, s.currentSequenceRewrite.outType.Elem)
	if err != nil {
		return nil, nil, err
	}
	if err := s.emitSequenceRewriteAppend(s.currentSequenceRewrite.outPtr, s.currentSequenceRewrite.outType, itemValue, "sequence.emit"); err != nil {
		return nil, nil, err
	}
	return nil, voidType, nil
}

func (s *functionState) emitSequenceRewriteAppend(outPtr llvmIRValueRef, outType *semantic.DArrayType, itemValue llvmIRValueRef, name string) error {
	if outPtr == nil || outType == nil {
		return fmt.Errorf("missing sequence rewrite output")
	}
	owner, ok := s.regionArenaOwner(outType.Region)
	if !ok {
		owner, ok = s.lookupTreeAllocOwner()
	}
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return fmt.Errorf("%s", semantic.NoContainerRegionMessage("sequence rewrite"))
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, name+".owner.arena")
		if err != nil {
			return err
		}
		owner.arenaRef = arenaRef
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(outPtr, outType)
	if err != nil {
		return err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	currentCount := llvmBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree(name+".count"))
	neededValue, err := s.emitCheckedUSizeAdd(currentCount, llvmConstInt(usizeLLVMType, 1, 0), name+".needed")
	if err != nil {
		return err
	}
	if err := s.emitBuiltinDArrayEnsureCapacity(outPtr, outType, owner.arenaRef, neededValue, name); err != nil {
		return err
	}
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(outPtr, outType)
	if err != nil {
		return err
	}
	voidPtrType := llvmPointerTypeInContext(s.g.context, 0)
	itemsValue := llvmBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree(name+".items"))
	elemLLVMType, err := s.g.lowerType(outType.Elem)
	if err != nil {
		return err
	}
	slotPtr := llvmBuildGEP2(s.builder, elemLLVMType, itemsValue, llvmValueSlicePtr([]llvmIRValueRef{currentCount}), 1, cStringFree(name+".slot"))
	llvmBuildStore(s.builder, itemValue, slotPtr)
	llvmBuildStore(s.builder, neededValue, countPtr)
	return nil
}

func (s *functionState) emitSequenceRewriteAppendView(outPtr llvmIRValueRef, outType *semantic.DArrayType, viewValue llvmIRValueRef, viewType *semantic.ViewType, name string) error {
	if outPtr == nil || outType == nil || viewType == nil {
		return fmt.Errorf("missing sequence rewrite output")
	}
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	elemLLVMType, err := s.g.lowerType(outType.Elem)
	if err != nil {
		return err
	}
	dataValue := llvmBuildExtractValue(s.builder, viewValue, 0, cStringFree(name+".data"))
	lenValue := llvmBuildExtractValue(s.builder, viewValue, 1, cStringFree(name+".len"))
	indexPtr := llvmBuildAlloca(s.builder, usizeLLVMType, cStringFree(name+".index"))
	zero := llvmConstInt(usizeLLVMType, 0, 0)
	one := llvmConstInt(usizeLLVMType, 1, 0)
	llvmBuildStore(s.builder, zero, indexPtr)
	loopBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".loop"))
	bodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".body"))
	doneBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".done"))
	llvmBuildBr(s.builder, loopBB)

	llvmPositionBuilderAtEnd(s.builder, loopBB)
	indexValue := llvmBuildLoad2(s.builder, usizeLLVMType, indexPtr, cStringFree(name+".index.load"))
	condValue := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), indexValue, lenValue, cStringFree(name+".cond"))
	llvmBuildCondBr(s.builder, condValue, bodyBB, doneBB)

	llvmPositionBuilderAtEnd(s.builder, bodyBB)
	elemPtr := llvmBuildGEP2(s.builder, elemLLVMType, dataValue, llvmValueSlicePtr([]llvmIRValueRef{indexValue}), 1, cStringFree(name+".elem.ptr"))
	elemValue := llvmBuildLoad2(s.builder, elemLLVMType, elemPtr, cStringFree(name+".elem"))
	if err := s.emitSequenceRewriteAppend(outPtr, outType, elemValue, name); err != nil {
		return err
	}
	nextValue := llvmBuildAdd(s.builder, indexValue, one, cStringFree(name+".next"))
	llvmBuildStore(s.builder, nextValue, indexPtr)
	llvmBuildBr(s.builder, loopBB)

	llvmPositionBuilderAtEnd(s.builder, doneBB)
	return nil
}

func (s *functionState) emitSequenceRewriteArmBody(body []ast.Stmt) error {
	if len(body) == 0 {
		return nil
	}
	scope := s.scope
	for _, stmt := range body {
		if err := s.emitStmt(stmt); err != nil {
			s.discardScopeCleanups(scope)
			return err
		}
		if s.currentBlockTerminated() {
			s.discardScopeCleanups(scope)
			return nil
		}
	}
	return s.emitScopeCleanups(scope)
}

func (s *functionState) emitSequenceRewriteArms(elemValue llvmIRValueRef, elemType semantic.Type, arms []ast.VisitArm, outPtr llvmIRValueRef, outType *semantic.DArrayType, name string) error {
	endBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".end"))
	for index, arm := range arms {
		armInfo, err := s.resolveSequenceRewriteArmInfoBackend(elemType, arm)
		if err != nil {
			return err
		}
		var nextBB llvmIRBlockRef
		_ = nextBB
		s.pushScope()
		if armInfo.bindName != "" && armInfo.bindType != nil {
			if err := s.emitMoveBindLocal(armInfo.bindName, armInfo.bindType, elemValue); err != nil {
				s.popScope()
				return err
			}
		}
		if arm.Guard != nil {
			bodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".guard.body"))
			if nextBB == nil {
				nextBB = llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".guard.next"))
			}
			guardValue, _, err := s.emitExpr(arm.Guard, s.g.namedType("bool"))
			if err != nil {
				s.popScope()
				return err
			}
			llvmBuildCondBr(s.builder, guardValue, bodyBB, nextBB)
			llvmPositionBuilderAtEnd(s.builder, bodyBB)
		}
		saved := s.currentSequenceRewrite
		s.currentSequenceRewrite = &sequenceRewriteCodegenContext{outPtr: outPtr, outType: outType}
		err = s.emitSequenceRewriteArmBody(arm.Body)
		s.currentSequenceRewrite = saved
		if err != nil {
			s.popScope()
			return err
		}
		if !s.currentBlockTerminated() {
			llvmBuildBr(s.builder, endBB)
		}
		s.popScope()
		if armInfo.alwaysMatches && arm.Guard == nil {
			break
		}
		if nextBB != nil {
			llvmPositionBuilderAtEnd(s.builder, nextBB)
			if index == len(arms)-1 {
				llvmBuildBr(s.builder, endBB)
			}
		}
	}
	if !s.currentBlockTerminated() {
		llvmBuildBr(s.builder, endBB)
	}
	llvmPositionBuilderAtEnd(s.builder, endBB)
	return nil
}

func (s *functionState) emitSequenceRewriteExpr(expr *ast.FoldExpr) (llvmIRValueRef, semantic.Type, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("missing sequence rewrite expression")
	}
	resultType, ok := s.exprType(expr).(*semantic.DArrayType)
	if !ok || resultType == nil {
		return nil, nil, fmt.Errorf("sequence rewrite requires a darray result type")
	}
	sourceType := s.exprType(expr.Value)
	sourceElemType, ok := sequenceRewriteCarrierElemTypeBackend(sourceType)
	if !ok || sourceElemType == nil {
		return nil, nil, fmt.Errorf("sequence rewrite expects a darray or view source, got %s", sourceType.String())
	}
	sourceValue, _, err := s.emitExpr(expr.Value, sourceType)
	if err != nil {
		return nil, nil, err
	}
	sourceViewType := &semantic.ViewType{Elem: sourceElemType, SurfaceName: "view"}
	sourceViewValue, err := s.coerceValue(sourceValue, sourceType, sourceViewType)
	if err != nil {
		return nil, nil, err
	}
	zeroOut, err := s.zeroValue(resultType)
	if err != nil {
		return nil, nil, err
	}
	outPtr, err := s.createEntryAlloca("sequence.rewrite.out", resultType)
	if err != nil {
		return nil, nil, err
	}
	llvmBuildStore(s.builder, zeroOut, outPtr)
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, err
	}
	elemLLVMType, err := s.g.lowerType(sourceElemType)
	if err != nil {
		return nil, nil, err
	}
	// NOTE: do not spill sourceViewValue into a `view`-typed alloca here. When the
	// source is a by-value darray parameter, coerceValue returns the 24-byte darray
	// aggregate unchanged, and storing it through a 16-byte `view` slot overflows the
	// adjacent stack slot (clobbering, e.g., the `alloc` arena pointer) at -O0. The
	// data/len fields are read straight out of sourceViewValue, so the spill was dead.
	dataValue := llvmBuildExtractValue(s.builder, sourceViewValue, 0, cStringFree("sequence.rewrite.data"))
	lenValue := llvmBuildExtractValue(s.builder, sourceViewValue, 1, cStringFree("sequence.rewrite.len"))
	indexPtr := llvmBuildAlloca(s.builder, usizeLLVMType, cStringFree("sequence.rewrite.index"))
	zero := llvmConstInt(usizeLLVMType, 0, 0)
	one := llvmConstInt(usizeLLVMType, 1, 0)
	llvmBuildStore(s.builder, zero, indexPtr)
	loopBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("sequence.rewrite.loop"))
	bodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("sequence.rewrite.body"))
	endBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("sequence.rewrite.done"))
	llvmBuildBr(s.builder, loopBB)

	llvmPositionBuilderAtEnd(s.builder, loopBB)
	indexValue := llvmBuildLoad2(s.builder, usizeLLVMType, indexPtr, cStringFree("sequence.rewrite.index.load"))
	condValue := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), indexValue, lenValue, cStringFree("sequence.rewrite.cond"))
	llvmBuildCondBr(s.builder, condValue, bodyBB, endBB)

	llvmPositionBuilderAtEnd(s.builder, bodyBB)
	elemPtr := llvmBuildGEP2(s.builder, elemLLVMType, dataValue, llvmValueSlicePtr([]llvmIRValueRef{indexValue}), 1, cStringFree("sequence.rewrite.elem.ptr"))
	elemValue := llvmBuildLoad2(s.builder, elemLLVMType, elemPtr, cStringFree("sequence.rewrite.elem"))
	if err := s.emitSequenceRewriteArms(elemValue, sourceElemType, expr.Arms, outPtr, resultType, "sequence.rewrite.arm"); err != nil {
		return nil, nil, err
	}
	if !s.currentBlockTerminated() {
		nextValue := llvmBuildAdd(s.builder, indexValue, one, cStringFree("sequence.rewrite.next"))
		llvmBuildStore(s.builder, nextValue, indexPtr)
		llvmBuildBr(s.builder, loopBB)
	}

	llvmPositionBuilderAtEnd(s.builder, endBB)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	resultValue := llvmBuildLoad2(s.builder, resultLLVMType, outPtr, cStringFree("sequence.rewrite.result"))
	return resultValue, resultType, nil
}
