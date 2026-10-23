//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/BitReader.h>
#include <llvm-c/BitWriter.h>
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
static LLVMMemoryBufferRef elisacoreOptimizedCacheBuffer(const char *data, size_t size) {
 return LLVMCreateMemoryBufferWithMemoryRangeCopy(data, size, "optimized-cache");
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
	"unsafe"
)

// optimizeObjectModule caches the complete O-level pipeline, before native
// partitioning. Inputs are exact lowered bitcode, actual target-machine settings
// and pipeline knobs, inside the caller's executable-content namespace. Changes
// which lower identically can reuse O3 without weakening whole-module inlining.
// A restored module owns a separate context; no generator handles cross contexts.
func (s *IncrementalObjectSession) optimizeObjectModule(g *llvmGenerator, level OptimizationLevel) (*llvmGenerator, error) {
	if g.optimizedForCodegen {
		return g, nil
	}
	if s.disk == nil || level == OptimizationLevel0 || os.Getenv("ELISACORE_LLVM_PASS_DEBUG") == "1" {
		return g, g.optimizeModule(level)
	}
	start := time.Now()
	input := C.LLVMWriteBitcodeToMemoryBuffer(g.module)
	if input == nil {
		return g, g.optimizeModule(level)
	}
	defer C.LLVMDisposeMemoryBuffer(input)
	size := uint64(C.LLVMGetBufferSize(input))
	if size > uint64(s.disk.maxBytes) || size > uint64(^uint(0)>>1) {
		return g, g.optimizeModule(level)
	}
	bytes := unsafe.Slice((*byte)(unsafe.Pointer(C.LLVMGetBufferStart(input))), int(size))
	triple := C.LLVMGetTargetMachineTriple(g.targetMachine)
	cpu := C.LLVMGetTargetMachineCPU(g.targetMachine)
	features := C.LLVMGetTargetMachineFeatureString(g.targetMachine)
	defer C.LLVMDisposeMessage(triple)
	defer C.LLVMDisposeMessage(cpu)
	defer C.LLVMDisposeMessage(features)
	config, _ := json.Marshal([]any{"optimized-module-v1", int(level), C.GoString(triple), C.GoString(cpu), C.GoString(features), optimizationAAPipeline, optimizationInlinerThreshold(level), optimizationUnrollThresholdO3})
	key := string(config)
	if data := s.disk.load(bytes, key); len(data) > 0 {
		context := C.LLVMContextCreate()
		buffer := C.elisacoreOptimizedCacheBuffer((*C.char)(unsafe.Pointer(unsafe.SliceData(data))), C.size_t(len(data)))
		var module C.LLVMModuleRef
		status := C.LLVMParseBitcodeInContext2(context, buffer, &module)
		C.LLVMDisposeMemoryBuffer(buffer)
		if status == 0 {
			// The bitcode reader uses the buffer name as ModuleID; preserve the
			// original lowering identity rather than exposing the cache filename.
			var nameSize C.size_t
			name := C.LLVMGetModuleIdentifier(g.module, &nameSize)
			C.LLVMSetModuleIdentifier(module, name, nameSize)
			cached := &llvmGenerator{context: context, module: module, result: g.result, optLevel: g.optLevel, requestedTargetTriple: g.requestedTargetTriple, optimizedForCodegen: true}
			if err := cached.ensureTargetMachine(); err == nil {
				if err := cached.verify(); err == nil {
					cached.verifyAutovecExpectations()
					s.recordStats(func(stats *IncrementalObjectStats) { stats.OptimizedModulesReused++ })
					if os.Getenv("ELISACORE_BUILD_TIMING") == "1" {
						fmt.Fprintf(os.Stderr, "[build-timing] optimized module disk hit: %v\n", time.Since(start).Round(time.Millisecond))
					}
					return cached, nil
				}
			}
			cached.dispose()
		} else {
			if module != nil {
				C.LLVMDisposeModule(module)
			}
			C.LLVMContextDispose(context)
		}
	}
	if err := g.optimizeModule(level); err != nil {
		return g, err
	}
	optimized := C.LLVMWriteBitcodeToMemoryBuffer(g.module)
	if optimized != nil {
		defer C.LLVMDisposeMemoryBuffer(optimized)
		n := uint64(C.LLVMGetBufferSize(optimized))
		if n <= uint64(s.disk.maxBytes) && n <= uint64(^uint(0)>>1) {
			data := unsafe.Slice((*byte)(unsafe.Pointer(C.LLVMGetBufferStart(optimized))), int(n))
			s.disk.store(string(bytes), key, data)
		}
	}
	if os.Getenv("ELISACORE_BUILD_TIMING") == "1" {
		fmt.Fprintf(os.Stderr, "[build-timing] optimized module disk miss: %v\n", time.Since(start).Round(time.Millisecond))
	}
	return g, nil
}
