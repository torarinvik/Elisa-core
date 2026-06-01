package interpreter

import (
	"bufio"
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
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

type DebugSession struct {
	State         DebugSessionState
	Debugger      *Debugger
	Events        []DebugSessionEvent
	NextBreakID   int
	EventHandlers []func(DebugSessionEvent)
}

type DebugSessionEvent struct {
	SchemaVersion string                `json:"schema_version"`
	Type          DebugSessionEventType `json:"type"`
	Step          int64                 `json:"step,omitempty"`
	StopReason    DebugStopReason       `json:"stop_reason,omitempty"`
	Snapshot      *DebugSnapshotRecord  `json:"snapshot,omitempty"`
	Hit           *DebugHitRecord       `json:"hit,omitempty"`
	Error         string                `json:"error,omitempty"`
}

type DebugCommandResult struct {
	SchemaVersion string               `json:"schema_version"`
	Command       string               `json:"command"`
	OK            bool                 `json:"ok"`
	Step          int64                `json:"step,omitempty"`
	StopReason    DebugStopReason      `json:"stop_reason,omitempty"`
	Snapshot      *DebugSnapshotRecord `json:"snapshot,omitempty"`
	Diff          []DebugValueDiff     `json:"diff,omitempty"`
	Output        string               `json:"output,omitempty"`
	Error         string               `json:"error,omitempty"`
}

func NewDebugSession(config DebuggerConfig, conditions ...DebugCondition) *DebugSession {
	if config.TraceLimit == 0 && !config.FullTrace {
		config.TraceLimit = 10_000
	}
	session := &DebugSession{State: DebugSessionReady}
	debugger := &Debugger{Config: config, cursor: -1, Session: session}
	session.Debugger = debugger
	for _, condition := range conditions {
		session.SetBreakpoint(condition)
	}
	return session
}

func (s *DebugSession) Run() {
	if s != nil {
		s.State = DebugSessionRunning
		s.emit(DebugSessionEvent{Type: DebugSessionEventExecutionStarted})
	}
}

func (s *DebugSession) Pause() {
	if s != nil {
		s.State = DebugSessionPaused
	}
}

func (s *DebugSession) Continue() {
	if s != nil {
		s.State = DebugSessionRunning
	}
}

func (s *DebugSession) ContinueUntilBreak() DebugCommandResult {
	if s == nil || s.Debugger == nil {
		return debugCommandError("continue", DebugStopRuntimeError, "missing debug session")
	}
	s.ClearHit()
	if s.Debugger.ReplayOnly {
		return s.commandResult("continue", false, DebugStopReplayOnly, "imported traces are replay-only and cannot resume execution")
	}
	s.State = DebugSessionCompleted
	if len(s.Debugger.Trace) != 0 {
		s.Debugger.cursor = len(s.Debugger.Trace) - 1
	}
	return s.commandResult("continue", true, DebugStopEndOfProgram, "")
}

func (s *DebugSession) NextSourceStep() DebugCommandResult {
	return s.seekNextSourceStep("next", false, false)
}

func (s *DebugSession) StepIntoSource() DebugCommandResult {
	return s.seekNextSourceStep("step", true, false)
}

func (s *DebugSession) StepOutSource() DebugCommandResult {
	return s.seekNextSourceStep("out", true, true)
}

func (s *DebugSession) OnEvent(handler func(DebugSessionEvent)) {
	if s == nil || handler == nil {
		return
	}
	s.EventHandlers = append(s.EventHandlers, handler)
}

func (s *DebugSession) Breakpoints() []DebugCondition {
	if s == nil || s.Debugger == nil {
		return nil
	}
	return append([]DebugCondition(nil), s.Debugger.Conditions...)
}

func (s *DebugSession) StepBack() (DebugSnapshot, bool) {
	if s == nil || s.Debugger == nil {
		return DebugSnapshot{}, false
	}
	return s.Debugger.StepBack()
}

func (s *DebugSession) BackCommand() DebugCommandResult {
	if s == nil || s.Debugger == nil {
		return debugCommandError("back", DebugStopRuntimeError, "missing debug session")
	}
	if _, ok := s.Debugger.StepBack(); !ok {
		return s.commandResult("back", false, DebugStopTraceExpired, "no earlier trace event is available")
	}
	return s.commandResult("back", true, DebugStopManualPause, "")
}

func (s *DebugSession) ForwardCommand() DebugCommandResult {
	if s == nil || s.Debugger == nil {
		return debugCommandError("forward", DebugStopRuntimeError, "missing debug session")
	}
	if _, ok := s.Debugger.StepForward(); !ok {
		return s.commandResult("forward", false, DebugStopEndOfProgram, "no later trace event is available")
	}
	return s.commandResult("forward", true, DebugStopManualPause, "")
}

func (s *DebugSession) StepForward() (DebugSnapshot, bool) {
	if s == nil || s.Debugger == nil {
		return DebugSnapshot{}, false
	}
	return s.Debugger.StepForward()
}

func (s *DebugSession) Seek(step int) (DebugSnapshot, bool) {
	if s == nil || s.Debugger == nil {
		return DebugSnapshot{}, false
	}
	for index, snapshot := range s.Debugger.Trace {
		if int(snapshot.Step) == step || snapshot.Index == step {
			return s.Debugger.Seek(index)
		}
	}
	return DebugSnapshot{}, false
}

func (s *DebugSession) SeekCommand(step int) DebugCommandResult {
	if _, ok := s.Seek(step); !ok {
		return s.commandResult("seek", false, DebugStopTraceExpired, fmt.Sprintf("step %d is not in the current trace window", step))
	}
	return s.commandResult("seek", true, DebugStopManualPause, "")
}

func (s *DebugSession) SetBreakpoint(condition DebugCondition) int {
	if s == nil || s.Debugger == nil {
		return 0
	}
	s.NextBreakID++
	condition.ID = s.NextBreakID
	if !condition.Enabled {
		condition.Enabled = true
	}
	if condition.Name == "" {
		condition.Name = fmt.Sprintf("breakpoint-%d", s.NextBreakID)
	}
	s.Debugger.Conditions = append(s.Debugger.Conditions, condition)
	return s.NextBreakID
}

func (s *DebugSession) RemoveBreakpoint(id int) bool {
	if s == nil || s.Debugger == nil || id <= 0 {
		return false
	}
	for index, condition := range s.Debugger.Conditions {
		if condition.ID == id {
			s.Debugger.Conditions = append(s.Debugger.Conditions[:index], s.Debugger.Conditions[index+1:]...)
			return true
		}
	}
	return false
}

func (s *DebugSession) SetBreakpointEnabled(id int, enabled bool) bool {
	if s == nil || s.Debugger == nil || id <= 0 {
		return false
	}
	for index := range s.Debugger.Conditions {
		if s.Debugger.Conditions[index].ID == id {
			s.Debugger.Conditions[index].Enabled = enabled
			return true
		}
	}
	return false
}

func (s *DebugSession) ClearHit() {
	if s == nil || s.Debugger == nil {
		return
	}
	s.Debugger.Hit = nil
	if s.State == DebugSessionHalted {
		s.State = DebugSessionPaused
	}
}

func (s *DebugSession) MarkCompleted() {
	if s == nil {
		return
	}
	s.State = DebugSessionCompleted
	var snapshot DebugSnapshot
	if s.Debugger != nil {
		snapshot, _ = s.Debugger.Current()
	}
	s.emit(DebugSessionEvent{Type: DebugSessionEventExecutionCompleted, StopReason: DebugStopEndOfProgram, Step: snapshot.Step})
}

func (s *DebugSession) MarkFailed(err error, reason DebugStopReason) {
	if s == nil {
		return
	}
	s.State = DebugSessionFailed
	message := ""
	if err != nil {
		message = err.Error()
	}
	var snapshot DebugSnapshot
	if s.Debugger != nil {
		snapshot, _ = s.Debugger.Current()
	}
	s.emit(DebugSessionEvent{Type: DebugSessionEventExecutionFailed, StopReason: reason, Step: snapshot.Step, Error: message})
}

func (s *DebugSession) Inspect(path string) (string, bool) {
	if s == nil || s.Debugger == nil {
		return "", false
	}
	snapshot, ok := s.Debugger.Current()
	if !ok {
		for _, diff := range snapshot.Diff {
			if diff.Path == path {
				return diff.New, true
			}
		}
		if s.Debugger.Hit != nil {
			for _, diff := range s.Debugger.Hit.Snapshot.Diff {
				if diff.Path == path {
					return diff.New, true
				}
			}
		}
		return "", false
	}
	return snapshot.LookupString(path)
}

func (s *DebugSession) ListLocals() map[string]string {
	if snapshot, ok := s.currentSnapshot(); ok {
		return snapshot.DebugLocals()
	}
	return nil
}

func (s *DebugSession) ListGlobals() map[string]string {
	if snapshot, ok := s.currentSnapshot(); ok {
		return snapshot.DebugGlobals()
	}
	return nil
}

func (s *DebugSession) ListCallStack() []DebugCallFrame {
	if snapshot, ok := s.currentSnapshot(); ok {
		return append([]DebugCallFrame(nil), snapshot.CallStack...)
	}
	return nil
}

func (s *DebugSession) ListDiff() []DebugValueDiff {
	if snapshot, ok := s.currentSnapshot(); ok {
		return append([]DebugValueDiff(nil), snapshot.Diff...)
	}
	return nil
}

func (s *DebugSession) currentSnapshot() (DebugSnapshot, bool) {
	if s == nil || s.Debugger == nil {
		return DebugSnapshot{}, false
	}
	return s.Debugger.Current()
}

func (s *DebugSession) seekNextSourceStep(command string, allowEnter bool, stepOut bool) DebugCommandResult {
	if s == nil || s.Debugger == nil {
		return debugCommandError(command, DebugStopRuntimeError, "missing debug session")
	}
	current, ok := s.Debugger.Current()
	if !ok {
		return s.commandResult(command, false, DebugStopTraceExpired, "no current trace event")
	}
	currentDepth := len(current.CallStack)
	currentFunction := current.Function
	for index := s.Debugger.cursor + 1; index < len(s.Debugger.Trace); index++ {
		candidate := s.Debugger.Trace[index]
		if candidate.Event != DebugAfterStmt {
			continue
		}
		depth := len(candidate.CallStack)
		if stepOut {
			if depth < currentDepth || candidate.Function != currentFunction {
				s.Debugger.Seek(index)
				return s.commandResult(command, true, DebugStopManualPause, "")
			}
			continue
		}
		if allowEnter || candidate.Function == currentFunction {
			s.Debugger.Seek(index)
			return s.commandResult(command, true, DebugStopManualPause, "")
		}
	}
	return s.commandResult(command, false, DebugStopEndOfProgram, "no matching source step is available")
}

func (s *DebugSession) commandResult(command string, ok bool, reason DebugStopReason, message string) DebugCommandResult {
	result := DebugCommandResult{SchemaVersion: DebugSchemaVersion, Command: command, OK: ok, StopReason: reason}
	if ok {
		result.Output = message
	} else {
		result.Error = message
	}
	if snapshot, hasSnapshot := s.currentSnapshot(); hasSnapshot {
		record := snapshot.Record()
		result.Step = snapshot.Step
		result.Snapshot = &record
		result.Diff = append([]DebugValueDiff(nil), snapshot.Diff...)
	}
	return result
}

func debugCommandError(command string, reason DebugStopReason, message string) DebugCommandResult {
	return DebugCommandResult{SchemaVersion: DebugSchemaVersion, Command: command, OK: false, StopReason: reason, Error: message}
}

func (i *Interpreter) debugRecord(frame *frame, event DebugEvent, stmt ast.Stmt) error {
	if i == nil || i.debugger == nil || stmt == nil {
		return nil
	}
	snapshot := DebugSnapshot{
		SchemaVersion: DebugSchemaVersion,
		Index:         len(i.debugger.Trace),
		Step:          i.debugger.nextStep,
		Event:         event,
		ThreadID:      "main",
		Function:      debugFrameFunction(frame),
		CallStack:     debugCallStack(frame, stmt),
		Position:      stmt.Pos(),
		StatementKind: debugStatementKind(stmt),
		Locals:        debugCloneVisibleLocals(frame),
		Globals:       debugCloneValues(i.globals),
		Registers:     map[string]string{},
	}
	i.debugger.nextStep++
	if previous, ok := i.debugger.lastSnapshot(); ok {
		snapshot.Diff = debugDiffSnapshots(previous, snapshot)
	}
	i.debugger.appendSnapshot(snapshot)
	if event != DebugAfterStmt || i.debugger.Hit != nil {
		return nil
	}
	for _, condition := range i.debugger.Conditions {
		if !condition.Enabled {
			continue
		}
		var matched bool
		if expr := strings.TrimSpace(condition.Expr); expr != "" {
			var err error
			matched, err = evalDebugConditionExpr(snapshot, expr)
			if err != nil {
				if err == errDebugOperandNotInScope {
					// Operand not in scope at this snapshot; record it and keep stepping
					// rather than aborting. If it is never in scope, deferredConditionError
					// reports it at completion.
					i.debugger.markConditionUnresolved(expr)
					continue
				}
				if i.debugger.Session != nil {
					i.debugger.Session.State = DebugSessionFailed
				}
				return &DebugConditionError{Condition: expr, Err: err}
			}
			i.debugger.markConditionResolvable(expr)
		} else if condition.Predicate != nil {
			matched = condition.Predicate(snapshot)
		} else {
			continue
		}
		if !matched {
			continue
		}
		reason := DebugStopBreakpoint
		if condition.Watchpoint {
			reason = DebugStopWatchpoint
		}
		hit := DebugHit{Condition: condition.Name, ConditionID: condition.ID, StopReason: reason, Snapshot: snapshot}
		i.debugger.Hit = &hit
		if i.debugger.Session != nil {
			i.debugger.Session.State = DebugSessionHalted
			i.debugger.emitSessionEvent(DebugSessionEventBreakpointHit, snapshot, &hit)
			i.debugger.emitSessionEvent(DebugSessionEventHalted, snapshot, &hit)
		}
		return &DebugHaltError{Hit: hit}
	}
	return nil
}

type DebugConditionError struct {
	Condition string
	Err       error
}

func (e *DebugConditionError) Error() string {
	return fmt.Sprintf("debug condition %q failed: %v", e.Condition, e.Err)
}

func (d *Debugger) lastSnapshot() (DebugSnapshot, bool) {
	if d == nil || len(d.Trace) == 0 {
		return DebugSnapshot{}, false
	}
	return d.Trace[len(d.Trace)-1], true
}

func (d *Debugger) appendSnapshot(snapshot DebugSnapshot) {
	if d == nil {
		return
	}
	limit := d.Config.TraceLimit
	if d.Config.FullTrace || limit <= 0 {
		d.Trace = append(d.Trace, snapshot)
		d.cursor = len(d.Trace) - 1
		d.emitSessionEvent(DebugSessionEventSnapshotAdded, snapshot, nil)
		return
	}
	if len(d.Trace) >= limit {
		copy(d.Trace, d.Trace[1:])
		d.Trace[len(d.Trace)-1] = snapshot
		d.Dropped++
	} else {
		d.Trace = append(d.Trace, snapshot)
	}
	for index := range d.Trace {
		d.Trace[index].Index = int(d.Dropped) + index
	}
	d.cursor = len(d.Trace) - 1
	d.emitSessionEvent(DebugSessionEventSnapshotAdded, snapshot, nil)
}

func (d *Debugger) emitSessionEvent(eventType DebugSessionEventType, snapshot DebugSnapshot, hit *DebugHit) {
	if d == nil || d.Session == nil {
		return
	}
	record := DebugSessionEvent{SchemaVersion: DebugSchemaVersion, Type: eventType, Step: snapshot.Step}
	snapshotRecord := snapshot.Record()
	record.Snapshot = &snapshotRecord
	if hit != nil {
		hitRecord := hit.Record()
		record.Hit = &hitRecord
		record.StopReason = hit.StopReason
	}
	d.Session.emit(record)
}

func (s *DebugSession) emit(record DebugSessionEvent) {
	if s == nil {
		return
	}
	if record.SchemaVersion == "" {
		record.SchemaVersion = DebugSchemaVersion
	}
	s.Events = append(s.Events, record)
	for _, handler := range s.EventHandlers {
		handler(record)
	}
}

// debugRaise records a snapshot at a `raise` site (exposing the raised value as a synthetic
// local "raised") so it joins the time-travel trace, and halts when BreakOnRaise is set --
// giving an error returned through an error union the same step-backward-to-find-the-cause
// power an exception breakpoint would, without raise being an exception.
func (i *Interpreter) debugRaise(frame *frame, expr *ast.RaiseExpr, raised Value) error {
	if i == nil || i.debugger == nil {
		return nil
	}
	locals := debugCloneVisibleLocals(frame)
	locals["raised"] = raised.Clone()
	snapshot := DebugSnapshot{
		SchemaVersion: DebugSchemaVersion,
		Index:         len(i.debugger.Trace),
		Step:          i.debugger.nextStep,
		Event:         DebugRaise,
		ThreadID:      "main",
		Function:      debugFrameFunction(frame),
		CallStack:     debugCallStack(frame, nil),
		Position:      expr.Pos(),
		StatementKind: "RaiseExpr",
		Locals:        locals,
		Globals:       debugCloneValues(i.globals),
		Registers:     map[string]string{},
	}
	i.debugger.nextStep++
	if previous, ok := i.debugger.lastSnapshot(); ok {
		snapshot.Diff = debugDiffSnapshots(previous, snapshot)
	}
	i.debugger.appendSnapshot(snapshot)
	if !i.debugger.Config.BreakOnRaise || i.debugger.Hit != nil {
		return nil
	}
	hit := DebugHit{Condition: "raise", StopReason: DebugStopRaise, Snapshot: snapshot}
	i.debugger.Hit = &hit
	if i.debugger.Session != nil {
		i.debugger.Session.State = DebugSessionHalted
		i.debugger.emitSessionEvent(DebugSessionEventBreakpointHit, snapshot, &hit)
		i.debugger.emitSessionEvent(DebugSessionEventHalted, snapshot, &hit)
	}
	return &DebugHaltError{Hit: hit}
}

func debugFrameFunction(frame *frame) string {
	for current := frame; current != nil; current = current.parent {
		if current.function != "" {
			return current.function
		}
	}
	return ""
}

func debugCallStack(frame *frame, stmt ast.Stmt) []DebugCallFrame {
	frames := []DebugCallFrame{}
	for current := frame; current != nil; current = current.parent {
		if current.function == "" {
			continue
		}
		item := DebugCallFrame{Function: current.function}
		if stmt != nil && len(frames) == 0 {
			pos := stmt.Pos()
			item.Source = pos.File
			item.Line = pos.Line
			item.Column = pos.Col
			item.Active = true
		}
		frames = append(frames, item)
	}
	for left, right := 0, len(frames)-1; left < right; left, right = left+1, right-1 {
		frames[left], frames[right] = frames[right], frames[left]
	}
	return frames
}

func debugCloneVisibleLocals(active *frame) map[string]Value {
	frames := []*frame{}
	for current := active; current != nil; current = current.parent {
		frames = append(frames, current)
	}
	locals := map[string]Value{}
	for idx := len(frames) - 1; idx >= 0; idx-- {
		for name, value := range frames[idx].locals {
			locals[name] = value.Clone()
		}
	}
	return locals
}

func debugCloneValues(values map[string]Value) map[string]Value {
	cloned := make(map[string]Value, len(values))
	for name, value := range values {
		cloned[name] = value.Clone()
	}
	return cloned
}

func debugStatementKind(stmt ast.Stmt) string {
	if stmt == nil {
		return ""
	}
	name := reflect.TypeOf(stmt).String()
	name = strings.TrimPrefix(name, "*")
	name = filepath.Base(name)
	return strings.TrimPrefix(name, "ast.")
}

func debugDiffSnapshots(previous DebugSnapshot, current DebugSnapshot) []DebugValueDiff {
	diffs := []DebugValueDiff{}
	diffs = append(diffs, debugDiffValueMaps("local", previous.Locals, current.Locals)...)
	diffs = append(diffs, debugDiffValueMaps("global", previous.Globals, current.Globals)...)
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Path < diffs[j].Path })
	return diffs
}

