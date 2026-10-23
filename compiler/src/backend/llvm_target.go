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
#include "parallel_emit.h"

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

static LLVMTargetMachineRef elisacoreCreateTargetMachineForOpt(
	LLVMTargetRef Target,
	char *Triple,
	char *CPU,
	char *Features,
	int Unoptimized) {
	return LLVMCreateTargetMachine(
		Target,
		Triple,
		CPU,
		Features,
		Unoptimized ? LLVMCodeGenLevelNone : LLVMCodeGenLevelDefault,
		LLVMRelocDefault,
		LLVMCodeModelDefault);
}

static char *elisacoreRunOptimizationPipeline(
	LLVMModuleRef Module,
	LLVMTargetMachineRef TargetMachine,
	char *PassPipeline,
	char *AAPipeline,
	int InlinerThreshold,
	int DebugLogging) {
	LLVMPassBuilderOptionsRef Options = LLVMCreatePassBuilderOptions();
	if (Options == NULL) {
		return LLVMGetErrorMessage(LLVMCreateStringError("failed to create LLVM pass builder options"));
	}
	LLVMPassBuilderOptionsSetDebugLogging(Options, DebugLogging);
	LLVMPassBuilderOptionsSetLoopInterleaving(Options, 1);
	LLVMPassBuilderOptionsSetLoopVectorization(Options, 1);
	LLVMPassBuilderOptionsSetSLPVectorization(Options, 1);
	LLVMPassBuilderOptionsSetLoopUnrolling(Options, 1);
	LLVMPassBuilderOptionsSetAAPipeline(Options, AAPipeline);
	if (InlinerThreshold > 0) LLVMPassBuilderOptionsSetInlinerThreshold(Options, InlinerThreshold);
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
	"os"
	"strings"
	"sync"
	"unsafe"

	"elisacore/src/semantic"
)

var llvmTargetInitOnce sync.Once
var llvmTargetInitErr error

// LLVM's default optimization pipeline is not safe to run concurrently in this
// embedding (the existing parallel backend uses worker processes for it). Keep
// only that pipeline serialized; independent O0 lowering/codegen remains parallel.
var llvmOptimizationMu sync.Mutex

func ensureLLVMTargetRegistry() error {
	llvmTargetInitOnce.Do(func() {
		if C.elisacoreInitializeNativeTarget() != 0 {
			llvmTargetInitErr = fmt.Errorf("failed to initialize native LLVM target")
			return
		}
		if C.elisacoreInitializeNativeAsmPrinter() != 0 {
			llvmTargetInitErr = fmt.Errorf("failed to initialize native LLVM asm printer")
			return
		}
		C.elisacoreInitializeAllTargetsForCrossEmission()
	})
	return llvmTargetInitErr
}

// optimizationAAPipeline is LLVM's default alias-analysis stack plus scev-aa. scev-aa proves
// `a[i]` and `a[j]` disjoint from their index recurrences (j in i+1..<n with known trip counts),
// so the a[i] fields of a pairwise loop are promoted to registers instead of being reloaded and
// re-stored on every j iteration (AWFY NBody: -20% cycles). Measured cost: +1.8% user time on
// the stage1 self-compile. part_optimize in parallel_emit.c must use the same string.
const optimizationAAPipeline = "scoped-noalias-aa,tbaa,basic-aa,globals-aa,scev-aa"

// optimizationInlinerThresholdO3 raises LLVM's -O3 inline threshold (default 250) to 400.
// Elisa code is dominated by small checked helpers (index/field accessors, view constructors,
// `cur_is(p, "x")`-style leaf predicates) whose bounds/validity checks push them just past the
// default budget; once inlined their checks fold against the caller's facts. Measured on AWFY:
// Richards -21%, Towers -12%, Json -4% instructions, others flat; +7% object size; compile
// time within noise (+0..4% user time over all 14 ports). part_optimize in parallel_emit.c uses
// the same value. -O1/-O2 keep LLVM's defaults.
const optimizationInlinerThresholdO3 = 400

