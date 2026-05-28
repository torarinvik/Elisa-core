//go:build cgo

package backend

import (
	"strings"
	"testing"
)

const watchdogTrustedIndexSrc = `def at(xs: darray[i32]&, i: usize) -> i32:
    trusted Unsafe.UncheckedIndex:
        return xs[i]
`

// In debug (-O0) a trusted/unchecked user index is verified by the watchdog: a
// runtime bounds compare plus llvm.trap on violation. "Debug verifies what
// release assumes."
func TestIndexWatchdogTrapsInDebug(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "watchdog_trusted_debug.elisa", watchdogTrustedIndexSrc)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "wd.in_bounds") {
		t.Fatalf("expected debug watchdog bounds check on trusted index, got:\n%s", output)
	}
	if !strings.Contains(output, "@llvm.trap") {
		t.Fatalf("expected debug watchdog to emit llvm.trap on violation, got:\n%s", output)
	}
}

// In release the trusted/unchecked index is exactly as before — zero watchdog
// overhead.
func TestIndexWatchdogAbsentInRelease(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "watchdog_trusted_release.elisa", watchdogTrustedIndexSrc)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel2)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "wd.in_bounds") || strings.Contains(output, "wd.fail") {
		t.Fatalf("expected no watchdog instrumentation in release, got:\n%s", output)
	}
}

// A checked `get arr[i] else ...` performs its own semantic bounds check; the
// debug watchdog must not also instrument it (no duplicate check).
func TestCheckedGetIndexHasNoWatchdogDuplicate(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "watchdog_get_no_dup.elisa", `def at(xs: darray[i32]&, i: usize) -> i32:
    return get xs[i] else -1
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "wd.in_bounds") {
		t.Fatalf("checked get index must not get a duplicate watchdog bounds check, got:\n%s", output)
	}
}
