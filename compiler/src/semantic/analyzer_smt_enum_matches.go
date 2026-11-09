package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"sort"
	"strings"
)

// enumMatchTerm lowers a match EXPRESSION over an enum value into a nested ite. `lower` translates
// each arm's tail (Int or Bool lane). The last arm is the default only when it is a wildcard or the
// arms cover every variant.
func (tr *smtTranslator) enumMatchTerm(n *ast.MatchExpr, env map[string]string, lower func(ast.Expr, map[string]string) (string, bool)) (string, bool) {
	if n == nil || n.Store != nil || len(n.Arms) == 0 {
		return "", false
	}
	v, ok := tr.enumValue(n.Value, env)
	if !ok {
		return "", false
	}
	type armTerm struct{ guard, body string }
	arms := make([]armTerm, 0, len(n.Arms))
	covered := map[*EnumVariant]bool{}
	hasDefault := false
	for i, arm := range n.Arms {
		if arm.Guard != nil || len(arm.Body) != 1 {
			return "", false
		}
		tail, ok := arm.Body[0].(*ast.ExprStmt)
		if !ok || tail == nil {
			return "", false
		}
		if _, wild := arm.Pattern.(*ast.MatchWildcardPattern); wild {
			if i != len(n.Arms)-1 {
				return "", false
			}
			body, ok := lower(tail.Expr, env)
			if !ok {
				return "", false
			}
			arms = append(arms, armTerm{guard: "true", body: body})
			hasDefault = true
			continue
		}
		guard, variant, ok := tr.enumPatternGuard(v, arm.Pattern)
		if !ok {
			return "", false
		}
		armEnv := env
		if vp, isVariant := arm.Pattern.(*ast.MatchVariantPattern); isVariant {
			armEnv, ok = tr.bindEnumPatternBinders(v, variant, vp, env)
			if !ok {
				return "", false
			}
			covered[variant] = true
		} else if or, isOr := arm.Pattern.(*ast.MatchOrPattern); isOr {
			for _, option := range or.Options {
				if vp, ok := option.(*ast.MatchVariantPattern); ok {
					if variant, ok := tr.enumPatternVariant(v.et, vp); ok {
						covered[variant] = true
					}
				}
			}
		}
		body, ok := lower(tail.Expr, armEnv)
		if !ok {
			return "", false
		}
		arms = append(arms, armTerm{guard: guard, body: body})
	}
	if !hasDefault {
		for _, variant := range v.et.Variants {
			if !covered[variant] {
				return "", false
			}
		}
	}
	// Fold from the last arm: under exhaustiveness the last arm's guard is implied by the negation of
	// every earlier guard together with the tag-range constraint.
	result := arms[len(arms)-1].body
	for i := len(arms) - 2; i >= 0; i-- {
		result = "(ite " + arms[i].guard + " " + arms[i].body + " " + result + ")"
	}
	return result, true
}

// enumEqualityTerm lowers `a == b` over enum values. Tag equality is value equality only when at
// least one side is a payload-free constructor, or the enum carries no payload at all.
func (tr *smtTranslator) enumEqualityTerm(n *ast.BinaryExpr, env map[string]string) (string, bool) {
	l, ok := tr.enumValue(n.Left, env)
	if !ok {
		return "", false
	}
	r, ok := tr.enumValue(n.Right, env)
	if !ok || l.et != r.et {
		return "", false
	}
	payloadFree := func(v smtEnumValue) bool { return v.ctor != nil && len(v.ctor.Payload) == 0 }
	if !payloadFree(l) && !payloadFree(r) && !enumHasNoPayload(l.et) {
		return "", false
	}
	lt, rt := tr.enumTagTerm(l), tr.enumTagTerm(r)
	eq := "(= " + lt + " " + rt + ")"
	if lt == rt {
		eq = "true"
	}
	if n.Op == lexer.TOKEN_BANGEQ {
		if eq == "true" {
			return "false", true
		}
		return "(not " + eq + ")", true
	}
	return eq, true
}

func enumHasNoPayload(et *EnumType) bool {
	for _, v := range et.Variants {
		if len(v.Payload) != 0 {
			return false
		}
	}
	return true
}

// enumIsFactTerm lowers a top-level `value is Variant(binders)` PATH FACT: the tag test plus, for each
// binder still resolving to the very symbol the pattern introduced (`pinned`), the equality between
// that binder and the payload it denotes.
func (tr *smtTranslator) enumIsFactTerm(n *ast.BinaryExpr, pinned map[string]*Symbol) (string, bool) {
	tr.enumPinnedBinders = pinned
	defer func() { tr.enumPinnedBinders = nil }()
	return tr.enumIsTerm(n, nil, true)
}

