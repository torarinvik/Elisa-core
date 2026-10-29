package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func environmentWithOverrides(overrides map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[name]; !replaced {
			env = append(env, entry)
		}
	}
	for name, value := range overrides {
		env = append(env, name+"="+value)
	}
	return env
}

func clearAnalysisDiskObserverEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range analysisDiskRequestObserverVariables {
		t.Setenv(name, "")
	}
	for _, name := range []string{
		"ELISACORE_ANALYSIS_CACHE_DEBUG",
		"ELISACORE_BUILD_TIMING",
		"ELISACORE_CPUPROFILE",
		"ELISACORE_MEMPROFILE",
		"ELISACORE_EMIT_LL",
		"ELISACORE_LEXER_CAPACITY_REPORT",
		"ELISACORE_RETAINED_DEBUG_SPANS",
		"ELISACORE_RETAINED_FAST_DEBUG",
		"ELISACORE_RETAINED_FAST_PREPARE",
		"ELISACORE_TEST_TIMING",
		"ELISACORE_TEST_PHASE_DEBUG",
		"ELISACORE_KEEP_TEST_TEMP",
		"ELISA_BACKEND_PARITY_OUT",
	} {
		t.Setenv(name, "")
	}
}

func TestAnalysisDiskRequestObserverPredicate(t *testing.T) {
	clearAnalysisDiskObserverEnvironment(t)
	if analysisDiskRequestObserverEnabled() {
		t.Fatal("empty observer settings bypassed the semantic cache")
	}
	for _, name := range analysisDiskRequestObserverVariables {
		t.Run("semantic/"+name, func(t *testing.T) {
			clearAnalysisDiskObserverEnvironment(t)
			t.Setenv(name, "1")
			if !analysisDiskRequestObserverEnabled() || analysisDiskRequestObserver() != name {
				t.Fatalf("%s did not request semantic-cache bypass", name)
			}
			if !retainedRequestRequiresLocalProcess() {
				t.Fatalf("%s did not request local execution around a retained server", name)
			}
		})
	}
	for _, name := range []string{
		"ELISACORE_ANALYSIS_CACHE_DEBUG",
		"ELISACORE_BUILD_TIMING",
		"ELISACORE_CPUPROFILE",
		"ELISACORE_MEMPROFILE",
		"ELISACORE_EMIT_LL",
		"ELISACORE_LEXER_CAPACITY_REPORT",
		"ELISACORE_RETAINED_DEBUG_SPANS",
		"ELISACORE_RETAINED_FAST_DEBUG",
		"ELISA_BACKEND_PARITY_OUT",
	} {
		t.Run("not-semantic/"+name, func(t *testing.T) {
			clearAnalysisDiskObserverEnvironment(t)
			t.Setenv(name, "1")
			if analysisDiskRequestObserverEnabled() {
				t.Fatalf("%s incorrectly bypassed semantic cache", name)
			}
		})
	}
	t.Run("retained-fast-prepare-shadow", func(t *testing.T) {
		clearAnalysisDiskObserverEnvironment(t)
		t.Setenv("ELISACORE_RETAINED_FAST_PREPARE", "shadow")
		if analysisDiskRequestObserver() != "ELISACORE_RETAINED_FAST_PREPARE" {
			t.Fatal("retained fast-prepare shadow mode did not bypass semantic cache")
		}
		if !retainedRequestRequiresLocalProcess() {
			t.Fatal("retained fast-prepare shadow mode did not force local execution")
		}
		if !buildCacheRequestObserverEnabled() || !testRunnerCacheObserverEnabled() {
			t.Fatal("retained fast-prepare shadow mode did not bypass build/test-runner caches")
		}
	})
	for _, mode := range []string{"on", "off"} {
		t.Run("retained-fast-prepare-"+mode, func(t *testing.T) {
			clearAnalysisDiskObserverEnvironment(t)
			t.Setenv("ELISACORE_RETAINED_FAST_PREPARE", mode)
			if analysisDiskRequestObserverEnabled() || retainedRequestRequiresLocalProcess() {
				t.Fatalf("retained fast-prepare %s unexpectedly requested observer routing", mode)
			}
			if buildCacheRequestObserverEnabled() || testRunnerCacheObserverEnabled() {
				t.Fatalf("retained fast-prepare %s unexpectedly bypassed build/test-runner caches", mode)
			}
		})
	}
	clearAnalysisDiskObserverEnvironment(t)
	if retainedRequestRequiresLocalProcess() {
		t.Fatal("empty observer settings unexpectedly forced local execution")
	}
	t.Setenv("ELISA_BACKEND_PARITY_OUT", "backend.tsv")
	if analysisDiskRequestObserverEnabled() {
		t.Fatal("backend parity incorrectly classified as a semantic-cache observer")
	}
	if !retainedRequestRequiresLocalProcess() {
		t.Fatal("backend parity did not force local execution for startup-captured lexer state")
	}
}

