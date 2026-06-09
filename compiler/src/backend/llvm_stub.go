//go:build !cgo

package backend

import (
	"fmt"

	"elisacore/src/semantic"
)

func GenerateLLVMIR(result *semantic.Result) (string, error) {
	return GenerateLLVMIRWithOpt(result, OptimizationLevel0)
}

func GenerateLLVMIRWithOpt(result *semantic.Result, optLevel OptimizationLevel) (string, error) {
	return GenerateLLVMIRWithOptAndPackedLoweringProfile(result, optLevel, DefaultPackedLoweringProfile())
}

func GenerateLLVMIRWithOptAndPackedLoweringProfile(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile) (string, error) {
	return GenerateLLVMIRWithOptAndPackedLoweringProfileForTarget(result, optLevel, profile, "")
}

func GenerateLLVMIRWithOptAndPackedLoweringProfileForTarget(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string) (string, error) {
	if result == nil {
		return "", fmt.Errorf("backend requires a semantic result")
	}
	_ = optLevel
	_ = profile
	_ = targetTriple
	return "", fmt.Errorf("LLVM backend requires cgo and LLVM development libraries")
}

func GenerateLLVMIRWithWarnings(result *semantic.Result, optLevel OptimizationLevel, profile PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool) (string, []string, error) {
	_ = optLevel
	_ = profile
	_ = targetTriple
	_ = debugInfo
	_ = traceInfo
	if result == nil {
		return "", nil, fmt.Errorf("backend requires a semantic result")
	}
	return "", nil, fmt.Errorf("LLVM backend requires cgo and LLVM development libraries")
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

func WriteLLVMObjectFile(result *semantic.Result, outputPath string) error {
	return WriteLLVMObjectFileWithOpt(result, outputPath, OptimizationLevel3)
}

func WriteLLVMObjectFileWithOpt(result *semantic.Result, outputPath string, optLevel OptimizationLevel) error {
	return WriteLLVMObjectFileWithOptAndPackedLoweringProfile(result, outputPath, optLevel, DefaultPackedLoweringProfile())
}

func WriteLLVMObjectFileWithOptAndPackedLoweringProfile(result *semantic.Result, outputPath string, optLevel OptimizationLevel, profile PackedLoweringProfile) error {
	return WriteLLVMObjectFileWithOptions(result, outputPath, LLVMObjectEmitOptions{
		OptLevel:      optLevel,
		PackedProfile: profile,
	})
}

type LLVMObjectEmitOptions struct {
	OptLevel      OptimizationLevel
	PackedProfile PackedLoweringProfile
	TargetTriple  string
}

func WriteLLVMObjectFileWithOptions(result *semantic.Result, outputPath string, options LLVMObjectEmitOptions) error {
	if result == nil {
		return fmt.Errorf("backend requires a semantic result")
	}
	_ = outputPath
	_ = options
	return fmt.Errorf("LLVM backend requires cgo and LLVM development libraries")
}
