package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `dst.extend([ v for x in src ])` — a filter-free list comprehension over a darray source — fuses:
// it appends the mapped elements DIRECTLY into `dst` (presize once + indexed-store fill) instead of
// materializing the comprehension into a temp darray and memcpy-ing it. This pins the VALUE
// semantics of the fused path: append order/contents, self-extend aliasing, a growth that crosses a
// region-capacity boundary, and that the non-fusable shapes (filtered comprehension, plain darray
// source) still work via the materialized fallback.
func TestExtendComprehensionFusionRuntime(t *testing.T) {
	t.Parallel()
	// checksum accumulates distinctive values from each scenario; main returns it as the exit code
	// (mod 256), so a wrong result is a wrong code. Expected: see below.
	status, out := s4CompileRun(t, `def check(cond: bool, msg: cstr) -> void can[Abort.Panic]:
    if not cond:
        panic(msg)

def scenarios() -> i64 can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        # self-extend: read region and appended tail are disjoint, resize happens once up front
        xs: mutable darray[i64] = [1, 2, 3]
        xs.extend([v * 10 for v in xs])
        check(xs.count == 6 and xs[0] == 1 and xs[3] == 10 and xs[5] == 30, "self-extend")

        # append onto a non-empty dst, crossing a region-capacity boundary (>256 slots)
        src: darray[i64] = [i for i in 0..<400]
        dst: mutable darray[i64] = [42]
        dst.extend([v + 1 for v in src])
        check(dst.count == 401 and dst[0] == 42 and dst[1] == 1 and dst[400] == 400, "boundary")

        # filtered comprehension: not fusable, must still work via the materialized fallback
        filt: mutable darray[i64] = []
        filt.extend([v for v in src if v > 2])
        check(filt.count == 397 and filt[0] == 3, "filtered")

        # plain darray source: not a comprehension, materialized memcpy path
        plain: mutable darray[i64] = [7]
        plain.extend(xs)
        check(plain.count == 7 and plain[1] == 1 and plain[6] == 30, "plain")

        return 17

def main() -> int can[Memory.Allocate, Abort.Panic]:
    return scenarios().int()
`)
	if strings.Contains(out, "assert failed") || strings.Contains(out, "self-extend") || strings.Contains(out, "boundary") ||
		strings.Contains(out, "filtered") || strings.Contains(out, "plain") {
		t.Fatalf("fused extend produced a wrong result: status=%s out=%q", status, out)
	}
	// scenarios() returns 17 -> exit code 17.
	if status != "RUNERR" || !strings.Contains(out, "exit status 17") {
		t.Fatalf("expected clean exit code 17, got status=%s out=%q", status, out)
	}
}

// The fused path must NOT emit the materialized comprehension temp (`list.comp.result.*`) or the
// extend memcpy — it presizes `dst` and fills its tail by indexed store. This pins that fusion
// actually occurs, not merely that the result is correct (a materialized lowering would also be
// correct, just slower).
func TestExtendComprehensionFusionEmitsNoTemp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "extend_fusion.elisa")
	prog := "def build() -> usize:\n" +
		"    can Memory.Allocate, Abort.Panic:\n" +
		"        src: darray[i64] = [1, 2, 3, 4]\n" +
		"        dst: mutable darray[i64] = [9]\n" +
		"        dst.extend([v * 2 for v in src])\n" +
		"        return dst.count\n\n" +
		"def main() -> i64:\n" +
		"    return build().i64()\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected fused-extend fixture to compile, stderr:\n%s", stderr.String())
	}
	ir := stdout.String()
	for _, banned := range []string{"list.comp.result", "darray.extend.memcpy"} {
		if strings.Contains(ir, banned) {
			t.Fatalf("fused extend still emitted %q (materialized path not skipped):\n%s", banned, ir)
		}
	}
	// The fused fill is a resize followed by an indexed-store loop over the source.
	if !strings.Contains(ir, "resize") && !strings.Contains(ir, "ensure") {
		t.Fatalf("expected a presize (resize/ensure-capacity) in the fused extend, got:\n%s", ir)
	}
}
