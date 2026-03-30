package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"llcontext/src/backend"
	"llcontext/src/semantic"
)

func buildNativeExecutable(result *semantic.Result, foreignFiles []string, outputPath string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, stderr io.Writer) (string, func(), error) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		return "", func() {}, fmt.Errorf("clang is required to build native executables: %w", err)
	}
	return buildNativeExecutableWithClang(clangPath, result, foreignFiles, outputPath, optLevel, packedProfile, stderr)
}

func buildNativeExecutableWithClang(clangPath string, result *semantic.Result, foreignFiles []string, outputPath string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, stderr io.Writer) (string, func(), error) {
	if result == nil {
		return "", func() {}, fmt.Errorf("semantic result is nil")
	}
	tempDir, err := os.MkdirTemp("", "llcontext-native-run-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	exePath := strings.TrimSpace(outputPath)
	if exePath == "" {
		exePath = filepath.Join(tempDir, "llcontext_program")
	} else if err := ensureOutputParentExists(exePath); err != nil {
		cleanup()
		return "", func() {}, err
	}

	objectPath := filepath.Join(tempDir, "llcontext_module.o")
	if err := backend.WriteLLVMObjectFileWithOptAndPackedLoweringProfile(result, objectPath, optLevel, packedProfile); err != nil {
		cleanup()
		return "", func() {}, err
	}
	headerSource, err := backend.GenerateCHeader(result)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	for _, name := range nativeHeaderNames(result, exePath) {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte(headerSource), 0o644); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}

	linkArgs := make([]string, 0, 4+len(foreignFiles))
	linkArgs = append(linkArgs, "-I", tempDir)
	if runtime.GOOS == "darwin" {
		linkArgs = append(linkArgs, "-Wl,-undefined,dynamic_lookup")
	}
	linkArgs = append(linkArgs, objectPath)
	linkArgs = append(linkArgs, foreignFiles...)
	linkArgs = append(linkArgs, "-o", exePath)

	linkCmd := exec.Command(clangPath, linkArgs...)
	linkCmd.Stdout = stderr
	linkCmd.Stderr = stderr
	if err := linkCmd.Run(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("failed to link native executable: %w", err)
	}
	return exePath, cleanup, nil
}

func runNativeExecutable(exePath string, stdout io.Writer, stderr io.Writer) error {
	var runStdout bytes.Buffer
	var runStderr bytes.Buffer
	cmd := exec.Command(exePath)
	cmd.Stdout = &runStdout
	cmd.Stderr = &runStderr
	err := cmd.Run()
	if runStdout.Len() != 0 && stdout != nil {
		_, _ = io.Copy(stdout, &runStdout)
	}
	if runStderr.Len() != 0 && stderr != nil {
		_, _ = io.Copy(stderr, &runStderr)
	}
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return err
	}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
		if status.Signaled() {
			return fmt.Errorf("native executable terminated with signal %s", status.Signal())
		}
		if status.Exited() {
			if stdout != nil {
				if runStdout.Len() != 0 {
					data := runStdout.Bytes()
					if data[len(data)-1] != '\n' {
						_, _ = fmt.Fprintln(stdout)
					}
				}
				_, _ = fmt.Fprintf(stdout, "[ result   ] %d\n", status.ExitStatus())
			}
			return nil
		}
	}
	if code := exitErr.ExitCode(); code >= 0 {
		if stdout != nil {
			if runStdout.Len() != 0 {
				data := runStdout.Bytes()
				if data[len(data)-1] != '\n' {
					_, _ = fmt.Fprintln(stdout)
				}
			}
			_, _ = fmt.Fprintf(stdout, "[ result   ] %d\n", code)
		}
		return nil
	}
	return err
}

func nativeHeaderNames(result *semantic.Result, outputPath string) []string {
	names := []string{"llcontext.h"}
	for _, candidate := range []string{nativeHeaderBaseName(outputPath), nativeHeaderBaseName(nativeResultFilename(result))} {
		if candidate == "" {
			continue
		}
		duplicate := false
		for _, existing := range names {
			if existing == candidate {
				duplicate = true
				break
			}
		}
		if !duplicate {
			names = append(names, candidate)
		}
	}
	return names
}

func nativeResultFilename(result *semantic.Result) string {
	if result == nil || result.File == nil {
		return ""
	}
	return result.File.Filename
}

func nativeHeaderBaseName(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	base := filepath.Base(trimmed)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		return ""
	}
	return stem + ".h"
}