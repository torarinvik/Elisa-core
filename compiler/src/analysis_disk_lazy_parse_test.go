package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/semantic"
)

func TestAnalysisDiskLazyRestoreDefersIndependentParse(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "lazy.elisa")
	before := []byte("def first(x: i64) -> i64:\n    return x + 1\n\ndef second(x: i64) -> i64:\n    return x + 2\n\ndef main() -> i64:\n    return first(4)\n")
	after := []byte(strings.Replace(string(before), "first(4)", "second(4)", 1))
	file, ok := parseProgram(filename, before, io.Discard)
	if !ok {
		t.Fatal("baseline parse")
	}
	pristine, ok := parseProgram(filename, before, io.Discard)
	if !ok {
		t.Fatal("baseline comparison parse")
	}
	initial, state := semantic.AnalyzeRetained(file, pristine, before, semantic.AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	for _, badSource := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "parse_failure"}[badSource], func(t *testing.T) {
			source := after
			if badSource {
				source = []byte("def : !!!\n")
			}
			program := &loadedProgram{filename: filename, source: source, tokenCache: newRequestSourceTokenCache()}
			restored, preparation, err := restoreAnalysisWithScratch(data, program, semantic.AnalyzeOptions{}, true)
			if err != nil {
				t.Fatal(err)
			}
			if preparation.file != nil || preparation.elapsed != 0 || !preparation.prepared.AcceptsScratch(nil) {
				t.Fatal("lazy hydration materialized an independent parse")
			}
			current, ok := parseProgram(filename, after, io.Discard)
			if !ok {
				t.Fatal("primary parse")
			}
			warm, err := restored.UpdatePrepared(current, preparation.file, after, preparation.prepared)
			if badSource {
				if err == nil || !strings.Contains(err.Error(), "scratch parse failed") {
					t.Fatalf("deferred parse failure not propagated: %v", err)
				}
				preparation.prepared.Wait() // failed lazy parse remains joinable
				if _, ok := restored.ResultForSource(before); !ok {
					t.Fatal("parse failure consumed admitted baseline")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !restored.LastStats.ScratchPrepared {
				t.Fatalf("new callee must use deferred scratch: %+v", restored.LastStats)
			}
			coldFile, ok := parseProgram(filename, after, io.Discard)
			if !ok {
				t.Fatal("cold parse")
			}
			cold := semantic.AnalyzeWithOptions(coldFile, semantic.AnalyzeOptions{})
			if len(cold.Diagnostics) != len(warm.Diagnostics) {
				t.Fatal("deferred parse diagnostics differ")
			}
			for i := range cold.Diagnostics {
				if cold.Diagnostics[i].String() != warm.Diagnostics[i].String() {
					t.Fatal("deferred parse diagnostics differ")
				}
			}
		})
	}
	// A rejected hydrated graph must not start the unused lazy parser either.
	_, preparation, err := restoreAnalysisWithScratch([]byte("corrupt state"), &loadedProgram{filename: filename, source: []byte("def : !!!\n")}, semantic.AnalyzeOptions{}, true)
	if err == nil || preparation.file != nil || !preparation.prepared.AcceptsScratch(nil) {
		t.Fatal("rejected lazy restoration unexpectedly parsed source")
	}
}
