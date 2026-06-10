package semantic

import (
	"fmt"

	"elisacore/src/ast"
)

func moveBindVariantAsMatchPattern(pattern *ast.MoveBindVariantPattern) *ast.MatchVariantPattern {
	if pattern == nil {
		return nil
	}
	return &ast.MatchVariantPattern{Position: pattern.Position, EnumName: pattern.EnumName, Variant: pattern.Variant, Args: append([]ast.MatchPatternArg(nil), pattern.Args...)}
}

func moveBindVariantFieldKey(variant *EnumVariant, index int) string {
	if variant == nil {
		return ""
	}
	if label := variant.PayloadLabel(index); label != "" {
		return label
	}
	return fmt.Sprintf("#%d", index)
}

func (a *Analyzer) resolveMoveBindVariantPattern(stmt *ast.MoveBindStmt, pattern *ast.MoveBindVariantPattern, actual Type) ([]moveBindResolvedVariantField, *EnumType, *regionRefState, bool) {
	if pattern == nil {
		return nil, nil, nil, false
	}
	enumType, _, ok := resolveMatchableEnumType(actual)
	if !ok {
		a.errorf(pattern.Pos(), "move-as variant pattern %q.%q requires an enum value, got %s", pattern.EnumName, pattern.Variant, actual)
		return nil, nil, nil, false
	}
	if enumType.Name != pattern.EnumName {
		a.errorf(pattern.Pos(), "move-as pattern expects enum %q, got %q", pattern.EnumName, enumType.Name)
		return nil, nil, nil, false
	}
	var storeState *regionRefState
	if enumType.Packed {
		a.validateMoveBindStore(pattern.Pos(), stmt.Value, actual, enumType, stmt.Store)
		if stmt.Store != nil {
			if state, ok := a.regionRefStateForExpr(stmt.Store); ok {
				cloned := cloneRegionRefState(state)
				storeState = &cloned
			}
		}
	} else if stmt.Store != nil {
		a.errorf(stmt.Store.Pos(), "ordinary enum move-as over %q does not take an in-store clause", enumType.Name)
		return nil, nil, nil, false
	}
	variant, ok := enumType.Variant(pattern.Variant)
	if !ok {
		a.errorf(pattern.Pos(), "enum %q has no variant %q", enumType.Name, pattern.Variant)
		return nil, nil, nil, false
	}
	orderedArgs := a.resolveMatchPatternArgs(moveBindVariantAsMatchPattern(pattern), variant, enumType.Name+"."+variant.Name, false)
	fields := make([]moveBindResolvedVariantField, 0, len(orderedArgs))
	for i, arg := range orderedArgs {
		if arg == nil {
			continue
		}
		fields = a.collectMoveBindVariantBindings(arg.Pattern, variant.Payload[i], []string{moveBindVariantFieldKey(variant, i)}, fields)
	}
	return fields, enumType, storeState, true
}

func (a *Analyzer) resolveMoveBindTreeVariantPattern(pattern *ast.MoveBindVariantPattern, treeType *TreeCategoryType) ([]moveBindResolvedVariantField, bool) {
	if pattern == nil || treeType == nil {
		return nil, false
	}
	if treeType.Name != pattern.EnumName {
		a.errorf(pattern.Pos(), "move-as pattern expects tree category %q, got %q", pattern.EnumName, treeType.Name)
		return nil, false
	}
	variant, ok := treeType.Variant(pattern.Variant)
	if !ok {
		a.errorf(pattern.Pos(), "tree category %q has no variant %q", treeType.Name, pattern.Variant)
		return nil, false
	}
	orderedArgs := a.resolveMatchPatternArgs(moveBindVariantAsMatchPattern(pattern), variant, treeType.Name+"."+variant.Name, false)
	fields := make([]moveBindResolvedVariantField, 0, len(orderedArgs))
	for i, arg := range orderedArgs {
		if arg == nil {
			continue
		}
		fields = a.collectMoveBindVariantBindings(arg.Pattern, variant.Payload[i], []string{moveBindVariantFieldKey(variant, i)}, fields)
	}
	return fields, true
}

