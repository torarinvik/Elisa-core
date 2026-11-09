package interpreter

import (
	"fmt"
)

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
