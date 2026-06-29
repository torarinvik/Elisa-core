//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"unsafe"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

const staticDictInitCap = 8

type staticDictSlot struct {
	keyExpr   ast.Expr
	valueExpr ast.Expr
	occupied  bool
}

func (g *llvmGenerator) constDictLiteralValue(expr *ast.ListLitExpr, dictType *semantic.DictType, namespace string) (C.LLVMValueRef, error) {
	if expr == nil || dictType == nil {
		return nil, fmt.Errorf("const dict literal requires a dict type")
	}
	if len(expr.Keys) != len(expr.Elems) {
		return nil, fmt.Errorf("dict literal has %d keys for %d values", len(expr.Keys), len(expr.Elems))
	}
	if len(expr.Spreads) != 0 {
		for _, spread := range expr.Spreads {
			if spread {
				return nil, fmt.Errorf("const dict literal does not support spread entries")
			}
		}
	}
	capacity := staticDictCapacity(len(expr.Elems))
	slots := make([]staticDictSlot, capacity)
	if capacity > 0 {
		mask := uint64(capacity - 1)
		for i, keyExpr := range expr.Keys {
			keyValue, ok := g.evalConstExpr(keyExpr)
			if !ok {
				return nil, fmt.Errorf("const dict key %d is not a compile-time value", i)
			}
			hash, fingerprint, err := staticDictKeyHash(dictType.Key, keyValue, g.wordBits)
			if err != nil {
				return nil, err
			}
			slot := int(hash & mask)
			for {
				if !slots[slot].occupied {
					slots[slot] = staticDictSlot{keyExpr: keyExpr, valueExpr: expr.Elems[i], occupied: true}
					break
				}
				existingValue, ok := g.evalConstExpr(slots[slot].keyExpr)
				if !ok {
					return nil, fmt.Errorf("const dict key %d is not a compile-time value", i)
				}
				_, existingFingerprint, err := staticDictKeyHash(dictType.Key, existingValue, g.wordBits)
				if err != nil {
					return nil, err
				}
				if existingFingerprint == fingerprint {
					return nil, fmt.Errorf("duplicate const dict key")
				}
				slot = (slot + 1) & int(mask)
			}
		}
	}

	dictLLVMType, err := g.lowerType(dictType)
	if err != nil {
		return nil, err
	}
	usizeType, err := g.lowerType(g.result.NamedTypes["usize"])
	if err != nil {
		return nil, err
	}
	ptrType := C.LLVMPointerTypeInContext(g.context, 0)

	itemsValue := C.LLVMConstNull(ptrType)
	if capacity > 0 {
		itemsValue, err = g.constDictBucketArray(expr, dictType, slots, capacity, namespace)
		if err != nil {
			return nil, err
		}
	}

	count := C.LLVMConstInt(usizeType, C.ulonglong(len(expr.Elems)), 0)
	capValue := C.LLVMConstInt(usizeType, C.ulonglong(capacity), 0)
	fields := []C.LLVMValueRef{
		itemsValue,
		count,
		count,
		capValue,
		C.LLVMConstNull(ptrType),
	}
	return C.LLVMConstNamedStruct(dictLLVMType, llvmValueSlicePtr(fields), C.unsigned(len(fields))), nil
}

