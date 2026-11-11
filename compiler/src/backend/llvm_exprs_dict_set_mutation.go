//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitBuiltinDictReceiverValue(receiver ast.Expr, receiverType semantic.Type) (llvmIRValueRef, *semantic.DictType, error) {
	dictType, receiverRefType, ok := builtinDictReceiverType(receiverType)
	if !ok || dictType == nil {
		return nil, nil, fmt.Errorf("dict receiver is not a dict")
	}
	if receiverRefType != nil {
		value, _, err := s.emitExpr(receiver, receiverRefType)
		return value, dictType, err
	}
	ptr, _, err := s.emitAddress(receiver)
	return ptr, dictType, err
}
func (s *functionState) emitBuiltinDictEntryValue(entryExpr ast.Expr, entryType *semantic.DictEntryType, receiverRefType *semantic.RefType) (llvmIRValueRef, error) {
	if receiverRefType != nil {
		entryPtr, _, err := s.emitExpr(entryExpr, receiverRefType)
		if err != nil {
			return nil, err
		}
		entryLLVMType, err := s.g.lowerType(entryType)
		if err != nil {
			return nil, err
		}
		return llvmBuildLoad2(s.builder, entryLLVMType, entryPtr, cStringFree("dict.entry.load")), nil
	}
	entryValue, _, err := s.emitExpr(entryExpr, entryType)
	return entryValue, err
}
func (s *functionState) emitBuiltinDictEntryCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "entry" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	resultType, ok := s.exprType(expr).(*semantic.DictEntryType)
	if !ok || resultType == nil || resultType.Dict == nil {
		return nil, nil, true, fmt.Errorf("dict entry call missing semantic entry type")
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("dict entry expects 1 argument, got %d", len(expr.Args))
	}
	dictValue, dictType, err := s.emitBuiltinDictReceiverValue(fieldExpr.Object, s.exprType(fieldExpr.Object))
	if err != nil {
		return nil, nil, true, err
	}
	keyValue, _, err := s.emitExpr(expr.Args[0], dictType.Key)
	if err != nil {
		return nil, nil, true, err
	}
	getHelper := "arena_dict_get"
	if resultType.Mutable {
		getHelper = "arena_dict_get_mut"
	}
	getCallee, getType, err := s.ensureRuntimeFunction(getHelper, map[string]semantic.Type{"K": dictType.Key, "T": dictType.Value})
	if err != nil {
		return nil, nil, true, err
	}
	getLLVMType, err := s.g.lowerFunctionType(getType)
	if err != nil {
		return nil, nil, true, err
	}
	valuePtr := s.buildCall(getLLVMType, getCallee, []llvmIRValueRef{dictValue, keyValue}, "dict.entry.get")
	entryLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	entryValue := llvmGetUndef(entryLLVMType)
	entryValue = llvmBuildInsertValue(s.builder, entryValue, dictValue, 0, cStringFree("dict.entry.dict"))
	entryValue = llvmBuildInsertValue(s.builder, entryValue, keyValue, 1, cStringFree("dict.entry.key"))
	entryValue = llvmBuildInsertValue(s.builder, entryValue, valuePtr, 2, cStringFree("dict.entry.value"))
	return entryValue, resultType, true, nil
}
func (s *functionState) emitBuiltinDictEntryInsertCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "insert" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	entryType, receiverRefType, ok := builtinDictEntryReceiverType(s.exprType(fieldExpr.Object))
	if !ok || entryType == nil || entryType.Dict == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("dict entry insert expects 1 argument, got %d", len(expr.Args))
	}
	// Region-parameterized dict: prefer the arena of the dict's own region (if
	// live in s.regions) over the ambient scope, mirroring darray push. Today a
	// dict's region == its ambient scope, so this is behavior-preserving; it
	// becomes load-bearing once insert happens through a region-param dict.
	owner, ok := s.regionArenaOwner(entryType.Dict.Region)
	if !ok {
		owner, ok = s.lookupTreeAllocOwner()
	}
	if !ok {
		owner, ok, _ = s.permGrowthOwner(expr)
	}
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("%s", semantic.NoGrowthRegionMessage("dict entry insert"))
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "dict.entry.insert.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	var entryPtr llvmIRValueRef
	var err error
	if receiverRefType != nil {
		entryPtr, _, err = s.emitExpr(fieldExpr.Object, receiverRefType)
	} else {
		entryPtr, _, err = s.emitAddress(fieldExpr.Object)
		if err != nil {
			entryPtr = nil
			err = nil
		}
	}
	entryLLVMType, err := s.g.lowerType(entryType)
	if err != nil {
		return nil, nil, true, err
	}
	dictRefType := &semantic.RefType{Elem: entryType.Dict, Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	dictRefLLVMType, err := s.g.lowerType(dictRefType)
	if err != nil {
		return nil, nil, true, err
	}
	keyLLVMType, err := s.g.lowerType(entryType.Dict.Key)
	if err != nil {
		return nil, nil, true, err
	}
	valueRefType := builtinDictEntryValueRefType(entryType.Dict, true)
	valueRefLLVMType, err := s.g.lowerType(valueRefType)
	if err != nil {
		return nil, nil, true, err
	}
	var dictValue, keyValue, cachedValue llvmIRValueRef
	var valuePtrPtr llvmIRValueRef
	if entryPtr != nil {
		dictPtr := llvmBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 0, cStringFree("dict.entry.insert.dict.ptr"))
		keyPtr := llvmBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 1, cStringFree("dict.entry.insert.key.ptr"))
		valuePtrPtr = llvmBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 2, cStringFree("dict.entry.insert.value.ptr"))
		dictValue = llvmBuildLoad2(s.builder, dictRefLLVMType, dictPtr, cStringFree("dict.entry.insert.dict"))
		keyValue = llvmBuildLoad2(s.builder, keyLLVMType, keyPtr, cStringFree("dict.entry.insert.key"))
		cachedValue = llvmBuildLoad2(s.builder, valueRefLLVMType, valuePtrPtr, cStringFree("dict.entry.insert.cached"))
	} else {
		entryValue, err := s.emitBuiltinDictEntryValue(fieldExpr.Object, entryType, receiverRefType)
		if err != nil {
			return nil, nil, true, err
		}
		dictValue = llvmBuildExtractValue(s.builder, entryValue, 0, cStringFree("dict.entry.insert.dict"))
		keyValue = llvmBuildExtractValue(s.builder, entryValue, 1, cStringFree("dict.entry.insert.key"))
		cachedValue = llvmBuildExtractValue(s.builder, entryValue, 2, cStringFree("dict.entry.insert.cached"))
	}
	nullValue := llvmConstNull(valueRefLLVMType)
	hasCached := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntNE), cachedValue, nullValue, cStringFree("dict.entry.insert.has"))
	currentBB := llvmGetInsertBlock(s.builder)
	nonNullBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.insert.nonnull"))
	insertBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.insert.insert"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.insert.merge"))
	llvmBuildCondBr(s.builder, hasCached, nonNullBB, insertBB)
	llvmPositionBuilderAtEnd(s.builder, nonNullBB)
	llvmBuildBr(s.builder, mergeBB)
	nonNullEnd := llvmGetInsertBlock(s.builder)
	llvmPositionBuilderAtEnd(s.builder, insertBB)
	insertedArg, _, err := s.emitExpr(expr.Args[0], entryType.Dict.Value)
	if err != nil {
		return nil, nil, true, err
	}
	putCallee, putType, err := s.ensureRuntimeFunction(s.dictMutationHelperName("arena_dict_put"), map[string]semantic.Type{"K": entryType.Dict.Key, "T": entryType.Dict.Value})
	if err != nil {
		return nil, nil, true, err
	}
	putLLVMType, err := s.g.lowerFunctionType(putType)
	if err != nil {
		return nil, nil, true, err
	}
	insertedValue := s.buildCall(putLLVMType, putCallee, []llvmIRValueRef{owner.arenaRef, dictValue, keyValue, insertedArg}, "dict.entry.insert.result")
	if valuePtrPtr != nil {
		llvmBuildStore(s.builder, insertedValue, valuePtrPtr)
	}
	llvmBuildBr(s.builder, mergeBB)
	insertEnd := llvmGetInsertBlock(s.builder)
	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	phi := llvmBuildPhi(s.builder, valueRefLLVMType, cStringFree("dict.entry.insert.phi"))
	llvmAddIncoming(phi, llvmValueSlicePtr([]llvmIRValueRef{cachedValue, insertedValue}), llvmBlockSlicePtr([]llvmIRBlockRef{nonNullEnd, insertEnd}), 2)
	_ = currentBB
	return phi, valueRefType, true, nil
}

