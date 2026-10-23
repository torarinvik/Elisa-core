package semantic

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

// A retained update accepts adding or removing a wildcard `using M` line. The scratch
// declaration phase already resolved every signature, field and const under the new
// usings, and the pre-body fingerprint compares those results. What remains is body
// resolution: a bare name `x` gains or loses the candidate `M.x`, so every function in
// the line's scope that mentions a member name of M is re-analyzed, and every
// declaration in that scope takes its new usings. Member and alias forms register
// program-wide aliases and still fall back.

// retainedWildcardUsing: a `using M` line, the one form whose effect is its block's usings.
func retainedWildcardUsing(d ast.Decl) bool {
	u, ok := d.(*ast.UsingDecl)
	return ok && u.Member == "" && u.Alias == ""
}

// retainedDeclUsings computes each declaration's usings as flattenScopedDeclsWithVisibility
// does: a block's inherited usings plus its own wildcard `using` lines, wherever they stand
// in the block.
func (a *Analyzer) retainedDeclUsings(file *ast.File) map[ast.Decl][]string {
	out, _, _ := a.retainedLiveUsings(file, file)
	return out
}

// retainedLiveUsings computes the edited program's usings for every live declaration,
// synthesized ones included. The live and edited block trees are walked together: each live
// block takes its `using` lines from its edited counterpart, the next block of the same kind
// and name in the same parent (block positions are not kept current across updates, so they
// cannot pair them). It also returns each edited block's live counterpart, and reports the
// first live block with no counterpart.
func (a *Analyzer) retainedLiveUsings(live, edited *ast.File) (map[ast.Decl][]string, map[ast.Decl]ast.Decl, string) {
	out := map[ast.Decl][]string{}
	liveOf := map[ast.Decl]ast.Decl{}
	containerKey := func(d ast.Decl) (string, bool) {
		switch n := d.(type) {
		case *ast.NamespaceDecl:
			return "module " + n.Name, true
		case *ast.StaticIfDecl:
			return "static-if", true
		}
		return "", false
	}
	var walk func(liveDecls, editedDecls []ast.Decl, namespace string, inherited []string) string
	walk = func(liveDecls, editedDecls []ast.Decl, namespace string, inherited []string) string {
		var block []string
		counterparts := map[string][]ast.Decl{}
		for _, d := range editedDecls {
			if retainedWildcardUsing(d) {
				block = append(block, joinQualifiedName(namespace, d.(*ast.UsingDecl).Name))
			}
			if key, ok := containerKey(d); ok {
				counterparts[key] = append(counterparts[key], d)
			}
		}
		effective := dedupeQualifiedNames(append(append([]string(nil), inherited...), block...))
		for _, d := range liveDecls {
			key, isContainer := containerKey(d)
			if !isContainer {
				if _, skip := d.(*ast.UsingDecl); !skip {
					if _, skip := d.(*ast.ImportDecl); !skip {
						out[d] = effective
					}
				}
				continue
			}
			if len(counterparts[key]) == 0 {
				return fmt.Sprintf(" (%s at %s)", key, retainedLineKey(d.Pos()))
			}
			m := counterparts[key][0]
			counterparts[key] = counterparts[key][1:]
			liveOf[m] = d
			var why string
			switch n := d.(type) {
			case *ast.NamespaceDecl:
				why = walk(n.Decls, m.(*ast.NamespaceDecl).Decls, joinQualifiedName(namespace, n.Name), effective)
			case *ast.StaticIfDecl:
				why = walk(a.activeDeclBranch(n), a.activeDeclBranch(m.(*ast.StaticIfDecl)), namespace, effective)
			}
			if why != "" {
				return why
			}
		}
		return ""
	}
	why := walk(live.Decls, edited.Decls, "", nil)
	return out, liveOf, why
}

// retainedUsingMembers lists the bare names a `using` of each target can resolve: the
// undotted remainder of every declared name under the target.
func (a *Analyzer) retainedUsingMembers(targets map[string]bool, extra []string) map[string]bool {
	members := map[string]bool{}
	for _, name := range extra {
		members[name] = true
	}
	add := func(key string) {
		dot := strings.LastIndex(key, ".")
		if dot <= 0 || !targets[key[:dot]] {
			return
		}
		members[key[dot+1:]] = true
	}
	for key := range a.globalScope.Symbols {
		add(key)
	}
	for key := range a.namedTypes {
		add(key)
	}
	for key := range a.constValues {
		add(key)
	}
	for key := range a.permissions {
		add(key)
	}
	for key := range a.declaredModules {
		add(key)
	}
	return members
}

var retainedIdentToken = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// retainedSpanMentions reports whether a declaration's source text holds any of names as
// a token. Text, unlike the body walk, also sees type annotations; comments only make the
// check conservative.
func retainedSpanMentions(span string, names map[string]bool) bool {
	for _, tok := range retainedIdentToken.FindAllString(span, -1) {
		if names[tok] {
			return true
		}
	}
	return false
}

// retainedUsingChange is one live declaration whose usings an edit changes.
type retainedUsingChange struct {
	index   int // in r.decls
	usings  []string
	mention bool // a function whose text names a member of a changed target
}

// findUsingChanges returns the live declarations whose usings the edit changes, marking the
// functions whose text mentions an affected member. newOf pairs a live declaration with its
// edited form; spanOf gives its old and new source text. A synthesized function has no edited
// form and is read from its own formatted text.
func (r *RetainedAnalysis) findUsingChanges(file *ast.File, newOf func(ast.Decl) ast.Decl, spanOf func(ast.Decl) (string, string), extraMembers []string) ([]retainedUsingChange, string) {
	a := r.a
	newUsings, _, why := a.retainedLiveUsings(a.file, file)
	if why != "" {
		return nil, "a block holding a changed using has no edited counterpart" + why
	}
	var changes []retainedUsingChange
	targets := map[string]bool{}
	for k, sd := range r.decls {
		usings, ok := newUsings[sd.Decl]
		if !ok {
			return nil, fmt.Sprintf("usings of %T at %s not found", sd.Decl, retainedLineKey(sd.Decl.Pos()))
		}
		if slices.Equal(usings, sd.Usings) {
			continue
		}
		for _, u := range usings {
			if !slices.Contains(sd.Usings, u) {
				targets[u] = true
			}
		}
		for _, u := range sd.Usings {
			if !slices.Contains(usings, u) {
				targets[u] = true
			}
		}
		changes = append(changes, retainedUsingChange{index: k, usings: usings})
	}
	if len(changes) == 0 {
		return nil, ""
	}
	members := a.retainedUsingMembers(targets, extraMembers)
	for k := range changes {
		fn, isFn := r.decls[changes[k].index].Decl.(*ast.FuncDecl)
		if !isFn {
			continue
		}
		oldSpan, newSpan := spanOf(fn)
		if newOf(fn) == nil {
			oldSpan = unparse.FormatDecl(fn)
		}
		changes[k].mention = retainedSpanMentions(oldSpan, members) || retainedSpanMentions(newSpan, members)
	}
	return changes, ""
}

// applyUsingChanges installs the new usings in the live analysis.
func (r *RetainedAnalysis) applyUsingChanges(changes []retainedUsingChange, scoped map[*ast.FuncDecl]scopedDecl) {
	a := r.a
	for _, c := range changes {
		r.decls[c.index].Usings = c.usings
		if fn, ok := r.decls[c.index].Decl.(*ast.FuncDecl); ok {
			a.funcDeclUsings[fn] = append([]string(nil), c.usings...)
			if sd, known := scoped[fn]; known {
				sd.Usings = c.usings
				scoped[fn] = sd
			}
		}
	}
}
