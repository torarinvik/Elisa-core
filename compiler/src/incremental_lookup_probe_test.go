package main

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Audit one frozen project's real function lookup decisions. This deliberately
// performs fresh analysis, including the ordinary loader provenance policy.
func TestLocalLookupDependencyProbe(t *testing.T) {
	path := os.Getenv("ELISA_INCREMENTAL_LOOKUP_PROBE_SNAPSHOT_IN")
	if path == "" {
		t.Skip("set ELISA_INCREMENTAL_LOOKUP_PROBE_SNAPSHOT_IN")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot projectProbeSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Filename == "" || snapshot.Source == "" {
		t.Fatal("missing frozen source")
	}
	program := &loadedProgram{filename: snapshot.Filename, source: []byte(snapshot.Source), sourceFiles: snapshot.SourceFiles}
	var coldOutput, tracedOutput bytes.Buffer
	_, _, coldOK := analyzeLoadedProgramWithSession(program, &coldOutput, semantic.AnalyzeOptions{}, nil)
	var trace semantic.LookupDependencyTrace
	started := time.Now()
	_, _, tracedOK := analyzeLoadedProgramUsing(program, &tracedOutput, semantic.AnalyzeOptions{}, func(file *ast.File, options semantic.AnalyzeOptions) *semantic.Result {
		var result *semantic.Result
		result, trace = semantic.AnalyzeWithLookupTrace(file, options)
		return result
	})
	if coldOK != tracedOK || coldOutput.String() != tracedOutput.String() {
		t.Fatal("lookup observation changed diagnostics/status")
	}
	t.Logf("traced_ms=%.3f lookup_reads=%d distinct_records=%d dropped=%d truncated=%v diagnostic_bytes=%d", float64(time.Since(started).Microseconds())/1000, trace.Reads, len(trace.Records), trace.Dropped, trace.Truncated, tracedOutput.Len())
	if trace.Reads == 0 || len(trace.Records) == 0 {
		t.Fatal("no real function lookup decisions observed")
	}
	if out := os.Getenv("ELISA_INCREMENTAL_LOOKUP_PROBE_OUT"); out != "" {
		encoded, err := json.Marshal(trace)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
