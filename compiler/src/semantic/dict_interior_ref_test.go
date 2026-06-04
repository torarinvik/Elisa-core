package semantic

import (
	"strings"
	"testing"
)

// arena_dict_get returns an interior reference into the dict's bucket array; a later relocating
// insert (arena_dict_put*/get_or_insert resize → realloc) dangles it. Using the reference after
// such an insert must be a stale-reference error (the dict analogue of interior-ref-after-push).
func TestDictInteriorRefInvalidatedByInsert(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "dict_interior_insert.elisa", `def f(owner: mutable Arena&) -> void:
    m: mutable dict[i64, i64] = arena_dict_new[i64, i64](owner, 16)
    _ = arena_dict_put_checked[i64, i64](owner, m.ref[mutable dict[i64, i64]&], 1, 100)
    v: mutable i64&? = arena_dict_get[i64, i64](m.ref[dict[i64, i64]&], 1)
    _ = arena_dict_put_checked[i64, i64](owner, m.ref[mutable dict[i64, i64]&], 2, 200)
    ok: bool = v != null
    if ok:
        return
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "cannot be used") {
		t.Fatalf("expected stale dict interior-ref error, got:\n%s", all)
	}
}

// Same hazard with the insert in a LOOP and the use after it — invalidation must propagate out of
// the loop body (this is the realistic insert-in-a-loop-while-holding-a-get-ref pattern).
func TestDictInteriorRefInvalidatedByInsertInLoop(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "dict_interior_loop.elisa", `def f(owner: mutable Arena&) -> void:
    m: mutable dict[i64, i64] = arena_dict_new[i64, i64](owner, 4)
    _ = arena_dict_put_checked[i64, i64](owner, m.ref[mutable dict[i64, i64]&], 1, 100)
    v: mutable i64&? = arena_dict_get[i64, i64](m.ref[dict[i64, i64]&], 1)
    for k in 0..<100:
        _ = arena_dict_put_checked[i64, i64](owner, m.ref[mutable dict[i64, i64]&], (k + 10).i64(), k.i64())
    ok: bool = v != null
    if ok:
        return
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "cannot be used") {
		t.Fatalf("expected stale dict interior-ref error across loop, got:\n%s", all)
	}
}

// A get reference used BEFORE any insert is valid — it must NOT be flagged stale (no false
// positive). The bare semantic harness lacks the dict runtime functions, so we assert only the
// absence of the stale-ref diagnostic; runtime correctness of the valid pattern is covered by the
// CLI tests.
func TestDictInteriorRefValidBeforeInsert(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "dict_interior_ok.elisa", `def f(owner: mutable Arena&) -> void:
    m: mutable dict[i64, i64] = arena_dict_new[i64, i64](owner, 16)
    _ = arena_dict_put_checked[i64, i64](owner, m.ref[mutable dict[i64, i64]&], 1, 100)
    v: mutable i64&? = arena_dict_get[i64, i64](m.ref[dict[i64, i64]&], 1)
    if v != null:
        v[0] <- 7
`)
	if all := strings.Join(result.Errors(), "\n"); strings.Contains(all, "cannot be used") {
		t.Fatalf("valid get-then-use (no insert between) must not be flagged stale, got:\n%s", all)
	}
}

// The loop-merge fix also strengthens darrays: an interior ref held across a push IN A LOOP, used
// after the loop, must now be a stale reference.
func TestDarrayInteriorRefInvalidatedByPushInLoop(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "darray_interior_loop.elisa", `def f(owner: mutable Arena&, i: usize) -> u8:
    xs: mutable darray[u8] = []
    in owner:
        xs.push(1)
        r: u8& = xs[0].ref[u8&]
        for k in 0..<10:
            xs.push(2)
        return r
    return 0
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "cannot be used") && !strings.Contains(all, "stale") {
		t.Fatalf("expected darray interior-ref-after-push-in-loop to be stale, got:\n%s", all)
	}
}
