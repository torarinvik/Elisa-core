package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultAnalysisDiskPolicyHonorsExplicitSelections(t *testing.T) {
	for _, test := range []struct {
		cache, build, server string
		want                 bool
	}{
		{"", "", "", true}, {"", "0", "", false}, {"", "1", "chosen.sock", false},
		{"0", "1", "", false}, {"1", "0", "chosen.sock", true}, {"1", "1", "", true},
	} {
		t.Run(test.cache+"/"+test.build+"/"+test.server, func(t *testing.T) {
			t.Setenv("ELISACORE_ANALYSIS_CACHE", test.cache)
			t.Setenv("ELISACORE_BUILD_CACHE", test.build)
			t.Setenv("ELISACORE_RETAINED_SERVER", test.server)
			if got := analysisDiskCacheEnabled(); got != test.want {
				t.Fatalf("disk selection=%v want=%v", got, test.want)
			}
		})
	}
}

func TestDefaultDiskSmallProgramFinishesBodyAndPostInOneCommand(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "tiny.elisa")
	out := filepath.Join(dir, "tiny.ll")
	if err := os.WriteFile(source, []byte("def main() -> i64:\n    return 3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "", "ELISACORE_BUILD_CACHE": "1", "ELISACORE_RETAINED_SERVER": "", "ELISACORE_ANALYSIS_BOOTSTRAP": "", "ELISACORE_ANALYSIS_BOOTSTRAP_DECLS": "", "ELISACORE_ANALYSIS_BOOTSTRAP_SECONDS": "", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1"}
	first, code := runDiskCLIChildResult(t, env, "-emit", "llvm", "-o", out, source)
	if code != 0 || !strings.Contains(first, "bootstrap complete") || strings.Contains(first, "build incomplete") {
		t.Fatalf("tiny cold default command did not complete: %d %s", code, first)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "bootstrap body boundary checkpoint capacity=") {
		t.Fatal("automatic post continuation did not refresh actual checkpoint capacity")
	}
	remaining, _ := filepath.Glob(filepath.Join(dir, "cache", "analysis", "*.building"))
	if len(remaining) != 0 {
		t.Fatal("successfully published completed state retained obsolete cold checkpoint")
	}
	second, code := runDiskCLIChildResult(t, env, "-emit", "llvm", "-o", out, source)
	if code != 0 || !strings.Contains(second, "reuse unchanged revision") {
		t.Fatalf("normal default command missed completed semantic disk state: %d %s", code, second)
	}
}

