package semantic

import (
	"sort"
	"strings"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func matchBodyReferencesVariantFields(stmts []ast.Stmt, valueExpr ast.Expr) bool {
	ident, ok := unwrapPackedVariantViewExpr(valueExpr).(*ast.Ident)
	if !ok || ident == nil || ident.Name == "" {
		return false
	}
	for _, stmt := range stmts {
		if stmtReferencesVariantFields(stmt, ident.Name) {
			return true
		}
	}
	return false
}

func stmtReferencesVariantFields(stmt ast.Stmt, name string) bool {
	if stmt == nil || name == "" {
		return false
	}
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		return exprReferencesVariantFields(n.Value, name)
	case *ast.MoveBindStmt:
		return exprReferencesVariantFields(n.Value, name) || exprReferencesVariantFields(n.Store, name)
	case *ast.AssignStmt:
		return exprReferencesVariantFields(n.Target, name) || exprReferencesVariantFields(n.Value, name)
	case *ast.AugAssignStmt:
		return exprReferencesVariantFields(n.Target, name) || exprReferencesVariantFields(n.Value, name)
	case *ast.AsRefAssignStmt:
		return exprReferencesVariantFields(n.Target, name) || exprReferencesVariantFields(n.Value, name)
	case *ast.ReturnStmt:
		return exprReferencesVariantFields(n.Value, name)
	case *ast.IfStmt:
		if exprReferencesVariantFields(n.Cond, name) {
			return true
		}
		for _, inner := range n.Then {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
		for _, elif := range n.Elifs {
			if exprReferencesVariantFields(elif.Cond, name) {
				return true
			}
			for _, inner := range elif.Body {
				if stmtReferencesVariantFields(inner, name) {
					return true
				}
			}
		}
		for _, inner := range n.Else {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.MatchStmt:
		if exprReferencesVariantFields(n.Value, name) || exprReferencesVariantFields(n.Store, name) {
			return true
		}
		for _, arm := range n.Arms {
			for _, inner := range arm.Body {
				if stmtReferencesVariantFields(inner, name) {
					return true
				}
			}
		}
	case *ast.InStoreStmt:
		if exprReferencesVariantFields(n.Store, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.CanStmt:
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.PoolStmt:
		if exprReferencesVariantFields(n.Workers, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.LockStmt:
		if exprReferencesVariantFields(n.Mutex, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.WhileStmt:
		if exprReferencesVariantFields(n.Cond, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.ForStmt:
		if exprReferencesVariantFields(n.Start, name) || exprReferencesVariantFields(n.End, name) || exprReferencesVariantFields(n.Step, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.IterForStmt:
		if exprReferencesVariantFields(n.Source, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.ParallelForStmt:
		if exprReferencesVariantFields(n.Source, name) {
			return true
		}
		for _, inner := range n.Body {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.PanicStmt:
		return exprReferencesVariantFields(n.Message, name)
	case *ast.ExprStmt:
		return exprReferencesVariantFields(n.Expr, name)
	case *ast.StaticIfStmt:
		for _, inner := range n.Then {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
		for _, elif := range n.Elifs {
			for _, inner := range elif.Body {
				if stmtReferencesVariantFields(inner, name) {
					return true
				}
			}
		}
		for _, inner := range n.Else {
			if stmtReferencesVariantFields(inner, name) {
				return true
			}
		}
	case *ast.StaticErrorStmt:
		return exprReferencesVariantFields(n.Message, name)
	case *ast.DiscardStmt:
		return exprReferencesVariantFields(n.Value, name)
	}
	return false
}

func exprReferencesVariantFields(expr ast.Expr, name string) bool {
	if expr == nil || name == "" {
		return false
	}
	switch n := expr.(type) {
	case *ast.Ident:
		return false
	case *ast.FieldExpr:
		if ident, ok := unwrapPackedVariantViewExpr(n.Object).(*ast.Ident); ok && ident != nil && ident.Name == name {
			return true
		}
		return exprReferencesVariantFields(n.Object, name)
	case *ast.BinaryExpr:
		return exprReferencesVariantFields(n.Left, name) || exprReferencesVariantFields(n.Right, name)
	case *ast.UnaryExpr:
		return exprReferencesVariantFields(n.Operand, name)
	case *ast.CallExpr:
		if exprReferencesVariantFields(n.Func, name) {
			return true
		}
		for _, arg := range n.Args {
			if exprReferencesVariantFields(arg, name) {
				return true
			}
		}
	case *ast.IndexExpr:
		return exprReferencesVariantFields(n.Object, name) || exprReferencesVariantFields(n.Index, name) || exprReferencesVariantFields(n.Fallback, name)
	case *ast.SliceExpr:
		return exprReferencesVariantFields(n.Object, name) || exprReferencesVariantFields(n.Start, name) || exprReferencesVariantFields(n.End, name)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			if exprReferencesVariantFields(elem, name) {
				return true
			}
		}
	case *ast.CastExpr:
		return exprReferencesVariantFields(n.Operand, name)
	case *ast.TernaryExpr:
		return exprReferencesVariantFields(n.Value, name) || exprReferencesVariantFields(n.Cond, name) || exprReferencesVariantFields(n.Alt, name)
	case *ast.AddrOfExpr:
		return exprReferencesVariantFields(n.Operand, name)
	case *ast.MoveExpr:
		return exprReferencesVariantFields(n.Operand, name)
	case *ast.SpecializeExpr:
		return exprReferencesVariantFields(n.Operand, name)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			if exprReferencesVariantFields(arg, name) {
				return true
			}
		}
	case *ast.ParenExpr:
		return exprReferencesVariantFields(n.Inner, name)
	case *ast.RaiseExpr:
		return exprReferencesVariantFields(n.Error, name)
	case *ast.TryExpr:
		return exprReferencesVariantFields(n.Value, name) || exprReferencesVariantFields(n.Fallback, name)
	case *ast.CatchExpr:
		if exprReferencesVariantFields(n.Value, name) {
			return true
		}
		for _, stmt := range n.Success.Body {
			if stmtReferencesVariantFields(stmt, name) {
				return true
			}
		}
		for _, arm := range n.Arms {
			for _, inner := range arm.Body {
				if stmtReferencesVariantFields(inner, name) {
					return true
				}
			}
		}
		return false
	case *ast.UnwrapElseExpr:
		return exprReferencesVariantFields(n.Value, name) || exprReferencesVariantFields(n.Fallback, name)
	case *ast.OptionalBindExpr:
		return exprReferencesVariantFields(n.Value, name)
	case *ast.AllocExpr:
		return exprReferencesVariantFields(n.Owner, name) || exprReferencesVariantFields(n.NodeSpan, name) || exprReferencesVariantFields(n.Value, name)
	case *ast.CanExpr:
		return exprReferencesVariantFields(n.Expr, name)
	case *ast.MatchExpr:
		if exprReferencesVariantFields(n.Value, name) || exprReferencesVariantFields(n.Store, name) {
			return true
		}
		for _, arm := range n.Arms {
			for _, inner := range arm.Body {
				if stmtReferencesVariantFields(inner, name) {
					return true
				}
			}
		}
	case *ast.VisitExpr:
		if exprReferencesVariantFields(n.Value, name) {
			return true
		}
		for _, arm := range n.Arms {
			for _, inner := range arm.Body {
				if stmtReferencesVariantFields(inner, name) {
					return true
				}
			}
		}
	case *ast.FoldExpr:
		if exprReferencesVariantFields(n.Value, name) {
			return true
		}
		for _, arm := range n.Arms {
			for _, inner := range arm.Body {
				if stmtReferencesVariantFields(inner, name) {
					return true
				}
			}
		}
	}
	return false
}

func (a *Analyzer) analyzeNestedMatchPattern(pattern ast.MatchPattern, expected Type, valueExpr ast.Expr, scope *Scope) {
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return
	case *ast.MatchBindPattern:
		sym := &Symbol{Name: p.Name, Kind: SymbolLocal, Type: expected, Node: p, Mutable: false}
		a.defineLocal(sym, p.Pos())
		a.recordValueBinding(sym, valueExpr)
		a.recordBorrowedOwnerRefBinding(sym, valueExpr)
		a.recordFunctionValueBinding(sym, valueExpr)
		a.recordImmutableSymbolOptimizationFacts(sym, valueExpr)
		a.recordRegionRefBinding(sym, valueExpr)
	case *ast.MatchVariantPattern:
		switch variantBase := expected.(type) {
		case *EnumType:
			if p.EnumName != variantBase.Name {
				a.errorf(p.Pos(), "nested match pattern expects enum %q, got %q", variantBase.Name, p.EnumName)
				return
			}
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				a.errorf(p.Pos(), "enum %q has no variant %q", variantBase.Name, p.Variant)
				return
			}
			orderedArgs := a.resolveMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				payloadExpr, _ := a.resolveMatchVariantPayloadValueExpr(valueExpr, p, moveBindVariantFieldKey(variant, i))
				a.analyzeNestedMatchPattern(arg.Pattern, variant.Payload[i], payloadExpr, scope)
			}
		case *ConstEnumType:
			if p.EnumName != variantBase.Name {
				a.errorf(p.Pos(), "nested match pattern expects const enum %q, got %q", variantBase.Name, p.EnumName)
				return
			}
			if _, ok := variantBase.Member(p.Variant); !ok {
				a.errorf(p.Pos(), "const enum %q has no member %q", variantBase.Name, p.Variant)
				return
			}
			if len(p.Args) != 0 {
				a.errorf(p.Pos(), "nested match arm %q expects 0 payload patterns, got %d", variantBase.Name+"."+p.Variant, len(p.Args))
			}
		case *TreeCategoryType:
			if p.EnumName != variantBase.Name {
				a.errorf(p.Pos(), "nested match pattern expects tree category %q, got %q", variantBase.Name, p.EnumName)
				return
			}
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				a.errorf(p.Pos(), "tree category %q has no variant %q", variantBase.Name, p.Variant)
				return
			}
			orderedArgs := a.resolveMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				payloadExpr, _ := a.resolveMatchVariantPayloadValueExpr(valueExpr, p, moveBindVariantFieldKey(variant, i))
				a.analyzeNestedMatchPattern(arg.Pattern, variant.Payload[i], payloadExpr, scope)
			}
		default:
			a.errorf(p.Pos(), "nested variant pattern %q requires an enum, const enum, or tree-category payload, got %s", p.EnumName+"."+p.Variant, expected)
		}
	case *ast.MatchStringLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), &ast.StringLit{Position: p.Position, Value: p.Value}, expected, "nested literal match pattern")
	case *ast.MatchLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), p.Value, expected, "nested literal match pattern")
	case *ast.MatchStructPattern:
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			fieldExpr := &ast.FieldExpr{Position: arg.Position, Object: valueExpr, Field: fields[i].Name}
			a.analyzeNestedMatchPattern(arg.Pattern, fields[i].Type, fieldExpr, scope)
		}
	default:
		a.errorf(pattern.Pos(), "unsupported nested match pattern %T", pattern)
	}
}

