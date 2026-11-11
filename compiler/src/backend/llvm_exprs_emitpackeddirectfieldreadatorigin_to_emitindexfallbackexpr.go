//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitPackedDirectFieldReadAtOrigin(ops *packedStoreOps, handleValue llvmIRValueRef, enumType *semantic.EnumType, fieldType semantic.Type, fieldOffsetBytes uint64, origin packedReadOriginKey, name string) (llvmIRValueRef, error) {
	if ops == nil || enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("packed direct field read requires packed enum metadata")
	}
	fieldSizeBytes, err := s.g.abiSizeOfType(fieldType)
	if err != nil {
		return nil, err
	}
	if fieldSizeBytes == 0 {
		return s.zeroValue(fieldType)
	}
	var cacheKey packedDirectFieldReadCacheKey
	cacheDirectField := false
	if ops.canCacheDirectReadValues(enumType) {
		if s.packedDirectFieldReads == nil {
			s.packedDirectFieldReads = map[packedDirectFieldReadCacheKey]llvmIRValueRef{}
		}
		originKey, cacheHandle := ops.directReadCacheIdentity(enumType, origin, handleValue)
		cacheKey = packedDirectFieldReadCacheKey{
			block:    ops.cacheKeyBlock(),
			store:    ops.cacheKeyStoreSSA(originKey, ops.storeValue),
			enumType: enumType,
			origin:   originKey,
			handle:   cacheHandle,
			offset:   fieldOffsetBytes,
			size:     fieldSizeBytes,
			typeKey:  fieldType.String(),
		}
		if cached, ok := s.packedDirectFieldReads[cacheKey]; ok && cached != nil {
			return cached, nil
		}
		cacheDirectField = true
	}
	wordBytes := s.g.payloadSlotBytes(enumType)
	wordOffset := fieldOffsetBytes / wordBytes
	byteOffsetInWord := fieldOffsetBytes % wordBytes
	if byteOffsetInWord+fieldSizeBytes <= wordBytes {
		usizeType, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, err
		}
		wordValue, err := ops.loadPayloadWordAtOrigin(handleValue, enumType, llvmConstInt(usizeType, llvmIRUnsignedLongLong(wordOffset), 0), origin, name+".word")
		if err != nil {
			return nil, err
		}
		if byteOffsetInWord != 0 {
			uintptrLLVMType, err := s.g.lowerBuiltin("uintptr")
			if err != nil {
				return nil, err
			}
			shiftBits := llvmConstInt(uintptrLLVMType, llvmIRUnsignedLongLong(byteOffsetInWord*8), 0)
			wordValue = llvmBuildLShr(s.builder, wordValue, shiftBits, cStringFree(name+".shift"))
		}
		// The word holds the field's BIT PATTERN, so a FLOAT field must be reinterpreted,
		// not numerically converted. coerceValue emits `uitofp`, which turned the stored
		// 7.0f (bits 0x40E00000) into the value 1088421888.0 -- an `@storage(inline)` common
		// under the AoS profile read back garbage while the constructor's own row GEP was
		// correct. The multi-word path below already reinterprets, via a memcpy through
		// allocas; this fast path did not.
		var coerced llvmIRValueRef
		if semantic.IsFloatType(fieldType) {
			fieldLLVMType, err := s.g.lowerType(fieldType)
			if err != nil {
				return nil, err
			}
			bitsType := llvmIntTypeInContext(s.g.context, llvmIRUnsigned(fieldSizeBytes*8))
			truncated := llvmBuildTrunc(s.builder, wordValue, bitsType, cStringFree(name+".bits"))
			coerced = llvmBuildBitCast(s.builder, truncated, fieldLLVMType, cStringFree(name+".float"))
		} else {
			converted, err := s.coerceValue(wordValue, s.g.namedType("uintptr"), fieldType)
			if err != nil {
				return nil, err
			}
			coerced = converted
		}
		if cacheDirectField {
			s.packedDirectFieldReads[cacheKey] = coerced
		}
		return coerced, nil
	}

	fieldPtr, err := s.createEntryAlloca(name+".tmp", fieldType)
	if err != nil {
		return nil, err
	}
	fieldLLVMType, err := s.g.lowerType(fieldType)
	if err != nil {
		return nil, err
	}
	llvmBuildStore(s.builder, llvmConstNull(fieldLLVMType), fieldPtr)
	lastByte := byteOffsetInWord + fieldSizeBytes
	wordCount := lastByte / wordBytes
	if lastByte%wordBytes != 0 {
		wordCount++
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	for i := uint64(0); i < wordCount; i++ {
		wordValue, err := ops.loadPayloadWordAtOrigin(handleValue, enumType, llvmConstInt(usizeType, llvmIRUnsignedLongLong(wordOffset+i), 0), origin, name+".word")
		if err != nil {
			return nil, err
		}
		wordPtr, err := s.createEntryAlloca(name+".word.tmp", s.g.namedType("uintptr"))
		if err != nil {
			return nil, err
		}
		llvmBuildStore(s.builder, wordValue, wordPtr)
		wordStart := i * wordBytes
		copyStart := wordStart
		if copyStart < byteOffsetInWord {
			copyStart = byteOffsetInWord
		}
		copyEnd := wordStart + wordBytes
		if copyEnd > lastByte {
			copyEnd = lastByte
		}
		if copyEnd <= copyStart {
			continue
		}
		srcPtr := wordPtr
		if copyStart > wordStart {
			srcPtr, err = s.emitByteOffsetPtr(wordPtr, copyStart-wordStart, name+".src")
			if err != nil {
				return nil, err
			}
		}
		dstPtr, err := s.emitByteOffsetPtr(fieldPtr, copyStart-byteOffsetInWord, name+".dst")
		if err != nil {
			return nil, err
		}
		if err := s.emitRawMemcpy(dstPtr, srcPtr, copyEnd-copyStart, name+".copy"); err != nil {
			return nil, err
		}
	}
	value, err := s.loadValue(fieldPtr, fieldType, name+".value")
	if err != nil {
		return nil, err
	}
	if cacheDirectField {
		s.packedDirectFieldReads[cacheKey] = value
	}
	return value, nil
}
func (s *functionState) emitPackSideTableFieldValue(bufferPtr llvmIRValueRef, byteOffset uint64, fieldValue llvmIRValueRef, fieldType semantic.Type, name string) error {
	fieldSizeBytes, err := s.g.abiSizeOfType(fieldType)
	if err != nil {
		return err
	}
	if fieldSizeBytes == 0 {
		return nil
	}
	fieldPtr, err := s.emitStackTempValue(fieldValue, fieldType, name+".field.tmp")
	if err != nil {
		return err
	}
	dstPtr, err := s.emitByteOffsetPtr(bufferPtr, byteOffset, name+".dst")
	if err != nil {
		return err
	}
	return s.emitRawMemcpy(dstPtr, fieldPtr, fieldSizeBytes, name+".copy")
}
func (s *functionState) packedEnumDirectFieldByteOffset(enumType *semantic.EnumType, fieldIndex int) (uint64, bool, error) {
	if enumType == nil || !enumType.Packed || fieldIndex <= 0 {
		return 0, false, nil
	}
	payloadIndex, err := s.g.packedEnumPayloadFieldIndex(enumType)
	if err != nil {
		return 0, false, err
	}
	if fieldIndex >= payloadIndex {
		return 0, false, nil
	}
	rowType, err := s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return 0, false, err
	}
	offsetBytes, err := s.g.abiOffsetOfLLVMElement(rowType, fieldIndex)
	if err != nil {
		return 0, false, err
	}
	return offsetBytes, true, nil
}
func (s *functionState) readPackedEnumWordWithStore(handleValue llvmIRValueRef, enumType *semantic.EnumType, store *packedStoreBinding, wordOffset llvmIRValueRef) (llvmIRValueRef, error) {
	ops, ok := s.packedStoreOpsFromBinding(store)
	if !ok {
		return nil, fmt.Errorf("packed enum %s common-field read requires store context", enumType.Name)
	}
	return ops.loadPayloadWord(handleValue, enumType, wordOffset, "packed.common.store")
}

// The owner resolves exactly as an enum constructor's does (enumConstructorInfoFromField):
// through the enclosing module's visible names. A raw NamedTypes lookup of the bare
// spelling missed every packed enum declared inside a module, so `Message.Store(owner)`
// there fell through to the enum-constructor path and failed as "unknown enum constructor".
func (s *functionState) packedStoreConstructorInfoFromField(expr *ast.FieldExpr) (*semantic.PackedEnumStoreType, bool) {
	if expr == nil || expr.Field != "Store" {
		return nil, false
	}
	ownerName, _, ok := qualifiedFieldOwnerAndLeaf(expr)
	if !ok {
		return nil, false
	}
	base, _, ok := s.lookupVisibleNamedType(ownerName)
	if !ok {
		return nil, false
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok || !enumType.Packed || expr.Field != "Store" || enumType.StoreType == nil {
		return nil, false
	}
	return semantic.PackedEnumStoreWithState(enumType.StoreType, s.g.namedType("Local")), true
}
func (s *functionState) packedStoreConstructorCall(expr *ast.CallExpr) (*semantic.PackedEnumStoreType, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, false
	}
	return s.packedStoreConstructorInfoFromField(fieldExpr)
}
