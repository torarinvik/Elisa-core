//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
#include <llvm-c/Target.h>
*/
import "C"

import (
	"fmt"
	"unsafe"

	"elisacore/src/semantic"
)


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
	abi, err := g.exportFuncABI(exported.Signature)
	if err != nil {
		return fmt.Errorf("export wrapper %s: %w", exported.PublicName, err)
	}
	fnType := abi.fnType
	exportSymbol := exported.PublicName
	if exported.LinkName != "" {
		exportSymbol = exported.LinkName
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

	var fnValue C.LLVMValueRef
	if g.isSameNameExport(exported) {
		if !g.sameNameExportNeedsWrapper(exported) {
			return nil
		}
		nameC := cString(exportSymbol)
		defer C.free(unsafe.Pointer(nameC))
		if existing := C.LLVMGetNamedFunction(g.module, nameC); existing != nil {
			return nil
		}
		fnValue = C.LLVMAddFunction(g.module, nameC, fnType)
		C.LLVMSetLinkage(fnValue, C.LLVMExternalLinkage)
	} else {
		fnValue, err = g.ensureExportFunctionDeclared(exportSymbol, fnType)
		if err != nil {
			return err
		}
	}
	g.cAbiApplyAttrs(fnValue, abi)
	g.applyFunctionNoRecurseAttributes(fnValue, targetType)
	g.applyFunctionTemperatureAttributes(fnValue, targetType)
	if C.LLVMCountBasicBlocks(fnValue) != 0 {
		return nil
	}
	if len(targetType.ImplicitParamNames) != 0 {
		return fmt.Errorf("export wrapper %s has unsupported implicit parameters", exported.PublicName)
	}

	builder := C.LLVMCreateBuilderInContext(g.context)
	defer C.LLVMDisposeBuilder(builder)
	entry := C.LLVMAppendBasicBlockInContext(g.context, fnValue, cStringFree("entry"))
	C.LLVMPositionBuilderAtEnd(builder, entry)
	llvmTargetType, err := g.lowerFunctionType(targetType)
	if err != nil {
		return err
	}

	// Unpack each parameter from its ABI form into the value the implementation takes.
	pos := 0
	var sretPtr C.LLVMValueRef
	if abi.ret.sret {
		sretPtr = C.LLVMGetParam(fnValue, 0)
		pos = 1
	}
	args := make([]C.LLVMValueRef, 0, len(exported.Signature.Params))
	for i, plan := range abi.args {
		name := fmt.Sprintf("export.arg.%d", i)
		if !plan.direct {
			ptr := C.LLVMGetParam(fnValue, C.unsigned(pos))
			pos++
			args = append(args, C.LLVMBuildLoad2(builder, plan.llvmType, ptr, cStringFree(name)))
			continue
		}
		if !plan.aggregate {
			args = append(args, C.LLVMGetParam(fnValue, C.unsigned(pos)))
			pos++
			continue
		}
		bytes := uint64(C.LLVMABISizeOfType(g.targetData, plan.llvmType))
		for j, part := range plan.parts {
			if end := plan.partOffsets[j] + uint64(C.LLVMABISizeOfType(g.targetData, part)); end > bytes {
				bytes = end
			}
		}
		slot := g.cAbiSlot(builder, bytes, name+".slot")
		for j, part := range plan.parts {
			_ = part
			C.LLVMBuildStore(builder, C.LLVMGetParam(fnValue, C.unsigned(pos)), g.cAbiPtrAt(builder, slot, plan.partOffsets[j], name+".part"))
			pos++
		}
		args = append(args, C.LLVMBuildLoad2(builder, plan.llvmType, slot, cStringFree(name)))
	}

	callName := ""
	if !isVoidType(targetType.Return) {
		callName = "export.call"
	}
	callNameC := cString(callName)
	defer C.free(unsafe.Pointer(callNameC))
	var call C.LLVMValueRef
	var targetResult C.LLVMValueRef
	if retUnion, ok := nonVoidErrorUnion(targetType.Return); ok {
		resultLLVMType, err := g.lowerType(retUnion.Value)
		if err != nil {
			return err
		}
		resultSlot := C.LLVMBuildAlloca(builder, resultLLVMType, cStringFree("export.ret.slot"))
		C.LLVMBuildStore(builder, C.LLVMConstNull(resultLLVMType), resultSlot)
		callArgs := make([]C.LLVMValueRef, 0, len(args)+1)
		callArgs = append(callArgs, resultSlot)
		callArgs = append(callArgs, args...)
		call = C.LLVMBuildCall2(builder, llvmTargetType, targetValue, llvmValueSlicePtr(callArgs), C.unsigned(len(callArgs)), callNameC)
		unionLLVMType, err := g.lowerType(targetType.Return)
		if err != nil {
			return err
		}
		targetResult = C.LLVMGetUndef(unionLLVMType)
		targetResult = C.LLVMBuildInsertValue(builder, targetResult, call, 0, cStringFree("export.ret.err"))
		targetResult = C.LLVMBuildInsertValue(builder, targetResult, resultSlot, 1, cStringFree("export.ret.val.ptr"))
	} else {
		call = C.LLVMBuildCall2(builder, llvmTargetType, targetValue, llvmValueSlicePtr(args), C.unsigned(len(args)), callNameC)
		targetResult = call
	}
	if isVoidType(targetType.Return) {
		C.LLVMBuildRetVoid(builder)
		return nil
	}
	if !semantic.SameType(exported.Signature.Return, targetType.Return) {
		return fmt.Errorf("export wrapper %s return type %s does not match target return type %s", exported.PublicName, exported.Signature.Return.String(), targetType.Return.String())
	}
	// Pack the implementation's result into its ABI form.
	if abi.ret.sret {
		C.LLVMBuildStore(builder, targetResult, sretPtr)
		C.LLVMBuildRetVoid(builder)
		return nil
	}
	if !g.cAbiIsAggregate(exported.Signature.Return) || abi.ret.retType == abi.ret.llvmType {
		C.LLVMBuildRet(builder, targetResult)
		return nil
	}
	bytes := uint64(C.LLVMABISizeOfType(g.targetData, abi.ret.llvmType))
	if abiBytes := uint64(C.LLVMABISizeOfType(g.targetData, abi.ret.retType)); abiBytes > bytes {
		bytes = abiBytes
	}
	slot := g.cAbiSlot(builder, bytes, "export.ret.slot")
	C.LLVMBuildStore(builder, targetResult, slot)
	C.LLVMBuildRet(builder, C.LLVMBuildLoad2(builder, abi.ret.retType, slot, cStringFree("export.ret")))
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




// isSameNameExport reports whether an exported function's public name is its
// implementation's own name (`export fn foo(...) = foo`).
func (g *llvmGenerator) isSameNameExport(exported *semantic.ExportedFunc) bool {
	// TargetGenericDecl is set for plain functions too; a generic INSTANTIATION is
	// recognised by its bindings, exactly as emitExportedFunction does.
	return exported != nil && len(exported.TargetBindings) == 0 && exported.PublicName == exported.TargetName
}

// sameNameExportNeedsWrapper decides whether a same-name export can BE its
// implementation (direct: the implementation's lowered LLVM type equals the export
// ABI type) or needs a wrapper, in which case the implementation moves to
// `<name>.impl`. Compared by lowered type, not by a list of coercing cases, so a
// hidden parameter or an sret return is caught the same way as an aggregate coercion.
func (g *llvmGenerator) sameNameExportNeedsWrapper(exported *semantic.ExportedFunc) bool {
	if exported == nil || exported.Signature == nil {
		return false
	}
	if g.sameNameWrapperDecisions == nil {
		g.sameNameWrapperDecisions = map[*semantic.ExportedFunc]bool{}
	}
	if decided, ok := g.sameNameWrapperDecisions[exported]; ok {
		return decided
	}
	// Asked from llvmSymbolName while functions are being DECLARED, before the
	// export emitter's own ensureTargetMachine call; the ABI size query below needs
	// the target data, so bring it up here rather than inherit a half-initialised
	// generator. No answer is cached on failure: a wrapper is the safe default.
	if err := g.ensureTargetMachine(); err != nil {
		return true
	}
	decided := g.sameNameExportNeedsWrapperUncached(exported)
	g.sameNameWrapperDecisions[exported] = decided
	return decided
}

func (g *llvmGenerator) sameNameExportNeedsWrapperUncached(exported *semantic.ExportedFunc) bool {
	if exported == nil || exported.Signature == nil {
		return false
	}
	if exported.TargetSpecialized == nil {
		return false
	}
	if len(exported.TargetSpecialized.ImplicitParamNames) != 0 {
		return true
	}
	implType, err := g.lowerFunctionTypeForSymbol(exported.TargetName, exported.TargetSpecialized)
	if err != nil {
		return true
	}
	abi, err := g.exportFuncABI(exported.Signature)
	if err != nil {
		return true
	}
	if implType == abi.fnType {
		return false
	}
	return disposeLLVMMessage(C.LLVMPrintTypeToString(implType), "a") != disposeLLVMMessage(C.LLVMPrintTypeToString(abi.fnType), "b")
}

// sameNameExportImplSymbol returns the symbol an implementation is emitted under when
// a same-name export needs a wrapper: `<name>.impl`. Empty when `name` is not such a
// target, so llvmSymbolName falls through to its usual answer.
func (g *llvmGenerator) sameNameExportImplSymbol(name string) string {
	if g == nil || g.result == nil {
		return ""
	}
	for _, exported := range g.result.ExportedFuncs {
			if g.isSameNameExport(exported) && exported.TargetName == name {
			if g.sameNameExportNeedsWrapper(exported) {
				return name + ".impl"
			}
		}
	}
	return ""
}
