package semantic

import (
	"strings"

	"elisacore/src/ast"
)

// checkFrameWrite enforces the active frame clauses at a direct write site: if `target`'s root is a
// ref parameter, the written place must be inside the `changes` set (if any) AND must not overlap a
// `preserves` place (docs/87 channel 1).
func (a *Analyzer) checkFrameWrite(target ast.Expr) {
	if !a.currentHasChanges && !a.currentHasPreserves {
		return
	}
	a.checkFramePlace(target, "writes to")
}

// checkFrameMutableRefArg enforces the frame at a call argument passed by MUTABLE reference: the
// callee may write the place, so it counts as a write to it (docs/87 channel 2). Immutable-borrow
// args are not writes and are not checked here.
//
// calleeSuffixes is the callee's effective frame for THIS parameter (docs/87 87-3): the field
// suffixes it may write beneath the argument place. When non-nil (the callee declared a bounding
// `changes`/`fulfills` frame), the arg is REFINED — only `arg ⊕ suffix` is treated as written, so
// `f(&r.x)` where `f changes self.a` is checked as a write to `r.x.a`, not the whole `r.x`. When nil
// (the callee's writes are unbounded), the conservative whole-place rule applies.
func (a *Analyzer) checkFrameMutableRefArg(arg ast.Expr, calleeSuffixes [][]string) {
	if !a.currentHasChanges && !a.currentHasPreserves {
		return
	}
	if calleeSuffixes == nil {
		a.checkFramePlace(arg, "may write")
		return
	}
	root, fields, ok := frameWritePath(arg)
	if !ok {
		return
	}
	for _, suffix := range calleeSuffixes {
		full := append(append([]string(nil), fields...), suffix...)
		a.checkFrameResolvedPlace(arg, root, full, "may write")
	}
}

// calleeFrameSuffixesForParam returns the field suffixes a callee may write beneath its parameter i
// (docs/87 87-3), or nil if the callee declared no bounding frame (writes unbounded → caller stays
// conservative). A callee that declares a frame but writes nothing through this param yields an
// empty (non-nil) slice, refining the arg to "no caller-visible write".
func calleeFrameSuffixesForParam(ft *FuncType, i int) [][]string {
	if ft == nil || !ft.FrameBounded {
		return nil
	}
	suffixes := [][]string{}
	for _, w := range ft.FrameWrites {
		if w.ParamIndex == i {
			suffixes = append(suffixes, w.Fields)
		}
	}
	return suffixes
}

// checkFrameForMutatingBuiltinMethod enforces the active frame clauses at a mutating builtin method
// call: `b.items.push(v)` writes b.items, so — exactly like the direct assignment `b.items <- …` — it
// must lie inside the `changes` set and must not overlap a `preserves` place. The receiver of such a
// builtin is a VALUE, not a `mutable T&` arg, so checkFrameMutableRefArg never sees it; this closes the
// frame-channel analogue of the predFact builtin-method hole.
func (a *Analyzer) checkFrameForMutatingBuiltinMethod(expr *ast.CallExpr) {
	if !a.currentHasChanges && !a.currentHasPreserves {
		return
	}
	if recv, ok := a.mutatingBuiltinMethodReceiver(expr); ok {
		a.checkFramePlace(recv, "writes to")
	}
}

// resolveFrameAliasPath rewrites a write path rooted at a borrow LOCAL to the underlying place it
// borrows: `h := &r.health` makes a write to `h` (or `h.sub`) a write to `r.health` (or `r.health.sub`).
// Only an immutable local bound to an address-of expression is followed (a by-value copy does not alias
// the source); chained borrows resolve transitively, with `seen` guarding a cycle. A non-borrow root is
// returned unchanged.
func (a *Analyzer) resolveFrameAliasPath(root string, fields []string, seen map[string]bool) (string, []string) {
	if a.currentScope == nil || root == "" || seen[root] {
		return root, fields
	}
	seen[root] = true
	sym, ok := a.currentScope.Lookup(root)
	if !ok || sym == nil || sym.Kind != SymbolLocal {
		return root, fields
	}
	value, ok := a.immutableValueExprForSymbol(sym)
	if !ok || value == nil {
		return root, fields
	}
	if _, isAddr := stripOptimizationParens(value).(*ast.AddrOfExpr); !isAddr {
		return root, fields // a by-value binding does not alias the source place
	}
	uroot, ufields, ok := frameWritePath(value)
	if !ok || uroot == root {
		return root, fields
	}
	combined := append(append([]string(nil), ufields...), fields...)
	return a.resolveFrameAliasPath(uroot, combined, seen)
}

