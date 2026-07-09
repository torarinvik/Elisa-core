package main

import "testing"

// docs/125 §5 — `machine from` state-machine expressions compile and RUN: an acyclic
// machine (jump-threaded), a cyclic machine with `decreases` + a threaded mutable local,
// and the doc's number-scanner shape yielding a TokenKind.
func TestMachineFromRuntime(t *testing.T) {
	body := `
const enum TokenKind of u8:
    IntLit
    FloatLit

const enum Num of u8:
    Integer
    Fraction

def scan(is_float: bool) -> TokenKind:
    return machine from Num.Integer:
        Num.Integer:
            next Num.Fraction if is_float
            done TokenKind.IntLit
        Num.Fraction:
            done TokenKind.FloatLit

const enum Scan of u8:
    Step
    Stop

def count_down(start: i64) -> i64:
    n: mutable i64 = start
    return machine from Scan.Step decreases n:
        Scan.Step:
            n <- n - 1
            next Scan.Stop if n <= 0
            next Scan.Step
        Scan.Stop:
            done n

@test
def acyclic_machine() -> void:
    can Abort.Panic:
        if scan(false) != TokenKind.IntLit:
            panic("int")
        if scan(true) != TokenKind.FloatLit:
            panic("float")

@test
def cyclic_machine_with_decreases() -> void:
    can Abort.Panic:
        if count_down(5) != 0:
            panic("five")
        if count_down(1) != 0:
            panic("one")
        if count_down(0) != -1:
            panic("zero steps once to -1")
`
	exit, stdout, stderr := runStressProgram(t, "machine_from", body)
	assertAllPassed(t, exit, stdout, stderr, "acyclic_machine", "cyclic_machine_with_decreases")
}