func (a *Analyzer) analyzeLiteralMatchPatternExpr(pos lexer.Pos, literalExpr ast.Expr, expected Type, context string) {
	if literalExpr == nil || expected == nil {
		return
	}
	actual := a.analyzeValueExpr(literalExpr, expected)
	if runtimeStringComparable(expected, actual) {
		return
	}
	if IsNumericType(expected) && IsNumericType(actual) {
		return
	}
	if IsBoolType(expected) && IsBoolType(actual) {
		return
	}
	if (IsNullType(actual) && isRefLike(expected)) || (IsNullType(expected) && isRefLike(actual)) {
		return
	}
	if AssignableTo(expected, actual) || AssignableTo(actual, expected) || refsComparableIgnoringMutability(expected, actual) {
		return
	}
	a.errorf(pos, "%s cannot compare %s against %s", context, actual, expected)
}

func (a *Analyzer) resolveMatchPatternArgs(pattern *ast.MatchVariantPattern, variant *EnumVariant, qualified string, nested bool) []*ast.MatchPatternArg {
	return a.resolveMatchPatternArgsWithOptions(pattern, variant, qualified, nested, false)
}

func (a *Analyzer) resolvePartialMatchPatternArgs(pattern *ast.MatchVariantPattern, variant *EnumVariant, qualified string, nested bool) []*ast.MatchPatternArg {
	return a.resolveMatchPatternArgsWithOptions(pattern, variant, qualified, nested, true)
}

