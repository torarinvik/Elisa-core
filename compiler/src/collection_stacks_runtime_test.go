package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// Collection stacks: a dict/set table lives in an owner-tagged region of its own and growth
// ping-pongs between a live stack and a spare. The fixture asserts the three properties that
// define the design -- ~14 doublings add at most two regions, entries survive every rehash
// (and tombstones clear), and reset-then-regrow reuses the pooled stacks with the buckets
// correctly re-zeroed -- for dict and set both.
func TestRunCLIStdCollectionStacksRuntimeSmoke(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std_collection_stacks_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("collection stacks runtime smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	for _, name := range []string{"dict_growth_ping_pongs_two_stacks", "reset_then_regrow_reuses_stacks", "set_growth_ping_pongs_two_stacks"} {
		if !strings.Contains(stdout.String(), "[       OK ] "+name) {
			t.Fatalf("expected %s to pass, got stdout:\n%s\nstderr:\n%s", name, stdout.String(), stderr.String())
		}
	}
}
