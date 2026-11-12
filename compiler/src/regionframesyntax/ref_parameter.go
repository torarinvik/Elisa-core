package regionframesyntax

import "elisacore/src/ast"

// regionlessRefContainer reports whether a parameter type is a by-reference (`&`) region-less
// growable container, and if so returns a closure that stamps a region name onto the right AST node.
// Two container spellings qualify:
//   - `darray`/`dict`/`set` — a *ast.BuiltinTypeExpr; the region is stamped onto its `.Region`.
//   - `dstr` — a *ast.NamedType (the u8-darray string; it has no `@r` annotation surface, so it
//     can't carry a region itself). The region is stamped onto the enclosing *ast.RefType instead,
//     which resolveType then pushes DOWN onto the resolved DArrayType via stampContainerRegion —
//     the identical normalization the builtin path relies on, so both spellings produce the same
//     resolved region-carrying container type.
//
// The `&` is required: only a reference makes the callee's growth (header realloc + count) visible
// in the caller — a by-value container param copies the header, so growth would be lost (the S1
// footgun). An existing `@r` anywhere (on the ref or the container) means the user is already
// managing the region: leave it alone.
func RegionlessRefContainer(typ ast.TypeExpr) (*string, bool) {
	var ref *ast.RefType
	for {
		switch t := typ.(type) {
		case *ast.MutableType:
			typ = t.Elem
		case *ast.OwnedType:
			typ = t.Elem
		case *ast.RefType:
			if t.Region != "" {
				return nil, false
			}
			ref = t
			typ = t.Elem
		case *ast.BuiltinTypeExpr:
			if ref == nil || t.Region != "" {
				return nil, false
			}
			switch t.Name {
			case "darray", "dict", "set":
				return &t.Region, true
			}
			return nil, false
		case *ast.NamedType:
			if ref == nil || t.Name != "dstr" {
				return nil, false
			}
			// dstr has no region surface of its own; stamp the ref, which resolveType
			// pushes down onto the resolved DArrayType.
			return &ref.Region, true
		default:
			return nil, false
		}
	}
}

// regionlessRefStruct reports whether a parameter type is a by-reference (`&`) region-less STRUCT
// (a *ast.NamedType that is NOT a region-carrying container — those go through regionlessRefContainer),
// and if so returns the region field on the enclosing *ast.RefType for stamping. A struct
// has no `@r` surface of its own; the region rides on the ref, and the field-region propagation in
// analyzeFieldExpr (plus the backend's containerRegionName reading RefType.Region for struct refs)
// pushes it onto the resolved container field — the same normalization the dstr path relies on.
//
// The `&` is required for the same reason as containers: only a reference makes the callee's field
// growth (header realloc + count) visible in the caller. An existing `@r` on the ref means the user
// already manages the region: leave it alone (Stage 0's explicit form).
func RegionlessRefStruct(typ ast.TypeExpr) (*string, bool) {
	var ref *ast.RefType
	for {
		switch t := typ.(type) {
		case *ast.MutableType:
			typ = t.Elem
		case *ast.OwnedType:
			typ = t.Elem
		case *ast.RefType:
			if t.Region != "" {
				return nil, false
			}
			ref = t
			typ = t.Elem
		case *ast.NamedType:
			if ref == nil || t.Name == "dstr" {
				return nil, false
			}
			return &ref.Region, true
		case *ast.GenericType:
			// A by-ref generic struct instantiation (`v: mutable Vector[T]&`) whose container
			// field is grown needs the same caller-threaded region as a concrete struct ref.
			if ref == nil || t.Region != "" {
				return nil, false
			}
			return &ref.Region, true
		default:
			return nil, false
		}
	}
}
