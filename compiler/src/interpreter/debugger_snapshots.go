package interpreter

import (
	"elisacore/src/ast"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

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
