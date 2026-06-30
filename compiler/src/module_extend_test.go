package main

import (
	"strings"
	"testing"
)

// `extend Foo:` adds members to an already-declared `module Foo:`; members from
// the extension merge into Foo's namespace and are reachable via `Foo::member`.
func TestExtendModuleMergesMembers(t *testing.T) {
	prog := `module M:
    public:
        def base() -> i64:
            return 10

extend M:
    public:
        def added() -> i64:
            return 32

def main() -> int can[Abort.Panic]:
    return (M::base() + M::added()).int()
`
	status, _ := s4CompileRun(t, prog)
	// base()+added() == 42 -> exit code 42 -> RUNERR "exit status 42".
	if status != "RUNERR" {
		t.Fatalf("extend merge: got status=%s, want RUNERR (exit 42)", status)
	}
}

// `extend Foo:` where no `module Foo:` is declared is an error.
func TestExtendUndeclaredModuleRejected(t *testing.T) {
	prog := `extend Nope:
    public:
        def x() -> i64:
            return 1

def main() -> int:
    return 0
`
	status, out := s4CompileRun(t, prog)
	if status != "REJECTED" || !strings.Contains(out, "no module \"Nope\" to extend") {
		t.Fatalf("extend undeclared: got status=%s out=%q, want REJECTED with 'no module \"Nope\" to extend'", status, out)
	}
}

// Two canonical `module Foo:` declarations are a duplicate error (use extend instead).
func TestDuplicateModuleDeclarationRejected(t *testing.T) {
	prog := `module Dup:
    public:
        def a() -> i64:
            return 1

module Dup:
    public:
        def b() -> i64:
            return 2

def main() -> int:
    return 0
`
	status, out := s4CompileRun(t, prog)
	if status != "REJECTED" || !strings.Contains(out, "module \"Dup\" is already declared") {
		t.Fatalf("duplicate module: got status=%s out=%q, want REJECTED with 'module \"Dup\" is already declared'", status, out)
	}
}

// `extend` is order-independent: the extension may appear before the canonical module.
func TestExtendBeforeModuleDeclaration(t *testing.T) {
	prog := `extend M:
    public:
        def added() -> i64:
            return 7

module M:
    public:
        def base() -> i64:
            return 35

def main() -> int can[Abort.Panic]:
    return (M::base() + M::added()).int()
`
	status, _ := s4CompileRun(t, prog)
	// 35+7 == 42 -> exit 42.
	if status != "RUNERR" {
		t.Fatalf("extend-before-module: got status=%s, want RUNERR (exit 42)", status)
	}
}
