package atplcli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/semantic"
)

type fakeEvalOutcome struct {
	result EvalResult
	err    error
}

type fakeEvaluator struct {
	resetCount int
	resetErr   error
	outcomes   map[string]fakeEvalOutcome
}

func (f *fakeEvaluator) Reset() error {
	f.resetCount++
	return f.resetErr
}

func (f *fakeEvaluator) Eval(source string) (EvalResult, error) {
	outcome, ok := f.outcomes[source]
	if !ok {
		return EvalResult{}, errors.New("unexpected eval: " + source)
	}
	return outcome.result, outcome.err
}

func testModules() []string {
	return []string{"core", "control", "js", "oop", "reflect", "string"}
}

func TestRunPlainREPLMatchesReferenceTranscript(t *testing.T) {
	stdin := strings.Join([]string{
		"x = 40",
		"",
		"x + 2",
		"",
		":modules",
		":reset",
		"x",
		"",
		":q",
		"",
	}, "\n")

	evaluator := &fakeEvaluator{outcomes: map[string]fakeEvalOutcome{
		"x = 40\n": {result: EvalResult{Text: "nil", Kind: EvalKindNil}},
		"x + 2\n":  {result: EvalResult{Text: "42", Kind: EvalKindNumber}},
		"x\n":      {result: EvalResult{Text: "name error at 1:1: " + semantic.UndefinedIdentifierMessage("x"), Kind: EvalKindOther}, err: errors.New("name error at 1:1: " + semantic.UndefinedIdentifierMessage("x"))},
	}}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := Run([]string{"--repl"}, evaluator, testModules(), strings.NewReader(stdin), &stdout, &stderr, false)
	if status != 0 {
		t.Fatalf("Run returned %d\nstdout:\n%s\nstderr:\n%s", status, stdout.String(), stderr.String())
	}
	if evaluator.resetCount != 2 {
		t.Fatalf("expected 2 resets, got %d", evaluator.resetCount)
	}
	for _, check := range []string{
		"ATPL REPL — blank line evaluates, :help shows commands, :quit exits.",
		"atpl> ....> nil",
		"atpl> ....> 42",
		"available modules: core, control, js, oop, reflect, string",
		"session reset",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected stdout to contain %q, got:\n%s", check, stdout.String())
		}
	}
	if !strings.Contains(stderr.String(), "name error at 1:1: "+semantic.UndefinedIdentifierMessage("x")) {
		t.Fatalf("expected stderr to contain detailed undefined identifier diagnostic, got:\n%s", stderr.String())
	}
}

