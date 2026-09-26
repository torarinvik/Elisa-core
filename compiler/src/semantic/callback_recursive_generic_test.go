//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestMutualGenericValueCycleReportsCallbackSpecializationLimit(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "mutual_generic_cycle.elisa", `
struct First[T]:
    value: T
    next: Second[T]

struct Second[T]:
    value: T
    next: First[T]

def read(value: First[i64]) -> i64:
    return 42
`)
	for _, diagnostic := range result.Errors() {
		if strings.Contains(diagnostic, "callback-carrying type specialization recursion limit") {
			return
		}
	}
	t.Fatalf("expected bounded callback-specialization rejection, got %v", result.Errors())
}

func TestMutualGenericReferenceCycleRemainsValid(t *testing.T) {
	analyzeTreeTestSource(t, "mutual_generic_reference.elisa", `
struct First[T]:
    value: mutable T
    next: Second[T]&?

struct Second[T]:
    value: mutable T
    next: First[T]&?

def main() -> i64:
    value: mutable First[i64] = zeroed
    value.value <- 42
    return value.value
`)
}
