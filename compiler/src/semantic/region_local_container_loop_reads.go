package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// In-loop element reads of a container the same loop pushes into.
//
// A by-value read `xs[i]` inside a loop that also pushes into xs sees, on a later iteration, the
// elements pushed after it in the loop body. The flow-sensitive element state at the read only
// covers the pushes analyzed before it, so such a read used to keep the container's own
// (conservative) region. Here it uses that state as an ASSUMPTION instead: every later push (or
// filling call) into xs lexically inside the loop must add nothing the assumed state lacks. A
// push that would widen it is rejected at the push, so the read's verdict stays sound for every
// iteration; a push after the loop is irrelevant (no read of that loop runs again).

type elementLoopInfo struct {
	// reads maps an in-loop read to the outermost enclosing loop that also pushes into its
	// container; modLoops maps each element-adding site to the loops enclosing it.
	reads       map[*ast.IndexExpr]ast.Node
	modLoops    map[ast.Node][]ast.Node
	assumptions []elementLoopAssumption
}

type elementLoopAssumption struct {
	sym      *Symbol
	state    regionRefState
	read     *ast.IndexExpr
	loop     ast.Node
	reported bool
}

func (info *elementLoopInfo) assume(sym *Symbol, state regionRefState, read *ast.IndexExpr) {
	info.assumptions = append(info.assumptions, elementLoopAssumption{sym: sym, state: cloneRegionRefState(state), read: read, loop: info.reads[read]})
}

// checkElementLoopAssumptions is called before sym's element state becomes next (or stops being
// tracked) at the element-adding site: any assumption of a read in a loop enclosing site must
// already cover next.
func (a *Analyzer) checkElementLoopAssumptions(sym *Symbol, site ast.Node, next regionRefState, tracked bool) {
	info := a.currentElementLoop
	if info == nil || sym == nil || len(info.assumptions) == 0 {
		return
	}
	loops := info.modLoops[site]
	for i := range info.assumptions {
		assumption := &info.assumptions[i]
		if assumption.sym != sym || assumption.reported || !elementLoopContains(loops, assumption.loop) {
			continue
		}
		if tracked && regionRefStateCovers(assumption.state, next) {
			continue
		}
		assumption.reported = true
		a.errorf(site.Pos(), "element added to %q inside a loop may be read by the element copy at line %d on a later iteration, which was checked only against the elements added before it; add it after the loop or copy it into the container's region first", sym.Name, assumption.read.Pos().Line)
	}
}

func elementLoopContains(loops []ast.Node, loop ast.Node) bool {
	for _, l := range loops {
		if l == loop {
			return true
		}
	}
	return false
}

// regionRefStateCovers reports whether merging next into assumed changes nothing.
func regionRefStateCovers(assumed, next regionRefState) bool {
	base, ok := mergeRegionRefStates(assumed)
	if !ok {
		return false
	}
	merged, ok := mergeRegionRefStates(assumed, next)
	return ok && reflect.DeepEqual(base, merged)
}
