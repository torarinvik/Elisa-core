package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalysisDiskBootstrapAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "program.elisa")
	out := filepath.Join(dir, "out.ll")
	if err := os.WriteFile(source, []byte(retainedCodegenBase), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1", "ELISACORE_ANALYSIS_BOOTSTRAP": "1", "ELISACORE_ANALYSIS_BOOTSTRAP_DECLS": "2", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": filepath.Join(dir, "missing.sock")}
	args := []string{"-emit", "llvm", "-o", out, source}
	paused := 0
	for step := 0; step < 50; step++ {
		output, code := runDiskCLIChildResult(t, env, args...)
		if step > 0 && !strings.Contains(output, "bootstrap restore") {
			t.Fatalf("next process did not restore progress: %s", output)
		}
		if code == 0 {
			if !strings.Contains(output, "bootstrap complete") {
				t.Fatalf("unexpected build completion: %s", output)
			}
			break
		}
		if !strings.Contains(output, "bootstrap checkpoint completed=") || !strings.Contains(output, "rerun the same build") {
			t.Fatalf("build failed without a checkpoint: %s", output)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("unfinished build emitted an artifact: %v", err)
		}
		paused++
	}
	if paused == 0 || paused >= 50 {
		t.Fatal("did not exercise resumable builds", paused)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	cold := filepath.Join(dir, "cold.ll")
	runDiskCLIChild(t, map[string]string{"ELISACORE_ANALYSIS_CACHE": "0", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": ""}, "-emit", "llvm", "-o", cold, source)
	want, err := os.ReadFile(cold)
	if err != nil {
		t.Fatal(err)
	}
	if retainedGensym.ReplaceAllString(string(got), "__${1}_N") != retainedGensym.ReplaceAllString(string(want), "__${1}_N") {
		t.Fatal(firstLineDiff(string(got), string(want)))
	}
	// Completed semantic state is shared with ordinary disk-cache builds. The
	// bootstrap controls do not create a different semantic artifact namespace.
	env["ELISACORE_ANALYSIS_BOOTSTRAP"] = "0"
	output := runDiskCLIChild(t, env, args...)
	if !strings.Contains(output, "reuse unchanged revision") || strings.Contains(output, "bootstrap restore") {
		t.Fatalf("completed state was not reused normally: %s", output)
	}
}

func TestAnalysisDiskBootstrapRejectsOldRevisionAndCorruptCheckpoint(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "program.elisa")
	out := filepath.Join(dir, "out.ll")
	initial := "def first() -> i32:\n    return 1\ndef second() -> i32:\n    return 2\ndef main() -> i32:\n    return first() + second()\n"
	if err := os.WriteFile(source, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1", "ELISACORE_ANALYSIS_BOOTSTRAP": "1", "ELISACORE_ANALYSIS_BOOTSTRAP_DECLS": "1", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": ""}
	args := []string{"-emit", "llvm", "-o", out, source}
	output, code := runDiskCLIChildResult(t, env, args...)
	if code == 0 || !strings.Contains(output, "bootstrap checkpoint") {
		t.Fatal(output)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "cache", "analysis", "*.building"))
	if err != nil || len(paths) != 1 {
		t.Fatal(paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err := os.WriteFile(paths[0], data, 0600); err != nil {
		t.Fatal(err)
	}
	output, code = runDiskCLIChildResult(t, env, args...)
	if code == 0 || !strings.Contains(output, "declined checkpoint") || strings.Contains(output, "bootstrap restore") {
		t.Fatal(output)
	}
	changed := strings.Replace(initial, "return 1", "return 4", 1)
	if err := os.WriteFile(source, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	output, code = runDiskCLIChildResult(t, env, args...)
	if code == 0 || strings.Contains(output, "bootstrap restore") || !strings.Contains(output, "bootstrap checkpoint") {
		t.Fatal("old revision was admitted", output)
	}
}