func debugDiffValueMaps(scope string, oldValues map[string]Value, newValues map[string]Value) []DebugValueDiff {
	paths := map[string]bool{}
	for name := range oldValues {
		paths[name] = true
	}
	for name := range newValues {
		paths[name] = true
	}
	diffs := []DebugValueDiff{}
	for name := range paths {
		oldValue, oldOK := oldValues[name]
		newValue, newOK := newValues[name]
		if oldOK && newOK && valuesEqual(oldValue, newValue) {
			continue
		}
		if oldOK && newOK {
			nested := debugDiffNestedValues(scope, name, oldValue, newValue)
			if len(nested) != 0 {
				diffs = append(diffs, nested...)
				continue
			}
		}
		diff := DebugValueDiff{Path: name, Scope: scope}
		if oldOK {
			diff.Old = oldValue.String()
			diff.Kind = debugValueKind(oldValue)
		} else {
			diff.Old = "<missing>"
		}
		if newOK {
			diff.New = newValue.String()
			diff.Kind = debugValueKind(newValue)
		} else {
			diff.New = "<missing>"
		}
		diffs = append(diffs, diff)
	}
	return diffs
}

func debugDiffNestedValues(scope string, path string, oldValue Value, newValue Value) []DebugValueDiff {
	oldValue = derefValue(oldValue)
	newValue = derefValue(newValue)
	if oldValue.kind != valueStruct || newValue.kind != valueStruct || oldValue.structVal == nil || newValue.structVal == nil || oldValue.structVal.Name != newValue.structVal.Name {
		return nil
	}
	fields := map[string]bool{}
	for name := range oldValue.structVal.Fields {
		fields[name] = true
	}
	for name := range newValue.structVal.Fields {
		fields[name] = true
	}
	diffs := []DebugValueDiff{}
	for field := range fields {
		oldField, oldOK := oldValue.structVal.Fields[field]
		newField, newOK := newValue.structVal.Fields[field]
		fieldPath := path + "." + field
		if oldOK && newOK && valuesEqual(oldField, newField) {
			continue
		}
		if oldOK && newOK {
			if nested := debugDiffNestedValues(scope, fieldPath, oldField, newField); len(nested) != 0 {
				diffs = append(diffs, nested...)
				continue
			}
		}
		diff := DebugValueDiff{Path: fieldPath, Scope: scope}
		if oldOK {
			diff.Old = oldField.String()
			diff.Kind = debugValueKind(oldField)
		} else {
			diff.Old = "<missing>"
		}
		if newOK {
			diff.New = newField.String()
			diff.Kind = debugValueKind(newField)
		} else {
			diff.New = "<missing>"
		}
		diffs = append(diffs, diff)
	}
	return diffs
}

