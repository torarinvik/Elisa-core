package main

import (
	"bytes"
	"os"
	"testing"
	"time"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// Opt-in real compiler-input qualification. Keep this out of routine unit runs;
// the caller supplies a source input and enforces the 30-second process cap.
func TestDiskStateCompilerInput(t *testing.T) {
	filename := os.Getenv("ELISACORE_STATE_QUALIFY_INPUT")
	if filename == "" {
		t.Skip("set ELISACORE_STATE_QUALIFY_INPUT for real-source qualification")
	}
	var diagnostics bytes.Buffer
	program, ok := loadProgramInput(filename, &diagnostics)
	if !ok {
		t.Fatal(diagnostics.String())
	}
	cli, err := parseArgs([]string{"-emit", "obj", filename})
	if err != nil {
		t.Fatal(err)
	}
	var state *semantic.RetainedAnalysis
	var initial *semantic.Result
	started := time.Now()
	_, _, valid := analyzeLoadedProgramUsing(program, &diagnostics, semanticOptionsForCLI(cli), func(file *ast.File, options semantic.AnalyzeOptions) *semantic.Result {
		pristine, ok := parseProgram(filename, program.source, &diagnostics)
		if !ok {
			t.Fatal(diagnostics.String())
		}
		initial, state = semantic.AnalyzeRetained(file, pristine, program.source, options)
		return initial
	})
	t.Logf("initial analysis: %v; valid=%v", time.Since(started), valid)
	if state == nil {
		t.Fatalf("no semantic state: %s", &diagnostics)
	}
	started = time.Now()
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("encode: %v; bytes=%d", time.Since(started), len(data))
	started = time.Now()
	restored, err := semantic.RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("decode: %v", time.Since(started))
	if output := os.Getenv("ELISACORE_STATE_QUALIFY_OUTPUT"); output != "" {
		if err := os.WriteFile(output, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	file, ok := parseProgram(filename, program.source, &diagnostics)
	if !ok {
		t.Fatal(diagnostics.String())
	}
	scratch, ok := parseProgram(filename, program.source, &diagnostics)
	if !ok {
		t.Fatal(diagnostics.String())
	}
	started = time.Now()
	updated, err := restored.Update(file, scratch, program.source)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("restored no-op update: %v", time.Since(started))
	if len(updated.Errors()) != len(initial.Errors()) || len(updated.Notices()) != len(initial.Notices()) {
		t.Fatal("restored no-op changed diagnostic counts")
	}
}
