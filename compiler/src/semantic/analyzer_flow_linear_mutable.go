package semantic

import (
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Linear-mutable (`lmut T`) call-site checker — Phase 2 of the lmut mode.
//
// `lmut T` is codegen-identical to `mutable T&` (an exclusive in-place mutable reference), so the
// runtime lowering carries none of the novelty. Source validity is likewise already covered: the
// ordinary mutable-ref argument typecheck rejects an immutable binding or a temporary passed to an
// `lmut` parameter (a temporary is not an addressable place; an immutable binding fails the
// mutable-ref match). What `lmut` adds on top is the ONE guarantee that makes mutation trackable and
// is cheaper to check than borrows — because the argument is conceptually MOVED into the parameter
// and threaded back out, only one live binding ever names the value:
//
//	No aliasing within the call. An `lmut` argument may not name the same place as any other
//	argument of the same call (same root symbol with an overlapping path). This is the whole
//	single-live-binding guarantee, enforced at the one point where two names could collide.
//
// The value threads back (the source is reacquired immediately after the call), so unlike `owned`
// there is no persistent consumption — subsequent uses of the source binding remain valid.
//
// NB: an `lmut`/`mutable T&` PARAMETER binding is intentionally NOT marked Symbol.Mutable (the ref
// is not reassignable, though you mutate through it), so this checker keys off the resolved
// RefType.Linear bit and the place path, never the root symbol's Mutable flag — forwarding an lmut
// param onward (the lexer's UFCS `lx.advance()` pattern) is a single, non-aliasing, valid pass.
type linearArgPlace struct {
	index  int
	root   *Symbol
	path   string
	linear bool
	pos    lexer.Pos
}

// checkLinearMutableCallArgs applies the lmut non-aliasing rule to one resolved call. params are the
// post-substitution parameter types (so `rt.Linear` is reliable); args is the position-aligned
// argument list (ordered args followed by any resolved implicit args).
func (a *Analyzer) checkLinearMutableCallArgs(params []Type, args []ast.Expr) {
	if a == nil {
		return
	}
	limit := len(params)
	if len(args) < limit {
		limit = len(args)
	}
	if limit == 0 {
		return
	}
	places := make([]linearArgPlace, 0, limit)
	anyLinear := false
	for i := 0; i < limit; i++ {
		rt, ok := params[i].(*RefType)
		isLinear := ok && rt != nil && rt.Mutable && rt.Linear
		if isLinear {
			anyLinear = true
		}
		root, steps, pathOK := a.namedStateMutationTargetPath(args[i])
		if pathOK && root != nil {
			places = append(places, linearArgPlace{index: i, root: root, path: linearStepPathString(steps), linear: isLinear, pos: args[i].Pos()})
		}
	}
	if !anyLinear {
		return
	}
	// Rule 2: an lmut place may not alias any other argument place of the same call.
	for x := 0; x < len(places); x++ {
		for y := x + 1; y < len(places); y++ {
			p, q := places[x], places[y]
			if !p.linear && !q.linear {
				continue
			}
			if p.root != q.root {
				continue
			}
			if !linearPathsOverlap(p.path, q.path) {
				continue
			}
			// Report at the later argument; name the offending place.
			name := q.root.Name
			a.errorf(q.pos, "`lmut` argument %q aliases another argument of the same call; a linear-mutable value must be named by exactly one live binding", name)
		}
	}
}

// linearStepPathString renders a place path to a canonical, comparable string. Field steps become
// ".field"; constant index steps "[n]"; a wildcard/dynamic index "[*]" (conservatively overlaps any
// sibling index, since we cannot prove two dynamic indices distinct).
func linearStepPathString(steps []borrowReturnAnnotationStep) string {
	var b strings.Builder
	for _, st := range steps {
		switch {
		case st.Wildcard || st.Index == nil && st.Field == "":
			b.WriteString("[*]")
		case st.Index != nil:
			b.WriteByte('[')
			b.WriteString(strconv.FormatInt(*st.Index, 10))
			b.WriteByte(']')
		default:
			b.WriteByte('.')
			b.WriteString(st.Field)
		}
	}
	return b.String()
}

// linearPathsOverlap reports whether two place paths (same root assumed) may refer to overlapping
// storage: equal paths, one a prefix of the other at a segment boundary, or either touching a
// dynamic index that could collide with the other's index at that position.
func linearPathsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	// Whole-root vs anything: the empty path is the root, which contains every sub-place.
	if a == "" || b == "" {
		return true
	}
	if linearPathPrefix(a, b) || linearPathPrefix(b, a) {
		return true
	}
	// A dynamic index "[*]" at a divergence point could alias the other's concrete index there.
	return strings.Contains(a, "[*]") && sharedIndexColumn(a, b)
}

// linearPathPrefix reports whether prefix is a segment-boundary prefix of full (so ".a" is a prefix
// of ".a.b" and ".a[0]" but NOT of ".ab").
func linearPathPrefix(prefix, full string) bool {
	if !strings.HasPrefix(full, prefix) {
		return false
	}
	if len(full) == len(prefix) {
		return true
	}
	next := full[len(prefix)]
	return next == '.' || next == '['
}

// sharedIndexColumn reports whether a and b share the same field prefix up to a "[*]" — used only as
// a conservative dynamic-index overlap fallback.
func sharedIndexColumn(a, b string) bool {
	i := strings.Index(a, "[*]")
	if i < 0 {
		return false
	}
	return strings.HasPrefix(b, a[:i])
}
