//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

void llctxSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight);
*/
import "C"

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/semantic"
)

func (s *functionState) emitIterLoopPatternBindings(pattern ast.MoveBindPattern, mode ast.IterBindMode, itemType semantic.Type, itemValue C.LLVMValueRef, itemPtr C.LLVMValueRef) error {
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
				C.LLVMBuildStore(s.builder, itemValue, tempAlloca)
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
					fieldValue := C.LLVMBuildExtractValue(s.builder, itemValue, C.unsigned(field.Index), cStringFree(arg.Name+".iter.field"))
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
				fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, itemPtr, C.unsigned(field.Index), cStringFree(arg.Name+".iter.field.ptr"))
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
				fieldValue := C.LLVMBuildExtractValue(s.builder, itemValue, C.unsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.field"))
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
			fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, itemPtr, C.unsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.field.ptr"))
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
				fieldValue := C.LLVMBuildExtractValue(s.builder, itemValue, C.unsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.tuple.field"))
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
			fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, itemPtr, C.unsigned(fields[i].Index), cStringFree(p.Args[i].Name+".iter.tuple.field.ptr"))
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
func (s *functionState) buildEnumerateItemValue(tupleType semantic.Type, indexValue C.LLVMValueRef, itemValue C.LLVMValueRef, itemActualType semantic.Type, name string) (C.LLVMValueRef, error) {
	tuple, ok := semantic.StripAggregateStateType(tupleType).(*semantic.TupleType)
	if !ok || tuple == nil || len(tuple.Fields) != 2 {
		return nil, fmt.Errorf("enumerate loop item requires a 2-field tuple type")
	}
	tupleLLVMType, err := s.g.lowerType(tuple)
	if err != nil {
		return nil, err
	}
	indexCoerced, err := s.coerceValue(indexValue, s.g.result.NamedTypes["usize"], tuple.Fields[0].Type)
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
	value := C.LLVMGetUndef(tupleLLVMType)
	value = C.LLVMBuildInsertValue(s.builder, value, indexCoerced, 0, cStringFree(name+".enumerate.item.index.insert"))
	value = C.LLVMBuildInsertValue(s.builder, value, itemCoerced, 1, cStringFree(name+".enumerate.item.value.insert"))
	return value, nil
}
func (s *functionState) emitIterForStmt(stmt *ast.IterForStmt) error {
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
	C.LLVMBuildStore(s.builder, sourceValue, sourceAlloca)

	iterSourceAlloca := sourceAlloca
	iterSourceType := sourceType
	var enumerateItemType semantic.Type
	if carrierType, ok := semantic.EnumerateViewInstance(sourceType); ok {
		innerSourceType, ok := semantic.EnumerateViewSourceType(carrierType)
		if !ok || innerSourceType == nil {
			return fmt.Errorf("enumerate carrier is missing its source type")
		}
		enumerateItemType, ok = semantic.EnumerateViewItemType(carrierType)
		if !ok || enumerateItemType == nil {
			return fmt.Errorf("enumerate carrier is missing its tuple item type")
		}
		if stmt.Mode != ast.IterBindValue {
			return fmt.Errorf("iterable loop does not support ref binding for %s", sourceType.String())
		}
		iterSourceAlloca, err = s.createEntryAlloca(sourceName+".enumerate.source", innerSourceType)
		if err != nil {
			return err
		}
		innerSourceValue := C.LLVMBuildExtractValue(s.builder, sourceValue, 0, cStringFree(sourceName+".enumerate.source.extract"))
		C.LLVMBuildStore(s.builder, innerSourceValue, iterSourceAlloca)
		iterSourceType = innerSourceType
	}

	iterSourceExpr := stmt.Source
	if enumerateCall, ok := stmt.Source.(*ast.CallExpr); ok && callIdentName(enumerateCall) == "enumerate" && len(enumerateCall.Args) == 1 {
		iterSourceExpr = enumerateCall.Args[0]
	}
	countValue, err := s.emitIterLoopCount(iterSourceExpr, iterSourceAlloca, iterSourceType, sourceName)
	if err != nil {
		return err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	indexAlloca, err := s.createEntryAlloca(sourceName+".index", usizeType)
	if err != nil {
		return err
	}
	zeroValue := C.LLVMConstInt(usizeLLVMType, 0, 0)
	C.LLVMBuildStore(s.builder, zeroValue, indexAlloca)

	condBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.cond"))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.body"))
	stepBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.step"))
	exitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.end"))
	C.LLVMBuildBr(s.builder, condBB)

	C.LLVMPositionBuilderAtEnd(s.builder, condBB)
	indexValue, err := s.loadValue(indexAlloca, usizeType, sourceName+".index")
	if err != nil {
		return err
	}
	condValue := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, countValue, cStringFree("iter.cmp"))
	C.LLVMBuildCondBr(s.builder, condValue, bodyBB, exitBB)

	itemType, ok := iterLoopItemTypeBackend(iterSourceType)
	if !ok && stmt.Mode != ast.IterBindValue {
		return fmt.Errorf("iterable loop ref binding requires an addressable array-like source, got %s", iterSourceType.String())
	}
	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	s.pushScope()
	iterIndexValue := indexValue
	if stmt.Reverse {
		lastIndex := C.LLVMBuildSub(s.builder, countValue, C.LLVMConstInt(usizeLLVMType, 1, 0), cStringFree("iter.rev.last"))
		iterIndexValue = C.LLVMBuildSub(s.builder, lastIndex, indexValue, cStringFree("iter.rev.index"))
	}
	if stmt.Mode == ast.IterBindValue {
		itemValue, resolvedItemType, err := s.emitIterLoopElementValue(iterSourceExpr, iterSourceAlloca, iterSourceType, iterIndexValue, sourceName)
		if err != nil {
			s.popScope()
			return err
		}
		if enumerateItemType != nil {
			itemValue, err = s.buildEnumerateItemValue(enumerateItemType, iterIndexValue, itemValue, resolvedItemType, sourceName)
			if err != nil {
				s.popScope()
				return err
			}
			resolvedItemType = enumerateItemType
			itemType = resolvedItemType
		}
		if itemType == nil {
			itemType = resolvedItemType
		}
		if err := s.emitIterLoopPatternBindings(stmt.Pattern, stmt.Mode, itemType, itemValue, nil); err != nil {
			s.popScope()
			return err
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
		if err := s.emitIterLoopPatternBindings(stmt.Pattern, stmt.Mode, itemType, nil, itemPtr); err != nil {
			s.popScope()
			return err
		}
	}
	if stmt.Filter != nil {
		filterValue, filterType, err := s.emitExpr(stmt.Filter, nil)
		if err != nil {
			s.popScope()
			return err
		}
		filterBool, err := s.coerceValue(filterValue, filterType, s.g.result.NamedTypes["bool"])
		if err != nil {
			s.popScope()
			return err
		}
		filterBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("iter.filter.body"))
		C.LLVMBuildCondBr(s.builder, filterBool, filterBodyBB, stepBB)
		C.LLVMPositionBuilderAtEnd(s.builder, filterBodyBB)
	}
	if err := s.emitBlock(stmt.Body, true); err != nil {
		s.popScope()
		return err
	}
	s.popScope()
	if !s.currentBlockTerminated() {
		C.LLVMBuildBr(s.builder, stepBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, stepBB)
	if !s.currentBlockTerminated() {
		nextValue := C.LLVMBuildAdd(s.builder, indexValue, C.LLVMConstInt(usizeLLVMType, 1, 0), cStringFree("iter.next"))
		C.LLVMBuildStore(s.builder, nextValue, indexAlloca)
		C.LLVMBuildBr(s.builder, condBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, exitBB)
	return nil
}
func unwrapPackedStoreOriginExpr(expr ast.Expr) ast.Expr {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return unwrapPackedStoreOriginExpr(n.Inner)
	case *ast.CastExpr:
		return unwrapPackedStoreOriginExpr(n.Operand)
	case *ast.MoveExpr:
		return unwrapPackedStoreOriginExpr(n.Operand)
	case *ast.CanExpr:
		return unwrapPackedStoreOriginExpr(n.Expr)
	default:
		return expr
	}
}
func (s *functionState) bindPackedStoreOriginsForExprPath(path string, expr ast.Expr, typ semantic.Type) error {
	if path == "" || expr == nil || typ == nil {
		return nil
	}
	stripped := semantic.StripAggregateStateType(typ)
	if enumType, ok := stripped.(*semantic.EnumType); ok {
		if !enumType.Packed {
			return nil
		}
		origin, ok, err := s.resolvePackedNodeStoreBinding(expr, enumType)
		if err != nil {
			return err
		}
		if ok {
			s.bindPackedEnumStoreOrigin(path, enumType, origin)
		}
		return nil
	}
	switch stripped.(type) {
	case *semantic.StructType, *semantic.GenericInstanceType:
	default:
		return nil
	}
	fields, err := s.g.structLiteralFields(stripped)
	if err != nil {
		return err
	}
	sourceExpr := unwrapPackedStoreOriginExpr(expr)
	if lit, ok := sourceExpr.(*ast.StructLitExpr); ok {
		args := lit.LoweredArgs()
		limit := len(fields)
		if len(args) < limit {
			limit = len(args)
		}
		for i := 0; i < limit; i++ {
			if args[i] == nil {
				continue
			}
			childPath := path + "." + fields[i].Decl.Name
			if err := s.bindPackedStoreOriginsForExprPath(childPath, args[i], fields[i].Type); err != nil {
				return err
			}
		}
		return nil
	}
	for _, field := range fields {
		childPath := path + "." + field.Decl.Name
		childExpr := &ast.FieldExpr{Position: expr.Pos(), Object: expr, Field: field.Decl.Name}
		if err := s.bindPackedStoreOriginsForExprPath(childPath, childExpr, field.Type); err != nil {
			return err
		}
	}
	return nil
}
func (s *functionState) emitInStore(stmt *ast.InStoreStmt) error {
	savedTreeOwner := s.treeAllocOwner
	if owner, ok, err := s.classifyTreeAllocOwnerExpr(stmt.Store); err != nil {
		return err
	} else if ok {
		s.treeAllocOwner = owner
		defer func() {
			s.treeAllocOwner = savedTreeOwner
		}()
		return s.emitBlock(stmt.Body, true)
	}
	storeValue, actualType, err := s.emitExpr(stmt.Store, nil)
	if err != nil {
		return err
	}
	savedStores := s.packedStores
	if treeStore, ok := actualType.(*semantic.TreeStoreType); ok {
		s.treeAllocOwner = treeAllocOwnerBinding{storeValue: storeValue, storeType: treeStore}
		defer func() {
			s.treeAllocOwner = savedTreeOwner
			s.packedStores = savedStores
		}()
		return s.emitBlock(stmt.Body, true)
	}
	storeType, ok := actualType.(*semantic.PackedEnumStoreType)
	if !ok {
		return fmt.Errorf("in-block requires a tree store, packed enum store, perm, an Arena value, or an Arena reference, got %s", actualType.String())
	}
	s.packedStores = s.clonePackedStores()
	if s.packedStores == nil {
		s.packedStores = map[string]packedStoreBinding{}
	}
	s.packedStores[storeType.Enum.Name] = packedStoreBinding{value: storeValue, typ: storeType}
	defer func() {
		s.treeAllocOwner = savedTreeOwner
		s.packedStores = savedStores
	}()
	return s.emitBlock(stmt.Body, true)
}
func (s *functionState) emitPoolStmt(stmt *ast.PoolStmt) error {
	poolType := s.g.result.NamedTypes["ThreadPool"]
	usizeType := s.g.result.NamedTypes["usize"]
	workersValue, _, err := s.emitExpr(stmt.Workers, usizeType)
	if err != nil {
		return err
	}
	poolNewType := &semantic.FuncType{Name: "pool_new", Params: []semantic.Type{usizeType}, Return: poolType}
	poolNew, err := s.g.ensureFunctionDeclared("pool_new", poolNewType)
	if err != nil {
		return err
	}
	poolNewLLVMType, err := s.g.lowerFunctionType(poolNewType)
	if err != nil {
		return err
	}
	poolValue := s.buildCall(poolNewLLVMType, poolNew, []C.LLVMValueRef{workersValue}, "pool.new")
	poolAlloca, err := s.createEntryAlloca(stmt.Name, poolType)
	if err != nil {
		return err
	}
	s.pushScope()
	defer s.popScope()
	s.defineBinding(stmt.Name, valueBinding{ptr: poolAlloca, typ: poolType})
	C.LLVMBuildStore(s.builder, poolValue, poolAlloca)
	pool := scopedCleanupBinding{kind: scopedCleanupThreadPool, name: stmt.Name, ptr: poolAlloca, typ: poolType}
	s.registerScopedCleanup(pool)
	s.poolScopes = append(s.poolScopes, activePoolBinding{name: stmt.Name, ptr: poolAlloca, typ: poolType, workers: workersValue})
	defer func() {
		s.poolScopes = s.poolScopes[:len(s.poolScopes)-1]
	}()
	return s.emitBlockInCurrentScope(stmt.Body)
}
