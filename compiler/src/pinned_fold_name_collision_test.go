package main

import "testing"

func TestPinnedFoldPreservesUserBindings(t *testing.T) {
	runActualDictInactiveNative(t, `def main() -> int:
    values: darray[i64] = [1, 2, 3, 4, 5, 6, 7, 8]
    __fold_i: i64 = 100
    result: i64 = (acc + (value + __fold_i) for value in values with acc = 0)
    assert result == 836
    result2: i64 = (acc + __fold_a0 for __fold_a0 in values with acc = 0)
    assert result2 == 36
    __fold_n: darray[i64] = [1, 2, 3, 4, 5, 6, 7, 8]
    result3: i64 = (acc + value for value in __fold_n with acc = 0)
    assert result3 == 36
    result4: i64 = (__fold_a1 + value for value in values with __fold_a1 = 0)
    assert result4 == 36
    return 0
`, false)
}
