package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// Per-iteration reclamation of loop-local allocations (runtime-perf 2026-10-13).
//
// A loop body whose fresh allocations are iteration-local is wrapped in a per-iteration lazy
// region (parser wrapReclaimableLoopBodies). Growing an OUTER container used to veto that wrap
// entirely, so `local = [..]; out.push(local[3])` kept every iteration's literal alive until the
// function returned (2M iterations peaked at 3.6 GB, each literal also reserved a 256-element
// floor). These tests pin: the scalar-push shape is reclaimed per iteration (bounded peak RSS),
// values that escape the iteration still survive (no use-after-free), and repeated
// string_view_copy stays linear (the retired write-only pointer table made it quadratic).

// buildAndRunReclaimTest builds `src` (with the std runtime included) as a test runner, runs the
// named test and returns its combined output and peak RSS in bytes.
func buildAndRunReclaimTest(t *testing.T, name, src, testName string) (string, int64) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	full := "include \"" + std + "\"\n" + src
	dir := t.TempDir()
	path := filepath.Join(dir, name+".elisa")
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("ELISA_KEEP_TEST_BINARY", "1")
	var stdout, stderr strings.Builder
	if code := runCLI([]string{"-emit", "test", "-O2", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("build failed (exit %d)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	exePath := ""
	for _, line := range strings.Split(stderr.String(), "\n") {
		if idx := strings.Index(line, "test binary: "); idx >= 0 {
			exePath = strings.TrimSpace(line[idx+len("test binary: "):])
			break
		}
	}
	if exePath == "" {
		t.Skipf("could not locate kept test binary:\n%s", stderr.String())
	}
	defer os.Remove(exePath)
	defer os.RemoveAll(exePath + ".dSYM")
	cmd := exec.Command(exePath, testName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run %q failed: %v\noutput:\n%s", testName, err, string(out))
	}
	var maxRSS int64
	if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok && ru != nil {
		maxRSS = int64(ru.Maxrss)
		if runtime.GOOS != "darwin" {
			maxRSS *= 1024 // Linux reports KiB, Darwin bytes
		}
	}
	return string(out), maxRSS
}

const loopReclaimPrelude = `
def total(xs: darray[usize]) -> usize:
    s: mutable usize = 0
    for x in xs |s|:
        s += x
    return s
`

// 200k iterations of a 16-element usize literal: ~26 MB retained if every iteration's literal
// survives to function exit (~500 MB with the old 256-element floor); a per-iteration region
// keeps the peak near the process baseline.
func TestLoopLiteralWithOuterScalarPushIsReclaimedPerIteration(t *testing.T) {
	src := loopReclaimPrelude + `
@test
def reclaim() -> void:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[usize] = []
        for i in 0..<200000 |out|:
            local: mutable darray[usize] = [i, i+1, i+2, i+3, i, i, i, i, i, i, i, i, i, i, i, i]
            out.push(local[3])
        if out.count != 200000 or out[0] != 3 or out[199999] != 200002:
            panic("outer container contents wrong after per-iteration reclamation")
`
	_, rss := buildAndRunReclaimTest(t, "loop_reclaim_scalar_push", src, "reclaim")
	// out itself grows to 200k*8 = 1.6 MB (plus doubling slack); allow generous baseline headroom.
	if rss > 16<<20 {
		t.Fatalf("peak RSS %d bytes: loop-local literal not reclaimed per iteration", rss)
	}
}

// Values that DO escape the iteration must survive it. Each shape below must not be tightened
// (or must keep the escaping value in the outer region); the checks read every survivor after the
// loop has churned through many later iterations' allocations.
func TestLoopEscapingArraysSurviveIteration(t *testing.T) {
	src := loopReclaimPrelude + `
@test
def escapes() -> void:
    can Memory.Allocate, Abort.Panic:
        rows: mutable darray[darray[usize]] = []
        keep: mutable darray[usize] = []
        firsts: mutable darray[usize] = []
        for i in 0..<5000 |rows, keep, firsts|:
            local: mutable darray[usize] = [i, i+1, i+2]
            firsts.push(local[0])
            rows.push(local)
            if i == 7:
                keep <- local
        for i in 0..<5000 |rows, firsts|:
            row: darray[usize] = rows[i]
            if row.count != 3 or row[0] != i or row[2] != i + 2 or firsts[i] != i:
                panic("escaped loop-local array was reclaimed with its iteration")
        if keep.count != 3 or keep[0] != 7 or keep[2] != 9:
            panic("loop-local array kept in an outer binding was reclaimed")

@test
def nested_elements() -> void:
    can Memory.Allocate, Abort.Panic:
        heads: mutable darray[darray[usize]] = []
        for i in 0..<3000 |heads|:
            grid: mutable darray[darray[usize]] = [[i, i+1], [i+2]]
            heads.push(grid[0])
        for i in 0..<3000 |heads|:
            if heads[i].count != 2 or heads[i][0] != i or heads[i][1] != i + 1:
                panic("element header copied out of a loop-local nested array was reclaimed")

@test
def call_arg_untouched() -> void:
    can Memory.Allocate, Abort.Panic:
        out: mutable usize = 0
        for i in 0..<1000 |out|:
            local: mutable darray[usize] = [i, 1]
            out += total(local)
        if out != 499500 + 1000:
            panic("call-argument loop produced the wrong sum")
`
	for _, name := range []string{"escapes", "nested_elements", "call_arg_untouched"} {
		out, _ := buildAndRunReclaimTest(t, "loop_reclaim_escapes_"+name, src, name)
		if strings.Contains(out, "panic") {
			t.Fatalf("%s: %s", name, out)
		}
	}
}

// string_view_copy used to register every copy in a program-wide pointer table whose 4096 bucket
// chains were walked on each insert (and whose entries were never freed): 2M copies took ~33 s.
// The table was write-only (runtime_strlen never consults it), so it is gone; this pins both
// correctness of the copies and that a large copy count completes quickly.
func TestStringViewCopyManyCopiesStaysLinear(t *testing.T) {
	src := `
@test
def svcopies() -> void:
    can Memory.Allocate, Abort.Panic, Global{Read,Write}:
        v: sview = "a_fairly_long_identifier_name_here"
        total: mutable usize = 0
        for i in 0..<300000 |total|:
            c: cstr = string_view_copy(v)
            if c[0] != 'a'.u8() or c[33] != 'e'.u8() or c[34] != 0u8:
                panic("string_view_copy produced a wrong copy")
            total += 1
        if total != 300000:
            panic("copy loop count wrong")
`
	out, _ := buildAndRunReclaimTest(t, "svcopy_linear", src, "svcopies")
	if strings.Contains(out, "panic") {
		t.Fatalf("%s", out)
	}
}
