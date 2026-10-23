package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
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
	if os.Getenv("ELISACORE_STATE_QUALIFY_RAW") != "1" {
		envelope := bytes.SplitN(data, []byte{'\n'}, 3)
		if len(envelope) != 3 || string(envelope[0]) != "ELISACSTATE1" || len(envelope[1]) != 64 {
			t.Fatal("invalid state envelope")
		}
		data = envelope[2]
	}
	start := time.Now()
	var state *semantic.RetainedAnalysis
	var preparation analysisScratchPreparation
	prefetch := os.Getenv("ELISACORE_STATE_QUALIFY_PREFETCH") == "1"
	if !prefetch {
		state, err = semantic.RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("semantic restore: %v", time.Since(start))
	}
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
	var invocationOptions semantic.AnalyzeOptions
	_, result, valid := analyzeLoadedProgramUsing(program, &diagnostics, semanticOptionsForCLI(cli), func(file *ast.File, options semantic.AnalyzeOptions) *semantic.Result {
		invocationOptions = options
		if prefetch {
			started := time.Now()
			state, preparation, err = restoreAnalysisWithScratch(data, program, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("overlapped restore/scratch: %v; scratch=%v", time.Since(started), preparation.elapsed)
		}
		if !state.MatchesInvocation(filename, options) {
			t.Fatal("snapshot invocation options differ from native qualification flags")
		}
		result, exact := state.ResultForSource(program.source)
		if !exact {
			if os.Getenv("ELISACORE_STATE_QUALIFY_REPLAY") != "1" {
				t.Fatal("native qualification input does not match the saved revision")
			}
			if profilePath := os.Getenv("ELISACORE_STATE_QUALIFY_REPLAY_PROFILE"); profilePath != "" {
				profile, err := os.Create(profilePath)
				if err != nil {
					t.Fatal(err)
				}
				if err := pprof.StartCPUProfile(profile); err != nil {
					profile.Close()
					t.Fatal(err)
				}
				defer func() { pprof.StopCPUProfile(); profile.Close() }()
			}
			started := time.Now()
			var err error
			if prefetch {
				if preparation.file == nil || preparation.prepared == nil {
					t.Fatal("replay scratch preparation failed")
				}
				result, err = state.UpdatePrepared(file, preparation.file, program.source, preparation.prepared)
			} else {
				scratch, ok := parseProgram(filename, program.source, io.Discard)
				if !ok {
					t.Fatal("replay scratch parse failed")
				}
				started = time.Now()
				result, err = state.Update(file, scratch, program.source)
			}
			if err != nil {
				t.Fatalf("qualified baseline replay: %v", err)
			}
			t.Logf("baseline replay: %v stats=%+v", time.Since(started), state.LastStats)
			if _, exact := state.ResultForSource(program.source); !exact {
				t.Fatal("replay did not install current expanded source")
			}
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
	profileName := "native"
	if os.Getenv("ELISACORE_STATE_QUALIFY_SAVE_ONLY") == "1" {
		profileName = "snapshot"
	}
	profile, err := os.Create(filepath.Join(root, profileName+".cpu.pprof"))
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
	if os.Getenv("ELISACORE_STATE_QUALIFY_SAVE_ONLY") == "1" {
		start = time.Now()
		snapshot, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("semantic snapshot: %v bytes=%d", time.Since(start), len(snapshot))
		stop()
		if err := os.WriteFile(filepath.Join(root, "qualification.payload"), snapshot, 0600); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("ELISACORE_STATE_QUALIFY_SAVE_RESTORE") == "1" {
			restored, err := semantic.RestoreAnalysisState(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if !restored.MatchesInvocation(filename, invocationOptions) {
				t.Fatal("re-encoded snapshot changed invocation")
			}
			replay, exact := restored.ResultForSource(program.source)
			if !exact || len(replay.Diagnostics) != len(result.Diagnostics) {
				t.Fatal("re-encoded snapshot changed source or diagnostic count")
			}
			for i, diagnostic := range result.Diagnostics {
				if diagnostic.String() != replay.Diagnostics[i].String() {
					t.Fatalf("re-encoded snapshot changed diagnostic %d", i)
				}
			}
			t.Logf("re-encoded snapshot restored; diagnostics=%d", len(replay.Diagnostics))
		}
		return
	}
	session := backend.NewIncrementalObjectSession(backend.NativeObjectPartitions(), 4<<30)
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
