package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeScopeCheckpointAndReverseIterableLoop(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "scope_checkpoint.llcontext", `extern pool_new(workers: usize) -> ThreadPool can[Pool.Create]

def build(owner: Arena, items: darray[int]) -> usize:
    alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
    total: mutable usize = 0u
    for rev value in items:
        total <- total + 1u
    scope pool_new(2u):
        pass
    in alloc:
        xs: mutable darray[int] = [1, 2, 3]
        checkpoint mark = xs:
            xs.push(4)
        return xs.count + total
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
}

func TestAnalyzeRejectsReverseRangeForNow(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "reverse_range_rejected.llcontext", `def build() -> void:
    for rev i in 0..<10:
        pass
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "reverse range for loops are not supported yet") {
		t.Fatalf("expected reverse range diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStoreAndDictSugar(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "store_dict_sugar.llcontext", `
store PendingGotoStore:
    name_key: u32
    depth: u32

def arena_dict_get[T](m: any dict[dstr[key_shape], T]&, key: dstr[key_shape]) -> mutable any T&?:
    return null

def arena_dict_contains[T](m: any dict[dstr[key_shape], T]&, key: dstr[key_shape]) -> bool:
    return false

def arena_dict_remove[T](m: mutable any dict[dstr[key_shape], T]&, key: dstr[key_shape]) -> bool:
    return false

def arena_dict_clear[T](m: mutable any dict[dstr[key_shape], T]&):
    pass

def arena_dict_reserve[T](a: mutable any Arena&, m: mutable any dict[dstr[key_shape], T]&, min_capacity: usize) -> void:
    pass

def arena_dict_put[T](a: mutable any Arena&, m: mutable any dict[dstr[key_shape], T]&, key: dstr[key_shape], value: T) -> mutable any T&?:
    return null

def arena_dict_get_or_insert[T](a: mutable any Arena&, m: mutable any dict[dstr[key_shape], T]&, key: dstr[key_shape], value: T) -> mutable any T&?:
    return null

def build(owner: Arena, key: dstr[key_shape]) -> usize:
    alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
    in alloc:
        pending: mutable PendingGotoStore = zeroed
        pending.reserve(8u)
        pending.push(1u32, 2u32)
        pending.truncate(1u)
        pending.clear()
        values: mutable dict[dstr[key_shape], i64] = zeroed
        _ = values.put(key, 7)
        _ = values.get_or_insert(key, 9)
        if values.entry(key).found == false:
            _ = values.entry(key).insert(11)
        _ = values.entry(key).value
        _ = values.get(key)
        _ = values.contains(key)
        _ = values.remove(key)
        values.clear()
        return pending.name_key.count + values.count
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}
