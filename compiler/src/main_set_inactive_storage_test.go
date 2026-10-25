package main

import "testing"

// Reuse the actual-runtime+heap CLI/native harness committed with Dict repair.
// Public set keys remain cstr; raw SetBucket[sview] is tested independently.
func TestActualSetInactiveStorageNativeAddRemoveRehashAndClear(t *testing.T) {
	runActualDictInactiveNative(t, `def main() -> int:
    can Memory.Allocate, Memory.Release, Abort.Panic, Atomics{Exchange, Store}, Global{Read, Write}:
        arena: mutable Arena = zeroed
        defer block:
            arena_free(&arena)
        sentinel: mutable SetBucket[cstr] = SetBucket[cstr]{key: "unchanged", state: 1}
        arena_set_zero_buckets(&sentinel, 0)
        assert sentinel.state == 1
        assert sentinel.key == "unchanged"
        raw: mutable SetBucket[sview] = SetBucket[sview]{key: sview("raw-key", 0, -1), state: 1}
        arena_set_zero_buckets(&raw, 1)
        assert raw.state == 0
        values: mutable set[cstr] = arena_set_new[cstr](&arena, 8)
        assert values.items != null
        initial_items: SetBucket[cstr]& = values.items
        for i in 0..<values.capacity:
            assert initial_items[i].state == 0
        assert not arena_set_contains(&values, "alpha")
        assert arena_set_add_or_panic(&arena, &values, "alpha")
        assert arena_set_add_or_panic(&arena, &values, "beta")
        assert not arena_set_add_or_panic(&arena, &values, "alpha")
        assert values.count == 2
        assert arena_set_contains(&values, "alpha")
        assert arena_set_remove(&values, "alpha")
        assert not arena_set_contains(&values, "alpha")
        assert arena_set_contains(&values, "beta")
        tombstones: mutable usize = 0
        assert values.items != null
        removed_items: SetBucket[cstr]& = values.items
        for i in 0..<values.capacity:
            tombstones <- tombstones + 1 if removed_items[i].state == 2
        assert tombstones == 1
        assert arena_set_add_or_panic(&arena, &values, "alpha")
        arena_set_reserve_or_panic(&arena, &values, 64)
        assert values.capacity >= 64
        assert values.count == 2
        assert arena_set_contains(&values, "alpha")
        assert arena_set_contains(&values, "beta")
        arena_set_clear(&values)
        assert values.count == 0 and values.used == 0
        assert not arena_set_contains(&values, "alpha")
        assert values.items != null
        cleared_items: SetBucket[cstr]& = values.items
        for i in 0..<values.capacity:
            assert cleared_items[i].state == 0
        assert arena_set_add_or_panic(&arena, &values, "gamma")
        assert arena_set_contains(&values, "gamma")
        return 0
`, false)
}

func TestActualSetInactiveStorageOverflowRejectsBeforeWrite(t *testing.T) {
	runActualDictInactiveNative(t, `def main() -> int:
    can Abort.Panic:
        bucket: mutable SetBucket[cstr] = SetBucket[cstr]{key: "key", state: 1}
        arena_set_zero_buckets(&bucket, 9223372036854775807.usize())
        return 9
`, true)
}