// enumIsTerm lowers `value is Variant(binders)`. With factMode (a top-level path fact) it also
// equates each pinned in-scope binder with the payload it denotes; otherwise binders must be absent
// or `_`.
func (tr *smtTranslator) enumIsTerm(n *ast.BinaryExpr, env map[string]string, factMode bool) (string, bool) {
	test, ok := n.Right.(*ast.VariantTestExpr)
	if !ok || test == nil || test.Pattern == nil {
		return "", false
	}
	v, ok := tr.enumValue(n.Left, env)
	if !ok {
		return "", false
	}
	variant, ok := tr.enumPatternVariant(v.et, test.Pattern)
	if !ok {
		return "", false
	}
	guard := tr.enumVariantGuard(v, variant)
	binders := matchVariantPatternBinders(test.Pattern)
	if len(binders) == 0 {
		return guard, true
	}
	if !factMode {
		return "", false
	}
	bound, ok := tr.bindEnumPatternBinders(v, variant, test.Pattern, map[string]string{})
	if !ok {
		return guard, true
	}
	conj := []string{guard}
	names := make([]string, 0, len(bound))
	for name := range bound {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		payload := bound[name]
		if strings.HasPrefix(payload, dtTokenPrefix) {
			continue // a struct/enum binder: its scalar fields carry the equalities
		}
		local, ok := tr.enumBinderLocalTerm(name)
		if !ok {
			continue // sound: a missing equality only forgoes a fact
		}
		conj = append(conj, "(= "+local+" "+payload+")")
	}
	if len(conj) == 1 {
		return guard, true
	}
	return "(and " + strings.Join(conj, " ") + ")", true
}

// enumBinderLocalTerm translates an in-scope binder (or one of its struct-field projections, named
// `b.f`) exactly as the surrounding code will reference it.
func (tr *smtTranslator) enumBinderLocalTerm(name string) (string, bool) {
	parts := strings.Split(name, smtFieldSeparator)
	var expr ast.Expr = &ast.Ident{Name: parts[0]}
	if tr.a.currentScope == nil {
		return "", false
	}
	sym, found := tr.a.currentScope.Lookup(parts[0])
	if !found || sym == nil || sym.Mutable || sym.Kind != SymbolLocal {
		return "", false
	}
	if pinned, ok := tr.enumPinnedBinders[parts[0]]; !ok || pinned != sym {
		return "", false // shadowed (or unpinned): the name no longer denotes the pattern binder
	}
	t := stripRefForBounds(sym.Type)
	for _, field := range parts[1:] {
		st, ok := t.(*StructType)
		if !ok || st == nil {
			return "", false
		}
		f, ok := st.Fields[field]
		if !ok {
			return "", false
		}
		expr = &ast.FieldExpr{Object: expr, Field: field}
		t = stripRefForBounds(f.Type)
	}
	if IsBoolType(t) {
		return tr.boolTerm(expr, nil)
	}
	return tr.termEnv(expr, nil)
}

func matchVariantPatternBinders(p *ast.MatchVariantPattern) []string {
	var out []string
	for _, arg := range p.Args {
		if b, ok := arg.Pattern.(*ast.MatchBindPattern); ok && b.Name != "" && b.Name != "_" {
			out = append(out, b.Name)
		} else if _, ok := arg.Pattern.(*ast.MatchWildcardPattern); !ok && arg.Pattern != nil {
			out = append(out, "<sub-pattern>")
		}
	}
	return out
}

// enumPreamble emits enum tag/payload declarations deterministically.
func (tr *smtTranslator) enumPreamble() string {
	if len(tr.enumDecls) == 0 {
		return ""
	}
	syms := make([]string, 0, len(tr.enumDecls))
	for sym := range tr.enumDecls {
		syms = append(syms, sym)
	}
	sort.Strings(syms)
	var b strings.Builder
	for _, sym := range syms {
		b.WriteString(tr.enumDecls[sym])
	}
	return b.String()
}

// enumPatternArgs returns a variant pattern's payload sub-patterns in declaration order, using the
// order the analyzer already resolved; it never re-resolves (which could report diagnostics).
func enumPatternArgs(p *ast.MatchVariantPattern, variant *EnumVariant) []*ast.MatchPatternArg {
	if len(p.Args) == 0 {
		return nil
	}
	if len(p.ResolvedArgs) == len(variant.Payload) {
		return p.ResolvedArgs
	}
	if len(p.Args) != len(variant.Payload) {
		return nil
	}
	out := make([]*ast.MatchPatternArg, len(p.Args))
	for i := range p.Args {
		if p.Args[i].Name != "" {
			return nil
		}
		out[i] = &p.Args[i]
	}
	return out
}

