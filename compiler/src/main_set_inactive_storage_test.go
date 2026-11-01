package main

import "testing"

// Reuse the actual-runtime+heap CLI/native harness committed with Dict repair.
// Test both cstr keys and length-aware sview keys against the actual runtime.
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

func TestActualSetStringViewKeysPreserveContentAndLength(t *testing.T) {
	runActualDictInactiveNative(t, `def main() -> int:
    can Memory.Allocate, Memory.Release, Abort.Panic, Atomics{Exchange, Store}, Global{Read, Write}:
        arena: mutable Arena = zeroed
        defer block:
            arena_free(&arena)
        values: mutable set[sview] = arena_set_new[sview](&arena, 8)
        key_bytes: darray[u8] = [97, 0, 98]
        equal_bytes: darray[u8] = [120, 120, 97, 0, 98, 121, 121]
        other_bytes: darray[u8] = [97, 0, 99]
        key: sview = bytes_view_range_ref(&key_bytes, 0, 3)
        equal: sview = bytes_view_range_ref(&equal_bytes, 2, 3)
        other: sview = bytes_view_range_ref(&other_bytes, 0, 3)
        literal: set[sview] = {key, equal, other}
        assert literal.count == 2
        assert equal in literal
        assert other in literal
        assert not values.contains(key)
        assert arena_set_add_or_panic(&arena, &values, key)
        assert not arena_set_add_or_panic(&arena, &values, equal)
        assert values.contains(equal)
        assert not values.has(other)
        assert arena_set_add_or_panic(&arena, &values, other)
        assert arena_set_add_or_panic(&arena, &values, sview("", 0, 0))
        arena_set_reserve_or_panic(&arena, &values, 64)
        assert values.count == 3
        assert equal in values
        assert other in values
        assert sview("", 0, 0) in values
        assert arena_set_remove(&values, equal)
        assert not values.contains(key)
        assert values.contains(other)
        assert arena_set_add_or_panic(&arena, &values, equal)
        arena_set_clear(&values)
        assert values.count == 0 and values.used == 0
        assert not values.contains(other)
        return 0
`, false)
}
