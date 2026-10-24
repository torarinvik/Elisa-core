package main

import (
	"bytes"
	"context"
	"elisacore/src/semantic"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func runDiskCLIChild(t *testing.T, overrides map[string]string, args ...string) string {
	t.Helper()
	output, code := runDiskCLIChildResult(t, overrides, args...)
	if code != 0 {
		t.Fatalf("compiler child exited %d:\n%s", code, output)
	}
	return output
}

func runDiskCLIChildResult(t *testing.T, overrides map[string]string, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	childArgs := append([]string{"-test.run=^TestBuildObjectCacheMainAcrossProcesses$", "--"}, args...)
	cmd := exec.CommandContext(ctx, os.Args[0], childArgs...)
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if _, replace := overrides[name]; !replace && name != "TEST_BUILD_CACHE_MAIN" && name != "ELISACORE_TEST_NATIVE_WORKERS" {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	for name, value := range overrides {
		cmd.Env = append(cmd.Env, name+"="+value)
	}
	cmd.Env = append(cmd.Env, "TEST_BUILD_CACHE_MAIN=1", "ELISACORE_TEST_NATIVE_WORKERS=1")
	output, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && ctx.Err() == nil {
			code = exit.ExitCode()
		} else {
			t.Fatalf("compiler child: %v\n%s", err, output)
		}
	}
	if strings.Contains(string(output), "[retained]") {
		t.Fatalf("standalone build contacted server: %s", output)
	}
	return string(output), code
}

func diskDiagnosticTranscript(text string) string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line != "" && !strings.HasPrefix(line, "[analysis-disk]") {
			lines = append(lines, line)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// Every revision runs in a fresh compiler process, so an in-memory analysis
// or daemon cannot authorize a hit. Compare generated IR and diagnostics with
// an explicitly cache-disabled fresh compile after each edit and revert.
func TestAnalysisDiskCacheFreshProcessEditOracle(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "program.elisa")
	cacheEnv := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": filepath.Join(dir, "nonexistent.sock")}
	coldEnv := map[string]string{"ELISACORE_ANALYSIS_CACHE": "0", "ELISACORE_ANALYSIS_CACHE_DEBUG": "0", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": ""}
	edits := [][2]string{
		{"return p.a + p.b", "return p.a + p.b + 1"},
		{"        a: i64\n        b: i64\n", "        a: i64\n        z: i64 = 0\n        b: i64\n"},
		{"ensure result == b\n", "ensure result == b and i < n\n"},
		{"can[Abort.Panic]:", "can[Abort.Panic, Console.Write]:"},
		{"    def width(p: Pair) -> i64:\n", "    def width(p: bool) -> i64:\n        return 1\n\n    def width(p: Pair) -> i64:\n"},
		{"def tag(x: i64) -> i64 can[Abort.Panic]:\n        return x", "def tag(x: i64) -> i32 can[Abort.Panic]:\n        return 1"},
		{"    def tag(x: i64) -> i64 can[Abort.Panic]:\n        return x\n", "    def tag(x: i64) -> i64 can[Abort.Panic]:\n        return x\n\n    def unused(x: i64) -> i64:\n        return x\n"},
	}
	sources := []string{retainedCodegenBase, retainedCodegenBase}
	for _, edit := range edits {
		changed := strings.Replace(retainedCodegenBase, edit[0], edit[1], 1)
		if changed == retainedCodegenBase {
			t.Fatal("edit did not apply")
		}
		sources = append(sources, changed, retainedCodegenBase)
	}
	for i, text := range sources {
		if err := os.WriteFile(source, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		cached, cold := filepath.Join(dir, "cached.ll"), filepath.Join(dir, "cold.ll")
		gotDiagnostics := runDiskCLIChild(t, cacheEnv, "-emit", "llvm", "-o", cached, source)
		if i > 0 && !strings.Contains(gotDiagnostics, "[analysis-disk] restore") {
			t.Fatalf("revision%d did not restore from disk: %s", i, gotDiagnostics)
		}
		if i > 0 && strings.Contains(gotDiagnostics, "cold analysis") {
			t.Fatalf("revision%d fell back: %s", i, gotDiagnostics)
		}
		if strings.Contains(gotDiagnostics, "not stored") {
			t.Fatalf("revision%d was not persisted: %s", i, gotDiagnostics)
		}
		wantDiagnostics := runDiskCLIChild(t, coldEnv, "-emit", "llvm", "-o", cold, source)
		got, err := os.ReadFile(cached)
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(cold)
		if err != nil {
			t.Fatal(err)
		}
		got = retainedGensym.ReplaceAll(got, []byte("__${1}_N"))
		want = retainedGensym.ReplaceAll(want, []byte("__${1}_N"))
		if !bytes.Equal(got, want) {
			t.Fatalf("revision%d IR mismatch: %s", i, firstLineDiff(string(got), string(want)))
		}
		if diskDiagnosticTranscript(gotDiagnostics) != diskDiagnosticTranscript(wantDiagnostics) {
			t.Fatalf("revision%d diagnostic mismatch\ngot:%s\nwant:%s", i, gotDiagnostics, wantDiagnostics)
		}
	}
}

func TestAnalysisDiskCacheFreshProcessNativeExecution(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "native.elisa")
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1", "ELISACORE_BUILD_CACHE": "1", "ELISACORE_RETAINED_SERVER": filepath.Join(dir, "nonexistent.sock")}
	for revision, value := range []string{"7", "7", "8", "7"} {
		var text strings.Builder
		for i := 0; i < 80; i++ {
			fmt.Fprintf(&text, "def unused_%d() -> i64:\n    return %d\n\n", i, i)
		}
		text.WriteString("def answer() -> i64:\n    return " + value + "\n\ndef main() -> i32:\n    return i32(answer())\n")
		if err := os.WriteFile(source, []byte(text.String()), 0600); err != nil {
			t.Fatal(err)
		}
		object := filepath.Join(dir, "native.o")
		output := runDiskCLIChild(t, env, "-emit", "obj", "-o", object, source)
		if revision > 0 && (!strings.Contains(output, "[analysis-disk] restore") || strings.Contains(output, "cold analysis")) {
			t.Fatalf("revision%d did not use disk state: %s", revision, output)
		}
		if strings.Contains(output, "not stored") {
			t.Fatalf("revision%d failed to persist: %s", revision, output)
		}
		executable := filepath.Join(dir, "native.exe")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		linked, err := exec.CommandContext(ctx, "clang", object, "-o", executable).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("link: %v %s", err, linked)
		}
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		err = exec.CommandContext(ctx, executable).Run()
		cancel()
		status := 0
		if exit, ok := err.(*exec.ExitError); ok {
			status = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		expected, _ := strconv.Atoi(value)
		if status != expected {
			t.Fatalf("revision%d exit=%d want=%d", revision, status, expected)
		}
	}
}

func TestAnalysisDiskCacheKeyTracksSemanticInputs(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "program.elisa")
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	program := &loadedProgram{filename: filename, source: []byte("def main() -> i32:\n    return 0\n"), sourceFiles: []string{filename}}
	options := semantic.AnalyzeOptions{}
	key := func() string {
		t.Helper()
		a, ok := analysisDiskArtifactFor(program, options)
		if !ok {
			t.Fatal("no analysis cache key")
		}
		return a.key
	}
	original := key()
	program.source = []byte("def main() -> i32:\n    return 1\n")
	if key() != original {
		t.Fatal("body edit discarded reusable analysis state")
	}
	options.EnforceStrictProofs = true
	if key() == original {
		t.Fatal("proof policy omitted from analysis identity")
	}
	options.EnforceStrictProofs = false
	t.Setenv("ELISACORE_FORCE_BOUNDS_CHECK", "1")
	if key() == original {
		t.Fatal("environment input omitted")
	}
	t.Setenv("ELISACORE_FORCE_BOUNDS_CHECK", "")
	reset := key()
	program.sourceFiles = []string{filepath.Join(dir, "different.elisa")}
	if key() == reset {
		t.Fatal("source authority omitted")
	}
	solver := filepath.Join(dir, "solver")
	t.Setenv("ELISA_Z3", solver)
	options.EnableSMT = true
	if err := os.WriteFile(solver, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	initialSolver := key()
	if err := os.WriteFile(solver, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if key() == initialSolver {
		t.Fatal("same-path solver replacement omitted")
	}
}

func TestAnalysisDiskCacheCorruptionRebuildsSafely(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "program.elisa")
	if err := os.WriteFile(source, []byte("def main() -> i32:\n    return 7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": ""}
	args := []string{"-emit", "llvm", "-o", filepath.Join(dir, "out.ll"), source}
	runDiskCLIChild(t, env, args...)
	paths, err := filepath.Glob(filepath.Join(dir, "cache", "analysis", "*.state"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("cache paths=%v err=%v", paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(paths[0], data, 0600); err != nil {
		t.Fatal(err)
	}
	output := runDiskCLIChild(t, env, args...)
	if !strings.Contains(output, "state checksum mismatch") || !strings.Contains(output, "cold analysis") || strings.Contains(output, "not stored") {
		t.Fatalf("corrupt state not repaired: %s", output)
	}
	output = runDiskCLIChild(t, env, args...)
	if !strings.Contains(output, "[analysis-disk] restore") || strings.Contains(output, "cold analysis") {
		t.Fatalf("repaired state did not restore: %s", output)
	}
}

func TestAnalysisDiskCacheRetractsProofAfterContractEdit(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "contracts.elisa")
	before := `def pick(x: u8, y: u8) -> u8:
    ensure result == x
    r: mutable u8 = x
    for i in 0 ..< 3:
        r = x
    return r

def caller(x: u8, y: u8) -> u8:
    ensure result == x
    return pick(x, y)
`
	after := strings.Replace(before, "    ensure result == x\n    r:", "    ensure result == x or result == y\n    r:", 1)
	cachedEnv := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": filepath.Join(dir, "absent.sock")}
	coldEnv := map[string]string{"ELISACORE_ANALYSIS_CACHE": "0", "ELISACORE_ANALYSIS_CACHE_DEBUG": "0", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": ""}
	args := []string{"-emit", "llvm", "-o", filepath.Join(dir, "out.ll"), source}
	for revision, text := range []string{before, after, before, after} {
		if err := os.WriteFile(source, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		got, code := runDiskCLIChildResult(t, cachedEnv, args...)
		want, wantCode := runDiskCLIChildResult(t, coldEnv, args...)
		expected := 0
		if text == after {
			expected = 1
		}
		if code != expected || wantCode != expected {
			t.Fatalf("revision%d acceptance cached=%d cold=%d want=%d\n%s\n%s", revision, code, wantCode, expected, got, want)
		}
		if revision > 0 && (!strings.Contains(got, "[analysis-disk] restore") || strings.Contains(got, "cold analysis")) {
			t.Fatalf("revision%d did not restore: %s", revision, got)
		}
		if strings.Contains(got, "not stored") {
			t.Fatalf("revision%d was not persisted: %s", revision, got)
		}
		if diskDiagnosticTranscript(got) != diskDiagnosticTranscript(want) {
			t.Fatalf("revision%d diagnostic mismatch\n%s\nwant:\n%s", revision, got, want)
		}
	}
}

func TestAnalysisDiskCacheUnchangedRevisionDoesNotRewriteState(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "program.elisa")
	if err := os.WriteFile(source, []byte("def main() -> i32:\n    return 7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": ""}
	args := []string{"-emit", "llvm", "-o", filepath.Join(dir, "out.ll"), source}
	runDiskCLIChild(t, env, args...)
	paths, err := filepath.Glob(filepath.Join(dir, "cache", "analysis", "*.state"))
	if err != nil || len(paths) != 1 {
		t.Fatal("no unique saved state", paths, err)
	}
	before, err := os.Stat(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	output := runDiskCLIChild(t, env, args...)
	after, err := os.Stat(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "reuse unchanged revision") || strings.Contains(output, "[analysis-disk] store") {
		t.Fatalf("unchanged state was republished: %s", output)
	}
	if !before.ModTime().Equal(after.ModTime()) || !bytes.Equal(content, current) {
		t.Fatal("unchanged state file changed")
	}
}

func TestAnalysisDiskCachePreservesProtocolEffectRejection(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "protocol.elisa")
	out := filepath.Join(dir, "out.ll")
	base := `struct Net:
    fd: i64
protocol Sender:
    def send(self: Self, n: i64) -> void can[Atomics.Load]
impl Sender for Net:
    def send(self: Net, n: i64) -> void can[Atomics.Load, Atomics.Store]:
        return
def helper() -> i32:
    return 1
def main() -> i32:
    return helper()
`
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": filepath.Join(dir, "missing.sock")}
	cold := map[string]string{"ELISACORE_ANALYSIS_CACHE": "0", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": ""}
	edited := strings.Replace(base, "return 1", "return 2", 1)
	for i, text := range []string{base, base, edited, "\n\n" + edited} {
		if err := os.WriteFile(source, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		got, code := runDiskCLIChildResult(t, env, "-emit", "llvm", "-o", out, source)
		want, wantCode := runDiskCLIChildResult(t, cold, "-emit", "llvm", "-o", out, source)
		if code == 0 || wantCode == 0 || !strings.Contains(got, "Atomics.Store") || diskDiagnosticTranscript(got) != diskDiagnosticTranscript(want) {
			t.Fatalf("step %d protocol rejection differs:\ngot=%s\nwant=%s", i, got, want)
		}
		if i >= 2 && (!strings.Contains(got, "[analysis-disk] update") || strings.Contains(got, "fallback")) {
			t.Fatalf("step %d did not exercise retained protocol replay: %s", i, got)
		}
	}
}

func TestAnalysisDiskCacheReplaysEditsWithoutRewritingBaseline(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "program.elisa")
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": ""}
	base := "def helper(x: i64) -> i64:\n    return x + 1\n\ndef main() -> i64:\n    return helper(6)\n"
	args := []string{"-emit", "llvm", "-o", filepath.Join(dir, "cached.ll"), source}
	if err := os.WriteFile(source, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	runDiskCLIChild(t, env, args...)
	paths, _ := filepath.Glob(filepath.Join(dir, "cache", "analysis", "*.state"))
	if len(paths) != 1 {
		t.Fatal("no unique baseline", paths)
	}
	baseline, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(base, "x + 1", "x + 2", 1)
	added := strings.Replace(edited, "return helper(6)", "return extra(helper(6))", 1) + "\ndef extra(x: i64) -> i64:\n    return x + 4\n"
	cumulative := strings.Replace(strings.Replace(added, "x + 2", "x + 3", 1), "x + 4", "x + 5", 1)
	for _, text := range []string{edited, edited, added, cumulative, edited, base} {
		if err := os.WriteFile(source, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		output := runDiskCLIChild(t, env, args...)
		if strings.Contains(output, "[analysis-disk] store") || strings.Contains(output, "fallback") || strings.Contains(output, "cold analysis") {
			t.Fatalf("did not replay baseline: %s", output)
		}
		marker := "replay baseline"
		if text == base {
			marker = "reuse unchanged revision"
		}
		if !strings.Contains(output, marker) {
			t.Fatalf("missing %s: %s", marker, output)
		}
		current, err := os.ReadFile(paths[0])
		if err != nil {
			t.Fatal(err)
		}
		after, err := os.Stat(paths[0])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(current, baseline) || !after.ModTime().Equal(info.ModTime()) {
			t.Fatal("source edit rewrote admitted baseline")
		}
		cold := map[string]string{"ELISACORE_ANALYSIS_CACHE": "0", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": ""}
		coldPath := filepath.Join(dir, "cold.ll")
		wantDiagnostics := runDiskCLIChild(t, cold, "-emit", "llvm", "-o", coldPath, source)
		got, err := os.ReadFile(filepath.Join(dir, "cached.ll"))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(coldPath)
		if err != nil {
			t.Fatal(err)
		}
		got = retainedGensym.ReplaceAll(got, []byte("__${1}_N"))
		want = retainedGensym.ReplaceAll(want, []byte("__${1}_N"))
		if !bytes.Equal(got, want) || diskDiagnosticTranscript(output) != diskDiagnosticTranscript(wantDiagnostics) {
			t.Fatal("baseline replay differed from cold compilation")
		}
	}
}

// Each request starts a new process. A member reorder must regenerate the
// current constants and body IR while retaining the admitted analysis baseline.
func TestAnalysisDiskCacheConstantEnumMemberEdits(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "program.elisa")
	cachedPath, coldPath := filepath.Join(dir, "cached.ll"), filepath.Join(dir, "cold.ll")
	env := map[string]string{"ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "1", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": ""}
	base := "const enum Choice of u8:\n    First\n    Second\n\ndef main() -> i64:\n    match Choice.First:\n        Choice.First:\n            return 41\n        Choice.Second:\n            return 42\n"
	if err := os.WriteFile(source, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	runDiskCLIChild(t, env, "-emit", "llvm", "-o", cachedPath, source)
	paths, _ := filepath.Glob(filepath.Join(dir, "cache", "analysis", "*.state"))
	if len(paths) != 1 {
		t.Fatal("no unique baseline", paths)
	}
	baseline, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	reordered := strings.Replace(base, "    First\n    Second\n", "    Second\n    First\n", 1)
	renamed := strings.ReplaceAll(reordered, "Second", "Last")
	for _, text := range []string{reordered, renamed, reordered, base} {
		if err := os.WriteFile(source, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		output := runDiskCLIChild(t, env, "-emit", "llvm", "-o", cachedPath, source)
		marker := "replay baseline"
		if text == base {
			marker = "reuse unchanged revision"
		}
		if !strings.Contains(output, marker) || strings.Contains(output, "fallback") {
			t.Fatalf("did not reuse baseline: %s", output)
		}
		cold := map[string]string{"ELISACORE_ANALYSIS_CACHE": "0", "ELISACORE_BUILD_CACHE": "0", "ELISACORE_RETAINED_SERVER": ""}
		coldOutput := runDiskCLIChild(t, cold, "-emit", "llvm", "-o", coldPath, source)
		got, err := os.ReadFile(cachedPath)
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(coldPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(retainedGensym.ReplaceAll(got, []byte("__${1}_N")), retainedGensym.ReplaceAll(want, []byte("__${1}_N"))) || diskDiagnosticTranscript(output) != diskDiagnosticTranscript(coldOutput) {
			t.Fatal("constant enum replay differs from cold compilation")
		}
		current, err := os.ReadFile(paths[0])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(current, baseline) {
			t.Fatal("member edit rewrote baseline")
		}
	}
}