// recordEnumMatchArmFacts records the path facts a statement-match arm establishes over a stable
// enum subject: the arm's own variant test (binders equated with their payloads when rendered), and
// the negation of every earlier unguarded variant arm. Facts depend on the subject's root and the
// arm's binders, so a mutation or rebinding of either invalidates them.
func (a *Analyzer) recordEnumMatchArmFacts(subject ast.Expr, arm ast.MatchArm, prior []ast.MatchPattern, scope *Scope) {
	if a == nil || scope == nil || !enumFactSubject(subject) {
		return
	}
	deps := smtFactDeps(subject)
	if deps == nil {
		deps = map[string]bool{}
	}
	if vp, ok := arm.Pattern.(*ast.MatchVariantPattern); ok && vp != nil && vp.As == "" {
		armDeps := make(map[string]bool, len(deps)+len(vp.Args))
		for k := range deps {
			armDeps[k] = true
		}
		for _, name := range matchVariantPatternBinders(vp) {
			armDeps[name] = true
		}
		fact := &ast.BinaryExpr{Position: vp.Position, Op: lexer.TOKEN_IS, Left: subject, Right: &ast.VariantTestExpr{Position: vp.Position, Pattern: vp}}
		binders := map[string]*Symbol{}
		for _, name := range matchVariantPatternBinders(vp) {
			if sym, ok := scope.Symbols[name]; ok && sym != nil {
				binders[name] = sym
			}
		}
		scope.smtAssertFacts = append(scope.smtAssertFacts, smtFact{Expr: fact, Deps: armDeps, Binders: binders, Pins: a.smtFactPins(scope, fact)})
	}
	for _, p := range prior {
		for _, vp := range enumPatternVariants(p) {
			tagOnly := &ast.MatchVariantPattern{Position: vp.Position, EnumName: vp.EnumName, Variant: vp.Variant}
			test := &ast.BinaryExpr{Position: vp.Position, Op: lexer.TOKEN_IS, Left: subject, Right: &ast.VariantTestExpr{Position: vp.Position, Pattern: tagOnly}}
			fact := &ast.UnaryExpr{Position: vp.Position, Op: lexer.TOKEN_NOT, Operand: test}
			scope.smtAssertFacts = append(scope.smtAssertFacts, smtFact{Expr: fact, Deps: deps, Pins: a.smtFactPins(scope, fact)})
		}
	}
}

// enumPatternVariants lists the variant patterns an unguarded arm pattern fully matches by tag alone:
// a variant whose payload sub-patterns are all binders or wildcards, or an alternation of those.
func enumPatternVariants(p ast.MatchPattern) []*ast.MatchVariantPattern {
	switch pp := p.(type) {
	case *ast.MatchVariantPattern:
		if pp == nil {
			return nil
		}
		for _, name := range matchVariantPatternBinders(pp) {
			if name == "<sub-pattern>" {
				return nil // a refuting sub-pattern: failing it does not exclude the variant
			}
		}
		return []*ast.MatchVariantPattern{pp}
	case *ast.MatchOrPattern:
		var out []*ast.MatchVariantPattern
		for _, option := range pp.Options {
			vs := enumPatternVariants(option)
			if vs == nil {
				return nil
			}
			out = append(out, vs...)
		}
		return out
	}
	return nil
}

// enumFactSubject reports whether a match subject is a stable place (an identifier or a field
// projection rooted at one) whose value the facts can name across the arm.
func enumFactSubject(expr ast.Expr) bool {
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.Ident:
		return n != nil
	case *ast.FieldExpr:
		return n != nil && enumFactSubject(n.Object)
	}
	return false
}

