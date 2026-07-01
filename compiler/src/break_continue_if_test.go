package main

import "testing"

// `break if <cond>` / `continue if <cond>` are postfix-guard sugar that desugar at parse
// time to `if <cond>: break` / `if <cond>: continue` (mirroring the postfix `return if`
// form). No new AST node, so loop lowering is unchanged — this test pins the runtime
// behavior end to end.
//
// The loop counts i = 1,2,3,4,5,6:
//   - continue if i == 3   -> skips adding 3
//   - break    if i > 5    -> stops at i == 6 before adding it
// so total = 1+2+4+5 = 12.
func TestBreakAndContinueIfPostfixGuards(t *testing.T) {
	prog := `def main() -> int can[Console.Write, Abort.Panic]:
    total: mutable int = 0
    i: mutable int = 0
    while true:
        i <- i + 1
        continue if i == 3
        break if i > 5
        total <- total + i
    if total == 12:
        print("ok")
    else:
        print("bad")
    return 0
`
	status, out := s4CompileRun(t, prog)
	if status != "RAN" || out != "ok" {
		t.Fatalf("break-if/continue-if postfix guards: got status=%s out=%q, want RAN \"ok\"", status, out)
	}
}

// Bare `return if <cond>` desugars to `if <cond>: return` (early-return guard). An `if`
// immediately after `return` is a guard, never a ternary (which needs a preceding value).
// `classify` returns early for negatives/zero and only reaches the "3" print for n > 0,
// so classify(5) prints "pos" and classify(-2)/classify(0) print nothing.
func TestBareReturnIfGuard(t *testing.T) {
	prog := `def classify(n: int) -> void can[Console.Write, Abort.Panic]:
    return if n < 0
    return if n == 0
    print("pos")

def main() -> int can[Console.Write, Abort.Panic]:
    classify(-2)
    classify(0)
    classify(5)
    return 0
`
	status, out := s4CompileRun(t, prog)
	if status != "RAN" || out != "pos" {
		t.Fatalf("bare return-if guard: got status=%s out=%q, want RAN \"pos\"", status, out)
	}
}
