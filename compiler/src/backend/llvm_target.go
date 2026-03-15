//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/BitWriter.h>
#include <llvm-c/Core.h>
#include <llvm-c/Target.h>
#include <llvm-c/TargetMachine.h>

static LLVMBool llcontextInitializeNativeTarget(void) {
	return LLVMInitializeNativeTarget();
}

static LLVMBool llcontextInitializeNativeAsmPrinter(void) {
	return LLVMInitializeNativeAsmPrinter();
}
*/
import "C"

import (
	"fmt"
	"strings"
	"unsafe"

	"llcontext/src/semantic"
)

func WriteLLVMBitcodeFile(result *semantic.Result, outputPath string) error {
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("output path cannot be empty")
	}
	g, err := compileLLVMModule(result)
	if err != nil {
		return err
	}
	defer g.dispose()
	return g.writeBitcodeFile(outputPath)
}

func WriteLLVMObjectFile(result *semantic.Result, outputPath string) error {
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("output path cannot be empty")
	}
	g, err := compileLLVMModule(result)
	if err != nil {
		return err
	}
	defer g.dispose()
	return g.writeObjectFile(outputPath)
}

func (g *llvmGenerator) writeBitcodeFile(outputPath string) error {
	pathC := cString(outputPath)
	defer C.free(unsafe.Pointer(pathC))
	if C.LLVMWriteBitcodeToFile(g.module, pathC) != 0 {
		return fmt.Errorf("failed to write LLVM bitcode to %s", outputPath)
	}
	return nil
}

func (g *llvmGenerator) writeObjectFile(outputPath string) error {
	if err := g.ensureTargetMachine(); err != nil {
		return err
	}

	pathC := cString(outputPath)
	defer C.free(unsafe.Pointer(pathC))

	var message *C.char
	if C.LLVMTargetMachineEmitToFile(g.targetMachine, g.module, pathC, C.LLVMObjectFile, &message) != 0 {
		return fmt.Errorf("failed to write LLVM object file to %s: %s", outputPath, disposeLLVMMessage(message, "unknown LLVM target emission error"))
	}
	return nil
}

func (g *llvmGenerator) ensureTargetMachine() error {
	if g.targetMachine != nil && g.targetData != nil && g.targetTriple != nil {
		return nil
	}
	if C.llcontextInitializeNativeTarget() != 0 {
		return fmt.Errorf("failed to initialize native LLVM target")
	}
	if C.llcontextInitializeNativeAsmPrinter() != 0 {
		return fmt.Errorf("failed to initialize native LLVM asm printer")
	}

	triple := C.LLVMGetDefaultTargetTriple()
	if triple == nil {
		return fmt.Errorf("failed to determine default target triple")
	}

	var target C.LLVMTargetRef
	var message *C.char
	if C.LLVMGetTargetFromTriple(triple, &target, &message) != 0 {
		errText := disposeLLVMMessage(message, "unknown LLVM target lookup error")
		tripleText := C.GoString(triple)
		C.LLVMDisposeMessage(triple)
		return fmt.Errorf("failed to resolve LLVM target %q: %s", tripleText, errText)
	}

	cpu := cString("")
	features := cString("")
	tm := C.LLVMCreateTargetMachine(target, triple, cpu, features, C.LLVMCodeGenLevelDefault, C.LLVMRelocDefault, C.LLVMCodeModelDefault)
	C.free(unsafe.Pointer(cpu))
	C.free(unsafe.Pointer(features))
	if tm == nil {
		tripleText := C.GoString(triple)
		C.LLVMDisposeMessage(triple)
		return fmt.Errorf("failed to create LLVM target machine for %s", tripleText)
	}

	dataLayout := C.LLVMCreateTargetDataLayout(tm)
	layoutText := C.LLVMCopyStringRepOfTargetData(dataLayout)
	C.LLVMSetDataLayout(g.module, layoutText)
	C.LLVMSetTarget(g.module, triple)
	C.LLVMDisposeMessage(layoutText)
	g.targetMachine = tm
	g.targetData = dataLayout
	g.targetTriple = triple
	return nil
}

func (g *llvmGenerator) abiSizeOfType(t semantic.Type) (uint64, error) {
	if isVoidType(t) {
		return 0, nil
	}
	if err := g.ensureTargetMachine(); err != nil {
		return 0, err
	}
	llvmType, err := g.lowerType(t)
	if err != nil {
		return 0, err
	}
	return uint64(C.LLVMABISizeOfType(g.targetData, llvmType)), nil
}

func disposeLLVMMessage(message *C.char, fallback string) string {
	if message == nil {
		return fallback
	}
	text := strings.TrimSpace(C.GoString(message))
	C.LLVMDisposeMessage(message)
	if text == "" {
		return fallback
	}
	return text
}
