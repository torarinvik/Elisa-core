package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Store/Handle unification (docs/69): darray now implements the stdlib `Store`
// interface (collections.elisa), so generic code over `[S: Store]` works on a darray —
// resolving a handle (usize index) to an element and reporting the count — without the
// program defining the interface itself. This exercises the parametric impl end to end
// through the real stdlib surface.
func TestRunCLIStoreSurfaceOverDArray(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"))
	src := preamble + `
def get_at[S: Store](s: S&, h: S.Handle) -> S.Elem&:
    return S.store_get(s, h)

def how_many[S: Store](s: S&) -> usize:
    return S.store_count(s)

@test
def store_over_darray() -> void:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(11)
        xs.push(22)
        xs.push(33)
        if how_many[darray[i64]](xs) != 3:
            panic("Store.store_count over darray wrong")
        if get_at[darray[i64]](xs, 0) != 11:
            panic("Store.store_get[0] over darray wrong")
        if get_at[darray[i64]](xs, 2) != 33:
            panic("Store.store_get[2] over darray wrong")
`
	fixturePath := filepath.Join(fixtureDir, "store_over_darray.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected Store-over-darray test to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"[       OK ] store_over_darray",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, stdout.String())
		}
	}
}

// Focused regression: a ref-returning generic static-interface method used INLINE as a
// binary-comparison operand must auto-deref correctly — same as when bound to a local.
func TestRunCLIStoreSurfaceOverDequeInline(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"), rel("deque.elisa"))
	src := preamble + `
def get_at[S: Store](s: S&, h: S.Handle) -> S.Elem&:
    return S.store_get(s, h)

@test
def store_over_deque_inline() -> void:
    can Memory.Allocate, Abort.Panic:
        region r:
            a: mutable Arena& = &r
            dq: mutable Deque[i64] = arena_deque_with_capacity[i64](a, 8)
            _ = arena_deque_push_back[i64](a, dq, 100)
            _ = arena_deque_push_back[i64](a, dq, 200)
            _ = arena_deque_push_back[i64](a, dq, 300)
            if get_at[Deque[i64]](dq, 2) != 300:
                panic("Store.store_get[2] over deque (inline) wrong")
`
	fixturePath := filepath.Join(fixtureDir, "store_over_deque_inline.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected Store-over-deque inline test to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] store_over_deque_inline") {
		t.Fatalf("expected store_over_deque_inline to pass, got:\n%s", stdout.String())
	}
}

// A second Store backing: Deque (a ring buffer, collections.elisa) implements the same
// surface as darray, so the very same generic `[S: Store]` code works on it — handle
// (usize logical index) → element, mapped through the ring's head/wrap. This proves the
// unification generalizes beyond darray to a structurally different container.
func TestRunCLIStoreSurfaceOverDeque(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"), rel("deque.elisa"))
	src := preamble + `
def get_at[S: Store](s: S&, h: S.Handle) -> S.Elem&:
    return S.store_get(s, h)

def how_many[S: Store](s: S&) -> usize:
    return S.store_count(s)

@test
def store_over_deque() -> void:
    can Memory.Allocate, Abort.Panic:
        region r:
            a: mutable Arena& = &r
            dq: mutable Deque[i64] = arena_deque_with_capacity[i64](a, 8)
            _ = arena_deque_push_back[i64](a, dq, 100)
            _ = arena_deque_push_back[i64](a, dq, 200)
            _ = arena_deque_push_back[i64](a, dq, 300)
            n: usize = how_many[Deque[i64]](dq)
            v0: i64 = get_at[Deque[i64]](dq, 0)
            v2: i64 = get_at[Deque[i64]](dq, 2)
            if n != 3:
                panic("Store.store_count over deque wrong")
            if v0 != 100:
                panic("Store.store_get[0] over deque wrong")
            if v2 != 300:
                panic("Store.store_get[2] over deque wrong")
`
	fixturePath := filepath.Join(fixtureDir, "store_over_deque.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected Store-over-deque test to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] store_over_deque") {
		t.Fatalf("expected store_over_deque to pass, got:\n%s", stdout.String())
	}
}
