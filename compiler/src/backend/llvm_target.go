//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/BitWriter.h>
#include <llvm-c/Core.h>
#include <llvm-c/Error.h>
#include <llvm-c/Target.h>
#include <llvm-c/TargetMachine.h>
#include <llvm-c/Transforms/PassBuilder.h>

static LLVMBool llcontextInitializeNativeTarget(void) {
	return LLVMInitializeNativeTarget();
}

static LLVMBool llcontextInitializeNativeAsmPrinter(void) {
	return LLVMInitializeNativeAsmPrinter();
}

static LLVMTargetMachineRef llcontextCreateTargetMachineAggressive(
	LLVMTargetRef Target,
	char *Triple,
	char *CPU,
	char *Features) {
	return LLVMCreateTargetMachine(
		Target,
		Triple,
		CPU,
		Features,
		LLVMCodeGenLevelAggressive,
		LLVMRelocDefault,
		LLVMCodeModelDefault);
}

static char *llcontextRunOptimizationPipeline(
	LLVMModuleRef Module,
	LLVMTargetMachineRef TargetMachine,
	char *PassPipeline) {
	LLVMPassBuilderOptionsRef Options = LLVMCreatePassBuilderOptions();
	if (Options == NULL) {
		return LLVMGetErrorMessage(LLVMCreateStringError("failed to create LLVM pass builder options"));
	}
	LLVMPassBuilderOptionsSetVerifyEach(Options, 1);
	LLVMPassBuilderOptionsSetLoopInterleaving(Options, 1);
	LLVMPassBuilderOptionsSetLoopVectorization(Options, 1);
	LLVMPassBuilderOptionsSetSLPVectorization(Options, 1);
	LLVMPassBuilderOptionsSetLoopUnrolling(Options, 1);
	LLVMErrorRef Err = LLVMRunPasses(Module, PassPipeline, TargetMachine, Options);
	LLVMDisposePassBuilderOptions(Options);
	if (Err == NULL) {
		return NULL;
	}
	return LLVMGetErrorMessage(Err);
}

static void llcontextDisposeLLVMErrorMessage(char *ErrMsg) {
	if (ErrMsg != NULL) {
		LLVMDisposeErrorMessage(ErrMsg);
	}
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
	return WriteLLVMBitcodeFileWithOpt(result, outputPath, OptimizationLevel3)
}

func WriteLLVMBitcodeFileWithOpt(result *semantic.Result, outputPath string, optLevel OptimizationLevel) error {
	return WriteLLVMBitcodeFileWithOptAndPackedLoweringProfile(result, outputPath, optLevel, DefaultPackedLoweringProfile())
}

func WriteLLVMBitcodeFileWithOptAndPackedLoweringProfile(result *semantic.Result, outputPath string, optLevel OptimizationLevel, profile PackedLoweringProfile) error {
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("output path cannot be empty")
	}
	g, err := compileLLVMModule(result, optLevel, profile)
	if err != nil {
		return err
	}
	defer g.dispose()
	if err := g.optimizeModule(optLevel); err != nil {
		return err
	}
	return g.writeBitcodeFile(outputPath)
}

func WriteLLVMBitcodeFileWithOptAndPackedABI(result *semantic.Result, outputPath string, optLevel OptimizationLevel, packedABI PackedEnumABI) error {
	profile, err := LegacyPackedLoweringProfile(packedABI)
	if err != nil {
		return err
	}
	return WriteLLVMBitcodeFileWithOptAndPackedLoweringProfile(result, outputPath, optLevel, profile)
}

func WriteLLVMObjectFile(result *semantic.Result, outputPath string) error {
	return WriteLLVMObjectFileWithOpt(result, outputPath, OptimizationLevel3)
}

func WriteLLVMObjectFileWithOpt(result *semantic.Result, outputPath string, optLevel OptimizationLevel) error {
	return WriteLLVMObjectFileWithOptAndPackedLoweringProfile(result, outputPath, optLevel, DefaultPackedLoweringProfile())
}

