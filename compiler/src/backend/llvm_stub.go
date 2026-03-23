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
	return GenerateLLVMIRWithOptAndPackedLoweringProfile(result, optLevel, DefaultPackedLoweringProfile())
}

func GenerateLLVMIRWithOptAndPackedLoweringProfile(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile) (string, error) {
	if result == nil {
		return "", fmt.Errorf("backend requires a semantic result")
	}
	_ = optLevel
	_ = profile
	return "", fmt.Errorf("LLVM backend requires cgo and LLVM development libraries")
}

func GenerateLLVMIRWithOptAndPackedABI(result *semantic.Result, optLevel OptimizationLevel, packedABI PackedEnumABI) (string, error) {
	profile, err := LegacyPackedLoweringProfile(packedABI)
	if err != nil {
		return "", err
	}
	return GenerateLLVMIRWithOptAndPackedLoweringProfile(result, optLevel, profile)
}

func WriteLLVMBitcodeFile(result *semantic.Result, outputPath string) error {
	return WriteLLVMBitcodeFileWithOpt(result, outputPath, OptimizationLevel3)
}

func WriteLLVMBitcodeFileWithOpt(result *semantic.Result, outputPath string, optLevel OptimizationLevel) error {
	return WriteLLVMBitcodeFileWithOptAndPackedLoweringProfile(result, outputPath, optLevel, DefaultPackedLoweringProfile())
}

func WriteLLVMBitcodeFileWithOptAndPackedLoweringProfile(result *semantic.Result, outputPath string, optLevel OptimizationLevel, profile PackedLoweringProfile) error {
	if result == nil {
		return fmt.Errorf("backend requires a semantic result")
	}
	_ = outputPath
	_ = optLevel
	_ = profile
	return fmt.Errorf("LLVM backend requires cgo and LLVM development libraries")
}

func WriteLLVMBitcodeFileWithOptAndPackedABI(result *semantic.Result, outputPath string, optLevel OptimizationLevel, packedABI PackedEnumABI) error {
	profile, err := LegacyPackedLoweringProfile(packedABI)
	if err != nil {
		return err
	}
	return WriteLLVMBitcodeFileWithOptAndPackedLoweringProfile(result, outputPath, optLevel, profile)
}

func WriteLLVMObjectFile(result *semantic.Result, outputPath string) error {
	return WriteLLVMObjectFileWithOpt(result, outputPath, OptimizationLevel3)
}

func WriteLLVMObjectFileWithOpt(result *semantic.Result, outputPath string, optLevel OptimizationLevel) error {
	return WriteLLVMObjectFileWithOptAndPackedLoweringProfile(result, outputPath, optLevel, DefaultPackedLoweringProfile())
}

func WriteLLVMObjectFileWithOptAndPackedLoweringProfile(result *semantic.Result, outputPath string, optLevel OptimizationLevel, profile PackedLoweringProfile) error {
	if result == nil {
		return fmt.Errorf("backend requires a semantic result")
	}
	_ = outputPath
	_ = optLevel
	_ = profile
	return fmt.Errorf("LLVM backend requires cgo and LLVM development libraries")
}

func WriteLLVMObjectFileWithOptAndPackedABI(result *semantic.Result, outputPath string, optLevel OptimizationLevel, packedABI PackedEnumABI) error {
	profile, err := LegacyPackedLoweringProfile(packedABI)
	if err != nil {
		return err
	}
	return WriteLLVMObjectFileWithOptAndPackedLoweringProfile(result, outputPath, optLevel, profile)
}
