package main

import "testing"

// Builtin dict insertion specializes the standard runtime helpers. Moving a
// droppable affine value must remain valid through native specialization.
func TestActualDictAffineValueNativeInsertion(t *testing.T) {
	runActualDictInactiveNative(t, `affine struct Handle:
    value: i64

def main() -> int:
    can Memory.Allocate, Abort.Panic:
        handle: Handle = Handle{value: 42}
        values: mutable dict[i64, Handle] = {}
        values.put(1, move handle)
        for i in 2..<40:
            next: Handle = Handle{value: i}
            values.put(i, move next)
        assert values.count == 39
        return 0
`, false)
}
