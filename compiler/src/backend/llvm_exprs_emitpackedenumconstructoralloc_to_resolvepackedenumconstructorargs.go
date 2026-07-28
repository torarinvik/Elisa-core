//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int elisacoreLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

static LLVMMetadataRef elisa_coreAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef elisa_coreAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned elisa_coreMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void elisa_coreSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = elisa_coreAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, elisa_coreMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef elisa_coreCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = elisa_coreAliasMDString(ctx, domainName);
	return elisa_coreAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef elisa_coreCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = elisa_coreAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return elisa_coreAliasMDNode(ctx, operands, 2);
}

static void elisa_coreAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = elisa_coreCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = elisa_coreCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	elisa_coreSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		elisa_coreSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitPackedEnumConstructorAlloc(callExpr *ast.CallExpr, storeValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) (C.LLVMValueRef, semantic.Type, error) {
	if enumType == nil || variant == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("missing packed enum constructor metadata")
	}
	orderedArgs, commonArgs, err := s.resolvePackedEnumConstructorArgs(callExpr, enumType, variant, args, argNames)
	if err != nil {
		return nil, nil, err
	}
	if len(orderedArgs) != len(variant.Payload) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s expects %d arguments, got %d", enumType.Name, variant.Name, len(variant.Payload), len(args))
	}
	tagValue, err := s.enumTagConstant(enumType, variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	if s.canInlinePackedEnumVariant(enumType, variant) {
		var payloadValue C.LLVMValueRef
		var payloadType semantic.Type
		if len(orderedArgs) == 1 {
			payloadType = variant.Payload[0]
			payloadValue, _, err = s.emitExpr(orderedArgs[0], payloadType)
			if err != nil {
				return nil, nil, err
			}
		}
		inlineHandle, err := s.buildInlinePackedEnumHandle(tagValue, payloadValue, payloadType)
		if err != nil {
			return nil, nil, err
		}
		return inlineHandle, enumType, nil
	}
	rowType, err := s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return nil, nil, err
	}
	sideWords, err := s.g.packedEnumCommonSideTableWordCount(enumType)
	if err != nil {
		return nil, nil, err
	}
	tailPlan, err := s.preparePackedEnumTailPayloadPlan(variant, orderedArgs)
	if err != nil {
		return nil, nil, err
	}
	allocPtr, enumValue, rowSizeValue, err := s.emitPackedEnumStorageAlloc(storeValue, enumType, tailPlan, tagValue)
	if err != nil {
		return nil, nil, err
	}
	rowValue := C.LLVMConstNull(rowType)
	rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, tagValue, 0, cStringFree("packed.enum.tag.ins"))
	commonValues := make(map[string]C.LLVMValueRef, len(enumType.Root().Decl.Common))
	for _, commonDecl := range enumType.Root().Decl.Common { // docs/77: common(...) lives on the root
		arg, ok := commonArgs[commonDecl.Name]
		if !ok {
			continue
		}
		layout, err := s.g.packedEnumCommonFieldLayout(enumType, commonDecl.Name)
		if err != nil {
			return nil, nil, err
		}
		fieldValue, _, err := s.emitExpr(arg, layout.Field.Type)
		if err != nil {
			return nil, nil, err
		}
		commonValues[commonDecl.Name] = fieldValue
		if layout.StoredInline {
			rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, fieldValue, C.unsigned(layout.RowFieldIndex), cStringFree("packed.enum.common.ins"))
		}
	}
	rowStore := C.LLVMBuildStore(s.builder, rowValue, allocPtr)
	s.g.setInstrAlignment(rowStore, s.g.payloadSlotBytes(enumType))
	if len(variant.Payload) > 0 {
		payloadPtr, err := s.enumPayloadPtr(allocPtr, enumType)
		if err != nil {
			return nil, nil, err
		}
		var tailDataPtr C.LLVMValueRef
		if tailPlan != nil {
			tailDataPtr, err = s.emitPackedEnumTailDataPtr(allocPtr, rowSizeValue)
			if err != nil {
				return nil, nil, err
			}
		}
		if len(variant.Payload) == 1 {
			argValue, err := s.emitPackedEnumConstructorPayloadValue(variant, 0, orderedArgs[0], tailPlan, tailDataPtr, storeValue, enumType)
			if err != nil {
				return nil, nil, err
			}
			if !llvmValueIsZeroConstant(argValue) {
				st := C.LLVMBuildStore(s.builder, argValue, payloadPtr)
				s.g.setInstrAlignment(st, s.g.payloadSlotBytes(enumType))
			}
		} else {
			payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
			if err != nil {
				return nil, nil, err
			}
			aggregate := C.LLVMGetUndef(payloadType)
			allZero := true
			for i, payload := range variant.Payload {
				_ = payload
				argValue, err := s.emitPackedEnumConstructorPayloadValue(variant, i, orderedArgs[i], tailPlan, tailDataPtr, storeValue, enumType)
				if err != nil {
					return nil, nil, err
				}
				if !llvmValueIsZeroConstant(argValue) {
					allZero = false
				}
				aggregate = C.LLVMBuildInsertValue(s.builder, aggregate, argValue, C.unsigned(i), cStringFree("packed.enum.payload.ins"))
			}
			if !allZero {
				st := C.LLVMBuildStore(s.builder, aggregate, payloadPtr)
				s.g.setInstrAlignment(st, s.g.payloadSlotBytes(enumType))
			}
		}
	}
	ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: enumType.StoreType}
	if sideWords > 0 {
		wordBytes := uint64(s.g.wordBits / 8)
		if wordBytes == 0 {
			wordBytes = 8
		}
		sideBufferType := &semantic.ArrayType{Elem: s.g.result.NamedTypes["uintptr"], HasConstSize: true, ConstSize: int64(sideWords)}
		sideBufferPtr, err := s.createEntryAlloca("packed.side.words", sideBufferType)
		if err != nil {
			return nil, nil, err
		}
		sideBufferLLVMType, err := s.g.lowerType(sideBufferType)
		if err != nil {
			return nil, nil, err
		}
		C.LLVMBuildStore(s.builder, C.LLVMConstNull(sideBufferLLVMType), sideBufferPtr)
		for _, commonDecl := range enumType.Root().Decl.Common { // docs/77: common(...) lives on the root
			layout, err := s.g.packedEnumCommonFieldLayout(enumType, commonDecl.Name)
			if err != nil {
				return nil, nil, err
			}
			if layout.StoredInline {
				continue
			}
			fieldValue, ok := commonValues[commonDecl.Name]
			if !ok {
				continue
			}
			if err := s.emitPackSideTableFieldValue(sideBufferPtr, layout.SideWordOffset*wordBytes, fieldValue, layout.Field.Type, "packed.side.pack"); err != nil {
				return nil, nil, err
			}
		}
		if err := ops.recordSideWords(sideBufferPtr, enumValue, "packed.side.record"); err != nil {
			return nil, nil, err
		}
	}
	if err := ops.recordPrefixWords(allocPtr, "packed.prefix.record"); err != nil {
		return nil, nil, err
	}
	mode := s.g.packedModeForEnum(enumType)
	if mode != packedEnumABIVariantSparse && tailPlan != nil {
		if err := s.emitPackedStoreRecordTag(storeValue, enumType.StoreType, tagValue); err != nil {
			return nil, nil, err
		}
	}
	return enumValue, enumType, nil
}
func (s *functionState) canInlinePackedEnumVariant(enumType *semantic.EnumType, variant *semantic.EnumVariant) bool {
	return false
}
func (s *functionState) buildInlinePackedEnumHandle(tagValue C.LLVMValueRef, payloadValue C.LLVMValueRef, payloadType semantic.Type) (C.LLVMValueRef, error) {
	uintptrLLVMType, err := s.g.lowerBuiltin("uintptr")
	if err != nil {
		return nil, err
	}
	handleValue := C.LLVMBuildZExt(s.builder, tagValue, uintptrLLVMType, cStringFree("packed.inline.tag.zext"))
	handleValue = C.LLVMBuildShl(s.builder, handleValue, C.LLVMConstInt(uintptrLLVMType, 49, 0), cStringFree("packed.inline.tag.shift"))
	if payloadValue != nil && payloadType != nil {
		payloadBits := C.LLVMBuildZExt(s.builder, payloadValue, uintptrLLVMType, cStringFree("packed.inline.payload.zext"))
		payloadBits = C.LLVMBuildShl(s.builder, payloadBits, C.LLVMConstInt(uintptrLLVMType, 1, 0), cStringFree("packed.inline.payload.shift"))
		handleValue = C.LLVMBuildOr(s.builder, handleValue, payloadBits, cStringFree("packed.inline.payload.or"))
	}
	return C.LLVMBuildOr(s.builder, handleValue, C.LLVMConstInt(uintptrLLVMType, 1, 0), cStringFree("packed.inline.handle")), nil
}
func (s *functionState) emitPackedStoreRecordTag(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, tagValue C.LLVMValueRef) error {
	if storeType == nil {
		return fmt.Errorf("missing packed store type for tag record")
	}
	ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}
	return ops.recordTag(tagValue, "packed.tag.record")
}

