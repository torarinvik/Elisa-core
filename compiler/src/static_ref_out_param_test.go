package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIStaticStringRefOutParamRebindsCallerSlot(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "static_ref_out_param_fixture.elisa")
	testPath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "test.elisa")
	testInclude, err := filepath.Rel(fixtureDir, testPath)
	if err != nil {
		t.Fatalf("failed to compute test include path: %v", err)
	}
	testInclude = filepath.ToSlash(testInclude)
	src := fmt.Sprintf(`# include %q

def set_out(out: mutable static u8&) -> void:
	out <- "D4yla3vx4tY"

@test
def static_string_ref_out_param_rebinds_caller_slot() -> void:
	can Abort.Panic:
		value: mutable static u8& = ""
		set_out(value.ref[mutable static u8&])
		assert_eq(value, "D4yla3vx4tY")
`, testInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write static ref out-param fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected static ref out-param test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] static_string_ref_out_param_rebinds_caller_slot",
		"[       OK ] static_string_ref_out_param_rebinds_caller_slot",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected static ref out-param output to contain %q, got:\n%s", check, output)
		}
	}
}
