package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeAcceptsBackingStrategies(t *testing.T) {
	src := `def f():
	region a(1024) using reserve_commit
	region b(1024) using fixed
	region c(1024) using chained
	region d(1024) using scratch
	destroy a
	destroy b
	destroy c
	destroy d
`
	_, errs := parseAndAnalyze(t, "region_backing_ok.elisa", src)
	if len(errs) != 0 {
		t.Fatalf("expected the backing strategies to be accepted, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsUnknownBackingStrategy(t *testing.T) {
	src := `def f():
	region a(1024) using bogus
	destroy a
`
	_, errs := parseAndAnalyze(t, "region_backing_unknown_reject.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "unknown region backing \"bogus\"") {
		t.Fatalf("expected unknown-backing diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

// adopt requires a compatible backing family: you cannot splice a reserve_commit
// region into a chained one.
func TestAnalyzeRejectsAdoptAcrossIncompatibleBackingFamilies(t *testing.T) {
	src := `def f():
	region parent(1024) using chained
	region child(1024) using reserve_commit
	adopt child into parent
	destroy parent
`
	_, errs := parseAndAnalyze(t, "adopt_incompatible_backing_reject.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "incompatible backing families") {
		t.Fatalf("expected incompatible-backing diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeAcceptsAdoptWithinSameBackingFamily(t *testing.T) {
	src := `def f() -> i32:
	region parent(1024) using reserve_commit
	region child(1024) using reserve_commit
	value: i32& = new[child] 1
	adopt child into parent
	answer: i32 = value[0]
	destroy parent
	return answer
`
	_, errs := parseAndAnalyze(t, "adopt_same_backing_ok.elisa", src)
	if len(errs) != 0 {
		t.Fatalf("expected adopt within the same backing family to succeed, got:\n%s", strings.Join(errs, "\n"))
	}
}
