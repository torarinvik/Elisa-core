//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Target.h>
#include "parallel_emit.h"

static LLVMBool elisacorePartInitNativeTarget(void) { return LLVMInitializeNativeTarget(); }
static LLVMBool elisacorePartInitNativeAsmPrinter(void) { return LLVMInitializeNativeAsmPrinter(); }
static void elisacorePartInitAllTargets(void) {
	LLVMInitializeAllTargetInfos();
	LLVMInitializeAllTargets();
	LLVMInitializeAllTargetMCs();
	LLVMInitializeAllAsmPrinters();
}
*/
import "C"

import (
	"bytes"
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

// parallelOptEnabled reports ELISACORE_PARALLEL_OPT=1: run the optimization pipeline
// per partition in the workers instead of once over the whole module. Off by default.
func parallelOptEnabled() bool {
	return strings.TrimSpace(os.Getenv("ELISACORE_PARALLEL_OPT")) == "1"
}

// parallelOptImportWeight reads ELISACORE_PARALLEL_OPT_IMPORT: under parallel
// optimization, foreign functions of at most this many instructions are kept as
// available_externally copies so they can still be inlined. 0 (default) disables it.
func parallelOptImportWeight() int64 {
	if text := strings.TrimSpace(os.Getenv("ELISACORE_PARALLEL_OPT_IMPORT")); text != "" {
		if weight, err := strconv.ParseInt(text, 10, 64); err == nil && weight > 0 {
			return weight
		}
	}
	return 0
}

// Worker-mode environment: the parent re-runs this executable once per partition with
// these set (see RunPartitionWorkerIfRequested).
const (
	partWorkerEnv         = "ELISACORE_PART_WORKER"
	partWorkerBitcodeEnv  = "ELISACORE_PART_BITCODE"
	partWorkerJobsEnv     = "ELISACORE_PART_JOBS"
	partWorkerIndexEnv    = "ELISACORE_PART_INDEX"
	partWorkerPipelineEnv = "ELISACORE_PART_PIPELINE"
	partWorkerImportEnv   = "ELISACORE_PART_IMPORT"
	partWorkerTripleEnv   = "ELISACORE_PART_TRIPLE"
	partWorkerCPUEnv      = "ELISACORE_PART_CPU"
	partWorkerFeaturesEnv = "ELISACORE_PART_FEATURES"
	partWorkerOutputEnv   = "ELISACORE_PART_OUTPUT"
)

// partitionWorkersAvailable is set once main has installed the worker entry point.
// Other executables embedding the backend (go test binaries) would not handle the
// worker environment when re-run, so they keep the serial path.
var partitionWorkersAvailable bool

// RunPartitionWorkerIfRequested runs one partition worker and exits the process when
// this executable was started as one; otherwise it returns. main calls it first.
func RunPartitionWorkerIfRequested() {
	if os.Getenv(partWorkerEnv) != "1" {
		partitionWorkersAvailable = true
		return
	}
	jobs, err1 := strconv.Atoi(os.Getenv(partWorkerJobsEnv))
	part, err2 := strconv.Atoi(os.Getenv(partWorkerIndexEnv))
	importWeight, err3 := strconv.ParseInt(os.Getenv(partWorkerImportEnv), 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || jobs < 1 || part < 0 || part >= jobs {
		fmt.Fprintln(os.Stderr, "elisac partition worker: malformed worker environment")
		os.Exit(2)
	}
	if C.elisacorePartInitNativeTarget() != 0 || C.elisacorePartInitNativeAsmPrinter() != 0 {
		fmt.Fprintln(os.Stderr, "elisac partition worker: failed to initialize native LLVM target")
		os.Exit(1)
	}
	C.elisacorePartInitAllTargets()
	strs := []string{partWorkerBitcodeEnv, partWorkerPipelineEnv, partWorkerTripleEnv, partWorkerCPUEnv, partWorkerFeaturesEnv, partWorkerOutputEnv}
	cs := make([]*C.char, len(strs))
	for i, name := range strs {
		cs[i] = C.CString(os.Getenv(name))
	}
	var errMessage *C.char
	ok := C.elisacorePartitionWorker(cs[0], C.int(jobs), C.int(part), cs[1], C.longlong(importWeight), cs[2], cs[3], cs[4], cs[5], &errMessage)
	if ok == 0 {
		text := "unknown error"
		if errMessage != nil {
			text = C.GoString(errMessage)
		}
		fmt.Fprintf(os.Stderr, "elisac partition worker %d: %s\n", part, text)
		os.Exit(1)
	}
	os.Exit(0)
}

// emitObjectParallel writes the module to outputPath with `jobs` worker processes (see
// parallel_emit.c), each running `pipeline` (when non-empty) on its partition before
// code generation. It reports false, with no error, when the module is too small to
// partition and the caller should use the serial path.
func (g *llvmGenerator) emitObjectParallel(outputPath string, jobs int, pipeline string, importWeight int64) (bool, error) {
	if !partitionWorkersAvailable {
		return false, nil
	}
	self, err := os.Executable()
	if err != nil {
		return false, nil
	}
	bitcodePath := outputPath + ".parts.bc"
	partPaths := make([]string, jobs)
	for k := range partPaths {
		partPaths[k] = fmt.Sprintf("%s.part%d.o", outputPath, k)
	}
	defer func() {
		if os.Getenv("ELISACORE_PART_KEEP") == "1" {
			return
		}
		_ = os.Remove(bitcodePath)
		for _, p := range partPaths {
			_ = os.Remove(p)
		}
	}()
	bitcodeC := C.CString(bitcodePath)
	status := C.elisacorePartitionPrepare(g.module, C.int(jobs), C.longlong(parallelEmitMinWeight), bitcodeC)
	C.free(unsafe.Pointer(bitcodeC))
	if status == 0 {
		return false, nil
	}
	if status < 0 {
		return true, fmt.Errorf("failed to write partition bitcode %s", bitcodePath)
	}
	triple := C.LLVMGetTargetMachineTriple(g.targetMachine)
	cpu := C.LLVMGetTargetMachineCPU(g.targetMachine)
	features := C.LLVMGetTargetMachineFeatureString(g.targetMachine)
	baseEnv := append(os.Environ(),
		partWorkerEnv+"=1",
		partWorkerBitcodeEnv+"="+bitcodePath,
		partWorkerJobsEnv+"="+strconv.Itoa(jobs),
		partWorkerPipelineEnv+"="+pipeline,
		partWorkerImportEnv+"="+strconv.FormatInt(importWeight, 10),
		partWorkerTripleEnv+"="+C.GoString(triple),
		partWorkerCPUEnv+"="+C.GoString(cpu),
		partWorkerFeaturesEnv+"="+C.GoString(features),
	)
	C.LLVMDisposeMessage(triple)
	C.LLVMDisposeMessage(cpu)
	C.LLVMDisposeMessage(features)

	cmds := make([]*exec.Cmd, jobs)
	outputs := make([]bytes.Buffer, jobs)
	for k := range cmds {
		cmd := exec.Command(self)
		cmd.Env = append(append([]string{}, baseEnv...),
			partWorkerIndexEnv+"="+strconv.Itoa(k),
			partWorkerOutputEnv+"="+partPaths[k])
		cmd.Stdout = &outputs[k]
		cmd.Stderr = &outputs[k]
		if err := cmd.Start(); err != nil {
			for _, started := range cmds[:k] {
				_ = started.Wait()
			}
			return true, fmt.Errorf("failed to start partition worker %d: %v", k, err)
		}
		cmds[k] = cmd
	}
	var firstErr error
	for k, cmd := range cmds {
		if err := cmd.Wait(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("failed to write LLVM object file to %s: partition worker %d: %v: %s", outputPath, k, err, strings.TrimSpace(outputs[k].String()))
		}
	}
	if firstErr != nil {
		return true, firstErr
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
