//go:build cgo && (darwin || linux || freebsd)

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type responseIdentityProbe struct {
	Kind, Skip, Response, Compiler string
	Declined, OK                   bool
	Status, PID                    int
	Analyses, ResponseHits, Builds uint64
}

func TestCompilerResponsePinnedImageHelper(t *testing.T) {
	if os.Getenv("COMPILER_RESPONSE_IMAGE_HELPER") != "1" {
		return
	}
	cache := newCompileServerSessionCache(4)
	req := compileServerRequest{Mode: "obj", Filename: filepath.Join(filepath.Dir(os.Args[0]), "unit.elisa"), Source: "def main() -> i64:\n    return 7\n", Incremental: true}
	encoder := json.NewEncoder(os.Stdout)
	program, err := loadedProgramFromRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, admitted := serverNativeObjectIdentity(req, program, cache); !admitted {
		encoder.Encode(responseIdentityProbe{Kind: "ready", Skip: "requires audited native cache configuration"})
		os.Exit(0)
	}
	encoder.Encode(responseIdentityProbe{Kind: "ready", PID: os.Getpid()})
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if scanner.Text() != "compile" {
			break
		}
		response, status := executeCompileServerRequestWithSessions(req, cache)
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		stamp, stampErr := compilerSourceStamp()
		encoder.Encode(responseIdentityProbe{
			Kind: "reply", Response: fmt.Sprintf("%x", sha256.Sum256(encoded)), Compiler: stamp,
			Declined: stampErr != nil, OK: response.OK, Status: status, PID: os.Getpid(),
			Analyses:     cache.session(req.Filename, emitObject).Stats().Analyses,
			ResponseHits: cache.native.Stats().ResponseHits, Builds: cache.objects.Stats().Builds,
		})
	}
	os.Exit(0)
}

func TestCompileServerResponsePinnedCompilerReplacement(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copy := filepath.Join(t.TempDir(), "compiler-server")
	if err := copyExecutableFile(executable, copy); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(copy)
	if err != nil {
		t.Fatal(err)
	}
	original := fmt.Sprintf("%x", sha256.Sum256(contents))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, copy, "-test.run=^TestCompilerResponsePinnedImageHelper$")
	cmd.Env = append(os.Environ(), "COMPILER_RESPONSE_IMAGE_HELPER=1")
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	decoder := json.NewDecoder(output)
	var ready responseIdentityProbe
	if err := decoder.Decode(&ready); err != nil || ready.Kind != "ready" {
		t.Fatalf("helper startup: %v %s", err, stderr.String())
	}
	if ready.Skip != "" {
		cmd.Wait()
		t.Skip(ready.Skip)
	}
	if ready.PID != cmd.Process.Pid {
		t.Fatal("protocol did not originate from copied compiler process")
	}
	run := func() responseIdentityProbe {
		t.Helper()
		if _, err := fmt.Fprintln(input, "compile"); err != nil {
			t.Fatal(err)
		}
		var reply responseIdentityProbe
		if err := decoder.Decode(&reply); err != nil || reply.Kind != "reply" || !reply.OK || reply.Status != http.StatusOK || reply.PID != ready.PID {
			t.Fatalf("compile reply: %+v %v %s", reply, err, stderr.String())
		}
		return reply
	}
	cold, warm := run(), run()
	if cold.Compiler != original || warm.Compiler != original || warm.Response != cold.Response || warm.Analyses != cold.Analyses || warm.ResponseHits != cold.ResponseHits+1 {
		t.Fatal("helper did not establish original-image response reuse")
	}
	replacement := []byte("a different installed compiler image")
	if err := os.WriteFile(copy+".new", replacement, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(copy+".new", copy); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(copy)
	if err != nil || !bytes.Equal(installed, replacement) {
		t.Fatal("installed compiler path was not replaced", err)
	}
	after := run()
	if after.Response != cold.Response || (!after.Declined && after.Compiler != original) {
		t.Fatal("running server adopted replacement image or changed output")
	}
	if after.Declined {
		if after.ResponseHits != warm.ResponseHits || after.Analyses != warm.Analyses+1 || after.Builds != warm.Builds+1 {
			t.Fatal("unavailable compiler identity bypassed current validation")
		}
	} else if after.ResponseHits != warm.ResponseHits+1 || after.Analyses != warm.Analyses || after.Builds != warm.Builds {
		t.Fatal("stable pinned original identity lost exact reuse")
	}
	t.Logf("same process PID=%d identity_declined=%v analyses=%d->%d response_hits=%d->%d builds=%d->%d", after.PID, after.Declined, warm.Analyses, after.Analyses, warm.ResponseHits, after.ResponseHits, warm.Builds, after.Builds)
	input.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper exit: %v %s", err, stderr.String())
	}
}