func (a *Analyzer) collectMoveBindVariantBindings(pattern ast.MatchPattern, expected Type, path []string, fields []moveBindResolvedVariantField) []moveBindResolvedVariantField {
	if pattern == nil {
		return fields
	}
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return fields
	case *ast.MatchBindPattern:
		return append(fields, moveBindResolvedVariantField{Path: append([]string(nil), path...), Type: expected, BindName: p.Name, Position: p.Pos()})
	case *ast.MatchStringLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), &ast.StringLit{Position: p.Position, Value: p.Value}, expected, "move-as nested pattern")
		return fields
	case *ast.MatchLiteralPattern:
		a.analyzeLiteralMatchPatternExpr(p.Pos(), p.Value, expected, "move-as nested pattern")
		return fields
	case *ast.MatchTuplePattern:
		resolvedFields, ok := a.resolveMatchTuplePattern(p, expected)
		if !ok {
			return fields
		}
		limit := len(p.Elems)
		if len(resolvedFields) < limit {
			limit = len(resolvedFields)
		}
		for i := 0; i < limit; i++ {
			childPath := append(append([]string(nil), path...), resolvedFields[i].Name)
			fields = a.collectMoveBindVariantBindings(p.Elems[i], resolvedFields[i].Type, childPath, fields)
		}
		return fields
	case *ast.MatchOrPattern:
		for _, option := range p.Options {
			fields = a.collectMoveBindVariantBindings(option, expected, path, fields)
		}
		return fields
	case *ast.MatchStructPattern:
		resolvedFields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return fields
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			childPath := append(append([]string(nil), path...), resolvedFields[i].Name)
			fields = a.collectMoveBindVariantBindings(arg.Pattern, resolvedFields[i].Type, childPath, fields)
		}
		return fields
	case *ast.MatchVariantPattern:
		enumType, _, enumOK := resolveMatchableEnumType(expected)
		if enumOK && enumType != nil {
			p.EnumName = a.canonicalizeMatchEnumName(p.EnumName, enumType.Name)
			if p.EnumName != enumType.Name {
				a.errorf(p.Pos(), "nested move-as pattern expects enum %q, got %q", enumType.Name, p.EnumName)
				return fields
			}
			variant, ok := enumType.Variant(p.Variant)
			if !ok {
				a.errorf(p.Pos(), "enum %q has no variant %q", enumType.Name, p.Variant)
				return fields
			}
			orderedArgs := a.resolveMatchPatternArgs(p, variant, enumType.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				childPath := append(append([]string(nil), path...), moveBindVariantFieldKey(variant, i))
				fields = a.collectMoveBindVariantBindings(arg.Pattern, variant.Payload[i], childPath, fields)
			}
			return fields
		}
		treeType, _, treeOK := resolveMatchableTreeCategoryType(expected)
		if !treeOK || treeType == nil {
			a.errorf(p.Pos(), "nested move-as pattern %q requires an enum or tree-category payload, got %s", p.EnumName+"."+p.Variant, expected)
			return fields
		}
		p.EnumName = a.canonicalizeMatchEnumName(p.EnumName, treeType.Name)
		if p.EnumName != treeType.Name {
			a.errorf(p.Pos(), "nested move-as pattern expects tree category %q, got %q", treeType.Name, p.EnumName)
			return fields
		}
		variant, ok := treeType.Variant(p.Variant)
		if !ok {
			a.errorf(p.Pos(), "tree category %q has no variant %q", treeType.Name, p.Variant)
			return fields
		}
		orderedArgs := a.resolveMatchPatternArgs(p, variant, treeType.Name+"."+variant.Name, true)
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			childPath := append(append([]string(nil), path...), moveBindVariantFieldKey(variant, i))
			fields = a.collectMoveBindVariantBindings(arg.Pattern, variant.Payload[i], childPath, fields)
		}
		return fields
	default:
		a.errorf(pattern.Pos(), "unsupported move-as nested pattern %T", pattern)
		return fields
	}
}

