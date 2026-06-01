package interpreter_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"elisacore/src/interpreter"
)

func TestDebuggerBreaksOnConditionAndTimeTravels(t *testing.T) {
	src := `struct Player:
    dead: mutable bool

def run() -> i64:
    player: Player = Player(false)
    player.dead <- true
    return 0
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_debugger.elisa", src)
	debugger := interpreter.NewDebugger(interpreter.BreakWhenPathBool("player.dead", true))
	_, err := interpreter.Execute(result, interpreter.Options{Entry: "run", Debugger: debugger})
	var halt *interpreter.DebugHaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected debugger halt, got %v", err)
	}
	if halt.Hit.Condition != "player.dead == true" {
		t.Fatalf("unexpected halt condition %q", halt.Hit.Condition)
	}
	current, ok := debugger.Current()
	if !ok {
		t.Fatalf("debugger has no current snapshot")
	}
	if got, ok := current.LookupString("player.dead"); !ok || got != "true" {
		t.Fatalf("expected current player.dead true, got %q ok=%v", got, ok)
	}
	if current.Event != interpreter.DebugAfterStmt || current.StatementKind != "AssignStmt" {
		t.Fatalf("expected halt after assignment, got %s %s", current.Event, current.StatementKind)
	}
	previous, ok := debugger.StepBack()
	if !ok {
		t.Fatalf("could not step backward from halt")
	}
	if got, ok := previous.LookupString("player.dead"); !ok || got != "false" {
		t.Fatalf("expected previous player.dead false, got %q ok=%v", got, ok)
	}
	next, ok := debugger.StepForward()
	if !ok {
		t.Fatalf("could not step forward to halt")
	}
	if next.Index != current.Index {
		t.Fatalf("expected step forward to return halt snapshot %d, got %d", current.Index, next.Index)
	}
}

func TestDebugSessionRingBufferAndSeek(t *testing.T) {
	src := `def run() -> i64:
    value: mutable i64 = 0
    value <- 1
    value <- 2
    value <- 3
    return value
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_debugger_ring.elisa", src)
	session := interpreter.NewDebugSession(interpreter.DebuggerConfig{TraceLimit: 3})
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run", Debugger: session.Debugger})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "3" {
		t.Fatalf("expected result 3, got %s", got)
	}
	if got := len(session.Debugger.Trace); got != 3 {
		t.Fatalf("expected ring trace length 3, got %d", got)
	}
	if session.Debugger.Dropped == 0 {
		t.Fatalf("expected dropped events to be reported")
	}
	current, ok := session.Debugger.Current()
	if !ok {
		t.Fatalf("expected current snapshot")
	}
	if _, ok := session.Seek(int(current.Step)); !ok {
		t.Fatalf("expected seek by step %d to succeed", current.Step)
	}
	id := session.SetBreakpoint(interpreter.BreakWhenExpr("value == 3"))
	if id == 0 || !session.RemoveBreakpoint(id) {
		t.Fatalf("expected breakpoint id to be removable")
	}
}

