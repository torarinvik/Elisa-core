package semantic

import "testing"

// narrowStoreMode: what a callee's verdict means for the caller's source, given what the argument
// passed for the callee's parameter carried from that source. Exhaustive over the lattice.
func TestNarrowStoreModeLattice(t *testing.T) {
	modes := []storeMode{storeNone, storeViews, storeElements, storeHeader}
	for _, arg := range modes[1:] {
		for _, inner := range modes {
			got := narrowStoreMode(arg, inner)
			want := inner
			switch {
			case inner == storeNone:
				want = storeNone
			case arg == storeHeader:
				want = inner
			case inner > arg:
				want = arg
			}
			if got != want {
				t.Fatalf("narrowStoreMode(%d, %d) = %d, want %d", arg, inner, got, want)
			}
			if got > inner || (arg != storeHeader && got > arg) {
				t.Fatalf("narrowStoreMode(%d, %d) = %d widens", arg, inner, got)
			}
		}
	}
	// The pre-views behaviour is preserved: an element-mode argument caps a header store at elements.
	if narrowStoreMode(storeElements, storeHeader) != storeElements {
		t.Fatal("element argument must cap the callee's header store")
	}
}
