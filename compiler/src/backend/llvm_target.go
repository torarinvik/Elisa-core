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

static LLVMBool elisacoreInitializeNativeTarget(void) {
	return LLVMInitializeNativeTarget();
}

static LLVMBool elisacoreInitializeNativeAsmPrinter(void) {
	return LLVMInitializeNativeAsmPrinter();
}

static void elisacoreInitializeAllTargetsForCrossEmission(void) {
	LLVMInitializeAllTargetInfos();
	LLVMInitializeAllTargets();
	LLVMInitializeAllTargetMCs();
	LLVMInitializeAllAsmPrinters();
}

static LLVMTargetMachineRef elisacoreCreateTargetMachineDefault(
	LLVMTargetRef Target,
	char *Triple,
	char *CPU,
	char *Features) {
	return LLVMCreateTargetMachine(
		Target,
		Triple,
		CPU,
		Features,
		LLVMCodeGenLevelDefault,
		LLVMRelocDefault,
		LLVMCodeModelDefault);
}

static char *elisacoreRunOptimizationPipeline(
	LLVMModuleRef Module,
	LLVMTargetMachineRef TargetMachine,
	char *PassPipeline) {
	LLVMPassBuilderOptionsRef Options = LLVMCreatePassBuilderOptions();
	if (Options == NULL) {
		return LLVMGetErrorMessage(LLVMCreateStringError("failed to create LLVM pass builder options"));
	}
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

static void elisacoreDisposeLLVMErrorMessage(char *ErrMsg) {
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

	"elisacore/src/semantic"
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

func WriteLLVMObjectFile(result *semantic.Result, outputPath string) error {
	return WriteLLVMObjectFileWithOpt(result, outputPath, OptimizationLevel3)
}

func WriteLLVMObjectFileWithOpt(result *semantic.Result, outputPath string, optLevel OptimizationLevel) error {
	return WriteLLVMObjectFileWithOptAndPackedLoweringProfile(result, outputPath, optLevel, DefaultPackedLoweringProfile())
}

func WriteLLVMObjectFileWithOptAndPackedLoweringProfile(result *semantic.Result, outputPath string, optLevel OptimizationLevel, profile PackedLoweringProfile) error {
	return WriteLLVMObjectFileWithOptions(result, outputPath, LLVMObjectEmitOptions{
		OptLevel:      optLevel,
		PackedProfile: profile,
	})
}

type LLVMObjectEmitOptions struct {
	OptLevel      OptimizationLevel
	PackedProfile PackedLoweringProfile
	TargetTriple  string
	DebugInfo     bool
}

func WriteLLVMObjectFileWithOptions(result *semantic.Result, outputPath string, options LLVMObjectEmitOptions) error {
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("output path cannot be empty")
	}
	if options.PackedProfile == (PackedLoweringProfile{}) {
		options.PackedProfile = DefaultPackedLoweringProfile()
	}
	g, err := compileLLVMModuleWithTargetAndDebug(result, options.OptLevel, options.PackedProfile, strings.TrimSpace(options.TargetTriple), options.DebugInfo)
	if err != nil {
		return err
	}
	defer g.dispose()
	return g.writeObjectFile(outputPath, options.OptLevel)
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
	if g.module == nil {
		return fmt.Errorf("cannot write object file for a nil LLVM module")
	}
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
	errMessage := C.elisacoreRunOptimizationPipeline(g.module, g.targetMachine, passPipeline)
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
	if C.elisacoreInitializeNativeTarget() != 0 {
		return fmt.Errorf("failed to initialize native LLVM target")
	}
	if C.elisacoreInitializeNativeAsmPrinter() != 0 {
		return fmt.Errorf("failed to initialize native LLVM asm printer")
	}
	C.elisacoreInitializeAllTargetsForCrossEmission()

	tripleOwnedByLLVM := false
	var triple *C.char
	if requested := strings.TrimSpace(g.requestedTargetTriple); requested != "" {
		triple = cString(requested)
	} else {
		triple = C.LLVMGetDefaultTargetTriple()
		tripleOwnedByLLVM = true
		if triple == nil {
			return fmt.Errorf("failed to determine default target triple")
		}
	}

	var target C.LLVMTargetRef
	var message *C.char
	if C.LLVMGetTargetFromTriple(triple, &target, &message) != 0 {
		errText := disposeLLVMMessage(message, "unknown LLVM target lookup error")
		tripleText := C.GoString(triple)
		if tripleOwnedByLLVM {
			C.LLVMDisposeMessage(triple)
		} else {
			C.free(unsafe.Pointer(triple))
		}
		return fmt.Errorf("failed to resolve LLVM target %q: %s", tripleText, errText)
	}

	cpu := cString("")
	features := cString("")
	tm := C.elisacoreCreateTargetMachineDefault(target, triple, cpu, features)
	C.free(unsafe.Pointer(cpu))
	C.free(unsafe.Pointer(features))
	if tm == nil {
		tripleText := C.GoString(triple)
		if tripleOwnedByLLVM {
			C.LLVMDisposeMessage(triple)
		} else {
			C.free(unsafe.Pointer(triple))
		}
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
	g.targetTripleOwnedByLLVM = tripleOwnedByLLVM
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

func (g *llvmGenerator) abiAlignmentOfType(t semantic.Type) (uint64, error) {
	if isVoidType(t) {
		return 1, nil
	}
	if err := g.ensureTargetMachine(); err != nil {
		return 0, err
	}
	llvmType, err := g.lowerType(t)
	if err != nil {
		return 0, err
	}
	return g.abiAlignmentOfLLVMType(llvmType)
}

func (g *llvmGenerator) abiAlignmentOfLLVMType(llvmType C.LLVMTypeRef) (uint64, error) {
	if err := g.ensureTargetMachine(); err != nil {
		return 0, err
	}
	return uint64(C.LLVMABIAlignmentOfType(g.targetData, llvmType)), nil
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
	C.elisacoreDisposeLLVMErrorMessage(message)
	if text == "" {
		return fallback
	}
	return text
}
