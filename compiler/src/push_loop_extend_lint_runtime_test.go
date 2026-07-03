package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pushLoopExtendWarning = "prefer the declarative fill"

// The push-loop-extend lint (docs/70, extend-comprehension-fusion): a clean map-and-append loop
// `for x in src: dst.push(f(x))` is nudged toward `dst.extend([f(x) for x in src])`, which fuses
// into a single presize-and-fill instead of per-iteration growth.
func TestRunCLIPushLoopExtendLintFlagsPlainMapLoop(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "push_map_loop.elisa", `def build(src: darray[i64]) -> usize:
    can Memory.Allocate, Abort.Panic:
        dst: mutable darray[i64] = []
        for x in src:
            dst.push(x * 2)
        return dst.count
`)
	if !strings.Contains(out, pushLoopExtendWarning) {
		t.Fatalf("expected a push-loop-extend nudge, got:\n%s", out)
	}
	if !strings.Contains(out, "dst.extend([(x * 2) for x in src])") {
		t.Fatalf("expected the suggestion to spell the fused extend, got:\n%s", out)
	}
}

// A single filter guard maps onto the filtered comprehension `[f(x) for x in src if cond]`.
func TestRunCLIPushLoopExtendLintFlagsFilteredMapLoop(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "push_filtered_loop.elisa", `def build(src: darray[i64]) -> usize:
    can Memory.Allocate, Abort.Panic:
        dst: mutable darray[i64] = []
        for x in src:
            if x > 2:
                dst.push(x)
        return dst.count
`)
	if !strings.Contains(out, pushLoopExtendWarning) {
		t.Fatalf("expected a filtered push-loop-extend nudge, got:\n%s", out)
	}
	if !strings.Contains(out, "for x in src if (x > 2)") {
		t.Fatalf("expected the suggestion to carry the filter, got:\n%s", out)
	}
}

// A self-extend loop (`for x in xs: xs.push(...)`) is NOT nudged: the source is the destination, a
// subtler shape the fusion handles by snapshotting but the plain loop should not be rewritten to.
func TestRunCLIPushLoopExtendLintSkipsSelfExtend(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "push_self.elisa", `def build() -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = [1, 2, 3]
        for x in xs:
            xs.push(x * 10)
        return xs.count
`)
	if strings.Contains(out, pushLoopExtendWarning) {
		t.Fatalf("self-extend push loop must not be nudged, got:\n%s", out)
	}
}

// A constant/side-channel fill (the pushed value does not use the loop variable) is NOT a map, so
// it is not nudged — the comprehension rewrite would be misleading.
func TestRunCLIPushLoopExtendLintSkipsNonMap(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "push_const.elisa", `def build(src: darray[i64]) -> usize:
    can Memory.Allocate, Abort.Panic:
        dst: mutable darray[i64] = []
        for x in src:
            dst.push(0)
        return dst.count
`)
	if strings.Contains(out, pushLoopExtendWarning) {
		t.Fatalf("constant fill must not be nudged, got:\n%s", out)
	}
}

// A loop whose body does more than a single push (multiple statements, side effects) is not the
// clean map/filter shape and is left alone.
func TestRunCLIPushLoopExtendLintSkipsMultiStatementBody(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "push_multi.elisa", `def build(src: darray[i64]) -> usize:
    can Memory.Allocate, Abort.Panic:
        dst: mutable darray[i64] = []
        total: mutable i64 = 0
        for x in src:
            total = total + x
            dst.push(x + total)
        return dst.count
`)
	if strings.Contains(out, pushLoopExtendWarning) {
		t.Fatalf("multi-statement loop body must not be nudged, got:\n%s", out)
	}
}

// Under -Wperf the nudge becomes a hard compile error.
func TestRunCLIPushLoopExtendLintIsErrorUnderWperf(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "push_map_werror.elisa")
	prog := `def build(src: darray[i64]) -> usize:
    can Memory.Allocate, Abort.Panic:
        dst: mutable darray[i64] = []
        for x in src:
            dst.push(x * 2)
        return dst.count
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{"-emit", "llvm", "-Wperf", src}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected -Wperf to reject the push-loop map, but compilation succeeded:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), pushLoopExtendWarning) {
		t.Fatalf("expected the push-loop-extend diagnostic under -Wperf, got:\n%s", stderr.String())
	}
}
