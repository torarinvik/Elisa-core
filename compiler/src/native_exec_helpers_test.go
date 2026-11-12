package main

import (
	"elisacore/src/backend"
	"elisacore/src/semantic"
	"fmt"
	"io"
	"os/exec"
)

func buildNativeExecutable(result *semantic.Result, foreignFiles []string, linkFlags []string, outputPath string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool, stderr io.Writer) (string, func(), error) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		return "", func() {}, fmt.Errorf("clang is required to build native executables: %w", err)
	}
	exePath, cleanup, _, err := buildNativeExecutableWithClang(clangPath, result, foreignFiles, linkFlags, outputPath, optLevel, packedProfile, targetTriple, debugInfo, traceInfo, stderr)
	return exePath, cleanup, err
}
