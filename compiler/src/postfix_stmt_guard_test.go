package main

import "testing"

// Generalized postfix statement guards (docs/125 §6b): `STMT if COND` is do-or-skip sugar
// on assignments, compound assignments, and expression statements, desugaring at parse time
// to `if COND: STMT` (no new AST node — exactly the break/continue/return-if mechanism).
// A postfix guard has NO `else`: two-way selection stays the ternary's job, and the ternary
// grammar is untouched (`else` remains mandatory wherever a guard is not recorded).
//
// The loop counts i = 1..6:
//   - total <- total + i if i != 3   -> skips adding 3      (assignment guard)
//   - evens += 1 if i % 2 == 0       -> counts 2,4,6        (compound-assign guard)
//
// so total = 1+2+4+5+6 = 18, evens = 3.
func TestPostfixAssignAndAugAssignGuards(t *testing.T) {
	prog := `def main() -> int can[Console.Write, Abort.Panic]:
    total: mutable int = 0
    evens: mutable int = 0
    i: mutable int = 0
    while i < 6:
        i <- i + 1
        total <- total + i if i != 3
        evens += 1 if i % 2 == 0
    if total == 18 and evens == 3:
        print("ok")
    else:
        print("bad")
    return 0
`
	status, out := s4CompileRun(t, prog)
	if status != "RAN" || out != "ok" {
		t.Fatalf("postfix assign guards: got status=%s out=%q, want RAN \"ok\"", status, out)
	}
}

// Expression-statement guard: a call runs only when the condition holds. classify(5)
// prints, classify(0) does not.
func TestPostfixExprStmtGuard(t *testing.T) {
	prog := `def report(n: int) -> void can[Console.Write, Abort.Panic]:
    print("pos") if n > 0

def main() -> int can[Console.Write, Abort.Panic]:
    report(0)
    report(5)
    return 0
`
	status, out := s4CompileRun(t, prog)
	if status != "RAN" || out != "pos" {
		t.Fatalf("postfix expr-stmt guard: got status=%s out=%q, want RAN \"pos\"", status, out)
	}
}

// The guarded RHS may itself be a completed ternary: `x <- (a if c else b) if cond` spelled
// without parens — the FIRST engagement is the ternary (its `else` is present), and the
// second `if` guards the statement... which is exactly the chained form we REJECT (one
// engagement per statement). Pin the accepted spelling with parens and the ternary intact.
func TestPostfixGuardOverParenthesizedTernaryRHS(t *testing.T) {
	prog := `def main() -> int can[Console.Write, Abort.Panic]:
    x: mutable int = 0
    flag: bool = true
    x <- (7 if flag else 9) if flag
    if x == 7:
        print("ok")
    else:
        print("bad")
    return 0
`
	status, out := s4CompileRun(t, prog)
	if status != "RAN" || out != "ok" {
		t.Fatalf("guard over parenthesized ternary: got status=%s out=%q, want RAN \"ok\"", status, out)
	}
}

// Ternary regressions the guard must NOT disturb:
//   - a bracketed `a if c` still demands its `else` (the guard never fires inside brackets)
//   - a declaration RHS still demands its `else` (declarations bind unconditionally)
//   - a chained `a if c else b if c2` still demands the second `else`
func TestPostfixGuardRejectsIllegalForms(t *testing.T) {
	cases := []struct{ name, prog string }{
		{"bracketed", `def f(n: int) -> int:
    return n

def main() -> int can[Console.Write, Abort.Panic]:
    x: int = f(1 if true)
    return 0
`},
		{"vardecl", `def main() -> int can[Console.Write, Abort.Panic]:
    flag: bool = true
    x: int = 5 if flag
    return 0
`},
		{"chained", `def main() -> int can[Console.Write, Abort.Panic]:
    flag: bool = true
    x: mutable int = 0
    x <- 1 if flag else 2 if flag
    return 0
`},
	}
	for _, tc := range cases {
		status, out := s4CompileRun(t, tc.prog)
		if status == "RAN" {
			t.Fatalf("%s: expected a compile error, but program ran (out=%q)", tc.name, out)
		}
	}
}
