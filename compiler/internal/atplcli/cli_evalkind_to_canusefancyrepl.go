package atplcli

import (
	"bufio"
	"fmt"
	"github.com/peterh/liner"
	"io"
	"os"
	"strings"
)

type EvalKind int

const (
	EvalKindOther EvalKind = iota
	EvalKindNil
	EvalKindBool
	EvalKindNumber
	EvalKindString
	EvalKindList
	EvalKindObject
	EvalKindBody
	EvalKindSyntax
	EvalKindSyntaxEval
	EvalKindActivation
)

type EvalResult struct {
	Text string
	Kind EvalKind
}
type Evaluator interface {
	Reset() error
	Eval(source string) (EvalResult, error)
}

var replCommands = []string{":help", ":modules", ":load", ":open", ":reload", ":reset", ":clear", ":quit", ":q", "exit"}

func Run(args []string, evaluator Evaluator, modules []string, in io.Reader, out io.Writer, errOut io.Writer, interactive bool) int {
	forceREPL, file, ok := parseArgs(args)
	if !ok {
		fmt.Fprintln(errOut, "usage: atpl [--repl] [file]")
		return 2
	}

	if forceREPL || (file == "" && interactive) {
		if err := runREPL(in, out, errOut, true, evaluator, modules); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return 0
	}

	var (
		src []byte
		err error
	)

	if file != "" {
		src, err = os.ReadFile(file)
	} else {
		src, err = io.ReadAll(in)
	}
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	if err := evaluator.Reset(); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	result, err := evaluator.Eval(string(src))
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	fmt.Fprintln(out, result.Text)
	return 0
}
func parseArgs(args []string) (forceREPL bool, file string, ok bool) {
	for _, arg := range args {
		switch arg {
		case "--repl", "-repl":
			if forceREPL || file != "" {
				return false, "", false
			}
			forceREPL = true
		default:
			if strings.HasPrefix(arg, "-") || file != "" {
				return false, "", false
			}
			file = arg
		}
	}
	return forceREPL, file, true
}

type replTheme struct {
	fancy bool
	color bool
}

func newREPLTheme(fancy bool, out io.Writer) replTheme {
	return replTheme{
		fancy: fancy,
		color: fancy && canUseColor(out),
	}
}
func (t replTheme) primaryPrompt() string {
	if t.fancy {
		return "atpl λ> "
	}
	return "atpl> "
}
func (t replTheme) continuationPrompt() string {
	if t.fancy {
		return ".... │ "
	}
	return "....> "
}
func (t replTheme) banner() []string {
	if !t.fancy {
		return []string{"ATPL REPL — blank line evaluates, :help shows commands, :quit exits."}
	}

	return []string{
		t.style("1;38;5;213", "ATPL REPL ✨"),
		t.style("38;5;111", "Blank line evaluates • Tab completes commands/imports • Ctrl-C clears • Ctrl-D exits"),
		t.style("38;5;246", "Try :help for commands, :modules for stdlib modules, :reset for a fresh session"),
	}
}
func (t replTheme) info(text string) string {
	return t.style("38;5;111", text)
}
func (t replTheme) muted(text string) string {
	return t.style("38;5;246", text)
}
func (t replTheme) error(text string) string {
	return t.style("1;38;5;204", text)
}
func (t replTheme) resultPrefix() string {
	if !t.fancy {
		return ""
	}
	return t.style("1;38;5;81", "=>") + " "
}
func (t replTheme) formatResult(result EvalResult) string {
	if !t.color {
		return result.Text
	}

	switch result.Kind {
	case EvalKindNil:
		return t.style("38;5;244", result.Text)
	case EvalKindBool:
		return t.style("1;38;5;221", result.Text)
	case EvalKindNumber:
		return t.style("1;38;5;141", result.Text)
	case EvalKindString:
		return t.style("38;5;114", result.Text)
	case EvalKindList:
		return t.style("38;5;81", result.Text)
	case EvalKindObject:
		return t.style("38;5;81", result.Text)
	case EvalKindBody:
		return t.style("38;5;81", result.Text)
	case EvalKindSyntax:
		return t.style("38;5;213", result.Text)
	case EvalKindSyntaxEval:
		return t.style("38;5;213", result.Text)
	case EvalKindActivation:
		return t.style("38;5;213", result.Text)
	default:
		return result.Text
	}
}
func (t replTheme) style(code string, text string) string {
	if !t.color {
		return text
	}
	return "\033[" + code + "m" + text + "\033[0m"
}

