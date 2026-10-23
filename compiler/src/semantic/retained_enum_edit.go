package semantic

import (
	"regexp"
	"strings"

	"elisacore/src/ast"
)

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
	return d != nil && d.Parent == "" && retainedEnumOwnStateIsVariants(d)
}

// retainedRefinementEnum: a refinement (`enum E is P:`) with no annotations, layout, store
// region or common fields of its own. Beyond its variants its declaration-phase state is its
// place in the hierarchy (parent link, leaf tags, inherited common fields), which an edit
// recomputes for the whole hierarchy.
func retainedRefinementEnum(d *ast.EnumDecl) bool {
	return d != nil && d.Parent != "" && retainedEnumOwnStateIsVariants(d)
}

func retainedEnumOwnStateIsVariants(d *ast.EnumDecl) bool {
	if len(d.Annotations) != 0 || d.Packed || len(d.Common) != 0 || d.LayoutSet || d.StoreRegion != "" {
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

// retainedStoreBackedEnums lists the enums the declaration phase lowers through a store.
func retainedStoreBackedEnums(decls []ast.Decl) map[string]bool {
	var scoped []scopedDecl
	for _, d := range decls {
		if _, ok := d.(*ast.EnumDecl); ok {
			scoped = append(scoped, scopedDecl{Decl: d})
		}
	}
	_, storeBacked := computeEnumStoreSets(scoped)
	return storeBacked
}

// retainedConstVariantCount counts the variants the declaration phase records as const values:
// every variant of a store-backed enum (its tag type's members), else the payload-less ones.
func retainedConstVariantCount(d *ast.EnumDecl, storeBacked bool) int {
	if storeBacked {
		return len(d.Variants)
	}
	n := 0
	for _, v := range d.Variants {
		if len(v.Payload) == 0 {
			n++
		}
	}
	return n
}

// findLiveEnumEdits locates each edited plain enum's live declaration and type, and its scratch
// form.
func (r *RetainedAnalysis) findLiveEnumEdits(pairs [][2]*ast.EnumDecl, scratch *ast.File) ([]retainedEnumEdit, string) {
	return r.findLiveEnumEditsWhere(pairs, scratch, func(et *EnumType) bool {
		return !et.Packed && !et.RecursivePlain && !et.StoreBackedPlain && et.Parent == nil && len(et.Children) == 0 && et.TagType == nil
	})
}

// findLiveHierarchyEdits locates each edited refinement as findLiveEnumEdits does, and returns
// with them every enum of their hierarchies in declaration order.
func (r *RetainedAnalysis) findLiveHierarchyEdits(pairs [][2]*ast.EnumDecl, scratch *ast.File) ([]retainedEnumEdit, []scopedDecl, string) {
	edits, why := r.findLiveEnumEditsWhere(pairs, scratch, func(et *EnumType) bool {
		return et.Parent != nil && !et.SourcePacked
	})
	if why != "" {
		return nil, nil, why
	}
	roots := map[*EnumType]bool{}
	for _, e := range edits {
		roots[e.et.Root()] = true
	}
	var members []scopedDecl
	for _, sd := range r.structDecls() {
		if d, ok := sd.Decl.(*ast.EnumDecl); ok {
			if et, _ := r.a.namedTypes[joinQualifiedName(sd.Namespace, d.Name)].(*EnumType); et != nil && roots[et.Root()] {
				members = append(members, sd)
			}
		}
	}
	return edits, members, ""
}

// findLiveEnumEditsWhere pairs each edit with its live type, which accept must take.
func (r *RetainedAnalysis) findLiveEnumEditsWhere(pairs [][2]*ast.EnumDecl, scratch *ast.File, accept func(*EnumType) bool) ([]retainedEnumEdit, string) {
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
	for _, p := range pairs {
		sd, ok := byKey[retainedLineKey(p[0].Pos())+"|"+p[0].Name]
		edit := prepared[retainedLineKey(p[1].Pos())+"|"+p[1].Name]
		if !ok || edit == nil {
			return nil, "enum " + p[0].Name + " not found in the analysis"
		}
		live := sd.Decl.(*ast.EnumDecl)
		et, _ := a.namedTypes[joinQualifiedName(sd.Namespace, live.Name)].(*EnumType)
		if et == nil || !accept(et) {
			return nil, "enum " + p[0].Name + " has no analyzed type of its kind"
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

// applyHierarchyEdits installs edited refinements and recomputes their hierarchies' variant
// tables, parent links, leaf tags and inherited common fields in place. The scratch phase
// already reported this phase's diagnostics, so the rerun is silent. It reports whether the
// variant holding tag 0 (the zeroed value of each hierarchy enum) moved, which reaches checks
// in functions that never name the edited enum.
func (r *RetainedAnalysis) applyHierarchyEdits(edits []retainedEnumEdit, members []scopedDecl) (tagZeroMoved bool) {
	a := r.a
	replace := map[ast.Decl]ast.Decl{}
	for _, e := range edits {
		replace[e.live] = e.edit
	}
	r.replaceLiveDecls(replace)
	scoped := make([]scopedDecl, len(members))
	types := make([]*EnumType, len(members))
	for k, sd := range members {
		if edit, ok := replace[sd.Decl]; ok {
			sd.Decl = edit
		}
		d := sd.Decl.(*ast.EnumDecl)
		scoped[k] = sd
		types[k] = a.namedTypes[joinQualifiedName(sd.Namespace, d.Name)].(*EnumType)
	}
	tagZero := func() map[string]bool {
		out := map[string]bool{}
		for _, et := range types {
			for _, v := range et.Variants {
				if v.Tag == 0 {
					out[et.Name+"."+v.Name] = true
				}
			}
		}
		return out
	}
	before := tagZero()
	for k, et := range types {
		for _, v := range et.Variants {
			delete(a.constValues, et.Name+"."+v.Name)
		}
		if et.TagType != nil {
			for _, m := range et.TagType.Members {
				delete(a.constValues, et.TagType.Name+"."+m.Name)
			}
			et.TagType.Members, et.TagType.MemberMap = nil, map[string]*ConstEnumMember{}
		}
		et.Decl = scoped[k].Decl.(*ast.EnumDecl)
		et.Variants, et.VariantMap = nil, map[string]*EnumVariant{}
		et.Parent, et.Children = nil, nil
		et.Common = map[string]Field{}
	}
	saved := a.suppressDiagnostics
	a.suppressDiagnostics = true
	a.populateEnumVariants(scoped)
	a.assignHierarchyEnumTags(scoped)
	a.inheritHierarchyCommonFields(scoped)
	a.suppressDiagnostics = saved
	a.clearRetainedTypeMemos()
	after := tagZero()
	if len(before) != len(after) {
		return true
	}
	for k := range before {
		if !after[k] {
			return true
		}
	}
	return false
}

// retainedHierarchyUsers reports whether a function's source text can observe a hierarchy edit:
// a match naming an edited enum's variants (`E.`) with no catch-all arm, whose exhaustiveness
// the edit changes, or reflection over a hierarchy enum with `variants(...)`. Constructors, `is`
// tests and a match a catch-all arm completes see the same types and coverage; a category arm
// still covers its whole category.
func retainedHierarchyUsers(span string, edited, members map[string]bool) bool {
	toks := retainedIdentToken.FindAllStringIndex(span, -1)
	reflects, namesMember, namesEdited := false, false, false
	for _, t := range toks {
		tok := span[t[0]:t[1]]
		if edited[tok] && t[1] < len(span) && span[t[1]] == '.' {
			namesEdited = true
		}
		reflects = reflects || tok == "variants"
		namesMember = namesMember || members[tok]
	}
	if reflects && namesMember {
		return true
	}
	return namesEdited && retainedOpenMatchNames(span, edited)
}

var (
	retainedMatchHeader = regexp.MustCompile(`(^|[^A-Za-z0-9_])match\b`)
	retainedCatchAllArm = regexp.MustCompile(`^(_|[a-z_][A-Za-z0-9_]*)\s*:`)
)

// retainedOpenMatchNames reports whether span holds a match that names an edited enum's
// variant in an arm and has no catch-all arm (`_:` or a bare lowercase bind). A match it cannot
// lay out by indentation (a header not ending its line, arms not found) counts as open.
func retainedOpenMatchNames(span string, edited map[string]bool) bool {
	lines := strings.Split(span, "\n")
	indent := func(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }
	names := func(l string) bool {
		for _, t := range retainedIdentToken.FindAllStringIndex(l, -1) {
			if edited[l[t[0]:t[1]]] && t[1] < len(l) && l[t[1]] == '.' {
				return true
			}
		}
		return false
	}
	for i, line := range lines {
		code := line
		if hash := strings.IndexByte(code, '#'); hash >= 0 {
			code = code[:hash]
		}
		if !retainedMatchHeader.MatchString(code) || retainedInCommentOrString(line, retainedMatchHeader.FindStringIndex(code)[0]) {
			continue
		}
		if !strings.HasSuffix(strings.TrimRight(code, " "), ":") {
			if names(line) {
				return true
			}
			continue
		}
		header, arm := indent(line), -1
		named, catchAll := false, false
		for _, l := range lines[i+1:] {
			if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
				continue
			}
			in := indent(l)
			if in <= header {
				break
			}
			if arm < 0 {
				arm = in
			}
			if in != arm {
				continue
			}
			body := strings.TrimSpace(l)
			named = named || names(body)
			catchAll = catchAll || retainedCatchAllArm.MatchString(body)
		}
		if arm < 0 || (named && !catchAll) {
			return true
		}
	}
	return false
}