func WriteLLVMObjectFileWithOptAndPackedLoweringProfile(result *semantic.Result, outputPath string, optLevel OptimizationLevel, profile PackedLoweringProfile) error {
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("output path cannot be empty")
	}
	g, err := compileLLVMModule(result, optLevel, profile)
	if err != nil {
		return err
	}
	defer g.dispose()
	return g.writeObjectFile(outputPath, optLevel)
}

func WriteLLVMObjectFileWithOptAndPackedABI(result *semantic.Result, outputPath string, optLevel OptimizationLevel, packedABI PackedEnumABI) error {
	profile, err := LegacyPackedLoweringProfile(packedABI)
	if err != nil {
		return err
	}
	return WriteLLVMObjectFileWithOptAndPackedLoweringProfile(result, outputPath, optLevel, profile)
}

func (g *llvmGenerator) writeBitcodeFile(outputPath string) error {
	pathC := cString(outputPath)
	defer C.free(unsafe.Pointer(pathC))
	if C.LLVMWriteBitcodeToFile(g.module, pathC) != 0 {
		return fmt.Errorf("failed to write LLVM bitcode to %s", outputPath)
	}
	return nil
}

func (g *llvmGenerator) writeObjectFile(outputPath string, optLevel OptimizationLevel) error {
	if err := g.ensureTargetMachine(); err != nil {
		return err
	}
	if err := g.optimizeModule(optLevel); err != nil {
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

func (g *llvmGenerator) optimizeModule(optLevel OptimizationLevel) error {
	if optLevel == OptimizationLevel0 {
		return nil
	}
	if g.optimizedForCodegen {
		return nil
	}
	if g.module == nil {
		return fmt.Errorf("cannot optimize a nil LLVM module")
	}
	if err := g.ensureTargetMachine(); err != nil {
		return err
	}

	passPipeline := cString(fmt.Sprintf("default<O%d>", int(optLevel)))
	defer C.free(unsafe.Pointer(passPipeline))
	errMessage := C.llcontextRunOptimizationPipeline(g.module, g.targetMachine, passPipeline)
	if errMessage != nil {
		return fmt.Errorf("failed to optimize LLVM module for code generation: %s", disposeLLVMErrorMessage(errMessage, "unknown LLVM pass pipeline error"))
	}
	if err := g.verify(); err != nil {
		return fmt.Errorf("optimized LLVM module failed verification: %w", err)
	}
	g.optimizedForCodegen = true
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
	tm := C.llcontextCreateTargetMachineAggressive(target, triple, cpu, features)
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
	size, err := g.abiSizeOfLLVMType(llvmType)
	if err != nil {
		return 0, err
	}
	return size, nil
}

func (g *llvmGenerator) abiSizeOfLLVMType(llvmType C.LLVMTypeRef) (uint64, error) {
	if err := g.ensureTargetMachine(); err != nil {
		return 0, err
	}
	return uint64(C.LLVMABISizeOfType(g.targetData, llvmType)), nil
}

func (g *llvmGenerator) abiOffsetOfLLVMElement(llvmType C.LLVMTypeRef, elementIndex int) (uint64, error) {
	if llvmType == nil {
		return 0, fmt.Errorf("cannot compute ABI offset for nil LLVM type")
	}
	if elementIndex < 0 {
		return 0, fmt.Errorf("cannot compute ABI offset for negative element index %d", elementIndex)
	}
	if err := g.ensureTargetMachine(); err != nil {
		return 0, err
	}
	return uint64(C.LLVMOffsetOfElement(g.targetData, llvmType, C.unsigned(elementIndex))), nil
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

func disposeLLVMErrorMessage(message *C.char, fallback string) string {
	if message == nil {
		return fallback
	}
	text := strings.TrimSpace(C.GoString(message))
	C.llcontextDisposeLLVMErrorMessage(message)
	if text == "" {
		return fallback
	}
	return text
}
