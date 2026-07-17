package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `dst.extend([ v for x in src ])` fuses: the comprehension is appended DIRECTLY into `dst` with no
// intermediate darray / memcpy. Two fused shapes: filter-free (presize once + indexed-store fill,
// vectorizable) and filtered (reserve the upper bound + conditional push). This pins the VALUE
// semantics of both: append order/contents, self-extend aliasing, a growth that crosses a region-
// capacity boundary, the filtered subset, and that a plain darray source still uses materialized memcpy.
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

        # filtered comprehension: fused via reserve-upper-bound + conditional push
        filt: mutable darray[i64] = []
        filt.extend([v for v in src if v > 2])
        check(filt.count == 397 and filt[0] == 3, "filtered")

        # self-FILTERED-extend: source IS dst — the snapshotted bound must reproduce the
        # evaluate-comprehension-then-append semantics (not re-consume appended elements)
        sf: mutable darray[i64] = [1, 2, 3, 4, 5]
        sf.extend([v * 100 for v in sf if v > 2])
        check(sf.count == 8 and sf[4] == 5 and sf[5] == 300 and sf[7] == 500, "self-filtered")

        # plain darray source: not a comprehension, materialized memcpy path
        plain: mutable darray[i64] = [7]
        plain.extend(xs)
        check(plain.count == 7 and plain[1] == 1 and plain[6] == 30, "plain")

        return 17

def main() -> int can[Memory.Allocate, Abort.Panic]:
    return scenarios().int()
`)
	if strings.Contains(out, "assert failed") || strings.Contains(out, "self-extend") || strings.Contains(out, "boundary") ||
		strings.Contains(out, "filtered") || strings.Contains(out, "plain") || strings.Contains(out, "self-filtered") {
		t.Fatalf("fused extend produced a wrong result: status=%s out=%q", status, out)
	}
	// scenarios() returns 17 -> exit code 17.
	if status != "RUNERR" || !strings.Contains(out, "exit status 17") {
		t.Fatalf("expected clean exit code 17, got status=%s out=%q", status, out)
	}
}

// `dst.extend([b for b in sv])` fuses over an SVIEW source, exactly like a darray source: an sview
// is indexable (`sv[i]` -> u8) and count-bearing (`sv.len`, an i64), so it lowers to the same
// presize + indexed-store fill. (Motivated by dogfooding the frontend, whose byte-buffer builders
// iterate string views.) Pins the appended bytes and that appending onto a non-empty dst is correct.
func TestExtendSviewComprehensionFusionRuntime(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `def check(cond: bool, msg: cstr) -> void can[Abort.Panic]:
    if not cond:
        panic(msg)

def scenarios() -> int can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        sv: sview = sview("hello world", 0, 5)
        buf: mutable darray[u8] = [42]
        buf.extend([b for b in sv])
        # "hello" = 104,101,108,108,111
        check(buf.count == 6 and buf[0] == 42 and buf[1] == 104 and buf[5] == 111, "sview")
        return 44

def main() -> int can[Memory.Allocate, Abort.Panic]:
    return scenarios()
`)
	if strings.Contains(out, "assert failed") || strings.Contains(out, "sview") {
		t.Fatalf("fused sview extend produced a wrong result: status=%s out=%q", status, out)
	}
	if status != "RUNERR" || !strings.Contains(out, "exit status 44") {
		t.Fatalf("expected clean exit code 44, got status=%s out=%q", status, out)
	}
}

// The sview fused path presizes + indexed-stores — no materialized comprehension temp / memcpy
// (checked at the default opt level, where the presize is not folded away). The sview comes in as a
// param so the fixture needs no runtime include (whose own extend calls would pollute the
// whole-module substring check); sview is a builtin type, so `build` compiles standalone.
func TestExtendSviewComprehensionFusionEmitsNoTemp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "extend_sview_fusion.elisa")
	prog := "def build(sv: sview) -> usize:\n" +
		"    can Memory.Allocate, Abort.Panic:\n" +
		"        buf: mutable darray[u8] = [9]\n" +
		"        buf.extend([b for b in sv])\n" +
		"        return buf.count\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected fused sview-extend fixture to compile, stderr:\n%s", stderr.String())
	}
	ir := stdout.String()
	for _, banned := range []string{"list.comp.result", "darray.extend.memcpy"} {
		if strings.Contains(ir, banned) {
			t.Fatalf("fused sview extend still emitted %q (materialized path not skipped):\n%s", banned, ir)
		}
	}
	if !strings.Contains(ir, "resize") && !strings.Contains(ir, "ensure") {
		t.Fatalf("expected a presize (resize/ensure-capacity) in the fused sview extend, got:\n%s", ir)
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

// `dst.extend([ value for name in start..<end ])` fuses over a RANGE source: presize dst by the
// clamped span and fill its tail by indexed store at the shifted offset `__base + (name - start)`.
// This pins the value semantics: append onto a non-empty dst, crossing a region-capacity boundary,
// an empty (start >= end) range appending nothing, and a mapped value.
func TestExtendRangeComprehensionFusionRuntime(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `def check(cond: bool, msg: cstr) -> void can[Abort.Panic]:
    if not cond:
        panic(msg)