func TestDefaultDiskSemanticErrorsRemainOrdinaryErrors(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "bad.elisa")
	out := filepath.Join(dir, "bad.ll")
	if err := os.WriteFile(source, []byte("def main() -> i64:\n    return unknown_name\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "", "ELISACORE_BUILD_CACHE": "1", "ELISACORE_RETAINED_SERVER": "", "ELISACORE_ANALYSIS_BOOTSTRAP": "", "ELISACORE_ANALYSIS_BOOTSTRAP_DECLS": ""}
	output, code := runDiskCLIChildResult(t, env, "-emit", "llvm", "-o", out, source)
	if code == 0 || code == analysisIncompleteExitCode || strings.Contains(output, "build incomplete") || !strings.Contains(output, "unknown_name") {
		t.Fatalf("semantic errors changed into progress: %d %s", code, output)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("failed semantic build emitted an artifact")
	}
}

func TestDefaultDiskCheckpointStatusResumeAndCorruptRejection(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "progress.elisa")
	out := filepath.Join(dir, "progress.ll")
	cache := filepath.Join(dir, "cache")
	if err := os.WriteFile(source, []byte(retainedCodegenBase), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": cache, "ELISACORE_ANALYSIS_CACHE": "", "ELISACORE_BUILD_CACHE": "1", "ELISACORE_RETAINED_SERVER": "", "ELISACORE_ANALYSIS_BOOTSTRAP": "", "ELISACORE_ANALYSIS_BOOTSTRAP_DECLS": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1"}
	output, code := runDiskCLIChildResult(t, env, "-emit", "llvm", "-o", out, source)
	if code != analysisIncompleteExitCode || !strings.Contains(output, "build incomplete: initial analysis checkpoint saved") {
		t.Fatalf("checkpoint was not distinct durable progress: %d %s", code, output)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("unfinished checkpoint emitted output")
	}
	checkpoints, err := filepath.Glob(filepath.Join(cache, "analysis", "*.building"))
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("missing exact build checkpoint: %v %v", checkpoints, err)
	}
	if err := os.WriteFile(checkpoints[0], []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	rejected, code := runDiskCLIChildResult(t, env, "-emit", "llvm", "-o", out, source)
	if code != analysisIncompleteExitCode || !strings.Contains(rejected, "declined checkpoint") || strings.Contains(rejected, "bootstrap restore") {
		t.Fatalf("corrupt checkpoint admitted: %d %s", code, rejected)
	}
	env["ELISACORE_ANALYSIS_BOOTSTRAP_DECLS"] = ""
	resumed, code := runDiskCLIChildResult(t, env, "-emit", "llvm", "-o", out, source)
	if code != 0 || !strings.Contains(resumed, "bootstrap restore") || !strings.Contains(resumed, "bootstrap complete") {
		t.Fatalf("normal command failed to auto-resume exact checkpoint: %d %s", code, resumed)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
}

func TestAnalysisBootstrapBudgetNeverRestartsOrClampsPastDeadline(t *testing.T) {
	previous := processStartTime
	t.Cleanup(func() { processStartTime = previous })
	t.Setenv("ELISACORE_ANALYSIS_BOOTSTRAP_SECONDS", "15")
	processStartTime = time.Now().Add(-analysisBootstrapDeadline)
	if got := analysisBootstrapBudget(0, false); got != 0 {
		t.Fatalf("expired process received positive/unbounded work budget %v", got)
	}
	processStartTime = time.Now()
	first := analysisBootstrapBudget(0, false)
	processStartTime = processStartTime.Add(-time.Second)
	second := analysisBootstrapBudget(0, false)
	if first <= 0 || second > first {
		t.Fatal("continuation reset global process deadline")
	}
}

func TestDefaultDiskUnwritableCacheDoesNotBreakCompletedSmallBuild(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "tiny.elisa")
	out := filepath.Join(dir, "tiny.ll")
	cache := filepath.Join(dir, "cache-file")
	if err := os.WriteFile(source, []byte("def main() -> i64:\n    return 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": cache, "ELISACORE_ANALYSIS_CACHE": "", "ELISACORE_BUILD_CACHE": "1", "ELISACORE_RETAINED_SERVER": "", "ELISACORE_ANALYSIS_BOOTSTRAP": "", "ELISACORE_ANALYSIS_BOOTSTRAP_DECLS": ""}
	output, code := runDiskCLIChildResult(t, env, "-emit", "llvm", "-o", out, source)
	if code != 0 || strings.Contains(output, "build incomplete") {
		t.Fatalf("best-effort cache broke completed small build: %d %s", code, output)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultDiskObjectCapturePreservesIncompleteStatus(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "progress.elisa")
	out := filepath.Join(dir, "progress.o")
	if err := os.WriteFile(source, []byte(retainedCodegenBase), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "", "ELISACORE_BUILD_CACHE": "1", "ELISACORE_RETAINED_SERVER": "", "ELISACORE_ANALYSIS_BOOTSTRAP": "", "ELISACORE_ANALYSIS_BOOTSTRAP_DECLS": "1"}
	output, code := runDiskCLIChildResult(t, env, "-emit", "obj", "-o", out, source)
	if code != analysisIncompleteExitCode || !strings.Contains(output, "build incomplete") {
		t.Fatalf("object transcript capture lost typed incomplete status: %d %s", code, output)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("unfinished analysis emitted/cached an object")
	}
}

func TestAutomaticCheckpointStorageUsesRealIOAndCleansProbe(t *testing.T) {
	dir := t.TempDir()
	artifact := analysisDiskArtifact{path: filepath.Join(dir, "available", "state")}
	if err := analysisDiskCheckpointStorageAvailable(artifact); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(artifact.path))
	if err != nil || len(entries) != 0 {
		t.Fatalf("checkpoint preflight left files: %v %v", entries, err)
	}
	blocked := filepath.Join(dir, "regular-file")
	if err := os.WriteFile(blocked, []byte("cannot contain a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := analysisDiskCheckpointStorageAvailable(analysisDiskArtifact{path: filepath.Join(blocked, "analysis", "state")}); err == nil {
		t.Fatal("regular-file parent incorrectly admitted writable checkpoint storage")
	}
}

func TestDefaultAutomaticUnwritableCheckpointFallsBackUncachedEvenWithUnitCap(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "program.elisa")
	out := filepath.Join(dir, "program.ll")
	cache := filepath.Join(dir, "cache-file")
	if err := os.WriteFile(source, []byte(retainedCodegenBase), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache, []byte("blocked directory"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": cache, "ELISACORE_ANALYSIS_CACHE": "", "ELISACORE_BUILD_CACHE": "1", "ELISACORE_RETAINED_SERVER": "", "ELISACORE_ANALYSIS_BOOTSTRAP": "", "ELISACORE_ANALYSIS_BOOTSTRAP_DECLS": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1"}
	output, code := runDiskCLIChildResult(t, env, "-emit", "llvm", "-o", out, source)
	if code != 0 || !strings.Contains(output, "automatic checkpoint storage unavailable") || strings.Contains(output, "build incomplete") || strings.Contains(output, "bootstrap step declarations=") {
		t.Fatalf("implicit automatic mode did not fall back to ordinary uncached analysis: %d %s", code, output)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
	// Explicit manual qualification keeps its requested checkpoint/error policy.
	env["ELISACORE_ANALYSIS_BOOTSTRAP"] = "1"
	manualOut := filepath.Join(dir, "manual.ll")
	manual, manualCode := runDiskCLIChildResult(t, env, "-emit", "llvm", "-o", manualOut, source)
	if manualCode == 0 || manualCode == analysisIncompleteExitCode || strings.Contains(manual, "uncached local analysis") || !strings.Contains(manual, "checkpoint not saved") {
		t.Fatalf("explicit manual qualification silently bypassed storage/step constraints: %d %s", manualCode, manual)
	}
	if _, err := os.Stat(manualOut); !os.IsNotExist(err) {
		t.Fatal("failed manual checkpoint emitted output")
	}
}

func TestDefaultDiskStrictProofAndDiagnosticParityAcrossFreshProcesses(t *testing.T) {
	// Query timings/cache-hit telemetry are not proof facts or diagnostics and
	// naturally differ on a semantic cache hit. Keep every actual proof/goal/
	// known-fact and diagnostic line byte-exact, excluding only that SMT summary.
	proofTranscript := func(text string) string {
		lines := []string{}
		for _, line := range strings.Split(diskDiagnosticTranscript(text), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "SMT tier:") {
				continue
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "proof.elisa")
	text := `law Positive(self: i64) = self > 0

def need(value: i64 is Positive) -> i64:
    return value

def main() -> i64:
    return need(3)
`
	if err := os.WriteFile(source, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "", "ELISACORE_BUILD_CACHE": "1", "ELISACORE_RETAINED_SERVER": "", "ELISACORE_ANALYSIS_BOOTSTRAP": "", "ELISACORE_ANALYSIS_BOOTSTRAP_DECLS": "", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1"}
	for _, setting := range []string{"-strict", "-permissive"} {
		cachedOut, coldOut := filepath.Join(dir, "cached"+setting+".ll"), filepath.Join(dir, "cold"+setting+".ll")
		args := []string{"-emit", "llvm", "--explain", "--explain-hole", setting, "-o", cachedOut, source}
		first, firstCode := runDiskCLIChildResult(t, env, args...)
		warm, warmCode := runDiskCLIChildResult(t, env, args...)
		coldEnv := map[string]string{}
		for key, value := range env {
			coldEnv[key] = value
		}
		coldEnv["ELISACORE_ANALYSIS_CACHE"] = "0"
		coldEnv["ELISACORE_BUILD_CACHE"] = "0"
		cold, coldCode := runDiskCLIChildResult(t, coldEnv, "-emit", "llvm", "--explain", "--explain-hole", setting, "-o", coldOut, source)
		if firstCode != 0 || warmCode != 0 || coldCode != 0 || !strings.Contains(warm, "reuse unchanged revision") || !strings.Contains(warm, "refinement proof report") {
			t.Fatalf("default proof cache path not exercised (%s): %d/%d/%d %s", setting, firstCode, warmCode, coldCode, warm)
		}
		if proofTranscript(first) != proofTranscript(cold) || proofTranscript(warm) != proofTranscript(cold) {
			t.Fatalf("proof/diagnostic transcript differs under actual default cache (%s): first=%s warm=%s cold=%s", setting, first, warm, cold)
		}
		cached, err := os.ReadFile(cachedOut)
		if err != nil {
			t.Fatal(err)
		}
		uncached, err := os.ReadFile(coldOut)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(cached, uncached) {
			t.Fatal("default cached proof fixture changed LLVM output")
		}
	}
	states, _ := filepath.Glob(filepath.Join(dir, "cache", "analysis", "*.state"))
	if len(states) != 2 {
		t.Fatalf("strict/permissive invocation authorities did not retain separate cache identities: %d", len(states))
	}
	// A genuinely refuted obligation must preserve ordinary strict failure too.
	if err := os.WriteFile(source, []byte(strings.Replace(text, "need(3)", "need(-3)", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	got, gotCode := runDiskCLIChildResult(t, env, "-emit", "llvm", "-strict", "-o", filepath.Join(dir, "bad-default.ll"), source)
	coldEnv := map[string]string{}
	for key, value := range env {
		coldEnv[key] = value
	}
	coldEnv["ELISACORE_ANALYSIS_CACHE"] = "0"
	coldEnv["ELISACORE_BUILD_CACHE"] = "0"
	want, wantCode := runDiskCLIChildResult(t, coldEnv, "-emit", "llvm", "-strict", "-o", filepath.Join(dir, "bad-cold.ll"), source)
	if gotCode == 0 || gotCode == analysisIncompleteExitCode || gotCode != wantCode || diskDiagnosticTranscript(got) != diskDiagnosticTranscript(want) {
		t.Fatalf("default strict rejection differs: %d/%d %s / %s", gotCode, wantCode, got, want)
	}
}

// This child genuinely removes the selection variable rather than assigning
// CACHE="". Parent isolation must not leak into the shipping default policy.
func TestDefaultDiskTrulyUnsetFreshChildBypassesParentIsolation(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "tiny.elisa")
	out := filepath.Join(dir, "tiny.ll")
	if err := os.WriteFile(source, []byte("def main() -> i64:\n    return 7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	controls := map[string]bool{"ELISACORE_ANALYSIS_CACHE": true, "ELISACORE_BUILD_CACHE": true, "ELISACORE_RETAINED_SERVER": true, "ELISACORE_ANALYSIS_BOOTSTRAP": true, "ELISACORE_ANALYSIS_BOOTSTRAP_DECLS": true, "ELISACORE_ANALYSIS_BOOTSTRAP_SECONDS": true, "ELISAC_BUILD_CACHE_DIR": true, "ELISACORE_ANALYSIS_CACHE_DEBUG": true, "TEST_BUILD_CACHE_MAIN": true, "ELISACORE_TEST_NATIVE_WORKERS": true}
	run := func() string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBuildObjectCacheMainAcrossProcesses$", "--", "-emit", "llvm", "-o", out, source)
		for _, entry := range os.Environ() {
			name, _, _ := strings.Cut(entry, "=")
			if !controls[name] {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "TEST_BUILD_CACHE_MAIN=1", "ELISACORE_TEST_NATIVE_WORKERS=1", "ELISACORE_BUILD_CACHE=1", "ELISACORE_RETAINED_SERVER=", "ELISAC_BUILD_CACHE_DIR="+filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE_DEBUG=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("true-unset production child failed: %v %s", err, output)
		}
		return string(output)
	}
	first := run()
	if !strings.Contains(first, "bootstrap complete") || strings.Contains(first, "build incomplete") {
		t.Fatalf("parent isolation disabled actual unset child policy: %s", first)
	}
	second := run()
	if !strings.Contains(second, "reuse unchanged revision") {
		t.Fatalf("unset child did not reuse normal semantic state: %s", second)
	}
}
