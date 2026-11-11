//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"elisacore/src/unparse"
	"fmt"
)

func (s *functionState) emitIterLoopPatternBindings(pattern ast.MoveBindPattern, mode ast.IterBindMode, itemType semantic.Type, itemValue llvmIRValueRef, itemPtr llvmIRValueRef) error {
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
				llvmBuildStore(s.builder, itemValue, tempAlloca)
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
					fieldValue := llvmBuildExtractValue(s.builder, itemValue, llvmIRUnsigned(field.Index), cStringFree(arg.Name+".iter.field"))
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
				fieldPtr := llvmBuildStructGEP2(s.builder, containerLLVMType, itemPtr, llvmIRUnsigned(field.Index), cStringFree(arg.Name+".iter.field.ptr"))
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
				fieldValue := llvmBuildExtractValue(s.builder, itemValue, llvmIRUnsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.field"))
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
			fieldPtr := llvmBuildStructGEP2(s.builder, containerLLVMType, itemPtr, llvmIRUnsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.field.ptr"))
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
				fieldValue := llvmBuildExtractValue(s.builder, itemValue, llvmIRUnsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.tuple.field"))
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
			fieldPtr := llvmBuildStructGEP2(s.builder, containerLLVMType, itemPtr, llvmIRUnsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.tuple.field.ptr"))
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
func (s *functionState) buildEnumerateItemValue(tupleType semantic.Type, indexValue llvmIRValueRef, itemValue llvmIRValueRef, itemActualType semantic.Type, name string) (llvmIRValueRef, error) {
	tuple, ok := semantic.StripAggregateStateType(tupleType).(*semantic.TupleType)
	if !ok || tuple == nil || len(tuple.Fields) != 2 {
		return nil, fmt.Errorf("enumerate loop item requires a 2-field tuple type")
	}
	tupleLLVMType, err := s.g.lowerType(tuple)
	if err != nil {
		return nil, err
	}
	indexCoerced, err := s.coerceValue(indexValue, s.g.namedType("usize"), tuple.Fields[0].Type)
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
	value := llvmGetUndef(tupleLLVMType)
	value = llvmBuildInsertValue(s.builder, value, indexCoerced, 0, cStringFree(name+".enumerate.item.index.insert"))
	value = llvmBuildInsertValue(s.builder, value, itemCoerced, 1, cStringFree(name+".enumerate.item.value.insert"))
	return value, nil
}

type iterViewTransformKind int

const (
	iterViewTransformEnumerate iterViewTransformKind = iota
)

type iterViewTransform struct {
	kind     iterViewTransformKind
	itemType semantic.Type
}

func (s *functionState) peelIterViewTransforms(sourceName string, sourceAlloca llvmIRValueRef, sourceType semantic.Type, bindMode ast.IterBindMode) (llvmIRValueRef, semantic.Type, []iterViewTransform, error) {
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
			innerSourceValue := llvmBuildExtractValue(s.builder, carrierValue, 0, cStringFree(sourceName+".enumerate.source.extract"))
			llvmBuildStore(s.builder, innerSourceValue, innerSourceAlloca)
			transforms = append(transforms, iterViewTransform{kind: iterViewTransformEnumerate, itemType: enumerateItemType})
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
		default:
			return expr
		}
	}
}

func (s *functionState) applyIterViewTransforms(sourceName string, indexValue llvmIRValueRef, stepBB llvmIRBlockRef, itemValue llvmIRValueRef, itemType semantic.Type, transforms []iterViewTransform) (llvmIRValueRef, semantic.Type, error) {
	currentValue := itemValue
	currentType := itemType
	for i := len(transforms) - 1; i >= 0; i-- {
		transform := transforms[i]
		switch transform.kind {
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

func (s *functionState) emitEqualityCompare(left llvmIRValueRef, leftType semantic.Type, right llvmIRValueRef, rightType semantic.Type, name string) (llvmIRValueRef, error) {
	rightValue, err := s.coerceValue(right, rightType, leftType)
	if err != nil {
		return nil, err
	}
	if isFloatType(leftType) {
		return llvmBuildFCmp(s.builder, llvmIRRealPredicate(llvmRealOEQ), left, rightValue, cStringFree(name)), nil
	}
	return llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), left, rightValue, cStringFree(name)), nil
}

func (s *functionState) emitIterFilterBranch(pattern ast.MoveBindPattern, sourceType semantic.Type, rowValue llvmIRValueRef, filter ast.Expr, passBB llvmIRBlockRef, failBB llvmIRBlockRef, name string) error {
	if filter == nil {
		llvmBuildBr(s.builder, passBB)
		return nil
	}
	if binary, ok := filter.(*ast.BinaryExpr); ok && binary != nil {
		switch binary.Op {
		case lexer.TOKEN_AND:
			rightBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".and.rhs"))
			if err := s.emitIterFilterBranch(pattern, sourceType, rowValue, binary.Left, rightBB, failBB, name+".and.left"); err != nil {
				return err
			}
			llvmPositionBuilderAtEnd(s.builder, rightBB)
			return s.emitIterFilterBranch(pattern, sourceType, rowValue, binary.Right, passBB, failBB, name+".and.right")
		case lexer.TOKEN_OR:
			rightBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".or.rhs"))
			if err := s.emitIterFilterBranch(pattern, sourceType, rowValue, binary.Left, passBB, rightBB, name+".or.left"); err != nil {
				return err
			}
			llvmPositionBuilderAtEnd(s.builder, rightBB)
			return s.emitIterFilterBranch(pattern, sourceType, rowValue, binary.Right, passBB, failBB, name+".or.right")
		}
	}
	filterValue, filterType, err := s.emitExpr(filter, nil)
	if err != nil {
		return fmt.Errorf("emit iterable filter at %s (%T %q): %w", filter.Pos(), filter, unparse.FormatExpr(filter), err)
	}
	filterBool, err := s.coerceValue(filterValue, filterType, s.g.namedType("bool"))
	if err != nil {
		return err
	}
	llvmBuildCondBr(s.builder, filterBool, passBB, failBB)
	return nil
}

