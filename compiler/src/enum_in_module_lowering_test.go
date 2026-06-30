package main

import "testing"

// Regression: an enum defined inside a module and used from another function lowers
// correctly — both via `using M` (bare references) and via `M::`-qualified references.
// Previously the backend looked up the enum type/variant by bare name and failed with
// `unknown identifier "..." during LLVM lowering`, because the enum is registered under
// its module-qualified name. The fix routes those lookups through lookupVisibleNamedType
// (namespace + using-aware), mirroring the directCallTarget canonical-name fix.

func TestEnumInModuleUsingForm(t *testing.T) {
	prog := `module M:
    public:
        const enum Color of u8:
            Red
            Green
            Blue

using M

def main() -> int can[Console.Write, Console.Format, Abort.Panic]:
    c: Color = Color.Green
    match c:
        Color.Red:
            print("red\n")
        Color.Green:
            print("green\n")
        Color.Blue:
            print("blue\n")
    return 0
`
	status, out := s4CompileRun(t, prog)
	if status != "RAN" || out != "green" {
		t.Fatalf("enum-in-module via `using`: got status=%s out=%q, want RAN \"green\"", status, out)
	}
}

func TestEnumInModuleQualifiedValue(t *testing.T) {
	// `M::Color.Green` in value position (no `using`) must lower; name_of returns 2 for Green.
	prog := `module M:
    public:
        const enum Color of u8:
            Red
            Green
            Blue

def name_of(c: M::Color) -> i64:
    if c == M::Color.Red:
        return 1
    if c == M::Color.Green:
        return 2
    return 3

def main() -> int can[Console.Write, Console.Format, Abort.Panic]:
    return name_of(M::Color.Green).int()
`
	status, _ := s4CompileRun(t, prog)
	// main returns 2 -> process exit code 2 -> harness reports RUNERR with "exit status 2".
	if status != "RUNERR" {
		t.Fatalf("enum-in-module qualified value: got status=%s, want RUNERR (exit 2)", status)
	}
}

// `M::Color.Red` qualified enum variant in a MATCH PATTERN parses and lowers (no
// `using` needed). Previously the pattern parser only accepted `.` as a segment
// separator, so `::` left the arm unparseable.
func TestEnumInModuleQualifiedMatchPattern(t *testing.T) {
	prog := `module M:
    public:
        const enum Color of u8:
            Red
            Green
            Blue

def code(c: M::Color) -> i64:
    match c:
        M::Color.Red:
            return 1
        M::Color.Green:
            return 42
        M::Color.Blue:
            return 3

def main() -> int can[Abort.Panic]:
    return code(M::Color.Green).int()
`
	status, out := s4CompileRun(t, prog)
	if status != "RUNERR" { // exit 42
		t.Fatalf("qualified match pattern: got status=%s out=%q, want RUNERR (exit 42)", status, out)
	}
}