func (a *Analyzer) resolveMatchPatternArgsWithOptions(pattern *ast.MatchVariantPattern, variant *EnumVariant, qualified string, nested bool, allowPartialNamed bool) []*ast.MatchPatternArg {
	ordered := make([]*ast.MatchPatternArg, len(variant.Payload))
	if len(pattern.Args) == 0 {
		pattern.ResolvedArgs = ordered
		return ordered
	}
	namedCount := 0
	for i := range pattern.Args {
		if pattern.Args[i].Name != "" {
			namedCount++
		}
	}
	if namedCount != 0 && namedCount != len(pattern.Args) {
		a.errorf(pattern.Pos(), "%s cannot mix positional and named payload patterns", matchPatternContext(qualified, nested))
	}
	if namedCount == 0 {
		if len(pattern.Args) != len(variant.Payload) {
			a.errorf(pattern.Pos(), "%s expects %d payload patterns, got %d", matchPatternContext(qualified, nested), len(variant.Payload), len(pattern.Args))
		}
		limit := len(pattern.Args)
		if len(ordered) < limit {
			limit = len(ordered)
		}
		for i := 0; i < limit; i++ {
			ordered[i] = &pattern.Args[i]
		}
		pattern.ResolvedArgs = ordered
		return ordered
	}
	if !variant.HasNamedPayloads() {
		a.errorf(pattern.Pos(), "%s does not declare named payload fields", matchPatternContext(qualified, nested))
		pattern.ResolvedArgs = ordered
		return ordered
	}
	seen := map[int]lexer.Pos{}
	for i := range pattern.Args {
		arg := &pattern.Args[i]
		index, ok := variant.PayloadIndex(arg.Name)
		if !ok {
			a.errorf(arg.Position, "%s has no payload field %q", matchPatternContext(qualified, nested), arg.Name)
			continue
		}
		if prev, exists := seen[index]; exists {
			a.errorf(arg.Position, "%s payload field %q is matched more than once (first at %s:%d:%d)", matchPatternContext(qualified, nested), arg.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[index] = arg.Position
		ordered[index] = arg
	}
	missing := make([]string, 0)
	for i := range ordered {
		if ordered[i] == nil {
			missing = append(missing, variant.PayloadLabel(i))
		}
	}
	if len(missing) > 0 && !allowPartialNamed {
		sort.Strings(missing)
		a.errorf(pattern.Pos(), "%s is missing named payload patterns for: %s", matchPatternContext(qualified, nested), strings.Join(missing, ", "))
	}
	pattern.ResolvedArgs = ordered
	return ordered
}

func matchPatternContext(qualified string, nested bool) string {
	if nested {
		return "nested match arm " + strconvQuote(qualified)
	}
	return "match arm " + strconvQuote(qualified)
}

func (a *Analyzer) matchCoversAllVariants(variantBase Type, covered map[string]bool, hasWildcard bool) bool {
	if hasWildcard {
		return true
	}
	switch tt := variantBase.(type) {
	case *EnumType:
		if tt == nil {
			return false
		}
		for _, variant := range tt.Variants {
			if !covered[variant.Name] {
				return false
			}
		}
		return true
	case *ConstEnumType:
		if tt == nil {
			return false
		}
		for _, member := range tt.Members {
			if !covered[member.Name] {
				return false
			}
		}
		return true
	case *ErrorSetType:
		if tt == nil {
			return false
		}
		for _, tag := range tt.Tags {
			if !covered[tag] {
				return false
			}
		}
		return true
	case *TreeCategoryType:
		if tt == nil {
			return false
		}
		for _, variant := range tt.Variants {
			if !covered[variant.Name] {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func strconvQuote(s string) string {
	return "\"" + s + "\""
}

func (a *Analyzer) reportNonExhaustiveMatch(pos lexer.Pos, variantBase Type, covered map[string]bool, hasWildcard bool) {
	if hasWildcard {
		return
	}
	switch tt := variantBase.(type) {
	case *EnumType:
		if tt == nil {
			return
		}
		missing := make([]string, 0)
		for _, variant := range tt.Variants {
			if !covered[variant.Name] {
				missing = append(missing, tt.Name+"."+variant.Name)
			}
		}
		if len(missing) == 0 {
			return
		}
		a.errorf(pos, "non-exhaustive match over %q; missing variants: %s", tt.Name, strings.Join(missing, ", "))
	case *ConstEnumType:
		if tt == nil {
			return
		}
		missing := make([]string, 0)
		for _, member := range tt.Members {
			if !covered[member.Name] {
				missing = append(missing, tt.Name+"."+member.Name)
			}
		}
		if len(missing) == 0 {
			return
		}
		a.errorf(pos, "non-exhaustive match over %q; missing members: %s", tt.Name, strings.Join(missing, ", "))
	case *ErrorSetType:
		if tt == nil {
			return
		}
		missing := make([]string, 0)
		for _, tag := range tt.Tags {
			if !covered[tag] {
				missing = append(missing, tag)
			}
		}
		if len(missing) == 0 {
			return
		}
		a.errorf(pos, "non-exhaustive match over %q; missing tags: %s", tt.Name, strings.Join(missing, ", "))
	case *TreeCategoryType:
		if tt == nil {
			return
		}
		missing := make([]string, 0)
		for _, variant := range tt.Variants {
			if !covered[variant.Name] {
				missing = append(missing, tt.Name+"."+variant.Name)
			}
		}
		if len(missing) == 0 {
			return
		}
		a.errorf(pos, "non-exhaustive match over %q; missing variants: %s", tt.Name, strings.Join(missing, ", "))
	}
}

func (a *Analyzer) reportNonExhaustiveStringMatchExpr(pos lexer.Pos, hasWildcard bool) {
	if hasWildcard {
		return
	}
	a.errorf(pos, "non-exhaustive string match expression; add a final _ arm")
}