func TestDebugSessionEventsAndBreakpointControls(t *testing.T) {
	src := `def run() -> i64:
    hp: mutable i64 = 2
    hp <- hp - 1
    hp <- hp - 1
    return hp
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_debugger_events.elisa", src)
	session := interpreter.NewDebugSession(interpreter.DebuggerConfig{FullTrace: true})
	var events []interpreter.DebugSessionEvent
	session.OnEvent(func(event interpreter.DebugSessionEvent) {
		events = append(events, event)
	})
	disabledID := session.SetBreakpoint(interpreter.BreakWhenExpr("hp <= 0"))
	if !session.SetBreakpointEnabled(disabledID, false) {
		t.Fatalf("expected breakpoint to be disabled")
	}
	watchID := session.SetBreakpoint(interpreter.BreakWhenPathChanges("hp"))
	if watchID == 0 {
		t.Fatalf("expected watchpoint id")
	}
	_, err := interpreter.Execute(result, interpreter.Options{Entry: "run", Debugger: session.Debugger})
	var halt *interpreter.DebugHaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected watchpoint halt, got %v", err)
	}
	if halt.Hit.Condition != "hp changed" {
		t.Fatalf("expected watchpoint hit, got %q", halt.Hit.Condition)
	}
	if halt.Hit.StopReason != interpreter.DebugStopWatchpoint {
		t.Fatalf("expected watchpoint stop reason, got %q", halt.Hit.StopReason)
	}
	if session.State != interpreter.DebugSessionHalted {
		t.Fatalf("expected halted session state, got %s", session.State)
	}
	if len(events) == 0 {
		t.Fatalf("expected live session events")
	}
	var sawSnapshot, sawHalt bool
	for _, event := range events {
		if event.Type == interpreter.DebugSessionEventSnapshotAdded {
			sawSnapshot = true
		}
		if event.Type == interpreter.DebugSessionEventHalted {
			sawHalt = true
			if event.StopReason != interpreter.DebugStopWatchpoint {
				t.Fatalf("expected halted event stop reason watchpoint, got %q", event.StopReason)
			}
		}
	}
	if !sawSnapshot || !sawHalt {
		t.Fatalf("expected snapshot and halt events, got %#v", events)
	}
	session.ClearHit()
	if session.State != interpreter.DebugSessionPaused || session.Debugger.Hit != nil {
		t.Fatalf("expected ClearHit to leave session paused without hit")
	}
}

func TestDebuggerExpressionSupportsNumericComparisons(t *testing.T) {
	src := `def run() -> i64:
    hp: mutable i64 = 2
    hp <- 1
    hp <- 0
    return hp
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_debugger_numeric.elisa", src)
	debugger := interpreter.NewDebugger(interpreter.BreakWhenExpr("(hp <= 0)"))
	_, err := interpreter.Execute(result, interpreter.Options{Entry: "run", Debugger: debugger})
	var halt *interpreter.DebugHaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected numeric condition halt, got %v", err)
	}
	current, ok := debugger.Current()
	if !ok {
		t.Fatalf("expected current snapshot")
	}
	if got, ok := current.LookupString("hp"); !ok || got != "0" {
		t.Fatalf("expected hp 0 at halt, got %q ok=%v", got, ok)
	}
}

func TestDebuggerExpressionBreakpointAndFormats(t *testing.T) {
	src := `struct Player:
    dead: mutable bool

def run() -> i64:
    player: Player = Player(false)
    player.dead <- true
    return 0
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_debugger_expr.elisa", src)
	debugger := interpreter.NewDebuggerWithConfig(interpreter.DebuggerConfig{FullTrace: true}, interpreter.BreakWhenExpr("player.dead == true"))
	_, err := interpreter.Execute(result, interpreter.Options{Entry: "run", Debugger: debugger})
	var halt *interpreter.DebugHaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected debugger halt, got %v", err)
	}
	human := interpreter.FormatDebugHaltHuman(debugger)
	for _, want := range []string{"[ debugger ] halted: player.dead == true", "player.dead:", "false -> true"} {
		if !strings.Contains(human, want) {
			t.Fatalf("expected human format to contain %q, got:\n%s", want, human)
		}
	}
	jsonl, err := interpreter.FormatDebugTraceJSONL(debugger)
	if err != nil {
		t.Fatalf("FormatDebugTraceJSONL returned error: %v", err)
	}
	if !strings.Contains(jsonl, `"schema_version":"elisacore-debug-v1"`) || !strings.Contains(jsonl, `"record_type":"halt"`) {
		t.Fatalf("expected schema-versioned halt JSONL, got:\n%s", jsonl)
	}
	for _, line := range strings.Split(strings.TrimSpace(jsonl), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
	}
	llm := interpreter.FormatDebugContextForLLM(debugger, 1)
	if !strings.Contains(llm, "halt condition=\"player.dead == true\"") || !strings.Contains(llm, "diff player.dead:") {
		t.Fatalf("expected bounded LLM context with halt and diff, got:\n%s", llm)
	}
}

func TestDebuggerExpressionConditionErrorsAreStructured(t *testing.T) {
	src := `def run() -> i64:
    value: i64 = 1
    return value
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_debugger_bad_expr.elisa", src)
	debugger := interpreter.NewDebugger(interpreter.BreakWhenExpr("missing.value == true"))
	_, err := interpreter.Execute(result, interpreter.Options{Entry: "run", Debugger: debugger})
	var conditionErr *interpreter.DebugConditionError
	if !errors.As(err, &conditionErr) {
		t.Fatalf("expected DebugConditionError, got %v", err)
	}
	if !strings.Contains(conditionErr.Error(), "missing.value") {
		t.Fatalf("expected condition text in error, got %q", conditionErr.Error())
	}
}

