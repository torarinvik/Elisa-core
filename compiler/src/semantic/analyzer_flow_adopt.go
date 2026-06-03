package semantic

import (
	"fmt"

	"elisacore/src/ast"
)

// analyzeAdoptStmt handles `adopt <child-region> into <parent-region>` — a
// zero-copy splice of an entire child region into a longer-lived parent,
// consuming the child (docs/67). Every value that depended on the child is
// rebased onto the parent at the parent's current generation; the child owner's
// must-consume obligation is discharged (transferred to the parent, so adopt is
// safe — unlike `leak`); and the child region is ended.
func (a *Analyzer) analyzeAdoptStmt(n *ast.AdoptStmt) {
	childSym, childState := a.lookupRegionState(n.Child)
	if childSym == nil {
		a.errorf(n.Pos(), "undefined region %q", n.Child)
		return
	}
	parentSym, parentState := a.lookupRegionState(n.Parent)
	if parentSym == nil {
		a.errorf(n.Pos(), "undefined region %q", n.Parent)
		return
	}
	if childState.Destroyed {
		a.errorf(n.Pos(), "region %q has already been destroyed", n.Child)
		return
	}
	if parentState.Destroyed {
		a.errorf(n.Pos(), "region %q has already been destroyed", n.Parent)
		return
	}
	if childSym == parentSym {
		a.errorf(n.Pos(), "adopt: cannot adopt region %q into itself", n.Child)
		return
	}

	// adopt requires a compatible backing family — you can only splice storage
	// between regions whose backing can transfer (docs/67 §3.5).
	if !backingFamiliesCompatible(childState.Backing, parentState.Backing) {
		a.errorf(n.Pos(), "adopt: region %q (%s) and %q (%s) have incompatible backing families; cannot adopt",
			n.Child, normalizeBacking(childState.Backing), n.Parent, normalizeBacking(parentState.Backing))
		return
	}

	reason := fmt.Sprintf("adopt of region %q into %q", n.Child, n.Parent)
	// The child's storage now lives under the parent's lifetime: rebase every
	// value that named the child onto the parent at the parent's generation.
	a.rebaseRegionRefs(childSym, parentSym, parentState.Generation)
	// The child's checkpoints are meaningless once its storage belongs to parent.
	a.invalidateRegionMarks(childSym, func(regionMarkState) bool { return true }, reason)
	// Consume the child owner and end its lifetime. The obligation transfers to
	// the parent (which now owns the storage), so this is safe, not a leak.
	childState.Destroyed = true
	a.currentRegions[childSym] = childState
	a.recordAffineConsumption(affineValueKey{Root: childSym}, "adopt")
}

// rebaseRegionRefs re-keys every tracked value's dependency on `from` to instead
// name `to` at generation `gen`. It is the provenance-transfer counterpart of
// invalidateRegionRefs, built on rebaseRegionDependencyInState.
func (a *Analyzer) rebaseRegionRefs(from, to *Symbol, gen int) {
	for sym, state := range a.currentRegionRefs {
		next, changed := rebaseRegionDependencyInState(state, from, to, gen)
		if changed {
			a.currentRegionRefs[sym] = next
		}
	}
}
