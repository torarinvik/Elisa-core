//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/BitReader.h>
#include <llvm-c/BitWriter.h>
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
LLVMMemoryBufferRef elisacoreWriteOptimizedBitcodePreservingUses(LLVMModuleRef module);
static LLVMMemoryBufferRef elisacoreOptimizedCacheBuffer(const char *data, size_t size) {
 return LLVMCreateMemoryBufferWithMemoryRangeCopy(data, size, "optimized-cache");
}
*/
import "C"

import (
	"bytes"
	"crypto/sha256"
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
	key := optimizedModuleCacheKey(g, level, C.GoString(triple), C.GoString(cpu), C.GoString(features))
	data, expectedDigest, validEnvelope := decodeOptimizedModuleEnvelope(s.disk.load(bytes, key))
	if validEnvelope {
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
			cached := &llvmGenerator{context: context, module: module, result: g.result, optLevel: g.optLevel, requestedTargetTriple: g.requestedTargetTriple, optimizedForCodegen: true, emitDebugInfo: g.emitDebugInfo, emitTrace: g.emitTrace}
			if err := cached.ensureTargetMachine(); err == nil {
				if err := cached.verify(); err == nil && sha256.Sum256([]byte(cached.printModule())) == expectedDigest {
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
	optimized := C.elisacoreWriteOptimizedBitcodePreservingUses(g.module)
	if optimized != nil {
		defer C.LLVMDisposeMemoryBuffer(optimized)
		n := uint64(C.LLVMGetBufferSize(optimized))
		if n <= uint64(s.disk.maxBytes) && n <= uint64(^uint(0)>>1) {
			data := unsafe.Slice((*byte)(unsafe.Pointer(C.LLVMGetBufferStart(optimized))), int(n))
			// LLVM's reader may normalize intrinsic declarations and alter IR
			// attributes. Parsing and verification alone do not prove that the
			// cached module is the one the optimizer actually produced.
			if optimizedModuleRoundTripExact(g, data) {
				digest := sha256.Sum256([]byte(g.printModule()))
				s.disk.store(string(bytes), key, encodeOptimizedModuleEnvelope(digest, data))
			}
		}
	}
	if os.Getenv("ELISACORE_BUILD_TIMING") == "1" {
		fmt.Fprintf(os.Stderr, "[build-timing] optimized module disk miss: %v\n", time.Since(start).Round(time.Millisecond))
	}
	return g, nil
}

func optimizedModuleCacheKey(g *llvmGenerator, level OptimizationLevel, triple, cpu, features string) string {
	config, _ := json.Marshal([]any{
		"optimized-module-v4-split-half-add-authenticated-ir",
		int(level), triple, cpu, features,
		optimizationAAPipeline, optimizationInlinerThreshold(level), optimizationUnrollThresholdO3,
		"split-half-u64-add-v1", !g.emitDebugInfo, g.emitDebugInfo, g.emitTrace,
	})
	return string(config)
}

const optimizedModuleEnvelopeMagic = "ELISACORE-OPTIMIZED-IR-v1\n"

// The disk artifact binds the restored module to the exact printed IR that
// was produced before serialization. The enclosing disk cache authenticates
// the whole artifact; the digest is checked again on every replay because
// reader behavior can vary between processes.
func encodeOptimizedModuleEnvelope(digest [sha256.Size]byte, bitcode []byte) []byte {
	result := make([]byte, 0, len(optimizedModuleEnvelopeMagic)+sha256.Size+len(bitcode))
	result = append(result, optimizedModuleEnvelopeMagic...)
	result = append(result, digest[:]...)
	return append(result, bitcode...)
}

func decodeOptimizedModuleEnvelope(data []byte) ([]byte, [sha256.Size]byte, bool) {
	var digest [sha256.Size]byte
	header := len(optimizedModuleEnvelopeMagic) + sha256.Size
	if len(data) <= header || !bytes.Equal(data[:len(optimizedModuleEnvelopeMagic)], []byte(optimizedModuleEnvelopeMagic)) {
		return nil, digest, false
	}
	copy(digest[:], data[len(optimizedModuleEnvelopeMagic):header])
	return data[header:], digest, true
}

// optimizedModuleRoundTripExact proves that a just-produced cache artifact
// restores to the same printed module, including metadata and attributes. It
// is intentionally fail-closed, including for reader failures and changes to
// textual use-list order. A rejected artifact is simply not cached.
func optimizedModuleRoundTripExact(g *llvmGenerator, data []byte) bool {
	if g == nil || g.module == nil || len(data) == 0 {
		return false
	}
	context := C.LLVMContextCreate()
	if context == nil {
		return false
	}
	defer C.LLVMContextDispose(context)
	buffer := C.elisacoreOptimizedCacheBuffer((*C.char)(unsafe.Pointer(unsafe.SliceData(data))), C.size_t(len(data)))
	if buffer == nil {
		return false
	}
	var module C.LLVMModuleRef
	status := C.LLVMParseBitcodeInContext2(context, buffer, &module)
	C.LLVMDisposeMemoryBuffer(buffer)
	if status != 0 || module == nil {
		if module != nil {
			C.LLVMDisposeModule(module)
		}
		return false
	}
	defer C.LLVMDisposeModule(module)
	var nameSize C.size_t
	name := C.LLVMGetModuleIdentifier(g.module, &nameSize)
	C.LLVMSetModuleIdentifier(module, name, nameSize)
	return g.printModule() == (&llvmGenerator{module: module}).printModule()
}

// optimizedModulePreservedBitcodeRoundTripExact is used by cache diagnostics
// and tests to distinguish LLVM reader normalization from cache failures.
func optimizedModulePreservedBitcodeRoundTripExact(g *llvmGenerator) (bool, error) {
	if g == nil || g.module == nil {
		return false, fmt.Errorf("missing LLVM module")
	}
	bitcode := C.elisacoreWriteOptimizedBitcodePreservingUses(g.module)
	if bitcode == nil {
		return false, fmt.Errorf("failed to write optimized bitcode")
	}
	defer C.LLVMDisposeMemoryBuffer(bitcode)
	size := uint64(C.LLVMGetBufferSize(bitcode))
	if size == 0 || size > uint64(^uint(0)>>1) {
		return false, fmt.Errorf("invalid optimized bitcode size: %d", size)
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(C.LLVMGetBufferStart(bitcode))), int(size))
	return optimizedModuleRoundTripExact(g, data), nil
}