func TestDebuggerExpressionConditionWatchesLateDeclaredLocal(t *testing.T) {
	// The watched variable c is declared partway through the function. The condition must
	// halt when c comes into scope and reaches the value -- not abort on the earlier
	// snapshots where c does not exist yet.
	src := `def run() -> i64:
    a: i64 = 1
    b: i64 = 2
    c: i64 = 7
    return c
`
	result := parseAndAnalyzeInterpreterTest(t, "interpreter_debugger_late_local.elisa", src)
	debugger := interpreter.NewDebugger(interpreter.BreakWhenExpr("c == 7"))
	_, err := interpreter.Execute(result, interpreter.Options{Entry: "run", Debugger: debugger})
	var halt *interpreter.DebugHaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected debugger to halt on the late-declared local c, got %v", err)
	}
}

func TestDebuggerExpressionConditionSupportsHexAndArithmetic(t *testing.T) {
	src := `def run() -> i64:
    addr: i64 = 3840
    return addr
`
	for _, expr := range []string{
		"addr == 0xF00",         // hex literal
		"addr == 0xE00 + 0x100", // hex arithmetic (0xF00)
		"addr == 3836 + 4",      // decimal arithmetic
		"addr > 0xEFF",          // hex in an ordered comparison
	} {
		result := parseAndAnalyzeInterpreterTest(t, "interpreter_debugger_arith.elisa", src)
		debugger := interpreter.NewDebugger(interpreter.BreakWhenExpr(expr))
		_, err := interpreter.Execute(result, interpreter.Options{Entry: "run", Debugger: debugger})
		var halt *interpreter.DebugHaltError
		if !errors.As(err, &halt) {
			t.Fatalf("expected halt for condition %q, got %v", expr, err)
		}
	}
}

func TestDebuggerBreaksOnRaise(t *testing.T) {
	src := `error MyError:
    Bad

def run() -> i64 error[MyError]:
    x: mutable i64 = 5
    x <- 7
    raise MyError.Bad
    return x
`
	result := parseAndAnalyzeInterpreterTest(t, "interpreter_debugger_raise.elisa", src)
	debugger := interpreter.NewDebuggerWithConfig(interpreter.DebuggerConfig{BreakOnRaise: true})
	_, err := interpreter.Execute(result, interpreter.Options{Entry: "run", Debugger: debugger})
	var halt *interpreter.DebugHaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected break-on-raise halt, got %v", err)
	}
	if halt.Hit.StopReason != interpreter.DebugStopRaise {
		t.Fatalf("expected raise stop reason, got %q", halt.Hit.StopReason)
	}
	current, ok := debugger.Current()
	if !ok {
		t.Fatalf("expected current snapshot at the raise")
	}
	if got, ok := current.LookupString("raised"); !ok || got != "MyError.Bad" {
		t.Fatalf("expected raised=MyError.Bad, got %q ok=%v", got, ok)
	}
	if got, ok := current.LookupString("x"); !ok || got != "7" {
		t.Fatalf("expected x=7 captured at the raise, got %q ok=%v", got, ok)
	}
	if _, ok := debugger.StepBack(); !ok {
		t.Fatalf("expected to step backward from the raise to find the cause")
	}
}

