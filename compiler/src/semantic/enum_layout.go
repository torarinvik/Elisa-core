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
	byValueEnumRefs := func(ed *ast.EnumDecl) []string {
		var out []string
		if ed == nil {
			return out
		}
		for i := range ed.Variants {
			for _, pd := range ed.Variants[i].Payload {
				if nt, ok := pd.Type.(*ast.NamedType); ok && nt != nil {
					if _, isEnum := byName[nt.Name]; isEnum {
						out = append(out, hierarchyOf(nt.Name)...)
					}
				}
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
