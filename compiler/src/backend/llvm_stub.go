//go:build !cgo

package backend

import (
	"fmt"

	"llcontext/src/semantic"
)

func GenerateLLVMIR(result *semantic.Result) (string, error) {
	return GenerateLLVMIRWithOpt(result, OptimizationLevel0)
}

func GenerateLLVMIRWithOpt(result *semantic.Result, optLevel OptimizationLevel) (string, error) {
	return GenerateLLVMIRWithOptAndPackedABI(result, optLevel, PackedEnumABIRowHandle)
}

func GenerateLLVMIRWithOptAndPackedABI(result *semantic.Result, optLevel OptimizationLevel, packedABI PackedEnumABI) (string, error) {
	if result == nil {
		return "", fmt.Errorf("backend requires a semantic result")
	}
	_ = optLevel
	_ = packedABI
	return "", fmt.Errorf("LLVM backend requires cgo and LLVM development libraries")
}

func WriteLLVMBitcodeFile(result *semantic.Result, outputPath string) error {
	return WriteLLVMBitcodeFileWithOpt(result, outputPath, OptimizationLevel3)
}

func WriteLLVMBitcodeFileWithOpt(result *semantic.Result, outputPath string, optLevel OptimizationLevel) error {
	return WriteLLVMBitcodeFileWithOptAndPackedABI(result, outputPath, optLevel, PackedEnumABIRowHandle)
}

func WriteLLVMBitcodeFileWithOptAndPackedABI(result *semantic.Result, outputPath string, optLevel OptimizationLevel, packedABI PackedEnumABI) error {
	if result == nil {
		return fmt.Errorf("backend requires a semantic result")
	}
	_ = outputPath
	_ = optLevel
	_ = packedABI
	return fmt.Errorf("LLVM backend requires cgo and LLVM development libraries")
}

func WriteLLVMObjectFile(result *semantic.Result, outputPath string) error {
	return WriteLLVMObjectFileWithOpt(result, outputPath, OptimizationLevel3)
}

func WriteLLVMObjectFileWithOpt(result *semantic.Result, outputPath string, optLevel OptimizationLevel) error {
	return WriteLLVMObjectFileWithOptAndPackedABI(result, outputPath, optLevel, PackedEnumABIRowHandle)
}

func WriteLLVMObjectFileWithOptAndPackedABI(result *semantic.Result, outputPath string, optLevel OptimizationLevel, packedABI PackedEnumABI) error {
	if result == nil {
		return fmt.Errorf("backend requires a semantic result")
	}
	_ = outputPath
	_ = optLevel
	_ = packedABI
	return fmt.Errorf("LLVM backend requires cgo and LLVM development libraries")
}
