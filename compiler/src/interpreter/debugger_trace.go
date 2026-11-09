package interpreter

import (
	"elisacore/src/lexer"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

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
