package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A function's return value must be fully materialized before its owned arenas
// are freed. Two lowerings used to read function-owned storage AFTER arena_free:
// the auto-deref of a returned `T&` (coerceValue's `ref.value` load) and the
// memcpy that storeValue emits for a large aggregate `load` written to an sret
// or error-union payload slot. Both segfaulted or returned freed bytes.
const returnAfterArenaFreeSource = `struct Big:
    a: i64
    pad: i64[200]

error ReturnProblem:
    Bad

def ref_scalar() -> i64:
    mutable xs: darray[i64] = [5, 6]
    r: i64& = &xs[0]
    return r

def big_sret() -> Big:
    mutable xs: darray[Big] = [Big{a: 7, pad: zeroed}]
    return xs[0]

def ref_error_union(k: i64) -> i64 error[ReturnProblem]:
    mutable xs: darray[i64] = [9, 8]
    r: i64& = &xs[0]
    if k > 5:
        raise ReturnProblem.Bad
    return r

def big_error_union(k: i64) -> Big error[ReturnProblem]:
    mutable xs: darray[Big] = [Big{a: 11, pad: zeroed}]
    if k > 5:
        raise ReturnProblem.Bad
    return xs[0]

def big_named_region() -> Big:
    region r:
        xs: mutable darray[Big] @r = []
        xs.push(Big{a: 13, pad: zeroed})
        return xs[0]
`

func TestReturnValueMaterializedBeforeArenaFreeIR(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "return_after_arena_free_ir.elisa")
	if err := os.WriteFile(path, []byte(returnAfterArenaFreeSource+"\ndef main() -> i32:\n    return 0\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("build failed (exit %d)\nstderr:\n%s", code, stderr.String())
	}
	bodies := map[string][]string{}
	current := ""
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(line, "define") {
			current = ""
			for _, name := range []string{"ref_scalar", "big_sret", "ref_error_union", "big_error_union", "big_named_region"} {
				if strings.Contains(line, "@"+name+"(") {
					current = name
				}
			}
			continue
		}
		if line == "}" {
			current = ""
			continue
		}
		if current != "" {
			bodies[current] = append(bodies[current], line)
		}
	}
	for _, name := range []string{"ref_scalar", "big_sret", "ref_error_union", "big_error_union", "big_named_region"} {
		body := bodies[name]
		if len(body) == 0 {
			t.Fatalf("function %s not found in IR:\n%s", name, stdout.String())
		}
		// Every return path is: materialize, free, ret. Nothing that reads memory
		// may follow an arena_free until the block's terminator.
		freed := false
		sawFree := false
		for _, line := range body {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "@arena_free(") {
				freed = true
				sawFree = true
				continue
			}
			if strings.HasPrefix(trimmed, "ret ") || strings.HasPrefix(trimmed, "br ") || strings.HasSuffix(trimmed, ":") {
				freed = false
				continue
			}
			if freed && (strings.Contains(trimmed, " load ") || strings.Contains(trimmed, "@memcpy(")) {
				t.Fatalf("%s reads memory after arena_free: %q\n%s", name, trimmed, strings.Join(body, "\n"))
			}
		}
		if !sawFree {
			t.Fatalf("%s frees no arena; the fixture no longer exercises the ordering:\n%s", name, strings.Join(body, "\n"))
		}
	}
}

func TestReturnValueMaterializedBeforeArenaFreeRuntime(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil {
		t.Fatalf("resolve std runtime path: %v", err)
	}
	src := "include \"" + std + "\"\n" + returnAfterArenaFreeSource + `
@test
def return_after_arena_free_runtime_test() -> void:
    can Abort.Panic:
        if ref_scalar() != 5:
            panic("returned ref read freed arena")
        if big_sret().a != 7:
            panic("sret return copied from freed arena")
        v: i64 = try ref_error_union(1) else 0
        if v != 9:
            panic("error-union ref return read freed arena")
        b: Big = try big_error_union(1) else Big{a: 0, pad: zeroed}
        if b.a != 11:
            panic("error-union payload copied from freed arena")
        if big_named_region().a != 13:
            panic("named-region return copied from freed arena")
`
	path := filepath.Join(t.TempDir(), "return_after_arena_free_runtime.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("runtime test failed (exit %d)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "passed=1") {
		t.Fatalf("expected passed=1, got:\n%s", stdout.String())
	}
}
