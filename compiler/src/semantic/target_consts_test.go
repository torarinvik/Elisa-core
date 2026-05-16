package semantic

import (
	"strings"
	"testing"
)

func TestTargetOSConstsDriveStaticIf(t *testing.T) {
	src := `
static if ELISA_TARGET_OS_MACOS:
    const SELECTED: int = 1
static elif ELISA_TARGET_OS_LINUX:
    const SELECTED: int = 2
static else:
    const SELECTED: int = 3
`
	macos := analyzeFunctionAnalysisTestSourceWithOptions(t, "target_macos.elisa", src, AnalyzeOptions{TargetTriple: "arm64-apple-macosx15.4"})
	if value, ok := macos.ConstValues["SELECTED"]; !ok || value.Kind != ConstInt || value.Int != 1 {
		t.Fatalf("expected macOS target static if to select 1, got %#v ok=%v", value, ok)
	}
	linux := analyzeFunctionAnalysisTestSourceWithOptions(t, "target_linux.elisa", src, AnalyzeOptions{TargetTriple: "x86_64-unknown-linux-gnu"})
	if value, ok := linux.ConstValues["SELECTED"]; !ok || value.Kind != ConstInt || value.Int != 2 {
		t.Fatalf("expected Linux target static if to select 2, got %#v ok=%v", value, ok)
	}
	windows := analyzeFunctionAnalysisTestSourceWithOptions(t, "target_windows.elisa", src, AnalyzeOptions{TargetTriple: "x86_64-pc-windows-msvc"})
	if value, ok := windows.ConstValues["SELECTED"]; !ok || value.Kind != ConstInt || value.Int != 3 {
		t.Fatalf("expected Windows target static if to select 3, got %#v ok=%v", value, ok)
	}
}

func TestTargetOSConstsAreVisibleToStaticAssertions(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "target_assert.elisa", `
static assert ELISA_TARGET_OS_POSIX, "posix target expected"
`, AnalyzeOptions{TargetTriple: "x86_64-unknown-linux-gnu"})
	if errors := strings.Join(result.Errors(), "\n"); errors != "" {
		t.Fatalf("expected target static assert to pass, got:\n%s", errors)
	}
}
