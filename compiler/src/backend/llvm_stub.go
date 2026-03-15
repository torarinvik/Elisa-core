//go:build !cgo

package backend

import (
	"fmt"

	"llcontext/src/semantic"
)

func GenerateLLVMIR(result *semantic.Result) (string, error) {
	if result == nil {
		return "", fmt.Errorf("backend requires a semantic result")
	}
	return "", fmt.Errorf("LLVM backend requires cgo and LLVM development libraries")
}