type packedEnumTailPayloadPlan struct {
	index         int
	viewType      *semantic.ViewType
	elemSizeValue C.LLVMValueRef
	lenValue      C.LLVMValueRef
	byteCount     C.LLVMValueRef
	sourceData    C.LLVMValueRef
	literal       *ast.ListLitExpr
}

func (s *functionState) preparePackedEnumTailPayloadPlan(variant *semantic.EnumVariant, orderedArgs []ast.Expr) (*packedEnumTailPayloadPlan, error) {
	if variant == nil {
		return nil, nil
	}
	tailIndex, ok := variant.TailPayloadIndex()
	if !ok {
		return nil, nil
	}
	viewType, ok := variant.TailPayloadViewType()
	if !ok || viewType == nil {
		return nil, fmt.Errorf("packed enum %s tail payload metadata is inconsistent", variant.Name)
	}
	if tailIndex >= len(orderedArgs) {
		return nil, fmt.Errorf("packed enum %s tail payload argument is missing", variant.Name)
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	elemSize, err := s.sizeOfType(viewType.Elem)
	if err != nil {
		return nil, err
	}
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	arg := orderedArgs[tailIndex]
	if literal, ok := arg.(*ast.ListLitExpr); ok {
		lenValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(len(literal.Elems)), 0)
		byteCount := C.LLVMConstInt(usizeLLVMType, C.ulonglong(uint64(len(literal.Elems))*elemSize), 0)
		return &packedEnumTailPayloadPlan{index: tailIndex, viewType: viewType, elemSizeValue: elemSizeValue, lenValue: lenValue, byteCount: byteCount, literal: literal}, nil
	}
	sourceType := s.exprType(arg)
	if _, ok := sourceType.(*semantic.ViewType); !ok {
		if _, ok := sourceType.(*semantic.ViewType); !ok {
			return nil, fmt.Errorf("packed enum %s tail payload expects a list literal or view-compatible source, got %s", variant.Name, sourceType.String())
		}
	}
	viewValue, _, err := s.emitExpr(arg, sourceType)
	if err != nil {
		return nil, err
	}
	lenValue := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("packed.tail.src.len"))
	sourceData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("packed.tail.src.data"))
	byteCount, err := s.emitCheckedElemByteCount(lenValue, elemSize, "packed.tail")
	if err != nil {
		return nil, err
	}
	return &packedEnumTailPayloadPlan{index: tailIndex, viewType: viewType, elemSizeValue: elemSizeValue, lenValue: lenValue, byteCount: byteCount, sourceData: sourceData}, nil
}
func (s *functionState) emitPackedEnumTailDataPtr(allocPtr C.LLVMValueRef, rowSizeValue C.LLVMValueRef) (C.LLVMValueRef, error) {
	i8Type, err := s.g.lowerBuiltin("u8")
	if err != nil {
		return nil, err
	}
	indices := []C.LLVMValueRef{rowSizeValue}
	return C.LLVMBuildGEP2(s.builder, i8Type, allocPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("packed.tail.data")), nil
}
func (s *functionState) emitPackedEnumConstructorPayloadValue(variant *semantic.EnumVariant, index int, arg ast.Expr, tailPlan *packedEnumTailPayloadPlan, tailDataPtr C.LLVMValueRef, storeValue C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	if tailPlan != nil && index == tailPlan.index {
		return s.emitPackedEnumTailPayloadValue(tailPlan, tailDataPtr)
	}
	argValue, _, err := s.emitExpr(arg, variant.Payload[index])
	if err != nil {
		return nil, err
	}
	if nicheEnumType, ok := s.g.optionalEnumNicheField(variant.Payload[index]); ok {
		return s.packOptionalEnumNicheValue(argValue, nicheEnumType, "packed.enum.payload")
	}
	return s.emitPackedEnumPayloadDArrayAdopt(argValue, variant.Payload[index], storeValue, enumType)
}

