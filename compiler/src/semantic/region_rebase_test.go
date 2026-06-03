package semantic

import "testing"

// rebaseRegionDependencyInState re-keys region provenance from one region symbol
// to another at a new generation, recursing through fields. It is the shared
// substrate for `promote` and `adopt`.
func TestRebaseRegionDependencyInState(t *testing.T) {
	from := &Symbol{}
	to := &Symbol{}

	state := regionRefState{
		Deps: map[*Symbol]regionDependencyState{
			from: {Valid: true, Generation: 0},
		},
		Fields: map[string]regionRefState{
			"elem": {
				Deps: map[*Symbol]regionDependencyState{
					from: {Valid: true, Generation: 0},
				},
			},
		},
	}

	got, changed := rebaseRegionDependencyInState(state, from, to, 7)
	if !changed {
		t.Fatal("expected changed=true")
	}
	if _, ok := got.Deps[from]; ok {
		t.Error("top-level dep on `from` should be removed")
	}
	dep, ok := got.Deps[to]
	if !ok {
		t.Fatal("top-level dep should be re-keyed to `to`")
	}
	if dep.Generation != 7 {
		t.Errorf("rebased generation = %d, want 7", dep.Generation)
	}
	if !dep.Valid {
		t.Error("rebased dep should stay valid")
	}

	// Rebase recurses through fields.
	if _, ok := got.Fields["elem"].Deps[to]; !ok {
		t.Error("field dep should be re-keyed to `to`")
	}
	if _, ok := got.Fields["elem"].Deps[from]; ok {
		t.Error("field dep on `from` should be removed")
	}

	// Clone-on-write: the input state must be untouched.
	if _, ok := state.Deps[from]; !ok {
		t.Error("rebase mutated the input's top-level deps")
	}
	if _, ok := state.Fields["elem"].Deps[from]; !ok {
		t.Error("rebase mutated the input's field deps")
	}

	// No-op cases.
	if _, changed := rebaseRegionDependencyInState(state, to, to, 1); changed {
		t.Error("from==to should be a no-op")
	}
	other := &Symbol{}
	if _, changed := rebaseRegionDependencyInState(state, other, to, 1); changed {
		t.Error("rebasing an absent region should be a no-op")
	}
}
