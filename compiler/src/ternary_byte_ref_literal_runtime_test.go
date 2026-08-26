package main

import "testing"

// A string literal has the static u8& representation. It must merge with a
// non-static u8& parameter in a ternary; stage1's coarse ternary parity pass
// previously reported a false `u8 and static u8&` mismatch for this valid
// stage0 program.
const ternaryByteRefLiteralBody = `
def choose(message: u8&, condition: bool) -> u8&:
    return message if condition else "fallback"

@test
def ternary_byte_ref_literal_merge() -> void:
    choose("message", true)
`

func TestTernaryByteRefLiteralMerge(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "ternary_byte_ref_literal", ternaryByteRefLiteralBody)
	assertAllPassed(t, exit, stdout, stderr, "ternary_byte_ref_literal_merge")
}