func (s *functionState) emitBuiltinDictRegionMutationCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	method := fieldExpr.Field
	if method != "put" && method != "get_or_insert" && method != "reserve" {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	dictType, _, ok := builtinDictReceiverType(receiverType)
	if !ok || dictType == nil {
		return nil, nil, false, nil
	}
	expectedArgs := 2
	if method == "reserve" {
		expectedArgs = 1
	}
	if len(expr.Args) != expectedArgs {
		return nil, nil, true, fmt.Errorf("dict %s expects %d arguments, got %d", method, expectedArgs, len(expr.Args))
	}
	owner, ok := s.regionArenaOwner(dictType.Region)
	if !ok {
		owner, ok = s.lookupTreeAllocOwner()
	}
	if !ok {
		owner, ok, _ = s.permGrowthOwner(expr)
	}
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("%s", semantic.NoGrowthRegionMessage(fmt.Sprintf("dict %s", method)))
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "dict."+method+".owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	dictValue, dictType, err := s.emitBuiltinDictReceiverValue(fieldExpr.Object, receiverType)
	if err != nil {
		return nil, nil, true, err
	}
	if method == "reserve" {
		helperBase := "arena_dict_reserve"
		callee, helperType, err := s.ensureRuntimeFunction(s.dictMutationHelperName(helperBase), map[string]semantic.Type{"K": dictType.Key, "T": dictType.Value})
		if err != nil {
			return nil, nil, true, err
		}
		llvmType, err := s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeType := s.g.namedType("usize")
		minCapacity, _, err := s.emitExpr(expr.Args[0], usizeType)
		if err != nil {
			return nil, nil, true, err
		}
		value := s.buildCall(llvmType, callee, []llvmIRValueRef{owner.arenaRef, dictValue, minCapacity}, "")
		return value, s.g.namedType("void"), true, nil
	}
	keyValue, _, err := s.emitExpr(expr.Args[0], dictType.Key)
	if err != nil {
		return nil, nil, true, err
	}
	insertedArg, _, err := s.emitExpr(expr.Args[1], dictType.Value)
	if err != nil {
		return nil, nil, true, err
	}
	helperBase := "arena_dict_put"
	resultName := "dict.put.result"
	if method == "get_or_insert" {
		helperBase = "arena_dict_get_or_insert"
		resultName = "dict.get_or_insert.result"
	}
	callee, helperType, err := s.ensureRuntimeFunction(s.dictMutationHelperName(helperBase), map[string]semantic.Type{"K": dictType.Key, "T": dictType.Value})
	if err != nil {
		return nil, nil, true, err
	}
	llvmType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, true, err
	}
	value := s.buildCall(llvmType, callee, []llvmIRValueRef{owner.arenaRef, dictValue, keyValue, insertedArg}, resultName)
	return value, builtinDictEntryValueRefType(dictType, true), true, nil
}

