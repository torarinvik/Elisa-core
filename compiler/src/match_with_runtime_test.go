package main

import "testing"

// docs/125 §5 — `with NAME = LITERAL` or-alternative discriminators run correctly: each
// alternative binds its own constant, so an or-arm sharing a body reads the discriminator
// uniformly. Covers the signed-literal collapse the spec cites (depth-14 → one arm),
// multi-binding `with`, and the statement form.
func TestMatchWithDiscriminatorRuntime(t *testing.T) {
	body := `
enum E:
    Lit(v: i64)
    Neg(v: i64)

def signed(e: E) -> i64:
    return match e:
        E.Lit(magnitude) with negated = false | E.Neg(magnitude) with negated = true:
            (0 - magnitude) if negated else magnitude
        _: 0

def multi(e: E) -> i64:
    return match e:
        E.Lit(m) with sign = 1, tag = 10 | E.Neg(m) with sign = -1, tag = 20:
            m * sign + tag
        _: 0

def report(e: E, out: mutable i64&) -> void:
    match e:
        E.Lit(m) with negated = false | E.Neg(m) with negated = true:
            out <- (0 - m) if negated else m
        _:
            out <- 0

enum Tok:
    Yes
    No
    Other

# Bare-member (payload-free) or-arm with-discriminator in value position — the shape
# a dogfood sweep of the self-hosted parser surfaced (match on a scalar enum kind,
# each alternative distinguished only by its discriminating constant).
def flag(t: Tok) -> i64:
    return match t:
        Tok.Yes with bit = 1 | Tok.No with bit = 0:
            bit
        _: 2

@test
def with_discriminator() -> void:
    can Abort.Panic:
        if signed(E.Lit(5)) != 5:
            panic("lit -> positive")
        if signed(E.Neg(5)) != -5:
            panic("neg -> negated")
        if multi(E.Lit(3)) != 13:
            panic("multi lit")
        if multi(E.Neg(3)) != 17:
            panic("multi neg")

@test
def with_statement_form() -> void:
    can Abort.Panic:
        r: mutable i64 = 0
        report(E.Neg(8), &r)
        if r != -8:
            panic("stmt neg")
        report(E.Lit(8), &r)
        if r != 8:
            panic("stmt lit")

@test
def with_bare_member() -> void:
    can Abort.Panic:
        if flag(Tok.Yes) != 1:
            panic("yes -> 1")
        if flag(Tok.No) != 0:
            panic("no -> 0")
        if flag(Tok.Other) != 2:
            panic("other -> 2")
`
	exit, stdout, stderr := runStressProgram(t, "match_with_discriminator", body)
	assertAllPassed(t, exit, stdout, stderr, "with_discriminator", "with_statement_form", "with_bare_member")
}