// recordEnumMatchFallthroughFact records, after an exhaustive statement match, that control only
// reaches the following code through an arm that falls through: the subject is one of those arms'
// variants. Only for an IMMUTABLE subject root (no arm can have changed the value) and only when every
// fall-through arm is an unguarded variant/alternation pattern (a wildcard arm makes it vacuous).
func (a *Analyzer) recordEnumMatchFallthroughFact(subject ast.Expr, fallArms []ast.MatchPattern) {
	if a == nil || a.currentScope == nil || !enumFactSubject(subject) || len(fallArms) == 0 {
		return
	}
	root := stripOptimizationParens(subject)
	for {
		if f, ok := root.(*ast.FieldExpr); ok {
			root = stripOptimizationParens(f.Object)
			continue
		}
		break
	}
	id, ok := root.(*ast.Ident)
	if !ok || id == nil {
		return
	}
	sym, found := a.currentScope.Lookup(id.Name)
	if !found || sym == nil || sym.Mutable || symbolRootMutable(sym) {
		return
	}
	var disj ast.Expr
	for _, p := range fallArms {
		vps := enumPatternVariants(p)
		if vps == nil {
			return
		}
		for _, vp := range vps {
			tagOnly := &ast.MatchVariantPattern{Position: vp.Position, EnumName: vp.EnumName, Variant: vp.Variant}
			test := &ast.BinaryExpr{Position: vp.Position, Op: lexer.TOKEN_IS, Left: subject, Right: &ast.VariantTestExpr{Position: vp.Position, Pattern: tagOnly}}
			if disj == nil {
				disj = test
			} else {
				disj = &ast.BinaryExpr{Position: vp.Position, Op: lexer.TOKEN_OR, Left: disj, Right: test}
			}
		}
	}
	if disj != nil {
		a.currentScope.smtAssertFacts = append(a.currentScope.smtAssertFacts, smtFact{Expr: disj, Deps: smtFactDeps(subject), Pins: a.smtFactPins(a.currentScope, disj)})
	}
}

// constEnumMatchTerm lowers a match EXPRESSION over a const-enum value whose arms are member
// patterns (possibly module-qualified, possibly alternations) to a nested ite over the member values.
// Without a wildcard the last arm is the default only when the arms cover every member.
func (tr *smtTranslator) constEnumMatchTerm(n *ast.MatchExpr, env map[string]string, lower func(ast.Expr, map[string]string) (string, bool)) (string, bool) {
	if n == nil || n.Store != nil || len(n.Arms) == 0 {
		return "", false
	}
	ce, ok := stripRefForBounds(tr.a.exprType(n.Value)).(*ConstEnumType)
	if !ok || ce == nil {
		return "", false
	}
	subject, ok := tr.termEnv(n.Value, env)
	if !ok {
		return "", false
	}
	type armTerm struct{ guard, body string }
	arms := make([]armTerm, 0, len(n.Arms))
	covered := map[*ConstEnumMember]bool{}
	hasDefault := false
	for i, arm := range n.Arms {
		if arm.Guard != nil || len(arm.Body) != 1 {
			return "", false
		}
		tail, ok := arm.Body[0].(*ast.ExprStmt)
		if !ok || tail == nil {
			return "", false
		}
		body, ok := lower(tail.Expr, env)
		if !ok {
			return "", false
		}
		if _, wild := arm.Pattern.(*ast.MatchWildcardPattern); wild {
			if i != len(n.Arms)-1 {
				return "", false
			}
			arms = append(arms, armTerm{guard: "true", body: body})
			hasDefault = true
			continue
		}
		options := []ast.MatchPattern{arm.Pattern}
		if or, isOr := arm.Pattern.(*ast.MatchOrPattern); isOr {
			options = or.Options
		}
		var guards []string
		for _, option := range options {
			vp, ok := option.(*ast.MatchVariantPattern)
			if !ok || vp == nil || len(vp.Args) != 0 || vp.As != "" || !tr.a.constEnumPatternNamesEnum(vp.EnumName, ce) {
				return "", false
			}
			member, ok := ce.Member(vp.Variant)
			if !ok || member == nil {
				return "", false
			}
			covered[member] = true
			guards = append(guards, "(= "+subject+" "+smtInt(member.Value)+")")
		}
		if len(guards) == 0 {
			return "", false
		}
		guard := guards[0]
		if len(guards) > 1 {
			guard = "(or " + strings.Join(guards, " ") + ")"
		}
		arms = append(arms, armTerm{guard: guard, body: body})
	}
	if !hasDefault {
		for _, member := range ce.Members {
			if !covered[member] {
				return "", false
			}
		}
		// Exhaustive: the last arm is the else-branch of the dispatch chain. No range fact is assumed
		// about the subject — an out-of-member value (e.g. from a cast) takes the same last arm here as
		// the ite does, so the term never relies on the enum's typing.
	}
	result := arms[len(arms)-1].body
	for i := len(arms) - 2; i >= 0; i-- {
		result = "(ite " + arms[i].guard + " " + arms[i].body + " " + result + ")"
	}
	return result, true
}
