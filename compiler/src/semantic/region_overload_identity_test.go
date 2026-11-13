package semantic

import (
	"strings"
	"testing"
)

func TestRegionConstraintsDoNotDistinguishOverloadSignatures(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "region_overload.elisa", `struct Pair[@a, @b]:
    left: i64& @a
    right: i64& @b

def route[@r](ticket: i64, pair: Pair[r, r]) -> i64:
    return 1

def route[@r, @s](ticket: i64, pair: Pair[r, s]) -> i64:
    return 2
`)
	if errs := strings.Join(result.Errors(), "\n"); !strings.Contains(errs, `duplicate declaration "route"`) {
		t.Fatalf("expected duplicate overload diagnostic, got: %s", errs)
	}
}

func TestRegionOverloadWithDifferentFirstTypeIsAllowed(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "region_overload_distinct.elisa", `struct Pair[@a, @b]:
    left: i64& @a
    right: i64& @b

def route(ticket: i64, pair: Pair) -> i64:
    return 1

def route(ticket: bool, pair: Pair) -> i64:
    return 2
`)
	if errs := strings.Join(result.Errors(), "\n"); strings.Contains(errs, `duplicate declaration "route"`) {
		t.Fatalf("distinct receiver overloads must remain valid: %s", errs)
	}
}
