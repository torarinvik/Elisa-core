package interpreter

import (
	"elisacore/src/lexer"
	"fmt"
	"strings"
)

const DebugSchemaVersion = "elisacore-debug-v1"

type DebugEvent string

const (
	DebugBeforeStmt DebugEvent = "before-stmt"
	DebugAfterStmt  DebugEvent = "after-stmt"
	DebugRaise      DebugEvent = "raise"
)

type DebugSessionState string

const (
	DebugSessionReady     DebugSessionState = "ready"
	DebugSessionRunning   DebugSessionState = "running"
	DebugSessionPaused    DebugSessionState = "paused"
	DebugSessionHalted    DebugSessionState = "halted"
	DebugSessionCompleted DebugSessionState = "completed"
	DebugSessionFailed    DebugSessionState = "failed"
)

type DebugStopReason string

const (
	DebugStopNone           DebugStopReason = ""
	DebugStopBreakpoint     DebugStopReason = "breakpoint-hit"
	DebugStopWatchpoint     DebugStopReason = "watchpoint-hit"
	DebugStopManualPause    DebugStopReason = "manual-pause"
	DebugStopEndOfProgram   DebugStopReason = "end-of-program"
	DebugStopRuntimeError   DebugStopReason = "runtime-error"
	DebugStopConditionError DebugStopReason = "condition-error"
	DebugStopRaise          DebugStopReason = "raise"
	DebugStopTraceExpired   DebugStopReason = "trace-window-expired"
	DebugStopReplayOnly     DebugStopReason = "replay-only"
)

type DebugSessionEventType string

const (
	DebugSessionEventExecutionStarted   DebugSessionEventType = "execution-started"
	DebugSessionEventSnapshotAdded      DebugSessionEventType = "snapshot-added"
	DebugSessionEventHalted             DebugSessionEventType = "halted"
	DebugSessionEventCursorChanged      DebugSessionEventType = "cursor-changed"
	DebugSessionEventBreakpointHit      DebugSessionEventType = "breakpoint-hit"
	DebugSessionEventExecutionCompleted DebugSessionEventType = "execution-completed"
	DebugSessionEventExecutionFailed    DebugSessionEventType = "execution-failed"
)

type DebugCondition struct {
	ID         int
	Name       string
	Expr       string
	Enabled    bool
	Watchpoint bool
	Predicate  func(DebugSnapshot) bool
}

type DebugHit struct {
	Condition   string
	ConditionID int
	StopReason  DebugStopReason
	Snapshot    DebugSnapshot
}

type DebugHaltError struct {
	Hit DebugHit
}

func (e *DebugHaltError) Error() string {
	if e == nil {
		return "debugger halted"
	}
	pos := e.Hit.Snapshot.Position
	if pos.IsZero() {
		return fmt.Sprintf("debugger halted: %s", e.Hit.Condition)
	}
	return fmt.Sprintf("debugger halted: %s at %s", e.Hit.Condition, pos)
}

type DebugSnapshot struct {
	SchemaVersion string
	Index         int
	Step          int64
	Event         DebugEvent
	ThreadID      string
	Function      string
	CallStack     []DebugCallFrame
	Position      lexer.Pos
	StatementKind string
	Locals        map[string]Value
	Globals       map[string]Value
	Diff          []DebugValueDiff
	PC            string
	Instruction   string
	Registers     map[string]string
	MemoryReads   []DebugMemoryAccess
	MemoryWrites  []DebugMemoryAccess
}

