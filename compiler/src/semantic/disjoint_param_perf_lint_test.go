//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// A @hot kernel whose container params are NOT provably disjoint (no call site, so nothing is
// proven) must draw the perf hint: a warning by default, a hard error under -Wperf.
func TestHotKernelUnprovenDisjointWarnsThenPerfStrictErrors(t *testing.T) {
	src := `@hot
def axpy(y: mutable darray[f64]&, x: mutable darray[f64]&) -> void:
    for i in 0..<y.count:
        y[i] <- y[i] + x[i]
`
	warn := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "hot_disjoint_warn.elisa", src, AnalyzeOptions{})
	warnAll := allDiagnostics(warn)
	if !strings.Contains(warnAll, "will not vectorize") || !strings.Contains(warnAll, "y/x") {
		t.Fatalf("expected unproven-disjoint perf warning naming y/x, got:\n%s", warnAll)
	}
	if len(warn.Errors()) != 0 {
		t.Fatalf("default perf friction should be a warning, got errors:\n%v", warn.Errors())
	}

	strict := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "hot_disjoint_error.elisa", src, AnalyzeOptions{EnforcePerfLints: true})
	if !strings.Contains(allDiagnostics(strict), "will not vectorize") {
		t.Fatalf("expected unproven-disjoint perf error under -Wperf, got:\n%s", allDiagnostics(strict))
	}
	if len(strict.Errors()) == 0 {
		t.Fatal("performance-strict unproven-disjoint friction should be an error")
	}
}

// When every call site passes distinct fresh-local buffers, the pair is proven distinct, the
// kernel vectorizes, and NO perf hint fires — friction lands only on the unprovable shape.
func TestHotKernelProvenDisjointNoHint(t *testing.T) {
	src := `@hot
def axpy(y: mutable darray[f64]&, x: mutable darray[f64]&) -> void:
    for i in 0..<y.count:
        y[i] <- y[i] + x[i]

def run() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    axpy(&a, &b)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "hot_disjoint_proven.elisa", src, AnalyzeOptions{EnforcePerfLints: true})
	all := allDiagnostics(result)
	if strings.Contains(all, "will not vectorize") {
		t.Fatalf("proven-distinct @hot kernel should draw NO perf hint, got:\n%s", all)
	}
	if len(result.Errors()) != 0 {
		t.Fatalf("proven-distinct @hot kernel should be clean, got errors:\n%v", result.Errors())
	}
}

// A single container-ref param has no sibling to be disjoint from, so it is not a candidate and
// draws no hint regardless of -Wperf.
func TestHotKernelSingleContainerParamNoHint(t *testing.T) {
	src := `@hot
def scale(y: mutable darray[f64]&, k: f64) -> void:
    for i in 0..<y.count:
        y[i] <- y[i] * k
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "hot_single_param.elisa", src, AnalyzeOptions{EnforcePerfLints: true})
	if strings.Contains(allDiagnostics(result), "will not vectorize") {
		t.Fatalf("single-container-param @hot kernel should draw no hint, got:\n%s", allDiagnostics(result))
	}
}

// A non-@hot kernel with the same unprovable shape draws no hint: the friction is opt-in via @hot
// to keep prototyping noise-free.
func TestNonHotKernelNoDisjointHint(t *testing.T) {
	src := `def axpy(y: mutable darray[f64]&, x: mutable darray[f64]&) -> void:
    for i in 0..<y.count:
        y[i] <- y[i] + x[i]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "non_hot_disjoint.elisa", src, AnalyzeOptions{EnforcePerfLints: true})
	if strings.Contains(allDiagnostics(result), "will not vectorize") {
		t.Fatalf("non-@hot kernel should draw no disjoint perf hint, got:\n%s", allDiagnostics(result))
	}
}
