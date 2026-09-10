//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRAppliesProfileHotAttribute(t *testing.T) {
	t.Setenv("ELISACORE_PGO_HOT_FUNCTIONS", "hot_helper")
	result := parseAndAnalyzeBackendTest(t, "profile_hot.elisa", `
def hot_helper(value: i64) -> i64:
	return value + 1

@cold
def cold_helper(value: i64) -> i64:
	return value - 1

def main() -> i64:
	return hot_helper(1) + cold_helper(2)
`)
	g, err := compileLLVMModuleWithTarget(result, OptimizationLevel2, DefaultPackedLoweringProfile(), "")
	if err != nil {
		t.Fatalf("compile with profile hints returned error: %v", err)
	}
	defer g.dispose()
	hotAttrs := functionAttributeGroupForTest(t, g.printModule(), "hot_helper")
	if !strings.Contains(hotAttrs, "hot") {
		t.Fatalf("expected profile-selected helper to carry hot, got attributes {%s}", hotAttrs)
	}
	coldAttrs := functionAttributeGroupForTest(t, g.printModule(), "cold_helper")
	if !strings.Contains(coldAttrs, "cold") || strings.Contains(coldAttrs, "hot") {
		t.Fatalf("explicit cold annotation must win over profile hints, got attributes {%s}", coldAttrs)
	}
}
