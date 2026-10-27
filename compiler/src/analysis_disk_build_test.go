package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/semantic"
)

const analysisDiskBootstrapOrderSource = "def first() -> i64:\n    return 1\n\ndef second() -> i64:\n    return 2\n"

func analysisDiskBuildTestProgram(filename string, source []byte) *loadedProgram {
	return &loadedProgram{filename: filename, source: source, sourceFiles: []string{filename}}
}

func publishAnalysisDiskBuildTestBaseline(t *testing.T, filename string, source []byte, options semantic.AnalyzeOptions) analysisDiskArtifact {
	t.Helper()
	program := analysisDiskBuildTestProgram(filename, source)
	file, ok := parseProgram(filename, source, io.Discard)
	if !ok {
		t.Fatal("baseline fixture did not parse")
	}
	pristine, ok := parseProgram(filename, source, io.Discard)
	if !ok {
		t.Fatal("baseline pristine fixture did not parse")
	}
	_, state := semantic.AnalyzeRetained(file, pristine, source, options)
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	artifact, eligible := analysisDiskArtifactFor(program, options)
	if !eligible {
		t.Fatal("baseline fixture is not eligible for disk analysis")
	}
	if err := artifact.publish(data); err != nil {
		t.Fatal(err)
	}
	if err := artifact.publishBaselineSource(source); err != nil {
		t.Fatal(err)
	}
	return artifact
}

