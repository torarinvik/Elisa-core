package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeStaticAssertAcceptsTrueConstant(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_assert_true.elisa", `def keep() -> void:
	static assert 5 > 3
`)
}

func TestAnalyzeTopLevelStaticAssertAcceptsTrueConstant(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "top_level_static_assert_true.elisa", `static assert 5 > 3

def keep() -> void:
	pass
`)
}

func TestAnalyzeStaticAssertRejectsFalseConstant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_assert_false.elisa", `def keep() -> void:
	static assert 5 < 3, "math broke"
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "static assert failed: math broke") {
		t.Fatalf("expected static assert failure diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeTopLevelStaticAssertRejectsFalseConstant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "top_level_static_assert_false.elisa", `static assert 5 < 3, "math broke"

def keep() -> void:
	pass
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "static assert failed: math broke") {
		t.Fatalf("expected top-level static assert failure diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeStaticAssertRequiresBoolCondition(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_assert_non_bool.elisa", `def keep() -> void:
	static assert 5
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "static assert condition must be bool") {
		t.Fatalf("expected static assert bool diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}
