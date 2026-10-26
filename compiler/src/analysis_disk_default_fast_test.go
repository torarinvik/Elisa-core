package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nil mode removes the variable entirely, exercising both semantic package-init
// configuration and CLI lazy scratch scheduling in a genuine fresh process.
func runDefaultFastPreparationChild(t *testing.T, mode *string, cache string, cold bool, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	native := len(args) > 0 && args[0] == "LAB_NATIVE"
	testRun := "^TestBuildObjectCacheMainAcrossProcesses$"
	if native {
		args = args[1:]
		testRun = "^TestDefaultFastPreparationNativeChild$"
	}
	cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=" + testRun, "--"}, args...)...)
	controls := map[string]bool{"ELISACORE_RETAINED_FAST_PREPARE": true, "ELISACORE_ANALYSIS_CACHE": true, "ELISACORE_BUILD_CACHE": true, "ELISACORE_RETAINED_SERVER": true, "ELISACORE_ANALYSIS_BOOTSTRAP": true, "ELISACORE_ANALYSIS_BOOTSTRAP_DECLS": true, "ELISACORE_ANALYSIS_BOOTSTRAP_SECONDS": true, "ELISAC_BUILD_CACHE_DIR": true, "ELISACORE_ANALYSIS_CACHE_DEBUG": true, "TEST_BUILD_CACHE_MAIN": true, "TEST_DEFAULT_FAST_NATIVE": true, "ELISACORE_TEST_NATIVE_WORKERS": true}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !controls[name] {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "TEST_BUILD_CACHE_MAIN=1", "ELISACORE_TEST_NATIVE_WORKERS=1", "ELISAC_BUILD_CACHE_DIR="+cache, "ELISACORE_ANALYSIS_CACHE_DEBUG=1")
	if native {
		cmd.Env = append(cmd.Env, "TEST_DEFAULT_FAST_NATIVE=1")
	}
	if mode != nil {
		cmd.Env = append(cmd.Env, "ELISACORE_RETAINED_FAST_PREPARE="+*mode)
	}
	if cold {
		cmd.Env = append(cmd.Env, "ELISACORE_ANALYSIS_CACHE=0", "ELISACORE_BUILD_CACHE=0", "ELISACORE_RETAINED_SERVER=")
	}
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	if failure, ok := err.(*exec.ExitError); ok && ctx.Err() == nil {
		return string(output), failure.ExitCode()
	}
	t.Fatalf("fresh fast-preparation child: %v %s", err, output)
	return "", -1
}

