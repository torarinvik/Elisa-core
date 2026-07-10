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

// docs/125 §5 state payloads — a state that carries data legal only in that state: `next`
// constructs the successor's payload, the arm header binds it. States are variants of a
// regular (non-const) enum so variants can carry fields. Covers a `next`-constructed
// payload threaded through an arm binding, and a payload on the entry state itself.
func TestMachineFromPayloadRuntime(t *testing.T) {
	body := `
enum Phase:
    Start
    Mid(bool)
    End(i64)

def run(x: i64) -> i64:
    return machine from Phase.Start:
        Phase.Start:
            next Phase.Mid(true) if x > 0
            next Phase.Mid(false)
        Phase.Mid(flag):
            next Phase.End(10) if flag
            next Phase.End(20)
        Phase.End(val):
            done val

enum Once:
    Only(i64)

def echo(seed: i64) -> i64:
    return machine from Once.Only(seed):
        Once.Only(val):
            done val

@test
def payload_threads_through_states() -> void:
    can Abort.Panic:
        if run(5) != 10:
            panic("positive routes Mid(true) -> End(10)")
        if run(-1) != 20:
            panic("nonpositive routes Mid(false) -> End(20)")

@test
def entry_state_payload() -> void:
    can Abort.Panic:
        if echo(42) != 42:
            panic("start payload binds and yields")
        if echo(-7) != -7:
            panic("start payload negative")
`
	exit, stdout, stderr := runStressProgram(t, "machine_from_payload", body)
	assertAllPassed(t, exit, stdout, stderr, "payload_threads_through_states", "entry_state_payload")
}

// docs/125 §5 R5 — declared out-edges (`State -> {A, B}:`) are a compile-time contract with
// zero runtime cost: a machine whose transitions honor its declarations lowers and runs
// exactly as the undeclared form would. Combined here with an entry-state payload.
func TestMachineFromDeclaredOutRuntime(t *testing.T) {
	body := `
enum Route:
    In(i64)
    Left
    Right

def route(x: i64) -> i64:
    return machine from Route.In(x):
        Route.In(v) -> {Left, Right}:
            next Route.Left if v > 0
            next Route.Right
        Route.Left:
            done 1
        Route.Right:
            done 2

@test
def declared_out_runs() -> void:
    can Abort.Panic:
        if route(5) != 1:
            panic("positive routes Left")
        if route(-1) != 2:
            panic("nonpositive routes Right")
`
	exit, stdout, stderr := runStressProgram(t, "machine_from_declared_out", body)
	assertAllPassed(t, exit, stdout, stderr, "declared_out_runs")
}
