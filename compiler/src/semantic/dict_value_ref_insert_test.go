package semantic

import (
	"strings"
	"testing"
)

// A dict value reference (`m.get(k)`, directly or through a helper returning it) points into
// the bucket array; a later insert may rehash and re-slot every entry, so using the reference
// afterwards is stale even when the backing never relocates.
func TestDictValueRefInvalidatedByMethodInsert(t *testing.T) {
	cases := map[string]string{
		"direct": `def f() -> i64 can[Memory.Allocate, Abort.Panic]:
    m: mutable dict[i64, i64] = {}
    m <- m.put(1, 7)
    r: i64&? = m.get(1)
    for i in 2..<300:
        m <- m.put(i, i)
    if r is v:
        return v
    return 0
`,
		"helper": `def look(m: dict[i64, i64]&) -> i64&?:
    return m.get(1)

def f() -> i64 can[Memory.Allocate, Abort.Panic]:
    m: mutable dict[i64, i64] = {}
    m <- m.put(1, 7)
    r: i64&? = look(m)
    m <- m.put(2, 8)
    if r is v:
        return v
    return 0
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, name+".elisa", src)
			all := strings.Join(result.Errors(), "\n")
			if !strings.Contains(all, `view "r" cannot be used: storage dependency facts were invalidated by dict insert`) {
				t.Fatalf("expected the dict value ref to be invalidated by the insert, got:\n%s", all)
			}
		})
	}
}

func TestDictValueRefAcceptsUseBeforeInsertOrOtherDict(t *testing.T) {
	cases := map[string]string{
		"use_before_insert": `def f() -> i64 can[Memory.Allocate, Abort.Panic]:
    m: mutable dict[i64, i64] = {}
    m <- m.put(1, 7)
    r: i64&? = m.get(1)
    total: mutable i64 = 0
    if r is v:
        total <- v
    m <- m.put(2, 8)
    return total
`,
		"other_dict": `def f() -> i64 can[Memory.Allocate, Abort.Panic]:
    m: mutable dict[i64, i64] = {}
    n: mutable dict[i64, i64] = {}
    m <- m.put(1, 7)
    r: i64&? = m.get(1)
    n <- n.put(2, 8)
    if r is v:
        return v
    return 0
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, name+".elisa", src)
			// The bare harness lacks the dict runtime, so assert only the absence of the
			// stale-ref diagnostic (as dict_interior_ref_test does).
			if all := strings.Join(result.Errors(), "\n"); strings.Contains(all, "cannot be used") {
				t.Fatalf("expected no stale-ref error, got:\n%s", all)
			}
		})
	}
}
