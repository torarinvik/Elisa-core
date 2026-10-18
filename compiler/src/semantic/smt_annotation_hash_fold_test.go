//go:build cgo

package semantic

import (
	"os"
	"strings"
	"testing"
)

// The production-shaped row and delimited pair folds jointly exercise loop
// induction, stable view reads, char conversion, XOR congruence and module
// constants inspected before ordinary body typing. Keep all three loops here:
// the second pair loop starts from a computed immutable accumulator.
func TestSMTAnnotationHashFoldInduction(t *testing.T) {
	data, err := os.ReadFile("testdata/smt_annotation_hash_folds.elisa")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	t.Run("complete_folds", func(t *testing.T) {
		result := analyzeContractStrict(t, "annotation_hash_folds.elisa", source)
		if es := result.Errors(); len(es) != 0 {
			t.Fatalf("complete folds must prove: %v", es)
		}
	})
	t.Run("missing_signed_lower_bounds", func(t *testing.T) {
		if strings.Count(source, "                invariant 0 <= offset\n") != 3 {
			t.Fatal("fixture must exercise three signed loop indices")
		}
		variant := strings.ReplaceAll(source, "                invariant 0 <= offset\n", "")
		result := analyzeContractStrict(t, "annotation_hash_folds_unbounded.elisa", variant)
		failures := strings.Count(strings.Join(result.Errors(), "\n"), "invariant could not be proven")
		if failures != 3 {
			t.Fatalf("negative arbitrary induction states must not prove the folds, got %d: %v", failures, result.Errors())
		}
	})
	t.Run("wrong_accumulator_update", func(t *testing.T) {
		original := "hash <- (hash ^ text[offset].u64()) * ANNOTATION_INDEX_HASH_PRIME"
		if strings.Count(source, original) != 1 {
			t.Fatal("row update missing or ambiguous")
		}
		variant := strings.Replace(source, original, "hash <- 0", 1)
		result := analyzeContractStrict(t, "annotation_hash_folds_wrong_update.elisa", variant)
		if !strings.Contains(strings.Join(result.Errors(), "\n"), "invariant could not be proven") {
			t.Fatalf("wrong accumulator update must not prove: %v", result.Errors())
		}
	})
}
