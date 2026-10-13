//go:build cgo

package backend

import (
	"os"
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

// The unroll budget is a process-global LLVM option; it must not change -O2 output.
func TestO3UnrollThresholdLeavesO2Unchanged(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "unroll_threshold_o2.elisa", unrollThresholdSrc)
	before, err := GenerateLLVMIRWithOpt(result, OptimizationLevel2)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt -O2: %v", err)
	}
	ensureO3UnrollThreshold(OptimizationLevel3)
	result = parseAndAnalyzeBackendTest(t, "unroll_threshold_o2.elisa", unrollThresholdSrc)
	after, err := GenerateLLVMIRWithOpt(result, OptimizationLevel2)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt -O2 after: %v", err)
	}
	if before != after {
		t.Fatalf("-O2 IR changed after installing the -O3 unroll budget")
	}
	if !loopBackEdge.MatchString(functionBody(t, after, "mix")) {
		t.Fatalf("-O2 must keep the loop rolled (default -O2 unroll budget):\n%s", functionBody(t, after, "mix"))
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
