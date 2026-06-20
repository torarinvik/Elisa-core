package semantic

import "elisacore/src/ast"

// Frame-aware SMT-fact survival across calls (docs/87 — heap/frame fact survival).
//
// A call may write its mutable-ref arguments, so by default every proof fact that reads such an
// argument is dropped at the call site (invalidateSMTAssertFactsForCall). But when the callee
// declares a bounding frame (`changes`/`preserves`), it provably writes ONLY the places named in
// that frame — through that very parameter, since the caller's borrow is unique. A fact that reads a
// DISJOINT place of the same argument is therefore still true afterward and may survive.
//
// Soundness rests on the existing frame guarantee: `changes` is an enforced upper bound on writes
// (checkFrameResolvedPlace rejects any write outside it), and a mutable ref cannot alias, so the
// only way the callee can mutate the argument's storage is through that parameter — all of which is
// frame-checked. So a place the frame does not cover is provably untouched.
//
// This only ever lets MORE facts survive: an unframed callee (FrameBounded == false), an argument
// whose place cannot be characterized, or a fact whose read overlaps a declared write all fall back
// to the conservative whole-argument invalidation.

type callFrameCtx struct {
	ft   *FuncType
	args []ast.Expr
}

// cacheCallFrameContext records the resolved callee type and position-aligned arguments for a call,
// so the deferred SMT-fact invalidation can reason about the callee's frame.
func (a *Analyzer) cacheCallFrameContext(expr *ast.CallExpr, ft *FuncType, args []ast.Expr) {
	if a == nil || expr == nil || ft == nil {
		return
	}
	if a.callFrameContexts == nil {
		a.callFrameContexts = map[*ast.CallExpr]callFrameCtx{}
	}
	a.callFrameContexts[expr] = callFrameCtx{ft: ft, args: args}
}

// invalidateSMTAssertFactsFramed drops the proof facts a call may have falsified, consulting the
// callee's frame to keep facts about provably-untouched places. Falls back to whole-argument
// invalidation for any argument the frame does not let it refine.
func (a *Analyzer) invalidateSMTAssertFactsFramed(ft *FuncType, args []ast.Expr) {
	for i, arg := range args {
		if arg == nil || i >= len(ft.Params) {
			continue
		}
		rt, ok := ft.Params[i].(*RefType)
		if !ok || rt == nil || !rt.Mutable {
			continue // a by-value or immutable-borrow argument cannot be written
		}
		if !ft.FrameBounded {
			a.invalidateSMTAssertFactsForTarget(arg) // unbounded callee: conservative
			continue
		}
		root, fields, ok := frameWritePath(arg)
		if !ok {
			a.invalidateSMTAssertFactsForTarget(arg) // place not characterizable: conservative
			continue
		}
		// The concrete places this call may write through this argument: arg-place ⊕ frame-suffix.
		suffixes := calleeFrameSuffixesForParam(ft, i)
		writePaths := make([][]string, 0, len(suffixes))
		for _, suf := range suffixes {
			writePaths = append(writePaths, append(append([]string(nil), fields...), suf...))
		}
		a.dropSMTFactsOverlappingWrites(root, writePaths)
	}
}

// dropSMTFactsOverlappingWrites removes facts rooted at `root` whose read places overlap any of the
// callee's write places. A fact that does not depend on `root`, or reads only disjoint places (in
// particular, every fact when the callee writes nothing through this argument), survives.
func (a *Analyzer) dropSMTFactsOverlappingWrites(root string, writePaths [][]string) {
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		if len(sc.smtAssertFacts) == 0 {
			continue
		}
		out := sc.smtAssertFacts[:0]
		for _, fact := range sc.smtAssertFacts {
			if fact.Deps == nil || !fact.Deps[root] {
				out = append(out, fact) // independent of this argument
				continue
			}
			if a.factReadsOverlapWrites(fact.Expr, root, writePaths) {
				continue // a read of a written place: drop
			}
			out = append(out, fact)
		}
		sc.smtAssertFacts = out
	}
}

func (a *Analyzer) factReadsOverlapWrites(expr ast.Expr, root string, writePaths [][]string) bool {
	if len(writePaths) == 0 {
		return false // callee writes nothing through this argument: the fact cannot be falsified
	}
	var reads [][]string
	collectRootReadPaths(expr, root, &reads)
	for _, rd := range reads {
		for _, wr := range writePaths {
			if framePathsOverlap(framePath{root: root, fields: rd}, framePath{root: root, fields: wr}) {
				return true
			}
		}
	}
	return false
}

// collectRootReadPaths gathers the field paths beneath `root` that `expr` reads. A bare read of
// `root`, or any place it cannot resolve precisely, contributes the whole-root path (empty suffix),
// which overlaps every write — keeping the analysis sound. Literals contribute nothing. It mirrors
// the node set of collectSMTFactDeps, so any occurrence of `root` that made the fact a candidate is
// reached here.
func collectRootReadPaths(expr ast.Expr, root string, out *[][]string) {
	switch n := expr.(type) {
	case nil:
		return
	case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.CharLit, *ast.NullLit, *ast.ZeroedLit:
		return // literals read nothing
	case *ast.Ident:
		if n != nil && n.Name == root {
			*out = append(*out, []string{}) // whole-root read
		}
	case *ast.ParenExpr:
		if n != nil {
			collectRootReadPaths(n.Inner, root, out)
		}
	case *ast.AddrOfExpr:
		if n != nil {
			collectRootReadPaths(n.Operand, root, out)
		}
	case *ast.UnaryExpr:
		if n != nil {
			collectRootReadPaths(n.Operand, root, out)
		}
	case *ast.CastExpr:
		if n != nil {
			collectRootReadPaths(n.Operand, root, out)
		}
	case *ast.BinaryExpr:
		if n != nil {
			collectRootReadPaths(n.Left, root, out)
			collectRootReadPaths(n.Right, root, out)
		}
	case *ast.FieldExpr:
		if n != nil {
			if r, fields, ok := frameWritePath(n); ok && r == root {
				*out = append(*out, append([]string(nil), fields...))
			} else {
				collectRootReadPaths(n.Object, root, out)
			}
		}
	case *ast.IndexExpr:
		if n != nil {
			// `r.arr[i]` reads the place `r.arr` (index granularity is dropped, matching frame paths)…
			if r, fields, ok := frameWritePath(n.Object); ok && r == root {
				*out = append(*out, append([]string(nil), fields...))
			} else {
				collectRootReadPaths(n.Object, root, out)
			}
			// …and the index expression may itself read root (`b[a.i]`).
			collectRootReadPaths(n.Index, root, out)
		}
	case *ast.CallExpr:
		if n != nil {
			collectRootReadPaths(n.Func, root, out)
			for _, arg := range n.Args {
				collectRootReadPaths(arg, root, out)
			}
		}
	default:
		// An unhandled composite form: conservatively treat it as a whole-root read.
		*out = append(*out, []string{})
	}
}