type replState struct {
	evaluator      Evaluator
	modules        []string
	out            io.Writer
	errOut         io.Writer
	showPrompt     bool
	theme          replTheme
	buffer         []string
	lastLoadedPath string
}

func newREPLState(evaluator Evaluator, modules []string, out io.Writer, errOut io.Writer, showPrompt bool, theme replTheme) (*replState, error) {
	if err := evaluator.Reset(); err != nil {
		return nil, err
	}

	return &replState{
		evaluator:  evaluator,
		modules:    append([]string(nil), modules...),
		out:        out,
		errOut:     errOut,
		showPrompt: showPrompt,
		theme:      theme,
		buffer:     make([]string, 0, 8),
	}, nil
}
func runREPL(in io.Reader, out io.Writer, errOut io.Writer, showPrompt bool, evaluator Evaluator, modules []string) error {
	fancy := showPrompt && canUseFancyREPL(in, out)
	state, err := newREPLState(evaluator, modules, out, errOut, showPrompt, newREPLTheme(fancy, out))
	if err != nil {
		return err
	}

	if showPrompt {
		for _, line := range state.theme.banner() {
			fmt.Fprintln(out, line)
		}
	}

	if fancy {
		return runFancyREPL(state)
	}
	return runPlainREPL(in, state)
}
func runPlainREPL(in io.Reader, state *replState) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		if state.showPrompt {
			fmt.Fprint(state.out, state.prompt())
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return err
			}
			state.finishPending(false, nil)
			return nil
		}

		quit := state.consumeLine(scanner.Text(), nil)
		if quit {
			return nil
		}
	}
}
func runFancyREPL(state *replState) error {
	line := liner.NewLiner()
	defer line.Close()
	line.SetCtrlCAborts(true)
	line.SetCompleter(func(input string) []string {
		return replCompletions(state.modules, input)
	})

	historyFile := replHistoryFile()
	loadHistory(line, historyFile)
	defer saveHistory(line, historyFile)

	for {
		input, err := line.Prompt(state.prompt())
		if err == nil {
			var historyEntry string
			quit := state.consumeLine(input, func(entry string) {
				historyEntry = entry
			})
			if historyEntry != "" {
				line.AppendHistory(historyEntry)
			}
			if quit {
				fmt.Fprintln(state.out, state.theme.muted("bye!"))
				return nil
			}
			continue
		}

		if err == liner.ErrPromptAborted {
			if len(state.buffer) > 0 {
				state.buffer = state.buffer[:0]
				fmt.Fprintln(state.out, state.theme.muted("pending input cleared"))
				continue
			}
			fmt.Fprintln(state.out, state.theme.muted("input canceled — Ctrl-D or :quit exits"))
			continue
		}

		if err == io.EOF {
			state.finishPending(false, nil)
			fmt.Fprintln(state.out, state.theme.muted("bye!"))
			return nil
		}

		return err
	}
}
func (r *replState) prompt() string {
	if len(r.buffer) == 0 {
		return r.theme.primaryPrompt()
	}
	return r.theme.continuationPrompt()
}
func splitREPLInputChunk(input string) []string {
	if !strings.ContainsAny(input, "\r\n") {
		return []string{input}
	}

	lines := make([]string, 0, strings.Count(input, "\n")+strings.Count(input, "\r")+1)
	start := 0
	for index := 0; index < len(input); index++ {
		if input[index] != '\n' && input[index] != '\r' {
			continue
		}
		lines = append(lines, input[start:index])
		if input[index] == '\r' && index+1 < len(input) && input[index+1] == '\n' {
			index++
		}
		start = index + 1
	}
	lines = append(lines, input[start:])
	return lines
}
func (r *replState) consumeLogicalLine(line string, recordHistory func(string)) bool {
	trimmed := strings.TrimSpace(line)

	if len(r.buffer) == 0 && (strings.HasPrefix(trimmed, ":") || trimmed == "exit") {
		return r.handleCommand(trimmed)
	}

	if trimmed == "" {
		if len(r.buffer) == 0 {
			return false
		}
		r.finishPending(recordHistory != nil, recordHistory)
		return false
	}

	r.buffer = append(r.buffer, line)
	return false
}
func (r *replState) consumeLine(line string, recordHistory func(string)) bool {
	logicalLines := splitREPLInputChunk(line)
	multilineChunk := len(logicalLines) > 1
	lastBlank := false

	for _, logicalLine := range logicalLines {
		if r.consumeLogicalLine(logicalLine, recordHistory) {
			return true
		}
		lastBlank = strings.TrimSpace(logicalLine) == ""
	}

	if multilineChunk && !lastBlank && len(r.buffer) > 0 {
		r.finishPending(recordHistory != nil, recordHistory)
	}
	return false
}
func (r *replState) finishPending(remember bool, recordHistory func(string)) {
	if len(r.buffer) == 0 {
		return
	}

	source := strings.Join(r.buffer, "\n") + "\n"
	historyEntry := ""
	if remember && len(r.buffer) == 1 {
		historyEntry = r.buffer[0]
	}

	r.evaluateChunk(source, historyEntry, recordHistory)
	r.buffer = r.buffer[:0]
}
func (r *replState) evaluateChunk(source string, historyEntry string, recordHistory func(string)) {
	result, err := r.evaluator.Eval(source)
	if err != nil {
		r.writeError(err.Error())
		return
	}

	if historyEntry != "" && recordHistory != nil {
		recordHistory(historyEntry)
	}

	if r.theme.fancy {
		fmt.Fprintln(r.out, r.theme.resultPrefix()+r.theme.formatResult(result))
		return
	}

	fmt.Fprintln(r.out, result.Text)
}
func (r *replState) handleCommand(command string) bool {
	switch command {
	case ":q", ":quit", "exit":
		return true
	case ":help":
		r.printHelp()
	case ":clear":
		r.buffer = r.buffer[:0]
		fmt.Fprintln(r.out, r.theme.muted("pending input cleared"))
	case ":reset":
		r.buffer = r.buffer[:0]
		if err := r.evaluator.Reset(); err != nil {
			r.writeError(err.Error())
			return false
		}
		fmt.Fprintln(r.out, r.theme.info("session reset"))
	case ":modules":
		r.printModules()
	default:
		if path, ok := replCommandArgs(command, ":load"); ok {
			r.loadFileCommand(":load", path)
			return false
		}
		if path, ok := replCommandArgs(command, ":open"); ok {
			r.loadFileCommand(":open", path)
			return false
		}
		if args, ok := replCommandArgs(command, ":reload"); ok {
			r.reloadFileCommand(":reload", args)
			return false
		}
		r.writeError(fmt.Sprintf("unknown REPL command: %s", command))
	}
	return false
}
func replCommandArgs(command string, name string) (string, bool) {
	if command == name {
		return "", true
	}
	if !strings.HasPrefix(command, name) || len(command) <= len(name) {
		return "", false
	}
	next := command[len(name)]
	if next != ' ' && next != '\t' && next != '\n' && next != '\r' {
		return "", false
	}
	return strings.TrimSpace(command[len(name):]), true
}
func (r *replState) loadFileCommand(commandName string, path string) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		r.writeError(fmt.Sprintf("usage: %s <path>", commandName))
		return
	}

	data, err := os.ReadFile(trimmedPath)
	if err != nil {
		r.writeError(fmt.Sprintf("could not read file: %s", trimmedPath))
		return
	}

	r.lastLoadedPath = trimmedPath
	r.evaluateChunk(string(data), "", nil)
}
func (r *replState) reloadFileCommand(commandName string, args string) {
	if strings.TrimSpace(args) != "" {
		r.writeError(fmt.Sprintf("usage: %s", commandName))
		return
	}
	if strings.TrimSpace(r.lastLoadedPath) == "" {
		r.writeError("no file loaded; use :load <path> first")
		return
	}

	data, err := os.ReadFile(r.lastLoadedPath)
	if err != nil {
		r.writeError(fmt.Sprintf("could not read file: %s", r.lastLoadedPath))
		return
	}
	if err := r.evaluator.Reset(); err != nil {
		r.writeError(err.Error())
		return
	}

	r.evaluateChunk(string(data), "", nil)
}
func (r *replState) printHelp() {
	if !r.theme.fancy {
		fmt.Fprintln(r.out, ":help    show help")
		fmt.Fprintln(r.out, ":modules list bundled modules")
		fmt.Fprintln(r.out, ":load    evaluate a file in the current session")
		fmt.Fprintln(r.out, ":open    alias for :load")
		fmt.Fprintln(r.out, ":reload  reset session and re-evaluate the last loaded file")
		fmt.Fprintln(r.out, ":reset   reset the REPL session")
		fmt.Fprintln(r.out, ":clear   clear the pending input buffer")
		fmt.Fprintln(r.out, ":quit    exit the REPL")
		fmt.Fprintln(r.out, "Enter one or more lines of ATPL and submit a blank line to evaluate.")
		return
	}

	lines := []string{
		r.theme.info("Commands"),
		"  :help    show this help",
		"  :modules list bundled stdlib modules",
		"  :load    evaluate a file in the current session",
		"  :open    alias for :load",
		"  :reload  reset session and re-evaluate the last loaded file",
		"  :reset   clear bindings/imports and start fresh",
		"  :clear   clear the pending multi-line buffer",
		"  :quit    exit the REPL",
		r.theme.muted("Blank line evaluates the current chunk • Tab completes commands/imports"),
	}
	for _, line := range lines {
		fmt.Fprintln(r.out, line)
	}
}
func (r *replState) printModules() {
	if !r.theme.fancy {
		fmt.Fprintf(r.out, "available modules: %s\n", strings.Join(r.modules, ", "))
		return
	}

	fmt.Fprintln(r.out, r.theme.info("Bundled stdlib modules"))
	for _, module := range r.modules {
		fmt.Fprintf(r.out, "  %s\n", r.theme.style("38;5;81", module))
	}
}
func (r *replState) writeError(text string) {
	if r.theme.fancy {
		fmt.Fprintln(r.errOut, r.theme.error("error: ")+r.theme.error(text))
		return
	}
	fmt.Fprintln(r.errOut, text)
}
func replCompletions(modules []string, line string) []string {
	trimmedLeft := strings.TrimLeft(line, " \t")

	if strings.HasPrefix(trimmedLeft, ":") {
		return prefixMatches(replCommands, trimmedLeft)
	}

	if strings.HasPrefix(trimmedLeft, "import ") {
		prefix := trimmedLeft
		matches := make([]string, 0, len(modules))
		for _, module := range modules {
			candidate := "import " + module
			if strings.HasPrefix(candidate, prefix) {
				matches = append(matches, candidate)
			}
		}
		return matches
	}

	return nil
}
func prefixMatches(options []string, prefix string) []string {
	matches := make([]string, 0, len(options))
	for _, option := range options {
		if strings.HasPrefix(option, prefix) {
			matches = append(matches, option)
		}
	}
	return matches
}
func canUseFancyREPL(in io.Reader, out io.Writer) bool {
	inFile, ok := in.(*os.File)
	if !ok || !IsInteractive(inFile) {
		return false
	}
	outFile, ok := out.(*os.File)
	if !ok || !IsInteractive(outFile) {
		return false
	}
	return true
}
