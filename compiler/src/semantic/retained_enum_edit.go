package semantic

import "elisacore/src/ast"

// A retained update accepts an edit to a plain enum's variants (added, removed, renamed or with
// new payloads). As with structs, the scratch declaration phase already ran on the edited
// program; the live type takes the new variant table in place, and the functions whose types or
// text reach the enum are re-analyzed. Packed, store-backed, recursive and hierarchical enums
// carry declaration-phase state past their variant table, so their edits still fall back.

// retainedEnumEdit pairs an enum's live type with its edited declaration (from scratch).
type retainedEnumEdit struct {
	live, edit *ast.EnumDecl
	scope      scopedDecl
	et         *EnumType
}

// retainedPlainEnum: an enum with none of the features whose effects extend past its variants.
func retainedPlainEnum(d *ast.EnumDecl) bool {
	if d == nil || len(d.Annotations) != 0 || d.Packed || len(d.Common) != 0 || d.LayoutSet ||
		d.Parent != "" || d.StoreRegion != "" {
		return false
	}
	for _, v := range d.Variants {
		for _, p := range v.Payload {
			if _, tail := p.Type.(*ast.TailType); tail || p.Relation != ast.EnumPayloadRelationNone {
				return false
			}
		}
	}
	return true
}

// retainedEnumStoreSetsEqual: the edit leaves every enum's recursion and store-backing as it
// was, which the declaration phase fixes from these syntactic sets.
func retainedEnumStoreSetsEqual(oldDecls, newDecls []ast.Decl) bool {
	wrap := func(decls []ast.Decl) []scopedDecl {
		out := make([]scopedDecl, 0, len(decls))
		for _, d := range decls {
			if _, ok := d.(*ast.EnumDecl); ok {
				out = append(out, scopedDecl{Decl: d})
			}
		}
		return out
	}
	or, os := computeEnumStoreSets(wrap(oldDecls))
	nr, ns := computeEnumStoreSets(wrap(newDecls))
	same := func(x, y map[string]bool) bool {
		if len(x) != len(y) {
			return false
		}
		for k := range x {
			if !y[k] {
				return false
			}
		}
		return true
	}
	return same(or, nr) && same(os, ns)
}

// retainedConstVariantCount counts the payload-less variants, each of which the declaration
// phase records as a const value.
func retainedConstVariantCount(d *ast.EnumDecl) int {
	n := 0
	for _, v := range d.Variants {
		if len(v.Payload) == 0 {
			n++
		}
	}
	return n
}

// findLiveEnumEdits locates each edited enum's live declaration and type, and its scratch form.
func (r *RetainedAnalysis) findLiveEnumEdits(pairs [][2]*ast.EnumDecl, scratch *ast.File) ([]retainedEnumEdit, string) {
	a := r.a
	byKey := map[string]scopedDecl{}
	for _, sd := range r.structDecls() {
		if d, ok := sd.Decl.(*ast.EnumDecl); ok {
			byKey[retainedLineKey(d.Pos())+"|"+d.Name] = sd
		}
	}
	prepared := map[string]*ast.EnumDecl{}
	for _, d := range retainedWalkDecls(scratch) {
		if ed, ok := d.(*ast.EnumDecl); ok {
			prepared[retainedLineKey(ed.Pos())+"|"+ed.Name] = ed
		}
	}
	out := make([]retainedEnumEdit, 0, len(pairs))
	seen := map[string]bool{}
	for _, p := range pairs {
		// The declaration walk can list a declaration twice; it is one edit.
		key := retainedLineKey(p[0].Pos()) + "|" + p[0].Name
		if seen[key] {
			continue
		}
		seen[key] = true
		sd, ok := byKey[key]
		edit := prepared[retainedLineKey(p[1].Pos())+"|"+p[1].Name]
		if !ok || edit == nil {
			return nil, "enum " + p[0].Name + " not found in the analysis"
		}
		live := sd.Decl.(*ast.EnumDecl)
		et, _ := a.namedTypes[joinQualifiedName(sd.Namespace, live.Name)].(*EnumType)
		if et == nil || et.Packed || et.RecursivePlain || et.StoreBackedPlain || et.Parent != nil ||
			len(et.Children) != 0 || et.TagType != nil {
			return nil, "enum " + p[0].Name + " has no plain analyzed type"
		}
		out = append(out, retainedEnumEdit{live: live, edit: edit, scope: sd, et: et})
	}
	return out, ""
}

// applyEnumEdits installs each edited declaration and re-populates its type's variants in place,
// so every type already pointing at the enum sees the new variants.
func (r *RetainedAnalysis) applyEnumEdits(edits []retainedEnumEdit) {
	a := r.a
	replace := map[ast.Decl]ast.Decl{}
	for _, e := range edits {
		replace[e.live] = e.edit
	}
	r.replaceLiveDecls(replace)
	scoped := make([]scopedDecl, 0, len(edits))
	for _, e := range edits {
		for _, v := range e.et.Variants {
			delete(a.constValues, e.et.Name+"."+v.Name)
		}
		e.et.Decl = e.edit
		e.et.Variants, e.et.VariantMap = nil, map[string]*EnumVariant{}
		sd := e.scope
		sd.Decl = e.edit
		scoped = append(scoped, sd)
	}
	a.populateEnumVariants(scoped)
	a.clearRetainedTypeMemos()
}