func debugValueKind(value Value) string {
	value = derefValue(value)
	switch value.kind {
	case valueVoid:
		return "void"
	case valueNull:
		return "null"
	case valueInt:
		return "int"
	case valueFloat:
		return "float"
	case valueBool:
		return "bool"
	case valueString:
		return "string"
	case valueList:
		return "list"
	case valueStruct:
		if value.structVal != nil {
			return "struct:" + value.structVal.Name
		}
		return "struct"
	case valueFunction:
		return "function"
	case valueRef:
		return "ref"
	default:
		return "value"
	}
}

func debugStringMap(values map[string]Value) map[string]string {
	out := make(map[string]string, len(values))
	for name, value := range values {
		out[name] = value.String()
	}
	return out
}

type DebugSourceRecord struct {
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
	Text   string `json:"text,omitempty"`
}

type DebugSnapshotRecord struct {
	SchemaVersion string              `json:"schema_version"`
	Step          int64               `json:"step"`
	Event         DebugEvent          `json:"event"`
	ThreadID      string              `json:"thread_id"`
	Source        DebugSourceRecord   `json:"source"`
	Function      string              `json:"function"`
	CallStack     []DebugCallFrame    `json:"call_stack,omitempty"`
	StatementKind string              `json:"statement_kind,omitempty"`
	Locals        map[string]string   `json:"locals,omitempty"`
	Globals       map[string]string   `json:"globals,omitempty"`
	Diff          []DebugValueDiff    `json:"diff,omitempty"`
	PC            string              `json:"pc,omitempty"`
	Instruction   string              `json:"instruction,omitempty"`
	Registers     map[string]string   `json:"registers,omitempty"`
	MemoryReads   []DebugMemoryAccess `json:"memory_reads,omitempty"`
	MemoryWrites  []DebugMemoryAccess `json:"memory_writes,omitempty"`
}

