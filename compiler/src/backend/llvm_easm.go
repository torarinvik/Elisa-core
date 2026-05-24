//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
static LLVMValueRef elisacoreGetInlineAsmLocal(LLVMTypeRef Ty, const char* AsmString, size_t AsmStringSize, const char* Constraints, size_t ConstraintsSize, LLVMBool HasSideEffects, LLVMBool IsAlignStack) {
	return LLVMGetInlineAsm(Ty, (char*)AsmString, AsmStringSize, (char*)Constraints, ConstraintsSize, HasSideEffects, IsAlignStack, LLVMInlineAsmDialectATT, 0);
}
*/
import "C"

import (
	"elisacore/src/easm"
	"elisacore/src/semantic"
	"fmt"
	"strings"
	"unsafe"
)

func (g *llvmGenerator) emitEASMModule(module *easm.Module) error {
	if module == nil {
		return nil
	}
	for i := range module.Functions {
		if err := g.emitEASMFunction(&module.Functions[i]); err != nil {
			return err
		}
	}
	return nil
}

func (g *llvmGenerator) emitEASMFunction(fn *easm.Function) error {
	if fn == nil {
		return nil
	}
	_, semanticType, err := g.lowerEASMFunctionType(fn)
	if err != nil {
		return fmt.Errorf("EASM %s: %w", fn.Name, err)
	}
	value, err := g.ensureFunctionDeclared(fn.Name, semanticType)
	if err != nil {
		return fmt.Errorf("EASM %s extern signature mismatch: %w", fn.Name, err)
	}
	if C.LLVMCountBasicBlocks(value) != 0 {
		return fmt.Errorf("EASM %s conflicts with an already-defined function", fn.Name)
	}
	entry := C.LLVMAppendBasicBlockInContext(g.context, value, cStringFree("entry"))
	builder := C.LLVMCreateBuilderInContext(g.context)
	defer C.LLVMDisposeBuilder(builder)
	C.LLVMPositionBuilderAtEnd(builder, entry)

	asmText := easmInlineAsmText(fn)
	constraints := easmInlineAsmConstraints(fn)
	asmC := cString(asmText)
	defer C.free(unsafe.Pointer(asmC))
	constraintsC := cString(constraints)
	defer C.free(unsafe.Pointer(constraintsC))
	inlineAsmType, err := g.easmInlineAsmFunctionType(fn, semanticType)
	if err != nil {
		return err
	}
	inlineAsm := C.elisacoreGetInlineAsmLocal(inlineAsmType, asmC, C.size_t(len(asmText)), constraintsC, C.size_t(len(constraints)), 1, easmStackAlignFlag(fn))
	args := make([]C.LLVMValueRef, len(fn.Params))
	for i := range fn.Params {
		args[i] = C.LLVMGetParam(value, C.unsigned(i))
	}
	if len(args) == 0 {
		i32Type := C.LLVMInt32TypeInContext(g.context)
		args = append(args, C.LLVMConstInt(i32Type, 0, 0))
	}
	callName := "easm.call"
	argPtr := llvmValueSlicePtr(args)
	var dummyArg C.LLVMValueRef
	if len(args) == 0 {
		argPtr = &dummyArg
	}
	call := C.LLVMBuildCall2(builder, inlineAsmType, inlineAsm, argPtr, C.unsigned(len(args)), cStringFree(callName))
	if easmHasControl(fn, "noreturn") {
		C.LLVMBuildUnreachable(builder)
		return nil
	}
	if strings.TrimSpace(fn.ReturnType) == "void" {
		C.LLVMBuildRetVoid(builder)
	} else {
		C.LLVMBuildRet(builder, call)
	}
	return nil
}

func (g *llvmGenerator) easmInlineAsmFunctionType(fn *easm.Function, declared *semantic.FuncType) (C.LLVMTypeRef, error) {
	if strings.TrimSpace(fn.ReturnType) != "void" && len(declared.Params) != 0 {
		return g.lowerFunctionType(declared)
	}
	params := append([]semantic.Type(nil), declared.Params...)
	if len(params) == 0 {
		params = append(params, &semantic.BuiltinType{Name: "i32"})
	}
	if strings.TrimSpace(fn.ReturnType) != "void" {
		dummy := &semantic.FuncType{Name: fn.Name + "$easm_inline", Params: params, Return: declared.Return, CallConv: declared.CallConv}
		return g.lowerFunctionType(dummy)
	}
	dummy := &semantic.FuncType{Name: fn.Name + "$easm_inline", Params: params, Return: &semantic.BuiltinType{Name: "i32"}, CallConv: declared.CallConv}
	return g.lowerFunctionType(dummy)
}