func (a *Analyzer) resolveVariantPayloadValueExpr(value ast.Expr, typeName string, variantName string, key string) (ast.Expr, bool) {
	if value == nil || typeName == "" || variantName == "" || key == "" {
		return nil, false
	}
	switch n := value.(type) {
	case *ast.ParenExpr:
		return a.resolveVariantPayloadValueExpr(n.Inner, typeName, variantName, key)
	case *ast.CastExpr:
		return a.resolveVariantPayloadValueExpr(n.Operand, typeName, variantName, key)
	case *ast.MoveExpr:
		return a.resolveVariantPayloadValueExpr(n.Operand, typeName, variantName, key)
	case *ast.CanExpr:
		return a.resolveVariantPayloadValueExpr(n.Expr, typeName, variantName, key)
	case *ast.AllocExpr:
		return a.resolveVariantPayloadValueExpr(n.Value, typeName, variantName, key)
	case *ast.FieldExpr:
		resolved, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field)
		if !ok {
			return nil, false
		}
		return a.resolveVariantPayloadValueExpr(resolved, typeName, variantName, key)
	case *ast.Ident:
		if a.currentScope == nil {
			return nil, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym.Kind != SymbolLocal || sym.Mutable {
			return nil, false
		}
		decl, ok := sym.Node.(*ast.VarDeclStmt)
		if !ok || decl.Value == nil {
			return nil, false
		}
		return a.resolveVariantPayloadValueExpr(decl.Value, typeName, variantName, key)
	case *ast.CallExpr:
		enumType, variant, ok := a.enumConstructorCall(n)
		if ok && enumType != nil && variant != nil {
			if enumType.Name != typeName || variant.Name != variantName {
				return nil, false
			}
			var orderedArgs []ast.Expr
			if enumType.Packed {
				var commonArgs map[string]ast.Expr
				orderedArgs, commonArgs, ok = a.resolvePackedEnumConstructorArgs(n, enumType, variant)
				_ = commonArgs
			} else {
				orderedArgs, ok = a.resolveEnumConstructorArgs(n, enumType, variant)
			}
			if !ok {
				return nil, false
			}
			for i, arg := range orderedArgs {
				if moveBindVariantFieldKey(variant, i) == key {
					return arg, true
				}
			}
			return nil, false
		}
		treeType, variant, ok := a.treeConstructorCall(n)
		if !ok || treeType == nil || variant == nil {
			return nil, false
		}
		if treeType.Name != typeName || variant.Name != variantName {
			return nil, false
		}
		orderedArgs, _, ok := a.resolveTreeConstructorArgs(n, treeType, variant)
		if !ok {
			return nil, false
		}
		for i, arg := range orderedArgs {
			if moveBindVariantFieldKey(variant, i) == key {
				return arg, true
			}
		}
		return nil, false
	default:
		return nil, false
	}
}

func (a *Analyzer) resolveMoveBindVariantPayloadValueExpr(value ast.Expr, pattern *ast.MoveBindVariantPattern, key string) (ast.Expr, bool) {
	if pattern == nil {
		return nil, false
	}
	return a.resolveVariantPayloadValueExpr(value, pattern.EnumName, pattern.Variant, key)
}

func (a *Analyzer) resolveMatchVariantPayloadValueExprPath(value ast.Expr, pattern *ast.MatchVariantPattern, path []string) (ast.Expr, bool) {
	if pattern == nil || len(path) == 0 {
		return nil, false
	}
	current, ok := a.resolveMatchVariantPayloadValueExpr(value, pattern, path[0])
	if !ok {
		return nil, false
	}
	if len(path) == 1 {
		return current, true
	}
	base, _, ok := a.lookupVisibleType(pattern.EnumName)
	if !ok {
		return nil, false
	}
	switch variantBase := base.(type) {
	case *EnumType:
		if variantBase == nil {
			return nil, false
		}
		variant, ok := variantBase.Variant(pattern.Variant)
		if !ok || variant == nil {
			return nil, false
		}
		orderedArgs := a.resolveMatchPatternArgs(pattern, variant, variantBase.Name+"."+variant.Name, true)
		for i, arg := range orderedArgs {
			if arg == nil || moveBindVariantFieldKey(variant, i) != path[0] {
				continue
			}
			nested, ok := arg.Pattern.(*ast.MatchVariantPattern)
			if !ok {
				return nil, false
			}
			return a.resolveMatchVariantPayloadValueExprPath(current, nested, path[1:])
		}
	case *TreeCategoryType:
		if variantBase == nil {
			return nil, false
		}
		variant, ok := variantBase.Variant(pattern.Variant)
		if !ok || variant == nil {
			return nil, false
		}
		orderedArgs := a.resolveMatchPatternArgs(pattern, variant, variantBase.Name+"."+variant.Name, true)
		for i, arg := range orderedArgs {
			if arg == nil || moveBindVariantFieldKey(variant, i) != path[0] {
				continue
			}
			nested, ok := arg.Pattern.(*ast.MatchVariantPattern)
			if !ok {
				return nil, false
			}
			return a.resolveMatchVariantPayloadValueExprPath(current, nested, path[1:])
		}
	}
	return nil, false
}

