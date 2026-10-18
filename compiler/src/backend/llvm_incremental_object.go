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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"elisacore/src/semantic"
)

// WriteObject regenerates and optimizes current IR, then reuses native partitions
// only when their complete current bitcode and target-machine inputs match. Global
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
	irStart := time.Now()
	g, err := compileLLVMModuleWithTargetDebugTrace(result, options.OptLevel, options.PackedProfile, options.TargetTriple, options.DebugInfo, options.Trace)
	if err != nil {
		return err
	}
	defer g.dispose()
	if err := g.ensureTargetMachine(); err != nil {
		return err
	}
	if err := g.optimizeModule(options.OptLevel); err != nil {
		return err
	}
	s.recordStats(func(stats *IncrementalObjectStats) { stats.IRTime += time.Since(irStart) })
	filename := result.ActiveFile().Filename
	identity := strings.TrimSpace(filename)
	virtual := identity == "" || identity == "-" || identity == "/dev/stdin" || (strings.HasPrefix(identity, "<") && strings.HasSuffix(identity, ">"))
	if options.Serial || virtual || native.Triple != target.Triple || int64(C.elisacoreObjectPartitionWeight(g.module)) < s.minWeight {
		return s.emitDirectObject(g, outputPath)
	}
	// Fixed codegen/relocation/code-model settings are shared with writeObjectFile.
	// Actual resolved machine strings, rather than environment spellings, are keys.
	triple := C.LLVMGetTargetMachineTriple(g.targetMachine)
	cpu := C.LLVMGetTargetMachineCPU(g.targetMachine)
	features := C.LLVMGetTargetMachineFeatureString(g.targetMachine)
	config, _ := json.Marshal([]string{"incremental-object-v1", C.GoString(triple), C.GoString(cpu), C.GoString(features), "default-codegen", "default-reloc", "default-code-model"})
	C.LLVMDisposeMessage(triple)
	C.LLVMDisposeMessage(cpu)
	C.LLVMDisposeMessage(features)
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
	for part := range parts {
		partitionStart := time.Now()
		module := C.elisacoreCloneObjectPartition(g.module, C.int(s.partitions), C.int(part), namespace)
		if module == nil {
			return s.emitDirectObject(g, outputPath)
		}
		cloneDuration := time.Since(partitionStart)
		bitcodeStart := time.Now()
		bitcode := C.LLVMWriteBitcodeToMemoryBuffer(module)
		if uint64(C.LLVMGetBufferSize(bitcode)) > 1<<31-1 {
			C.LLVMDisposeMemoryBuffer(bitcode)
			C.LLVMDisposeModule(module)
			return s.emitDirectObject(g, outputPath)
		}
		input := C.GoStringN(C.LLVMGetBufferStart(bitcode), C.int(C.LLVMGetBufferSize(bitcode)))
		C.LLVMDisposeMemoryBuffer(bitcode)
		bitcodeDuration := time.Since(bitcodeStart)
		s.recordStats(func(stats *IncrementalObjectStats) {
			stats.CloneTime += cloneDuration
			stats.BitcodeTime += bitcodeDuration
			stats.PartitionTime += cloneDuration + bitcodeDuration
		})
		object := s.lookupObject(input, string(config))
		if object == nil {
			nativeStart := time.Now()
			var buffer C.LLVMMemoryBufferRef
			var message *C.char
			failed := C.LLVMTargetMachineEmitToMemoryBuffer(g.targetMachine, module, C.LLVMObjectFile, &message, &buffer)
			if failed != 0 {
				C.LLVMDisposeModule(module)
				return fmt.Errorf("incremental object partition %d: %s", part, disposeLLVMMessage(message, "native emission failed"))
			}
			if uint64(C.LLVMGetBufferSize(buffer)) > 1<<31-1 {
				C.LLVMDisposeMemoryBuffer(buffer)
				C.LLVMDisposeModule(module)
				return fmt.Errorf("incremental native partition exceeds buffer limit")
			}
			object = C.GoBytes(unsafe.Pointer(C.LLVMGetBufferStart(buffer)), C.int(C.LLVMGetBufferSize(buffer)))
			C.LLVMDisposeMemoryBuffer(buffer)
			nativeDuration := time.Since(nativeStart)
			s.recordStats(func(stats *IncrementalObjectStats) {
				stats.NativeTime += nativeDuration
				stats.PartitionsEmitted++
			})
			s.retainObject(input, string(config), object)
		}
		C.LLVMDisposeModule(module)
		parts[part] = filepath.Join(dir, fmt.Sprintf("part%d.o", part))
		if err := os.WriteFile(parts[part], object, 0600); err != nil {
			return err
		}
	}
	mergeStart := time.Now()
	linker := strings.Fields(strings.TrimSpace(os.Getenv("ELISA_LD")))
	if len(linker) == 0 {
		linker = []string{"ld"}
	}
	merged := filepath.Join(dir, "merged.o")
	args := append(append(append([]string{}, linker[1:]...), "-r", "-o", merged), parts...)
	if out, err := exec.Command(linker[0], args...).CombinedOutput(); err != nil {
		return fmt.Errorf("merge incremental objects: %v: %s", err, strings.TrimSpace(string(out)))
	}
	err = os.Rename(merged, outputPath)
	mergeDuration := time.Since(mergeStart)
	s.recordStats(func(stats *IncrementalObjectStats) { stats.MergeTime += mergeDuration })
	return err
}

func (s *IncrementalObjectSession) emitDirectObject(g *llvmGenerator, outputPath string) error {
	start := time.Now()
	err := g.emitNativeObjectFile(outputPath)
	nativeDuration := time.Since(start)
	s.recordStats(func(stats *IncrementalObjectStats) {
		stats.NativeTime += nativeDuration
		stats.SerialFallbacks++
	})
	return err
}
