package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeAcceptsSpacedParenthesizedAssertCondition(t *testing.T) {
	source := `def sample_case(left: int) -> void can[Abort.Panic]:
    assert (left > 0) == true
`
	_, errs := parseAndAnalyze(t, "parenthesized_assert_condition.elisa", source)
	if len(errs) != 0 {
		t.Fatalf("expected no semantic errors, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsParenthesizedAssertWithLocalGrant(t *testing.T) {
	source := `def sample_case(left: int) -> void:
    assert (left > 0) can Abort.Panic
`
	_, errs := parseAndAnalyze(t, "parenthesized_assert_local_grant.elisa", source)
	if len(errs) != 0 {
		t.Fatalf("expected no semantic errors, got:\n%s", strings.Join(errs, "\n"))
	}
}
