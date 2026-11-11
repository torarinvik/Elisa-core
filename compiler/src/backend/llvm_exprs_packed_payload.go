//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

// emitDeepAdoptElementContainers, for a darray payload whose element is a STRUCT with owned
// container (darray) fields, copies each such field's backing into the store arena too — the
// recursive counterpart of the shallow memcpy in emitPackedEnumPayloadDArrayAdopt. Without it a
// nested field like `MatchArm.body: darray[Stmt]` keeps pointing at the builder's freed region
// (the self-host next_token use-after-free). sview/view/handle fields are borrows and are skipped.
// `base` points at the freshly-copied element array (count elements) in the store arena.
func (s *functionState) emitDeepAdoptElementContainers(base, count llvmIRValueRef, elemType semantic.Type, arenaValue llvmIRValueRef, allocLLVMType llvmIRTypeRef, allocCallee llvmIRValueRef) error {
	st, ok := elemType.(*semantic.StructType)
	if !ok || st == nil || st.Decl == nil {
		return nil
	}
	type ownedField struct {
		index    int
		darrayLL llvmIRTypeRef
		elemSize uint64
		nested   semantic.Type
	}
	var owned []ownedField
	for i, fd := range st.Decl.Fields {
		f, present := st.Fields[fd.Name]
		if !present {
			continue
		}
		dt, isD := f.Type.(*semantic.DArrayType)
		if !isD || dt == nil {
			continue
		}
		fElemSize, err := s.sizeOfType(dt.Elem)
		if err != nil {
			return err
		}
		fLL, err := s.g.lowerType(f.Type)
		if err != nil {
			return err
		}
		owned = append(owned, ownedField{index: i, darrayLL: fLL, elemSize: fElemSize, nested: dt.Elem})
	}
	if len(owned) == 0 {
		return nil
	}
	elemLLVMType, err := s.g.lowerType(st)
	if err != nil {
		return err
	}
	usizeType := s.g.namedType("usize")
	usizeLL, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	ptrLL := llvmPointerTypeInContext(s.g.context, 0)
	iPtr := llvmBuildAlloca(s.builder, usizeLL, cStringFree("adopt.deep.i"))
	llvmBuildStore(s.builder, llvmConstInt(usizeLL, 0, 0), iPtr)
	headerBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("adopt.deep.header"))
	bodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("adopt.deep.body"))
	exitBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("adopt.deep.exit"))
	llvmBuildBr(s.builder, headerBB)
	llvmPositionBuilderAtEnd(s.builder, headerBB)
	iVal := llvmBuildLoad2(s.builder, usizeLL, iPtr, cStringFree("adopt.deep.i.load"))
	cond := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), iVal, count, cStringFree("adopt.deep.cond"))
	llvmBuildCondBr(s.builder, cond, bodyBB, exitBB)
	llvmPositionBuilderAtEnd(s.builder, bodyBB)
	elemIndices := []llvmIRValueRef{iVal}
	elemPtr := llvmBuildGEP2(s.builder, elemLLVMType, base, llvmValueSlicePtr(elemIndices), llvmIRUnsigned(len(elemIndices)), cStringFree("adopt.deep.elem"))
	for _, of := range owned {
		fieldPtr := llvmBuildStructGEP2(s.builder, elemLLVMType, elemPtr, llvmIRUnsigned(of.index), cStringFree("adopt.deep.field"))
		dataPtr := llvmBuildStructGEP2(s.builder, of.darrayLL, fieldPtr, 0, cStringFree("adopt.deep.fdata.ptr"))
		countPtr := llvmBuildStructGEP2(s.builder, of.darrayLL, fieldPtr, 1, cStringFree("adopt.deep.fcount.ptr"))
		capPtr := llvmBuildStructGEP2(s.builder, of.darrayLL, fieldPtr, 2, cStringFree("adopt.deep.fcap.ptr"))
		fdata := llvmBuildLoad2(s.builder, ptrLL, dataPtr, cStringFree("adopt.deep.fdata"))
		fcount := llvmBuildLoad2(s.builder, usizeLL, countPtr, cStringFree("adopt.deep.fcount"))
		fbytes, err := s.emitCheckedElemByteCount(fcount, of.elemSize, "adopt.deep.field")
		if err != nil {
			return err
		}
		fnew := s.buildCall(allocLLVMType, allocCallee, []llvmIRValueRef{arenaValue, fbytes}, "adopt.deep.falloc")
		if err := s.emitPackedEnumTailMemcpy(fnew, fdata, fbytes); err != nil {
			return err
		}
		llvmBuildStore(s.builder, fnew, dataPtr)
		llvmBuildStore(s.builder, fcount, capPtr)
		// Recurse: a field whose elements are themselves structs with owned containers.
		if err := s.emitDeepAdoptElementContainers(fnew, fcount, of.nested, arenaValue, allocLLVMType, allocCallee); err != nil {
			return err
		}
	}
	iNext := llvmBuildAdd(s.builder, iVal, llvmConstInt(usizeLL, 1, 0), cStringFree("adopt.deep.i.next"))
	llvmBuildStore(s.builder, iNext, iPtr)
	llvmBuildBr(s.builder, headerBB)
	llvmPositionBuilderAtEnd(s.builder, exitBB)
	return nil
}