type DebugHitRecord struct {
	Condition   string              `json:"condition"`
	ConditionID int                 `json:"condition_id,omitempty"`
	StopReason  DebugStopReason     `json:"stop_reason,omitempty"`
	Snapshot    DebugSnapshotRecord `json:"snapshot"`
}

func (s DebugSnapshot) Record() DebugSnapshotRecord {
	return DebugSnapshotRecord{
		SchemaVersion: s.schemaVersion(),
		Step:          s.Step,
		Event:         s.Event,
		ThreadID:      defaultString(s.ThreadID, "main"),
		Source:        DebugSourceRecord{File: s.Position.File, Line: s.Position.Line, Column: s.Position.Col, Text: s.DisplaySource()},
		Function:      s.Function,
		CallStack:     append([]DebugCallFrame(nil), s.CallStack...),
		StatementKind: s.StatementKind,
		Locals:        s.DebugLocals(),
		Globals:       s.DebugGlobals(),
		Diff:          append([]DebugValueDiff(nil), s.Diff...),
		PC:            s.PC,
		Instruction:   s.Instruction,
		Registers:     cloneStringMap(s.Registers),
		MemoryReads:   append([]DebugMemoryAccess(nil), s.MemoryReads...),
		MemoryWrites:  append([]DebugMemoryAccess(nil), s.MemoryWrites...),
	}
}

func (h DebugHit) Record() DebugHitRecord {
	return DebugHitRecord{Condition: h.Condition, ConditionID: h.ConditionID, StopReason: h.StopReason, Snapshot: h.Snapshot.Record()}
}

