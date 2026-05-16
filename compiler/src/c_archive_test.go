package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmitCArchiveBuildsLinkableArchiveWithRuntime(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	if _, err := exec.LookPath("ar"); err != nil {
		t.Skip("ar not available")
	}

	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "first_byte.elisa")
	headerPath := filepath.Join(dir, "first_byte_abi.h")
	smokePath := filepath.Join(dir, "smoke.c")
	archivePath := filepath.Join(dir, "libfirst_byte.a")
	exePath := filepath.Join(dir, "smoke")

	writeFixtureFile(t, sourcePath, `
def first_byte_inner(text: cstr) -> i64:
    trusted Unsafe.UncheckedIndex:
        return text[0.usize()].i64()

def first_byte_impl(text: u8&) -> i64:
    return first_byte_inner(text)

export func elisa_first_byte(text: u8&) -> i64 = first_byte_impl
`)
	writeFixtureFile(t, headerPath, `#ifndef FIRST_BYTE_ABI_H
#define FIRST_BYTE_ABI_H
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
intptr_t elisa_first_byte(uint8_t* text);
#ifdef __cplusplus
}
#endif
#endif
`)
	writeFixtureFile(t, smokePath, `#include "first_byte_abi.h"
int main(void) {
    return elisa_first_byte((uint8_t*)"Zed") == 'Z' ? 0 : 1;
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "c-archive", "-O0", "-o", archivePath, sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected c-archive emit to succeed, stderr:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout from c-archive emit, got:\n%s", stdout.String())
	}
	for _, path := range []string{
		archivePath,
		filepath.Join(dir, "libfirst_byte.elisa-abi.json"),
		filepath.Join(dir, "libfirst_byte.h"),
		filepath.Join(dir, "libfirst_byte.unsafe.txt"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected c-archive output %s: %v", path, err)
		}
	}

	manifestBytes, err := os.ReadFile(filepath.Join(dir, "libfirst_byte.elisa-abi.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest cArchiveManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("decode manifest: %v\n%s", err, string(manifestBytes))
	}
	if !manifest.RuntimeIncluded {
		t.Fatalf("expected runtime object to be included in manifest:\n%s", string(manifestBytes))
	}
	if !containsString(manifest.ExportedFunctions, "elisa_first_byte") {
		t.Fatalf("expected exported function in manifest:\n%s", string(manifestBytes))
	}
	if !strings.Contains(manifest.ABIContract, "checked-in C/C++ headers") {
		t.Fatalf("expected manifest to document checked-in header contract:\n%s", string(manifestBytes))
	}

	cmd := exec.Command("clang", "-I", dir, smokePath, archivePath, "-o", exePath)
	var clangStderr bytes.Buffer
	cmd.Stderr = &clangStderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to link smoke program against archive: %v\n%s", err, clangStderr.String())
	}
	if output, err := exec.Command(exePath).CombinedOutput(); err != nil {
		t.Fatalf("smoke executable failed: %v\n%s", err, string(output))
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
