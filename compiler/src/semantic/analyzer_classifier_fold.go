package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Classified-dispatch table folding (docs/125 §9.6 / roadmap C3).
//
// A "char classifier" is a pure, total function `char -> ClosedConstEnum` whose body is a
// single `match`/`when` over the parameter with literal / range / alternation patterns and
// a wildcard default, each arm yielding a member of the return enum. This is exactly what
// production lexers hand-write as a 256-byte lookup table. `when c:` lowers to a comparison
// LADDER (verified: 13 `icmp` for the number classifier, zero `switch`), which LLVM does not
// turn into a table — so a hot classifier pays a branch chain per character.
//
// This pass folds a recognized classifier into that hand-written table FOR FREE: it
// evaluates the classifier at compile time for every byte 0..255 (char is byte-sized in this
// dialect), synthesizes a file-scope `const __classtable_<fn>: Enum[256] = [...]` (which
// lowers to an `internal constant` array — static rodata), and rewrites the function body to
// a single indexed load `return __classtable_<fn>[c]`. The transform is behavior-preserving
// by construction (the table is the classifier's own truth table over its whole domain) and
// unconditional: a non-recognized function is left untouched.
//
// It runs before analysis so the synthesized const decl and rewritten body are analyzed and
// lowered by the normal paths — no backend changes.

// foldCharClassifierTables rewrites every foldable char classifier in the file to a static
// table lookup, appending the backing const decls. Returns the names of folded functions
// (for diagnostics / tests). Safe to call on any file; a no-op when nothing qualifies.
func foldCharClassifierTables(file *ast.File) []string {
	if file == nil {
		return nil
	}
	return foldDeclScope(&file.Decls, nil)
}

// foldDeclScope folds classifiers declared directly in one decl scope (a file body or a
// `module`/`extend`/`private:` block, which all lower to NamespaceDecl.Decls), placing each
// synthesized table in that same scope so the enum reference and table name resolve
// unqualified. Const enums from enclosing scopes are inherited so a classifier can return an
// enum declared one level out. Recurses into nested namespaces.
func foldDeclScope(decls *[]ast.Decl, inherited map[string]map[string]bool) []string {
	// Enums visible in this scope: inherited plus those declared here.
	enums := map[string]map[string]bool{}
	for k, v := range inherited {
		enums[k] = v
	}
	for _, d := range *decls {
		if ce, ok := d.(*ast.ConstEnumDecl); ok {
			set := make(map[string]bool, len(ce.Members))
			for _, m := range ce.Members {
				set[m.Name] = true
			}
			enums[ce.Name] = set
		}
	}

	var folded []string
	var newDecls []ast.Decl
	for _, d := range *decls {
		switch n := d.(type) {
		case *ast.FuncDecl:
			enumName, table, ok := foldableClassifierTable(n, enums)
			if !ok {
				continue
			}
			tableName := "__classtable_" + n.Name
			pos := n.Position
			// const __classtable_fn: Enum[256] = [Enum.m0, Enum.m1, ...]
			elems := make([]ast.Expr, 256)
			for i, member := range table {
				elems[i] = enumMemberExpr(pos, enumName, member)
			}
			newDecls = append(newDecls, &ast.ConstDecl{
				Position: pos,
				Name:     tableName,
				Type: &ast.ArrayType{
					Position: pos,
					Elem:     &ast.NamedType{Position: pos, Name: enumName},
					Size:     &ast.IntLit{Position: pos, Value: "256"},
				},
				Value: &ast.ListLitExpr{Position: pos, Elems: elems},
			})
			// return __classtable_fn[c]
			param := n.Params[0].Name
			n.Body = []ast.Stmt{&ast.ReturnStmt{Position: pos, Value: &ast.IndexExpr{
				Position: pos,
				Object:   &ast.Ident{Position: pos, Name: tableName},
				Index:    &ast.Ident{Position: pos, Name: param},
			}}}
			folded = append(folded, n.Name)
		case *ast.NamespaceDecl:
			folded = append(folded, foldDeclScope(&n.Decls, enums)...)
		}
	}
	if len(newDecls) > 0 {
		*decls = append(*decls, newDecls...)
	}
	return folded
}

// enumMemberExpr builds `Enum.Member`.
func enumMemberExpr(pos lexer.Pos, enum, member string) ast.Expr {
	return &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: enum}, Field: member}
}

