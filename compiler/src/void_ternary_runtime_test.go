package main

import "testing"

// A ternary whose arms are both void (`f(x) if cond else g(x)`) is a choice between two
// effects, not between two values. Codegen used to merge the arms with a phi regardless
// of type, and `phi void` is not a legal instruction — the backend died with "Instruction
// has a name, but provides a void value". Both arms still have to run for their effects,
// and exactly one of them must.
const voidTernaryBody = `
def bump(out: mutable i64&, by: i64) -> void:
    out <- out + by

def choose(out: mutable i64&, cond: bool) -> void:
    bump(out, 1) if cond else bump(out, 10)

@test
def void_ternary_selects_one_arm() -> void:
    can Abort.Panic:
        taken: mutable i64 = 0
        choose(&taken, true)
        if taken != 1:
            panic("void ternary did not run only the then arm")

        not_taken: mutable i64 = 0
        choose(&not_taken, false)
        if not_taken != 10:
            panic("void ternary did not run only the else arm")

        # Repeated, so a mis-merged phi cannot pass by landing on the right value once.
        total: mutable i64 = 0
        for i in 0..<4:
            choose(&total, i % 2 == 0)
        if total != 22:
            panic("void ternary arms did not accumulate one effect per evaluation")
`

func TestVoidTernary(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "void_ternary", voidTernaryBody)
	assertAllPassed(t, exit, stdout, stderr, "void_ternary")
}
