package main

import "testing"

// A payload-less variant `is` target inside a module (`p is P.Wildcard`) parses as a TYPE
// PATH — ast.TypeExprExpr with the dotted name "P.Wildcard" — rather than the FieldExpr a
// payload form (`P.Binding(n)`) produces. The backend's TypeExprExpr path used to resolve
// the owner with a raw NamedTypes["P"] lookup, which misses when the enum is declared in a
// module and interned as "A.P"; the `is` then fell through to generic expression emission
// and crashed with "unsupported expression *ast.TypeExprExpr". The FieldExpr path never hit
// this because it already resolved through the namespace-aware lookupVisibleNamedType, which
// is why only the payload-LESS form broke.
//
// Discrimination must be correct at runtime, not merely compile: guard_form is asked about
// both variants, so a lowering that constant-folded either way would print "bad".
func TestPayloadLessVariantIsTargetInModule(t *testing.T) {
	prog := `module A:
    public:
        enum Node layout(handle: u32):
            pass
        enum P is Node:
            Wildcard
            Binding(name: sview)
using A

def guard_form(p: P) -> bool:
    true return if p is P.Wildcard
    return false

def main() -> int can[Console.Write, Abort.Panic]:
    if guard_form(P.Wildcard) and not guard_form(P.Binding("x")):
        print("ok")
    else:
        print("bad")
    return 0
`
	status, out := s4CompileRun(t, prog)
	if status != "RAN" || out != "ok" {
		t.Fatalf("payload-less variant `is` target in module: got status=%s out=%q, want RAN \"ok\"", status, out)
	}
}

// The block form must lower identically to the postfix-guard form — the original report saw
// both fail, confirming the bug was in `is`-target resolution and not in guard parsing.
func TestPayloadLessVariantIsTargetInModuleBlockForm(t *testing.T) {
	prog := `module A:
    public:
        enum Node layout(handle: u32):
            pass
        enum P is Node:
            Wildcard
            Binding(name: sview)
using A

def block_form(p: P) -> bool:
    if p is P.Wildcard:
        return true
    return false

def main() -> int can[Console.Write, Abort.Panic]:
    if block_form(P.Wildcard) and not block_form(P.Binding("x")):
        print("ok")
    else:
        print("bad")
    return 0
`
	status, out := s4CompileRun(t, prog)
	if status != "RAN" || out != "ok" {
		t.Fatalf("payload-less variant `is` block form in module: got status=%s out=%q, want RAN \"ok\"", status, out)
	}
}
