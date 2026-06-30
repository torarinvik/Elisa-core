package main

import (
	"strings"
	"testing"
)

// `using Foo` (wildcard) brings all public names in unqualified — including in
// TYPE-annotation position, not just value/pattern position (the documented wart).
func TestUsingWildcardReachesTypePosition(t *testing.T) {
	prog := `module M:
    public:
        const enum Color of u8:
            Red
            Green
        def base() -> i64:
            return 40

using M

def pick(c: Color) -> i64:
    if c == Color.Green:
        return 2
    return 0

def main() -> int can[Abort.Panic]:
    return (base() + pick(Color.Green)).int()
`
	status, _ := s4CompileRun(t, prog)
	// 40 + 2 == 42 -> exit 42.
	if status != "RUNERR" {
		t.Fatalf("using wildcard (type position): got status=%s, want RUNERR (exit 42)", status)
	}
}

// `using Foo::bar` (selective) brings only `bar` in unqualified.
func TestUsingSelective(t *testing.T) {
	prog := `module M:
    public:
        def base() -> i64:
            return 42
        def other() -> i64:
            return 99

using M::base

def main() -> int can[Abort.Panic]:
    return base().int()
`
	status, _ := s4CompileRun(t, prog)
	if status != "RUNERR" { // exit 42
		t.Fatalf("using selective: got status=%s, want RUNERR (exit 42)", status)
	}
}

// A selective `using Foo::bar` does NOT bring sibling names in unqualified.
func TestUsingSelectiveDoesNotLeakSiblings(t *testing.T) {
	prog := `module M:
    public:
        def base() -> i64:
            return 1
        def other() -> i64:
            return 2

using M::base

def main() -> int can[Abort.Panic]:
    return other().int()
`
	status, out := s4CompileRun(t, prog)
	if status != "REJECTED" || !strings.Contains(out, "other") {
		t.Fatalf("using selective leak: got status=%s out=%q, want REJECTED (other undefined)", status, out)
	}
}

// `using Foo as F` aliases the module qualifier: `F::x` resolves to `Foo.x`.
func TestUsingModuleAlias(t *testing.T) {
	prog := `module Geometry:
    public:
        def area() -> i64:
            return 42

using Geometry as G

def main() -> int can[Abort.Panic]:
    return G::area().int()
`
	status, _ := s4CompileRun(t, prog)
	if status != "RUNERR" { // exit 42
		t.Fatalf("using alias: got status=%s, want RUNERR (exit 42)", status)
	}
}

// Two wildcard `using` imports that both export `clash` make a bare reference
// ambiguous — it must be qualified.
func TestUsingWildcardCollisionRejected(t *testing.T) {
	prog := `module A:
    public:
        def clash() -> i64:
            return 1

module B:
    public:
        def clash() -> i64:
            return 2

using A
using B

def main() -> int can[Abort.Panic]:
    return clash().int()
`
	status, out := s4CompileRun(t, prog)
	if status != "REJECTED" || !strings.Contains(out, "ambiguous reference \"clash\"") {
		t.Fatalf("using collision: got status=%s out=%q, want REJECTED with ambiguity", status, out)
	}
}

// The collision is resolvable by qualifying the reference.
func TestUsingCollisionResolvableByQualifying(t *testing.T) {
	prog := `module A:
    public:
        def clash() -> i64:
            return 1

module B:
    public:
        def clash() -> i64:
            return 42

using A
using B

def main() -> int can[Abort.Panic]:
    return B::clash().int()
`
	status, _ := s4CompileRun(t, prog)
	if status != "RUNERR" { // exit 42
		t.Fatalf("qualified resolve: got status=%s, want RUNERR (exit 42)", status)
	}
}