func (g *llvmGenerator) lowerEASMFunctionType(fn *easm.Function) (C.LLVMTypeRef, *semantic.FuncType, error) {
	params := make([]semantic.Type, 0, len(fn.Params))
	for _, param := range fn.Params {
		t, err := g.easmSemanticType(param.Type)
		if err != nil {
			return nil, nil, err
		}
		params = append(params, t)
	}
	ret, err := g.easmSemanticType(fn.ReturnType)
	if err != nil {
		return nil, nil, err
	}
	semanticType := &semantic.FuncType{Name: fn.Name, Params: params, Return: ret, CallConv: easmCallConv(fn.ABI)}
	llvmType, err := g.lowerFunctionType(semanticType)
	return llvmType, semanticType, err
}

func (g *llvmGenerator) easmSemanticType(name string) (semantic.Type, error) {
	name = strings.TrimSpace(name)
	switch name {
	case "void", "bool", "char", "int", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "usize", "uintptr", "f32", "f64":
		if t, ok := g.result.NamedTypes[name]; ok {
			return t, nil
		}
		return &semantic.BuiltinType{Name: name}, nil
	default:
		if strings.HasSuffix(name, "&?") || strings.HasSuffix(name, "&") {
			return g.result.NamedTypes["void"], fmt.Errorf("EASM v1 only accepts primitive value types in signatures, got %s", name)
		}
		if t, ok := g.result.NamedTypes[name]; ok {
			return t, nil
		}
		return nil, fmt.Errorf("unknown EASM type %s", name)
	}
}

func easmCallConv(abi string) string {
	switch strings.ToLower(strings.TrimSpace(abi)) {
	case "", "c", "sysv", "sysv_x86_64", "ps4_sysv":
		return "c"
	default:
		return abi
	}
}

func easmInlineAsmText(fn *easm.Function) string {
	lines := make([]string, 0, len(fn.Instructions))
	for _, inst := range fn.Instructions {
		lines = append(lines, inst.Text)
	}
	return strings.Join(lines, "\n")
}

func easmInlineAsmConstraints(fn *easm.Function) string {
	var parts []string
	boundRegisters := map[string]bool{}
	if strings.TrimSpace(fn.ReturnType) == "void" {
		parts = append(parts, "=r")
	} else {
		constraint, reg := easmOutputConstraint(fn)
		parts = append(parts, constraint)
		if reg != "" {
			boundRegisters[reg] = true
		}
	}
	for _, input := range fn.Inputs {
		constraint, reg := easmInputConstraint(input)
		parts = append(parts, constraint)
		if reg != "" {
			boundRegisters[reg] = true
		}
	}
	if len(fn.Params) == 0 {
		parts = append(parts, "r")
	}
	for len(parts) < outputConstraintCount(fn)+len(fn.Params) {
		parts = append(parts, "r")
	}
	for _, clobber := range fn.Clobbers {
		for _, item := range strings.FieldsFunc(clobber, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			item = strings.ToLower(strings.TrimSpace(item))
			if item == "" {
				continue
			}
			if item == "memory" || item == "cc" {
				parts = append(parts, "~{"+item+"}")
				continue
			}
			item = strings.TrimPrefix(item, "%")
			if boundRegisters[item] {
				continue
			}
			parts = append(parts, "~{"+item+"}")
		}
	}
	return strings.Join(parts, ",")
}

func outputConstraintCount(fn *easm.Function) int {
	return 1
}

func easmOutputConstraint(fn *easm.Function) (string, string) {
	for _, output := range fn.Outputs {
		if reg := registerAfterEquals(output); reg != "" {
			return "={" + reg + "}", reg
		}
	}
	return "=r", ""
}

func easmInputConstraint(input string) (string, string) {
	if reg := registerAfterEquals(input); reg != "" {
		return "{" + reg + "}", reg
	}
	fields := strings.Fields(input)
	if len(fields) > 0 {
		last := strings.TrimPrefix(strings.ToLower(fields[len(fields)-1]), "%")
		if isRegisterName(last) {
			return "{" + last + "}", last
		}
	}
	return "r", ""
}

func registerAfterEquals(value string) string {
	if i := strings.LastIndex(value, "="); i >= 0 {
		reg := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value[i+1:])), "%")
		if isRegisterName(reg) {
			return reg
		}
	}
	return ""
}

func isRegisterName(value string) bool {
	switch value {
	case "rax", "rbx", "rcx", "rdx", "rsi", "rdi", "rsp", "rbp", "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15",
		"eax", "ebx", "ecx", "edx", "esi", "edi", "esp", "ebp":
		return true
	default:
		return false
	}
}

func easmHasControl(fn *easm.Function, control string) bool {
	for _, item := range fn.Control {
		for _, field := range strings.FieldsFunc(item, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			if strings.EqualFold(field, control) {
				return true
			}
		}
	}
	return false
}

func easmStackAlignFlag(fn *easm.Function) C.LLVMBool {
	for _, item := range fn.Stack {
		if strings.Contains(strings.ToLower(item), "aligned") {
			return 1
		}
	}
	return 0
}