func (g *llvmGenerator) constDictBucketArray(expr *ast.ListLitExpr, dictType *semantic.DictType, slots []staticDictSlot, capacity int, namespace string) (C.LLVMValueRef, error) {
	base, ok := g.result.NamedTypes["DictBucket"]
	if !ok {
		return nil, fmt.Errorf("missing runtime struct DictBucket")
	}
	bucketType := &semantic.GenericInstanceType{Name: "DictBucket", Base: base, Args: []semantic.Type{dictType.Key, dictType.Value}}
	bucketLLVMType, err := g.lowerType(bucketType)
	if err != nil {
		return nil, err
	}
	arrayType := C.LLVMArrayType2(bucketLLVMType, C.uint64_t(capacity))

	zeroKey, err := g.constZero(dictType.Key)
	if err != nil {
		return nil, err
	}
	zeroValue, err := g.constZero(dictType.Value)
	if err != nil {
		return nil, err
	}
	stateType := C.LLVMInt8TypeInContext(g.context)
	buckets := make([]C.LLVMValueRef, 0, capacity)
	for _, slot := range slots {
		key := zeroKey
		value := zeroValue
		state := C.LLVMConstInt(stateType, 0, 0)
		if slot.occupied {
			key, err = g.constExprValueInNamespace(slot.keyExpr, dictType.Key, namespace)
			if err != nil {
				return nil, err
			}
			value, err = g.constExprValueInNamespace(slot.valueExpr, dictType.Value, namespace)
			if err != nil {
				return nil, err
			}
			state = C.LLVMConstInt(stateType, 1, 0)
		}
		fields := []C.LLVMValueRef{key, value, state}
		buckets = append(buckets, C.LLVMConstNamedStruct(bucketLLVMType, llvmValueSlicePtr(fields), C.unsigned(len(fields))))
	}
	initializer := C.LLVMConstArray2(bucketLLVMType, llvmValueSlicePtr(buckets), C.uint64_t(len(buckets)))
	name := cString(g.nextSyntheticName(".const.dict.buckets."))
	defer C.free(unsafe.Pointer(name))
	global := C.LLVMAddGlobal(g.module, arrayType, name)
	C.LLVMSetLinkage(global, C.LLVMPrivateLinkage)
	C.LLVMSetGlobalConstant(global, 1)
	C.LLVMSetUnnamedAddress(global, C.LLVMGlobalUnnamedAddr)
	C.LLVMSetInitializer(global, initializer)
	zero32 := C.LLVMConstInt(C.LLVMInt32TypeInContext(g.context), 0, 0)
	indices := []C.LLVMValueRef{zero32, zero32}
	return C.LLVMConstInBoundsGEP2(arrayType, global, llvmValueSlicePtr(indices), C.unsigned(len(indices))), nil
}

func staticDictCapacity(count int) int {
	if count == 0 {
		return 0
	}
	capacity := staticDictInitCap
	for count*4 > capacity*3 {
		capacity *= 2
	}
	return capacity
}

func staticDictKeyHash(keyType semantic.Type, value semantic.ConstValue, wordBits int) (uint64, string, error) {
	if _, ok := semantic.StripAggregateStateType(keyType).(*semantic.DStrType); ok {
		if value.Kind != semantic.ConstString {
			return 0, "", fmt.Errorf("const dict cstr key must be a string literal")
		}
		hash := staticDictHashCStr(value.String)
		return hash, "s:" + value.String, nil
	}
	if semantic.IsBoolType(semantic.StripAggregateStateType(keyType)) {
		if value.Kind != semantic.ConstBool {
			return 0, "", fmt.Errorf("const dict bool key must be a bool literal")
		}
		var raw uint64
		if value.Bool {
			raw = 1
		}
		return staticDictHashU64(raw), fmt.Sprintf("b:%t", value.Bool), nil
	}
	if storage, ok := semantic.ConstEnumStorageType(semantic.StripAggregateStateType(keyType)); ok {
		return staticDictIntegralHash(storage, value, wordBits)
	}
	if semantic.IsIntegralType(semantic.StripAggregateStateType(keyType)) {
		return staticDictIntegralHash(keyType, value, wordBits)
	}
	return 0, "", fmt.Errorf("const dict key type %s is not supported", keyType)
}

func staticDictIntegralHash(keyType semantic.Type, value semantic.ConstValue, wordBits int) (uint64, string, error) {
	if value.Kind != semantic.ConstInt {
		return 0, "", fmt.Errorf("const dict integer key must be an integer literal")
	}
	raw := uint64(value.Int)
	width := integerBitWidth(keyType, wordBits)
	if width > 0 && width < 64 {
		raw &= (uint64(1) << width) - 1
	}
	return staticDictHashU64(raw), fmt.Sprintf("i:%d:%d", width, raw), nil
}

func staticDictHashCStr(value string) uint64 {
	hash := uint64(0xcbf29ce484222325)
	for _, b := range []byte(value) {
		hash ^= uint64(b)
		hash *= 0x100000001b3
	}
	return hash
}

func staticDictHashU64(value uint64) uint64 {
	hash := value
	hash = (hash ^ (hash >> 30)) * 0xbf58476d1ce4e5b9
	hash = (hash ^ (hash >> 27)) * 0x94d049bb133111eb
	hash = hash ^ (hash >> 31)
	return hash
}