// optimizationUnrollThresholdO3 raises LLVM's -O3 full-unroll budget (-unroll-threshold-aggressive,
// default 300) to 600. Small constant-trip loops whose bodies carry bounds checks (AWFY NBody's
// pairwise 5-body loop, Queens' 8-row loop) cost just over 300 and stayed rolled; Rust/Zig unroll
// them. Measured on AWFY: NBody -22% instructions / -33% cycles, Queens -28% / -33%, all other
// ports within +-1M instructions; compile time within noise. The option is process-global, but
// LoopUnroll reads it only when OptLevel > 2, so -O1/-O2 output is unchanged (a test checks this).
// It is installed lazily, the first time this process optimises at -O3. part_optimize in
// parallel_emit.c installs the same value in worker processes.
// Kept as a package variable so the subprocess test can compare LLVM's default budget (300)
// with the production setting (600) before LLVM's process-global option is initialized.
var optimizationUnrollThresholdO3 = 600

var o3UnrollThresholdOnce sync.Once

func ensureO3UnrollThreshold(optLevel OptimizationLevel) {
	if optLevel < OptimizationLevel3 || optimizationUnrollThresholdO3 <= 0 {
		return
	}
	o3UnrollThresholdOnce.Do(func() { C.elisacoreEnsureO3UnrollThreshold(C.int(optimizationUnrollThresholdO3)) })
}

func optimizationInlinerThreshold(optLevel OptimizationLevel) int {
	if optLevel >= OptimizationLevel3 {
		return optimizationInlinerThresholdO3
	}
	return 0
}

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
	Trace         bool
	Serial        bool // preserve local symbol binding without partition externalization
}

func WriteLLVMObjectFileWithOptions(result *semantic.Result, outputPath string, options LLVMObjectEmitOptions) error {
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("output path cannot be empty")
	}
	if options.PackedProfile == (PackedLoweringProfile{}) {
		options.PackedProfile = DefaultPackedLoweringProfile()
	}
	g, err := compileLLVMModuleWithTargetDebugTrace(result, options.OptLevel, options.PackedProfile, strings.TrimSpace(options.TargetTriple), options.DebugInfo, options.Trace)
	if err != nil {
		return err
	}
	defer g.dispose()
	if options.Serial {
		if err := g.ensureTargetMachine(); err != nil {
			return err
		}
		if err := g.optimizeModule(options.OptLevel); err != nil {
			return err
		}
		return g.emitNativeObjectFile(outputPath)
	}
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
	jobs := codegenJobs()
	if jobs > 1 && optLevel != OptimizationLevel0 && !g.optimizedForCodegen && parallelOptEnabled() {
		// Opt-in: optimize each partition in its worker (see parallel_emit.c). This skips
		// the module-wide verifyAutovecExpectations, which needs the optimized module.
		pipeline := fmt.Sprintf("default<O%d>", int(optLevel))
		if done, err := g.emitObjectParallel(outputPath, jobs, pipeline, parallelOptImportWeight()); done || err != nil {
			return err
		}
	}
	if err := g.optimizeModule(optLevel); err != nil {
		return err
	}

	if jobs > 1 {
		if done, err := g.emitObjectParallel(outputPath, jobs, "", 0); done || err != nil {
			return err
		}
	}

	return g.emitNativeObjectFile(outputPath)
}

// emitNativeObjectFile emits an already configured/optimized module directly.
// Incremental fallbacks use this to avoid the host partition merger for cross
// targets or modules that cannot safely be partitioned.
func (g *llvmGenerator) emitNativeObjectFile(outputPath string) error {
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

	llvmOptimizationMu.Lock()
	defer llvmOptimizationMu.Unlock()
	ensureO3UnrollThreshold(optLevel)
	passPipeline := cString(fmt.Sprintf("default<O%d>", int(optLevel)))
	defer C.free(unsafe.Pointer(passPipeline))
	aaPipeline := cString(optimizationAAPipeline)
	defer C.free(unsafe.Pointer(aaPipeline))
	// LLVM emits pass progress before running each pass, so a bounded process
	// can identify its active pass even when optimization exceeds its timeout.
	debugLogging := C.int(0)
	if os.Getenv("ELISACORE_LLVM_PASS_DEBUG") == "1" {
		debugLogging = 1
	}
	errMessage := C.elisacoreRunOptimizationPipeline(g.module, g.targetMachine, passPipeline, aaPipeline, C.int(optimizationInlinerThreshold(optLevel)), debugLogging)
	if errMessage != nil {
		return fmt.Errorf("failed to optimize LLVM module for code generation: %s", disposeLLVMErrorMessage(errMessage, "unknown LLVM pass pipeline error"))
	}
	if err := g.verify(); err != nil {
		return fmt.Errorf("optimized LLVM module failed verification: %w", err)
	}
	g.verifyAutovecExpectations()
	g.optimizedForCodegen = true
	return nil
}