func TestConsumeLineFinishesPastedMultilineChunk(t *testing.T) {
	pasted := strings.Join([]string{
		"obj = {",
		"    value: 41,",
		"    bump: {",
		"        parent.value = parent.value + 1",
		"        parent.value",
		"    }",
		"}",
		"#obj.bump",
	}, "\n")

	evaluator := &fakeEvaluator{outcomes: map[string]fakeEvalOutcome{
		pasted + "\n": {result: EvalResult{Text: "42", Kind: EvalKindNumber}},
	}}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	state, err := newREPLState(evaluator, testModules(), &stdout, &stderr, false, replTheme{})
	if err != nil {
		t.Fatalf("newREPLState failed: %v", err)
	}

	if quit := state.consumeLine(pasted, nil); quit {
		t.Fatal("consumeLine unexpectedly requested quit")
	}
	if len(state.buffer) != 0 {
		t.Fatalf("expected pasted chunk to evaluate immediately, buffer has %d line(s)", len(state.buffer))
	}
	if got := stdout.String(); got != "42\n" {
		t.Fatalf("unexpected pasted chunk output %q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
}

func TestFancyThemeFormatsResultKinds(t *testing.T) {
	theme := replTheme{fancy: true, color: true}

	if got, want := theme.resultPrefix(), "\x1b[1;38;5;81m=>\x1b[0m "; got != want {
		t.Fatalf("resultPrefix = %q, want %q", got, want)
	}

	tests := []struct {
		name   string
		result EvalResult
		want   string
	}{
		{name: "nil", result: EvalResult{Text: "nil", Kind: EvalKindNil}, want: "\x1b[38;5;244mnil\x1b[0m"},
		{name: "number", result: EvalResult{Text: "42", Kind: EvalKindNumber}, want: "\x1b[1;38;5;141m42\x1b[0m"},
		{name: "string", result: EvalResult{Text: "\"hi\"", Kind: EvalKindString}, want: "\x1b[38;5;114m\"hi\"\x1b[0m"},
		{name: "object", result: EvalResult{Text: "<object>", Kind: EvalKindObject}, want: "\x1b[38;5;81m<object>\x1b[0m"},
		{name: "syntax", result: EvalResult{Text: "syntax { x }", Kind: EvalKindSyntax}, want: "\x1b[38;5;213msyntax { x }\x1b[0m"},
		{name: "other", result: EvalResult{Text: "<value>", Kind: EvalKindOther}, want: "<value>"},
	}

	for _, tt := range tests {
		if got := theme.formatResult(tt.result); got != tt.want {
			t.Fatalf("%s formatResult = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestFancyHelpAndModulesMatchReferenceText(t *testing.T) {
	evaluator := &fakeEvaluator{outcomes: map[string]fakeEvalOutcome{}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	state, err := newREPLState(evaluator, testModules(), &stdout, &stderr, true, replTheme{fancy: true, color: false})
	if err != nil {
		t.Fatalf("newREPLState failed: %v", err)
	}

	state.printHelp()
	state.printModules()

	want := strings.Join([]string{
		"Commands",
		"  :help    show this help",
		"  :modules list bundled stdlib modules",
		"  :load    evaluate a file in the current session",
		"  :open    alias for :load",
		"  :reload  reset session and re-evaluate the last loaded file",
		"  :reset   clear bindings/imports and start fresh",
		"  :clear   clear the pending multi-line buffer",
		"  :quit    exit the REPL",
		"Blank line evaluates the current chunk • Tab completes commands/imports",
		"Bundled stdlib modules",
		"  core",
		"  control",
		"  js",
		"  oop",
		"  reflect",
		"  string",
	}, "\n") + "\n"

	if stdout.String() != want {
		t.Fatalf("unexpected fancy help/modules output:\n%s", stdout.String())
	}
	if evaluator.resetCount != 1 {
		t.Fatalf("expected 1 reset, got %d", evaluator.resetCount)
	}
}

func TestRunPlainREPLLoadAndOpenCommands(t *testing.T) {
	fixtureRoot := filepath.Join(t.TempDir(), "fixtures with spaces")
	if err := os.MkdirAll(fixtureRoot, 0o755); err != nil {
		t.Fatalf("failed to create reference REPL fixture dir: %v", err)
	}

	firstSource := "loaded = 40\nloaded\n"
	secondSource := "second = 10\nsecond\n"
	firstPath := filepath.Join(fixtureRoot, "first file.atpl")
	secondPath := filepath.Join(fixtureRoot, "second file.atpl")
	if err := os.WriteFile(firstPath, []byte(firstSource), 0o644); err != nil {
		t.Fatalf("failed to write first reference REPL fixture: %v", err)
	}
	if err := os.WriteFile(secondPath, []byte(secondSource), 0o644); err != nil {
		t.Fatalf("failed to write second reference REPL fixture: %v", err)
	}

	stdin := strings.Join([]string{
		":load " + firstPath,
		":open " + secondPath,
		"loaded + second",
		"",
		":q",
		"",
	}, "\n")

	evaluator := &fakeEvaluator{outcomes: map[string]fakeEvalOutcome{
		firstSource:         {result: EvalResult{Text: "40", Kind: EvalKindNumber}},
		secondSource:        {result: EvalResult{Text: "10", Kind: EvalKindNumber}},
		"loaded + second\n": {result: EvalResult{Text: "50", Kind: EvalKindNumber}},
	}}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := Run([]string{"--repl"}, evaluator, testModules(), strings.NewReader(stdin), &stdout, &stderr, false)
	if status != 0 {
		t.Fatalf("Run returned %d\nstdout:\n%s\nstderr:\n%s", status, stdout.String(), stderr.String())
	}
	if evaluator.resetCount != 1 {
		t.Fatalf("expected 1 reset, got %d", evaluator.resetCount)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	for _, check := range []string{
		"atpl> 40",
		"atpl> 10",
		"atpl> ....> 50",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected stdout to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestReloadCommandReReadsLastLoadedFileAndResetsSession(t *testing.T) {
	fixturePath := filepath.Join(t.TempDir(), "reload.atpl")
	firstSource := "loaded = 40\nloaded\n"
	secondSource := "loaded = 55\nloaded\n"
	if err := os.WriteFile(fixturePath, []byte(firstSource), 0o644); err != nil {
		t.Fatalf("failed to write first reload fixture: %v", err)
	}

	evaluator := &fakeEvaluator{outcomes: map[string]fakeEvalOutcome{
		firstSource:  {result: EvalResult{Text: "40", Kind: EvalKindNumber}},
		secondSource: {result: EvalResult{Text: "55", Kind: EvalKindNumber}},
	}}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	state, err := newREPLState(evaluator, testModules(), &stdout, &stderr, false, replTheme{})
	if err != nil {
		t.Fatalf("newREPLState failed: %v", err)
	}

	state.loadFileCommand(":load", fixturePath)
	if err := os.WriteFile(fixturePath, []byte(secondSource), 0o644); err != nil {
		t.Fatalf("failed to rewrite reload fixture: %v", err)
	}
	state.reloadFileCommand(":reload", "")

	if evaluator.resetCount != 2 {
		t.Fatalf("expected 2 resets, got %d", evaluator.resetCount)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	if got := stdout.String(); got != "40\n55\n" {
		t.Fatalf("unexpected reload output %q", got)
	}
}

func TestReplCompletions(t *testing.T) {
	modules := testModules()

	if got := replCompletions(modules, ":m"); len(got) != 1 || got[0] != ":modules" {
		t.Fatalf("unexpected command completions: %#v", got)
	}
	if got := replCompletions(modules, ":lo"); len(got) != 1 || got[0] != ":load" {
		t.Fatalf("unexpected load command completions: %#v", got)
	}
	if got := replCompletions(modules, ":op"); len(got) != 1 || got[0] != ":open" {
		t.Fatalf("unexpected open command completions: %#v", got)
	}
	if got := replCompletions(modules, ":rel"); len(got) != 1 || got[0] != ":reload" {
		t.Fatalf("unexpected reload command completions: %#v", got)
	}
	if got := replCompletions(modules, "import re"); len(got) != 1 || got[0] != "import reflect" {
		t.Fatalf("unexpected import completions: %#v", got)
	}
	if got := replCompletions(modules, "x + 1"); got != nil {
		t.Fatalf("expected no completions, got %#v", got)
	}
}
