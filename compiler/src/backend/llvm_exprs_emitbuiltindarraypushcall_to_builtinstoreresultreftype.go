//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitBuiltinDArrayPushCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "push" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("darray push expects 1 argument, got %d", len(expr.Args))
	}
	if s.darrayPushUsesBulkAppend(darrayType, expr.Args[0]) {
		extendExpr := &ast.CallExpr{
			Position: expr.Position,
			Func: &ast.FieldExpr{
				Position: fieldExpr.Position,
				Object:   fieldExpr.Object,
				Field:    "extend",
			},
			Args: expr.Args,
		}
		if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnExprTypesMap() {
			{
				key, value := extendExpr.Func, s.exprType(expr.Func)
				s.g.setResultExprType(key, value)
			}
			{
				key, value := extendExpr, s.exprType(expr)
				s.g.setResultExprType(key, value)
			}
		}
		return s.emitBuiltinDArrayExtendCall(extendExpr)
	}
	// Region-parameterized containers: prefer the arena of the darray's own
	// region (if that region is live in s.regions) over the ambient scope. In
	// today's code a container's region == its ambient scope, so this resolves
	// to the same arena (behavior-preserving); it becomes load-bearing once a
	// helper pushes through a borrow whose region differs from the ambient one.
	owner, ok := s.darrayGrowthOwner(expr, fieldExpr.Object, darrayType)
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("%s", semantic.NoGrowthRegionMessage("darray push"))
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "darray.push.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	itemValue, _, err := s.emitExpr(expr.Args[0], darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	currentCount := llvmBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.push.count"))
	neededValue, err := s.emitCheckedUSizeAdd(currentCount, llvmConstInt(usizeLLVMType, 1, 0), "darray.push.needed")
	if err != nil {
		return nil, nil, true, err
	}
	if err := s.emitBuiltinDArrayEnsureCapacity(darrayPtr, darrayType, owner.arenaRef, neededValue, "darray.push"); err != nil {
		return nil, nil, true, err
	}
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	voidPtrType := llvmPointerTypeInContext(s.g.context, 0)
	itemsValue := llvmBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("darray.push.items"))
	elemLLVMType, err := s.g.lowerType(darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	slotPtr := llvmBuildGEP2(s.builder, elemLLVMType, itemsValue, llvmValueSlicePtr([]llvmIRValueRef{currentCount}), 1, cStringFree("darray.push.slot"))
	llvmBuildStore(s.builder, itemValue, slotPtr)
	llvmBuildStore(s.builder, neededValue, countPtr)
	return darrayPtr, resultType, true, nil
}

// emitBuiltinDArrayPopCall lowers `d.pop()` directly for every darray element type.
// Pop is value-producing, so leaving it to the generic `darray_pop[T]` body makes the
// compiler instantiate an aggregate-indexing helper while it is compiling itself.  That
// path used to emit the count decrement followed by an unconditional trap for aggregate
// elements (sview, AST nodes, and compiler tables), which made the stage1 compiler trap in
// its own semantic pass.  Keep the operation beside push/resize so the aggregate load uses
// the same typed GEP path as ordinary darray indexing.
func (s *functionState) emitBuiltinDArrayPopCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "pop" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 0 {
		return nil, nil, true, fmt.Errorf("darray pop expects no arguments, got %d", len(expr.Args))
	}
	darrayPtr, _, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	count := llvmBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.pop.count"))
	zero := llvmConstInt(usizeLLVMType, 0, 0)
	nonEmpty := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntUGT), count, zero, cStringFree("darray.pop.nonempty"))
	okBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.pop.ok"))
	failBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.pop.empty"))
	llvmBuildCondBr(s.builder, nonEmpty, okBB, failBB)
	llvmPositionBuilderAtEnd(s.builder, failBB)
	if err := s.emitTrapUnreachable("darray.pop.empty"); err != nil {
		return nil, nil, true, err
	}
	llvmPositionBuilderAtEnd(s.builder, okBB)
	newCount := llvmBuildSub(s.builder, count, llvmConstInt(usizeLLVMType, 1, 0), cStringFree("darray.pop.newcount"))
	llvmBuildStore(s.builder, newCount, countPtr)
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	voidPtrType := llvmPointerTypeInContext(s.g.context, 0)
	itemsValue := llvmBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("darray.pop.items"))
	elemLLVMType, err := s.g.lowerType(darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	elemPtr := llvmBuildGEP2(s.builder, elemLLVMType, itemsValue, llvmValueSlicePtr([]llvmIRValueRef{newCount}), 1, cStringFree("darray.pop.element"))
	value := llvmBuildLoad2(s.builder, elemLLVMType, elemPtr, cStringFree("darray.pop.value"))
	return value, darrayType.Elem, true, nil
}

