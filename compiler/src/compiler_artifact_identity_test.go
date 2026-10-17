package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"elisacore/src/backend"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPinnedCompilerArtifactNeverIdentifiesReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compiler")
	original := []byte("original compiler")
	if err := os.WriteFile(path, original, 0700); err != nil {
		t.Fatal(err)
	}
	artifact, err := pinCompilerArtifact(path)
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.file.Close()
	replacement := path + ".new"
	if err := os.WriteFile(replacement, []byte("replaced compiler"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	stamp, err := artifact.stamp()
	if err != nil {
		return
	} // Unlink metadata changes conservatively disable reuse.
	expected := sha256.Sum256(original)
	if stamp != hex.EncodeToString(expected[:]) {
		t.Fatal("identified replacement instead of pinned compiler")
	}
}

func TestPinnedCompilerArtifactRejectsInPlaceMutation(t *testing.T) {
	for _, warm := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-first-key", true: "after-first-key"}[warm], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "compiler")
			if err := os.WriteFile(path, []byte("compiler A"), 0700); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			artifact, err := pinCompilerArtifact(path)
			if err != nil {
				t.Fatal(err)
			}
			defer artifact.file.Close()
			if warm {
				if _, err := artifact.stamp(); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, []byte("compiler B"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			if _, err := artifact.stamp(); err == nil {
				t.Fatal("accepted modified compiler despite restored size/mtime")
			}
		})
	}
}

func TestPinnedCompilerArtifactStableConcurrentReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compiler")
	data := []byte("unchanged compiler")
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	artifact, err := pinCompilerArtifact(path)
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.file.Close()
	expected := sha256.Sum256(data)
	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			stamp, err := artifact.stamp()
			if err != nil || stamp != hex.EncodeToString(expected[:]) {
				t.Errorf("unexpected identity %q: %v", stamp, err)
			}
		}()
	}
	group.Wait()
}

func TestCompilerIdentityProcessHelper(t *testing.T) {
	if os.Getenv("ELISA_NATIVE_IDENTITY_HELPER") != "1" {
		return
	}
	fmt.Println("READY")
	if !bufio.NewScanner(os.Stdin).Scan() {
		t.Fatal("missing parent signal")
	}
	stamp, err := compilerSourceStamp()
	if err != nil {
		fmt.Println("DECLINED")
	} else {
		fmt.Println(stamp)
	}
	os.Exit(0)
}

func TestRunningCompilerIdentitySurvivesPathReplacementBeforeFirstKey(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(t.TempDir(), "compiler-test")
	if err := copyExecutableFile(executable, copied); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(copied)
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256(bytes)
	command := exec.Command(copied, "-test.run=^TestCompilerIdentityProcessHelper$")
	command.Env = append(os.Environ(), "ELISA_NATIVE_IDENTITY_HELPER=1")
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			command.Process.Kill()
			command.Wait()
		}
	}()
	reader := bufio.NewScanner(output)
	if !reader.Scan() || reader.Text() != "READY" {
		t.Fatalf("helper not ready: %s", stderr.String())
	}
	replacement := copied + ".new"
	if err := os.WriteFile(replacement, []byte("different installed executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, copied); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(input, "continue"); err != nil {
		t.Fatal(err)
	}
	input.Close()
	if !reader.Scan() {
		t.Fatal("helper omitted identity")
	}
	result := reader.Text()
	if err := command.Wait(); err != nil {
		t.Fatalf("helper failed: %v %s", err, stderr.String())
	}
	if result != "DECLINED" && result != hex.EncodeToString(expected[:]) {
		t.Fatalf("running compiler adopted replacement identity: %s", result)
	}
}

func TestTestRunnerUnavailableCompilerIdentityCompilesCold(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang unavailable")
	}
	t.Setenv("ELISACORE_TEST_CACHE", "1")
	t.Setenv("ELISACORE_TEST_CACHE_DEBUG", "1")
	previous := nativeCompilerArtifactErr
	nativeCompilerArtifactErr = errors.New("forced unavailable compiler identity")
	t.Cleanup(func() { nativeCompilerArtifactErr = previous })
	var diagnostics bytes.Buffer
	executable, cleanup, timing, _, _, err := compileTestRunnerExecutableWithShim(clang, "def main() -> int:\n\treturn 0\n", "", nil, nil, nil, backend.OptimizationLevel0, backend.DefaultPackedLoweringProfile(), "", false, false, &diagnostics)
	defer cleanup()
	if err != nil {
		t.Fatalf("identity failure prevented cold compilation: %v\n%s", err, diagnostics.String())
	}
	if !strings.Contains(diagnostics.String(), "forced unavailable compiler identity") {
		t.Fatal("test did not exercise unavailable-identity cache fallback")
	}
	if timing.CacheHit || timing.CachePublish != 0 {
		t.Fatal("unidentified compiler reused or published cache artifact")
	}
	if output, err := exec.Command(executable).CombinedOutput(); err != nil {
		t.Fatalf("cold executable failed: %v %s", err, output)
	}
}