func (s DebugSnapshot) schemaVersion() string {
	if s.SchemaVersion != "" {
		return s.SchemaVersion
	}
	return DebugSchemaVersion
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for name, value := range values {
		cloned[name] = value
	}
	return cloned
}

func defaultString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func FormatDebugHaltHuman(debugger *Debugger) string {
	if debugger == nil || debugger.Hit == nil {
		return ""
	}
	hit := debugger.Hit
	snapshot := hit.Snapshot
	var out strings.Builder
	fmt.Fprintf(&out, "[ debugger ] halted: %s\n", hit.Condition)
	if hit.StopReason != "" {
		fmt.Fprintf(&out, "[ reason   ] %s\n", hit.StopReason)
	}
	fmt.Fprintf(&out, "[ source   ] %s\n", snapshot.DisplaySource())
	if snapshot.Function != "" {
		fmt.Fprintf(&out, "[ function ] %s\n", snapshot.Function)
	}
	if len(snapshot.CallStack) != 0 {
		parts := make([]string, 0, len(snapshot.CallStack))
		for _, frame := range snapshot.CallStack {
			parts = append(parts, frame.Function)
		}
		fmt.Fprintf(&out, "[ stack    ] %s\n", strings.Join(parts, " -> "))
	}
	fmt.Fprintf(&out, "[ event    ] step=%d event=%s stmt=%s\n", snapshot.Step, snapshot.Event, snapshot.StatementKind)
	if len(snapshot.Diff) != 0 {
		fmt.Fprintf(&out, "[ diff     ]\n")
		for _, diff := range snapshot.Diff {
			fmt.Fprintf(&out, "  %s: %s -> %s\n", diff.Path, diff.Old, diff.New)
		}
	}
	if debugger.Dropped > 0 {
		fmt.Fprintf(&out, "[ trace    ] dropped %d older events due to trace limit\n", debugger.Dropped)
	}
	fmt.Fprintf(&out, "[ hint     ] step back one event to inspect the prior state\n")
	return out.String()
}

type debugJSONLRecord struct {
	SchemaVersion string               `json:"schema_version"`
	RecordType    string               `json:"record_type"`
	Snapshot      *DebugSnapshotRecord `json:"snapshot,omitempty"`
	Hit           *DebugHitRecord      `json:"hit,omitempty"`
	Event         *DebugSessionEvent   `json:"event,omitempty"`
	Dropped       int64                `json:"dropped,omitempty"`
	State         DebugSessionState    `json:"state,omitempty"`
}

func FormatDebugTraceJSONL(debugger *Debugger) (string, error) {
	if debugger == nil {
		return "", nil
	}
	var out strings.Builder
	for _, snapshot := range debugger.Trace {
		record := debugJSONLRecord{SchemaVersion: DebugSchemaVersion, RecordType: "snapshot"}
		snapshotRecord := snapshot.Record()
		record.Snapshot = &snapshotRecord
		if err := writeJSONLRecord(&out, record); err != nil {
			return "", err
		}
	}
	if debugger.Session != nil {
		for _, event := range debugger.Session.Events {
			eventCopy := event
			record := debugJSONLRecord{SchemaVersion: DebugSchemaVersion, RecordType: "event", Event: &eventCopy, State: debugger.Session.State}
			if err := writeJSONLRecord(&out, record); err != nil {
				return "", err
			}
		}
	}
	if debugger.Hit != nil {
		hitRecord := debugger.Hit.Record()
		record := debugJSONLRecord{SchemaVersion: DebugSchemaVersion, RecordType: "halt", Hit: &hitRecord, Dropped: debugger.Dropped}
		if debugger.Session != nil {
			record.State = debugger.Session.State
		}
		if err := writeJSONLRecord(&out, record); err != nil {
			return "", err
		}
	}
	return out.String(), nil
}

type debugTraceFile struct {
	SchemaVersion string                `json:"schema_version"`
	State         DebugSessionState     `json:"state"`
	Dropped       int64                 `json:"dropped"`
	Cursor        int                   `json:"cursor"`
	Trace         []DebugSnapshotRecord `json:"trace"`
	Events        []DebugSessionEvent   `json:"events,omitempty"`
	Hit           *DebugHitRecord       `json:"hit,omitempty"`
}

func ExportDebugTrace(debugger *Debugger) ([]byte, error) {
	if debugger == nil {
		return json.MarshalIndent(debugTraceFile{SchemaVersion: DebugSchemaVersion}, "", "  ")
	}
	file := debugTraceFile{SchemaVersion: DebugSchemaVersion, Dropped: debugger.Dropped, Cursor: debugger.cursor}
	if debugger.Session != nil {
		file.State = debugger.Session.State
		file.Events = append([]DebugSessionEvent(nil), debugger.Session.Events...)
	}
	for _, snapshot := range debugger.Trace {
		file.Trace = append(file.Trace, snapshot.Record())
	}
	if debugger.Hit != nil {
		hit := debugger.Hit.Record()
		file.Hit = &hit
	}
	return json.MarshalIndent(file, "", "  ")
}

func ImportDebugTrace(data []byte) (*Debugger, error) {
	var file debugTraceFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	session := NewDebugSession(DebuggerConfig{FullTrace: true})
	session.State = file.State
	session.Events = append([]DebugSessionEvent(nil), file.Events...)
	debugger := session.Debugger
	debugger.Dropped = file.Dropped
	debugger.ReplayOnly = true
	for index, record := range file.Trace {
		snapshot := snapshotFromRecord(record)
		snapshot.Index = index
		debugger.Trace = append(debugger.Trace, snapshot)
		if snapshot.Step >= debugger.nextStep {
			debugger.nextStep = snapshot.Step + 1
		}
	}
	debugger.cursor = file.Cursor
	if debugger.cursor < 0 || debugger.cursor >= len(debugger.Trace) {
		debugger.cursor = len(debugger.Trace) - 1
	}
	if file.Hit != nil {
		snapshot := snapshotFromRecord(file.Hit.Snapshot)
		debugger.Hit = &DebugHit{Condition: file.Hit.Condition, ConditionID: file.Hit.ConditionID, StopReason: file.Hit.StopReason, Snapshot: snapshot}
	}
	return debugger, nil
}

