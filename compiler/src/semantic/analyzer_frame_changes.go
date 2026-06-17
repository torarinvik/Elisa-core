package semantic

import (
	"strings"

	"elisacore/src/ast"
)

// framePath is a resolved `changes` target (docs/87): a ref parameter root plus a field chain.
// Index steps are dropped at resolve/extract time, so the path is field-granular (`r.arr` covers
// every `r.arr[i]`).
type framePath struct {
	root   string
	fields []string
}

// resolveFramePaths validates a function's frame clause (`changes` or `preserves`) and returns the
// resolved paths. Each path must root at a parameter that is a reference (writes only reach the
// caller through a ref); a non-ref or unknown root is an error. clause names the clause for errors.
func (a *Analyzer) resolveFramePaths(paths []ast.EnsuresPath, clause string) []framePath {
	if len(paths) == 0 {
		return nil
	}
	out := make([]framePath, 0, len(paths))
	for _, p := range paths {
		sym, ok := a.currentScope.Lookup(p.Root)
		if !ok || sym == nil || sym.Kind != SymbolParam {
			a.errorf(p.Position, "`%s` target %q is not a parameter", clause, p.Root)
			continue
		}
		if !isFrameWritableRefType(sym.Type) {
			a.errorf(p.Position, "`%s %s` has no effect: %q is not a mutable reference parameter (only writes through a ref reach the caller)", clause, frameTargetString(p), p.Root)
			continue
		}
		out = append(out, framePath{root: p.Root, fields: append([]string(nil), p.Fields...)})
	}
	return out
}

// checkFrameConsistency enforces the docs/87 §7 identity `preserves Y ⟺ Y ∩ changes(f) = ∅`: a
// place may not be in both clauses. An overlap is contradictory (it would be both required-writable
// and forbidden-to-write).
func (a *Analyzer) checkFrameConsistency(fn *ast.FuncDecl) {
	for _, pres := range a.currentPreservesPaths {
		for _, chg := range a.currentChangesPaths {
			if framePathsOverlap(pres, chg) {
				a.errorf(fn.Pos(), "`preserves %s` conflicts with `changes %s`: a place cannot be both preserved and changed", frameWriteString(pres.root, pres.fields), frameWriteString(chg.root, chg.fields))
			}
		}
	}
}

// isFrameWritableRefType reports whether a param type is a reference whose pointee a callee body
// can write back to the caller (a plain or mutable ref; the write itself is gated elsewhere by
// mutability, but for frame purposes any ref param exposes caller-visible state).
func isFrameWritableRefType(t Type) bool {
	_, ok := t.(*RefType)
	return ok
}

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
func (a *Analyzer) checkFrameMutableRefArg(arg ast.Expr) {
	if !a.currentHasChanges && !a.currentHasPreserves {
		return
	}
	a.checkFramePlace(arg, "may write")
}

func (a *Analyzer) checkFramePlace(place ast.Expr, verb string) {
	root, fields, ok := frameWritePath(place)
	if !ok {
		return
	}
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
