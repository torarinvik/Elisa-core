package main

import "testing"

// docs/119 gap #3: every f-string lowers through __fstr, so `f"…"` has ONE type (owned
// formatted string) whether or not it interpolates. A match/if expression whose arms
// mix interpolated and plain f-strings now unifies (previously a plain f-string typed
// as `static u8&` and would not unify with an interpolated `darray[u8]` arm).
const fstringMatchArmBody = `
enum Kind:
    Named
    Anon
    Other

def label(k: Kind, name: cstr) -> dstr:
    return match k:
        Kind.Named: f"named '{name}'"
        Kind.Anon: f"anonymous"
        _: f""

@test
def fstring_match_arms() -> void:
    can Abort.Panic:
        if label(Kind.Named, "x").count == 0:
            panic("named empty")
        if label(Kind.Anon, "x").count == 0:
            panic("anon empty")
        # the empty-f-string arm yields a zero-length owned string, not a crash
        if label(Kind.Other, "x").count != 0:
            panic("other not empty")
`

func TestFStringMatchArms(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "fstring_match_arms", fstringMatchArmBody)
	assertAllPassed(t, exit, stdout, stderr, "fstring_match_arms")
}