func TestAnalysisDiskSemanticParityObserverBypassesWarmState(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "observer.elisa")
	parityPath := filepath.Join(dir, "semantic-parity.tsv")
	const source = "def main() -> i32:\n    return 0\n"
	if err := os.WriteFile(sourcePath, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-emit", "llvm", "-o", filepath.Join(dir, "out.ll"), sourcePath}
	baseEnv := map[string]string{
		"ELISAC_BUILD_CACHE_DIR":         filepath.Join(dir, "cache"),
		"ELISACORE_ANALYSIS_CACHE":       "1",
		"ELISACORE_ANALYSIS_CACHE_DEBUG": "1",
		"ELISACORE_BUILD_CACHE":          "0",
		"ELISACORE_RETAINED_SERVER":      "",
		"ELISA_BACKEND_PARITY_OUT":       "",
	}
	for _, name := range analysisDiskRequestObserverVariables {
		baseEnv[name] = ""
	}
	runDiskCLIChild(t, baseEnv, args...)
	states, err := filepath.Glob(filepath.Join(dir, "cache", "analysis", "*.state"))
	if err != nil || len(states) != 1 {
		t.Fatalf("expected one primed semantic state, got %v (err=%v)", states, err)
	}

	observerEnv := make(map[string]string, len(baseEnv)+1)
	for name, value := range baseEnv {
		observerEnv[name] = value
	}
	observerEnv["ELISA_SEMANTIC_INTERNAL_PARITY_OUT"] = parityPath
	output := runDiskCLIChild(t, observerEnv, args...)
	if !strings.Contains(output, "[analysis-disk] bypass request observer ELISA_SEMANTIC_INTERNAL_PARITY_OUT") || strings.Contains(output, "[analysis-disk] restore") {
		t.Fatalf("warm state suppressed or ignored the semantic observer: %s", output)
	}
	rows, err := os.ReadFile(parityPath)
	if err != nil {
		t.Fatalf("semantic parity side effect was not emitted: %v", err)
	}
	line := strings.TrimSuffix(string(rows), "\n")
	fields := strings.Split(line, "\t")
	if len(fields) != 7 {
		t.Fatalf("unexpected semantic parity record shape: %q", rows)
	}
	recordedSource, err := base64.StdEncoding.DecodeString(fields[4])
	if err != nil {
		t.Fatalf("decode semantic parity source: %v", err)
	}
	canonicalPath, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	expectedSource := fmt.Sprintf("#line 1 %s\n%s", filepath.Clean(canonicalPath), source)
	if string(recordedSource) != expectedSource {
		t.Fatalf("semantic parity source mismatch:\n got: %q\nwant: %q", recordedSource, expectedSource)
	}
}

