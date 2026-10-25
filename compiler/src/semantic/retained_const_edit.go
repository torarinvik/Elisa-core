package semantic

import (
	"regexp"
	"strings"

	"elisacore/src/ast"
)

// A retained update accepts a new value for a typed const. The value is evaluated in the body
// phase (analyzeScopedDecl). When other declarations and resolved signature shapes do not
// depend on it, the live analysis re-evaluates the const and rechecks its users. Value
// pre/postconditions also invalidate their transitive callers, whose proofs read them.

// retainedConstHeader is a const's text up to its value: `const NAME: TYPE`.
func retainedConstHeader(span string) string {
	if i := strings.Index(span, "="); i >= 0 {
		return strings.TrimSpace(span[:i])
	}
	return span
}

// retainedConstValueEdit: the same typed const with only its value changed.
func retainedConstValueEdit(old, cur *ast.ConstDecl, oldSpan, newSpan string) bool {
	return old.Name == cur.Name && old.Type != nil && cur.Type != nil &&
		retainedConstHeader(oldSpan) == retainedConstHeader(newSpan)
}

// retainedNamesPattern matches any of names as a whole identifier.
func retainedNamesPattern(names map[string]bool) *regexp.Regexp {
	quoted := make([]string, 0, len(names))
	for n := range names {
		quoted = append(quoted, regexp.QuoteMeta(n))
	}
	return regexp.MustCompile(`(^|[^A-Za-z0-9_])(` + strings.Join(quoted, "|") + `)([^A-Za-z0-9_]|$)`)
}

// A literal-name miss proves the identifier regexp cannot match. Most function
// spans do not mention a changed type, so avoid the regexp's boundary scan.
func retainedNameTextMatch(text string, names map[string]bool, pattern *regexp.Regexp) bool {
	for name := range names {
		if strings.Contains(text, name) {
			return pattern.MatchString(text)
		}
	}
	return false
}

// replaceLiveDecls swaps declarations in the live file and the retained declaration list.
func (r *RetainedAnalysis) replaceLiveDecls(replace map[ast.Decl]ast.Decl) {
	var swap func(decls []ast.Decl)
	swap = func(decls []ast.Decl) {
		for i, d := range decls {
			if n, ok := replace[d]; ok {
				decls[i] = n
			}
			if ns, ok := d.(*ast.NamespaceDecl); ok {
				swap(ns.Decls)
			}
		}
	}
	swap(r.a.file.Decls)
	for i, sd := range r.decls {
		if n, ok := replace[sd.Decl]; ok {
			r.decls[i].Decl = n
		}
	}
}

// applyConstEdits installs each edited const and returns their scoped declarations, which the
// body phase re-evaluates.
func (r *RetainedAnalysis) applyConstEdits(edits [][2]*ast.ConstDecl) []scopedDecl {
	replace := map[ast.Decl]ast.Decl{}
	for _, e := range edits {
		r.a.clearDeclarationIndexBoundsProofs(e[0])
		replace[e[0]] = e[1]
	}
	r.replaceLiveDecls(replace)
	out := make([]scopedDecl, 0, len(edits))
	for _, sd := range r.decls {
		cd, ok := sd.Decl.(*ast.ConstDecl)
		if !ok {
			continue
		}
		for _, e := range edits {
			if cd == e[1] {
				if sym, found := r.a.globalScope.Lookup(joinQualifiedName(sd.Namespace, cd.Name)); found {
					sym.Node = cd
				}
				out = append(out, sd)
			}
		}
	}
	return out
}

// removeLiveConsts drops removed consts and globals from the live analysis: their declaration,
// symbol and value.
func (r *RetainedAnalysis) removeLiveConsts(cds []ast.Decl) {
	a := r.a
	gone := map[ast.Decl]bool{}
	for _, cd := range cds {
		a.clearDeclarationIndexBoundsProofs(cd)
		gone[cd] = true
	}
	decls := r.decls[:0]
	for _, sd := range r.decls {
		if gone[sd.Decl] {
			name := joinQualifiedName(sd.Namespace, retainedValueDeclName(sd.Decl))
			delete(a.globalScope.Symbols, name)
			delete(a.constValues, name)
			delete(a.declVisibility, sd.Decl)
			continue
		}
		decls = append(decls, sd)
	}
	r.decls = decls
	var prune func(decls []ast.Decl) []ast.Decl
	prune = func(decls []ast.Decl) []ast.Decl {
		out := decls[:0]
		for _, d := range decls {
			if n, ok := d.(*ast.NamespaceDecl); ok {
				n.Decls = prune(n.Decls)
			}
			if !gone[d] {
				out = append(out, d)
			}
		}
		return out
	}
	a.file.Decls = prune(a.file.Decls)
}

// retainedValueDeclName names an added const or global.
func retainedValueDeclName(d ast.Decl) string {
	switch n := d.(type) {
	case *ast.ConstDecl:
		return n.Name
	case *ast.GlobalDecl:
		return n.Name
	}
	return ""
}

// Value pre/postconditions are evaluated against the live const table during
// body/proof analysis. Other header uses (array extents, defaults, annotations,
// named/frame contracts, etc.) retain declaration-phase results and must fall back.
func retainedConstValueContractOnly(fn *ast.FuncDecl, names *regexp.Regexp) bool {
	shape := *fn
	shape.Requires, shape.EnsureValues = nil, nil
	return !names.MatchString(retainedNormalized(retainedHeader(&shape)))
}