func snapshotFromRecord(record DebugSnapshotRecord) DebugSnapshot {
	locals := map[string]Value{}
	for name, value := range record.Locals {
		locals[name] = debugValueFromString(value)
	}
	globals := map[string]Value{}
	for name, value := range record.Globals {
		globals[name] = debugValueFromString(value)
	}
	return DebugSnapshot{
		SchemaVersion: record.SchemaVersion,
		Step:          record.Step,
		Event:         record.Event,
		ThreadID:      record.ThreadID,
		Function:      record.Function,
		CallStack:     append([]DebugCallFrame(nil), record.CallStack...),
		Position:      lexer.Pos{File: record.Source.File, Line: record.Source.Line, Col: record.Source.Column},
		StatementKind: record.StatementKind,
		Locals:        locals,
		Globals:       globals,
		Diff:          append([]DebugValueDiff(nil), record.Diff...),
		PC:            record.PC,
		Instruction:   record.Instruction,
		Registers:     cloneStringMap(record.Registers),
		MemoryReads:   append([]DebugMemoryAccess(nil), record.MemoryReads...),
		MemoryWrites:  append([]DebugMemoryAccess(nil), record.MemoryWrites...),
	}
}

func debugValueFromString(text string) Value {
	text = strings.TrimSpace(text)
	if text == "true" {
		return BoolValue(true)
	}
	if text == "false" {
		return BoolValue(false)
	}
	if parsed, err := strconv.ParseInt(text, 10, 64); err == nil {
		return IntValue(parsed)
	}
	open := strings.Index(text, "(")
	if open > 0 && strings.HasSuffix(text, ")") {
		name := text[:open]
		body := strings.TrimSuffix(text[open+1:], ")")
		fields := map[string]Value{}
		order := []string{}
		for _, part := range strings.Split(body, ",") {
			pieces := strings.SplitN(strings.TrimSpace(part), ":", 2)
			if len(pieces) != 2 {
				continue
			}
			field := strings.TrimSpace(pieces[0])
			order = append(order, field)
			fields[field] = debugValueFromString(strings.TrimSpace(pieces[1]))
		}
		return StructInstanceValue(name, order, fields)
	}
	return StringValue(text)
}

func WriteDebugTraceJSONL(w io.Writer, debugger *Debugger) error {
	text, err := FormatDebugTraceJSONL(debugger)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, text)
	return err
}

func FormatDebugContextForLLM(debugger *Debugger, context int) string {
	if debugger == nil {
		return ""
	}
	current, ok := debugger.Current()
	if !ok {
		return ""
	}
	if context < 0 {
		context = 0
	}
	start := debugger.cursor - context
	if start < 0 {
		start = 0
	}
	end := debugger.cursor + context
	if end >= len(debugger.Trace) {
		end = len(debugger.Trace) - 1
	}
	var out strings.Builder
	fmt.Fprintf(&out, "debug_context schema=%s current_step=%d source=%s\n", DebugSchemaVersion, current.Step, current.DisplaySource())
	if debugger.Hit != nil {
		fmt.Fprintf(&out, "halt condition=%q\n", debugger.Hit.Condition)
	}
	for index := start; index <= end; index++ {
		snapshot := debugger.Trace[index]
		marker := " "
		if index == debugger.cursor {
			marker = "*"
		}
		fmt.Fprintf(&out, "%s step=%d event=%s function=%s stmt=%s\n", marker, snapshot.Step, snapshot.Event, snapshot.Function, snapshot.StatementKind)
		for _, diff := range snapshot.Diff {
			fmt.Fprintf(&out, "  diff %s: %s -> %s\n", diff.Path, diff.Old, diff.New)
		}
	}
	return out.String()
}

func RunDebugREPL(session *DebugSession, in io.Reader, out io.Writer, context int) error {
	if session == nil || session.Debugger == nil {
		return fmt.Errorf("missing debug session")
	}
	scanner := bufio.NewScanner(in)
	fmt.Fprint(out, FormatDebugHaltHuman(session.Debugger))
	for {
		fmt.Fprint(out, "(elisadb) ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		result, quit := ExecuteDebugREPLCommand(session, line, context)
		if result.Output != "" {
			fmt.Fprint(out, result.Output)
			if !strings.HasSuffix(result.Output, "\n") {
				fmt.Fprintln(out)
			}
		}
		if result.Error != "" {
			fmt.Fprintf(out, "error: %s\n", result.Error)
		}
		if quit {
			return nil
		}
	}
}

func ExecuteDebugREPLCommand(session *DebugSession, line string, context int) (DebugCommandResult, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return debugCommandError("", DebugStopManualPause, "empty command"), false
	}
	command := fields[0]
	args := strings.TrimSpace(strings.TrimPrefix(line, command))
	switch command {
	case "c", "continue":
		result := session.ContinueUntilBreak()
		result.Output = formatCommandSnapshot(result)
		return result, false
	case "n", "next":
		result := session.NextSourceStep()
		result.Output = formatCommandSnapshot(result)
		return result, false
	case "s", "step":
		result := session.StepIntoSource()
		result.Output = formatCommandSnapshot(result)
		return result, false
	case "out":
		result := session.StepOutSource()
		result.Output = formatCommandSnapshot(result)
		return result, false
	case "back":
		result := session.BackCommand()
		result.Output = formatCommandSnapshot(result)
		return result, false
	case "forward":
		result := session.ForwardCommand()
		result.Output = formatCommandSnapshot(result)
		return result, false
	case "seek":
		step, err := strconv.Atoi(strings.TrimSpace(args))
		if err != nil {
			return debugCommandError(command, DebugStopRuntimeError, "usage: seek <step>"), false
		}
		result := session.SeekCommand(step)
		result.Output = formatCommandSnapshot(result)
		return result, false
	case "p", "print":
		if args == "" {
			return debugCommandError(command, DebugStopRuntimeError, "usage: print <path>"), false
		}
		value, ok := session.Inspect(args)
		result := session.commandResult(command, ok, DebugStopManualPause, "")
		if ok {
			result.Output = fmt.Sprintf("%s = %s\n", args, value)
		} else {
			result.Error = fmt.Sprintf("unknown path %q", args)
		}
		return result, false
	case "locals":
		return session.commandResult(command, true, DebugStopManualPause, formatStringMap(session.ListLocals())), false
	case "globals":
		return session.commandResult(command, true, DebugStopManualPause, formatStringMap(session.ListGlobals())), false
	case "stack":
		return session.commandResult(command, true, DebugStopManualPause, formatCallStack(session.ListCallStack())), false
	case "diff":
		return session.commandResult(command, true, DebugStopManualPause, formatDiff(session.ListDiff())), false
	case "source":
		result := session.commandResult(command, true, DebugStopManualPause, "")
		if snapshot, ok := session.currentSnapshot(); ok {
			result.Output = snapshot.DisplaySource() + "\n"
		}
		return result, false
	case "break":
		if args == "" {
			return debugCommandError(command, DebugStopRuntimeError, "usage: break <expr>"), false
		}
		id := session.SetBreakpoint(BreakWhenExpr(args))
		return session.commandResult(command, true, DebugStopManualPause, fmt.Sprintf("breakpoint %d: %s\n", id, args)), false
	case "watch":
		if args == "" {
			return debugCommandError(command, DebugStopRuntimeError, "usage: watch <path>"), false
		}
		id := session.SetBreakpoint(BreakWhenPathChanges(args))
		return session.commandResult(command, true, DebugStopManualPause, fmt.Sprintf("watchpoint %d: %s changed\n", id, args)), false
	case "disable", "enable", "delete":
		id, err := strconv.Atoi(strings.TrimSpace(args))
		if err != nil {
			return debugCommandError(command, DebugStopRuntimeError, "usage: "+command+" <id>"), false
		}
		var ok bool
		if command == "delete" {
			ok = session.RemoveBreakpoint(id)
		} else {
			ok = session.SetBreakpointEnabled(id, command == "enable")
		}
		result := session.commandResult(command, ok, DebugStopManualPause, "")
		if ok {
			result.Output = fmt.Sprintf("%s breakpoint %d\n", command, id)
		} else {
			result.Error = fmt.Sprintf("unknown breakpoint %d", id)
		}
		return result, false
	case "breakpoints":
		return session.commandResult(command, true, DebugStopManualPause, formatBreakpoints(session.Breakpoints())), false
	case "llm":
		if args != "" {
			if parsed, err := strconv.Atoi(args); err == nil {
				context = parsed
			}
		}
		return session.commandResult(command, true, DebugStopManualPause, FormatDebugContextForLLM(session.Debugger, context)), false
	case "json":
		payload, err := json.Marshal(session.commandResult(command, true, DebugStopManualPause, ""))
		if err != nil {
			return debugCommandError(command, DebugStopRuntimeError, err.Error()), false
		}
		return session.commandResult(command, true, DebugStopManualPause, string(payload)+"\n"), false
	case "help":
		return session.commandResult(command, true, DebugStopManualPause, debugREPLHelp()), false
	case "q", "quit":
		return session.commandResult(command, true, DebugStopManualPause, "quit\n"), true
	default:
		return debugCommandError(command, DebugStopRuntimeError, "unknown command "+command), false
	}
}

