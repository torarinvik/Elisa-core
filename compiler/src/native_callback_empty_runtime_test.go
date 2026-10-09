package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// With no callconv callbacks in the source, the generated bridge must still link
// and preserve the missing-callback fallback contract.
func TestRunCLIEmptyNativeCallbackShimFailsClosed(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"))
	src := preamble + `
@test
def empty_native_callback_shim_fails_closed() -> void:
    can Abort.Panic, Memory.Allocate, Memory.Release, Thread.Spawn, Thread.Join:
        missing: cstr = "no_such_callback"
        if native_callback_ptr(missing) != null:
            panic("missing callback unexpectedly resolved")
        if native_callback_call_u32_voidp(missing, null, 19) != 19:
            panic("u32 missing callback did not return fallback")
        if native_callback_call_i32_voidp(missing, null, -23) != -23:
            panic("i32 missing callback did not return fallback")
        if native_callback_call_usize_voidp(missing, null, 29) != 29:
            panic("usize missing callback did not return fallback")
        if native_callback_call_isize_voidp(missing, null, -31) != -31:
            panic("isize missing callback did not return fallback")
        if native_callback_spawn_join_u32_voidp(missing, null, 37) != 37:
            panic("missing callback spawn/join did not return fallback")
        ctx: mutable heap void&? = native_callback_context_new_u32_voidp(missing, null, 41)
        if ctx != null:
            panic("missing callback unexpectedly created a context")
        thread: mutable uintptr = 1
        if native_callback_context_start_u32_voidp(ctx, &thread) != -1:
            panic("null callback context unexpectedly started")
        if native_callback_context_join_u32_voidp(thread, ctx, 43) != 43:
            panic("null callback context join did not return fallback")
        if native_callback_context_spawn_join_u32_voidp(ctx, 47) != 47:
            panic("null callback context spawn/join did not return fallback")
        if native_callback_context_result_u32(ctx, 53) != 53:
            panic("null callback context result did not return fallback")
        native_callback_context_free(ctx)
`
	fixturePath := filepath.Join(fixtureDir, "empty_native_callback_shim.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write empty callback fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected empty callback shim test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] empty_native_callback_shim_fails_closed") {
		t.Fatalf("expected empty callback shim test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
