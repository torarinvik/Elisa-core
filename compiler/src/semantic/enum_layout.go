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
	case "ptr":
		// docs/82 `handle: ptr`: the handle IS the record address, carried as a
		// uintptr-width integer (so all handle plumbing — coercion, switches, phis —
		// is width-machinery as usual).
		return 64
	default:
		return 32
	}
}

// HandleIsPointer reports whether the enum's opaque handle is the record address itself
// (docs/82 `layout(handle: ptr)`). The dial is a root-level fact: one store, one shape.
func (e *EnumType) HandleIsPointer() bool {
	if e == nil {
		return false
	}
	return e.Root().IndexWidth == "ptr"
}

// NullSentinelValue returns the reserved handle value meaning "absent" (docs/76 free null sentinel):
// the top value at the resolved index width. An optional child reuses this instead of paying for a
// separate presence flag — the index-world equivalent of pointer niche optimization.
func (e *EnumType) NullSentinelValue() uint64 {
	if e.HandleIsPointer() {
		return 0 // docs/82: pointer handles get null from the hardware (address 0)
	}
	bits := e.ResolvedIndexWidthBits()
	if bits >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << uint(bits)) - 1
}

// MaxNodeCount is how many real nodes a store of this enum can hold before the index width overflows
// (the top value is reserved for the null sentinel, so valid indices are 0 .. sentinel-1).
func (e *EnumType) MaxNodeCount() uint64 {
	if e.HandleIsPointer() {
		return ^uint64(0) // not index-capped: the handle is the address
	}
	return e.NullSentinelValue()
}

// enumDeclHasDirectSelfReference reports whether a plain enum is recursive: a variant payload whose
// type is the enum itself, by value (a bare `Expr`, not `Expr&` and not `darray[Expr]`). That is
// exactly the by-value self-reference the old "cannot contain Expr by value" error rejected — a
// computeRecursiveEnumSet returns the set of enum names (by declared name) that are recursive AST
// nodes: an enum reaches itself by following by-value enum-typed payload fields, directly
// (List→List) or transitively through other enums (Tree→Forest→Tree, Expr↔Stmt). Only bare
// `*ast.NamedType` payloads count — a reference (`Expr&`) or container (`darray[Expr]`) is already
// indirect and does not force the node into a store. Every enum in a recursive cycle is promoted to
// the region-backed AoS machinery.
func computeRecursiveEnumSet(decls []scopedDecl) map[string]bool {
	byName := map[string]*ast.EnumDecl{}
	for _, scoped := range decls {
		if ed, ok := scoped.Decl.(*ast.EnumDecl); ok && ed != nil {
			byName[ed.Name] = ed
		}
	}
	// docs/77: a sealed hierarchy is recursive *through the subtype relation* — a field typed as the
	// root `Node` can hold any of its refinements, so `Expr.Add(left: Node)` makes `Expr` recursive.
	// Build the descendant closure (over syntactic `is Parent` links) so a reference to enum X expands
	// to X and every refinement of X.
	descendants := map[string][]string{} // X -> all transitive refinements of X (excluding X)
	for name, ed := range byName {
		_ = name
		for p := ed.Parent; p != ""; {
			descendants[p] = append(descendants[p], ed.Name)
			parentDecl, ok := byName[p]
			if !ok {
				break
			}
			p = parentDecl.Parent
		}
	}
	hierarchyOf := func(name string) []string { // name + its descendants (a value of type name can be any of these)
		out := []string{name}
		out = append(out, descendants[name]...)
		return out
	}
	// collectEnumRefs gathers the enum names a payload field reaches by VALUE — a direct
	// `left: Node`, but also one buried in a container/optional/array/tail (`body: darray[Node]`,
	// `else: Node?`, `items: tail Node`). A node holding a darray of children IS a recursive tree
	// (the canonical AST case), so the self-typed element must promote the hierarchy to the AoS
	// handle store exactly like a direct field — otherwise the only-recursive-through-a-container
	// hierarchy stays an inline value enum and its child container dangles on a region-poly return.
	// References (`Node&`) and closure signatures are deliberately NOT descended: a borrow or a
	// func-type mention is not by-value containment.
	var collectEnumRefs func(t ast.TypeExpr, out *[]string)
	collectEnumRefs = func(t ast.TypeExpr, out *[]string) {
		switch tt := t.(type) {
		case *ast.NamedType:
			if tt != nil {
				if _, isEnum := byName[tt.Name]; isEnum {
					*out = append(*out, hierarchyOf(tt.Name)...)
				}
			}
		case *ast.GenericType:
			if tt != nil {
				if _, isEnum := byName[tt.Name]; isEnum {
					*out = append(*out, hierarchyOf(tt.Name)...)
				}
				for _, arg := range tt.Args {
					collectEnumRefs(arg, out)
				}
			}
		case *ast.BuiltinTypeExpr: // darray[T], dict[K,V], set[T], view[T]: a self-typed element makes the node recursive
			if tt != nil {
				for _, arg := range tt.TypeArgs {
					collectEnumRefs(arg, out)
				}
			}
		case *ast.OptionalTypeExpr:
			if tt != nil {
				collectEnumRefs(tt.Value, out)
			}
		case *ast.ArrayType:
			if tt != nil {
				collectEnumRefs(tt.Elem, out)
			}
		case *ast.TailType:
			if tt != nil {
				collectEnumRefs(tt.Elem, out)
			}
		case *ast.MutableType:
			if tt != nil {
				collectEnumRefs(tt.Elem, out)
			}
		}
	}
	byValueEnumRefs := func(ed *ast.EnumDecl) []string {
		var out []string
		if ed == nil {
			return out
		}
		for i := range ed.Variants {
			for _, pd := range ed.Variants[i].Payload {
				collectEnumRefs(pd.Type, &out)
			}
		}
		return out
	}
	result := map[string]bool{}
	for start := range byName {
		visited := map[string]bool{}
		var reachesStart func(cur string) bool
		reachesStart = func(cur string) bool {
			for _, next := range byValueEnumRefs(byName[cur]) {
				if next == start {
					return true
				}
				if !visited[next] {
					visited[next] = true
					if reachesStart(next) {
						return true
					}
				}
			}
			return false
		}
		if reachesStart(start) {
			result[start] = true
		}
	}
	// `common(...)` fields are per-node shared metadata, and the store row is where they live: the
	// value layout has no slot for them (constructor args were rejected, reads failed). Declaring
	// commons therefore promotes the hierarchy to the region-backed store machinery even without
	// by-value recursion — "you asked for node metadata; you have nodes, and nodes live in stores".
	for name, ed := range byName {
		if !ed.Packed && len(ed.Common) > 0 {
			result[name] = true
		}
	}
	// If any member of a hierarchy is recursive, the whole hierarchy is region-backed (it shares one
	// store), so promote the root and every refinement — including non-recursive leaves and the root.
	rootOf := func(name string) string {
		for {
			ed, ok := byName[name]
			if !ok || ed.Parent == "" {
				return name
			}
			name = ed.Parent
		}
	}
	for name := range byName {
		if !result[name] {
			continue
		}
		root := rootOf(name)
		result[root] = true
		for _, d := range descendants[root] {
			result[d] = true
		}
	}
	return result
}