func (s *functionState) emitIterForStmt(stmt *ast.IterForStmt) error {
	// Auto-reservation (region inference, Phase A): presize the fill target before the loop.
	for _, preReserve := range ast.EmittedPreReserveStmts(stmt) {
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
	llvmBuildStore(s.builder, sourceValue, sourceAlloca)

	iterSourceAlloca, iterSourceType, transforms, err := s.peelIterViewTransforms(sourceName, sourceAlloca, sourceType, stmt.Mode)
	if err != nil {
		return err
	}
	iterSourceExpr := iterLoopBaseSourceExpr(stmt.Source)
	countValue, err := s.emitIterLoopCount(iterSourceExpr, iterSourceAlloca, iterSourceType, sourceName)
	if err != nil {
		return err
	}
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	indexAlloca, err := s.createEntryAlloca(sourceName+".index", usizeType)
	if err != nil {
		return err
	}
	zeroValue := llvmConstInt(usizeLLVMType, 0, 0)
	llvmBuildStore(s.builder, zeroValue, indexAlloca)

	condBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.cond"))
	bodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.body"))
	stepBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.step"))
	exitBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.end"))
	llvmBuildBr(s.builder, condBB)

	llvmPositionBuilderAtEnd(s.builder, condBB)
	indexValue, err := s.loadValue(indexAlloca, usizeType, sourceName+".index")
	if err != nil {
		return err
	}
	condValue := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), indexValue, countValue, cStringFree("iter.cmp"))
	llvmBuildCondBr(s.builder, condValue, bodyBB, exitBB)

	itemType, ok := iterLoopItemTypeBackend(iterSourceType)
	if !ok && stmt.Mode != ast.IterBindValue {
		return fmt.Errorf("iterable loop ref binding requires an addressable array-like source, got %s", iterSourceType.String())
	}
	llvmPositionBuilderAtEnd(s.builder, bodyBB)
	s.pushScope()
	iterScope := s.scope
	drainCleanupFloor := -1
	var drainBinder scopedCleanupBinding
	iterIndexValue := indexValue
	if stmt.Reverse {
		lastIndex := llvmBuildSub(s.builder, countValue, llvmConstInt(usizeLLVMType, 1, 0), cStringFree("iter.rev.last"))
		iterIndexValue = llvmBuildSub(s.builder, lastIndex, indexValue, cStringFree("iter.rev.index"))
	}
	if colExpr, ok := iterSourceExpr.(*ast.EnumColumnExpr); ok {
		if _, err := s.emitEnumColumnScanCategoryFilter(colExpr, iterIndexValue, stepBB, sourceName); err != nil {
			s.popScope()
			return err
		}
	}
	if iterLoopIsDictSource(iterSourceType) {
		occupiedBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.dict.occupied"))
		occupiedValue, err := s.emitIterLoopDictOccupied(iterSourceAlloca, iterSourceType, iterIndexValue, sourceName)
		if err != nil {
			s.popScope()
			return err
		}
		llvmBuildCondBr(s.builder, occupiedValue, occupiedBB, stepBB)
		llvmPositionBuilderAtEnd(s.builder, occupiedBB)
	}
	var boundItemValue llvmIRValueRef
	var boundItemPtr llvmIRValueRef
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
		// docs/126 D1 for a move-drain: each element is moved OUT of the container into
		// the binder, so the binder owns it for one iteration. Arm its destructor like any
		// drop-typed local; the body's `move x` clears the flag, and an unconsumed binder
		// is dropped at the end of the iteration instead of leaking.
		if stmt.MovedSource {
			if namePattern, ok := stmt.Pattern.(*ast.MoveBindNamePattern); ok && namePattern.Name != "_" {
				if binding, found := s.lookupBinding(namePattern.Name); found {
					armed, err := s.registerDropCleanup(namePattern.Name, binding.ptr, itemType)
					if err != nil {
						s.popScope()
						return err
					}
					if armed {
						drainCleanupFloor = len(s.scopedCleanups) - 1
						drainBinder = s.scopedCleanups[drainCleanupFloor]
					}
				}
			}
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
	// A filtered-out drained element still belongs to the binder: route the filters'
	// skip edge through a block that drops it before stepping.
	filterSkipBB := stepBB
	if drainCleanupFloor >= 0 && (stmt.PatternFilter != nil || stmt.WhereFilter != nil || stmt.Filter != nil) {
		filterSkipBB = llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.drain.filtered"))
		resumeBB := llvmGetInsertBlock(s.builder)
		llvmPositionBuilderAtEnd(s.builder, filterSkipBB)
		if err := s.emitConditionalDrop(drainBinder); err != nil {
			s.popScope()
			return err
		}
		if !s.currentBlockTerminated() {
			llvmBuildBr(s.builder, stepBB)
		}
		llvmPositionBuilderAtEnd(s.builder, resumeBB)
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
		filterBodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.pattern.filter.body"))
		if _, _, err := s.emitMatchPatternTest(stmt.PatternFilter, filterValue, nil, filterType, nil, nil, nil, filterBodyBB, filterSkipBB); err != nil {
			s.popScope()
			return err
		}
		llvmPositionBuilderAtEnd(s.builder, filterBodyBB)
	}
	if stmt.WhereFilter != nil {
		filterBodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.where.filter.body"))
		if err := s.emitIterFilterBranch(stmt.Pattern, iterSourceType, boundItemValue, stmt.WhereFilter, filterBodyBB, filterSkipBB, "iter.where.filter"); err != nil {
			s.popScope()
			return err
		}
		llvmPositionBuilderAtEnd(s.builder, filterBodyBB)
	}
	if stmt.Filter != nil {
		filterBodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.filter.body"))
		if err := s.emitIterFilterBranch(stmt.Pattern, iterSourceType, boundItemValue, stmt.Filter, filterBodyBB, filterSkipBB, "iter.filter"); err != nil {
			s.popScope()
			return err
		}
		llvmPositionBuilderAtEnd(s.builder, filterBodyBB)
	}
	s.pushLoopTargets(exitBB, stepBB)
	if drainCleanupFloor >= 0 {
		// The binder belongs to this iteration: a break/continue leaving it must drop it too.
		s.loopCleanupFloors[len(s.loopCleanupFloors)-1] = drainCleanupFloor
	}
	if err := s.emitBlock(stmt.Body, true); err != nil {
		s.popLoopTargets()
		s.discardScopeCleanups(iterScope)
		s.popScope()
		return err
	}
	s.popLoopTargets()
	if drainCleanupFloor >= 0 {
		if s.currentBlockTerminated() {
			s.discardScopeCleanups(iterScope)
		} else if err := s.emitScopeCleanups(iterScope); err != nil {
			s.popScope()
			return err
		}
	}
	s.popScope()
	if !s.currentBlockTerminated() {
		llvmBuildBr(s.builder, stepBB)
	}

	llvmPositionBuilderAtEnd(s.builder, stepBB)
	if !s.currentBlockTerminated() {
		nextValue := llvmBuildAdd(s.builder, indexValue, llvmConstInt(usizeLLVMType, 1, 0), cStringFree("iter.next"))
		llvmBuildStore(s.builder, nextValue, indexAlloca)
		backedge := llvmBuildBr(s.builder, condBB)
		// A plain forward `for x in xs:` whose body is the clean element-wise store shape is
		// expected to vectorize (docs/70). Filtered / reversed / view-transformed / dict / column
		// -scan loops have body-side branching that legitimately explains scalar execution, so
		// only the unbranched shape is tagged; a `can Scalar` grant suppresses the tag (checked
		// inside tagAutovecExpectedLoop). Scalar fallback then warns, or errors under -Wperf.
		_, isColumnScan := iterSourceExpr.(*ast.EnumColumnExpr)
		if stmt.PatternFilter == nil && stmt.WhereFilter == nil && stmt.Filter == nil && !stmt.Reverse &&
			len(transforms) == 0 && !isColumnScan && !iterLoopIsDictSource(iterSourceType) &&
			s.userLoopVectorEligible(stmt.Body) {
			s.tagAutovecExpectedLoop(backedge, stmt.Position, "loop")
		}
	}

	llvmPositionBuilderAtEnd(s.builder, exitBB)
	return nil
}