// emitPackedEnumPayloadDArrayAdopt copies a darray payload's backing into the packed store's
// arena before the darray struct is written into the record. A side-table darray (`Call.arguments`,
// `Func.body`, ...) is usually BUILT in the constructing function's own region — region inference
// sees the ctor return only a u32 handle, so nothing marks the backing as escaping, and once the
// builder's region is freed the record holds a dangling data pointer (the comprehension-filter
// parse crash: the payload's `arguments` pointed one page past its freed chunk). The ctor consumes
// the darray affinely (consumeAffineValueExpr), so a copy cannot break aliasing; capacity clamps to
// count — a later grow re-allocates in the ambient region instead of writing the store arena.
// Elements are copied SHALLOWLY: handle/scalar/sview elements (every AST side-table today) are
// self-contained; an element type that itself owns region storage still needs its own adoption.
func (s *functionState) emitPackedEnumPayloadDArrayAdopt(argValue C.LLVMValueRef, payloadType semantic.Type, storeValue C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	darrayType, ok := payloadType.(*semantic.DArrayType)
	if !ok || darrayType == nil {
		return argValue, nil
	}
	if enumType == nil || enumType.StoreType == nil || storeValue == nil {
		return argValue, nil
	}
	elemSize, err := s.sizeOfType(darrayType.Elem)
	if err != nil {
		return nil, err
	}
	countValue := C.LLVMBuildExtractValue(s.builder, argValue, 1, cStringFree("packed.payload.adopt.count"))
	oldData := C.LLVMBuildExtractValue(s.builder, argValue, 0, cStringFree("packed.payload.adopt.src"))
	byteCount, err := s.emitCheckedElemByteCount(countValue, elemSize, "packed.payload.adopt")
	if err != nil {
		return nil, err
	}
	ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: enumType.StoreType}
	arenaValue, err := ops.arenaValue("packed.payload.adopt.arena")
	if err != nil {
		return nil, err
	}
	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	usizeType := s.g.result.NamedTypes["usize"]
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, err
	}
	newData := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaValue, byteCount}, "packed.payload.adopt.alloc")
	if err := s.emitPackedEnumTailMemcpy(newData, oldData, byteCount); err != nil {
		return nil, err
	}
	// DEEP adoption: the shallow memcpy above copies the element structs verbatim, so any OWNED
	// container field inside an element (e.g. `MatchArm.body: darray[Stmt]`) still points at the
	// builder's region, which frees on return — a use-after-free (the self-host next_token crash).
	// Recursively copy each such field's backing into the store arena too. sview/view/handle fields
	// are borrows into persistent buffers and need no adoption.
	if err := s.emitDeepAdoptElementContainers(newData, countValue, darrayType.Elem, arenaValue, allocLLVMType, allocCallee); err != nil {
		return nil, err
	}
	adopted := C.LLVMBuildInsertValue(s.builder, argValue, newData, 0, cStringFree("packed.payload.adopt.data"))
	adopted = C.LLVMBuildInsertValue(s.builder, adopted, countValue, 2, cStringFree("packed.payload.adopt.cap"))
	return adopted, nil
}
// emitDeepAdoptElementContainers, for a darray payload whose element is a STRUCT with owned
// container (darray) fields, copies each such field's backing into the store arena too — the
// recursive counterpart of the shallow memcpy in emitPackedEnumPayloadDArrayAdopt. Without it a
// nested field like `MatchArm.body: darray[Stmt]` keeps pointing at the builder's freed region
// (the self-host next_token use-after-free). sview/view/handle fields are borrows and are skipped.
// `base` points at the freshly-copied element array (count elements) in the store arena.
func (s *functionState) emitDeepAdoptElementContainers(base, count C.LLVMValueRef, elemType semantic.Type, arenaValue C.LLVMValueRef, allocLLVMType C.LLVMTypeRef, allocCallee C.LLVMValueRef) error {
	st, ok := elemType.(*semantic.StructType)
	if !ok || st == nil || st.Decl == nil {
		return nil
	}
	type ownedField struct {
		index    int
		darrayLL C.LLVMTypeRef
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
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLL, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	ptrLL := C.LLVMPointerTypeInContext(s.g.context, 0)
	iPtr := C.LLVMBuildAlloca(s.builder, usizeLL, cStringFree("adopt.deep.i"))
	C.LLVMBuildStore(s.builder, C.LLVMConstInt(usizeLL, 0, 0), iPtr)
	headerBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("adopt.deep.header"))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("adopt.deep.body"))
	exitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("adopt.deep.exit"))
	C.LLVMBuildBr(s.builder, headerBB)
	C.LLVMPositionBuilderAtEnd(s.builder, headerBB)
	iVal := C.LLVMBuildLoad2(s.builder, usizeLL, iPtr, cStringFree("adopt.deep.i.load"))
	cond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), iVal, count, cStringFree("adopt.deep.cond"))
	C.LLVMBuildCondBr(s.builder, cond, bodyBB, exitBB)
	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	elemIndices := []C.LLVMValueRef{iVal}
	elemPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, base, llvmValueSlicePtr(elemIndices), C.unsigned(len(elemIndices)), cStringFree("adopt.deep.elem"))
	for _, of := range owned {
		fieldPtr := C.LLVMBuildStructGEP2(s.builder, elemLLVMType, elemPtr, C.unsigned(of.index), cStringFree("adopt.deep.field"))
		dataPtr := C.LLVMBuildStructGEP2(s.builder, of.darrayLL, fieldPtr, 0, cStringFree("adopt.deep.fdata.ptr"))
		countPtr := C.LLVMBuildStructGEP2(s.builder, of.darrayLL, fieldPtr, 1, cStringFree("adopt.deep.fcount.ptr"))
		capPtr := C.LLVMBuildStructGEP2(s.builder, of.darrayLL, fieldPtr, 2, cStringFree("adopt.deep.fcap.ptr"))
		fdata := C.LLVMBuildLoad2(s.builder, ptrLL, dataPtr, cStringFree("adopt.deep.fdata"))
		fcount := C.LLVMBuildLoad2(s.builder, usizeLL, countPtr, cStringFree("adopt.deep.fcount"))
		fbytes, err := s.emitCheckedElemByteCount(fcount, of.elemSize, "adopt.deep.field")
		if err != nil {
			return err
		}
		fnew := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaValue, fbytes}, "adopt.deep.falloc")
		if err := s.emitPackedEnumTailMemcpy(fnew, fdata, fbytes); err != nil {
			return err
		}
		C.LLVMBuildStore(s.builder, fnew, dataPtr)
		C.LLVMBuildStore(s.builder, fcount, capPtr)
		// Recurse: a field whose elements are themselves structs with owned containers.
		if err := s.emitDeepAdoptElementContainers(fnew, fcount, of.nested, arenaValue, allocLLVMType, allocCallee); err != nil {
			return err
		}
	}
	iNext := C.LLVMBuildAdd(s.builder, iVal, C.LLVMConstInt(usizeLL, 1, 0), cStringFree("adopt.deep.i.next"))
	C.LLVMBuildStore(s.builder, iNext, iPtr)
	C.LLVMBuildBr(s.builder, headerBB)
	C.LLVMPositionBuilderAtEnd(s.builder, exitBB)
	return nil
}

