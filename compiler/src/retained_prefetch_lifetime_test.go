package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func prefetchLifetimeParse(t *testing.T, filename string, src []byte) *ast.File {
	t.Helper()
	file, ok := parseProgram(filename, src, io.Discard)
	if !ok {
		t.Fatal("fixture parse")
	}
	return file
}

func prefetchLifetimeAwaitPreparation(t *testing.T, done <-chan struct{}, pending *semantic.RetainedScratch, options semantic.AnalyzeOptions) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("request discarded a scratch before its preparation completed")
	case <-time.After(100 * time.Millisecond):
	}
	pending.Prepare(options)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish after its preparation completed")
	}
}

func TestRetainedMismatchedPrefetchJoinsPreparation(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "current.elisa")
	src := []byte("def helper(x: i64) -> i64:\n    return x + 1\n")
	pending := semantic.NewPendingRetainedScratch(prefetchLifetimeParse(t, filename, src))
	parsed := make(chan struct{})
	close(parsed)
	rt := &retainedRuntime{filename: filename, src: src, log: io.Discard,
		prefetch: &retainedPrefetch{filename: filename + ".old", src: src, done: parsed, prepared: pending}}
	done := make(chan struct{})
	go func() { defer close(done); _, _, _ = rt.parseScratch("current-key") }()
	prefetchLifetimeAwaitPreparation(t, done, pending, semantic.AnalyzeOptions{})
	if rt.prefetch != nil {
		t.Fatal("consumed prefetch still owned by runtime")
	}
}

func TestRetainedFastReturnJoinsPreparation(t *testing.T) {
	if os.Getenv("ELISACORE_RETAINED_FAST_PREPARE") != "on" {
		t.Skip("requires fast mode at process startup")
	}
	filename := filepath.Join(t.TempDir(), "fast.elisa")
	before := []byte("def helper(x: i64) -> i64:\n    return x + 1\n")
	after := []byte("def helper(x: i64) -> i64:\n    return x + 2\n")
	options := semantic.AnalyzeOptions{}
	_, state := semantic.AnalyzeRetained(prefetchLifetimeParse(t, filename, before), prefetchLifetimeParse(t, filename, before), before, options)
	current := prefetchLifetimeParse(t, filename, after)
	scratch := prefetchLifetimeParse(t, filename, after)
	pending := semantic.NewPendingRetainedScratch(scratch)
	parsed := make(chan struct{})
	close(parsed)
	key := filename + "\x00" + fmt.Sprintf("%#v", options)
	rt := &retainedRuntime{filename: filename, src: after, file: current, state: state,
		key: key, options: options, log: io.Discard,
		prefetch: &retainedPrefetch{filename: filename, src: after, done: parsed, file: scratch, ok: true, key: key, prepared: pending}}
	done := make(chan struct{})
	var result *semantic.Result
	go func() { defer close(done); result = rt.analyze(current, options) }()
	prefetchLifetimeAwaitPreparation(t, done, pending, options)
	if !state.LastStats.FastPrepared {
		t.Fatalf("test did not exercise fast return: %+v", state.LastStats)
	}
	if result == nil || len(result.Errors()) != 0 {
		t.Fatalf("invalid retained result: %v", result)
	}
	cold := semantic.AnalyzeWithOptions(prefetchLifetimeParse(t, filename, after), options)
	if len(result.Diagnostics) != len(cold.Diagnostics) {
		t.Fatal("cold diagnostics differ")
	}
	for i := range cold.Diagnostics {
		if result.Diagnostics[i].String() != cold.Diagnostics[i].String() {
			t.Fatal("cold diagnostic differs")
		}
	}
}
