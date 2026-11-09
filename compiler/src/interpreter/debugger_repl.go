package interpreter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

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
