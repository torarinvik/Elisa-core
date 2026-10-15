package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// docs/128 section 5: a program-lived region allocates in one program arena that is never
// freed. A region-polymorphic call inside `in source:` binds its region to that arena, and
// `new[source]` allocates there; the bytes stay valid after the allocating function and its
// local dstr are gone.
func TestRunCLIProgramLivedRegionArena(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixturePath := filepath.Join(t.TempDir(), "program_region_runtime_fixture.elisa")
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil {
		t.Fatalf("resolve std runtime path: %v", err)
	}
	src := `global region source

struct Node[@r]:
    name: mutable sview @r

def copy_in[@r](text: sview) -> sview @r:
    can Memory.Allocate:
        bytes: mutable darray[u8] @r = []
        for b in text:
            bytes.push(b)
        return bytes.as_sview()

def fill(n: mutable Node[source]&) -> void:
    can Memory.Allocate:
        local: mutable dstr = []
        local.push(104)
        local.push(105)
        in source:
            n.name <- copy_in(local.as_sview())

@test
def program_region_runtime_test() -> void:
    can Abort.Panic:
        n: mutable Node[source] = Node[source]{name: ""}
        fill(n)
        p: i64& = new[source] 41
        if n.name != "hi" or p + 1 != 42:
            panic("program region lost data")
`
	full := "include \"" + std + "\"\n" + src
	if err := os.WriteFile(fixturePath, []byte(full), 0o644); err != nil {
		t.Fatalf("failed to write program region fixture: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected program region runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] program_region_runtime_test") {
		t.Fatalf("expected the program region test to pass, got:\n%s", stdout.String())
	}
}

// docs/128 section 8: a declared program-lived store is one store for the whole program.
// Nodes built in different functions (and with no region in scope) decode against it.
func TestRunCLIDeclaredProgramStore(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixturePath := filepath.Join(t.TempDir(), "declared_store_runtime_fixture.elisa")
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil {
		t.Fatalf("resolve std runtime path: %v", err)
	}
	src := `global region source

enum Node layout(handle: u32) @source:
    pass

enum Expr is Node:
    Name(name: sview @source)
    Pair(left: Expr, right: Expr)

def build(a: sview @source, b: sview @source) -> Expr:
    return Expr.Pair(Expr.Name(a), Expr.Name(b))

def first(e: Expr) -> sview @source:
    match e:
        Expr.Pair(l, r):
            match l:
                Expr.Name(n):
                    return n
                Expr.Pair(x, y):
                    return ""
        Expr.Name(n):
            return n

@test
def store_test() -> void:
    can Abort.Panic:
        e: Expr = build("hi", "yo")
        if first(e) != "hi":
            panic("bad")

def keep(xs: mutable darray[Expr]&, a: sview @source) -> void:
    can Memory.Allocate:
        xs.push(Expr.Name(a))

@test
def store_across_test() -> void:
    can Abort.Panic, Memory.Allocate:
        xs: mutable darray[Expr] = []
        keep(xs, "zz")
        keep(xs, "hi")
        e: Expr = build("aa", "bb")
        if first(xs[1]) != "hi" or first(xs[0]) != "zz" or first(e) != "aa":
            panic("bad store")
`
	full := "include \"" + std + "\"\n" + src
	if err := os.WriteFile(fixturePath, []byte(full), 0o644); err != nil {
		t.Fatalf("failed to write declared store fixture: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected declared store runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "passed=2") {
		t.Fatalf("expected both declared store tests to pass, got:\n%s", stdout.String())
	}
}
