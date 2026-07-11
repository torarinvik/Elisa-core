package main

import "testing"

// docs/125 §9.6 / roadmap C3 — a pure total `char -> const enum` classifier folds to a
// static 256-entry lookup table. This checks the FOLDED classifier is behavior-identical to
// the branch chain it replaces, by asserting every byte 0..255 maps to the class a
// hand-written switch would give. (The fold is a semantic-phase rewrite, so if it were
// miscompiled these tallies would diverge.)
func TestClassifierFoldRuntime(t *testing.T) {
	body := `
const enum NumberClass of u8:
    Digit
    ExpMark
    HexAlpha
    Dot
    Sign
    Other

def classify(c: char) -> NumberClass:
    return when c:
        '0'..='9' -> NumberClass.Digit
        'e' | 'E' -> NumberClass.ExpMark
        'a'..='d' | 'f' | 'A'..='D' | 'F' -> NumberClass.HexAlpha
        '.' -> NumberClass.Dot
        '+' | '-' -> NumberClass.Sign
        _ -> NumberClass.Other

# Independent oracle: the same partition written as explicit boolean tests, so a
# miscompiled fold table would disagree with it.
def oracle(c: char) -> NumberClass:
    if c >= '0' and c <= '9':
        return NumberClass.Digit
    if c == 'e' or c == 'E':
        return NumberClass.ExpMark
    if (c >= 'a' and c <= 'd') or c == 'f' or (c >= 'A' and c <= 'D') or c == 'F':
        return NumberClass.HexAlpha
    if c == '.':
        return NumberClass.Dot
    if c == '+' or c == '-':
        return NumberClass.Sign
    return NumberClass.Other

@test
def fold_matches_oracle_over_all_bytes() -> void:
    can Abort.Panic:
        i: mutable i64 = 0
        while i < 256:
            c: char = i.char()
            if classify(c) != oracle(c):
                panic("folded classifier disagrees with oracle")
            i <- i + 1
`
	exit, stdout, stderr := runStressProgram(t, "classifier_fold", body)
	assertAllPassed(t, exit, stdout, stderr, "fold_matches_oracle_over_all_bytes")
}
