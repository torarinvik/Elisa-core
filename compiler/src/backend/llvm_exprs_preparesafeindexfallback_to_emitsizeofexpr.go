//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) prepareSafeIndexFallback(expr *ast.IndexExpr, indexValue llvmIRValueRef) (llvmIRValueRef, func() (llvmIRValueRef, semantic.Type, error), error) {
	objectType := s.exprType(expr.Object)
	if objectType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for safe index operand")
	}
	if storeType, ok := packedStoreOperandType(objectType); ok {
		storeValue, _, err := s.emitPackedStoreValueFromExpr(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		countValue, err := s.emitPackedStoreCountValue(storeValue, storeType, "safe.index.count")
		if err != nil {
			return nil, nil, err
		}
		ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}
		return countValue, func() (llvmIRValueRef, semantic.Type, error) {
			return ops.storeValueAt(indexValue, "safe.index.value")
		}, nil
	}
	switch t := objectType.(type) {
	case *semantic.ArrayType:
		arrayPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		countValue, err := s.safeIndexArrayCountValue(t)
		if err != nil {
			return nil, nil, err
		}
		return countValue, func() (llvmIRValueRef, semantic.Type, error) {
			return s.loadArrayIndexValue(arrayPtr, t, indexValue, "safe.index.value")
		}, nil
	case *semantic.DArrayType:
		containerPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		countValue, err := s.emitContainerCountValue(containerPtr, t, "safe.index.count")
		if err != nil {
			return nil, nil, err
		}
		return countValue, func() (llvmIRValueRef, semantic.Type, error) {
			ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, t, t.Elem, indexValue)
			if err != nil {
				return nil, nil, err
			}
			value, err := s.loadValue(ptr, elemType, "safe.index.value")
			return value, elemType, err
		}, nil
	case *semantic.ViewType:
		containerPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		countValue, err := s.emitContainerCountValue(containerPtr, t, "safe.index.count")
		if err != nil {
			return nil, nil, err
		}
		return countValue, func() (llvmIRValueRef, semantic.Type, error) {
			ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, t, t.Elem, indexValue)
			if err != nil {
				return nil, nil, err
			}
			value, err := s.loadValue(ptr, elemType, "safe.index.value")
			return value, elemType, err
		}, nil
	case *semantic.RefType:
		switch elem := t.Elem.(type) {
		case *semantic.ArrayType:
			arrayPtr, _, err := s.emitExpr(expr.Object, objectType)
			if err != nil {
				return nil, nil, err
			}
			countValue, err := s.safeIndexArrayCountValue(elem)
			if err != nil {
				return nil, nil, err
			}
			return countValue, func() (llvmIRValueRef, semantic.Type, error) {
				return s.loadArrayIndexValue(arrayPtr, elem, indexValue, "safe.index.value")
			}, nil
		case *semantic.DArrayType:
			containerPtr, _, err := s.emitExpr(expr.Object, objectType)
			if err != nil {
				return nil, nil, err
			}
			countValue, err := s.emitContainerCountValue(containerPtr, elem, "safe.index.count")
			if err != nil {
				return nil, nil, err
			}
			return countValue, func() (llvmIRValueRef, semantic.Type, error) {
				ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, elem, elem.Elem, indexValue)
				if err != nil {
					return nil, nil, err
				}
				value, err := s.loadValue(ptr, elemType, "safe.index.value")
				return value, elemType, err
			}, nil
		case *semantic.ViewType:
			containerPtr, _, err := s.emitExpr(expr.Object, objectType)
			if err != nil {
				return nil, nil, err
			}
			countValue, err := s.emitContainerCountValue(containerPtr, elem, "safe.index.count")
			if err != nil {
				return nil, nil, err
			}
			return countValue, func() (llvmIRValueRef, semantic.Type, error) {
				ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, elem, elem.Elem, indexValue)
				if err != nil {
					return nil, nil, err
				}
				value, err := s.loadValue(ptr, elemType, "safe.index.value")
				return value, elemType, err
			}, nil
		}
	}
	return nil, nil, fmt.Errorf("safe index fallback is not implemented for %s", objectType.String())
}
func (s *functionState) safeIndexArrayCountValue(arrayType *semantic.ArrayType) (llvmIRValueRef, error) {
	if arrayType == nil || !arrayType.HasConstSize || arrayType.ConstSize < 0 {
		return nil, fmt.Errorf("safe index fallback requires a concrete array bound")
	}
	usizeLLVMType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	return llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(arrayType.ConstSize), 0), nil
}
func (s *functionState) loadArrayIndexValue(arrayPtr llvmIRValueRef, arrayType *semantic.ArrayType, indexValue llvmIRValueRef, name string) (llvmIRValueRef, semantic.Type, error) {
	arrayLLVMType, err := s.g.lowerType(arrayType)
	if err != nil {
		return nil, nil, err
	}
	zero := llvmConstInt(llvmInt32TypeInContext(s.g.context), 0, 0)
	indices := []llvmIRValueRef{zero, indexValue}
	ptr := llvmBuildGEP2(s.builder, arrayLLVMType, arrayPtr, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree(name+".ptr"))
	value, err := s.loadValue(ptr, arrayType.Elem, name)
	return value, arrayType.Elem, err
}
func (s *functionState) emitContainerCountValue(containerPtr llvmIRValueRef, containerType semantic.Type, name string) (llvmIRValueRef, error) {
	containerLLVMType, err := s.g.lowerType(containerType)
	if err != nil {
		return nil, err
	}
	countPtr := llvmBuildStructGEP2(s.builder, containerLLVMType, containerPtr, 1, cStringFree(name+".ptr"))
	usizeType := s.g.namedType("usize")
	count, err := s.loadValue(countPtr, usizeType, name)
	if err != nil {
		return nil, err
	}
	switch containerType.(type) {
	case *semantic.DArrayType, *semantic.ViewType:
		// The count lives in header memory, exactly like the data pointer: tag it "hdr" so a
		// scalar element store ("elt") never forces a reload of the bound inside a hot loop.
		s.tagDarrayHeaderLoad(count)
	}
	return count, nil
}
func (s *functionState) emitChunksExactIndexExpr(expr *ast.IndexExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	chunkType, ok := semantic.ChunksExactViewItemType(s.exprType(expr.Object))
	if !ok || chunkType == nil {
		return nil, nil, false, nil
	}
	objectType, ok := s.exprType(expr.Object).(*semantic.GenericInstanceType)
	if !ok || objectType == nil {
		return nil, nil, true, fmt.Errorf("chunks_exact index expects a carrier value")
	}
	objectValue, _, err := s.emitExpr(expr.Object, objectType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.namedType("usize")
	indexValue, _, err := s.emitExpr(expr.Index, usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	sourceValue := llvmBuildExtractValue(s.builder, objectValue, 0, cStringFree("chunks.source"))
	chunkSizeValue := llvmBuildExtractValue(s.builder, objectValue, 1, cStringFree("chunks.chunk_size"))
	startValue := llvmBuildMul(s.builder, indexValue, chunkSizeValue, cStringFree("chunks.start"))
	endValue := llvmBuildAdd(s.builder, startValue, chunkSizeValue, cStringFree("chunks.end"))
	value, err := s.emitArenaViewSliceValue(sourceValue, chunkType, startValue, endValue, "chunks.item")
	if err != nil {
		return nil, nil, true, err
	}
	return value, chunkType, true, nil
}
func (s *functionState) emitRuntimeStringIndexExpr(expr *ast.IndexExpr, helperName string, operandType semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	stringValue, _, err := s.emitExpr(expr.Object, operandType)
	if err != nil {
		return nil, nil, err
	}
	indexType := s.g.namedType("i64")
	resultType := s.g.namedType("char")
	indexValue, _, err := s.emitExpr(expr.Index, indexType)
	if err != nil {
		return nil, nil, err
	}
	if _, isView := operandType.(*semantic.SViewType); isView && s.g.wordBits == 64 && s.g.optLevel != OptimizationLevel0 {
		value, err := s.emitInlineStringViewIndex(stringValue, indexValue, resultType)
		return value, resultType, err
	}
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{operandType, indexType},
		Return: resultType,
	}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []llvmIRValueRef{stringValue, indexValue}
	call := s.buildCall(llvmFnType, callee, args, "stridx")
	return call, resultType, nil
}
func semanticStringArrayType(t semantic.Type) (*semantic.ArrayType, bool) {
	if arrayType, ok := t.(*semantic.ArrayType); ok {
		if arrayType.SurfaceName == "str" || arrayType.SurfaceName == "string" {
			return arrayType, true
		}
		return nil, false
	}
	ref, ok := t.(*semantic.RefType)
	if !ok || ref.State != semantic.RefStateNonNull {
		return nil, false
	}
	arrayType, ok := ref.Elem.(*semantic.ArrayType)
	if !ok || (arrayType.SurfaceName != "str" && arrayType.SurfaceName != "string") {
		return nil, false
	}
	return arrayType, true
}
func (s *functionState) emitStaticStringIndexExpr(expr *ast.IndexExpr) (llvmIRValueRef, semantic.Type, error) {
	ptr, _, err := s.emitIndexAddress(expr, false)
	if err != nil {
		return nil, nil, err
	}
	byteType := s.g.namedType("u8")
	loaded, err := s.loadValue(ptr, byteType, "str.byte")
	if err != nil {
		return nil, nil, err
	}
	resultType := s.g.namedType("char")
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	return llvmBuildZExt(s.builder, loaded, llvmResultType, cStringFree("stridx.zext")), resultType, nil
}
func runtimeStringIndexedOperand(t semantic.Type) (string, semantic.Type, bool) {
	if _, ok := t.(*semantic.CStrType); ok {
		return "ctx_string_index", t, true
	}
	if _, ok := t.(*semantic.SViewType); ok {
		return "ctx_string_view_index", t, true
	}
	if st, ok := t.(*semantic.StructType); ok && st.Name == "StringView" {
		return "ctx_string_view_index", t, true
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return "", nil, false
	}
	if ref.State != semantic.RefStateNonNull {
		return "", nil, false
	}
	if _, ok := ref.Elem.(*semantic.CStrType); ok {
		return "ctx_string_index", ref.Elem, true
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return "ctx_string_view_index", ref.Elem, true
	}
	if st, ok := ref.Elem.(*semantic.StructType); ok && st.Name == "StringView" {
		return "ctx_string_view_index", ref.Elem, true
	}
	return "", nil, false
}
func cstrSyntheticFieldType(t semantic.Type, fieldName string) (semantic.Type, bool) {
	if fieldName != "len" {
		return nil, false
	}
	if _, ok := cstrFieldOperandType(t); !ok {
		return nil, false
	}
	return &semantic.BuiltinType{Name: "i64"}, true
}
func cstrFieldOperandType(t semantic.Type) (semantic.Type, bool) {
	if _, ok := t.(*semantic.CStrType); ok {
		return t, true
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return nil, false
	}
	if ref.State != semantic.RefStateNonNull {
		return nil, false
	}
	if _, ok := ref.Elem.(*semantic.CStrType); ok {
		return ref.Elem, true
	}
	return nil, false
}

type runtimeSliceInfo struct {
	helperName  string
	operandType semantic.Type
	resultType  semantic.Type
	indexType   semantic.Type
	useAddress  bool
}

func runtimeSliceOperandInfo(objectType semantic.Type, resultType semantic.Type) (runtimeSliceInfo, bool) {
	i64Type := &semantic.BuiltinType{Name: "i64"}
	usizeType := &semantic.BuiltinType{Name: "usize"}
	if view, ok := objectType.(*semantic.ViewType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view_slice",
			operandType: objectType,
			resultType:  &semantic.ViewType{Elem: view.Elem, SurfaceName: "view"},
			indexType:   usizeType,
		}, true
	}
	if view, ok := objectType.(*semantic.ViewType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view_slice",
			operandType: objectType,
			resultType:  &semantic.ViewType{Elem: view.Elem},
			indexType:   usizeType,
		}, true
	}
	if _, ok := objectType.(*semantic.CStrType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	if _, ok := objectType.(*semantic.SViewType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	if st, ok := objectType.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	ref, ok := objectType.(*semantic.RefType)
	if !ok || ref.State != semantic.RefStateNonNull {
		return runtimeSliceInfo{}, false
	}
	if view, ok := ref.Elem.(*semantic.ViewType); ok {
		return runtimeSliceInfo{helperName: "arena_da_view_slice", operandType: ref.Elem, resultType: &semantic.ViewType{Elem: view.Elem, SurfaceName: "view"}, indexType: usizeType}, true
	}
	if _, ok := ref.Elem.(*semantic.CStrType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	if st, ok := ref.Elem.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	return runtimeSliceInfo{}, false
}

// stringCompareValueOperandType peels an `sview&` (a reference to a view — e.g. the result
// of `dict.get`, which returns `sview&?`) to the value `sview`. The returned type is used
// BOTH as the runtime helper's declared parameter type and (via emitStringCompareValue) as
// the type the operand value is materialized at, so the two always agree. Without this the
// operand keeps its ref type: `ctx_string_views_eq` gets declared once as i64(ptr, ...) and
// elsewhere as i64(StringView, ...), tripping "conflicting LLVM function declaration". A
// `u8&` (raw cstr) operand is intentionally NOT peeled — it is a pointer by design.
func stringCompareValueOperandType(t semantic.Type) semantic.Type {
	if ref, ok := t.(*semantic.RefType); ok {
		if _, ok := ref.Elem.(*semantic.SViewType); ok {
			return ref.Elem
		}
	}
	return t
}

func runtimeStringCompareInfo(leftType semantic.Type, rightType semantic.Type) (string, semantic.Type, semantic.Type, bool, bool) {
	leftKind := classifyRuntimeStringCompareKind(leftType)
	rightKind := classifyRuntimeStringCompareKind(rightType)
	if leftKind == runtimeStringCompareNone || rightKind == runtimeStringCompareNone {
		return "", nil, nil, false, false
	}
	if leftKind == runtimeStringCompareRaw && rightKind == runtimeStringCompareRaw {
		return "", nil, nil, false, false
	}
	left := stringCompareValueOperandType(leftType)
	right := stringCompareValueOperandType(rightType)
	if leftKind == runtimeStringCompareView && rightKind == runtimeStringCompareView {
		return "ctx_string_views_eq", left, right, false, true
	}
	if leftKind == runtimeStringCompareView {
		return "ctx_string_view_eq", left, right, false, true
	}
	if rightKind == runtimeStringCompareView {
		return "ctx_string_view_eq", right, left, true, true
	}
	return "ctx_streq", left, right, false, true
}

type runtimeStringCompareKind int

const (
	runtimeStringCompareNone runtimeStringCompareKind = iota
	runtimeStringCompareCStr
	runtimeStringCompareView
	runtimeStringCompareRaw
)

func classifyRuntimeStringCompareKind(t semantic.Type) runtimeStringCompareKind {
	if t == nil {
		return runtimeStringCompareNone
	}
	if _, ok := t.(*semantic.CStrType); ok {
		return runtimeStringCompareCStr
	}
	if _, ok := t.(*semantic.SViewType); ok {
		return runtimeStringCompareView
	}
	if st, ok := t.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeStringCompareView
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return runtimeStringCompareNone
	}
	if builtin, ok := ref.Elem.(*semantic.BuiltinType); ok && builtin.Name == "u8" {
		return runtimeStringCompareRaw
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return runtimeStringCompareView
	}
	return runtimeStringCompareNone
}