func (a *Analyzer) checkFramePlace(place ast.Expr, verb string) {
	root, fields, ok := frameWritePath(place)
	if !ok {
		return
	}
	// A write laundered through a borrow LOCAL (`h := &r.health; h <- 999`) writes the underlying place,
	// so resolve the local to the place it borrows before checking the frame — otherwise the write looks
	// like a harmless local write and slips past `changes`/`preserves` (audit cluster C, frame channel).
	root, fields = a.resolveFrameAliasPath(root, fields, map[string]bool{})
	a.checkFrameResolvedPlace(place, root, fields, verb)
}

// checkFrameResolvedPlace enforces the active frame clauses against an already-resolved (root,
// fields) place, reporting at `place`. Shared by the direct-write path (checkFramePlace) and the
// 87-3 refined mutable-ref-arg path (which synthesizes the place by appending the callee's frame
// suffix to the argument place).
func (a *Analyzer) checkFrameResolvedPlace(place ast.Expr, root string, fields []string, verb string) {
	sym, found := a.currentScope.Lookup(root)
	if !found || sym == nil || sym.Kind != SymbolParam || !isFrameWritableRefType(sym.Type) {
		return // not a caller-visible (ref-param) place: a local write is never a frame violation
	}
	written := framePath{root: root, fields: fields}
	if a.currentHasChanges && !a.framePathCovered(root, fields) {
		a.errorf(place.Pos(), "%s %s, which is outside the `changes` set of %q", verb, frameWriteString(root, fields), a.currentFuncDecl.Name)
		return
	}
	for _, pres := range a.currentPreservesPaths {
		if framePathsOverlap(written, pres) {
			a.errorf(place.Pos(), "%s %s, which %q `preserves`", verb, frameWriteString(root, fields), a.currentFuncDecl.Name)
			return
		}
	}
}

// framePathsOverlap reports whether two frame paths touch the same storage: same root and one field
// chain is a prefix of the other (writing `r.health` touches `r.health.x`, and vice versa).
func framePathsOverlap(a, b framePath) bool {
	if a.root != b.root {
		return false
	}
	shorter := a.fields
	if len(b.fields) < len(shorter) {
		shorter = b.fields
	}
	for i := range shorter {
		if a.fields[i] != b.fields[i] {
			return false
		}
	}
	return true
}

// framePathCovered reports whether a write to (root, fields) is permitted by some declared path:
// the declared path shares the root and is a PREFIX of the written field chain (declared `r.px`
// covers `r.px` and `r.px.sub`; declared bare `r` covers everything under r).
func (a *Analyzer) framePathCovered(root string, fields []string) bool {
	for _, fp := range a.currentChangesPaths {
		if fp.root != root || len(fp.fields) > len(fields) {
			continue
		}
		match := true
		for i, f := range fp.fields {
			if fields[i] != f {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// frameWritePath extracts a field-granular (root, fields) place from an assignment target or a
// ref-argument expression, dropping index steps (field granularity) and address-of/paren wrappers.
func frameWritePath(expr ast.Expr) (string, []string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil {
			return "", nil, false
		}
		return n.Name, nil, true
	case *ast.ParenExpr:
		return frameWritePath(n.Inner)
	case *ast.AddrOfExpr:
		return frameWritePath(n.Operand)
	case *ast.FieldExpr:
		root, fields, ok := frameWritePath(n.Object)
		if !ok {
			return "", nil, false
		}
		return root, append(fields, n.Field), true
	case *ast.IndexExpr:
		// Field granularity: `r.arr[i]` is treated as a write to `r.arr`.
		return frameWritePath(n.Object)
	default:
		return "", nil, false
	}
}

func frameWriteString(root string, fields []string) string {
	if len(fields) == 0 {
		return root
	}
	return root + "." + strings.Join(fields, ".")
}

func frameTargetString(p ast.EnsuresPath) string {
	return frameWriteString(p.Root, p.Fields)
}
