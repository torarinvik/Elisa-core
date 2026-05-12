package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"strconv"
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
	case *ast.StaticAssertStmt:
		return exprReferencesVariantFields(n.Cond, name) || (n.Message != nil && exprReferencesVariantFields(n.Message, name))
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
		if exprReferencesVariantFields(n.SafeReceiver, name) {
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
		return exprReferencesVariantFields(n.Owner, name)
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
	case *ast.MatchOrPattern:
		if _, ok := a.collectOrPatternBindingTypes(p, expected); !ok || len(p.Options) == 0 {
			return
		}
		a.analyzeNestedMatchPattern(p.Options[0], expected, valueExpr, scope)
	case *ast.MatchBindPattern:
		if a.analyzePredicateMatchPattern(p, expected, valueExpr) {
			return
		}
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
			patternCategory, variant, ok := a.resolveTreeMatchPatternCategory(variantBase, p)
			if !ok {
				return
			}
			orderedArgs := a.resolveMatchPatternArgs(p, variant, patternCategory.Name+"."+variant.Name, true)
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
	case *ast.MatchListPattern:
		elemType, ok := SequenceMatchElementType(expected)
		if !ok {
			a.errorf(p.Pos(), "list pattern requires an array, darray, view, or string-like value, got %s", expected)
			return
		}
		for i, elem := range p.Elems {
			if _, ok := elem.(*ast.MatchRestPattern); ok {
				if i != len(p.Elems)-1 {
					a.errorf(elem.Pos(), "list pattern rest marker must be the final element")
				}
				continue
			}
			indexExpr := &ast.IndexExpr{
				Position: elem.Pos(),
				Object:   valueExpr,
				Index:    &ast.IntLit{Position: elem.Pos(), Value: fmt.Sprintf("%d", i), Suffix: "u"},
			}
			a.analyzeNestedMatchPattern(elem, elemType, indexExpr, scope)
		}
	case *ast.MatchStructPattern:
		if a.analyzeCountMatchPattern(p, expected) {
			return
		}
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
func (a *Analyzer) analyzeCountMatchPattern(pattern *ast.MatchStructPattern, expected Type) bool {
	if pattern == nil || pattern.TypeName != "count" || pattern.Brace {
		return false
	}
	if _, ok := SequenceMatchElementType(expected); !ok {
		a.errorf(pattern.Pos(), "count pattern requires an array, darray, view, or string-like value, got %s", expected)
		return true
	}
	if len(pattern.Args) != 1 || pattern.Args[0].Name != "" {
		a.errorf(pattern.Pos(), "count pattern expects one positional integer literal")
		return true
	}
	literal, ok := pattern.Args[0].Pattern.(*ast.MatchLiteralPattern)
	if !ok {
		a.errorf(pattern.Args[0].Pattern.Pos(), "count pattern expects an integer literal")
		return true
	}
	intLit, ok := literal.Value.(*ast.IntLit)
	if !ok {
		a.errorf(literal.Pos(), "count pattern expects an integer literal")
		return true
	}
	if _, err := strconv.ParseUint(intLit.Value, 10, 64); err != nil {
		a.errorf(intLit.Pos(), "count pattern expects a non-negative integer literal")
	}
	return true
}
func SequenceMatchElementType(actual Type) (Type, bool) {
	actual = StripAggregateStateType(actual)
	switch t := actual.(type) {
	case *ArrayType:
		if isStringArrayType(t) {
			return builtinCharType(), true
		}
		return t.Elem, true
	case *DArrayType:
		return t.Elem, true
	case *ViewType:
		return t.Elem, true
	case *DArrayViewType:
		return t.Elem, true
	case *DStrType:
		return builtinCharType(), true
	case *RefType:
		if t.State != RefStateNonNull {
			return nil, false
		}
		return SequenceMatchElementType(t.Elem)
	default:
		if isStringViewType(actual) {
			return builtinCharType(), true
		}
		if elem, ok := ChunksExactViewItemType(actual); ok {
			return elem, true
		}
		return nil, false
	}
}
func (a *Analyzer) analyzePredicateMatchPattern(pattern *ast.MatchBindPattern, expected Type, valueExpr ast.Expr) bool {
	if pattern == nil || pattern.Name == "" || pattern.Name == "_" {
		return false
	}
	if _, ok := a.predicateMatchPatternFuncType(pattern.Name); !ok {
		return false
	}
	if valueExpr == nil {
		return true
	}
	call := &ast.CallExpr{
		Position: pattern.Position,
		Func:     &ast.Ident{Position: pattern.Position, Name: pattern.Name},
		Args:     []ast.Expr{valueExpr},
	}
	boolType := a.namedTypes["bool"]
	if boolType == nil {
		boolType = &BuiltinType{Name: "bool"}
	}
	a.analyzeValueExpr(call, boolType)
	return true
}
func (a *Analyzer) predicateMatchPatternFuncType(name string) (*FuncType, bool) {
	if a == nil || name == "" {
		return nil, false
	}
	sym, _, ok := a.lookupVisibleGlobal(name)
	if !ok || sym == nil {
		return nil, false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || funcTypeExplicitParamCount(fnType) != 1 || !IsBoolType(fnType.Return) {
		return nil, false
	}
	return fnType, true
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
