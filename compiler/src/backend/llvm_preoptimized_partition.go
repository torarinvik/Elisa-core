//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
#include "parallel_emit.h"
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The partition has already been restricted, namespaced, pruned and verified.
// A one-part worker retains exactly these definitions. Running LLVM optimization
// in a child process avoids its process-global pass-state races; the parent owns
// only the worker's finished object bytes, suitable for ordinary disk caching.
func emitPreoptimizedPartition(bitcode []byte, pipeline string, triple, cpu, features *C.char, fragments ...*nativeFragmentContext) ([]byte, error) {
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
	// Reuse exactly the current bytes already checked by the native cache.
	// Serializing the module a second time adds work to every cache miss.
	if err := os.WriteFile(input, bitcode, 0600); err != nil {
		return nil, fmt.Errorf("write worker bitcode: %w", err)
	}

	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), partWorkerEnv+"=1", partWorkerBitcodeEnv+"="+input,
		partWorkerJobsEnv+"=1", partWorkerIndexEnv+"=0", partWorkerPipelineEnv+"="+pipeline,
		partWorkerImportEnv+"=0", partWorkerTripleEnv+"="+C.GoString(triple),
		partWorkerCPUEnv+"="+C.GoString(cpu), partWorkerFeaturesEnv+"="+C.GoString(features), partWorkerOutputEnv+"="+output)
	var fragment *nativeFragmentContext
	encoded := ""
	if len(fragments) != 0 && fragments[0] != nil {
		fragment = fragments[0]
		request := fragment.request
		request.Report = filepath.Join(dir, "fragments.json")
		data, err := json.Marshal(request)
		if err != nil {
			return nil, err
		}
		encoded = string(data)
	}
	cmd.Env = append(cmd.Env, partWorkerFragmentsEnv+"="+encoded)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("optimization worker: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if fragment != nil {
		data, err := os.ReadFile(filepath.Join(dir, "fragments.json"))
		if err != nil {
			return nil, err
		}
		var stats nativeFragmentStats
		if err := json.Unmarshal(data, &stats); err != nil {
			return nil, err
		}
		if fragment.record != nil {
			fragment.record(stats)
		}
	}
	return os.ReadFile(output)
}