func TestRetainedServerObserverRequestRunsLocally(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "server-observer.elisa")
	parityPath := filepath.Join(dir, "backend-parity.tsv")
	socketDir, err := os.MkdirTemp("/tmp", "elisa-observer-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(socketDir, "r.sock")
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	serverLogPath := filepath.Join(dir, "retained.log")
	const source = "def main() -> i32:\n    return 0\n"
	if err := os.WriteFile(sourcePath, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	serverLog, err := os.OpenFile(serverLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer serverLog.Close()
	server := exec.Command(os.Args[0], "-test.run=^TestBuildObjectCacheMainAcrossProcesses$", "--", "-emit", emitRetainedServe, "-addr", socket)
	serverOverrides := map[string]string{
		"TEST_BUILD_CACHE_MAIN":              "1",
		"ELISACORE_RETAINED_SERVER":          "",
		"ELISACORE_ANALYSIS_CACHE":           "0",
		"ELISACORE_BUILD_CACHE":              "0",
		"ELISACORE_RETAINED_IDLE":            "1h",
		"ELISA_SEMANTIC_INTERNAL_PARITY_OUT": "",
		"ELISA_BACKEND_PARITY_OUT":           "",
	}
	for _, name := range analysisDiskRequestObserverVariables {
		serverOverrides[name] = ""
	}
	server.Env = environmentWithOverrides(serverOverrides)
	server.Stdout, server.Stderr = serverLog, serverLog
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = server.Process.Kill()
		_ = server.Wait()
	})
	if !waitForRetained(socket) {
		log, _ := os.ReadFile(serverLogPath)
		t.Fatalf("retained server did not start:\n%s", log)
	}

	requestEnv := map[string]string{
		"ELISACORE_RETAINED_SERVER": socket,
		"ELISACORE_ANALYSIS_CACHE":  "0",
		"ELISACORE_BUILD_CACHE":     "0",
		"ELISA_BACKEND_PARITY_OUT":  parityPath,
	}
	for _, name := range analysisDiskRequestObserverVariables {
		requestEnv[name] = ""
	}
	runDiskCLIChild(t, requestEnv, "-emit", "llvm", "-o", filepath.Join(dir, "out.ll"), sourcePath)
	rows, err := os.ReadFile(parityPath)
	if err != nil {
		t.Fatalf("observer request was forwarded to a server whose lexer observer was disabled at startup: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("backend parity observer emitted an empty record")
	}
	line := strings.TrimSuffix(string(rows), "\n")
	fields := strings.Split(line, "\t")
	if len(fields) != 5 {
		t.Fatalf("unexpected backend parity record shape: %q", rows)
	}
	recordedSource, err := base64.StdEncoding.DecodeString(fields[3])
	if err != nil {
		t.Fatalf("decode backend parity source: %v", err)
	}
	canonicalPath, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	expectedSource := fmt.Sprintf("#line 1 %s\n%s", filepath.Clean(canonicalPath), source)
	if string(recordedSource) != expectedSource {
		t.Fatalf("server observer request used unexpected source:\n got: %q\nwant: %q", recordedSource, expectedSource)
	}
}

func TestAnalysisDiskRegionObserverBypassesWarmObjectAndSemanticCaches(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "region.elisa")
	const source = "def main() -> i32:\n    region scratch(64):\n        values: mutable darray[i64] = []\n        return 0\n"
	if err := os.WriteFile(sourcePath, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-emit", "obj", "-o", filepath.Join(dir, "region.o"), sourcePath}
	baseEnv := map[string]string{
		"ELISAC_BUILD_CACHE_DIR":         filepath.Join(dir, "cache"),
		"ELISACORE_ANALYSIS_CACHE":       "1",
		"ELISACORE_ANALYSIS_CACHE_DEBUG": "",
		"ELISACORE_BUILD_CACHE":          "1",
		"ELISACORE_TEST_CACHE_DEBUG":     "1",
		"ELISACORE_RETAINED_SERVER":      "",
		"ELISACORE_BUILD_TIMING":         "",
		"ELISACORE_CPUPROFILE":           "",
		"ELISACORE_MEMPROFILE":           "",
		"ELISACORE_EMIT_LL":              "",
		"ELISA_BACKEND_PARITY_OUT":       "",
	}
	for _, name := range analysisDiskRequestObserverVariables {
		baseEnv[name] = ""
	}
	output := runDiskCLIChild(t, baseEnv, args...)
	if !strings.Contains(output, "build-object miss-store") {
		t.Fatalf("first compile did not prime the object cache: %s", output)
	}
	states, err := filepath.Glob(filepath.Join(dir, "cache", "analysis", "*.state"))
	if err != nil || len(states) != 1 {
		t.Fatalf("expected one primed semantic state, got %v (err=%v)", states, err)
	}
	output = runDiskCLIChild(t, baseEnv, args...)
	if !strings.Contains(output, "build-object hit") {
		t.Fatalf("unchanged ordinary request did not hit the primed object cache: %s", output)
	}

	observerEnv := make(map[string]string, len(baseEnv))
	for name, value := range baseEnv {
		observerEnv[name] = value
	}
	observerEnv["ELISA_REGION_DEBUG"] = "1"
	output = runDiskCLIChild(t, observerEnv, args...)
	if !strings.Contains(output, "REGIONSTAMP darray -> scratch") {
		t.Fatalf("warm caches suppressed the semantic region observer: %s", output)
	}
	if strings.Contains(output, "build-object hit") {
		t.Fatalf("observer request reused a layer that would suppress its output: %s", output)
	}
}