func (s *functionState) emitPackedEnumTailPayloadValue(plan *packedEnumTailPayloadPlan, tailDataPtr C.LLVMValueRef) (C.LLVMValueRef, error) {
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
		usizeType := s.g.result.NamedTypes["usize"]
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, err
		}
		for i, elem := range plan.literal.Elems {
			elemValue, _, err := s.emitExpr(elem, plan.viewType.Elem)
			if err != nil {
				return nil, err
			}
			indexValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(i), 0)
			indices := []C.LLVMValueRef{indexValue}
			elemPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, tailDataPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("packed.tail.elem.ptr"))
			C.LLVMBuildStore(s.builder, elemValue, elemPtr)
		}
	} else {
		if err := s.emitPackedEnumTailMemcpy(tailDataPtr, plan.sourceData, plan.byteCount); err != nil {
			return nil, err
		}
	}
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, tailDataPtr, 0, cStringFree("packed.tail.view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, plan.lenValue, 1, cStringFree("packed.tail.view.len"))
	return viewValue, nil
}
func (s *functionState) emitPackedEnumTailMemcpy(dstData C.LLVMValueRef, srcData C.LLVMValueRef, byteCount C.LLVMValueRef) error {
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	usizeType := s.g.result.NamedTypes["usize"]
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
	memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{dstData, srcData, byteCount}, "packed.tail.memcpy")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(1), "noalias")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(2), "noalias")
	return nil
}
func (s *functionState) emitPackedEnumStorageAlloc(storeValue C.LLVMValueRef, enumType *semantic.EnumType, tailPlan *packedEnumTailPayloadPlan, fixedTagValue C.LLVMValueRef) (C.LLVMValueRef, C.LLVMValueRef, C.LLVMValueRef, error) {
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
		totalSizeValue = C.LLVMBuildAdd(s.builder, rowSizeValue, tailPlan.byteCount, cStringFree("packed.alloc.bytes"))
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