type DebugCallFrame struct {
	Function string `json:"function"`
	Source   string `json:"source,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Active   bool   `json:"active,omitempty"`
}

type DebugValueDiff struct {
	Path  string `json:"path"`
	Old   string `json:"old"`
	New   string `json:"new"`
	Kind  string `json:"kind,omitempty"`
	Scope string `json:"scope,omitempty"`
}

type DebugMemoryAccess struct {
	Address string `json:"address"`
	Size    int    `json:"size,omitempty"`
	Value   string `json:"value,omitempty"`
}

func (s DebugSnapshot) Lookup(path string) (Value, bool) {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) == 0 || parts[0] == "" {
		return VoidValue(), false
	}
	value, ok := s.Locals[parts[0]]
	if !ok {
		value, ok = s.Globals[parts[0]]
	}
	if !ok {
		return VoidValue(), false
	}
	for _, field := range parts[1:] {
		field = strings.TrimSpace(field)
		if field == "" {
			return VoidValue(), false
		}
		value = derefValue(value)
		if value.kind != valueStruct || value.structVal == nil {
			return VoidValue(), false
		}
		next, ok := value.structVal.Fields[field]
		if !ok {
			return VoidValue(), false
		}
		value = next
	}
	return value.Clone(), true
}

func (s DebugSnapshot) LookupString(path string) (string, bool) {
	value, ok := s.Lookup(path)
	if !ok {
		return "", false
	}
	return value.String(), true
}

func (s DebugSnapshot) DisplaySource() string {
	if s.Position.IsZero() {
		return "<unknown>"
	}
	return s.Position.String()
}

func (s DebugSnapshot) DebugLocals() map[string]string {
	return debugStringMap(s.Locals)
}

func (s DebugSnapshot) DebugGlobals() map[string]string {
	return debugStringMap(s.Globals)
}

type DebuggerConfig struct {
	TraceLimit int
	FullTrace  bool
	Context    int
	// BreakOnRaise halts the debugger at every `raise` site, so an error returned through an
	// error union can be inspected -- and stepped backward from -- the way an exception
	// breakpoint would, without `raise` being an exception.
	BreakOnRaise bool
}

type Debugger struct {
	Trace      []DebugSnapshot
	Conditions []DebugCondition
	Hit        *DebugHit
	Session    *DebugSession
	Config     DebuggerConfig
	Dropped    int64
	ReplayOnly bool
	nextStep   int64
	cursor     int
	// conditionResolvable maps a break-condition expression to whether its operands have
	// ever been in scope during the run. An expression that was only ever not-in-scope (a
	// typo, or a reference to a never-declared variable) is surfaced as a DebugConditionError
	// at completion via deferredConditionError, while a late-declared local resolves and
	// evaluates normally.
	conditionResolvable map[string]bool
}

func (d *Debugger) markConditionUnresolved(expr string) {
	if d.conditionResolvable == nil {
		d.conditionResolvable = map[string]bool{}
	}
	if _, seen := d.conditionResolvable[expr]; !seen {
		d.conditionResolvable[expr] = false
	}
}

func (d *Debugger) markConditionResolvable(expr string) {
	if d.conditionResolvable == nil {
		d.conditionResolvable = map[string]bool{}
	}
	d.conditionResolvable[expr] = true
}

// deferredConditionError returns a structured error for any enabled break condition whose
// operand path was never in scope across the entire run (and which therefore never halted).
// This preserves typo detection while allowing watches on variables declared partway
// through a function.
func (d *Debugger) deferredConditionError() *DebugConditionError {
	if d == nil || d.Hit != nil {
		return nil
	}
	for _, condition := range d.Conditions {
		if !condition.Enabled {
			continue
		}
		expr := strings.TrimSpace(condition.Expr)
		if expr == "" {
			continue
		}
		if resolvable, seen := d.conditionResolvable[expr]; seen && !resolvable {
			if d.Session != nil {
				d.Session.State = DebugSessionFailed
			}
			return &DebugConditionError{Condition: expr, Err: errDebugOperandNotInScope}
		}
	}
	return nil
}

func NewDebugger(conditions ...DebugCondition) *Debugger {
	session := NewDebugSession(DebuggerConfig{}, conditions...)
	return session.Debugger
}

func NewDebuggerWithConfig(config DebuggerConfig, conditions ...DebugCondition) *Debugger {
	session := NewDebugSession(config, conditions...)
	return session.Debugger
}

func BreakWhenPathEquals(path string, expected string) DebugCondition {
	name := strings.TrimSpace(path) + " == " + expected
	return DebugCondition{
		Name:    name,
		Enabled: true,
		Predicate: func(snapshot DebugSnapshot) bool {
			got, ok := snapshot.LookupString(path)
			return ok && got == expected
		},
	}
}

func BreakWhenPathBool(path string, expected bool) DebugCondition {
	if expected {
		return BreakWhenPathEquals(path, "true")
	}
	return BreakWhenPathEquals(path, "false")
}

func BreakWhenExpr(expr string) DebugCondition {
	expr = strings.TrimSpace(expr)
	return DebugCondition{
		Name:    expr,
		Expr:    expr,
		Enabled: true,
	}
}

func BreakWhenPathChanges(path string) DebugCondition {
	path = strings.TrimSpace(path)
	return DebugCondition{
		Name:       path + " changed",
		Enabled:    true,
		Watchpoint: true,
		Predicate: func(snapshot DebugSnapshot) bool {
			for _, diff := range snapshot.Diff {
				if diff.Path == path {
					return true
				}
			}
			return false
		},
	}
}

func (d *Debugger) Reset() {
	if d == nil {
		return
	}
	d.Trace = nil
	d.Hit = nil
	d.Dropped = 0
	d.nextStep = 0
	d.cursor = -1
	d.conditionResolvable = nil
	if d.Session != nil {
		d.Session.State = DebugSessionReady
		d.Session.Events = nil
	}
}

func (d *Debugger) Current() (DebugSnapshot, bool) {
	if d == nil || d.cursor < 0 || d.cursor >= len(d.Trace) {
		return DebugSnapshot{}, false
	}
	return d.Trace[d.cursor], true
}

func (d *Debugger) StepBack() (DebugSnapshot, bool) {
	if d == nil || d.cursor <= 0 || d.cursor > len(d.Trace) {
		return DebugSnapshot{}, false
	}
	d.cursor--
	d.emitSessionEvent(DebugSessionEventCursorChanged, d.Trace[d.cursor], nil)
	return d.Trace[d.cursor], true
}

func (d *Debugger) StepForward() (DebugSnapshot, bool) {
	if d == nil || d.cursor < -1 || d.cursor+1 >= len(d.Trace) {
		return DebugSnapshot{}, false
	}
	d.cursor++
	d.emitSessionEvent(DebugSessionEventCursorChanged, d.Trace[d.cursor], nil)
	return d.Trace[d.cursor], true
}

func (d *Debugger) Seek(index int) (DebugSnapshot, bool) {
	if d == nil || index < 0 || index >= len(d.Trace) {
		return DebugSnapshot{}, false
	}
	d.cursor = index
	d.emitSessionEvent(DebugSessionEventCursorChanged, d.Trace[d.cursor], nil)
	return d.Trace[d.cursor], true
}