func (a *Analyzer) resolveMoveBindVariantPayloadValueExprPath(value ast.Expr, pattern *ast.MoveBindVariantPattern, path []string) (ast.Expr, bool) {
	if pattern == nil {
		return nil, false
	}
	return a.resolveMatchVariantPayloadValueExprPath(value, moveBindVariantAsMatchPattern(pattern), path)
}

func projectRegionFieldPathState(state regionRefState, path []string) (regionRefState, bool) {
	current := cloneRegionRefState(state)
	for _, field := range path {
		next, ok := projectRegionFieldState(current, field)
		if !ok {
			return regionRefState{}, false
		}
		current = next
	}
	return current, true
}

func projectBorrowedOwnerRefFieldPathState(state borrowedOwnerRefState, path []string) (borrowedOwnerRefState, bool) {
	current := cloneBorrowedOwnerRefState(state)
	for _, field := range path {
		next, ok := projectBorrowedOwnerRefFieldState(current, field)
		if !ok {
			return borrowedOwnerRefState{}, false
		}
		current = next
	}
	return current, true
}

func (a *Analyzer) bindResolvedMoveBindVariantFields(payloads []moveBindResolvedVariantField, value ast.Expr, pattern *ast.MoveBindVariantPattern, node ast.Node, valueState regionRefState, hasValueState bool, borrowedOwnerState borrowedOwnerRefState, hasBorrowedOwnerState bool, packedStoreState *regionRefState) {
	if a == nil || pattern == nil {
		return
	}
	for _, payload := range payloads {
		if payload.BindName == "" || payload.BindName == "_" {
			continue
		}
		sym := &Symbol{Name: payload.BindName, Kind: SymbolLocal, Type: payload.Type, Node: node, Mutable: false}
		a.defineLocal(sym, payload.Position)
		if valueExpr, ok := a.resolveMoveBindVariantPayloadValueExprPath(value, pattern, payload.Path); ok {
			a.recordValueBinding(sym, valueExpr)
			a.recordFunctionValueBinding(sym, valueExpr)
			a.recordImmutableSymbolOptimizationFacts(sym, valueExpr)
		}
		if hasBorrowedOwnerState {
			if fieldState, ok := projectBorrowedOwnerRefFieldPathState(borrowedOwnerState, payload.Path); ok {
				a.currentBorrowedOwnerRefs[sym] = fieldState
			}
		}
		if !hasValueState && packedStoreState == nil {
			continue
		}
		if hasValueState {
			if fieldState, ok := projectRegionFieldPathState(valueState, payload.Path); ok {
				a.recordResolvedRegionRefBinding(sym, fieldState)
				continue
			}
			if a.typeCanContainRegionRefs(payload.Type, map[string]bool{}) {
				a.recordResolvedRegionRefBinding(sym, valueState)
				continue
			}
		}
		if packedStoreState != nil && a.typeCanContainRegionRefs(payload.Type, map[string]bool{}) {
			a.recordResolvedRegionRefBinding(sym, *packedStoreState)
		}
	}
}

func (a *Analyzer) resolveMatchVariantPayloadValueExpr(value ast.Expr, pattern *ast.MatchVariantPattern, key string) (ast.Expr, bool) {
	if pattern == nil {
		return nil, false
	}
	return a.resolveVariantPayloadValueExpr(value, pattern.EnumName, pattern.Variant, key)
}