def scenarios() -> int can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        # append a mapped range onto a non-empty dst, crossing a region-capacity boundary (>256).
        # Range/loop-var element type is int, so dst must be darray[int] (extend requires the
        # source element type to match; there is no expected-type propagation into an extend arg).
        dst: mutable darray[int] = [42]
        dst.extend([i * 2 for i in 0..<400])
        check(dst.count == 401 and dst[0] == 42 and dst[1] == 0 and dst[400] == 798, "range")

        # empty range appends nothing (clamped count is 0)
        empty: mutable darray[int] = [7, 8]
        empty.extend([i for i in 5..<5])
        check(empty.count == 2 and empty[1] == 8, "empty-range")

        # non-zero start: offset shift is correct
        shifted: mutable darray[int] = [100]
        shifted.extend([i for i in 3..<6])
        check(shifted.count == 4 and shifted[1] == 3 and shifted[3] == 5, "shifted")

        return 23

def main() -> int can[Memory.Allocate, Abort.Panic]:
    return scenarios()
`)
	if strings.Contains(out, "assert failed") || strings.Contains(out, "range") || strings.Contains(out, "empty-range") || strings.Contains(out, "shifted") {
		t.Fatalf("fused range extend produced a wrong result: status=%s out=%q", status, out)
	}
	if status != "RUNERR" || !strings.Contains(out, "exit status 23") {
		t.Fatalf("expected clean exit code 23, got status=%s out=%q", status, out)
	}
}

// The RANGE fused path presizes + indexed-stores — no materialized comprehension temp / memcpy.
func TestExtendRangeComprehensionFusionEmitsNoTemp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "extend_range_fusion.elisa")
	prog := "def build() -> usize:\n" +
		"    can Memory.Allocate, Abort.Panic:\n" +
		"        dst: mutable darray[int] = [9]\n" +
		"        dst.extend([i * 3 for i in 0..<8])\n" +
		"        return dst.count\n\n" +
		"def main() -> i64:\n" +
		"    return build().i64()\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected fused range-extend fixture to compile, stderr:\n%s", stderr.String())
	}
	ir := stdout.String()
	for _, banned := range []string{"list.comp.result", "darray.extend.memcpy"} {
		if strings.Contains(ir, banned) {
			t.Fatalf("fused range extend still emitted %q (materialized path not skipped):\n%s", banned, ir)
		}
	}
	if !strings.Contains(ir, "resize") && !strings.Contains(ir, "ensure") {
		t.Fatalf("expected a presize (resize/ensure-capacity) in the fused range extend, got:\n%s", ir)
	}
}

// `dst.extend([ value for k, v in src ])` fuses over a DICT source: a dict's count is exact and the
// head is filter-free, so the fused path presizes dst once and fills the appended tail through a
// running counter (dicts have no integer element indexing). This became possible once the dict
// iteration overcount bug was fixed (task_dcf0ced9; pinned by
// TestRunCLIDictIterationVisitsExactlyCountEntries) — an overcount plus a presized buffer would be
// an out-of-bounds store. Pins the value semantics: appended count/contents (order-insensitive sum),
// append onto a non-empty dst, both single- and two-binder heads, and that a FILTERED dict source
// still routes through the safe push fallback.
func TestExtendDictComprehensionFusionRuntime(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `def check(cond: bool, msg: cstr) -> void can[Abort.Panic]:
    if not cond:
        panic(msg)

