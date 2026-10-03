package semantic

import "testing"

// `rewrite` is an ordinary identifier (the removed `rewrite … as sequence[T]:`
// expression no longer reserves the word): a local, parameter, field, capture
// and function name all analyze cleanly.
func TestRewriteIsOrdinaryIdentifier(t *testing.T) {
	result := analyzeBareSource(`struct Rule:
    rewrite: u32

def rewrite(rule: Rule) -> u32:
    return rule.rewrite

def apply(rewrite: u32) -> u32:
    return rewrite + 1

def scan(facts: view[u32]) -> u32:
    rewrite: (known: bool, value: u32) = (true, 0)
    return 0 if not rewrite.known
    total: mutable u32 = rewrite.value
    for fact in facts |total, rewrite|:
        total <- total + fact + rewrite.value
    return total + apply(1)

def call_rewrite() -> u32:
    return rewrite(Rule{rewrite: 2})
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}
