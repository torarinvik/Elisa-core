package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLITupleFieldEscapeIsRejectedOrKeepsBackingAlive(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang unavailable")
	}
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil {
		t.Fatal(err)
	}
	source := `include "` + std + `"

def tuple_keep(n: usize, dst: mutable darray[i64]&) -> void can[Memory.Allocate, Abort.Panic]:
    for i in 0..<n:
        pair: (items: darray[i64], count: usize) = ([7], 0)
        dst <- pair.items

@test
def tuple_loop_region_runtime_test() -> void:
    can Memory.Allocate, Abort.Panic:
        kept: mutable darray[i64] = []
        tuple_keep(3, &kept)
        if kept.count != 1:
            panic("tuple field header was not updated")
        if kept[0] != 7:
            panic("tuple field backing did not survive function return")
`
	path := filepath.Join(t.TempDir(), "tuple_loop.elisa")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	status := runCLI([]string{"-emit", "test", path}, &stdout, &stderr)
	if status == 0 {
		if !strings.Contains(stdout.String(), "[       OK ] tuple_loop_region_runtime_test") {
			t.Fatal(stdout.String())
		}
		return
	}
	// Future ownership inference may accept this program, but then the real
	// runtime must preserve its backing after the callee returns. A runtime
	// panic is never a substitute for an allocation/lifetime diagnostic.
	if strings.Contains(stdout.String(), "[ PANIC") {
		t.Fatalf("accepted a dangling tuple field: %s", stdout.String())
	}
	diagnostic := stderr.String()
	if !strings.Contains(diagnostic, "no region to allocate in") &&
		!strings.Contains(diagnostic, "short-lived region escapes") &&
		!strings.Contains(diagnostic, "stored into longer-lived") &&
		!strings.Contains(diagnostic, "leaving a dangling reference") {
		t.Fatalf("unexpected rejection: status=%d stdout=%s stderr=%s", status, stdout.String(), diagnostic)
	}
}