def scenarios() -> i64 can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        d: mutable dict[i64, i64] = {}
        d.put(1, 10)
        d.put(2, 20)
        d.put(3, 30)

        # two-binder head onto a non-empty dst
        dst: mutable darray[i64] = [7]
        dst.extend([k * 100 + v for k, v in d])
        sum: mutable i64 = 0
        for x in dst:
            sum <- sum + x
        check(dst.count == 4 and dst[0] == 7 and sum == 667, "dict-two-binder")

        # single-binder head binds the entry tuple
        tups: mutable darray[i64] = []
        tups.extend([e.key + e.value for e in d])
        tsum: mutable i64 = 0
        for x in tups:
            tsum <- tsum + x
        check(tups.count == 3 and tsum == 66, "dict-single-binder")

        # a larger dict crossing a region-capacity boundary (>256 entries)
        big: mutable dict[i64, i64] = {}
        for i in 0..<400:
            big.put(i, i * 2)
        wide: mutable darray[i64] = [1]
        wide.extend([v for _, v in big])
        wsum: mutable i64 = 0
        for x in wide:
            wsum <- wsum + x
        check(wide.count == 401 and wsum == 1 + 399 * 400, "dict-boundary")

        # filtered dict source: falls to the push fallback, still correct
        filt: mutable darray[i64] = []
        filt.extend([v for k, v in d if k > 1])
        fsum: mutable i64 = 0
        for x in filt:
            fsum <- fsum + x
        check(filt.count == 2 and fsum == 50, "dict-filtered")

        return 37

def main() -> int can[Memory.Allocate, Abort.Panic]:
    return scenarios().int()
`)
	if strings.Contains(out, "dict-two-binder") || strings.Contains(out, "dict-single-binder") ||
		strings.Contains(out, "dict-boundary") || strings.Contains(out, "dict-filtered") {
		t.Fatalf("fused dict extend produced a wrong result: status=%s out=%q", status, out)
	}
	if status != "RUNERR" || !strings.Contains(out, "exit status 37") {
		t.Fatalf("expected clean exit code 37, got status=%s out=%q", status, out)
	}
}

// The dict fused path presizes + counter-stores — no materialized comprehension temp / memcpy.
func TestExtendDictComprehensionFusionEmitsNoTemp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "extend_dict_fusion.elisa")
	prog := "def build(d: dict[i64, i64]) -> usize:\n" +
		"    can Memory.Allocate, Abort.Panic:\n" +
		"        dst: mutable darray[i64] = [9]\n" +
		"        dst.extend([k + v for k, v in d])\n" +
		"        return dst.count\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected fused dict-extend fixture to compile, stderr:\n%s", stderr.String())
	}
	ir := stdout.String()
	for _, banned := range []string{"list.comp.result", "darray.extend.memcpy"} {
		if strings.Contains(ir, banned) {
			t.Fatalf("fused dict extend still emitted %q (materialized path not skipped):\n%s", banned, ir)
		}
	}
	if !strings.Contains(ir, "resize") && !strings.Contains(ir, "ensure") {
		t.Fatalf("expected a presize (resize/ensure-capacity) in the fused dict extend, got:\n%s", ir)
	}
	// The presized dict path's synthetic locals ("extend.dict.*") distinguish it from the older
	// push fallback (which also avoids the temp, but never presizes).
	if !strings.Contains(ir, "extend.dict.") {
		t.Fatalf("expected the presized dict-extend lowering (extend.dict.* locals), got:\n%s", ir)
	}
}

// A FILTERED range (`dst.extend([ v for i in start..<end if cond ])`) is not caught by the
// filter-free range fast path; it falls to the generic push-direct fusion — no temp/memcpy, just
// pushing straight into dst instead of a discarded intermediate darray.
func TestExtendFilteredRangeComprehensionFusionRuntime(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `def check(cond: bool, msg: cstr) -> void can[Abort.Panic]:
    if not cond:
        panic(msg)