func formatCommandSnapshot(result DebugCommandResult) string {
	if result.Snapshot == nil {
		return ""
	}
	return fmt.Sprintf("step=%d %s %s\n", result.Snapshot.Step, result.Snapshot.Event, result.Snapshot.Source.Text)
}

func formatStringMap(values map[string]string) string {
	if len(values) == 0 {
		return "<empty>\n"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&out, "%s = %s\n", key, values[key])
	}
	return out.String()
}

func formatCallStack(frames []DebugCallFrame) string {
	if len(frames) == 0 {
		return "<empty>\n"
	}
	var out strings.Builder
	for _, frame := range frames {
		active := ""
		if frame.Active {
			active = " *"
		}
		fmt.Fprintf(&out, "%s%s\n", frame.Function, active)
	}
	return out.String()
}

func formatDiff(diffs []DebugValueDiff) string {
	if len(diffs) == 0 {
		return "<empty>\n"
	}
	var out strings.Builder
	for _, diff := range diffs {
		fmt.Fprintf(&out, "%s: %s -> %s\n", diff.Path, diff.Old, diff.New)
	}
	return out.String()
}

func formatBreakpoints(breakpoints []DebugCondition) string {
	if len(breakpoints) == 0 {
		return "<empty>\n"
	}
	var out strings.Builder
	for _, breakpoint := range breakpoints {
		state := "disabled"
		if breakpoint.Enabled {
			state = "enabled"
		}
		fmt.Fprintf(&out, "%d %s %s\n", breakpoint.ID, state, breakpoint.Name)
	}
	return out.String()
}

func debugREPLHelp() string {
	return "commands: continue, next, step, out, back, forward, seek <step>, print <path>, locals, globals, stack, diff, source, break <expr>, watch <path>, disable <id>, enable <id>, delete <id>, breakpoints, llm [context], json, help, quit\n"
}

func writeJSONLRecord(out *strings.Builder, record any) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	out.Write(payload)
	out.WriteByte('\n')
	return nil
}

func evalDebugConditionExpr(snapshot DebugSnapshot, expr string) (bool, error) {
	expr = trimDebugParens(strings.TrimSpace(expr))
	if expr == "" {
		return false, fmt.Errorf("empty expression")
	}
	return evalDebugOrExpr(snapshot, expr)
}

func evalDebugOrExpr(snapshot DebugSnapshot, expr string) (bool, error) {
	parts := splitDebugExpr(expr, " or ")
	if len(parts) > 1 {
		for _, part := range parts {
			value, err := evalDebugAndExpr(snapshot, part)
			if err != nil {
				return false, err
			}
			if value {
				return true, nil
			}
		}
		return false, nil
	}
	return evalDebugAndExpr(snapshot, expr)
}

func evalDebugAndExpr(snapshot DebugSnapshot, expr string) (bool, error) {
	parts := splitDebugExpr(expr, " and ")
	if len(parts) > 1 {
		for _, part := range parts {
			value, err := evalDebugNotExpr(snapshot, part)
			if err != nil {
				return false, err
			}
			if !value {
				return false, nil
			}
		}
		return true, nil
	}
	return evalDebugNotExpr(snapshot, expr)
}

func evalDebugNotExpr(snapshot DebugSnapshot, expr string) (bool, error) {
	expr = trimDebugParens(strings.TrimSpace(expr))
	if strings.HasPrefix(expr, "not ") {
		value, err := evalDebugNotExpr(snapshot, strings.TrimSpace(strings.TrimPrefix(expr, "not ")))
		return !value, err
	}
	return evalDebugCompareExpr(snapshot, expr)
}

func evalDebugCompareExpr(snapshot DebugSnapshot, expr string) (bool, error) {
	for _, op := range []string{"==", "!=", ">=", "<=", ">", "<"} {
		left, right, ok := splitDebugComparison(expr, op)
		if !ok {
			continue
		}
		leftValue, err := evalDebugOperandOrArith(snapshot, left)
		if err != nil {
			return false, err
		}
		rightValue, err := evalDebugOperandOrArith(snapshot, right)
		if err != nil {
			return false, err
		}
		return compareDebugOperands(leftValue, rightValue, op)
	}
	value, err := evalDebugOperandOrArith(snapshot, expr)
	if err != nil {
		return false, err
	}
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("expression %q did not evaluate to bool", expr)
	}
}

