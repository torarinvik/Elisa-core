//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>

// Stamp "target-cpu"/"target-features" on every defined function that lacks them, the
// way clang's frontend does. A non-LTO object gets its CPU from the TargetMachine that
// emits it; LTO bitcode is code-generated later by the LINKER, which only sees what the
// bitcode carries, so without these attributes `-flto` would silently drop the host
// tuning (`-mcpu=native`) that the in-process object path applies.
static void elisacoreStampTargetAttributes(LLVMModuleRef M, LLVMTargetMachineRef TM) {
	char *cpu = LLVMGetTargetMachineCPU(TM);
	char *features = LLVMGetTargetMachineFeatureString(TM);
	LLVMContextRef ctx = LLVMGetModuleContext(M);
	for (LLVMValueRef fn = LLVMGetFirstFunction(M); fn != NULL; fn = LLVMGetNextFunction(fn)) {
		if (LLVMIsDeclaration(fn)) {
			continue;
		}
		if (cpu != NULL && cpu[0] != '\0' &&
			LLVMGetStringAttributeAtIndex(fn, LLVMAttributeFunctionIndex, "target-cpu", 10) == NULL) {
			LLVMAttributeRef a = LLVMCreateStringAttribute(ctx, "target-cpu", 10, cpu, (unsigned)strlen(cpu));
			LLVMAddAttributeAtIndex(fn, LLVMAttributeFunctionIndex, a);
		}
		if (features != NULL && features[0] != '\0' &&
			LLVMGetStringAttributeAtIndex(fn, LLVMAttributeFunctionIndex, "target-features", 15) == NULL) {
			LLVMAttributeRef a = LLVMCreateStringAttribute(ctx, "target-features", 15, features, (unsigned)strlen(features));
			LLVMAddAttributeAtIndex(fn, LLVMAttributeFunctionIndex, a);
		}
	}
	LLVMDisposeMessage(cpu);
	LLVMDisposeMessage(features);
}
*/
import "C"

import (
	"fmt"
	"strings"

	"elisacore/src/semantic"
)

// LLVMLTOBitcodeEmitOptions mirrors LLVMObjectEmitOptions for LTO bitcode emission.
type LLVMLTOBitcodeEmitOptions struct {
	OptLevel      OptimizationLevel
	PackedProfile PackedLoweringProfile
	TargetTriple  string
	DebugInfo     bool
	Trace         bool
}

// WriteLLVMLTOBitcodeFile writes the program as an LTO input: LLVM bitcode that a linker
// with an LLVM LTO plugin (ld64 + libLTO, lld) optimizes together with the other bitcode
// inputs (the default runtime, foreign C compiled with -flto) and code-generates as one unit.
//
// The module is optimized with the SAME per-module pipeline as an ordinary object
// (default<On>, via optimizeModule) rather than LLVM's lto-pre-link pipeline. That keeps every
// guarantee the non-LTO build checks after optimization (the autovectorization verifier and
// its -Wperf warnings see exactly the module they see today); the link-time `lto<On>`
// pipeline then re-optimizes the merged module, which is where cross-object inlining happens.
// Returns the performance warnings optimizeModule collected.
func WriteLLVMLTOBitcodeFile(result *semantic.Result, outputPath string, options LLVMLTOBitcodeEmitOptions) ([]string, error) {
	if strings.TrimSpace(outputPath) == "" {
		return nil, fmt.Errorf("output path cannot be empty")
	}
	if options.PackedProfile == (PackedLoweringProfile{}) {
		options.PackedProfile = DefaultPackedLoweringProfile()
	}
	g, err := compileLLVMModuleWithTargetDebugTrace(result, options.OptLevel, options.PackedProfile, strings.TrimSpace(options.TargetTriple), options.DebugInfo, options.Trace)
	if err != nil {
		return nil, err
	}
	defer g.dispose()
	if err := g.ensureTargetMachine(); err != nil {
		return nil, err
	}
	if err := g.optimizeModule(options.OptLevel); err != nil {
		return nil, err
	}
	C.elisacoreStampTargetAttributes(g.module, g.targetMachine)
	if err := g.writeBitcodeFile(outputPath); err != nil {
		return nil, err
	}
	return g.PerfWarnings(), nil
}
