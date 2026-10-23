package semantic

import (
	"fmt"
	"regexp"
	"strings"

	"elisacore/src/ast"
)

type retainedConstEnumEdit struct {
	live, edit *ast.ConstEnumDecl
	scope      scopedDecl
	et         *ConstEnumType
}

// An explicit, unchanged storage type preserves the ABI. Implicit member values
// depend only on their order, so rebuilding the table needs no retained constant
// evaluation results from other declarations.
func retainedConstEnumEditAllowed(old, cur *ast.ConstEnumDecl, oldSpan, curSpan string) bool {
	if old.Name != cur.Name || old.Foreign || cur.Foreign || old.Open || cur.Open || old.Storage == nil || cur.Storage == nil {
		return false
	}
	header := func(s string) string {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			return strings.TrimSpace(s[:i])
		}
		return strings.TrimSpace(s)
	}
	if header(oldSpan) != header(curSpan) {
		return false
	}
	for _, d := range []*ast.ConstEnumDecl{old, cur} {
		for _, m := range d.Members {
			if m.Value != nil {
				return false
			}
		}
	}
	return true
}

func (r *RetainedAnalysis) findLiveConstEnumEdits(pairs [][2]*ast.ConstEnumDecl, scratch *ast.File, decls []ast.Decl, spans []string) ([]retainedConstEnumEdit, string) {
	live := map[string]scopedDecl{}
	for _, sd := range r.decls {
		if d, ok := sd.Decl.(*ast.ConstEnumDecl); ok {
			live[retainedLineKey(d.Pos())+"|"+d.Name] = sd
		}
	}
	prepared := map[string]*ast.ConstEnumDecl{}
	for _, d := range retainedWalkDecls(scratch) {
		if e, ok := d.(*ast.ConstEnumDecl); ok {
			prepared[retainedLineKey(e.Pos())+"|"+e.Name] = e
		}
	}
	var out []retainedConstEnumEdit
	edited := map[ast.Decl]bool{}
	names, members := map[string]bool{}, map[string]bool{}
	targets := map[Type]bool{}
	for _, pair := range pairs {
		sd, ok := live[retainedLineKey(pair[0].Pos())+"|"+pair[0].Name]
		e := prepared[retainedLineKey(pair[1].Pos())+"|"+pair[1].Name]
		if !ok || e == nil {
			return nil, "edited constant enum not found in the analysis"
		}
		d := sd.Decl.(*ast.ConstEnumDecl)
		et, _ := r.a.namedTypes[joinQualifiedName(sd.Namespace, d.Name)].(*ConstEnumType)
		if et == nil {
			return nil, "edited constant enum has no analyzed type"
		}
		out = append(out, retainedConstEnumEdit{d, e, sd, et})
		targets[et], edited[pair[1]] = true, true
		names[d.Name] = true
		for _, p := range pair {
			for _, m := range p.Members {
				members[m.Name] = true
			}
		}
	}
	// Include aliases: a header or another declaration may name the same values
	// through an alias, including an unqualified member imported by a using.
	for name, t := range r.a.namedTypes {
		if targets[t] {
			names[name[strings.LastIndex(name, ".")+1:]] = true
		}
	}
	pat, memberPat := retainedNamesPattern(names), retainedNamesPattern(members)
	// Bare members require a using of the enum, rejected below. Shorthand
	// members can inherit their type from a surrounding declaration instead.
	var quoted []string
	for n := range members {
		quoted = append(quoted, regexp.QuoteMeta(n))
	}
	shorthand := regexp.MustCompile(`(^|[^A-Za-z0-9_.])\.[ \t\r\n]*(` + strings.Join(quoted, "|") + `)([^A-Za-z0-9_]|$)`)
	codeMatch := func(pattern *regexp.Regexp, text string) bool {
		for _, match := range pattern.FindAllStringSubmatchIndex(text, -1) {
			if !retainedInCommentOrString(text, match[4]) {
				return true
			}
		}
		return false
	}
	for i, d := range decls {
		if edited[d] {
			continue
		}
		if fn, ok := d.(*ast.FuncDecl); ok {
			if (fn.Static || fn.IsLaw) && (codeMatch(pat, spans[i]) || codeMatch(shorthand, spans[i])) {
				return nil, "static function depends on an edited constant enum"
			}
			h := retainedText(fn).header
			if codeMatch(memberPat, h) {
				return nil, "function header names an edited constant enum member"
			}
			// A type name by itself in a signature is safe. Member access with whitespace
			// is already caught by the member-name test above, including missing members.
			for n := range names {
				for _, token := range retainedIdentTokens(h) {
					if h[token[0]:token[1]] == n && strings.HasPrefix(strings.TrimSpace(h[token[1]:]), ".") {
						return nil, "function header reads an edited constant enum"
					}
				}
			}
		} else if _, ns := d.(*ast.NamespaceDecl); !ns && (codeMatch(pat, spans[i]) || codeMatch(shorthand, spans[i])) {
			return nil, fmt.Sprintf("declaration at %s depends on an edited constant enum", retainedLineKey(d.Pos()))
		}
	}
	return out, ""
}

func (r *RetainedAnalysis) applyConstEnumEdits(edits []retainedConstEnumEdit) {
	if len(edits) == 0 {
		return
	}
	replace := map[ast.Decl]ast.Decl{}
	for _, e := range edits {
		replace[e.live] = e.edit
	}
	r.replaceLiveDecls(replace)
	for _, e := range edits {
		for _, m := range e.et.Members {
			delete(r.a.constValues, e.et.Name+"."+m.Name)
		}
		e.et.Decl = e.edit
		e.et.Members, e.et.MemberMap = nil, map[string]*ConstEnumMember{}
		sd := e.scope
		sd.Decl = e.edit
		r.a.collectConstValues([]scopedDecl{sd})
		r.a.populateConstEnumMembers([]scopedDecl{sd})
	}
	r.a.clearRetainedTypeMemos()
}

// A missing shorthand member has no old enum expression type. It still needs
// rechecking when a new member can make it valid; unrelated qualified fields
// with the same spelling do not acquire that meaning.
func retainedConstEnumShorthand(fn *ast.FuncDecl, members map[string]bool) bool {
	if fn == nil {
		return false
	}
	hit := false
	walkStoreScanNodes(funcScanStmts(fn), func(node any) {
		if expr, ok := node.(*ast.ShorthandMemberExpr); ok {
			for _, part := range expr.Parts {
				hit = hit || members[part]
			}
		}
	})
	return hit
}
