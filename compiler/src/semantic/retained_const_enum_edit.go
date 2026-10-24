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

// Implicit member values and closed literal expressions need no other declaration.
// A changed storage spelling
// is also eligible when both sides explicitly name primitive integers: member
// population resolves the new storage, and the same type/layout dependency walk
// used for member edits rechecks its users. Aliases and computed storage changes
// still need their declaration-phase dependencies rebuilt separately.
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
	if header(oldSpan) != header(curSpan) && !(retainedPrimitiveEnumStorage(old.Storage) && retainedPrimitiveEnumStorage(cur.Storage)) {
		return false
	}
	for _, d := range []*ast.ConstEnumDecl{old, cur} {
		for _, m := range d.Members {
			if m.Value != nil && !retainedClosedConstEnumValue(m.Value) {
				return false
			}
		}
	}
	return true
}

// This recognizes dependencies, not values: the ordinary scratch declaration
// phase still evaluates the complete expression and validates storage/bounds.
// Calls, names, reflected layouts and cast type bindings need a rebuilt
// declaration dependency graph and keep the conservative fallback.
func retainedClosedConstEnumValue(expr ast.Expr) bool {
	stack := []ast.Expr{expr}
	for len(stack) != 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		switch value := current.(type) {
		case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.CharLit, *ast.StringLit, *ast.NullLit:
		case *ast.ParenExpr:
			stack = append(stack, value.Inner)
		case *ast.UnaryExpr:
			if value.LoweredCall != nil {
				return false
			}
			stack = append(stack, value.Operand)
		case *ast.BinaryExpr:
			if value.LoweredCall != nil {
				return false
			}
			stack = append(stack, value.Left, value.Right)
		case *ast.TernaryExpr:
			stack = append(stack, value.Value, value.Cond, value.Alt)
		default:
			return false
		}
	}
	return true
}

func retainedPrimitiveEnumStorage(storage ast.TypeExpr) bool {
	named, ok := storage.(*ast.NamedType)
	return ok && named.Region == "" && (named.Spelling == "" || named.Spelling == named.Name) && IsIntegralStorageType(&BuiltinType{Name: named.Name})
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
			h := retainedNormalized(retainedHeader(fn))
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
			// A plain type reference keeps the live enum identity. Its containing
			// layouts are widened into the invalidation set by the update. Any
			// initializer, value-sized array or other declaration computation still
			// needs a fresh declaration phase instead of this in-place update.
			if !retainedConstEnumTypeOnlyDeclaration(d) {
				return nil, fmt.Sprintf("declaration at %s depends on an edited constant enum", retainedLineKey(d.Pos()))
			}
		}
	}
	return out, ""
}

func retainedConstEnumTypeOnlyDeclaration(decl ast.Decl) bool {
	switch d := decl.(type) {
	case *ast.TypeAliasDecl:
		return len(d.TypeParams) == 0 && retainedConstEnumPassiveType(d.Target)
	case *ast.StructDecl:
		if d.Layout != ast.StructLayoutDefault && d.Layout != ast.StructLayoutC {
			return false
		}
		shape := *d
		shape.ReprC, shape.Layout = false, ast.StructLayoutDefault
		if !retainedPlainStruct(&shape) {
			return false
		}
		for _, field := range d.Fields {
			if field.DefaultValue != nil || !retainedConstEnumPassiveType(field.Type) {
				return false
			}
		}
		return true
	case *ast.EnumDecl:
		if !retainedPlainEnum(d) {
			return false
		}
		for _, variant := range d.Variants {
			for _, payload := range variant.Payload {
				if !retainedConstEnumPassiveType(payload.Type) {
					return false
				}
			}
		}
		return true
	}
	return false
}

func retainedConstEnumPassiveType(typ ast.TypeExpr) bool {
	switch t := typ.(type) {
	case *ast.NamedType:
		return true
	case *ast.RefType:
		return retainedConstEnumPassiveType(t.Elem)
	case *ast.ArrayType:
		_, literal := t.Size.(*ast.IntLit)
		return literal && retainedConstEnumPassiveType(t.Elem)
	}
	return false
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
