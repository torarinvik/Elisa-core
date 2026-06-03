package semantic_test

import (
	"strings"
	"testing"
)

// promote re-types a value's provenance into a longer-lived region, so a borrow
// promoted out of `scratch` stays valid after `scratch` is destroyed.
func TestAnalyzeAcceptsPromoteRebasesProvenance(t *testing.T) {
	src := `def f() -> i32:
	region keep(1024)
	region scratch(1024)
	value: i32& = new[scratch] 1
	promote value into keep
	destroy scratch
	answer: i32 = value[0]
	destroy keep
	return answer
`
	_, errs := parseAndAnalyze(t, "promote_rebase_ok.elisa", src)
	if len(errs) != 0 {
		t.Fatalf("expected promote to rebase provenance with no errors, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsPromoteIntoUndefinedRegion(t *testing.T) {
	src := `def f():
	region scratch(1024)
	value: i32& = new[scratch] 1
	promote value into nope
	destroy scratch
`
	_, errs := parseAndAnalyze(t, "promote_undefined_region_reject.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "undefined region \"nope\"") {
		t.Fatalf("expected undefined-region diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsPromoteIntoSameRegion(t *testing.T) {
	src := `def f():
	region scratch(1024)
	value: i32& = new[scratch] 1
	promote value into scratch
	destroy scratch
`
	_, errs := parseAndAnalyze(t, "promote_same_region_reject.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "already lives in region \"scratch\"") {
		t.Fatalf("expected same-region diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsPromoteOfNonRegionValue(t *testing.T) {
	src := `def f():
	region keep(1024)
	x: i32 = 5
	promote x into keep
	destroy keep
`
	_, errs := parseAndAnalyze(t, "promote_non_region_reject.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "not region-backed") {
		t.Fatalf("expected non-region-backed diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
