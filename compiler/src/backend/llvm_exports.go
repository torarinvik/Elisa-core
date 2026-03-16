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

	"llcontext/src/semantic"
)

type exportABILowering struct {
	llvmType      C.LLVMTypeRef
	needsCoercion bool
}

func (g *llvmGenerator) emitExportedGlobal(exported *semantic.ExportedGlobal) error {
	if exported == nil {
		return nil
	}
	target, err := g.ensureGlobalDeclared(exported.TargetName, exported.Type, false)
	if err != nil {
		return err
	}
	if exported.PublicName == exported.TargetName {
		g.globals[exported.PublicName] = target
		return nil
	}
	if _, ok := g.globals[exported.PublicName]; ok {
		return nil
	}
	llvmType, err := g.lowerType(exported.Type)
	if err != nil {
		return err
	}
	nameC := cString(exported.PublicName)
	defer C.free(unsafe.Pointer(nameC))
	alias := C.LLVMAddAlias2(g.module, llvmType, 0, target, nameC)
	C.LLVMSetLinkage(alias, C.LLVMExternalLinkage)
	g.globals[exported.PublicName] = alias
	return nil
}

func (g *llvmGenerator) emitExportedFunction(exported *semantic.ExportedFunc) error {
	if exported == nil || exported.Signature == nil {
		return nil
	}
	if err := g.ensureTargetMachine(); err != nil {
		return err
	}
	paramLowerings := make([]exportABILowering, 0, len(exported.Signature.Params))
	paramTypes := make([]C.LLVMTypeRef, 0, len(exported.Signature.Params))
	for _, param := range exported.Signature.Params {
		lowering, err := g.exportABILowering(param)
		if err != nil {
			return fmt.Errorf("export wrapper %s parameter %s is not supported by the current ABI lowering: %w", exported.PublicName, param.String(), err)
		}
		paramLowerings = append(paramLowerings, lowering)
		paramTypes = append(paramTypes, lowering.llvmType)
	}
	returnLowering, err := g.exportABILowering(exported.Signature.Return)
	if err != nil {
		return fmt.Errorf("export wrapper %s return %s is not supported by the current ABI lowering: %w", exported.PublicName, exported.Signature.Return.String(), err)
	}
	fnType := C.LLVMFunctionType(returnLowering.llvmType, llvmTypeSlicePtr(paramTypes), C.unsigned(len(paramTypes)), 0)
	fnValue, err := g.ensureExportFunctionDeclared(exported.PublicName, fnType)
	if err != nil {
		return err
	}
	if C.LLVMCountBasicBlocks(fnValue) != 0 {
		return nil
	}

	var (
		targetValue C.LLVMValueRef
		targetType  *semantic.FuncType
	)
	if exported.TargetGenericDecl != nil && len(exported.TargetBindings) > 0 {
		targetValue, targetType, err = g.ensureSpecializedFunction(exported.TargetGenericDecl, exported.TargetBase, exported.TargetBindings)
		if err != nil {
			return err
		}
	} else {
		targetType = exported.TargetSpecialized
		targetValue, err = g.ensureFunctionDeclared(exported.TargetName, targetType)
		if err != nil {
			return err
		}
	}

	builder := C.LLVMCreateBuilderInContext(g.context)
	defer C.LLVMDisposeBuilder(builder)

	entryName := cString("entry")
	defer C.free(unsafe.Pointer(entryName))
	entry := C.LLVMAppendBasicBlockInContext(g.context, fnValue, entryName)
	C.LLVMPositionBuilderAtEnd(builder, entry)

	llvmTargetType, err := g.lowerFunctionType(targetType)
	if err != nil {
		return err
	}
	args := make([]C.LLVMValueRef, 0, len(exported.Signature.Params))
	for i, paramType := range exported.Signature.Params {
		arg := C.LLVMGetParam(fnValue, C.unsigned(i))
		unpacked, err := g.unpackExportABIValue(builder, arg, paramLowerings[i], paramType, fmt.Sprintf("export.arg.%d", i))
		if err != nil {
			return err
		}
		args = append(args, unpacked)
	}
	callName := ""
	if !isVoidType(targetType.Return) {
		callName = "export.call"
	}
	call := C.LLVMBuildCall2(builder, llvmTargetType, targetValue, llvmValueSlicePtr(args), C.unsigned(len(args)), cStringFree(callName))
	if isVoidType(targetType.Return) {
		C.LLVMBuildRetVoid(builder)
		return nil
	}
	if !semantic.SameType(exported.Signature.Return, targetType.Return) {
		return fmt.Errorf("export wrapper %s return type %s does not match target return type %s", exported.PublicName, exported.Signature.Return.String(), targetType.Return.String())
	}
	packed, err := g.packExportABIValue(builder, call, targetType.Return, returnLowering, "export.ret")
	if err != nil {
		return err
	}
	C.LLVMBuildRet(builder, packed)
	return nil
}

