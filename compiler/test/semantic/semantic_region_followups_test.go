package semantic_test

import (
	"strings"
	"testing"
)

// promote accepts an arbitrary value expression, not just a bare identifier — the
// parser gates on a depth-0 `into` before end of line.
func TestAnalyzeAcceptsPromoteExpressionValueForm(t *testing.T) {
	src := `def f() -> i32:
	region keep(1024)
	region scratch(1024)
	value: i32& = new[scratch] 1
	promote (value) into keep
	destroy scratch
	answer: i32 = value[0]
	destroy keep
	return answer
`
	_, errs := parseAndAnalyze(t, "promote_expr_form_ok.elisa", src)
	if len(errs) != 0 {
		t.Fatalf("expected parenthesized promote value to work, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Adopting a *scoped* child region inside its own body consumes the owner; the
// scope-exit discharge must not double-consume it.
func TestAnalyzeAcceptsAdoptOfScopedChildRegion(t *testing.T) {
	src := `def f():
	region parent(1024)
	region child(1024):
		adopt child into parent
	destroy parent
`
	_, errs := parseAndAnalyze(t, "adopt_scoped_child_ok.elisa", src)
	if len(errs) != 0 {
		t.Fatalf("expected adopt of a scoped child to not double-consume, got:\n%s", strings.Join(errs, "\n"))
	}
}