func (g *llvmGenerator) ensureTargetMachine() error {
	if g.targetMachine != nil && g.targetData != nil && g.targetTriple != nil {
		return nil
	}
	if err := ensureLLVMTargetRegistry(); err != nil {
		return err
	}

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

	// For a NATIVE build (no explicit cross-target), tune for the host CPU and enable its features —
	// exactly what clang does by default (e.g. `-target-cpu apple-m1` on Apple Silicon). A generic
	// target leaves the scheduler model and post-baseline ISA features on the table. Cross builds keep
	// the generic target so the output stays portable.
	//
	// `-mcpu=<name>` (ELISACORE_TARGET_CPU, see RequestedTargetCPU) overrides both defaults:
	// `native` selects the host CPU and its features even for an explicit triple, `generic`
	// gives a portable baseline for a native build, and any other value names an LLVM CPU
	// (its default feature set is implied by the CPU name).
	identity, err := ResolveTargetIdentity(g.requestedTargetTriple)
	if err != nil {
		if tripleOwnedByLLVM {
			C.LLVMDisposeMessage(triple)
		} else {
			C.free(unsafe.Pointer(triple))
		}
		return err
	}
	cpu, features := cString(identity.CPU), cString(identity.Features)
	// O0 must also disable machine-code optimization, not only IR passes.
	// The default machine pipeline spends substantial time on large debug builds.
	unoptimized := C.int(0)
	if g.optLevel == OptimizationLevel0 {
		unoptimized = 1
	}
	tm := C.elisacoreCreateTargetMachineForOpt(target, triple, cpu, features, unoptimized)
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

// syncWordBitsWithTarget makes `usize`/`isize`/`int`/`uintptr` follow the
// TARGET's pointer width instead of the host's.
//
// wordBits is seeded from unsafe.Sizeof(uintptr(0)) at construction, which is
// the compiler's OWN pointer size. That is right for a native build and wrong
// for every cross build: emitting for wasm32 on an arm64 host produced 4-byte
// pointers alongside an 8-byte `usize`, so a darray header laid out as
// {items, count, capacity} disagreed with the code indexing through it — the
// first bytes of a freshly built darray came back holding the LAST bytes
// written, and a module imported mmap(i64,…)->i64 next to memcpy(i32,i32,i64),
// which no host implementation can satisfy with one convention.
//
// The size comes from LLVM's data layout for the resolved triple rather than
// from matching triple strings, so a target this code has never heard of is
// still correct.
func (g *llvmGenerator) syncWordBitsWithTarget() error {
	if g == nil {
		return nil
	}
	if err := g.ensureTargetMachine(); err != nil {
		return err
	}
	if g.targetData == nil {
		return nil
	}
	// Address space 0: Elisa has no multi-address-space targets, and a
	// target whose AS0 pointer differs from its generic pointer would need a
	// deliberate decision here rather than a silent guess.
	pointerBytes := uint64(C.LLVMPointerSizeForAS(g.targetData, C.unsigned(0)))
	if pointerBytes == 0 {
		return nil
	}
	g.wordBits = int(pointerBytes * 8)
	return nil
}

// ResolveTargetIdentity returns the same concrete target settings used by emission.
// Cache keys must include host features: an empty or native CPU request is not
// a portable target identity when caches are shared across machines.
func ResolveTargetIdentity(requestedTriple string) (TargetIdentity, error) {
	triple := strings.TrimSpace(requestedTriple)
	if triple == "" {
		value := C.LLVMGetDefaultTargetTriple()
		if value == nil {
			return TargetIdentity{}, fmt.Errorf("failed to determine default target triple")
		}
		triple = C.GoString(value)
		C.LLVMDisposeMessage(value)
	}
	cpu := RequestedTargetCPU()
	if cpu == "native" || (cpu == "" && strings.TrimSpace(requestedTriple) == "") {
		name, features := C.LLVMGetHostCPUName(), C.LLVMGetHostCPUFeatures()
		if name == nil || features == nil {
			if name != nil {
				C.LLVMDisposeMessage(name)
			}
			if features != nil {
				C.LLVMDisposeMessage(features)
			}
			return TargetIdentity{}, fmt.Errorf("failed to determine host CPU identity")
		}
		defer C.LLVMDisposeMessage(name)
		defer C.LLVMDisposeMessage(features)
		return TargetIdentity{Triple: triple, CPU: C.GoString(name), Features: C.GoString(features)}, nil
	}
	return TargetIdentity{Triple: triple, CPU: cpu}, nil
}