func (s *functionState) emitPackedEnumTailPayloadValue(plan *packedEnumTailPayloadPlan, tailDataPtr llvmIRValueRef) (llvmIRValueRef, error) {
	if plan == nil || plan.viewType == nil {
		return nil, fmt.Errorf("missing packed enum tail payload plan")
	}
	viewLLVMType, err := s.g.lowerType(plan.viewType)
	if err != nil {
		return nil, err
	}
	if plan.literal != nil {
		elemLLVMType, err := s.g.lowerType(plan.viewType.Elem)
		if err != nil {
			return nil, err
		}
		usizeType := s.g.namedType("usize")
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, err
		}
		for i, elem := range plan.literal.Elems {
			elemValue, _, err := s.emitExpr(elem, plan.viewType.Elem)
			if err != nil {
				return nil, err
			}
			indexValue := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(i), 0)
			indices := []llvmIRValueRef{indexValue}
			elemPtr := llvmBuildGEP2(s.builder, elemLLVMType, tailDataPtr, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree("packed.tail.elem.ptr"))
			llvmBuildStore(s.builder, elemValue, elemPtr)
		}
	} else {
		if err := s.emitPackedEnumTailMemcpy(tailDataPtr, plan.sourceData, plan.byteCount); err != nil {
			return nil, err
		}
	}
	viewValue := llvmGetUndef(viewLLVMType)
	viewValue = llvmBuildInsertValue(s.builder, viewValue, tailDataPtr, 0, cStringFree("packed.tail.view.data"))
	viewValue = llvmBuildInsertValue(s.builder, viewValue, plan.lenValue, 1, cStringFree("packed.tail.view.len"))
	return viewValue, nil
}
func (s *functionState) emitPackedEnumTailMemcpy(dstData llvmIRValueRef, srcData llvmIRValueRef, byteCount llvmIRValueRef) error {
	voidType := s.g.namedType("void")
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	usizeType := s.g.namedType("usize")
	memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	})
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return err
	}
	memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []llvmIRValueRef{dstData, srcData, byteCount}, "packed.tail.memcpy")
	s.addCallSiteEnumAttribute(memcpyCall, llvmIRUnsigned(1), "noalias")
	s.addCallSiteEnumAttribute(memcpyCall, llvmIRUnsigned(2), "noalias")
	return nil
}
func (s *functionState) emitPackedEnumStorageAlloc(storeValue llvmIRValueRef, enumType *semantic.EnumType, tailPlan *packedEnumTailPayloadPlan, fixedTagValue llvmIRValueRef) (llvmIRValueRef, llvmIRValueRef, llvmIRValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, nil, nil, fmt.Errorf("missing packed enum storage metadata")
	}
	storeType := enumType.StoreType
	if storeType == nil {
		return nil, nil, nil, fmt.Errorf("packed enum %s is missing store metadata", enumType.Name)
	}
	ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}
	rowSizeValue, err := ops.rowBytesValue("packed.alloc.store")
	if err != nil {
		return nil, nil, nil, err
	}
	totalSizeValue := rowSizeValue
	if tailPlan != nil {
		totalSizeValue = llvmBuildAdd(s.builder, rowSizeValue, tailPlan.byteCount, cStringFree("packed.alloc.bytes"))
	}
	return ops.allocateStorage(enumType, totalSizeValue, tailPlan != nil, fixedTagValue, "packed.alloc.store")
}
func (s *functionState) resolveEnumConstructorArgs(callExpr *ast.CallExpr, enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) ([]ast.Expr, error) {
	if variant == nil {
		return nil, fmt.Errorf("missing enum constructor metadata")
	}
	if callExpr != nil && callExpr.ResolvedArgsValid && len(callExpr.ResolvedArgs) == len(variant.Payload) && callExpr.ResolvedCommonArgs == nil {
		return callExpr.ResolvedArgs, nil
	}
	namedCount := 0
	for _, name := range argNames {
		if name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		if callExpr != nil {
			callExpr.ResolvedArgsValid = true
			callExpr.ResolvedArgs = args
			callExpr.ResolvedCommonArgs = nil
		}
		return args, nil
	}
	if namedCount != len(args) {
		return nil, fmt.Errorf("enum constructor %s.%s cannot mix positional and named arguments", enumType.Name, variant.Name)
	}
	if !variant.HasNamedPayloads() {
		return nil, fmt.Errorf("enum constructor %s.%s does not declare named payload fields", enumType.Name, variant.Name)
	}
	ordered := make([]ast.Expr, len(variant.Payload))
	seen := make([]bool, len(variant.Payload))
	for i, arg := range args {
		name := ""
		if i < len(argNames) {
			name = argNames[i]
		}
		index, ok := variant.PayloadIndex(name)
		if !ok {
			return nil, fmt.Errorf("enum constructor %s.%s has no payload field %q", enumType.Name, variant.Name, name)
		}
		if seen[index] {
			return nil, fmt.Errorf("enum constructor %s.%s payload field %q is specified more than once", enumType.Name, variant.Name, name)
		}
		ordered[index] = arg
		seen[index] = true
	}
	for i, wasSeen := range seen {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label == "" {
				return nil, fmt.Errorf("enum constructor %s.%s is missing argument %d", enumType.Name, variant.Name, i+1)
			}
			return nil, fmt.Errorf("enum constructor %s.%s is missing payload field %q", enumType.Name, variant.Name, label)
		}
	}
	if callExpr != nil {
		callExpr.ResolvedArgsValid = true
		callExpr.ResolvedArgs = ordered
		callExpr.ResolvedCommonArgs = nil
	}
	return ordered, nil
}
func (s *functionState) resolvePackedEnumConstructorArgs(callExpr *ast.CallExpr, enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) ([]ast.Expr, map[string]ast.Expr, error) {
	if enumType == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing packed enum constructor metadata")
	}
	if callExpr != nil && callExpr.ResolvedArgsValid && len(callExpr.ResolvedArgs) == len(variant.Payload) {
		return callExpr.ResolvedArgs, callExpr.ResolvedCommonArgs, nil
	}
	namedCount := 0
	for _, name := range argNames {
		if name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		if callExpr != nil {
			callExpr.ResolvedArgsValid = true
			callExpr.ResolvedArgs = args
			callExpr.ResolvedCommonArgs = nil
		}
		return args, nil, nil
	}
	if namedCount != len(args) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s cannot mix positional and named arguments", enumType.Name, variant.Name)
	}
	ordered := make([]ast.Expr, len(variant.Payload))
	seenPayload := make([]bool, len(variant.Payload))
	commonArgs := make(map[string]ast.Expr)
	for i, arg := range args {
		name := ""
		if i < len(argNames) {
			name = argNames[i]
		}
		if index, ok := variant.PayloadIndex(name); ok {
			if seenPayload[index] {
				return nil, nil, fmt.Errorf("enum constructor %s.%s payload field %q is specified more than once", enumType.Name, variant.Name, name)
			}
			ordered[index] = arg
			seenPayload[index] = true
			continue
		}
		if _, ok := enumType.Common[name]; ok {
			if _, exists := commonArgs[name]; exists {
				return nil, nil, fmt.Errorf("packed enum constructor %s.%s common field %q is specified more than once", enumType.Name, variant.Name, name)
			}
			commonArgs[name] = arg
			continue
		}
		return nil, nil, fmt.Errorf("packed enum constructor %s.%s has no payload or common field %q", enumType.Name, variant.Name, name)
	}
	for i, wasSeen := range seenPayload {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label == "" {
				return nil, nil, fmt.Errorf("enum constructor %s.%s is missing argument %d", enumType.Name, variant.Name, i+1)
			}
			return nil, nil, fmt.Errorf("enum constructor %s.%s is missing payload field %q", enumType.Name, variant.Name, label)
		}
	}
	if callExpr != nil {
		callExpr.ResolvedArgsValid = true
		callExpr.ResolvedArgs = ordered
		callExpr.ResolvedCommonArgs = commonArgs
	}
	return ordered, commonArgs, nil
}