func (s *functionState) darrayPushUsesBulkAppend(darrayType *semantic.DArrayType, arg ast.Expr) bool {
	if s == nil || darrayType == nil || arg == nil {
		return false
	}
	sourceType := s.exprType(arg)
	if sourceType == nil || semantic.SameType(darrayType.Elem, sourceType) {
		return false
	}
	baseType, ok := builtinDArrayExtendSourceType(sourceType)
	if !ok || baseType == nil {
		return false
	}
	switch tt := baseType.(type) {
	case *semantic.DArrayType:
		return semantic.SameType(darrayType.Elem, tt.Elem)
	case *semantic.ViewType:
		return semantic.SameType(darrayType.Elem, tt.Elem)
	case *semantic.ArrayType:
		return semantic.SameType(darrayType.Elem, tt.Elem)
	default:
		return false
	}
}

func (s *functionState) emitBuiltinDArrayExtendCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "extend" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("darray extend expects 1 argument, got %d", len(expr.Args))
	}
	// Fused fast path: `dst.extend([ v for x in src ])` appends the comprehension DIRECTLY into
	// `dst` (presize once + vectorizable indexed-store fill), skipping the materialize-a-temp +
	// memcpy the general path below would do. resize/index-store resolve `dst`'s arena themselves,
	// so this needs no owner/source resolution. Falls through to the materialized path when the
	// argument is not a fusable comprehension.
	fused, fusedOK := s.fusedExtendComprehensionBlock(fieldExpr.Object, expr.Args[0], darrayType)
	if !fusedOK {
		fused, fusedOK = s.fusedFilteredExtendComprehensionBlock(fieldExpr.Object, expr.Args[0], darrayType)
	}
	if !fusedOK {
		fused, fusedOK = s.fusedRangeExtendComprehensionBlock(fieldExpr.Object, expr.Args[0], darrayType)
	}
	if !fusedOK {
		fused, fusedOK = s.fusedDictExtendComprehensionBlock(fieldExpr.Object, expr.Args[0], darrayType)
	}
	if !fusedOK {
		fused, fusedOK = s.fusedPushExtendComprehensionBlock(fieldExpr.Object, expr.Args[0], darrayType)
	}
	if fusedOK {
		if _, _, err := s.emitExprBlock(fused, s.g.namedType("usize")); err != nil {
			return nil, nil, true, err
		}
		darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
		if err != nil {
			return nil, nil, true, err
		}
		return darrayPtr, resultType, true, nil
	}
	owner, ok := s.darrayGrowthOwner(expr, fieldExpr.Object, darrayType)
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("%s", semantic.NoGrowthRegionMessage("darray extend"))
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "darray.extend.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	sourceData, sourceCount, err := s.emitBuiltinDArrayExtendSource(expr.Args[0], darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroCount := llvmConstInt(usizeLLVMType, 0, 0)
	isZero := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), sourceCount, zeroCount, cStringFree("darray.extend.count.zero"))
	callBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.extend.call"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.extend.merge"))
	llvmBuildCondBr(s.builder, isZero, mergeBB, callBB)

	llvmPositionBuilderAtEnd(s.builder, callBB)
	countPtr, _, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	currentCount := llvmBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.extend.count"))
	neededValue, err := s.emitCheckedUSizeAdd(currentCount, sourceCount, "darray.extend.needed")
	if err != nil {
		return nil, nil, true, err
	}
	if err := s.emitBuiltinDArrayEnsureCapacity(darrayPtr, darrayType, owner.arenaRef, neededValue, "darray.extend"); err != nil {
		return nil, nil, true, err
	}
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	voidPtrType := llvmPointerTypeInContext(s.g.context, 0)
	itemsValue := llvmBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("darray.extend.items"))
	elemLLVMType, err := s.g.lowerType(darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	dstPtr := llvmBuildGEP2(s.builder, elemLLVMType, itemsValue, llvmValueSlicePtr([]llvmIRValueRef{currentCount}), 1, cStringFree("darray.extend.dst"))
	elemSizeBytes, err := s.sizeOfType(darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	byteCount, err := s.emitCheckedElemByteCount(sourceCount, elemSizeBytes, "darray.extend")
	if err != nil {
		return nil, nil, true, err
	}
	voidRefType := &semantic.RefType{Elem: s.g.namedType("void"), State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, s.g.namedType("usize")}, Return: voidRefType}
	})
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	_ = s.buildCall(memcpyLLVMType, memcpyCallee, []llvmIRValueRef{dstPtr, sourceData, byteCount}, "darray.extend.memcpy")
	llvmBuildStore(s.builder, neededValue, countPtr)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	return darrayPtr, resultType, true, nil
}
func (s *functionState) emitBuiltinDArrayExtendSource(arg ast.Expr, elemType semantic.Type) (llvmIRValueRef, llvmIRValueRef, error) {
	sourceType := s.exprType(arg)
	baseType, ok := builtinDArrayExtendSourceType(sourceType)
	if !ok || baseType == nil {
		return nil, nil, fmt.Errorf("darray extend expects a compatible darray, array, or view source")
	}
	switch tt := baseType.(type) {
	case *semantic.DArrayType:
		var arrayValue llvmIRValueRef
		if refType, ok := sourceType.(*semantic.RefType); ok && refType != nil {
			ptr, _, err := s.emitExpr(arg, sourceType)
			if err != nil {
				return nil, nil, err
			}
			arrayValue, err = s.loadValue(ptr, tt, "darray.extend.src.darray")
			if err != nil {
				return nil, nil, err
			}
		} else {
			var err error
			arrayValue, _, err = s.emitExpr(arg, tt)
			if err != nil {
				return nil, nil, err
			}
		}
		data := llvmBuildExtractValue(s.builder, arrayValue, 0, cStringFree("darray.extend.src.data"))
		count := llvmBuildExtractValue(s.builder, arrayValue, 1, cStringFree("darray.extend.src.count"))
		return data, count, nil
	case *semantic.ViewType:
		var viewValue llvmIRValueRef
		if refType, ok := sourceType.(*semantic.RefType); ok && refType != nil {
			ptr, _, err := s.emitExpr(arg, sourceType)
			if err != nil {
				return nil, nil, err
			}
			viewValue, err = s.loadValue(ptr, tt, "darray.extend.src.view")
			if err != nil {
				return nil, nil, err
			}
		} else {
			var err error
			viewValue, _, err = s.emitExpr(arg, tt)
			if err != nil {
				return nil, nil, err
			}
		}
		data := llvmBuildExtractValue(s.builder, viewValue, 0, cStringFree("darray.extend.src.data"))
		count := llvmBuildExtractValue(s.builder, viewValue, 1, cStringFree("darray.extend.src.len"))
		return data, count, nil
	case *semantic.SViewType:
		// sview is the same {data, len} fat pointer as a view[u8] — extract its bytes and
		// feed the memcpy path (identical to the ViewType case above).
		var sviewValue llvmIRValueRef
		if refType, ok := sourceType.(*semantic.RefType); ok && refType != nil {
			ptr, _, err := s.emitExpr(arg, sourceType)
			if err != nil {
				return nil, nil, err
			}
			sviewValue, err = s.loadValue(ptr, tt, "darray.extend.src.sview")
			if err != nil {
				return nil, nil, err
			}
		} else {
			var err error
			sviewValue, _, err = s.emitExpr(arg, tt)
			if err != nil {
				return nil, nil, err
			}
		}
		data := llvmBuildExtractValue(s.builder, sviewValue, 0, cStringFree("darray.extend.src.data"))
		count := llvmBuildExtractValue(s.builder, sviewValue, 1, cStringFree("darray.extend.src.len"))
		return data, count, nil
	case *semantic.ArrayType:
		arrayType, arrayPtr, ok, err := s.fixedArraySliceBase(arg)
		if err != nil {
			return nil, nil, err
		}
		if !ok || arrayType == nil {
			return nil, nil, fmt.Errorf("darray extend could not materialize fixed array source")
		}
		usizeType := s.g.namedType("usize")
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, nil, err
		}
		count := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(arrayType.ConstSize), 0)
		elemRefType := &semantic.RefType{Elem: elemType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		elemRefLLVMType, err := s.g.lowerType(elemRefType)
		if err != nil {
			return nil, nil, err
		}
		if arrayType.ConstSize == 0 {
			return llvmConstNull(elemRefLLVMType), count, nil
		}
		arrayLLVMType, err := s.g.lowerType(arrayType)
		if err != nil {
			return nil, nil, err
		}
		zeroIndex := llvmConstInt(usizeLLVMType, 0, 0)
		indices := []llvmIRValueRef{zeroIndex, zeroIndex}
		data := llvmBuildGEP2(s.builder, arrayLLVMType, arrayPtr, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree("darray.extend.src.array.data"))
		return data, count, nil
	default:
		return nil, nil, fmt.Errorf("unsupported darray extend source %T", baseType)
	}
}
