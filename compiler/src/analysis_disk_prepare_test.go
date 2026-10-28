package main

import (
	"bytes"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func TestAnalysisDiskPreparationJoinsOnPrimaryParseFailure(t *testing.T) {
	program := &loadedProgram{filename: filepath.Join(t.TempDir(), "bad.elisa"), source: []byte("def : !!!\n"), tokenCache: newRequestSourceTokenCache()}
	started, joined, joinedWithTokens := false, false, false
	_, _, ok := analyzeLoadedProgramUsingPreparation(program, io.Discard, semantic.AnalyzeOptions{}, func(*ast.File, semantic.AnalyzeOptions) *semantic.Result {
		t.Fatal("invalid primary parse reached semantic analysis")
		return nil
	}, func(semantic.AnalyzeOptions) func() {
		started = true
		done := make(chan struct{})
		go func() { close(done) }()
		return func() {
			<-done
			joined = true
			joinedWithTokens = program.tokenCache.requestLexed != nil
		}
	})
	if ok || !started || !joined || !joinedWithTokens || program.tokenCache.requestLexed != nil {
		t.Fatalf("parse failure did not join invocation work before releasing request tokens: ok=%v started=%v joined=%v joinedWithTokens=%v tokens=%v", ok, started, joined, joinedWithTokens, program.tokenCache.requestLexed != nil)
	}
}

func TestAnalysisPreparationReleasesRequestTokensAfterJoin(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "request.elisa")
	source := []byte("def value() -> i64:\n    return 21\n\ndef main() -> i64:\n    return value()\n")
	program := &loadedProgram{filename: filename, source: source, tokenCache: newRequestSourceTokenCache()}
	backgroundFile := make(chan *ast.File, 1)
	backgroundErrors := make(chan string, 1)
	backgroundDone := make(chan struct{})
	joinedWithTokens := false

	file, result, ok := analyzeLoadedProgramUsingPreparation(program, io.Discard, semantic.AnalyzeOptions{}, func(file *ast.File, options semantic.AnalyzeOptions) *semantic.Result {
		if program.tokenCache.requestLexed == nil {
			t.Error("request token storage was released before semantic analysis")
		}
		return semantic.AnalyzeWithOptions(file, options)
	}, func(semantic.AnalyzeOptions) func() {
		go func() {
			defer close(backgroundDone)
			var diagnostics bytes.Buffer
			parsed, parsedOK := parseProgramWithTokenCache(filename, source, &diagnostics, program.tokenCache)
			if !parsedOK {
				backgroundErrors <- diagnostics.String()
				return
			}
			backgroundFile <- parsed
		}()
		return func() {
			<-backgroundDone
			joinedWithTokens = program.tokenCache.requestLexed != nil
		}
	})
	if !ok || result == nil || file == nil {
		t.Fatal("analysis failed")
	}
	if !joinedWithTokens {
		t.Fatal("request token storage was released before prepared parsing joined")
	}
	if program.tokenCache.requestLexed != nil {
		t.Fatal("request token storage remained live after analysis returned")
	}
	select {
	case message := <-backgroundErrors:
		t.Fatalf("prepared parse failed: %s", message)
	default:
	}
	preparedFile := <-backgroundFile
	coldFile, coldOK := parseProgram(filename, source, io.Discard)
	if !coldOK || !reflect.DeepEqual(preparedFile, coldFile) {
		t.Fatal("dropping request token storage changed the independently parsed AST")
	}
	value, isFunction := file.Decls[0].(*ast.FuncDecl)
	if !isFunction || value.Name != "value" {
		t.Fatal("analyzed AST strings became invalid after dropping request token storage")
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", result.Diagnostics)
	}
}

func TestAnalysisDiskRestoreJoinsPreparedScratch(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "program.elisa")
	// A new callee forces full scratch preparation even when fast mode is on.
	// Equal signatures keep the declaration classification unchanged.
	before := []byte("def first(x: i64) -> i64:\n    return x + 1\n\ndef second(x: i64) -> i64:\n    return x + 2\n\ndef helper(x: i64) -> i64:\n    return first(x)\n\ndef main() -> i64:\n    return helper(4)\n")
	after := []byte(strings.Replace(string(before), "return first(x)", "return second(x)", 1))
	parse := func(source []byte) *loadedProgram { return &loadedProgram{filename: filename, source: source} }
	file, ok := parseProgram(filename, before, io.Discard)
	if !ok {
		t.Fatal("fixture parse failed")
	}
	pristine, ok := parseProgram(filename, before, io.Discard)
	if !ok {
		t.Fatal("fixture pristine parse failed")
	}
	options := semantic.AnalyzeOptions{}
	_, state := semantic.AnalyzeRetained(file, pristine, before, options)
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range [][]byte{data, []byte("corrupt state")} {
		current, ok := parseProgram(filename, after, io.Discard)
		if !ok {
			t.Fatal("edited parse failed")
		}
		restored, prepared, err := restoreAnalysisWithScratch(payload, parse(after), options, false)
		if prepared.file == nil || prepared.prepared == nil || prepared.elapsed <= 0 {
			t.Fatal("restore returned before preparation finished")
		}
		if string(payload) == "corrupt state" {
			if err == nil || restored != nil {
				t.Fatal("corrupt snapshot admitted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		result, err := restored.UpdatePrepared(current, prepared.file, after, prepared.prepared)
		if err != nil {
			t.Fatal(err)
		}
		if !restored.LastStats.ScratchPrepared || restored.LastStats.Changed != 1 {
			t.Fatalf("prepared body was not used: %+v", restored.LastStats)
		}
		coldFile, ok := parseProgram(filename, after, io.Discard)
		if !ok {
			t.Fatal("cold parse failed")
		}
		cold := semantic.AnalyzeWithOptions(coldFile, options)
		if len(result.Diagnostics) != len(cold.Diagnostics) {
			t.Fatal("overlap changed diagnostics")
		}
		for i := range cold.Diagnostics {
			if result.Diagnostics[i].String() != cold.Diagnostics[i].String() {
				t.Fatal("overlap changed diagnostic")
			}
		}
	}
}
