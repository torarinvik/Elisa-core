package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"testing"
	"time"

	"elisacore/src/ast"
	"elisacore/src/backend"
	"elisacore/src/semantic"
)

// Opt-in phase profiling of a finished real-input semantic snapshot. This does
// not bypass CLI admission or publish a semantic snapshot into a production cache.
// Run the test binary with an external 30-second process cap.
func TestDiskStateNativeCompilerProfile(t *testing.T) {
	path := os.Getenv("ELISACORE_STATE_QUALIFY_NATIVE")
	if path == "" {
		t.Skip("set ELISACORE_STATE_QUALIFY_NATIVE")
	}
	applyDefaultGCPercent()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	envelope := bytes.SplitN(data, []byte{'\n'}, 3)
	if len(envelope) != 3 || string(envelope[0]) != "ELISACSTATE1" || len(envelope[1]) != 64 {
		t.Fatal("invalid state envelope")
	}
	start := time.Now()
	state, err := semantic.RestoreAnalysisState(envelope[2])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("semantic restore: %v", time.Since(start))
	filename := os.Getenv("ELISACORE_STATE_QUALIFY_INPUT")
	if filename == "" {
		t.Fatal("set ELISACORE_STATE_QUALIFY_INPUT to validate the snapshot revision")
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
	if selected := os.Getenv("ELISACORE_STATE_QUALIFY_NATIVE_OPT"); selected != "" {
		cli.optLevel, err = parseOptimizationArg(selected)
		if err != nil {
			t.Fatal(err)
		}
		cli.hasOptLevel = true
	}
	if err := configureCLIProfile(cli); err != nil {
		t.Fatal(err)
	}
	// Use the CLI's source-authority/options path and reject mismatched
	// snapshots. In particular default object emission is O3, not O0.
	_, result, valid := analyzeLoadedProgramUsing(program, &diagnostics, semanticOptionsForCLI(cli), func(_ *ast.File, options semantic.AnalyzeOptions) *semantic.Result {
		if !state.MatchesInvocation(filename, options) {
			t.Fatal("snapshot invocation options differ from native qualification flags")
		}
		result, exact := state.ResultForSource(program.source)
		if !exact {
			t.Fatal("native qualification input does not match the saved revision")
		}
		return result
	})
	if !valid {
		t.Fatal(diagnostics.String())
	}
	t.Logf("native flags: opt=%v target=%q debug=%v", effectiveOptimizationLevel(cli), cli.targetTriple, cli.debugInfo)
	// Allows go test -test.cpuprofile to measure restoration and checked source
	// admission without overlapping the native-stage profiler below.
	if os.Getenv("ELISACORE_STATE_QUALIFY_RESTORE_ONLY") == "1" {
		return
	}
	root := os.Getenv("ELISACORE_STATE_QUALIFY_NATIVE_OUTPUT")
	if root == "" {
		root = t.TempDir()
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	profile, err := os.Create(filepath.Join(root, "native.cpu.pprof"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pprof.StartCPUProfile(profile); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			pprof.StopCPUProfile()
			profile.Close()
			if f, err := os.Create(filepath.Join(root, "native.goroutines.txt")); err == nil {
				pprof.Lookup("goroutine").WriteTo(f, 2)
				f.Close()
			}
		})
	}
	timer := time.AfterFunc(18*time.Second, stop)
	defer timer.Stop()
	defer stop()
	session := backend.NewIncrementalObjectSession(runtime.NumCPU(), 4<<30)
	stamp, err := compilerSourceStamp()
	if err != nil {
		t.Fatal(err)
	}
	session.EnableDiskCache(filepath.Join(root, "partitions"), stamp, 2<<30)
	start = time.Now()
	err = session.WriteObject(result, filepath.Join(root, "compiler.o"), backend.LLVMObjectEmitOptions{OptLevel: effectiveOptimizationLevel(cli), PackedProfile: cli.packedProfile, TargetTriple: cli.targetTriple, DebugInfo: cli.debugInfo, Trace: cli.recordTrace})
	t.Logf("native: %v stats=%+v", time.Since(start), session.Stats())
	if err != nil {
		t.Fatal(err)
	}
}