func (s *functionState) emitBuiltinSetReceiverValue(receiver ast.Expr, receiverType semantic.Type) (llvmIRValueRef, *semantic.SetType, error) {
	setType, receiverRefType, ok := builtinSetReceiverType(receiverType)
	if !ok || setType == nil {
		return nil, nil, fmt.Errorf("set receiver is not a set")
	}
	if receiverRefType != nil {
		value, _, err := s.emitExpr(receiver, receiverRefType)
		return value, setType, err
	}
	ptr, _, err := s.emitAddress(receiver)
	return ptr, setType, err
}

func (s *functionState) emitBuiltinSetRegionMutationCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	method := fieldExpr.Field
	if method != "add" && method != "insert" && method != "reserve" {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	setType, _, ok := builtinSetReceiverType(receiverType)
	if !ok || setType == nil {
		return nil, nil, false, nil
	}
	expectedArgs := 1
	if len(expr.Args) != expectedArgs {
		return nil, nil, true, fmt.Errorf("set %s expects %d arguments, got %d", method, expectedArgs, len(expr.Args))
	}
	owner, ok := s.regionArenaOwner(setType.Region)
	if !ok {
		owner, ok = s.lookupTreeAllocOwner()
	}
	if !ok {
		owner, ok, _ = s.permGrowthOwner(expr)
	}
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("%s", semantic.NoGrowthRegionMessage(fmt.Sprintf("set %s", method)))
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "set."+method+".owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	setValue, setType, err := s.emitBuiltinSetReceiverValue(fieldExpr.Object, receiverType)
	if err != nil {
		return nil, nil, true, err
	}
	if method == "reserve" {
		callee, helperType, err := s.ensureRuntimeFunction(s.dictMutationHelperName("arena_set_reserve"), map[string]semantic.Type{"T": setType.Elem})
		if err != nil {
			return nil, nil, true, err
		}
		llvmType, err := s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeType := s.g.namedType("usize")
		minCapacity, _, err := s.emitExpr(expr.Args[0], usizeType)
		if err != nil {
			return nil, nil, true, err
		}
		value := s.buildCall(llvmType, callee, []llvmIRValueRef{owner.arenaRef, setValue, minCapacity}, "")
		return value, s.g.namedType("void"), true, nil
	}
	elemValue, _, err := s.emitExpr(expr.Args[0], setType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	callee, helperType, err := s.ensureRuntimeFunction(s.dictMutationHelperName("arena_set_add"), map[string]semantic.Type{"T": setType.Elem})
	if err != nil {
		return nil, nil, true, err
	}
	llvmType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, true, err
	}
	value := s.buildCall(llvmType, callee, []llvmIRValueRef{owner.arenaRef, setValue, elemValue}, "set.add.result")
	return value, s.g.namedType("bool"), true, nil
}
