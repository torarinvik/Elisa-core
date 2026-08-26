package main

import "testing"

// End-to-end: a module member calling its own sibling by a name a GENERIC also uses must
// call the sibling, and the emitted program must agree with the type-check.
//
// The semantic test alone does not cover this. The first attempt at the fix preferred the
// enclosing module's member in ident analysis only: the call type-checked against the
// module's signature while codegen still emitted the generic's symbol, so the program
// compiled clean and returned the WRONG ANSWER. Renaming the callee is what keeps the two
// in agreement, and only running it shows that.
func TestModuleSiblingCallRunsAgainstItsOwnSibling(t *testing.T) {
	t.Parallel()
	// No `Flags`/`add` declared here: the stdlib prelude is already in scope in this
	// harness (declaring them gives `duplicate type "Flags"`), so the collision this
	// guards is the REAL one -- `add[T](mutable Flags[T]&, T)` from collections.elisa.
	status, out := s4CompileRun(t, `module Box:
    struct Sack:
        items: mutable darray[i64]

    def add(s: mutable Sack&, v: i64) -> void can[Memory.Allocate, Abort.Panic]:
        s.items.push(v)
        return

    def fill(s: mutable Sack&) -> void can[Memory.Allocate, Abort.Panic]:
        add(s, 7) can Memory.Allocate, Abort.Panic
        return

def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    region a(4096):
        s: mutable Box::Sack = Box::Sack{items: []}
        Box::fill(&s) can Memory.Allocate, Abort.Panic
        print(s.items[0]) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "RAN" || out != "7" {
		t.Fatalf("expected RAN 7 (the sibling ran, not the generic), got %s %q", status, out)
	}
}
