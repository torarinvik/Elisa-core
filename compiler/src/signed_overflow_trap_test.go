package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Signed integer `+`/`-`/`*` TRAP on two's-complement overflow in contracts-live builds (debug / -O0)
// and WRAP in release (-O2+). This is the runtime side of the verifier's "signed arithmetic does not
// overflow" assumption: the debug trap makes that assumption sound exactly where contracts are
// enforced, while release keeps the plain wrapping op for performance.
func TestSignedOverflowTrapsInDebugWrapsInRelease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sadd.elisa")
	src := "def add_sig(a: i64, b: i64) -> i64:\n    return a + b\n\n" +
		"def sub_sig(a: i32, b: i32) -> i32:\n    return a - b\n\n" +
		"def mul_sig(a: i64, b: i64) -> i64:\n    return a * b\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	emit := func(opt string) string {
		var stdout, stderr bytes.Buffer
		args := []string{"-emit", "llvm"}
		if opt != "" {
			args = append(args, opt)
		}
		args = append(args, path)
		if code := runCLI(args, &stdout, &stderr); code != 0 {
			t.Fatalf("emit llvm %q failed (%d):\n%s", opt, code, stderr.String())
		}
		return stdout.String()
	}

	debug := emit("") // default opt level is O0 (contracts live)
	for _, intr := range []string{"llvm.sadd.with.overflow", "llvm.ssub.with.overflow", "llvm.smul.with.overflow"} {
		if !strings.Contains(debug, intr) {
			t.Fatalf("debug build must emit a checked signed op (%s); IR was:\n%s", intr, debug)
		}
	}
	if !strings.Contains(debug, "llvm.trap") {
		t.Fatalf("debug build must trap on signed overflow; IR had no llvm.trap:\n%s", debug)
	}

	release := emit("-O2")
	for _, intr := range []string{"llvm.sadd.with.overflow", "llvm.ssub.with.overflow", "llvm.smul.with.overflow"} {
		if strings.Contains(release, intr) {
			t.Fatalf("release build must NOT emit checked signed op (%s) — it should wrap:\n%s", intr, release)
		}
	}
}
