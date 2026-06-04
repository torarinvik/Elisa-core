package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// A scoped region (`in auto:` / `region`) is freed at SCOPE exit, so a region inside a loop
// reclaims its arena every iteration instead of leaking it until function return. Before the
// fix this leaked ~10-16 KB/iteration (1M iterations ≈ 10 GB); after, peak RSS is a few MB
// regardless of iteration count. This execs the built binary and asserts peak RSS stays bounded.
func TestRegionInLoopFreesPerIterationBoundsMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("heavy memory test; skipped under -short")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	const iterations = 1000000
	src := "include \"" + std + "\"\n" + `
@test
def region_loop() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        acc: mutable i64 = 0
        for i in 0..<1000000:
            in auto:
                xs: mutable darray[i64] = []
                xs.push(7)
                xs.push(11)
                acc <- acc + xs[0] + xs[1]
        if acc != 18000000:
            panic("region loop produced wrong result")
`
	dir := t.TempDir()
	path := filepath.Join(dir, "region_loop.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Build (and keep) the native test binary; capture its path from stderr.
	t.Setenv("ELISA_KEEP_TEST_BINARY", "1")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("build failed (exit %d)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	exePath := ""
	for _, line := range strings.Split(stderr.String(), "\n") {
		if idx := strings.Index(line, "test binary: "); idx >= 0 {
			exePath = strings.TrimSpace(line[idx+len("test binary: "):])
			break
		}
	}
	if exePath == "" {
		t.Skipf("could not locate kept test binary in stderr:\n%s", stderr.String())
	}
	defer os.Remove(exePath)
	defer os.RemoveAll(exePath + ".dSYM")

	// Run the binary in isolation and read its peak RSS.
	cmd := exec.Command(exePath, "region_loop")
	if err := cmd.Run(); err != nil {
		t.Fatalf("region_loop run failed: %v (iterations=%d)", err, iterations)
	}
	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		t.Skip("rusage unavailable on this platform")
	}
	// ru_maxrss is bytes on darwin, kilobytes on linux.
	maxRSSBytes := int64(ru.Maxrss)
	if runtime.GOOS == "linux" {
		maxRSSBytes *= 1024
	}
	const limitBytes = 512 * 1024 * 1024 // 512 MB — fix keeps it at a few MB; leak would be ~10 GB.
	if maxRSSBytes > limitBytes {
		t.Fatalf("region-in-loop peak RSS = %d MB over %d iterations — expected bounded (<512 MB); regions are not freed per iteration (leak regression)",
			maxRSSBytes/(1024*1024), iterations)
	}
	t.Logf("region-in-loop peak RSS = %d MB over %d iterations (bounded)", maxRSSBytes/(1024*1024), iterations)
}
