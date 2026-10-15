//go:build cgo

package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A constant-trip loop whose body carries bounds-checked darray accesses: over LLVM's default -O3
// full-unroll budget (300), under optimizationUnrollThresholdO3 (600).
const unrollThresholdSrc = `@inline(never)
def mix(a: mutable darray[f64]&, b: darray[f64]&) -> f64 can[Abort.Panic]:
    acc: mutable f64 = 0.0
    for i in 0..<8:
        a[i] <- a[i] * b[i] + b[(i + 1) % 8] - b[(i + 3) % 8]
        acc <- acc + a[i] / (b[i] + 1.0) + a[(i + 2) % 8] * b[(i + 5) % 8]
        a[(i + 4) % 8] <- acc - a[(i + 6) % 8]
        b2: f64 = a[(i + 7) % 8] * b[(i + 2) % 8] - a[(i + 1) % 8] / (b[(i + 4) % 8] + 2.0)
        a[(i + 5) % 8] <- b2 + a[(i + 3) % 8] * b[(i + 6) % 8]
        acc <- acc * 0.5 + b2 - a[(i + 5) % 8]
    return acc

def main() -> int can[Memory.Allocate, Abort.Panic]:
    a: mutable darray[f64] = [1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0]
    b: darray[f64] = [1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0]
    if mix(&a, &b) > 100.0:
        return 1
    return 0
`

// The rolled loop keeps its induction variable as `phi i64 [ 0, %entry ], ...`.
var loopBackEdge = regexp.MustCompile(`= phi i64 \[ 0, %entry \]`)

const unrollSubprocessThresholdEnv = "ELISAC_TEST_UNROLL_THRESHOLD"
const unrollSubprocessOutputEnv = "ELISAC_TEST_UNROLL_IR"
const unrollSubprocessModeEnv = "ELISAC_TEST_UNROLL_MODE"

// Run each option setting in a fresh process because LLVM's unroll threshold is process-global
// and cannot be reset after the first O3 pipeline.
func TestO3UnrollThresholdDiffersFromLLVMDefault(t *testing.T) {
	run := func(mode string, threshold int) string {
		t.Helper()
		irPath := filepath.Join(t.TempDir(), "module.ll")
		cmd := exec.Command(os.Args[0], "-test.run=^TestO3UnrollThresholdSubprocessHelper$")
		cmd.Env = append(os.Environ(),
			unrollSubprocessModeEnv+"="+mode,
			unrollSubprocessThresholdEnv+"="+strconv.Itoa(threshold),
			unrollSubprocessOutputEnv+"="+irPath,
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s subprocess with unroll threshold %d failed: %v\n%s", mode, threshold, err, output)
		}
		ir, err := os.ReadFile(irPath)
		if err != nil {
			t.Fatalf("read %s subprocess IR at threshold %d: %v", mode, threshold, err)
		}
		return string(ir)
	}

	// Zero is a test-only sentinel: ensureO3UnrollThreshold returns without parsing any option,
	// leaving LLVM's compiled-in aggressive unroll threshold untouched.
	defaultIR := run("o3", 0)
	if !loopBackEdge.MatchString(functionBody(t, defaultIR, "mix")) {
		t.Fatal("LLVM's compiled-in O3 unroll budget should leave the mix loop rolled")
	}
	raisedIR := run("o3", optimizationUnrollThresholdO3)
	if loopBackEdge.MatchString(functionBody(t, raisedIR, "mix")) {
		t.Fatal("production O3 unroll budget should fully unroll the 8-trip mix loop")
	}

	defaultO2 := run("o2", 0)
	raisedO2 := run("o2", optimizationUnrollThresholdO3)
	if defaultO2 != raisedO2 {
		t.Fatal("O2 IR differs between default process state and a process with the O3 threshold initialized")
	}
	if !loopBackEdge.MatchString(functionBody(t, raisedO2, "mix")) {
		t.Fatal("O2 must keep the mix loop rolled after the O3 unroll threshold is initialized")
	}
}

// Test helper invoked only by TestO3UnrollThresholdDiffersFromLLVMDefault's clean subprocesses.
func TestO3UnrollThresholdSubprocessHelper(t *testing.T) {
	thresholdText := os.Getenv(unrollSubprocessThresholdEnv)
	outputPath := os.Getenv(unrollSubprocessOutputEnv)
	if thresholdText == "" || outputPath == "" {
		t.Skip("subprocess helper")
	}
	threshold, err := strconv.Atoi(thresholdText)
	if err != nil || threshold < 0 {
		t.Fatalf("invalid subprocess unroll threshold %q", thresholdText)
	}
	optimizationUnrollThresholdO3 = threshold
	result := parseAndAnalyzeBackendTest(t, "unroll_threshold_subprocess.elisa", unrollThresholdSrc)
	optLevel := OptimizationLevel3
	if os.Getenv(unrollSubprocessModeEnv) == "o2" {
		// Initialize the process-global O3 option before generating O2 IR.
		ensureO3UnrollThreshold(OptimizationLevel3)
		optLevel = OptimizationLevel2
	}
	ir, err := GenerateLLVMIRWithOpt(result, optLevel)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt -O%d at threshold %d: %v", optLevel, threshold, err)
	}
	if err := os.WriteFile(outputPath, []byte(ir), 0o600); err != nil {
		t.Fatalf("write subprocess IR: %v", err)
	}
}

func TestO3UnrollThresholdFullyUnrollsSmallConstantTripLoop(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "unroll_threshold.elisa", unrollThresholdSrc)
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel3)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt -O3: %v", err)
	}
	if mix := functionBody(t, ir, "mix"); loopBackEdge.MatchString(mix) {
		t.Fatalf("-O3 must fully unroll the 8-trip loop under the raised unroll budget:\n%s", mix)
	}
}

func TestO3UnrollThresholdInBothPaths(t *testing.T) {
	src, err := os.ReadFile("parallel_emit.c")
	if err != nil {
		t.Fatalf("read parallel_emit.c: %v", err)
	}
	want := "elisacoreEnsureO3UnrollThreshold(" + strconv.Itoa(optimizationUnrollThresholdO3) + ")"
	if !strings.Contains(string(src), want) {
		t.Fatalf("parallel_emit.c part_optimize must install the same -O3 unroll budget (%s)", want)
	}
}
