//go:build cgo

package semantic

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestPrivateSpecializationRechecksCanonicalErrorOnSameTarget(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "specialization_same_error.elisa", `def broken[T](value: T) -> T:
    return missing_specialization_value
`)
	message := `undefined identifier "missing_specialization_value"`
	if !strings.Contains(strings.Join(result.Errors(), "\n"), message) {
		t.Fatal("fixture did not produce the real inherited target diagnostic", result.Errors())
	}
	before := slices.Clone(result.Diagnostics)
	fn := specializationDiagnosticDecl(t, result, "broken")
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	for attempt := 0; attempt < 2; attempt++ {
		product, err := result.SpecializeFunction(fn, []Type{result.NamedTypes["i64"]})
		if product != nil || err == nil || !strings.Contains(err.Error(), message) {
			t.Fatalf("canonical dedup hid an actual private error on the same target: product=%v error=%v", product, err)
		}
		if len(result.shared.bodies) != 0 {
			t.Fatal("failed repeated target entered successful memo")
		}
	}
	if !reflect.DeepEqual(before, result.Diagnostics) {
		t.Fatal("rechecking target errors changed published canonical diagnostics")
	}
}

func TestSpecializationDiagnosticClonePolicyIsPrivateBeforeMapCloning(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "specialization_reporter_clone.elisa", `def broken[T](value: T) -> T:
    return missing_specialization_value
`)
	source := result.analyzer
	if len(source.diagnostics) == 0 || len(source.reportedDiagnostics) == 0 {
		t.Fatal("real fixture did not populate canonical diagnostics/reporter")
	}
	private := cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
	if private.diagnostics != nil || private.reportedDiagnostics != nil {
		t.Fatal("specialization inherited diagnostic storage or dedup authority")
	}
	ordinary := cloneAnalyzerValueForBodyWorker(source).Addr().Interface().(*Analyzer)
	if !reflect.DeepEqual(ordinary.diagnostics, source.diagnostics) || !reflect.DeepEqual(ordinary.reportedDiagnostics, source.reportedDiagnostics) {
		t.Fatal("ordinary worker diagnostic policy changed")
	}
	if reflect.ValueOf(ordinary.reportedDiagnostics).Pointer() == reflect.ValueOf(source.reportedDiagnostics).Pointer() {
		t.Fatal("ordinary worker shared a writable deduplication map")
	}
	_, filename, _, _ := runtime.Caller(0)
	text, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "parallel_bodies.go"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(text), "func cloneAnalyzerValueForParallelPolicy(")
	if start < 0 {
		t.Fatal("clone policy source boundary missing")
	}
	body := string(text)[start:]
	guard := strings.Index(body, `if ownedFunctionAnalyses && (name == "diagnostics" || name == "reportedDiagnostics")`)
	typed := strings.Index(body, "cloneSpecializationOpaqueMap(")
	reflective := strings.Index(body, "deepCloneValue(settable(field))")
	if guard < 0 || typed < guard || reflective < guard {
		t.Fatal("fresh private diagnostic state was created after traversing inherited entries")
	}
}
