package semantic_test

import (
	"strings"
	"testing"
)

// adopt splices a child region into a parent: the child's allocations are rebased
// onto the parent's lifetime, so a borrow into the child stays valid afterward,
// and the child owner is consumed (no must-consume error without destroying it).
func TestAnalyzeAcceptsAdoptRebasesAndConsumesChild(t *testing.T) {
	src := `def f() -> i32:
	region parent(1024)
	region child(1024)
	value: i32& = new[child] 1
	adopt child into parent
	answer: i32 = value[0]
	destroy parent
	return answer
`
	_, errs := parseAndAnalyze(t, "adopt_rebase_ok.elisa", src)
	if len(errs) != 0 {
		t.Fatalf("expected adopt to rebase + consume child with no errors, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsAdoptIntoUndefinedRegion(t *testing.T) {
	src := `def f():
	region child(1024)
	adopt child into nope
`
	_, errs := parseAndAnalyze(t, "adopt_undefined_parent_reject.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "undefined region \"nope\"") {
		t.Fatalf("expected undefined-region diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsAdoptIntoItself(t *testing.T) {
	src := `def f():
	region scratch(1024)
	adopt scratch into scratch
	destroy scratch
`
	_, errs := parseAndAnalyze(t, "adopt_self_reject.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "cannot adopt region \"scratch\" into itself") {
		t.Fatalf("expected adopt-into-itself diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

// After adoption the child region is consumed; any further use is rejected.
func TestAnalyzeRejectsUseOfAdoptedChildRegion(t *testing.T) {
	src := `def f():
	region parent(1024)
	region child(1024)
	adopt child into parent
	destroy child
	destroy parent
`
	_, errs := parseAndAnalyze(t, "adopt_child_consumed_reject.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "region \"child\" has already been destroyed") {
		t.Fatalf("expected child-consumed diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