func analysisDiskBuildTestCheckpoint(t *testing.T, filename string, source []byte, options semantic.AnalyzeOptions) []byte {
	t.Helper()
	file, ok := parseProgram(filename, source, io.Discard)
	if !ok {
		t.Fatal("build fixture did not parse")
	}
	pristine, ok := parseProgram(filename, source, io.Discard)
	if !ok {
		t.Fatal("build pristine fixture did not parse")
	}
	build, err := semantic.BeginAnalysisBuild(file, pristine, source, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := build.AdvanceBefore(1, 0, -1); err != nil {
		t.Fatal(err)
	}
	done, total := build.Progress()
	if done == 0 || done >= total {
		t.Fatalf("fixture did not leave an unfinished build: %d/%d", done, total)
	}
	data, err := build.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAnalysisBootstrapStepLimitIsolatesKnownSlowDeclaration(t *testing.T) {
	tests := []struct {
		name       string
		configured int
		next       int
		slow       int
		want       int
	}{
		{name: "unlimited", configured: 0, next: 4, slow: 4, want: 1},
		{name: "larger explicit limit", configured: 8, next: 4, slow: 4, want: 1},
		{name: "smaller explicit limit", configured: 1, next: 4, slow: 4, want: 1},
		{name: "slow declaration later", configured: 3, next: 4, slow: 6, want: 3},
		{name: "no slow hint", configured: 0, next: 4, slow: -1, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := analysisBootstrapStepLimit(test.configured, test.next, test.slow); got != test.want {
				t.Fatalf("analysisBootstrapStepLimit(%d, %d, %d) = %d, want %d", test.configured, test.next, test.slow, got, test.want)
			}
		})
	}
}

func TestAnalysisDiskBootstrapAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "program.elisa")
	out := filepath.Join(dir, "out.ll")
	if err := os.WriteFile(source, []byte(retainedCodegenBase), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1", "ELISACORE_ANALYSIS_BOOTSTRAP": "1", "ELISACORE_ANALYSIS_BOOTSTRAP_DECLS": "2", "ELISACORE_BUILD_TIMING": "1", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": filepath.Join(dir, "missing.sock")}
	args := []string{"-emit", "llvm", "-o", out, source}
	paused := 0
	for step := 0; step < 50; step++ {
		output, code := runDiskCLIChildResult(t, env, args...)
		wantParses := 1
		if step == 0 {
			wantParses = 2
		}
		if got := strings.Count(output, "[front-timing] parse:"); got != wantParses {
			t.Fatalf("step %d parsed %d times, want %d: %s", step, got, wantParses, output)
		}
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

func TestAnalysisDiskBootstrapPrefersExactBuildWithoutRestoringBaseline(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_ANALYSIS_BOOTSTRAP", "1")
	filename := filepath.Join(dir, "program.elisa")
	baseline := []byte(analysisDiskBootstrapOrderSource)
	current := []byte(strings.Replace(analysisDiskBootstrapOrderSource, "return 1", "return 9", 1))
	options := semantic.AnalyzeOptions{}
	complete := publishAnalysisDiskBuildTestBaseline(t, filename, baseline, options)
	program := analysisDiskBuildTestProgram(filename, current)
	if currentArtifact, ok := analysisDiskArtifactFor(program, options); !ok || currentArtifact.key != complete.key {
		t.Fatal("body edit unexpectedly changed completed-state identity")
	}
	buildArtifact := analysisBuildArtifact(complete, current)
	if err := buildArtifact.publish(analysisDiskBuildTestCheckpoint(t, filename, current, options)); err != nil {
		t.Fatal(err)
	}

	pending := startAnalysisDiskRestoration(program, options)
	pending.join()
	if pending.build == nil || !pending.buildTried || pending.buildErr != nil {
		t.Fatalf("valid exact checkpoint was not restored first: build=%v tried=%v err=%v", pending.build != nil, pending.buildTried, pending.buildErr)
	}
	if pending.state != nil || pending.preparation.file != nil || pending.preparation.prepared != nil {
		t.Fatal("valid exact checkpoint also restored the rejected completed baseline or prepared scratch")
	}
	if err := validateAnalysisDiskBuildCheckpoint(complete, program, options, pending.build); err != nil {
		t.Fatalf("restored checkpoint failed handoff validation: %v", err)
	}
}

func TestAnalysisDiskBootstrapKeepsUnchangedCompletedStateFirst(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_ANALYSIS_BOOTSTRAP", "1")
	filename := filepath.Join(dir, "program.elisa")
	source := []byte(analysisDiskBootstrapOrderSource)
	options := semantic.AnalyzeOptions{}
	complete := publishAnalysisDiskBuildTestBaseline(t, filename, source, options)
	program := analysisDiskBuildTestProgram(filename, source)
	buildArtifact := analysisBuildArtifact(complete, source)
	if err := buildArtifact.publish(analysisDiskBuildTestCheckpoint(t, filename, source, options)); err != nil {
		t.Fatal(err)
	}

	pending := startAnalysisDiskRestoration(program, options)
	pending.join()
	if pending.buildTried || pending.build != nil || pending.state == nil || pending.err != nil {
		t.Fatalf("unchanged revision did not keep completed-state-first behavior: build=%v tried=%v state=%v err=%v", pending.build != nil, pending.buildTried, pending.state != nil, pending.err)
	}
	if !pending.state.MatchesInvocation(filename, options) {
		t.Fatal("restored completed state did not match the invocation")
	}
}

func TestAnalysisDiskBootstrapInvalidCheckpointFallsBackToCompletedState(t *testing.T) {
	tests := []struct {
		name    string
		payload func(*testing.T, string, []byte, semantic.AnalyzeOptions) []byte
		wrongID bool
	}{
		{name: "corrupt", payload: func(t *testing.T, _ string, _ []byte, _ semantic.AnalyzeOptions) []byte {
			return []byte("corrupt checkpoint")
		}},
		{name: "source mismatch", payload: func(t *testing.T, filename string, source []byte, options semantic.AnalyzeOptions) []byte {
			stale := []byte(strings.Replace(string(source), "return 9", "return 1", 1))
			return analysisDiskBuildTestCheckpoint(t, filename, stale, options)
		}},
		{name: "options mismatch", payload: func(t *testing.T, filename string, source []byte, _ semantic.AnalyzeOptions) []byte {
			return analysisDiskBuildTestCheckpoint(t, filename, source, semantic.AnalyzeOptions{EnforceUnsafePermissions: true})
		}},
		{name: "artifact identity mismatch", wrongID: true, payload: func(t *testing.T, filename string, source []byte, options semantic.AnalyzeOptions) []byte {
			return analysisDiskBuildTestCheckpoint(t, filename, source, options)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
			t.Setenv("ELISACORE_ANALYSIS_BOOTSTRAP", "1")
			filename := filepath.Join(dir, "program.elisa")
			baseline := []byte(analysisDiskBootstrapOrderSource)
			current := []byte(strings.Replace(analysisDiskBootstrapOrderSource, "return 1", "return 9", 1))
			options := semantic.AnalyzeOptions{}
			complete := publishAnalysisDiskBuildTestBaseline(t, filename, baseline, options)
			program := analysisDiskBuildTestProgram(filename, current)
			building := analysisBuildArtifact(complete, current)
			payload := test.payload(t, filename, current, options)
			if test.wrongID {
				if err := os.WriteFile(building.path, append([]byte("ELISACSTATE1\nwrong-identity\n"), payload...), 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := building.publish(payload); err != nil {
				t.Fatal(err)
			}

			pending := startAnalysisDiskRestoration(program, options)
			pending.join()
			if !pending.buildTried || pending.build != nil || pending.buildErr == nil {
				t.Fatalf("invalid checkpoint was not rejected: build=%v tried=%v err=%v", pending.build != nil, pending.buildTried, pending.buildErr)
			}
			if pending.state == nil || pending.err != nil {
				t.Fatalf("invalid checkpoint did not fall back to completed-state restore: state=%v err=%v", pending.state != nil, pending.err)
			}
			if !pending.state.MatchesInvocation(filename, options) {
				t.Fatal("fallback completed state did not match the invocation")
			}
		})
	}
}

func TestAnalysisDiskBuildCheckpointRevalidatesAtHandoff(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	filename := filepath.Join(dir, "program.elisa")
	source := []byte(analysisDiskBootstrapOrderSource)
	options := semantic.AnalyzeOptions{}
	complete := publishAnalysisDiskBuildTestBaseline(t, filename, source, options)
	program := analysisDiskBuildTestProgram(filename, source)
	checkpoint := analysisBuildArtifact(complete, source)
	data := analysisDiskBuildTestCheckpoint(t, filename, source, options)
	if err := checkpoint.publish(data); err != nil {
		t.Fatal(err)
	}
	valid, err := restoreAnalysisDiskBuildCheckpoint(complete, program, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAnalysisDiskBuildCheckpoint(complete, program, options, valid); err != nil {
		t.Fatalf("valid checkpoint rejected at handoff: %v", err)
	}

	changedSource := analysisDiskBuildTestProgram(filename, []byte(strings.Replace(string(source), "return 1", "return 7", 1)))
	if err := validateAnalysisDiskBuildCheckpoint(complete, changedSource, options, valid); err == nil {
		t.Fatal("handoff accepted a checkpoint after the source changed")
	}
	changedOptions := semantic.AnalyzeOptions{EnforceUnsafePermissions: true}
	if err := validateAnalysisDiskBuildCheckpoint(complete, program, changedOptions, valid); err == nil {
		t.Fatal("handoff accepted a checkpoint after semantic options changed")
	}
	otherDomain := analysisDiskArtifact{key: "different-domain", path: complete.path}
	if err := validateAnalysisDiskBuildCheckpoint(otherDomain, program, options, valid); err == nil {
		t.Fatal("handoff accepted a checkpoint from a different completed-artifact domain")
	}

	// If the pinned source changes between the async restore and handoff, reject
	// that future-owned object and load the checkpoint for the now-current source.
	t.Setenv("ELISACORE_ANALYSIS_CACHE_DEBUG", "1")
	t.Setenv("ELISACORE_ANALYSIS_BOOTSTRAP_DECLS", "1")
	changedArtifact := analysisBuildArtifact(complete, changedSource.source)
	if err := changedArtifact.publish(analysisDiskBuildTestCheckpoint(t, filename, changedSource.source, options)); err != nil {
		t.Fatal(err)
	}
	changedFile, ok := parseProgram(filename, changedSource.source, io.Discard)
	if !ok {
		t.Fatal("changed handoff source did not parse")
	}
	var debug bytes.Buffer
	advanceAnalysisDiskBuildWithCheckpoint(changedSource, changedFile, options, complete, &debug, valid, true)
	if !strings.Contains(debug.String(), "declined checkpoint: checkpoint artifact changed before handoff") || !strings.Contains(debug.String(), "bootstrap restore completed=") {
		t.Fatalf("handoff did not reject the old checkpoint and load the current source checkpoint: %s", debug.String())
	}
}
