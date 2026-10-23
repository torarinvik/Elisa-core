//go:build cgo

package backend

/*
#cgo darwin,arm64 CXXFLAGS: -std=c++17 -I/opt/homebrew/opt/llvm/include
#cgo darwin,amd64 CXXFLAGS: -std=c++17 -I/usr/local/opt/llvm/include
#cgo linux CXXFLAGS: -std=c++17 -I/usr/include -I/usr/lib/llvm-21/include -I/usr/lib/llvm-20/include -I/usr/lib/llvm-19/include -I/usr/lib/llvm-18/include -I/usr/lib/llvm-17/include -I/usr/lib/llvm-16/include -I/usr/lib/llvm-15/include
#cgo windows CXXFLAGS: -std=c++17 -IC:/msys64/mingw64/include
#include <stdlib.h>
#include <llvm-c/BitWriter.h>
#include <llvm-c/Core.h>
#include <llvm-c/TargetMachine.h>
#include "parallel_emit.h"
*/
import "C"

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"elisacore/src/semantic"
)

// WriteObject regenerates current IR, reusing an exact optimized-module disk
// artifact when possible, then reuses native partitions only when their complete
// current bitcode and target-machine inputs match. Global
// optimization dependencies are already reflected in the partitioned IR. This
// avoids native code generation, not frontend analysis or LLVM lowering.
func (s *IncrementalObjectSession) WriteObject(result *semantic.Result, outputPath string, options LLVMObjectEmitOptions) error {
	s.recordStats(func(stats *IncrementalObjectStats) { stats.Builds++ })
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("output path cannot be empty")
	}
	if options.PackedProfile == (PackedLoweringProfile{}) {
		options.PackedProfile = DefaultPackedLoweringProfile()
	}
	// The merger is the host linker. Cross-target output keeps the ordinary path.
	native, err := ResolveTargetIdentity("")
	if err != nil {
		return err
	}
	target, err := ResolveTargetIdentity(strings.TrimSpace(options.TargetTriple))
	if err != nil {
		return err
	}
	if s.partitionedIRApplies(result, options) && !options.Serial && native.Triple == target.Triple && !virtualObjectSource(result) {
		if done, err := s.tryPartitionedObject(result, outputPath, options); done {
			return err
		}
	}
	irStart := time.Now()
	g, err := compileLLVMModuleWithTargetDebugTrace(result, options.OptLevel, options.PackedProfile, options.TargetTriple, options.DebugInfo, options.Trace)
	if err != nil {
		return err
	}
	defer g.disposeInBackground()
	if err := g.ensureTargetMachine(); err != nil {
		return err
	}
	// Explicit parallel-opt mode runs O-level passes inside isolated workers.
	// Cache input is then each exact *unoptimized* partition, so an unchanged
	// partition skips both optimization and native emission across processes.
	partitionPipeline := ""
	if parallelOptEnabled() && parallelOptImportWeight() == 0 && partitionWorkersAvailable && options.OptLevel != OptimizationLevel0 &&
		!options.Serial && !virtualObjectSource(result) && native.Triple == target.Triple &&
		int64(C.elisacoreObjectPartitionWeight(g.module)) >= s.minWeight && C.elisacoreSelectiveCloneSupported(g.module) != 0 {
		partitionPipeline = fmt.Sprintf("default<O%d>", int(options.OptLevel))
	} else {
		optimized, err := s.optimizeObjectModule(g, options.OptLevel)
		if err != nil {
			return err
		}
		if optimized != g {
			defer optimized.disposeInBackground()
			g = optimized
		}
	}
	irDuration := time.Since(irStart)
	s.recordStats(func(stats *IncrementalObjectStats) { stats.IRTime += irDuration })
	filename := result.ActiveFile().Filename
	identity := strings.TrimSpace(filename)
	virtual := identity == "" || identity == "-" || identity == "/dev/stdin" || (strings.HasPrefix(identity, "<") && strings.HasSuffix(identity, ">"))
	if options.Serial || virtual || native.Triple != target.Triple || int64(C.elisacoreObjectPartitionWeight(g.module)) < s.minWeight {
		return s.emitDirectObject(g, outputPath)
	}
	// Fixed codegen/relocation/code-model settings are shared with writeObjectFile.
	// Actual resolved machine strings, rather than environment spellings, are keys.
	// An unoptimized build has no use for the optimizing instruction selector;
	// FastISel emits the same semantics several times faster.
	fastCodegen := options.OptLevel == OptimizationLevel0
	codegenLevel := "default-codegen"
	if fastCodegen {
		codegenLevel = "none-codegen"
	}
	triple := C.LLVMGetTargetMachineTriple(g.targetMachine)
	cpu := C.LLVMGetTargetMachineCPU(g.targetMachine)
	features := C.LLVMGetTargetMachineFeatureString(g.targetMachine)
	config, _ := json.Marshal([]string{"incremental-object-v1", C.GoString(triple), C.GoString(cpu), C.GoString(features), codegenLevel, "default-reloc", "default-code-model", partitionPipeline})
	C.LLVMDisposeMessage(triple)
	C.LLVMDisposeMessage(cpu)
	C.LLVMDisposeMessage(features)
	configKey := string(config)
	targetID := sha256.Sum256(config)
	dir, err := os.MkdirTemp(filepath.Dir(outputPath), ".elisa-object-parts-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	// A stable logical unit identity prevents formerly private symbols from
	// colliding with private helpers in separately compiled objects. It excludes
	// bodies, so a body edit does not rename every partition's symbols.
	origin, err := filepath.Abs(filename)
	if err != nil {
		return err
	}
	unitID := sha256.Sum256([]byte(origin))
	namespace := C.CString(hex.EncodeToString(unitID[:]))
	defer C.free(unsafe.Pointer(namespace))
	parts := make([]string, s.partitions)
	var ownerCount C.size_t
	ownerMapStart := time.Now()
	owners := C.elisacoreStableObjectOwners(g.module, C.int(s.partitions), &ownerCount)
	ownerMapDuration := time.Since(ownerMapStart)
	s.recordStats(func(stats *IncrementalObjectStats) {
		stats.OwnerMapTime += ownerMapDuration
		stats.PartitionTime += ownerMapDuration
		if owners != nil {
			stats.OwnerMapBuilds++
		}
	})
	if owners == nil || C.elisacoreSelectiveCloneSupported(g.module) == 0 {
		if owners != nil {
			C.free(unsafe.Pointer(owners))
		}
		return s.emitDirectObject(g, outputPath)
	}
	// The C owner array is indexed by the optimized module's function order. Each
	// clone consumes it synchronously without retaining it; keep the source module
	// unchanged until all partitions have been cloned, then free it once here.
	defer C.free(unsafe.Pointer(owners))
	// The module is serialized once; each worker loads only its partition's bodies
	// into a private LLVM context, so partitions are prepared, keyed and (on a
	// miss) compiled concurrently. Cache keys stay the exact partition bitcode.
	wholeStart := time.Now()
	whole := C.LLVMWriteBitcodeToMemoryBuffer(g.module)
	if whole == nil {
		return s.emitDirectObject(g, outputPath)
	}
	defer C.LLVMDisposeMemoryBuffer(whole)
	wholeData := C.LLVMGetBufferStart(whole)
	wholeSize := C.size_t(C.LLVMGetBufferSize(whole))
	wholeDuration := time.Since(wholeStart)
	s.recordStats(func(stats *IncrementalObjectStats) {
		stats.BitcodeTime += wholeDuration
		stats.PartitionTime += wholeDuration
	})
	machineTriple := C.LLVMGetTargetMachineTriple(g.targetMachine)
	machineCPU := C.LLVMGetTargetMachineCPU(g.targetMachine)
	machineFeatures := C.LLVMGetTargetMachineFeatureString(g.targetMachine)
	defer C.LLVMDisposeMessage(machineTriple)
	defer C.LLVMDisposeMessage(machineCPU)
	defer C.LLVMDisposeMessage(machineFeatures)
	objects := make([][]byte, len(parts))
	errs := make([]error, len(parts))
	declined := make([]bool, len(parts))
	workers := runtime.GOMAXPROCS(0)
	if workers > len(parts) {
		workers = len(parts)
	}
	next := make(chan int, len(parts))
	for part := range parts {
		next <- part
	}
	close(next)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for part := range next {
				objects[part], declined[part], errs[part] = s.buildPartition(part, fastCodegen, partitionPipeline, wholeData, wholeSize, namespace, owners, ownerCount, configKey, unitID, targetID, machineTriple, machineCPU, machineFeatures)
			}
		}()
	}
	wg.Wait()
	partitionsWall := time.Since(wholeStart)
	for part := range parts {
		if declined[part] {
			return s.emitDirectObject(g, outputPath)
		}
		if errs[part] != nil {
			return errs[part]
		}
	}
	for part := range parts {
		parts[part] = filepath.Join(dir, fmt.Sprintf("part%d.o", part))
		if err := os.WriteFile(parts[part], objects[part], 0600); err != nil {
			return err
		}
	}
	mergeStart := time.Now()
	err = s.mergeObjects(dir, parts, outputPath)
	mergeDuration := time.Since(mergeStart)
	if os.Getenv("ELISACORE_BUILD_TIMING") == "1" {
		fmt.Fprintf(os.Stderr, "[build-timing] incremental object: ir %v, bitcode+partitions %v (whole bitcode %v), merge %v\n", irDuration.Round(time.Millisecond), partitionsWall.Round(time.Millisecond), wholeDuration.Round(time.Millisecond), mergeDuration.Round(time.Millisecond))
	}
	s.recordStats(func(stats *IncrementalObjectStats) { stats.MergeTime += mergeDuration })
	return err
}

