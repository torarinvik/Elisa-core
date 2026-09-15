package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Invalid namespace-qualified type spellings must stop the entire CLI pipeline before backend
// code generation. This is deliberately an object-emission test: semantic-only coverage can
// pass while a suppressed speculative pass has accidentally hidden the diagnostic and left an
// invalid aggregate for LLVM's ABI sizing code.
func TestCLIRejectsDotModulePathBeforeObjectEmission(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "dot_namespace_type.elisa")
	objectPath := filepath.Join(dir, "dot_namespace_type.o")
	source := `module math:
	struct Box:
		value: i64

def take(box: math.Box) -> i64:
	return box.value

def main() -> i64:
	return 0
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI([]string{"-emit", "obj", "-O0", "-o", objectPath, sourcePath}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("invalid dotted namespace type unexpectedly compiled; stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), `"math" is a namespace; write math::Box`) {
		t.Fatalf("expected the namespace spelling diagnostic, got:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "SIGTRAP") || strings.Contains(stderr.String(), "LLVMABISizeOfType") {
		t.Fatalf("invalid source reached an LLVM trap instead of failing semantically:\n%s", stderr.String())
	}
	if _, err := os.Stat(objectPath); !os.IsNotExist(err) {
		t.Fatalf("rejected source left an object artifact at %s (stat error: %v)", objectPath, err)
	}
}
