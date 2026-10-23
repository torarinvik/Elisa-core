//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/BitWriter.h>
#include "parallel_emit.h"
*/
import "C"

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"
)

// The partition has already been restricted, namespaced, pruned and verified.
// A one-part worker retains exactly these definitions. Running LLVM optimization
// in a child process avoids its process-global pass-state races; the parent owns
// only the worker's finished object bytes, suitable for ordinary disk caching.
func emitPreoptimizedPartition(module C.LLVMModuleRef, pipeline string, triple, cpu, features *C.char) ([]byte, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "elisa-opt-part-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	input, output := filepath.Join(dir, "input.bc"), filepath.Join(dir, "output.o")
	path := C.CString(input)
	status := C.LLVMWriteBitcodeToFile(module, path)
	C.free(unsafe.Pointer(path))
	if status != 0 {
		return nil, fmt.Errorf("write worker bitcode")
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), partWorkerEnv+"=1", partWorkerBitcodeEnv+"="+input,
		partWorkerJobsEnv+"=1", partWorkerIndexEnv+"=0", partWorkerPipelineEnv+"="+pipeline,
		partWorkerImportEnv+"=0", partWorkerTripleEnv+"="+C.GoString(triple),
		partWorkerCPUEnv+"="+C.GoString(cpu), partWorkerFeaturesEnv+"="+C.GoString(features), partWorkerOutputEnv+"="+output)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("optimization worker: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return os.ReadFile(output)
}
