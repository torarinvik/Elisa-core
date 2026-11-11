//go:build cgo

package backend

/*
#cgo darwin,arm64 CFLAGS: -I/opt/homebrew/opt/llvm/include
#cgo darwin,arm64 LDFLAGS: -L/opt/homebrew/opt/llvm/lib -lLLVM
#cgo darwin,amd64 CFLAGS: -I/usr/local/opt/llvm/include
#cgo darwin,amd64 LDFLAGS: -L/usr/local/opt/llvm/lib -lLLVM
#cgo linux CFLAGS: -I/usr/lib/llvm-23/include -I/usr/include -I/usr/lib/llvm-21/include -I/usr/lib/llvm-20/include -I/usr/lib/llvm-19/include -I/usr/lib/llvm-18/include -I/usr/lib/llvm-17/include -I/usr/lib/llvm-16/include -I/usr/lib/llvm-15/include
#cgo linux LDFLAGS: -L/usr/lib/llvm-23/lib -Wl,-rpath,/usr/lib/llvm-23/lib -L/usr/lib/llvm-21/lib -L/usr/lib/llvm-20/lib -L/usr/lib/llvm-19/lib -L/usr/lib/llvm-18/lib -L/usr/lib/llvm-17/lib -L/usr/lib/llvm-16/lib -L/usr/lib/llvm-15/lib -lLLVM
#cgo windows CFLAGS: -IC:/msys64/mingw64/include
#cgo windows LDFLAGS: -LC:/msys64/mingw64/lib -lLLVM-22

#include <stdlib.h>
#include <llvm-c/Core.h>
#include <llvm-c/Error.h>
#include <llvm-c/Target.h>
#include <llvm-c/TargetMachine.h>
#include "parallel_emit.h"
unsigned elisacorePeelSmallValidationLoops(LLVMModuleRef Module);
unsigned elisacoreInlineSmallRecursion(LLVMModuleRef Module);
unsigned elisacorePredicatePrivateBitUpdates(LLVMModuleRef Module);
unsigned elisacoreSinkPrivateInitializations(LLVMModuleRef Module);
static LLVMTargetMachineRef elisacoreCreateTargetMachineForOpt(
	LLVMTargetRef Target,
	char *Triple,
	char *CPU,
	char *Features,
	int OptimizationLevel) {
	return LLVMCreateTargetMachine(
		Target,
		Triple,
		CPU,
		Features,
		OptimizationLevel == 0 ? LLVMCodeGenLevelNone :
            OptimizationLevel == 3 ? LLVMCodeGenLevelAggressive : LLVMCodeGenLevelDefault,
		LLVMRelocDefault,
		LLVMCodeModelDefault);
}

static void elisacoreDisposeLLVMErrorMessage(char *ErrMsg) {
	if (ErrMsg != NULL) {
		LLVMDisposeErrorMessage(ErrMsg);
	}
}

*/
import "C"

import (
	"elisacore/src/backend/llvmnative/memoryfills"
	"elisacore/src/semantic"
	"fmt"
	"strings"
	"unsafe"
)

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
	tm := C.elisacoreCreateTargetMachineForOpt(target, triple, cpu, features, C.int(g.optLevel))
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

func unrollNestedScans(module C.LLVMModuleRef) int {
	return int(C.elisacoreUnrollNestedScans(module))
}

func unrollGuardedReductionsForTest(module C.LLVMModuleRef) int {
	return int(C.elisacoreUnrollGuardedReductions(module))
}

func peelSmallValidationLoops(module C.LLVMModuleRef) int {
	return int(C.elisacorePeelSmallValidationLoops(module))
}

// Partition ABI mode: 0 preserves default codegen, 1 is O0, 2 is aggressive O3.
func machineCodegenMode(level OptimizationLevel) int {
	if level == OptimizationLevel0 {
		return 1
	}
	if level == OptimizationLevel3 {
		return 2
	}
	return 0
}

func expandSmallZeroFills(module C.LLVMModuleRef) int {
	return memoryfills.ExpandConstantByteFills(unsafe.Pointer(module))
}

func elideScratchPrefixInitializers(module C.LLVMModuleRef) int {
	return int(C.elisacoreElideScratchPrefixInitializers(module))
}

func scratchPrefixSupported() bool {
	return C.elisacoreScratchPrefixSupported() != 0
}

func inlineSmallRecursion(module C.LLVMModuleRef) int {
	return int(C.elisacoreInlineSmallRecursion(module))
}

func predicatePrivateBitUpdates(module C.LLVMModuleRef) int {
	return int(C.elisacorePredicatePrivateBitUpdates(module))
}

func upperBoundUnrollLimit() int { return int(C.elisacoreUpperBoundUnrollLimit()) }

func sinkPrivateInitializations(module C.LLVMModuleRef) int {
	return int(C.elisacoreSinkPrivateInitializations(module))
}

type llvmIRTargetDataRef = C.LLVMTargetDataRef
type llvmIRByteOrder = C.enum_LLVMByteOrdering

const llvmLittleEndian = C.LLVMLittleEndian

func llvmByteOrder(target llvmIRTargetDataRef) llvmIRByteOrder {
	return C.LLVMByteOrder(target)
}

func llvmABISizeOfType(target llvmIRTargetDataRef, typ llvmIRTypeRef) llvmIRUnsignedLongLong {
	return C.LLVMABISizeOfType(target, typ)
}

type llvmIRTargetMachineRef = C.LLVMTargetMachineRef

func llvmDisposeTargetData(target llvmIRTargetDataRef) {
	C.LLVMDisposeTargetData(target)
}

func llvmDisposeTargetMachine(target llvmIRTargetMachineRef) {
	C.LLVMDisposeTargetMachine(target)
}