func TestDebugSessionSourceStepCommands(t *testing.T) {
	src := `def inner(value: i64) -> i64:
    next: i64 = value + 1
    return next

def run() -> i64:
    first: i64 = 1
    second: i64 = inner(first)
    third: i64 = second + 1
    return third
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_debugger_steps.elisa", src)
	debugger := interpreter.NewDebuggerWithConfig(interpreter.DebuggerConfig{FullTrace: true})
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run", Debugger: debugger})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "3" {
		t.Fatalf("expected result 3, got %s", got)
	}
	session := debugger.Session
	var beforeCallIndex int
	for index, snapshot := range debugger.Trace {
		if snapshot.Event == interpreter.DebugBeforeStmt && snapshot.Function == "run" && snapshot.StatementKind == "VarDeclStmt" {
			if _, ok := snapshot.LookupString("first"); ok {
				beforeCallIndex = index
				debugger.Seek(index)
				break
			}
		}
	}
	if beforeCallIndex == 0 {
		t.Fatalf("expected to find call declaration snapshot")
	}
	next := session.NextSourceStep()
	if !next.OK || next.Snapshot == nil || next.Snapshot.Function != "run" {
		t.Fatalf("expected next to stay in run, got %#v", next)
	}
	debugger.Seek(beforeCallIndex)
	step := session.StepIntoSource()
	if !step.OK || step.Snapshot == nil || step.Snapshot.Function != "inner" {
		t.Fatalf("expected step to enter inner, got %#v", step)
	}
	for index, snapshot := range debugger.Trace {
		if snapshot.Event == interpreter.DebugAfterStmt && snapshot.Function == "inner" {
			debugger.Seek(index)
			break
		}
	}
	out := session.StepOutSource()
	if !out.OK || out.Snapshot == nil || out.Snapshot.Function != "run" {
		t.Fatalf("expected out to return to run, got %#v", out)
	}
}

func TestDebugREPLCommandsAndTraceImport(t *testing.T) {
	src := `struct Player:
    dead: mutable bool

def run() -> i64:
    player: Player = Player(false)
    player.dead <- true
    return 0
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_debugger_repl.elisa", src)
	session := interpreter.NewDebugSession(interpreter.DebuggerConfig{FullTrace: true}, interpreter.BreakWhenExpr("player.dead == true"))
	_, err := interpreter.Execute(result, interpreter.Options{Entry: "run", Debugger: session.Debugger})
	if err == nil {
		t.Fatalf("expected debug halt")
	}
	printResult, quit := interpreter.ExecuteDebugREPLCommand(session, "print player.dead", 1)
	if quit || !printResult.OK || !strings.Contains(printResult.Output, "player.dead = true") {
		t.Fatalf("unexpected print result %#v quit=%v", printResult, quit)
	}
	watchResult, _ := interpreter.ExecuteDebugREPLCommand(session, "watch player.dead", 1)
	if !watchResult.OK || !strings.Contains(watchResult.Output, "watchpoint") {
		t.Fatalf("unexpected watch result %#v", watchResult)
	}
	invalidResult, _ := interpreter.ExecuteDebugREPLCommand(session, "wat", 1)
	if invalidResult.OK || !strings.Contains(invalidResult.Error, "unknown command") {
		t.Fatalf("expected readable invalid command, got %#v", invalidResult)
	}
	payload, err := interpreter.ExportDebugTrace(session.Debugger)
	if err != nil {
		t.Fatalf("ExportDebugTrace returned error: %v", err)
	}
	imported, err := interpreter.ImportDebugTrace(payload)
	if err != nil {
		t.Fatalf("ImportDebugTrace returned error: %v", err)
	}
	if got, ok := imported.Session.Inspect("player.dead"); !ok || got != "true" {
		t.Fatalf("expected imported trace inspection to see player.dead true, got %q ok=%v", got, ok)
	}
	if _, ok := imported.StepBack(); !ok {
		t.Fatalf("expected imported trace to support step back")
	}
	resume := imported.Session.ContinueUntilBreak()
	if resume.OK || resume.StopReason != interpreter.DebugStopReplayOnly {
		t.Fatalf("expected imported trace resume to be replay-only, got %#v", resume)
	}
}
