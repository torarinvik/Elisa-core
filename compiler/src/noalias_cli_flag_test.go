package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The `-fnoalias` CLI flag is the stable, non-env entrypoint for the noalias stamp.
// It must (a) turn the stamp on for a build and (b) leave the default OFF when absent.
// The flag sets a process-global env var at parse time, so each subtest restores it to
// avoid leaking the opt-in into other tests (notably backend's default-OFF assertion).
func TestNoaliasCLIFlagStampsAndDefaultsOff(t *testing.T) {
	const envKey = "ELISACORE_NOALIAS_MUTABLE_REFS"
	repoRoot := repoRootFromMainTest(t)
	path := filepath.Join(repoRoot, "compiler", "noalias_cli_probe.elisa")

	emit := func(t *testing.T, extra ...string) string {
		prev, had := os.LookupEnv(envKey)
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(envKey, prev)
			} else {
				_ = os.Unsetenv(envKey)
			}
		})
		args := append([]string{"-emit", "llvm", "-O0"}, extra...)
		args = append(args, path)
		var stdout, stderr bytes.Buffer
		if code := runCLI(args, &stdout, &stderr); code != 0 {
			t.Fatalf("emit llvm failed (%d):\n%s", code, stderr.String())
		}
		return stdout.String()
	}

	defineHasNoalias := func(ir string) bool {
		for _, line := range strings.Split(ir, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "define ") && strings.Contains(trimmed, "noalias") {
				return true
			}
		}
		return false
	}

	t.Run("flag on stamps noalias", func(t *testing.T) {
		if !defineHasNoalias(emit(t, "-fnoalias")) {
			t.Fatal("expected -fnoalias to stamp noalias on the scalar mutable-ref param")
		}
	})

	t.Run("default off", func(t *testing.T) {
		if defineHasNoalias(emit(t)) {
			t.Fatal("expected NO noalias without -fnoalias")
		}
	})
}
