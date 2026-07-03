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