func defaultFastProofTranscript(text string) string {
	var lines []string
	for _, line := range strings.Split(diskDiagnosticTranscript(text), "\n") {
		// Solver telemetry naturally differs under query/disk hits. Preserve every
		// proof, goal, known-fact and actual diagnostic line exactly.
		if strings.HasPrefix(strings.TrimSpace(line), "SMT tier:") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func TestDefaultFastPreparationUnsetFreshProcessBodyEditMatchesOffAndCold(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "program.elisa")
	base := retainedCodegenBase
	edited := strings.Replace(base, "return p.a + p.b", "return p.a + p.b + 1", 1)
	off, empty, shadow := "off", "", "shadow"
	for _, test := range []struct {
		name     string
		mode     *string
		wantFast bool
	}{{"unset", nil, true}, {"off", &off, false}, {"explicit-empty", &empty, false}, {"shadow", &shadow, false}} {
		t.Run(test.name, func(t *testing.T) {
			cache := filepath.Join(dir, "cache-"+test.name)
			if err := os.WriteFile(source, []byte(base), 0600); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(dir, test.name+".ll")
			args := []string{"-emit", "llvm", "-strict", "--explain", "--explain-hole", "-o", output, source}
			before, code := runDefaultFastPreparationChild(t, test.mode, cache, false, args...)
			if code != 0 {
				t.Fatalf("baseline failed: %d %s", code, before)
			}
			if err := os.WriteFile(source, []byte(edited), 0600); err != nil {
				t.Fatal(err)
			}
			got, code := runDefaultFastPreparationChild(t, test.mode, cache, false, args...)
			if code != 0 || strings.Contains(got, "cold analysis") || !strings.Contains(got, "update changed=2") {
				t.Fatalf("actual edit/dependent caller did not replay: %d %s", code, got)
			}
			if test.wantFast {
				if !strings.Contains(got, "fast=true") || !strings.Contains(got, "scratch parsing deferred until needed") || strings.Contains(got, "prepared scratch in=") {
					t.Fatalf("genuinely unset default missed verified lazy fast path: %s", got)
				}
			} else if strings.Contains(got, "fast=true") {
				t.Fatalf("explicit mode %s ignored: %s", test.name, got)
			}
			coldOut := filepath.Join(dir, test.name+"-cold.ll")
			cold, coldCode := runDefaultFastPreparationChild(t, &off, filepath.Join(dir, "cold"), true, "-emit", "llvm", "-strict", "--explain", "--explain-hole", "-o", coldOut, source)
			if coldCode != 0 || test.name != "shadow" && defaultFastProofTranscript(got) != defaultFastProofTranscript(cold) {
				t.Fatalf("default/off proof+diagnostic output differs from independent cold: %d %s / %s", coldCode, got, cold)
			}
			a, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(coldOut)
			if err != nil {
				t.Fatal(err)
			}
			// This comparison grants no generated-name equivalence authority.
			// Every emitted byte must match the independent cold compilation.
			if !bytes.Equal(a, b) {
				t.Fatal("edited LLVM IR differs from independent cold")
			}
			if test.name == "unset" || test.name == "off" {
				binary := filepath.Join(dir, test.name+"-native")
				// Warm a distinct baseline namespace, then force the edited native
				// command through UpdatePrepared instead of an unchanged-state hit.
				nativeCache := filepath.Join(dir, "native-cache-"+test.name)
				if err := os.WriteFile(source, []byte(base), 0600); err != nil {
					t.Fatal(err)
				}
				nativeArgs := []string{"LAB_NATIVE", "-emit", "obj", "-strict", "--explain", "--explain-hole", "-o", binary, source}
				warm, warmCode := runDefaultFastPreparationChild(t, test.mode, nativeCache, false, nativeArgs...)
				if warmCode != 0 {
					t.Fatalf("native baseline failed: %d %s", warmCode, warm)
				}
				if got := defaultFastRuntimeStatus(t, binary); got != 4 {
					t.Fatalf("native baseline runtime=%d want4; trace: %s", got, warm)
				}
				if err := os.WriteFile(source, []byte(edited), 0600); err != nil {
					t.Fatal(err)
				}
				native, nativeCode := runDefaultFastPreparationChild(t, test.mode, nativeCache, false, nativeArgs...)
				if nativeCode != 0 {
					t.Fatalf("native control failed: %d %s", nativeCode, native)
				}
				if test.wantFast && !strings.Contains(native, "fast=true") {
					t.Fatalf("native default control skipped actual fast replay; baseline: %s; edited: %s", warm, native)
				}
				if got := defaultFastRuntimeStatus(t, binary); got != 5 {
					t.Fatalf("native edited runtime=%d want5; trace: %s", got, native)
				}
			}
		})
	}
	coldBinary := filepath.Join(dir, "cold-native")
	output, code := runDefaultFastPreparationChild(t, &off, filepath.Join(dir, "cold-native-cache"), true, "LAB_NATIVE", "-emit", "obj", "-strict", "--explain", "--explain-hole", "-o", coldBinary, source)
	if code != 0 || defaultFastRuntimeStatus(t, coldBinary) != 5 {
		t.Fatalf("independent cold runtime failed: %d %s", code, output)
	}
}

func defaultFastRuntimeStatus(t *testing.T, binary string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, binary).Run()
	if err == nil {
		return 0
	}
	if failure, ok := err.(*exec.ExitError); ok && ctx.Err() == nil {
		return failure.ExitCode()
	}
	t.Fatal(err)
	return -1
}

// Test-only native link seam: ordinary CLI tests above exercise parsing/cache
// selection; here the parsed production options select its existing native
// linker branch without adding a shipping CLI flag or changing semantic input.
func TestDefaultFastPreparationNativeChild(t *testing.T) {
	if os.Getenv("TEST_DEFAULT_FAST_NATIVE") != "1" {
		t.Skip("fresh native child only")
	}
	for i, arg := range os.Args {
		if arg == "--" {
			options, err := parseArgs(os.Args[i+1:])
			if err != nil {
				t.Fatal(err)
			}
			options.linkNative = true
			os.Exit(runWithOptions(options, os.Stdout, os.Stderr))
		}
	}
	t.Fatal("native child arguments missing")
}
