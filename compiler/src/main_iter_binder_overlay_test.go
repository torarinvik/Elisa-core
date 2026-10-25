package main

import "testing"

func TestActualGenericIterBinderOverlayNative(t *testing.T) {
	runActualDictInactiveNative(t, `def gather[T](tag: T, name: sview) -> i64:
    can Memory.Allocate, Abort.Panic:
        names: mutable darray[sview] = [name]
        held: mutable darray[sview] = []
        for value in names |held| -> held:
            held <- held.push(value)
        return held[0].len

def main() -> int:
    can Memory.Allocate, Memory.Release, Abort.Panic, Atomics{Exchange, Store}, Global{Read, Write}:
        assert gather[i64](1, sview("abc", 0, -1)) == 3
        return 0
`, false)
}
