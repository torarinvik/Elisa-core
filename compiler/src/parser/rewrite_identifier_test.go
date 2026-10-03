package parser

import (
	"strings"
	"testing"
)

// `rewrite` is an ordinary identifier: only the exact removed
// `rewrite EXPR as sequence[T]:` head is claimed (for its migration diagnostic).
func TestParseRewriteIsOrdinaryIdentifier(t *testing.T) {
	for _, src := range []string{
		// local (tuple-typed, as in elisa-proof's kernel), field access, capture list
		"def f(facts: view[u32]) -> bool:\n    rewrite: (known: bool, value: usize) = (true, 0)\n    return false if not rewrite.known\n    total: mutable usize = 0\n    for fact in facts |total, rewrite|:\n        total <- total + rewrite.value\n    return rewrite.known\n",
		// parameter
		"def g(rewrite: u32) -> u32:\n    return rewrite + 1\n",
		// field name
		"struct Rule:\n    rewrite: u32\n\ndef h(rule: Rule) -> u32:\n    return rule.rewrite\n",
		// function name, called
		"def rewrite(x: u32) -> u32:\n    return x\n\ndef k() -> u32:\n    return rewrite(1)\n",
		// bare operand of an expression
		"def m(rewrite: bool) -> bool:\n    return rewrite and true\n",
	} {
		if _, errs := parseSourceFile(t, src); len(errs) != 0 {
			t.Fatalf("unexpected parser errors for:\n%s\n%v", src, errs)
		}
	}
}

func TestParseLegacyRewriteExprReportsOnce(t *testing.T) {
	src := "def keep_non_zero(owner: mutable Arena&, items: view[u32]) -> darray[u32]:\n    can Abort.Panic, Memory.Allocate:\n        in owner:\n            return rewrite items as sequence[u32]:\n                item when item != 0:\n                    emit item\n"
	_, errs := parseSourceFile(t, src)
	if len(errs) != 1 || !strings.Contains(errs[0], "`rewrite … as sequence[T]:` has been removed") {
		t.Fatalf("expected exactly the rewrite removal diagnostic, got: %v", errs)
	}
}
