//go:build cgo

package semantic

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSMTProofCacheStorage(t *testing.T) {
	cache := &smtProofCache{directory: t.TempDir(), identity: "compiler+solver+options"}
	query := "(declare-const x Int)\n(assert (< x x))\n"
	if cache.contains(query) {
		t.Fatal("cold hit")
	}
	cache.publish(query)
	if !cache.contains(query) {
		t.Fatal("missing warm proof")
	}
	if cache.contains(query + " ") {
		t.Fatal("query bytes ignored")
	}
	other := &smtProofCache{directory: cache.directory, identity: "changed compiler or solver or options"}
	if other.contains(query) {
		t.Fatal("identity ignored")
	}
	path := cache.path(cache.key(query))
	for _, corrupt := range []string{"", "{}", "not json", strings.Repeat("x", 4097), `{"Schema":"elisa-exact-smt-proof-v1","Verdict":"sat"}`} {
		if err := os.WriteFile(path, []byte(corrupt), 0600); err != nil {
			t.Fatal(err)
		}
		if cache.contains(query) {
			t.Fatal("corrupt proof admitted")
		}
		cache.publish(query)
		if !cache.contains(query) {
			t.Fatal("corruption not recoverable")
		}
	}
	// An unusable cache must remain optional.
	cache.directory = filepath.Join(path, "not-a-directory")
	cache.publish(query)
	if cache.contains(query) {
		t.Fatal("unusable cache hit")
	}
}

const persistentProofSource = `law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type Small = i64 is Bounded[2, 10]
def mul(a: Small, b: Small) -> i64 is Bounded[4, 100]:
    return a * b
`

func TestSMTProofCacheProcessHelper(t *testing.T) {
	if os.Getenv("ELISA_TEST_SMT_PROOF_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	r := analyzeWithSMT(t, "persistent_proof.elisa", persistentProofSource)
	if len(r.Errors()) != 0 {
		t.Fatal(allDiagnostics(r))
	}
	fmt.Printf("persistent-hits=%d\n", r.SMTProfile.PersistentCacheHits)
	fmt.Printf("session-spawn-ns=%d\n", r.SMTProfile.SpawnTime.Nanoseconds())
	fmt.Printf("solver-proven=%d\n", r.SMTProfile.SolverProven)
}

func TestSMTProofCacheAcrossProcesses(t *testing.T) {
	t.Setenv("ELISACORE_SMT_CACHE_DIR", t.TempDir())
	t.Setenv("ELISA_TEST_SMT_PROOF_CHILD", "1")
	for _, warm := range []bool{false, true} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSMTProofCacheProcessHelper$", "-test.v")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child: %v\n%s", err, output)
		}
		result := string(output)
		zero := strings.Contains(result, "persistent-hits=0\n")
		if zero == warm {
			t.Fatalf("warm=%v: %s", warm, output)
		}
		if warm {
			if !strings.Contains(result, "session-spawn-ns=0\n") || !strings.Contains(result, "solver-proven=0\n") {
				t.Fatalf("warm persistent proofs opened or queried a solver session: %s", output)
			}
		} else if strings.Contains(result, "solver-proven=0\n") {
			t.Fatalf("cold persistent-cache baseline did not prove with the solver: %s", output)
		}
	}
	// The same names with a changed obligation must not inherit a previous proof.
	bad := strings.Replace(persistentProofSource, "return a * b", "return a * b + 1", 1)
	r := analyzeWithSMT(t, "persistent_proof.elisa", bad)
	if r.SMTProfile.Declined == 0 {
		t.Fatalf("changed false obligation reused an old proof: %+v", r.SMTProfile)
	}
	dependencyEdit := strings.Replace(persistentProofSource, "type Small = i64 is Bounded[2, 10]", "type Small = i64 is Bounded[2, 11]", 1)
	changed := analyzeWithSMT(t, "persistent_proof.elisa", dependencyEdit)
	if changed.SMTProfile.Declined == 0 {
		t.Fatal("changed dependency inherited an old proof")
	}
}

func TestSMTProofCacheSolverAndBudgetIdentity(t *testing.T) {
	directory := t.TempDir()
	solver := filepath.Join(directory, "solver")
	writeSolver := func(body string) {
		t.Helper()
		if err := os.WriteFile(solver, []byte("#!/bin/sh\nprintf '%s\\n' 'solver-version'\n#"+body+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeSolver("first")
	first := newSMTProofCache(directory, solver)
	if first == nil {
		t.Fatal("identity unavailable")
	}
	first.publish("query")
	writeSolver("second")
	replaced := newSMTProofCache(directory, solver)
	if replaced == nil || replaced.contains("query") {
		t.Fatal("same-path solver replacement ignored")
	}
	replaced.publish("query")
	t.Setenv("ELISA_SMT_RLIMIT", "1234")
	rebudgeted := newSMTProofCache(directory, solver)
	if rebudgeted == nil || rebudgeted.contains("query") {
		t.Fatal("budget change ignored")
	}
}

func TestSMTProofCachePinnedCompilerArtifact(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "compiler")
	if err := os.WriteFile(path, []byte("old compiler"), 0600); err != nil {
		t.Fatal(err)
	}
	opened, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	info, err := opened.Stat()
	if err != nil {
		t.Fatal(err)
	}
	first, err := smtProofOpenedArtifactHash(opened, info)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(directory, "new")
	if err := os.WriteFile(replacement, []byte("new compiler"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	retained, err := smtProofOpenedArtifactHash(opened, info)
	if err != nil || retained != first {
		t.Fatalf("running artifact changed on rename: %v", err)
	}
	fresh, err := smtProofArtifactHash(path)
	if err != nil || fresh == first {
		t.Fatal("replacement fixture unchanged")
	}
}

func TestSMTProofCacheConcurrentPublication(t *testing.T) {
	cache := &smtProofCache{directory: t.TempDir(), identity: "identity"}
	cache.publish("query")
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 20; j++ {
				cache.publish("query")
				if !cache.contains("query") {
					t.Error("partial publication observed")
				}
			}
		}()
	}
	workers.Wait()
}

func TestSMTProofCacheUnusableDirectoryDoesNotChangeProof(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ELISACORE_SMT_CACHE_DIR", path)
	r := analyzeWithSMT(t, "persistent_proof.elisa", persistentProofSource)
	if len(r.Errors()) != 0 || r.SMTProfile.Proven != 2 || r.SMTProfile.PersistentCacheHits != 0 {
		t.Fatalf("unusable cache changed results: %+v %v", r.SMTProfile, r.Errors())
	}
}

func TestSMTProofCacheVersionProbeIsBounded(t *testing.T) {
	directory := t.TempDir()
	solver := filepath.Join(directory, "solver")
	pidFile := filepath.Join(directory, "child.pid")
	script := "#!/bin/sh\nsleep 10 &\necho $! > '" + pidFile + "'\nwait\n"
	if err := os.WriteFile(solver, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	defer func() {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				child, err := os.FindProcess(pid)
				if err == nil {
					_ = child.Kill()
				}
			}
		}
	}()
	start := time.Now()
	cache := newSMTProofCache(directory, solver)
	if cache != nil {
		t.Fatal("stalled version probe admitted cache")
	}
	if elapsed := time.Since(start); elapsed > 7*time.Second {
		t.Fatalf("unbounded identity probe: %s", elapsed)
	}
}