// reference is wrapped in a reference type-expr and a container puts the name behind type args, so a
// *bare* `BuiltinTypeExpr{Name: <enum>}` with no type args is the recursive-AST-node case (docs/76).
func enumDeclHasDirectSelfReference(n *ast.EnumDecl) bool {
	if n == nil {
		return false
	}
	for i := range n.Variants {
		for _, pd := range n.Variants[i].Payload {
			// A bare user-type reference `Expr` parses to *ast.NamedType (builtins are
			// BuiltinTypeExpr; a reference `Expr&` is wrapped in *ast.RefType; a container puts the
			// name behind type args). So a direct *ast.NamedType whose name is the enum is exactly the
			// by-value self-reference.
			if nt, ok := pd.Type.(*ast.NamedType); ok && nt != nil && nt.Name == n.Name {
				return true
			}
		}
	}
	return false
}

// validateEnumLayout enforces the docs/76 constraints on the `layout` suffix of an enum: only `aos`
// and `soa` are enum layouts (`c`/`packed` are struct-FFI layouts), `(sparse)` is SoA-only, and
// `(index: uN)` is meaningful only for the columnar/array layouts that carry handles.
func (a *Analyzer) validateEnumLayout(enumDecl *ast.EnumDecl, enumType *EnumType) {
	if enumDecl == nil {
		return
	}
	// docs/77: `common(...)` fields live on the hierarchy ROOT — one store, one row shape. A
	// sub-category cannot add its own (the root's row has no slot for it; accepting it here only
	// to fail at codegen offset lookup would be a worse error).
	if len(enumDecl.Common) > 0 && enumType != nil && enumType.Parent != nil {
		a.errorf(enumDecl.Pos(), "enum %q: `common(...)` fields must be declared on the hierarchy root %q, not on a sub-category (one store, one row shape)", enumDecl.Name, enumType.Root().Name)
	}
	if !enumDecl.LayoutSet {
		return
	}
	// docs/82 `handle: ptr` constraints, enforced as compile errors:
	// pointer handles need stable record addresses (the AoS chunk store never relocates;
	// SoA's darray-backed columns do), and only a recursive region-backed enum has a
	// store-resident record to point at.
	if enumDecl.IndexWidth == "ptr" {
		if enumDecl.Layout == ast.StructLayoutSOA {
			a.errorf(enumDecl.Pos(), "enum %q: `handle: ptr` requires stable record addresses; `layout(soa)` columns relocate — use the AoS layout or an index handle (`handle: u32`)", enumDecl.Name)
		} else if enumType != nil && !enumType.RecursivePlain {
			a.errorf(enumDecl.Pos(), "enum %q: `handle: ptr` requires a recursive region-backed enum (an AoS store record to point at); this enum has no store — use an index width or drop the option", enumDecl.Name)
		}
	}
	switch enumDecl.Layout {
	case ast.StructLayoutC, ast.StructLayoutPacked:
		a.errorf(enumDecl.Pos(), "enum %q layout must be `aos` or `soa`; `c` and `packed` are struct layouts, not enum layouts", enumDecl.Name)
	}
	if enumDecl.LayoutSparse && enumDecl.Layout != ast.StructLayoutSOA {
		a.errorf(enumDecl.Pos(), "enum %q: `(sparse)` requires `layout(soa)` (variant-sparse payload columns)", enumDecl.Name)
	}
	// docs/82: the handle width is valid with `aos`, `soa`, or the mode-less `layout(handle: uN)`
	// form (StructLayoutDefault — keeps the compiler's default mode). Only the struct-FFI modes
	// (already rejected above) can't carry it.
	if enumDecl.IndexWidth != "" && enumDecl.Layout != ast.StructLayoutSOA && enumDecl.Layout != ast.StructLayoutAOS && enumDecl.Layout != ast.StructLayoutDefault {
		a.errorf(enumDecl.Pos(), "enum %q: `(handle: %s)` requires `layout(soa)`, `layout(aos)`, or the mode-less `layout(handle: %s)` form", enumDecl.Name, enumDecl.IndexWidth, enumDecl.IndexWidth)
	}
}
