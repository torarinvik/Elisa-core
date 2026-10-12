package main

import "testing"

func TestGuardConditionListRuntime(t *testing.T) {
	src := `def forbidden() -> bool:
    can Console.Write:
        print("bad")
    return false

def select(flag: bool) -> i64:
    return 7 if:
        flag
        forbidden() can Console.Write
    return 9

def forbidden_value() -> i64:
    can Console.Write:
        print("bad-value")
    return 7

def skipped_value() -> i64:
    can Console.Write:
        return forbidden_value() if:
            false
            false
    return 9

def main() -> i64:
    can Console.Write, Abort.Panic:
        total: mutable i64 = 0
        for i in 0..<8 |total|:
            continue if:
                i == 1
                i == 3
            break if:
                i == 6
                i == 7
            total <- total + i
        print("ok") if total == 11 and select(true) == 7 and skipped_value() == 9
        return 0
`
	status, out := s4CompileRun(t, src)
	if status != "RAN" || out != "ok" {
		t.Fatalf("status=%s output=%q, want RAN ok", status, out)
	}
}
