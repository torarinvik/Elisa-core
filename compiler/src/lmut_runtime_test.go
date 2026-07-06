package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// lmut (linear-mutable) Phase 1: `lmut T` is a parameter mode that desugars to a
// mutable reference — layout and codegen identical to `mutable T&`, in-place mutation
// through an exclusive pointer. These tests pin that the mode parses, compiles, runs
// natively, threads mutations in place, AND is behaviorally identical to the explicit
// `mutable T&` spelling. (Phase 2 adds the linear checker; this only covers the
// runtime semantics of the passing mode.)

func runLmutProgram(t *testing.T, name, body string) (int, string, string) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	path := filepath.Join(t.TempDir(), name+".elisa")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	exit := runCLI([]string{"-emit", "test", path}, &stdout, &stderr)
	return exit, stdout.String(), stderr.String()
}

const lmutThreadingBody = `
struct Counter:
    value: mutable i64

def bump(c: lmut Counter, by: i64) -> i64:
    c.value <- c.value + by
    return c.value

@test
def lmut_threads_in_place() -> void:
    c: mutable Counter = Counter{value: 10}
    r1: i64 = bump(c, 5)
    r2: i64 = bump(c, 100)
    if r1 != 15:
        panic("r1 wrong")
    if r2 != 115:
        panic("r2 wrong")
    if c.value != 115:
        panic("lmut did not thread the mutation in place")
`

// The same program with `mutable Counter&` instead of `lmut Counter` — must produce
// identical runtime behavior, since lmut is codegen-identical to a mutable ref.
const mutRefThreadingBody = `
struct Counter:
    value: mutable i64

def bump(c: mutable Counter&, by: i64) -> i64:
    c.value <- c.value + by
    return c.value

@test
def mutref_threads_in_place() -> void:
    c: mutable Counter = Counter{value: 10}
    r1: i64 = bump(&c, 5)
    r2: i64 = bump(&c, 100)
    if r1 != 15:
        panic("r1 wrong")
    if r2 != 115:
        panic("r2 wrong")
    if c.value != 115:
        panic("mutable& did not thread the mutation in place")
`

// lmut and `rebind` are two spellings of the SAME invalidate-and-reacquire model: `rebind`
// threads state through a return (the explicit manifest, for coarse boundaries) while `lmut`
// threads it in place through a parameter (silent, for hot paths). They compose — a coarse-
// boundary `rebind` line may call a function that threads one of its arguments via lmut, and both
// the returned manifest binding and the in-place lmut thread take effect. This pins that story so
// the "explicit at coarse boundaries, silent on hot paths" design stays intact.
const lmutRebindComposeBody = `
struct Lexer:
    pos: mutable i64

def advance(lx: lmut Lexer) -> i64:
    lx.pos <- lx.pos + 1
    return lx.pos

@test
def lmut_rebind_compose() -> void:
    lx: mutable Lexer = Lexer{pos: 0}
    rebind advanced: i64 = advance(lx)
    if advanced != 1:
        panic("rebind manifest wrong")
    if lx.pos != 1:
        panic("lmut did not thread through the rebind boundary")
`

func TestLmutRebindCompose(t *testing.T) {
	exit, stdout, stderr := runLmutProgram(t, "lmut_rebind", lmutRebindComposeBody)
	if exit != 0 || !strings.Contains(stdout, "failed=0") || !strings.Contains(stdout, "[       OK ] lmut_rebind_compose") {
		t.Fatalf("lmut+rebind composition did not pass cleanly: exit=%d\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
}

func TestLmutParamThreadsInPlace(t *testing.T) {
	exit, stdout, stderr := runLmutProgram(t, "lmut_threads", lmutThreadingBody)
	if exit != 0 || !strings.Contains(stdout, "failed=0") || !strings.Contains(stdout, "[       OK ] lmut_threads_in_place") {
		t.Fatalf("lmut program did not pass cleanly: exit=%d\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
}

func TestLmutIsParityWithMutableRef(t *testing.T) {
	le, ls, lerr := runLmutProgram(t, "lmut_parity", lmutThreadingBody)
	me, ms, merr := runLmutProgram(t, "mutref_parity", mutRefThreadingBody)
	lok := le == 0 && strings.Contains(ls, "failed=0")
	mok := me == 0 && strings.Contains(ms, "failed=0")
	if lok != mok {
		t.Fatalf("lmut and mutable& diverged: lmut ok=%v (exit %d) mutref ok=%v (exit %d)\nlmut stderr:\n%s\nmutref stderr:\n%s", lok, le, mok, me, lerr, merr)
	}
	if !lok {
		t.Fatalf("both lmut and mutable& failed unexpectedly:\nlmut:\n%s\nmutref:\n%s", ls, ms)
	}
}
