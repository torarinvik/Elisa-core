//go:build cgo

package backend

import (
	"elisacore/src/semantic"
	"fmt"
)

// aosRecordPtr resolves a node index handle to its record address via ctx_aos_store_record(state,
// index). The record holds the full rowType {tag, common, payload}; all field byte offsets are
// rowType-relative (packedEnumVariantPayloadFieldByteOffset uses abiOffsetOfLLVMElement(rowType,...)),
// so reads are direct loads at record+offset, consistent with the constructor's record-based writes.
func (ops *packedStoreOps) aosRecordPtr(handleValue llvmIRValueRef, enumType *semantic.EnumType, name string) (llvmIRValueRef, error) {
	// docs/82 `handle: ptr`: the handle is the record address carried as a uintptr-width
	// integer — handle-to-record is a bare inttoptr, no store state and no helper call.
	if enumType != nil && enumType.HandleIsPointer() {
		recordPtrType, err := ops.s.g.lowerType(ops.voidRefType())
		if err != nil {
			return nil, err
		}
		return llvmBuildIntToPtr(ops.s.builder, handleValue, recordPtrType, cStringFree(name+".record.ptr")), nil
	}
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, err
	}
	usizeType := ops.s.g.namedType("usize")
	u32Type := ops.s.g.namedType("u32")
	h32, err := ops.s.coerceValue(handleValue, enumType, u32Type)
	if err != nil {
		return nil, err
	}
	idx, err := ops.s.coerceValue(h32, u32Type, usizeType)
	if err != nil {
		return nil, err
	}
	helperType := ops.cachedRuntimeHelperType("ctx_aos_store_record", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "ctx_aos_store_record", Params: []semantic.Type{ops.voidRefType(), usizeType}, Return: ops.voidRefType()}
	})
	callee, err := ops.s.g.ensureFunctionDeclared("ctx_aos_store_record", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return ops.s.buildCall(llvmFnType, callee, []llvmIRValueRef{stateValue, idx}, name+".record"), nil
}
func (ops *packedStoreOps) storeTagAt(handleValue llvmIRValueRef, enumType *semantic.EnumType, name string) (llvmIRValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum tag metadata")
	}
	tagType := ops.s.g.namedType("u32")
	switch ops.s.g.packedModeForEnum(enumType) {
	case packedEnumABIIndexSOA:
		stateValue, err := ops.stateValue(name + ".state")
		if err != nil {
			return nil, err
		}
		helperType := ops.cachedRuntimeHelperType("ctx_packed_store_read_index_tag", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "ctx_packed_store_read_index_tag", Params: []semantic.Type{ops.voidRefType(), tagType}, Return: tagType}
		})
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_read_index_tag", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		coercedHandle, err := ops.s.coerceValue(handleValue, enumType, tagType)
		if err != nil {
			return nil, err
		}
		if ops.canCacheDenseHandleReads(enumType) {
			if ops.s.packedDenseTagReads == nil {
				ops.s.packedDenseTagReads = map[packedDenseTagReadCacheKey]llvmIRValueRef{}
			}
			key := packedDenseTagReadCacheKey{block: ops.currentBlock(), storeType: ops.storeType, state: stateValue, handle: ops.canonicalizeDenseHandleKey(coercedHandle)}
			if cached, ok := ops.s.packedDenseTagReads[key]; ok && cached != nil {
				return cached, nil
			}
			var tagValue llvmIRValueRef
			if ops.canUseOptimizedIndexSOADirectMetadataRead(enumType) {
				tagValue, err = ops.loadIndexSOATagDirect(stateValue, coercedHandle, name+".direct")
				if err != nil {
					return nil, err
				}
			} else {
				tagValue = ops.s.buildCall(llvmFnType, callee, []llvmIRValueRef{stateValue, coercedHandle}, name)
			}
			ops.s.packedDenseTagReads[key] = tagValue
			return tagValue, nil
		}
		if ops.canUseOptimizedIndexSOADirectMetadataRead(enumType) {
			return ops.loadIndexSOATagDirect(stateValue, coercedHandle, name+".direct")
		}
		return ops.s.buildCall(llvmFnType, callee, []llvmIRValueRef{stateValue, coercedHandle}, name), nil
	case packedEnumABIVariantSparse:
		stateValue, err := ops.stateValue(name + ".state")
		if err != nil {
			return nil, err
		}
		helperType := ops.cachedRuntimeHelperType("ctx_packed_store_read_variant_sparse_tag", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "ctx_packed_store_read_variant_sparse_tag", Params: []semantic.Type{ops.voidRefType(), tagType}, Return: tagType}
		})
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_read_variant_sparse_tag", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		coercedHandle, err := ops.s.coerceValue(handleValue, enumType, tagType)
		if err != nil {
			return nil, err
		}
		if ops.canCacheDenseHandleReads(enumType) {
			if ops.s.packedVariantSparseTagReads == nil {
				ops.s.packedVariantSparseTagReads = map[packedVariantSparseTagReadCacheKey]llvmIRValueRef{}
			}
			key := packedVariantSparseTagReadCacheKey{block: ops.currentBlock(), storeType: ops.storeType, state: stateValue, handle: ops.canonicalizeDenseHandleKey(coercedHandle)}
			if cached, ok := ops.s.packedVariantSparseTagReads[key]; ok && cached != nil {
				return cached, nil
			}
			tagValue := ops.s.buildCall(llvmFnType, callee, []llvmIRValueRef{stateValue, coercedHandle}, name)
			ops.s.packedVariantSparseTagReads[key] = tagValue
			return tagValue, nil
		}
		return ops.s.buildCall(llvmFnType, callee, []llvmIRValueRef{stateValue, coercedHandle}, name), nil
	case packedEnumABIAoS:
		// docs/76 Slice 2: the tag is rowType field 0 → byte offset 0 in the record.
		recordPtr, err := ops.aosRecordPtr(handleValue, enumType, name+".tag")
		if err != nil {
			return nil, err
		}
		tagLLVM, err := ops.s.g.lowerType(tagType)
		if err != nil {
			return nil, err
		}
		return llvmBuildLoad2(ops.s.builder, tagLLVM, recordPtr, cStringFree(name)), nil
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", ops.s.g.packedModeForEnum(enumType))
	}
}
func (ops *packedStoreOps) loadPayloadWordAtOrigin(handleValue llvmIRValueRef, enumType *semantic.EnumType, wordOffset llvmIRValueRef, origin packedReadOriginKey, _ string) (llvmIRValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum payload metadata")
	}
	uintptrType := ops.s.g.namedType("uintptr")
	switch ops.s.g.packedModeForEnum(enumType) {
	case packedEnumABIIndexSOA:
		arenaValue, err := ops.arenaValue("packed.arena")
		if err != nil {
			return nil, err
		}
		stateValue, err := ops.stateValue("packed.state")
		if err != nil {
			return nil, err
		}
		u32Type := ops.s.g.namedType("u32")
		helperType := ops.cachedRuntimeHelperType("ctx_packed_store_read_index_word", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "ctx_packed_store_read_index_word", Params: []semantic.Type{ops.arenaRefType(), u32Type, ops.voidRefType(), ops.s.g.namedType("usize")}, Return: uintptrType}
		})
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_read_index_word", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		coercedHandle, err := ops.s.coerceValue(handleValue, enumType, u32Type)
		if err != nil {
			return nil, err
		}
		if prefixWordOffset, ok := ops.constantUint64(wordOffset); ok && ops.canUseOptimizedIndexSOADirectPrefixRead(enumType) {
			prefixWordCount, err := ops.s.g.packedEnumConfiguredPrefixWordCount(enumType)
			if err != nil {
				return nil, err
			}
			if prefixWordOffset < prefixWordCount {
				if ops.canCacheDenseHandleReads(enumType) {
					if ops.s.packedDenseWordReads == nil {
						ops.s.packedDenseWordReads = map[packedDenseWordReadCacheKey]llvmIRValueRef{}
					}
					originKey, cacheHandle := ops.denseReadCacheIdentity(origin, coercedHandle)
					key := packedDenseWordReadCacheKey{block: ops.cacheKeyBlock(), storeType: ops.storeType, state: ops.cacheKeyStoreSSA(originKey, stateValue), origin: originKey, handle: cacheHandle, offset: wordOffset}
					if cached, ok := ops.s.packedDenseWordReads[key]; ok && cached != nil {
						return cached, nil
					}
					wordValue, err := ops.loadIndexSOAPrefixWordDirect(stateValue, coercedHandle, prefixWordOffset, "packed.index.prefix.word")
					if err != nil {
						return nil, err
					}
					ops.s.packedDenseWordReads[key] = wordValue
					return wordValue, nil
				}
				return ops.loadIndexSOAPrefixWordDirect(stateValue, coercedHandle, prefixWordOffset, "packed.index.prefix.word")
			}
		}
		if ops.canCacheDenseHandleReads(enumType) {
			if ops.s.packedDenseWordReads == nil {
				ops.s.packedDenseWordReads = map[packedDenseWordReadCacheKey]llvmIRValueRef{}
			}
			originKey, cacheHandle := ops.denseReadCacheIdentity(origin, coercedHandle)
			key := packedDenseWordReadCacheKey{block: ops.cacheKeyBlock(), storeType: ops.storeType, state: ops.cacheKeyStoreSSA(originKey, stateValue), origin: originKey, handle: cacheHandle, offset: wordOffset}
			if cached, ok := ops.s.packedDenseWordReads[key]; ok && cached != nil {
				return cached, nil
			}
			wordValue := ops.s.buildCall(llvmFnType, callee, []llvmIRValueRef{arenaValue, coercedHandle, stateValue, wordOffset}, "")
			ops.s.packedDenseWordReads[key] = wordValue
			return wordValue, nil
		}
		return ops.s.buildCall(llvmFnType, callee, []llvmIRValueRef{arenaValue, coercedHandle, stateValue, wordOffset}, ""), nil
	case packedEnumABIVariantSparse:
		stateValue, err := ops.stateValue("packed.state")
		if err != nil {
			return nil, err
		}
		u32Type := ops.s.g.namedType("u32")
		helperType := ops.cachedRuntimeHelperType("ctx_packed_store_read_variant_sparse_word", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "ctx_packed_store_read_variant_sparse_word", Params: []semantic.Type{u32Type, ops.voidRefType(), ops.s.g.namedType("usize")}, Return: uintptrType}
		})
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_read_variant_sparse_word", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		coercedHandle, err := ops.s.coerceValue(handleValue, enumType, u32Type)
		if err != nil {
			return nil, err
		}
		if ops.canCacheDenseHandleReads(enumType) {
			if ops.s.packedVariantSparseWordReads == nil {
				ops.s.packedVariantSparseWordReads = map[packedVariantSparseWordReadCacheKey]llvmIRValueRef{}
			}
			key := packedVariantSparseWordReadCacheKey{block: ops.currentBlock(), storeType: ops.storeType, state: stateValue, handle: ops.canonicalizeDenseHandleKey(coercedHandle), offset: wordOffset}
			if cached, ok := ops.s.packedVariantSparseWordReads[key]; ok && cached != nil {
				return cached, nil
			}
			wordValue := ops.s.buildCall(llvmFnType, callee, []llvmIRValueRef{coercedHandle, stateValue, wordOffset}, "")
			ops.s.packedVariantSparseWordReads[key] = wordValue
			return wordValue, nil
		}
		return ops.s.buildCall(llvmFnType, callee, []llvmIRValueRef{coercedHandle, stateValue, wordOffset}, ""), nil
	case packedEnumABIAoS:
		// docs/76 Slice 2: read the uintptr word at record + wordOffset*wordBytes. wordOffset is
		// rowType-relative (the field byte offset / word), so this covers both common and payload words.
		recordPtr, err := ops.aosRecordPtr(handleValue, enumType, "packed.aos")
		if err != nil {
			return nil, err
		}
		usizeLLVM, err := ops.s.g.lowerType(ops.s.g.namedType("usize"))
		if err != nil {
			return nil, err
		}
		wordBytes := ops.s.g.payloadSlotBytes(enumType)
		byteOffset := llvmBuildMul(ops.s.builder, wordOffset, llvmConstInt(usizeLLVM, llvmIRUnsignedLongLong(wordBytes), 0), cStringFree("packed.aos.byteoff"))
		i8LLVM, err := ops.s.g.lowerBuiltin("u8")
		if err != nil {
			return nil, err
		}
		indices := []llvmIRValueRef{byteOffset}
		wordPtr := llvmBuildGEP2(ops.s.builder, i8LLVM, recordPtr, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree("packed.aos.word.ptr"))
		uintptrLLVM, err := ops.s.g.lowerType(uintptrType)
		if err != nil {
			return nil, err
		}
		// Load EXACTLY one slot (not a full uintptr): with narrowed slots a uintptr load of the last
		// slot would over-read past the record into the next chunk record (or off the chunk at the
		// page boundary). The slot type's natural alignment matches the slot stride, so the load is
		// aligned. Callers expect a uintptr-typed word, so zero-extend a narrow slot back up.
		slotLLVM, err := ops.s.g.payloadSlotType(enumType)
		if err != nil {
			return nil, err
		}
		wordValue := llvmBuildLoad2(ops.s.builder, slotLLVM, wordPtr, cStringFree("packed.aos.word"))
		if slotLLVM != uintptrLLVM {
			wordValue = llvmBuildZExt(ops.s.builder, wordValue, uintptrLLVM, cStringFree("packed.aos.word.zext"))
		}
		return wordValue, nil
	default:
		arenaValue, err := ops.arenaValue("packed.arena")
		if err != nil {
			return nil, err
		}
		stateValue, err := ops.stateValue("packed.state")
		if err != nil {
			return nil, err
		}
		helperType := ops.cachedRuntimeHelperType("ctx_packed_store_read_word", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "ctx_packed_store_read_word", Params: []semantic.Type{ops.arenaRefType(), uintptrType, ops.voidRefType(), ops.s.g.namedType("usize")}, Return: uintptrType}
		})
		callee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_read_word", helperType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
		if err != nil {
			return nil, err
		}
		coercedHandle, err := ops.s.coerceValue(handleValue, enumType, uintptrType)
		if err != nil {
			return nil, err
		}
		return ops.s.buildCall(llvmFnType, callee, []llvmIRValueRef{arenaValue, coercedHandle, stateValue, wordOffset}, ""), nil
	}
}
func (ops *packedStoreOps) loadPayloadWord(handleValue llvmIRValueRef, enumType *semantic.EnumType, wordOffset llvmIRValueRef, name string) (llvmIRValueRef, error) {
	return ops.loadPayloadWordAtOrigin(handleValue, enumType, wordOffset, packedReadOriginKey{}, name)
}
func (ops *packedStoreOps) canDirectWordRead() bool {
	if ops == nil || ops.s == nil || ops.s.g == nil {
		return false
	}
	mode := ops.s.g.packedLoweringForStore(ops.storeType)
	return packedModeUsesDirectWordReads(mode)
}
func (ops *packedStoreOps) canDirectTagRead() bool {
	return ops.canDirectWordRead()
}
func (ops *packedStoreOps) loadPayloadWordsAsTypes(handleValue llvmIRValueRef, enumType *semantic.EnumType, wordOffsets []llvmIRValueRef, types []semantic.Type, namePrefix string) ([]llvmIRValueRef, bool, error) {
	if !ops.canDirectWordRead() || enumType == nil || !enumType.Packed {
		return nil, false, nil
	}
	if len(wordOffsets) != len(types) {
		return nil, false, fmt.Errorf("packed payload word/type arity mismatch")
	}
	values := make([]llvmIRValueRef, 0, len(types))
	uintptrType := ops.s.g.namedType("uintptr")
	for i, payloadType := range types {
		wordValue, err := ops.loadPayloadWord(handleValue, enumType, wordOffsets[i], namePrefix)
		if err != nil {
			return nil, false, err
		}
		coerced, err := ops.s.coerceValue(wordValue, uintptrType, payloadType)
		if err != nil {
			return nil, false, err
		}
		values = append(values, coerced)
	}
	return values, true, nil
}
func (ops *packedStoreOps) loadTailView(handleValue llvmIRValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, payloadIndex int, name string) (llvmIRValueRef, bool, error) {
	if ops == nil || enumType == nil || !enumType.Packed || variant == nil {
		return nil, false, nil
	}
	if ops.s == nil || ops.s.g == nil || ops.s.g.packedModeForEnum(enumType) != packedEnumABIIndexSOA {
		return nil, false, nil
	}
	tailIndex, ok := variant.TailPayloadIndex()
	if !ok || tailIndex != payloadIndex {
		return nil, false, nil
	}
	viewType, ok := variant.TailPayloadViewType()
	if !ok || viewType == nil {
		return nil, false, nil
	}
	// SoA (index-SOA) tail-view: columns are strided by the machine word, NOT the AoS inline
	// payload slot. Keep the uintptr word here; slot-narrowing applies only to the AoS ABI.
	wordBytes := uint64(ops.s.g.wordBits / 8)
	if wordBytes == 0 {
		wordBytes = 8
	}
	payloadFieldIndex, err := ops.s.g.packedEnumPayloadFieldIndex(enumType)
	if err != nil {
		return nil, false, err
	}
	rowType, err := ops.s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return nil, false, err
	}
	usizeType := ops.s.g.namedType("usize")
	usizeLLVMType, err := ops.s.g.lowerType(usizeType)
	if err != nil {
		return nil, false, err
	}
	i32Type := llvmInt32TypeInContext(ops.s.g.context)
	zeroIndex := llvmConstInt(i32Type, 0, 0)
	payloadFieldIndexValue := llvmConstInt(i32Type, llvmIRUnsignedLongLong(payloadFieldIndex), 0)
	nullPtr := llvmConstNull(llvmPointerTypeInContext(ops.s.g.context, 0))
	payloadIndices := []llvmIRValueRef{zeroIndex, payloadFieldIndexValue}
	payloadPtr := llvmBuildGEP2(ops.s.builder, rowType, nullPtr, llvmValueSlicePtr(payloadIndices), llvmIRUnsigned(len(payloadIndices)), cStringFree(name+".payload.word.ptr"))
	payloadOffsetBytes := llvmBuildPtrToInt(ops.s.builder, payloadPtr, usizeLLVMType, cStringFree(name+".payload.word.bytes"))
	wordBytesValue := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(wordBytes), 0)
	baseWordOffset := llvmBuildUDiv(ops.s.builder, payloadOffsetBytes, wordBytesValue, cStringFree(name+".payload.word.offset"))
	payloadType, err := ops.s.g.lowerEnumVariantPayloadType(variant)
	if err != nil {
		return nil, false, err
	}
	fieldIndexValue := llvmConstInt(i32Type, llvmIRUnsignedLongLong(payloadIndex), 0)
	fieldIndices := []llvmIRValueRef{zeroIndex, fieldIndexValue}
	fieldPtr := llvmBuildGEP2(ops.s.builder, payloadType, nullPtr, llvmValueSlicePtr(fieldIndices), llvmIRUnsigned(len(fieldIndices)), cStringFree(name+".field.word.ptr"))
	fieldOffsetBytes := llvmBuildPtrToInt(ops.s.builder, fieldPtr, usizeLLVMType, cStringFree(name+".field.word.bytes"))
	fieldWordOffset := llvmBuildUDiv(ops.s.builder, fieldOffsetBytes, wordBytesValue, cStringFree(name+".field.word.offset"))
	viewBaseWordOffset := llvmBuildAdd(ops.s.builder, baseWordOffset, fieldWordOffset, cStringFree(name+".field.word.base"))
	oneWord := llvmConstInt(usizeLLVMType, 1, 0)
	dataWord, err := ops.loadPayloadWord(handleValue, enumType, viewBaseWordOffset, name+".data.word")
	if err != nil {
		return nil, false, err
	}
	lenWordOffset := llvmBuildAdd(ops.s.builder, viewBaseWordOffset, oneWord, cStringFree(name+".len.word.offset"))
	lenWord, err := ops.loadPayloadWord(handleValue, enumType, lenWordOffset, name+".len.word")
	if err != nil {
		return nil, false, err
	}
	voidRefType := ops.voidRefType()
	dataValue, err := ops.s.coerceValue(dataWord, ops.s.g.namedType("uintptr"), voidRefType)
	if err != nil {
		return nil, false, err
	}
	lenValue, err := ops.s.coerceValue(lenWord, ops.s.g.namedType("uintptr"), usizeType)
	if err != nil {
		return nil, false, err
	}
	viewLLVMType, err := ops.s.g.lowerType(viewType)
	if err != nil {
		return nil, false, err
	}
	viewValue := llvmGetUndef(viewLLVMType)
	viewValue = llvmBuildInsertValue(ops.s.builder, viewValue, dataValue, 0, cStringFree(name+".data"))
	viewValue = llvmBuildInsertValue(ops.s.builder, viewValue, lenValue, 1, cStringFree(name+".len"))
	return viewValue, true, nil
}
func (ops *packedStoreOps) storeSlice(startValue llvmIRValueRef, endValue llvmIRValueRef, resultType semantic.Type, name string) (llvmIRValueRef, semantic.Type, error) {
	stateValue, err := ops.stateValue(name + ".state")
	if err != nil {
		return nil, nil, err
	}
	helperName := "ctx_packed_store_view"
	if packedModeUsesDenseIndexHandle(ops.s.g.packedLoweringForStore(ops.storeType)) {
		helperName = "ctx_packed_store_indices_view"
	}
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{ops.voidRefType(), ops.s.g.namedType("usize"), ops.s.g.namedType("usize")},
		Return: resultType,
	}
	callee, err := ops.s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := ops.s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	value := ops.s.buildCall(llvmFnType, callee, []llvmIRValueRef{stateValue, startValue, endValue}, name)
	return value, resultType, nil
}
