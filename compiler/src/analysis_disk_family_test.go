package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"elisacore/src/semantic"
)

func TestAnalysisDiskFamilyAddedFilesAuthorityGuards(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", dir)
	root := filepath.Join(dir, "root.elisa")
	helper := filepath.Join(dir, "helper.elisa")
	program := &loadedProgram{filename: root, source: []byte("def main() -> i64:\n    return 7\n"), sourceFiles: []string{root}}
	options := semantic.AnalyzeOptions{}
	baseline, ok := analysisDiskArtifactFor(program, options)
	if !ok {
		t.Fatal("baseline ineligible")
	}
	if err := baseline.publish([]byte("original envelope")); err != nil {
		t.Fatal(err)
	}
	if err := publishAnalysisDiskFamily(program, options, baseline); err != nil {
		t.Fatal(err)
	}
	program.sourceFiles = []string{root, helper}
	candidate, ok := analysisDiskAddedFilesBaseline(program, options)
	if !ok || candidate != baseline {
		t.Fatal("added file did not find exact original artifact")
	}
	data, err := candidate.read()
	if err != nil || string(data) != "original envelope" {
		t.Fatal("candidate envelope was relabeled", err)
	}
	options.EnforceStrictProofs = true
	if _, ok := analysisDiskAddedFilesBaseline(program, options); ok {
		t.Fatal("proof policy shared a family")
	}
	options.EnforceStrictProofs = false
	options.TrustedRuntimeSourceFiles = []string{helper}
	if _, ok := analysisDiskAddedFilesBaseline(program, options); ok {
		t.Fatal("changed trust shared a family")
	}
	options.TrustedRuntimeSourceFiles = nil
	program.sourceFiles = []string{helper}
	if _, ok := analysisDiskAddedFilesBaseline(program, options); ok {
		t.Fatal("removed authority shared a family")
	}
	program.sourceFiles = []string{root, helper}
	path, _ := analysisDiskFamilyPath(program, options)
	for _, record := range []analysisDiskFamily{{Version: 1, Key: strings.Repeat("0", 64), Origins: []string{root}}, {Version: 1, Key: baseline.key, Origins: []string{root, root}}, {Version: 2, Key: baseline.key, Origins: []string{root}}} {
		encoded, _ := json.Marshal(record)
		if err := os.WriteFile(path, encoded, 0600); err != nil {
			t.Fatal(err)
		}
		if _, ok := analysisDiskAddedFilesBaseline(program, options); ok {
			t.Fatal("invalid family hint admitted")
		}
	}
}

func TestAnalysisDiskFamilyFreshProcessAddedIncludeColdOracle(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "main.elisa")
	helper := filepath.Join(dir, "helper.elisa")
	cacheEnv := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": ""}
	coldEnv := map[string]string{"ELISACORE_ANALYSIS_CACHE": "0", "ELISACORE_ANALYSIS_CACHE_DEBUG": "0", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": ""}
	baseline := []byte("def main() -> i64:\n    return 7\n")
	if err := os.WriteFile(source, baseline, 0600); err != nil {
		t.Fatal(err)
	}
	runDiskCLIChild(t, cacheEnv, "-emit", "llvm", "-o", filepath.Join(dir, "baseline.ll"), source)
	// Native and textual IR use distinct semantic options; admit each honestly.
	runDiskCLIChild(t, cacheEnv, "-emit", "obj", "-o", filepath.Join(dir, "baseline.o"), source)
	states, err := filepath.Glob(filepath.Join(dir, "cache", "analysis", "*.state"))
	if err != nil || len(states) == 0 {
		t.Fatal("baseline not stored", states, err)
	}
	originals := map[string][]byte{}
	for _, path := range states {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		originals[path] = data
	}
	if err := os.WriteFile(source, []byte("include \"helper.elisa\"\n\ndef main() -> i64:\n    return helper()\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"42", "true", "43"} {
		if err := os.WriteFile(helper, []byte("def helper() -> i64:\n    return "+value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		cached, cold := filepath.Join(dir, "cached.ll"), filepath.Join(dir, "cold.ll")
		got, gotCode := runDiskCLIChildResult(t, cacheEnv, "-emit", "llvm", "-o", cached, source)
		want, wantCode := runDiskCLIChildResult(t, coldEnv, "-emit", "llvm", "-o", cold, source)
		if !strings.Contains(got, "replay added-file baseline") || strings.Contains(got, "cold analysis") {
			t.Fatalf("added file did not replay: %s", got)
		}
		if gotCode != wantCode || diskDiagnosticTranscript(got) != diskDiagnosticTranscript(want) {
			t.Fatalf("cold parity failed\ngot=%d %s\nwant=%d %s", gotCode, got, wantCode, want)
		}
		if value == "true" && gotCode == 0 {
			t.Fatal("invalid helper accepted")
		}
		if gotCode == 0 {
			gotIR, _ := os.ReadFile(cached)
			wantIR, _ := os.ReadFile(cold)
			if !bytes.Equal(gotIR, wantIR) {
				t.Fatal("added include IR differs from cold", firstLineDiff(string(gotIR), string(wantIR)))
			}
		}
		for path, original := range originals {
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(original, current) {
				t.Fatal("baseline changed", err)
			}
		}
	}
	object := filepath.Join(dir, "native.o")
	output := runDiskCLIChild(t, cacheEnv, "-emit", "obj", "-o", object, source)
	if !strings.Contains(output, "replay added-file baseline") {
		t.Fatal("native build did not replay", output)
	}
	binary := filepath.Join(dir, "native")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	linked, err := exec.CommandContext(ctx, "clang", object, "-o", binary).CombinedOutput()
	cancel()
	if err != nil {
		t.Fatalf("link %v %s", err, linked)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	err = exec.CommandContext(ctx, binary).Run()
	cancel()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 43 {
		t.Fatalf("added include native result: %v", err)
	}

}
