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

func TestTargetNamespaceConstsDriveStaticIf(t *testing.T) {
	src := `
static if target.os == "windows":
    const SELECTED: int = 1
static elif target.arch == "arm64":
    const SELECTED: int = 2
static elif target.features.posix:
    const SELECTED: int = 3
static else:
    const SELECTED: int = 4
`
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "target_namespace.elisa", src, AnalyzeOptions{TargetTriple: "x86_64-unknown-linux-gnu"})
	if value, ok := result.ConstValues["SELECTED"]; !ok || value.Kind != ConstInt || value.Int != 3 {
		t.Fatalf("expected target namespace static if to select 3, got %#v ok=%v", value, ok)
	}
	if value, ok := result.ConstValues["target.os"]; !ok || value.Kind != ConstString || value.String != "linux" {
		t.Fatalf("expected target.os const to be linux, got %#v ok=%v", value, ok)
	}
	if value, ok := result.ConstValues["target.libc.gnu_strerror_r"]; !ok || value.Kind != ConstBool || !value.Bool {
		t.Fatalf("expected target.libc.gnu_strerror_r const to be true, got %#v ok=%v", value, ok)
	}
}

func TestTargetCompatibilityAliasesDriveStaticIf(t *testing.T) {
	src := `
static if PLATFORM_WINDOWS:
    const SELECTED: int = 1
static elif PLATFORM_APPLE and ARCH_ARM64:
    const SELECTED: int = 2
static elif PLATFORM_LINUX and ARCH_X86_64:
    const SELECTED: int = 3
static else:
    const SELECTED: int = 4
`
	linux := analyzeFunctionAnalysisTestSourceWithOptions(t, "target_alias_linux.elisa", src, AnalyzeOptions{TargetTriple: "x86_64-unknown-linux-gnu"})
	if value, ok := linux.ConstValues["SELECTED"]; !ok || value.Kind != ConstInt || value.Int != 3 {
		t.Fatalf("expected Linux/x86_64 target alias static if to select 3, got %#v ok=%v", value, ok)
	}
	macos := analyzeFunctionAnalysisTestSourceWithOptions(t, "target_alias_macos.elisa", src, AnalyzeOptions{TargetTriple: "arm64-apple-macosx15.4"})
	if value, ok := macos.ConstValues["SELECTED"]; !ok || value.Kind != ConstInt || value.Int != 2 {
		t.Fatalf("expected macOS/arm64 target alias static if to select 2, got %#v ok=%v", value, ok)
	}
	windows := analyzeFunctionAnalysisTestSourceWithOptions(t, "target_alias_windows.elisa", src, AnalyzeOptions{TargetTriple: "x86_64-pc-windows-msvc"})
	if value, ok := windows.ConstValues["SELECTED"]; !ok || value.Kind != ConstInt || value.Int != 1 {
		t.Fatalf("expected Windows target alias static if to select 1, got %#v ok=%v", value, ok)
	}
}
