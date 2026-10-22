package semantic

import "testing"

func TestFillWalkSeenResetAfterDenseAndSparseWalks(t *testing.T) {
	state := &fillWalkSeen{values: make(map[uintptr]bool)}
	for key := uintptr(0); key < 10000; key++ {
		state.values[key] = true
	}
	if !state.reset() || len(state.values) != 0 {
		t.Fatal("dense walk was not reset")
	}
	for i := 0; i < 100; i++ {
		state.values[uintptr(i)] = i%2 == 0
		if !state.reset() || len(state.values) != 0 {
			t.Fatal("sparse walk retained a visited address")
		}
	}
	if state.peak > 1 {
		t.Fatal("sustained sparse walks did not release oversized storage")
	}
	for key := uintptr(0); key <= 1<<14; key++ {
		state.values[key] = true
	}
	if state.reset() {
		t.Fatal("exceptionally large walk was retained")
	}
}
