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

// resolveChangesPaths validates a function's `changes` clause and returns the resolved frame paths.
// Each path must root at a parameter that is a reference (writes only reach the caller through a
// ref); a non-ref or unknown root is an error. Field existence is not deeply checked in brick 1.
func (a *Analyzer) resolveChangesPaths(fn *ast.FuncDecl) []framePath {
	if fn == nil || len(fn.Changes) == 0 {
		return nil
	}
	out := make([]framePath, 0, len(fn.Changes))
	for _, p := range fn.Changes {
		sym, ok := a.currentScope.Lookup(p.Root)
		if !ok || sym == nil || sym.Kind != SymbolParam {
			a.errorf(p.Position, "`changes` target %q is not a parameter", p.Root)
			continue
		}
		if !isFrameWritableRefType(sym.Type) {
			a.errorf(p.Position, "`changes %s` has no effect: %q is not a mutable reference parameter (only writes through a ref reach the caller)", frameTargetString(p), p.Root)
			continue
		}
		out = append(out, framePath{root: p.Root, fields: append([]string(nil), p.Fields...)})
	}
	return out
}

// isFrameWritableRefType reports whether a param type is a reference whose pointee a callee body
// can write back to the caller (a plain or mutable ref; the write itself is gated elsewhere by
// mutability, but for frame purposes any ref param exposes caller-visible state).
func isFrameWritableRefType(t Type) bool {
	_, ok := t.(*RefType)
	return ok
}

// checkChangesWrite enforces the active `changes` clause at a direct write site: if the function
// declared a frame and `target`'s root is a ref parameter, the written place must be covered by a
// declared path, else it is a compile error (docs/87 channel 1).
func (a *Analyzer) checkChangesWrite(target ast.Expr) {
	if !a.currentHasChanges {
		return
	}
	a.checkChangesPlace(target, "writes to")
}

// checkChangesMutableRefArg enforces the frame at a call argument passed by MUTABLE reference: the
// callee may write the place, so it counts as a write to it (docs/87 channel 2). Immutable-borrow
// args are not writes and are not checked here.
func (a *Analyzer) checkChangesMutableRefArg(arg ast.Expr) {
	if !a.currentHasChanges {
		return
	}
	a.checkChangesPlace(arg, "may write")
}

func (a *Analyzer) checkChangesPlace(place ast.Expr, verb string) {
	root, fields, ok := frameWritePath(place)
	if !ok {
		return
	}
	sym, found := a.currentScope.Lookup(root)
	if !found || sym == nil || sym.Kind != SymbolParam || !isFrameWritableRefType(sym.Type) {
		return // not a caller-visible (ref-param) place: a local write is never a frame violation
	}
	if a.framePathCovered(root, fields) {
		return
	}
	a.errorf(place.Pos(), "%s %s, which is outside the `changes` set of %q", verb, frameWriteString(root, fields), a.currentFuncDecl.Name)
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
