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

func WriteLLVMBitcodeFile(result *semantic.Result, outputPath string) error {
	if result == nil {
		return fmt.Errorf("backend requires a semantic result")
	}
	_ = outputPath
	return fmt.Errorf("LLVM backend requires cgo and LLVM development libraries")
}

func WriteLLVMObjectFile(result *semantic.Result, outputPath string) error {
	if result == nil {
		return fmt.Errorf("backend requires a semantic result")
	}
	_ = outputPath
	return fmt.Errorf("LLVM backend requires cgo and LLVM development libraries")
}