func (s *IncrementalObjectSession) emitDirectObject(g *llvmGenerator, outputPath string) error {
	// A pre-optimization split may decline; its serial fallback still needs O3.
	if err := g.optimizeModule(g.optLevel); err != nil {
		return err
	}
	start := time.Now()
	err := g.emitNativeObjectFile(outputPath)
	nativeDuration := time.Since(start)
	s.recordStats(func(stats *IncrementalObjectStats) {
		stats.NativeTime += nativeDuration
		stats.SerialFallbacks++
	})
	return err
}

// buildPartition loads one partition into a private context, looks its exact
// bitcode up in the session and compiles it on a miss. declined reports a
// partition the loader cannot split; the caller then emits the module directly.
func (s *IncrementalObjectSession) buildPartition(part int, fastCodegen bool, pipeline string, wholeData *C.char, wholeSize C.size_t, namespace *C.char, owners *C.longlong, ownerCount C.size_t, configKey string, unitID, targetID [32]byte, triple, cpu, features *C.char) (object []byte, declined bool, err error) {
	partitionStart := time.Now()
	context := C.LLVMContextCreate()
	defer C.LLVMContextDispose(context)
	var message *C.char
	module := C.elisacoreLoadObjectPartition(context, wholeData, wholeSize, C.int(s.partitions), C.int(part), namespace, owners, ownerCount, &message)
	if module == nil {
		if message != nil {
			text := C.GoString(message)
			C.free(unsafe.Pointer(message))
			return nil, false, fmt.Errorf("incremental object partition %d: %s", part, text)
		}
		return nil, true, nil
	}
	defer C.LLVMDisposeModule(module)
	cloneDuration := time.Since(partitionStart)
	bitcodeStart := time.Now()
	bitcode := C.LLVMWriteBitcodeToMemoryBuffer(module)
	if uint64(C.LLVMGetBufferSize(bitcode)) > 1<<31-1 {
		C.LLVMDisposeMemoryBuffer(bitcode)
		return nil, true, nil
	}
	inputData := C.LLVMGetBufferStart(bitcode)
	inputSize := int(C.LLVMGetBufferSize(bitcode))
	inputBytes := unsafe.Slice((*byte)(unsafe.Pointer(inputData)), inputSize)
	retainInput := inputSize <= s.maxBytes-len(configKey)
	if retainInput {
		object = s.lookupObjectBytes(inputBytes, configKey)
	}
	var retainedInput string
	if object == nil && retainInput {
		retainedInput = C.GoStringN(inputData, C.int(inputSize))
	}
	C.LLVMDisposeMemoryBuffer(bitcode)
	bitcodeDuration := time.Since(bitcodeStart)
	s.recordStats(func(stats *IncrementalObjectStats) {
		stats.CloneTime += cloneDuration
		stats.BitcodeTime += bitcodeDuration
		stats.PartitionTime += cloneDuration + bitcodeDuration
	})
	if object != nil {
		return object, false, nil
	}
	nativeStart := time.Now()
	if pipeline != "" {
		object, err = emitPreoptimizedPartition(module, pipeline, triple, cpu, features)
		if err != nil {
			return nil, false, fmt.Errorf("incremental optimization partition %d: %w", part, err)
		}
	} else {
		var buffer C.LLVMMemoryBufferRef
		if C.elisacoreEmitPartitionObject(module, triple, cpu, features, cBool(fastCodegen), &buffer, &message) == 0 {
			text := "native emission failed"
			if message != nil {
				text = C.GoString(message)
				C.free(unsafe.Pointer(message))
			}
			return nil, false, fmt.Errorf("incremental object partition %d: %s", part, text)
		}
		if uint64(C.LLVMGetBufferSize(buffer)) > 1<<31-1 {
			C.LLVMDisposeMemoryBuffer(buffer)
			return nil, false, fmt.Errorf("incremental native partition exceeds buffer limit")
		}
		object = C.GoBytes(unsafe.Pointer(C.LLVMGetBufferStart(buffer)), C.int(C.LLVMGetBufferSize(buffer)))
		C.LLVMDisposeMemoryBuffer(buffer)
	}
	nativeDuration := time.Since(nativeStart)
	s.recordStats(func(stats *IncrementalObjectStats) {
		stats.NativeTime += nativeDuration
		stats.PartitionsEmitted++
	})
	if retainInput {
		s.retainObjectForSlot(retainedInput, configKey, object, incrementalObjectSlot(unitID, targetID, part))
	}
	return object, false, nil
}

