package semantic

import "elisacore/src/ast"

// `extern resource Name` (docs/127 §3.2). The parser desugared it into the handle type
// `Name__native` and the struct `Name{__handle: Name__native}`; these checks add the two
// rules the sugar promises:
//
//	D2: a resource declares `__drop__` in its module, so the native handle is released on
//	    every exit path (the destructor induces affinity, so a second release is the
//	    ordinary use-after-move error and a copy is impossible);
//	D3: an extern never returns a BORROWED resource (`Name&`); ownership of a handle a
//	    native constructor hands back is the point, so it returns `Name` or `Name?`.

func (a *Analyzer) checkResourceDrops() {
	for _, t := range a.namedTypes {
		st, ok := t.(*StructType)
		if !ok || st == nil || !st.Resource || st.DropHook != "" {
			continue
		}
		pos := st.Decl.Pos()
		a.errorf(pos, "extern resource %q declares no `__drop__`; add `def __drop__(self: %s)` in this module so the native handle is released on every exit path", st.Name, st.Name)
	}
}

// resourceStruct returns the resource struct behind t (direct, or through a non-null ref),
// with whether t was a reference.
func resourceStruct(t Type) (*StructType, bool, bool) {
	if ref, ok := t.(*RefType); ok && ref != nil {
		if st, ok := ref.Elem.(*StructType); ok && st != nil && st.Resource {
			return st, true, true
		}
		return nil, false, false
	}
	if st, ok := t.(*StructType); ok && st != nil && st.Resource {
		return st, false, true
	}
	return nil, false, false
}

// IsResourceStructType reports whether t is a resource struct (the backend's entry point).
func IsResourceStructType(t Type) bool {
	st, ok := t.(*StructType)
	return ok && st != nil && st.Resource
}

func (a *Analyzer) checkExternResourceSignature(fn *ast.ExternFuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil {
		return
	}
	if st, isRef, ok := resourceStruct(fnType.Return); ok && isRef {
		a.errorf(fn.Pos(), "extern function %q returns a borrowed resource handle %s&; return %s (owned) so the caller releases it, or %s? when the call can fail", fn.Name, st.Name, st.Name, st.Name)
	}
}
