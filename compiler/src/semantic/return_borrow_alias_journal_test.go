package semantic

import "testing"

// The journal must give every branch the environment the old per-branch clone gave it, and the
// join must equal merging that clone back.
func TestReturnBorrowAliasJournalRestoresAndReportsBranchWrites(t *testing.T) {
	a := &Analyzer{}
	p0 := returnBorrowFlow{Params: map[int]bool{0: true}}
	p1 := returnBorrowFlow{Params: map[int]bool{1: true}}
	local := returnBorrowFlow{Local: true}
	aliases := map[string]returnBorrowFlow{"x": p0}

	env := a.beginReturnBorrowBranch(aliases)
	a.writeReturnBorrowAlias(env, "x", p1)
	a.writeReturnBorrowAlias(env, "y", local)
	// A nested branch on the same map is undone by its own frame.
	inner := a.beginReturnBorrowBranch(env)
	a.writeReturnBorrowAlias(inner, "x", local)
	a.writeReturnBorrowAlias(inner, "z", local)
	innerChanges := a.endReturnBorrowBranch(env)
	if len(innerChanges) != 2 || !aliases["x"].Params[1] || aliases["x"].Local {
		t.Fatalf("inner frame must restore x=p1 and report x,z; got %v, x=%v", innerChanges, aliases["x"])
	}
	if _, ok := aliases["z"]; ok {
		t.Fatalf("inner frame must remove the binding it introduced")
	}
	// A second write of x in the same frame is reported once, with its first `existed`.
	a.writeReturnBorrowAlias(env, "x", local)
	changes := a.endReturnBorrowBranch(aliases)
	if len(aliases) != 1 || !aliases["x"].Params[0] || len(aliases["x"].Params) != 1 || aliases["x"].Local {
		t.Fatalf("frame must restore the environment before the branch; got %v", aliases)
	}
	if len(changes) != 2 || changes[0].name != "x" || !changes[0].existedBefore || !changes[0].flow.Local ||
		changes[1].name != "y" || changes[1].existedBefore {
		t.Fatalf("unexpected branch changes %+v", changes)
	}
	a.joinReturnBorrowBranch(aliases, changes, func(c returnBorrowBranchChange) bool { return !c.existedBefore })
	if _, ok := aliases["y"]; ok || !aliases["x"].Local || !aliases["x"].Params[0] {
		t.Fatalf("join must merge x and skip y; got %v", aliases)
	}
	if len(a.returnBorrowAliasFrames) != 0 || len(a.returnBorrowAliasJournal) != 0 {
		t.Fatalf("frames and journal must be empty after the outermost frame closes")
	}
	// A nil environment walks a private scratch map whose writes go nowhere.
	scratch := a.beginReturnBorrowBranch(nil)
	a.writeReturnBorrowAlias(scratch, "w", local)
	if got := a.endReturnBorrowBranch(nil); got != nil || len(a.returnBorrowAliasJournal) != 0 {
		t.Fatalf("nil environment must not journal; got %v", got)
	}
}