func cBool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

// mergeObjects links the partition objects into one relocatable object.
func (s *IncrementalObjectSession) mergeObjects(dir string, parts []string, outputPath string) error {
	linker := s.objectMergerCommand()
	merged := filepath.Join(dir, "merged.o")
	args := append(append(append([]string{}, linker[1:]...), "-r", "-o", merged), parts...)
	if out, err := exec.Command(linker[0], args...).CombinedOutput(); err != nil {
		return fmt.Errorf("merge incremental objects: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return os.Rename(merged, outputPath)
}

func virtualObjectSource(result *semantic.Result) bool {
	identity := strings.TrimSpace(result.ActiveFile().Filename)
	return identity == "" || identity == "-" || identity == "/dev/stdin" || (strings.HasPrefix(identity, "<") && strings.HasSuffix(identity, ">"))
}

// tryPartitionedObject resolves the partition cache keys and runs the
// partitioned IR path. done is false when it does not apply.
func (s *IncrementalObjectSession) tryPartitionedObject(result *semantic.Result, outputPath string, options LLVMObjectEmitOptions) (done bool, err error) {
	probe, err := newLLVMGenerator(result)
	if err != nil {
		return false, nil
	}
	probe.optLevel = options.OptLevel
	probe.requestedTargetTriple = options.TargetTriple
	if err := probe.ensureTargetMachine(); err != nil {
		probe.dispose()
		return false, nil
	}
	codegenLevel := "default-codegen"
	if options.OptLevel == OptimizationLevel0 {
		codegenLevel = "none-codegen"
	}
	triple := C.LLVMGetTargetMachineTriple(probe.targetMachine)
	cpu := C.LLVMGetTargetMachineCPU(probe.targetMachine)
	features := C.LLVMGetTargetMachineFeatureString(probe.targetMachine)
	config, _ := json.Marshal([]string{"partitioned-ir-object-v1", C.GoString(triple), C.GoString(cpu), C.GoString(features), codegenLevel, "default-reloc", "default-code-model"})
	C.LLVMDisposeMessage(triple)
	C.LLVMDisposeMessage(cpu)
	C.LLVMDisposeMessage(features)
	probe.dispose()
	origin, err := filepath.Abs(result.ActiveFile().Filename)
	if err != nil {
		return true, err
	}
	unitID := sha256.Sum256([]byte(origin))
	err = s.writePartitionedObject(result, outputPath, options, string(config), unitID, sha256.Sum256(config))
	if errors.Is(err, errPartitionDeclined) {
		return false, nil
	}
	return true, err
}
