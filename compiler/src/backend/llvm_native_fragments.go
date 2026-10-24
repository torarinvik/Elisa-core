//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/BitReader.h>
#include <llvm-c/BitWriter.h>
#include "parallel_emit.h"
*/
import "C"

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"unsafe"
)

const nativeFragmentCount = 4
const partWorkerFragmentsEnv = "ELISACORE_PART_NATIVE_FRAGMENTS"

type nativeFragmentRequest struct {
	Root, Namespace, Target, Report string
	Merger                          []string
	MaxBytes                        int64
}
type nativeFragmentStats struct {
	Emitted, Reused int
	Declined        bool
}
type nativeFragmentContext struct {
	request nativeFragmentRequest
	record  func(nativeFragmentStats)
}

func nativeFragmentsEnabled() bool { return os.Getenv("ELISACORE_NATIVE_FRAGMENTS") == "1" }

// A merged unit depends on the merger as well as LLVM. Fragment objects are
// independently keyed, but the enclosing pre-optimization object cache must not
// retrieve a unit merged with a different command or executable.
func (s *IncrementalObjectSession) nativeFragmentMergeKey() (string, error) {
	command, path, err := s.ObjectMergerCacheInputs()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	key, err := json.Marshal([]any{command, path, fmt.Sprintf("%x", digest)})
	return string(key), err
}

// This runs only in the isolated optimizer worker, after the entire unit has
// passed the unchanged optimization pipeline. Symbol ownership is stable across
// edits; exact fragment bitcode and target settings remain the cache authority.
func emitNativeFragments(input, output string, request nativeFragmentRequest, triple, cpu, features *C.char) (stats nativeFragmentStats, err error) {
	ctx := C.LLVMContextCreate()
	defer C.LLVMContextDispose(ctx)
	var buffer C.LLVMMemoryBufferRef
	var message *C.char
	path := C.CString(input)
	defer C.free(unsafe.Pointer(path))
	if C.LLVMCreateMemoryBufferWithContentsOfFile(path, &buffer, &message) != 0 {
		if message != nil {
			defer C.LLVMDisposeMessage(message)
			return stats, fmt.Errorf("read optimized bitcode: %s", C.GoString(message))
		}
		return stats, fmt.Errorf("read optimized bitcode")
	}
	defer C.LLVMDisposeMemoryBuffer(buffer)
	var module C.LLVMModuleRef
	if C.LLVMParseBitcodeInContext2(ctx, buffer, &module) != 0 {
		return stats, fmt.Errorf("parse optimized bitcode")
	}
	defer C.LLVMDisposeModule(module)
	emit := func(m C.LLVMModuleRef) ([]byte, error) {
		var object C.LLVMMemoryBufferRef
		var msg *C.char
		if C.elisacoreEmitPartitionObject(m, triple, cpu, features, 0, &object, &msg) == 0 {
			if msg != nil {
				defer C.free(unsafe.Pointer(msg))
				return nil, fmt.Errorf("native fragment: %s", C.GoString(msg))
			}
			return nil, fmt.Errorf("native fragment emission failed")
		}
		defer C.LLVMDisposeMemoryBuffer(object)
		size := uint64(C.LLVMGetBufferSize(object))
		if size > 1<<31-1 {
			return nil, fmt.Errorf("native fragment exceeds buffer limit")
		}
		return C.GoBytes(unsafe.Pointer(C.LLVMGetBufferStart(object)), C.int(size)), nil
	}
	fallback := func() (nativeFragmentStats, error) {
		stats.Declined = true
		data, e := emit(module)
		if e != nil {
			return stats, e
		}
		return stats, os.WriteFile(output, data, 0600)
	}
	var count C.size_t
	owners := C.elisacoreNativeFragmentOwners(module, nativeFragmentCount, &count)
	if owners == nil {
		return fallback()
	}
	defer C.free(unsafe.Pointer(owners))
	namespace := C.CString(request.Namespace)
	defer C.free(unsafe.Pointer(namespace))
	// Prepare every clone before publishing anything or invoking native emission.
	// A conservative clone decline can then fall back to the untouched whole unit.
	modules := make([]C.LLVMModuleRef, nativeFragmentCount)
	defer func() {
		for _, m := range modules {
			if m != nil {
				C.LLVMDisposeModule(m)
			}
		}
	}()
	for part := range modules {
		modules[part] = C.elisacoreCloneObjectPartitionPrechecked(module, nativeFragmentCount, C.int(part), namespace, owners, count)
		if modules[part] == nil {
			return fallback()
		}
	}
	disk := &incrementalObjectDisk{root: request.Root, maxBytes: request.MaxBytes}
	config, _ := json.Marshal([]string{"native-fragments-v1", request.Target, request.Namespace, "4", "default-codegen", "default-reloc", "default-code-model"})
	dir, e := os.MkdirTemp(filepath.Dir(output), "fragments-")
	if e != nil {
		return stats, e
	}
	defer os.RemoveAll(dir)
	objects := make([]string, nativeFragmentCount)
	for part, m := range modules {
		bitcode := C.LLVMWriteBitcodeToMemoryBuffer(m)
		if bitcode == nil {
			return stats, fmt.Errorf("serialize native fragment %d", part)
		}
		size := uint64(C.LLVMGetBufferSize(bitcode))
		if size > 1<<31-1 {
			C.LLVMDisposeMemoryBuffer(bitcode)
			return stats, fmt.Errorf("native fragment input exceeds buffer limit")
		}
		bytes := unsafe.Slice((*byte)(unsafe.Pointer(C.LLVMGetBufferStart(bitcode))), int(size))
		object := disk.load(bytes, string(config))
		if object != nil {
			stats.Reused++
		} else {
			object, e = emit(m)
			if e != nil {
				C.LLVMDisposeMemoryBuffer(bitcode)
				return stats, e
			}
			stats.Emitted++
			disk.store(string(bytes), string(config), object)
		}
		C.LLVMDisposeMemoryBuffer(bitcode)
		objects[part] = filepath.Join(dir, fmt.Sprintf("%d.o", part))
		if e = os.WriteFile(objects[part], object, 0600); e != nil {
			return stats, e
		}
	}
	if len(request.Merger) == 0 {
		return stats, fmt.Errorf("native fragment merger missing")
	}
	args := append(append(append([]string{}, request.Merger[1:]...), "-r", "-o", output), objects...)
	deadline, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, e := exec.CommandContext(deadline, request.Merger[0], args...).CombinedOutput(); e != nil {
		return stats, fmt.Errorf("merge native fragments: %w: %s", e, out)
	}
	return stats, nil
}
