package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeRejectsDuplicateExportName(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "duplicate_export_name.elisa", `
def keep() -> i64:
    return 1

export func api() -> i64 = keep
export func api() -> i64 = keep
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, DuplicateExportNameMessage("api")) {
		t.Fatalf("expected duplicate export name diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsUndefinedExportTarget(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "undefined_export_target.elisa", `
export func api() -> i64 = missing_target
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, UndefinedExportTargetMessage("missing_target")) {
		t.Fatalf("expected undefined export target diagnostic, got:\n%s", all)
	}
}
