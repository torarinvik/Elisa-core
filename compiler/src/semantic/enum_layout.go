package semantic

import "elisacore/src/ast"

// ResolvedIndexWidthBits returns the bit width of the enum's opaque index handle (docs/76): the
// explicit `(index: uN)` if set, else the default u32 (32 bits). The handle is a node-index, so this
// caps one store at MaxNodeCount() nodes; the top value at the width is the free null sentinel.
func (e *EnumType) ResolvedIndexWidthBits() int {
	if e == nil {
		return 32
	}
	switch e.IndexWidth {
	case "u8":
		return 8
	case "u16":
		return 16
	case "u64":
		return 64
	default:
		return 32
	}
}

// NullSentinelValue returns the reserved handle value meaning "absent" (docs/76 free null sentinel):
// the top value at the resolved index width. An optional child reuses this instead of paying for a
// separate presence flag — the index-world equivalent of pointer niche optimization.
func (e *EnumType) NullSentinelValue() uint64 {
	bits := e.ResolvedIndexWidthBits()
	if bits >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << uint(bits)) - 1
}

// MaxNodeCount is how many real nodes a store of this enum can hold before the index width overflows
// (the top value is reserved for the null sentinel, so valid indices are 0 .. sentinel-1).
func (e *EnumType) MaxNodeCount() uint64 {
	return e.NullSentinelValue()
}

// validateEnumLayout enforces the docs/76 constraints on the `layout` suffix of an enum: only `aos`
// and `soa` are enum layouts (`c`/`packed` are struct-FFI layouts), `(sparse)` is SoA-only, and
// `(index: uN)` is meaningful only for the columnar/array layouts that carry handles.
func (a *Analyzer) validateEnumLayout(enumDecl *ast.EnumDecl) {
	if enumDecl == nil || !enumDecl.LayoutSet {
		return
	}
	switch enumDecl.Layout {
	case ast.StructLayoutC, ast.StructLayoutPacked:
		a.errorf(enumDecl.Pos(), "enum %q layout must be `aos` or `soa`; `c` and `packed` are struct layouts, not enum layouts", enumDecl.Name)
	}
	if enumDecl.LayoutSparse && enumDecl.Layout != ast.StructLayoutSOA {
		a.errorf(enumDecl.Pos(), "enum %q: `(sparse)` requires `layout soa` (variant-sparse payload columns)", enumDecl.Name)
	}
	if enumDecl.IndexWidth != "" && enumDecl.Layout != ast.StructLayoutSOA && enumDecl.Layout != ast.StructLayoutAOS {
		a.errorf(enumDecl.Pos(), "enum %q: `(index: %s)` requires `layout soa` or `layout aos`", enumDecl.Name, enumDecl.IndexWidth)
	}
}
