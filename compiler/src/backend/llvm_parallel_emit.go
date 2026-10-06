//go:build cgo

package backend

/*
#cgo linux LDFLAGS: -lpthread
#include <stdlib.h>
#include "parallel_emit.h"
*/
import "C"

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"unsafe"
)

// parallelEmitMinWeight is the partitioned instruction count below which the serial
// path is used, so small programs do not pay for bitcode round-trips and `ld -r`.
const parallelEmitMinWeight = 2000

// codegenJobs reads ELISACORE_CODEGEN_JOBS (clamped to 1..64); the default is
// min(NumCPU, 16).
func codegenJobs() int {
	if text := strings.TrimSpace(os.Getenv("ELISACORE_CODEGEN_JOBS")); text != "" {
		if jobs, err := strconv.Atoi(text); err == nil {
			if jobs < 1 {
				return 1
			}
			if jobs > 64 {
				return 64
			}
			return jobs
		}
	}
	jobs := runtime.NumCPU()
	if jobs > 16 {
		jobs = 16
	}
	if jobs < 1 {
		jobs = 1
	}
	return jobs
}

// emitObjectParallel writes the optimized module to outputPath with `jobs` code
// generators (see parallel_emit.c). It reports false, with no error, when the module is
// too small to partition and the caller should use the serial path.
func (g *llvmGenerator) emitObjectParallel(outputPath string, jobs int) (bool, error) {
	partPaths := make([]string, jobs)
	cPaths := make([]*C.char, jobs)
	for k := range partPaths {
		partPaths[k] = fmt.Sprintf("%s.part%d.o", outputPath, k)
		cPaths[k] = C.CString(partPaths[k])
	}
	defer func() {
		for _, p := range cPaths {
			C.free(unsafe.Pointer(p))
		}
		for _, p := range partPaths {
			_ = os.Remove(p)
		}
	}()
	pathArray := C.malloc(C.size_t(jobs) * C.size_t(unsafe.Sizeof(uintptr(0))))
	defer C.free(pathArray)
	slots := unsafe.Slice((**C.char)(pathArray), jobs)
	copy(slots, cPaths)

	var errMessage *C.char
	status := C.elisacoreParallelEmit(g.module, g.targetMachine, C.int(jobs), C.longlong(parallelEmitMinWeight), (**C.char)(pathArray), &errMessage)
	if status == 0 {
		return false, nil
	}
	if status < 0 {
		text := "unknown parallel code generation error"
		if errMessage != nil {
			text = C.GoString(errMessage)
			C.free(unsafe.Pointer(errMessage))
		}
		return true, fmt.Errorf("failed to write LLVM object file to %s: %s", outputPath, text)
	}

	linker := strings.TrimSpace(os.Getenv("ELISA_LD"))
	if linker == "" {
		linker = "ld"
	}
	// Like stage1 (which runs it through the shell), $ELISA_LD may carry arguments.
	fields := strings.Fields(linker)
	args := append(append(fields[1:], "-r", "-o", outputPath), partPaths...)
	cmd := exec.Command(fields[0], args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return true, fmt.Errorf("failed to merge partition objects into %s with %s -r: %v: %s", outputPath, linker, err, strings.TrimSpace(string(output)))
	}
	return true, nil
}
