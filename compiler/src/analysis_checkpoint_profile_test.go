package main

import (
	"bytes"
	"os"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	"elisacore/src/semantic"
)

// Opt-in profiling of checkpoint encoding; never publishes into a production cache.
func TestAnalysisCheckpointEncodeProfile(t *testing.T) {
	path := os.Getenv("ELISACORE_CHECKPOINT_INPUT")
	if path == "" {
		t.Skip("set ELISACORE_CHECKPOINT_INPUT")
	}
	applyDefaultGCPercent()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parts := bytes.SplitN(data, []byte{'\n'}, 3)
	if len(parts) != 3 || string(parts[0]) != "ELISACSTATE1" || len(parts[1]) != 64 {
		t.Fatal("invalid checkpoint envelope")
	}
	build, err := semantic.RestoreAnalysisBuild(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	if profile := os.Getenv("ELISACORE_CHECKPOINT_PROFILE"); profile != "" {
		file, err := os.Create(profile)
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.StartCPUProfile(file); err != nil {
			file.Close()
			t.Fatal(err)
		}
		defer func() { pprof.StopCPUProfile(); file.Close() }()
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	encoded, err := build.MarshalStateWithCapacity(len(parts[2]) + len(parts[2])/16)
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("checkpoint encoding: %v; input=%d output=%d allocated=%d", elapsed, len(parts[2]), len(encoded), after.TotalAlloc-before.TotalAlloc)
	restored, err := semantic.RestoreAnalysisBuild(encoded)
	if err != nil {
		t.Fatal(err)
	}
	done, total := build.Progress()
	gotDone, gotTotal := restored.Progress()
	if gotDone != done || gotTotal != total {
		t.Fatalf("checkpoint progress changed: (%d,%d) != (%d,%d)", gotDone, gotTotal, done, total)
	}
}