func (g *llvmGenerator) ensureExportFunctionDeclared(name string, fnType C.LLVMTypeRef) (C.LLVMValueRef, error) {
	if value, ok := g.functions[name]; ok {
		return value, nil
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	value := C.LLVMAddFunction(g.module, nameC, fnType)
	C.LLVMSetLinkage(value, C.LLVMExternalLinkage)
	g.functions[name] = value
	return value, nil
}

func (g *llvmGenerator) exportABILowering(t semantic.Type) (exportABILowering, error) {
	if isVoidType(t) {
		llvmType, err := g.lowerType(t)
		return exportABILowering{llvmType: llvmType, needsCoercion: false}, err
	}
	switch t.(type) {
	case *semantic.BuiltinType, *semantic.RefType:
		llvmType, err := g.lowerType(t)
		return exportABILowering{llvmType: llvmType, needsCoercion: false}, err
	case *semantic.ArrayType, *semantic.StructType, *semantic.GenericInstanceType:
		size, err := g.abiSizeOfType(t)
		if err != nil {
			return exportABILowering{}, err
		}
		if size == 0 {
			llvmType, err := g.lowerType(t)
			return exportABILowering{llvmType: llvmType, needsCoercion: false}, err
		}
		if size > 8 {
			return exportABILowering{}, fmt.Errorf("aggregate ABI lowering currently supports exported values up to 8 bytes, got %d bytes", size)
		}
		return exportABILowering{llvmType: C.LLVMIntTypeInContext(g.context, C.unsigned(size*8)), needsCoercion: true}, nil
	default:
		llvmType, err := g.lowerType(t)
		if err != nil {
			return exportABILowering{}, err
		}
		return exportABILowering{llvmType: llvmType, needsCoercion: false}, nil
	}
}

func (g *llvmGenerator) unpackExportABIValue(builder C.LLVMBuilderRef, value C.LLVMValueRef, lowering exportABILowering, semanticType semantic.Type, name string) (C.LLVMValueRef, error) {
	if !lowering.needsCoercion {
		return value, nil
	}
	semanticLLVMType, err := g.lowerType(semanticType)
	if err != nil {
		return nil, err
	}
	storage := C.LLVMBuildAlloca(builder, lowering.llvmType, cStringFree(name+".abi"))
	C.LLVMBuildStore(builder, value, storage)
	castPtr := C.LLVMBuildBitCast(builder, storage, C.LLVMPointerTypeInContext(g.context, 0), cStringFree(name+".cast"))
	return C.LLVMBuildLoad2(builder, semanticLLVMType, castPtr, cStringFree(name+".load")), nil
}

func (g *llvmGenerator) packExportABIValue(builder C.LLVMBuilderRef, value C.LLVMValueRef, semanticType semantic.Type, lowering exportABILowering, name string) (C.LLVMValueRef, error) {
	if !lowering.needsCoercion {
		return value, nil
	}
	semanticLLVMType, err := g.lowerType(semanticType)
	if err != nil {
		return nil, err
	}
	storage := C.LLVMBuildAlloca(builder, semanticLLVMType, cStringFree(name+".semantic"))
	C.LLVMBuildStore(builder, value, storage)
	castPtr := C.LLVMBuildBitCast(builder, storage, C.LLVMPointerTypeInContext(g.context, 0), cStringFree(name+".cast"))
	return C.LLVMBuildLoad2(builder, lowering.llvmType, castPtr, cStringFree(name+".load")), nil
}