// foldableClassifierTable recognizes a foldable char classifier and, on success, returns the
// return-enum name and the 256-entry truth table (member name per byte). It bails (ok=false)
// on any deviation from the exact recognized shape — the fold is opt-out-by-mismatch, never a
// guess.
func foldableClassifierTable(fn *ast.FuncDecl, enumMembers map[string]map[string]bool) (string, [256]string, bool) {
	var empty [256]string
	// Signature: exactly one non-mutable `char` param, returning a named closed const enum.
	if len(fn.Params) != 1 || fn.Params[0].Mutable || !isCharType(fn.Params[0].Type) {
		return "", empty, false
	}
	if len(fn.TypeParams) != 0 || len(fn.GenericParams) != 0 {
		return "", empty, false
	}
	enumName, ok := namedTypeName(fn.ReturnType)
	if !ok {
		return "", empty, false
	}
	members, ok := enumMembers[enumName]
	if !ok {
		return "", empty, false
	}
	// Body must be exactly `return <MatchExpr over the param>`.
	if len(fn.Body) != 1 {
		return "", empty, false
	}
	ret, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok {
		return "", empty, false
	}
	me, ok := ret.Value.(*ast.MatchExpr)
	if !ok {
		return "", empty, false
	}
	scrut, ok := me.Value.(*ast.Ident)
	if !ok || scrut.Name != fn.Params[0].Name {
		return "", empty, false
	}

	// Evaluate arms in order; a byte's class is the first arm whose pattern matches.
	var table [256]string
	var filled [256]bool
	for _, arm := range me.Arms {
		if arm.Guard != nil {
			return "", empty, false // a guard is not statically evaluable — bail
		}
		member, ok := enumMemberResult(arm, enumName, members)
		if !ok {
			return "", empty, false
		}
		wildcard, ok := classifyArmBytes(arm.Pattern)
		if !ok {
			return "", empty, false
		}
		if wildcard {
			for c := 0; c < 256; c++ {
				if !filled[c] {
					table[c] = member
					filled[c] = true
				}
			}
			continue
		}
		for c := 0; c < 256; c++ {
			if patternMatchesByte(arm.Pattern, byte(c)) && !filled[c] {
				table[c] = member
				filled[c] = true
			}
		}
	}
	// Totality: every byte must be classified (the classifier is a total partition of char).
	for c := 0; c < 256; c++ {
		if !filled[c] {
			return "", empty, false
		}
	}
	return enumName, table, true
}

// enumMemberResult extracts the enum member an arm yields, requiring the arm body to be a
// single `Enum.Member` expression naming a member of `enumName`. (Leading-dot shorthand
// `.Member` is left unfolded — a rare classifier form; it simply keeps its branch chain.)
func enumMemberResult(arm ast.MatchArm, enumName string, members map[string]bool) (string, bool) {
	if len(arm.Body) != 1 {
		return "", false
	}
	es, ok := arm.Body[0].(*ast.ExprStmt)
	if !ok {
		return "", false
	}
	if v, ok := es.Expr.(*ast.FieldExpr); ok {
		if obj, ok := v.Object.(*ast.Ident); ok && obj.Name == enumName && members[v.Field] {
			return v.Field, true
		}
	}
	return "", false
}

// classifyArmBytes reports whether an arm's pattern is the wildcard catch-all. The bool is
// true for a wildcard; ok=false means the pattern is not a fold-supported form (literal /
// range / alternation of those / wildcard).
func classifyArmBytes(pat ast.MatchPattern) (wildcard bool, ok bool) {
	switch p := pat.(type) {
	case *ast.MatchWildcardPattern:
		return true, true
	case *ast.MatchLiteralPattern:
		_, isChar := charLitByte(p.Value)
		return false, isChar
	case *ast.MatchRangePattern:
		_, loOK := charLitByte(p.Lo)
		_, hiOK := charLitByte(p.Hi)
		return false, loOK && hiOK
	case *ast.MatchOrPattern:
		for _, opt := range p.Options {
			w, ok := classifyArmBytes(opt)
			if !ok || w {
				return false, false // a wildcard inside an alternation is not expected
			}
		}
		return false, true
	}
	return false, false
}

// patternMatchesByte evaluates a fold-supported pattern against a concrete byte.
func patternMatchesByte(pat ast.MatchPattern, c byte) bool {
	switch p := pat.(type) {
	case *ast.MatchWildcardPattern:
		return true
	case *ast.MatchLiteralPattern:
		if v, ok := charLitByte(p.Value); ok {
			return v == c
		}
	case *ast.MatchRangePattern:
		lo, loOK := charLitByte(p.Lo)
		hi, hiOK := charLitByte(p.Hi)
		if loOK && hiOK {
			if p.Inclusive {
				return c >= lo && c <= hi
			}
			return c >= lo && c < hi
		}
	case *ast.MatchOrPattern:
		for _, opt := range p.Options {
			if patternMatchesByte(opt, c) {
				return true
			}
		}
	}
	return false
}

// charLitByte returns the byte value of a char-literal expression.
func charLitByte(e ast.Expr) (byte, bool) {
	cl, ok := e.(*ast.CharLit)
	if !ok {
		return 0, false
	}
	v, ok := ParseCharLiteral(cl)
	if !ok || v < 0 || v > 255 {
		return 0, false
	}
	return byte(v), true
}

// isCharType reports whether a type expression names the builtin `char`.
func isCharType(t ast.TypeExpr) bool {
	name, ok := namedTypeName(t)
	return ok && name == "char"
}

// namedTypeName returns the bare type name for a NamedType / BuiltinTypeExpr with no args.
func namedTypeName(t ast.TypeExpr) (string, bool) {
	switch n := t.(type) {
	case *ast.NamedType:
		return n.Name, true
	case *ast.BuiltinTypeExpr:
		if len(n.TypeArgs) == 0 && len(n.ValueArgs) == 0 {
			return n.Name, true
		}
	}
	return "", false
}