def scenarios() -> int can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        dst: mutable darray[int] = [42]
        dst.extend([i for i in 0..<20 if i % 3 == 0])
        check(dst.count == 8 and dst[0] == 42 and dst[1] == 0 and dst[7] == 18, "filtered-range")

        # inclusive range, unfiltered — also routes through the generic push fallback
        inc: mutable darray[int] = []
        inc.extend([i for i in 1..=3])
        check(inc.count == 3 and inc[0] == 1 and inc[2] == 3, "inclusive-range")

        # stepped range, unfiltered
        stepped: mutable darray[int] = []
        stepped.extend([i for i in 0..<10..3])
        check(stepped.count == 4 and stepped[3] == 9, "stepped-range")

        return 31

def main() -> int can[Memory.Allocate, Abort.Panic]:
    return scenarios()
`)
	if strings.Contains(out, "assert failed") || strings.Contains(out, "filtered-range") || strings.Contains(out, "inclusive-range") || strings.Contains(out, "stepped-range") {
		t.Fatalf("fused filtered/stepped/inclusive range extend produced a wrong result: status=%s out=%q", status, out)
	}
	if status != "RUNERR" || !strings.Contains(out, "exit status 31") {
		t.Fatalf("expected clean exit code 31, got status=%s out=%q", status, out)
	}
}

// The generic push fallback for a filtered range skips the materialized temp/memcpy — dst.push is
// called directly from the loop body.
func TestExtendFilteredRangeComprehensionFusionEmitsNoTemp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "extend_filtered_range_fusion.elisa")
	prog := "def build() -> usize:\n" +
		"    can Memory.Allocate, Abort.Panic:\n" +
		"        dst: mutable darray[int] = [9]\n" +
		"        dst.extend([i for i in 0..<20 if i % 2 == 0])\n" +
		"        return dst.count\n\n" +
		"def main() -> i64:\n" +
		"    return build().i64()\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected fused filtered-range-extend fixture to compile, stderr:\n%s", stderr.String())
	}
	ir := stdout.String()
	for _, banned := range []string{"list.comp.result", "darray.extend.memcpy"} {
		if strings.Contains(ir, banned) {
			t.Fatalf("fused filtered range extend still emitted %q (materialized path not skipped):\n%s", banned, ir)
		}
	}
}

// The expected-type propagation fix: extend's argument comprehension now gets dst's element type as
// its expected type, so `darray[i64].extend([i for i in range])` (loop var default `int`) typechecks
// instead of being rejected as darray[int] vs darray[i64].
func TestExtendComprehensionElementTypeCoercion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "extend_elem_coerce.elisa")
	prog := "def build() -> usize:\n" +
		"    can Memory.Allocate, Abort.Panic:\n" +
		"        dst: mutable darray[i64] = [1]\n" +
		"        dst.extend([i for i in 0..<5])\n" +
		"        return dst.count\n\n" +
		"def main() -> i64:\n" +
		"    return build().i64()\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected darray[i64].extend([range comprehension]) to typecheck via propagated expected type, stderr:\n%s", stderr.String())
	}
}

// The FILTERED fused path (`dst.extend([ v for x in src if cond ])`) reserves the upper bound and
// conditionally pushes — no materialized temp / memcpy either.
func TestExtendFilteredComprehensionFusionEmitsNoTemp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "extend_filtered_fusion.elisa")
	prog := "def build() -> usize:\n" +
		"    can Memory.Allocate, Abort.Panic:\n" +
		"        src: darray[i64] = [1, 2, 3, 4, 5, 6]\n" +
		"        dst: mutable darray[i64] = [0]\n" +
		"        dst.extend([v * 2 for v in src if v > 3])\n" +
		"        return dst.count\n\n" +
		"def main() -> i64:\n" +
		"    return build().i64()\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", src}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected filtered fused-extend fixture to compile, stderr:\n%s", stderr.String())
	}
	ir := stdout.String()
	for _, banned := range []string{"list.comp.result", "darray.extend.memcpy"} {
		if strings.Contains(ir, banned) {
			t.Fatalf("filtered fused extend still emitted %q (materialized path not skipped):\n%s", banned, ir)
		}
	}
	if !strings.Contains(ir, "reserve") {
		t.Fatalf("expected an upper-bound reserve in the filtered fused extend, got:\n%s", ir)
	}
}