func evalDebugOperand(snapshot DebugSnapshot, operand string) (string, error) {
	operand = trimDebugParens(strings.TrimSpace(operand))
	if operand == "true" || operand == "false" {
		return operand, nil
	}
	if len(operand) >= 2 && operand[0] == '"' && operand[len(operand)-1] == '"' {
		return strings.Trim(operand, "\""), nil
	}
	if value, ok := snapshot.LookupString(operand); ok {
		return value, nil
	}
	if _, ok := parseDebugInt(operand); ok {
		// A decimal or hex integer literal (0xF00, 3840, -5); compareDebugOperands parses it.
		return operand, nil
	}
	if looksLikeDebugPath(operand) {
		// A syntactically valid identifier path that is absent from this snapshot is simply
		// not in scope at this step -- e.g. a local declared later in the function. Signal
		// that distinctly (errDebugOperandNotInScope) so the caller can keep stepping until
		// the variable exists, instead of aborting on the first snapshot. A path that is
		// NEVER in scope across the whole run is reported as an error at completion, so a
		// genuine typo is still caught.
		return "", errDebugOperandNotInScope
	}
	return "", fmt.Errorf("unknown debug operand %q", operand)
}

// errDebugOperandNotInScope marks a break-condition operand that is a valid identifier path
// but is not present in the current snapshot (not yet -- or never -- in scope).
var errDebugOperandNotInScope = fmt.Errorf("debug operand not in scope")

// looksLikeDebugPath reports whether operand is a dotted identifier path such as "foo" or
// "foo.bar" -- a variable/field reference rather than a literal or malformed token.
func looksLikeDebugPath(operand string) bool {
	if operand == "" {
		return false
	}
	for i, r := range operand {
		isAlpha := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
		isDigit := r >= '0' && r <= '9'
		if i == 0 {
			if !isAlpha {
				return false
			}
			continue
		}
		if !isAlpha && !isDigit && r != '.' {
			return false
		}
	}
	return true
}

// parseDebugInt parses a decimal or hex (0x...) integer literal, including the full u64
// range and an optional leading minus, returning the bit-equivalent int64.
func parseDebugInt(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if v, err := strconv.ParseInt(s, 0, 64); err == nil {
		return v, true
	}
	if v, err := strconv.ParseUint(s, 0, 64); err == nil {
		return int64(v), true
	}
	return 0, false
}

// evalDebugOperandOrArith evaluates one side of a comparison, supporting integer
// arithmetic over operands -- e.g. "base + 0x1000" or "seg0 - 4" -- where each operand is
// a hex/decimal literal or a variable path that resolves to an integer. Operators must be
// space-separated. When the side is not arithmetic it falls back to plain operand
// resolution (paths, literals, true/false), preserving the not-in-scope sentinel.
func evalDebugOperandOrArith(snapshot DebugSnapshot, side string) (string, error) {
	side = trimDebugParens(strings.TrimSpace(side))
	if idx := strings.Index(side, " + "); idx >= 0 {
		return combineDebugArith(snapshot, side[:idx], side[idx+3:], false)
	}
	if idx := strings.Index(side, " - "); idx >= 0 {
		return combineDebugArith(snapshot, side[:idx], side[idx+3:], true)
	}
	return evalDebugOperand(snapshot, side)
}

func combineDebugArith(snapshot DebugSnapshot, left string, right string, subtract bool) (string, error) {
	leftVal, err := evalDebugOperandOrArith(snapshot, left)
	if err != nil {
		return "", err
	}
	rightVal, err := evalDebugOperandOrArith(snapshot, right)
	if err != nil {
		return "", err
	}
	leftNum, leftOK := parseDebugInt(leftVal)
	rightNum, rightOK := parseDebugInt(rightVal)
	if !leftOK || !rightOK {
		return "", fmt.Errorf("arithmetic requires integer operands, got %q and %q", leftVal, rightVal)
	}
	if subtract {
		return strconv.FormatInt(leftNum-rightNum, 10), nil
	}
	return strconv.FormatInt(leftNum+rightNum, 10), nil
}

func compareDebugOperands(left string, right string, op string) (bool, error) {
	// Integer-aware comparison first: this handles hex literals (0x...), the full u64
	// range, and a numeric value compared against a decimal/hex literal regardless of how
	// the snapshot stringified it.
	if leftInt, leftOK := parseDebugInt(left); leftOK {
		if rightInt, rightOK := parseDebugInt(right); rightOK {
			switch op {
			case "==":
				return leftInt == rightInt, nil
			case "!=":
				return leftInt != rightInt, nil
			case ">":
				return leftInt > rightInt, nil
			case ">=":
				return leftInt >= rightInt, nil
			case "<":
				return leftInt < rightInt, nil
			case "<=":
				return leftInt <= rightInt, nil
			}
		}
	}
	switch op {
	case "==":
		return left == right, nil
	case "!=":
		return left != right, nil
	}
	leftNumber, leftErr := strconv.ParseFloat(left, 64)
	rightNumber, rightErr := strconv.ParseFloat(right, 64)
	if leftErr != nil || rightErr != nil {
		return false, fmt.Errorf("operator %s requires numeric operands, got %q and %q", op, left, right)
	}
	switch op {
	case ">":
		return leftNumber > rightNumber, nil
	case ">=":
		return leftNumber >= rightNumber, nil
	case "<":
		return leftNumber < rightNumber, nil
	case "<=":
		return leftNumber <= rightNumber, nil
	default:
		return false, fmt.Errorf("unsupported debug operator %q", op)
	}
}

func trimDebugParens(expr string) string {
	for {
		expr = strings.TrimSpace(expr)
		if len(expr) < 2 || expr[0] != '(' || expr[len(expr)-1] != ')' {
			return expr
		}
		depth := 0
		inString := false
		wraps := true
		for index := 0; index < len(expr); index++ {
			switch expr[index] {
			case '"':
				inString = !inString
			case '(':
				if !inString {
					depth++
				}
			case ')':
				if !inString {
					depth--
					if depth == 0 && index != len(expr)-1 {
						wraps = false
					}
				}
			}
		}
		if !wraps {
			return expr
		}
		expr = expr[1 : len(expr)-1]
	}
}

func splitDebugComparison(expr string, op string) (string, string, bool) {
	parts := splitDebugExpr(expr, op)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func splitDebugExpr(expr string, sep string) []string {
	parts := []string{}
	inString := false
	depth := 0
	start := 0
	for index := 0; index <= len(expr)-len(sep); index++ {
		switch expr[index] {
		case '"':
			inString = !inString
			continue
		case '(':
			if !inString {
				depth++
			}
		case ')':
			if !inString && depth > 0 {
				depth--
			}
		}
		if inString || depth != 0 {
			continue
		}
		if strings.HasPrefix(expr[index:], sep) {
			parts = append(parts, strings.TrimSpace(expr[start:index]))
			start = index + len(sep)
			index = start - 1
		}
	}
	if len(parts) == 0 {
		return []string{strings.TrimSpace(expr)}
	}
	parts = append(parts, strings.TrimSpace(expr[start:]))
	return parts
}
