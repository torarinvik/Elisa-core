package semantic

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"sort"
	"strings"
)

func (a *Analyzer) analyzeStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		var declType Type
		if n.Type != nil {
			declType = a.resolveType(n.Type)
		}
		if n.Value != nil {
			valueType := a.analyzeValueExpr(n.Value, declType)
			if declType == nil {
				declType = valueType
			} else if !AssignableTo(declType, valueType) {
				a.errorf(n.Pos(), "variable %q expects %s, got %s", n.Name, declType.String(), valueType.String())
				a.reportShapeMismatchNotes(n.Pos(), declType, valueType)
			}
		} else if declType == nil {
			a.errorf(n.Pos(), "variable %q requires a type or initializer", n.Name)
			declType = invalidType
		}
		sym := &Symbol{Name: n.Name, Kind: SymbolLocal, Type: declType, Node: n, Mutable: n.Mutable}
		a.defineLocal(sym, n.Pos())
		a.recordRegionRefBinding(sym, n.Value)
	case *ast.RegionStmt:
		if n.Capacity != nil {
			capacityType := a.analyzeExpr(n.Capacity)
			if !IsNumericType(capacityType) {
				a.errorf(n.Capacity.Pos(), "region capacity must be numeric, got %s", capacityType.String())
			}
		}
		arenaType, ok := a.namedTypes["Arena"]
		if !ok {
			a.errorf(n.Pos(), "missing builtin Arena type for region lowering")
			arenaType = invalidType
		}
		sym := &Symbol{Name: n.Name, Kind: SymbolRegion, Type: arenaType, Node: n, Mutable: false}
		a.defineLocal(sym, n.Pos())
		if a.currentRegions != nil {
			a.currentRegions[sym] = regionState{}
		}
	case *ast.MarkStmt:
		a.analyzeMarkStmt(n)
	case *ast.RestoreStmt:
		a.analyzeRestoreStmt(n)
	case *ast.ResetStmt:
		a.analyzeResetStmt(n)
	case *ast.DestroyStmt:
		sym, state := a.lookupRegionState(n.Name)
		if sym == nil {
			a.errorf(n.Pos(), "undefined region %q", n.Name)
			return
		}
		if state.Destroyed {
			a.errorf(n.Pos(), "region %q has already been destroyed", n.Name)
			return
		}
		state.Destroyed = true
		a.currentRegions[sym] = state
		a.invalidateRegionRefs(sym, func(regionRefState) bool { return true }, fmt.Sprintf("destroy of region %q", n.Name))
		a.invalidateRegionMarks(sym, func(regionMarkState) bool { return true }, fmt.Sprintf("destroy of region %q", n.Name))
	case *ast.AssignStmt:
		targetType := a.assignmentTargetType(n.Target)
		valueType := a.analyzeValueExpr(n.Value, targetType)
		if !AssignableTo(targetType, valueType) {
			a.errorf(n.Pos(), "cannot assign %s to %s", valueType.String(), targetType.String())
			a.reportShapeMismatchNotes(n.Pos(), targetType, valueType)
		}
		a.recordAssignmentRefinement(n.Target, targetType, valueType)
		a.recordRegionRefAssignment(n.Target, n.Value)
	case *ast.AugAssignStmt:
		targetType := a.assignmentTargetType(n.Target)
		valueType := a.analyzeExpr(n.Value)
		if !IsNumericType(targetType) || !IsNumericType(valueType) {
			a.errorf(n.Pos(), "augmented assignment requires numeric operands")
		}
	case *ast.AsRefAssignStmt:
		targetType := a.asRefTargetType(n.Target, n.AsKind)
		valueType := a.analyzeValueExpr(n.Value, targetType)
		if !AssignableTo(targetType, valueType) {
			a.errorf(n.Pos(), "cannot assign %s to %s", valueType.String(), targetType.String())
			a.reportShapeMismatchNotes(n.Pos(), targetType, valueType)
		}
		a.recordAssignmentRefinement(n.Target, targetType, targetType)
		a.recordRegionRefAssignment(n.Target, n.Value)
	case *ast.ReturnStmt:
		if n.Value == nil {
			if currentUnion, ok := a.currentReturn.(*ErrorUnionType); ok {
				if !SameType(currentUnion.Value, a.namedTypes["void"]) {
					a.errorf(n.Pos(), "return value required for %s", a.currentReturn.String())
				}
				return
			}
			if a.currentReturn != nil && !SameType(a.currentReturn, a.namedTypes["void"]) {
				a.errorf(n.Pos(), "return value required for %s", a.currentReturn.String())
			}
			return
		}
		valueType := a.analyzeValueExpr(n.Value, a.currentReturn)
		if a.currentReturn == nil {
			a.errorf(n.Pos(), "unexpected return value")
			return
		}
		if refState, ok := a.regionRefStateForExpr(n.Value); ok && refState.Valid && refState.Region != nil {
			a.errorf(n.Pos(), "cannot return reference allocated from local region %q", refState.Region.Name)
		}
		a.recordFreshReturnBindings(valueType)
		expectedReturn := a.matchReturnType(valueType)
		if !AssignableTo(expectedReturn, valueType) {
			a.errorf(n.Pos(), "return type expects %s, got %s", expectedReturn.String(), valueType.String())
			a.reportShapeMismatchNotes(n.Pos(), expectedReturn, valueType)
		}
	case *ast.IfStmt:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "if condition must be bool, got %s", condType.String())
		}
		a.analyzeBlockWithRegionClone(n.Then, a.refinedScopeForCondition(a.currentScope, n.Cond, true))
		for _, elif := range n.Elifs {
			elifType := a.analyzeExpr(elif.Cond)
			if !IsBoolType(elifType) {
				a.errorf(elif.Position, "elif condition must be bool, got %s", elifType.String())
			}
			a.analyzeBlockWithRegionClone(elif.Body, a.refinedScopeForCondition(a.currentScope, elif.Cond, true))
		}
		if len(n.Elifs) == 0 {
			a.analyzeBlockWithRegionClone(n.Else, a.refinedScopeForCondition(a.currentScope, n.Cond, false))
		} else {
			a.analyzeBlockWithRegionClone(n.Else, NewScope(a.currentScope))
		}
		a.applyPostIfFallthroughRefinement(n)
	case *ast.MatchStmt:
		a.analyzeMatchStmt(n)
	case *ast.InStoreStmt:
		a.analyzeInStoreStmt(n)
	case *ast.CanStmt:
		a.analyzeCanStmt(n)
	case *ast.WhileStmt:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "while condition must be bool, got %s", condType.String())
		}
		a.analyzeBlockWithRegionClone(n.Body, a.refinedScopeForCondition(a.currentScope, n.Cond, true))
	case *ast.PassStmt:
		return
	case *ast.PanicStmt:
		a.analyzeExpr(n.Message)
	case *ast.ExprStmt:
		if cond, ok := assertedCondition(n.Expr); ok {
			condType := a.analyzeCondExpr(cond)
			if !IsBoolType(condType) {
				a.errorf(n.Pos(), "assert condition must be bool, got %s", condType.String())
			}
			a.applyConditionRefinements(a.currentScope, cond, true)
			return
		}
		a.analyzeExpr(n.Expr)
	case *ast.StaticIfStmt:
		for _, stmt := range a.activeStmtBranch(n) {
			a.analyzeStmt(stmt)
		}
	case *ast.StaticErrorStmt:
		if msg, ok := a.evalConstStringExpr(n.Message); ok {
			a.errorf(n.Pos(), "static error: %s", msg)
		} else {
			a.errorf(n.Pos(), "static error triggered")
		}
	case *ast.DiscardStmt:
		a.analyzeExpr(n.Value)
	}
}

func (a *Analyzer) analyzeCanStmt(stmt *ast.CanStmt) {
	refs := a.resolvePermissionRefs(stmt.Permissions, true)
	a.recordFunctionPermissionRefs(refs)
	a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
}

func (a *Analyzer) analyzeInStoreStmt(stmt *ast.InStoreStmt) {
	storeType := a.analyzeExpr(stmt.Store)
	packedStore, ok := storeType.(*PackedEnumStoreType)
	if !ok {
		a.errorf(stmt.Store.Pos(), "in-store block requires a packed enum store, got %s", storeType.String())
		a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
		return
	}
	savedPackedStores := a.currentPackedStores
	a.currentPackedStores = a.clonePackedStores()
	if a.currentPackedStores == nil {
		a.currentPackedStores = map[string]*PackedEnumStoreType{}
	}
	a.currentPackedStores[packedStore.Enum.Name] = packedStore
	a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
	a.currentPackedStores = savedPackedStores
}

func (a *Analyzer) analyzeMarkStmt(stmt *ast.MarkStmt) {
	regionSym, state := a.lookupRegionState(stmt.RegionName)
	if regionSym == nil {
		a.errorf(stmt.Pos(), "undefined region %q", stmt.RegionName)
		return
	}
	if state.Destroyed {
		a.errorf(stmt.Pos(), "region %q has already been destroyed", stmt.RegionName)
		return
	}
	markType, ok := a.namedTypes["ArenaMark"]
	if !ok {
		a.errorf(stmt.Pos(), "missing builtin ArenaMark type for region checkpoints")
		markType = invalidType
	}
	markSym := &Symbol{Name: stmt.Name, Kind: SymbolRegionMark, Type: markType, Node: stmt, Mutable: false}
	a.defineLocal(markSym, stmt.Pos())
	state.Generation++
	a.currentRegions[regionSym] = state
	if a.currentRegionMarks == nil {
		a.currentRegionMarks = map[*Symbol]regionMarkState{}
	}
	a.currentRegionMarks[markSym] = regionMarkState{Region: regionSym, Generation: state.Generation, Valid: true}
}

func (a *Analyzer) analyzeRestoreStmt(stmt *ast.RestoreStmt) {
	regionSym, state := a.lookupRegionState(stmt.RegionName)
	if regionSym == nil {
		a.errorf(stmt.Pos(), "undefined region %q", stmt.RegionName)
		return
	}
	if state.Destroyed {
		a.errorf(stmt.Pos(), "region %q has already been destroyed", stmt.RegionName)
		return
	}
	markSym, markState := a.lookupRegionMark(stmt.MarkName)
	if markSym == nil {
		a.errorf(stmt.Pos(), "undefined checkpoint %q", stmt.MarkName)
		return
	}
	if markState.Region != regionSym {
		a.errorf(stmt.Pos(), "checkpoint %q belongs to region %q, not %q", stmt.MarkName, markState.Region.Name, stmt.RegionName)
		return
	}
	if !markState.Valid {
		a.errorf(stmt.Pos(), "checkpoint %q is invalid after %s", stmt.MarkName, markState.InvalidatedBy)
		return
	}
	reason := fmt.Sprintf("restore of region %q from checkpoint %q", stmt.RegionName, stmt.MarkName)
	a.invalidateRegionRefs(regionSym, func(refState regionRefState) bool {
		return refState.Generation >= markState.Generation
	}, reason)
	a.invalidateRegionMarks(regionSym, func(other regionMarkState) bool {
		return other.Generation > markState.Generation
	}, reason)
	state.Generation = markState.Generation
	a.currentRegions[regionSym] = state
	if saved, ok := a.currentRegionMarks[markSym]; ok {
		saved.Valid = true
		saved.InvalidatedBy = ""
		a.currentRegionMarks[markSym] = saved
	}
}

func (a *Analyzer) analyzeResetStmt(stmt *ast.ResetStmt) {
	regionSym, state := a.lookupRegionState(stmt.Name)
	if regionSym == nil {
		a.errorf(stmt.Pos(), "undefined region %q", stmt.Name)
		return
	}
	if state.Destroyed {
		a.errorf(stmt.Pos(), "region %q has already been destroyed", stmt.Name)
		return
	}
	reason := fmt.Sprintf("reset of region %q", stmt.Name)
	a.invalidateRegionRefs(regionSym, func(regionRefState) bool { return true }, reason)
	a.invalidateRegionMarks(regionSym, func(regionMarkState) bool { return true }, reason)
	state.Generation = 0
	a.currentRegions[regionSym] = state
}

func (a *Analyzer) analyzeMatchStmt(stmt *ast.MatchStmt) {
	valueType := a.analyzeExpr(stmt.Value)
	enumType, ok := valueType.(*EnumType)
	if !ok {
		a.errorf(stmt.Pos(), "match requires an enum value, got %s", valueType.String())
		for _, arm := range stmt.Arms {
			a.analyzeBlockWithRegionClone(arm.Body, NewScope(a.currentScope))
		}
		return
	}
	a.validateMatchStore(stmt.Pos(), enumType, stmt.Store)
	priorPatterns := make([]ast.MatchPattern, 0, len(stmt.Arms))
	for i, arm := range stmt.Arms {
		if a.matchPatternShadowedByPrevious(arm.Pattern, enumType, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		a.analyzeTopLevelMatchPattern(arm.Pattern, enumType, scope, i, len(stmt.Arms), nil)
		a.analyzeBlockWithRegionClone(arm.Body, scope)
		priorPatterns = append(priorPatterns, arm.Pattern)
	}
}

func (a *Analyzer) analyzeMatchExpr(expr *ast.MatchExpr) Type {
	valueType := a.analyzeExpr(expr.Value)
	enumType, ok := valueType.(*EnumType)
	if !ok {
		a.errorf(expr.Pos(), "match requires an enum value, got %s", valueType.String())
		for _, arm := range expr.Arms {
			a.analyzeMatchExprArmBody(arm.Body, NewScope(a.currentScope))
		}
		return invalidType
	}
	a.validateMatchStore(expr.Pos(), enumType, expr.Store)
	covered := map[string]bool{}
	hasWildcard := false
	resultType := Type(nil)
	priorPatterns := make([]ast.MatchPattern, 0, len(expr.Arms))
	for i, arm := range expr.Arms {
		if a.matchPatternShadowedByPrevious(arm.Pattern, enumType, priorPatterns) {
			a.errorf(arm.Position, "match arm %q is unreachable because an earlier arm already matches it", matchPatternSummary(arm.Pattern))
		}
		scope := NewScope(a.currentScope)
		if a.analyzeTopLevelMatchPattern(arm.Pattern, enumType, scope, i, len(expr.Arms), covered) {
			hasWildcard = true
		}
		armType := a.analyzeMatchExprArmBody(arm.Body, scope)
		if resultType == nil {
			resultType = armType
			priorPatterns = append(priorPatterns, arm.Pattern)
			continue
		}
		merged := MergeTypes(resultType, armType)
		if IsInvalidType(merged) {
			a.errorf(arm.Position, "match expression arms are incompatible: %s and %s", resultType.String(), armType.String())
			resultType = invalidType
			priorPatterns = append(priorPatterns, arm.Pattern)
			continue
		}
		resultType = merged
		priorPatterns = append(priorPatterns, arm.Pattern)
	}
	a.reportNonExhaustiveMatch(expr.Pos(), enumType, covered, hasWildcard)
	if resultType == nil {
		return neverType
	}
	return resultType
}

func (a *Analyzer) validateMatchStore(pos lexer.Pos, enumType *EnumType, storeExpr ast.Expr) {
	if enumType == nil {
		return
	}
	if !enumType.Packed {
		if storeExpr != nil {
			a.errorf(storeExpr.Pos(), "ordinary enum match over %q does not take an in-store clause", enumType.Name)
		}
		return
	}
	if storeExpr == nil {
		if _, ok := a.lookupPackedStore(enumType); ok {
			return
		}
		a.errorf(pos, "packed enum match over %q requires an in %s clause", enumType.Name, packedEnumStoreTypeName(enumType.Name))
		return
	}
	storeType := a.analyzeExpr(storeExpr)
	packedStore, ok := storeType.(*PackedEnumStoreType)
	if !ok {
		a.errorf(storeExpr.Pos(), "packed enum match over %q requires store type %q, got %s", enumType.Name, packedEnumStoreTypeName(enumType.Name), storeType.String())
		return
	}
	if packedStore.Enum != enumType {
		a.errorf(storeExpr.Pos(), "packed enum match over %q requires store type %q, got %s", enumType.Name, packedEnumStoreTypeName(enumType.Name), storeType.String())
	}
}

func (a *Analyzer) matchPatternShadowedByPrevious(pattern ast.MatchPattern, enumType *EnumType, prior []ast.MatchPattern) bool {
	for _, prev := range prior {
		if a.matchPatternCovers(prev, pattern, enumType) {
			return true
		}
	}
	return false
}

func (a *Analyzer) matchPatternCovers(prev ast.MatchPattern, current ast.MatchPattern, expected Type) bool {
	switch p := prev.(type) {
	case *ast.MatchWildcardPattern:
		return true
	case *ast.MatchBindPattern:
		return true
	case *ast.MatchVariantPattern:
		currVariant, ok := current.(*ast.MatchVariantPattern)
		if !ok {
			return false
		}
		enumType, ok := expected.(*EnumType)
		if !ok || p.EnumName != enumType.Name || currVariant.EnumName != enumType.Name || p.Variant != currVariant.Variant {
			return false
		}
		variant, ok := enumType.Variant(p.Variant)
		if !ok {
			return false
		}
		prevArgs, ok := orderedMatchPatternArgs(p, variant)
		if !ok {
			return false
		}
		currArgs, ok := orderedMatchPatternArgs(currVariant, variant)
		if !ok {
			return false
		}
		for i := range prevArgs {
			if prevArgs[i] == nil || currArgs[i] == nil {
				return false
			}
			if !a.matchPatternCovers(prevArgs[i].Pattern, currArgs[i].Pattern, variant.Payload[i]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func orderedMatchPatternArgs(pattern *ast.MatchVariantPattern, variant *EnumVariant) ([]*ast.MatchPatternArg, bool) {
	ordered := make([]*ast.MatchPatternArg, len(variant.Payload))
	if len(pattern.Args) == 0 {
		return ordered, true
	}
	namedCount := 0
	for i := range pattern.Args {
		if pattern.Args[i].Name != "" {
			namedCount++
		}
	}
	if namedCount != 0 && namedCount != len(pattern.Args) {
		return nil, false
	}
	if namedCount == 0 {
		if len(pattern.Args) != len(variant.Payload) {
			return nil, false
		}
		for i := range pattern.Args {
			ordered[i] = &pattern.Args[i]
		}
		return ordered, true
	}
	if !variant.HasNamedPayloads() || len(pattern.Args) != len(variant.Payload) {
		return nil, false
	}
	seen := map[int]bool{}
	for i := range pattern.Args {
		arg := &pattern.Args[i]
		index, ok := variant.PayloadIndex(arg.Name)
		if !ok || seen[index] {
			return nil, false
		}
		seen[index] = true
		ordered[index] = arg
	}
	for i := range ordered {
		if ordered[i] == nil {
			return nil, false
		}
	}
	return ordered, true
}
func matchPatternSummary(pattern ast.MatchPattern) string {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return "_"
	case *ast.MatchBindPattern:
		return p.Name
	case *ast.MatchVariantPattern:
		if len(p.Args) == 0 {
			return p.EnumName + "." + p.Variant
		}
		parts := make([]string, 0, len(p.Args))
		for _, arg := range p.Args {
			part := matchPatternSummary(arg.Pattern)
			if arg.Name != "" {
				part = arg.Name + ": " + part
			}
			parts = append(parts, part)
		}
		return p.EnumName + "." + p.Variant + "(" + strings.Join(parts, ", ") + ")"
	default:
		return "<pattern>"
	}
}

func (a *Analyzer) analyzeMatchExprArmBody(body []ast.Stmt, scope *Scope) Type {
	savedScope := a.currentScope
	savedRegions := a.currentRegions
	savedPackedStores := a.currentPackedStores
	a.currentScope = scope
	a.currentRegions = a.cloneRegionStates()
	a.currentPackedStores = a.clonePackedStores()
	defer func() {
		a.currentScope = savedScope
		a.currentRegions = savedRegions
		a.currentPackedStores = savedPackedStores
	}()
	if len(body) == 0 {
		return invalidType
	}
	for i, stmt := range body {
		isLast := i == len(body)-1
		if !isLast {
			a.analyzeStmt(stmt)
			if stmtDefinitelyExits(stmt) {
				return neverType
			}
			continue
		}
		if exprStmt, ok := stmt.(*ast.ExprStmt); ok {
			return a.analyzeExpr(exprStmt.Expr)
		}
		a.analyzeStmt(stmt)
		if stmtDefinitelyExits(stmt) {
			return neverType
		}
		a.errorf(stmt.Pos(), "match expression arm must end with an expression")
		return invalidType
	}
	return invalidType
}

func (a *Analyzer) analyzeTopLevelMatchPattern(pattern ast.MatchPattern, enumType *EnumType, scope *Scope, index int, armCount int, covered map[string]bool) bool {
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		if index != armCount-1 {
			a.errorf(p.Pos(), "wildcard match arm must be the final arm")
		}
		return true
	case *ast.MatchVariantPattern:
		if p.EnumName != enumType.Name {
			a.errorf(p.Pos(), "match arm expects enum %q, got %q", enumType.Name, p.EnumName)
			return false
		}
		variant, ok := enumType.Variant(p.Variant)
		if !ok {
			a.errorf(p.Pos(), "enum %q has no variant %q", enumType.Name, p.Variant)
			return false
		}
		qualified := enumType.Name + "." + variant.Name
		if covered != nil {
			covered[variant.Name] = true
		}
		orderedArgs := a.resolveMatchPatternArgs(p, variant, qualified, false)
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			a.analyzeNestedMatchPattern(arg.Pattern, variant.Payload[i], scope)
		}
		return false
	case *ast.MatchBindPattern:
		a.errorf(p.Pos(), "top-level match arm must use %q variants or _", enumType.Name)
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported match pattern %T", pattern)
		return false
	}
}

func (a *Analyzer) analyzeNestedMatchPattern(pattern ast.MatchPattern, expected Type, scope *Scope) {
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return
	case *ast.MatchBindPattern:
		a.defineLocal(&Symbol{Name: p.Name, Kind: SymbolLocal, Type: expected, Node: p, Mutable: false}, p.Pos())
	case *ast.MatchVariantPattern:
		enumType, ok := expected.(*EnumType)
		if !ok {
			a.errorf(p.Pos(), "nested variant pattern %q requires an enum payload, got %s", p.EnumName+"."+p.Variant, expected.String())
			return
		}
		if p.EnumName != enumType.Name {
			a.errorf(p.Pos(), "nested match pattern expects enum %q, got %q", enumType.Name, p.EnumName)
			return
		}
		variant, ok := enumType.Variant(p.Variant)
		if !ok {
			a.errorf(p.Pos(), "enum %q has no variant %q", enumType.Name, p.Variant)
			return
		}
		orderedArgs := a.resolveMatchPatternArgs(p, variant, enumType.Name+"."+variant.Name, true)
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			a.analyzeNestedMatchPattern(arg.Pattern, variant.Payload[i], scope)
		}
	default:
		a.errorf(pattern.Pos(), "unsupported nested match pattern %T", pattern)
	}
}

func (a *Analyzer) resolveMatchPatternArgs(pattern *ast.MatchVariantPattern, variant *EnumVariant, qualified string, nested bool) []*ast.MatchPatternArg {
	ordered := make([]*ast.MatchPatternArg, len(variant.Payload))
	if len(pattern.Args) == 0 {
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
		return ordered
	}
	if !variant.HasNamedPayloads() {
		a.errorf(pattern.Pos(), "%s does not declare named payload fields", matchPatternContext(qualified, nested))
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
	if len(missing) > 0 {
		sort.Strings(missing)
		a.errorf(pattern.Pos(), "%s is missing named payload patterns for: %s", matchPatternContext(qualified, nested), strings.Join(missing, ", "))
	}
	return ordered
}

func matchPatternContext(qualified string, nested bool) string {
	if nested {
		return "nested match arm " + strconvQuote(qualified)
	}
	return "match arm " + strconvQuote(qualified)
}

func strconvQuote(s string) string {
	return "\"" + s + "\""
}

func (a *Analyzer) reportNonExhaustiveMatch(pos lexer.Pos, enumType *EnumType, covered map[string]bool, hasWildcard bool) {
	if enumType == nil || hasWildcard {
		return
	}
	missing := make([]string, 0)
	for _, variant := range enumType.Variants {
		if !covered[variant.Name] {
			missing = append(missing, enumType.Name+"."+variant.Name)
		}
	}
	if len(missing) == 0 {
		return
	}
	a.errorf(pos, "non-exhaustive match over %q; missing variants: %s", enumType.Name, strings.Join(missing, ", "))
}

func (a *Analyzer) analyzeBlock(stmts []ast.Stmt) {
	saved := a.currentScope
	a.currentScope = NewScope(saved)
	for _, stmt := range stmts {
		a.analyzeStmt(stmt)
	}
	a.currentScope = saved
}

func (a *Analyzer) analyzeBlockInScope(stmts []ast.Stmt, scope *Scope) {
	saved := a.currentScope
	a.currentScope = scope
	for _, stmt := range stmts {
		a.analyzeStmt(stmt)
	}
	a.currentScope = saved
}

func (a *Analyzer) analyzeBlockWithRegionClone(stmts []ast.Stmt, scope *Scope) {
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedRegionRefs := a.currentRegionRefs
	savedPackedStores := a.currentPackedStores
	a.currentRegions = a.cloneRegionStates()
	a.currentRegionMarks = a.cloneRegionMarkStates()
	a.currentRegionRefs = a.cloneRegionRefStates()
	a.currentPackedStores = a.clonePackedStores()
	a.analyzeBlockInScope(stmts, scope)
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentPackedStores = savedPackedStores
}

func (a *Analyzer) cloneRegionStates() map[*Symbol]regionState {
	if a.currentRegions == nil {
		return nil
	}
	cloned := make(map[*Symbol]regionState, len(a.currentRegions))
	for sym, state := range a.currentRegions {
		cloned[sym] = state
	}
	return cloned
}

func (a *Analyzer) cloneRegionMarkStates() map[*Symbol]regionMarkState {
	if a.currentRegionMarks == nil {
		return nil
	}
	cloned := make(map[*Symbol]regionMarkState, len(a.currentRegionMarks))
	for sym, state := range a.currentRegionMarks {
		cloned[sym] = state
	}
	return cloned
}

func (a *Analyzer) cloneRegionRefStates() map[*Symbol]regionRefState {
	if a.currentRegionRefs == nil {
		return nil
	}
	cloned := make(map[*Symbol]regionRefState, len(a.currentRegionRefs))
	for sym, state := range a.currentRegionRefs {
		cloned[sym] = state
	}
	return cloned
}

func (a *Analyzer) clonePackedStores() map[string]*PackedEnumStoreType {
	if a.currentPackedStores == nil {
		return nil
	}
	cloned := make(map[string]*PackedEnumStoreType, len(a.currentPackedStores))
	for name, store := range a.currentPackedStores {
		cloned[name] = store
	}
	return cloned
}

func (a *Analyzer) lookupPackedStore(enumType *EnumType) (*PackedEnumStoreType, bool) {
	if a.currentPackedStores == nil || enumType == nil {
		return nil, false
	}
	store, ok := a.currentPackedStores[enumType.Name]
	if !ok || store == nil {
		return nil, false
	}
	return store, true
}

func (a *Analyzer) lookupRegionMark(name string) (*Symbol, regionMarkState) {
	if a.currentScope == nil {
		return nil, regionMarkState{}
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym.Kind != SymbolRegionMark {
		return nil, regionMarkState{}
	}
	state, ok := a.currentRegionMarks[sym]
	if !ok {
		return nil, regionMarkState{}
	}
	return sym, state
}

func (a *Analyzer) lookupRegionState(name string) (*Symbol, regionState) {
	if a.currentScope == nil {
		return nil, regionState{}
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym.Kind != SymbolRegion {
		return nil, regionState{}
	}
	state, ok := a.currentRegions[sym]
	if !ok {
		return nil, regionState{}
	}
	return sym, state
}

func (a *Analyzer) recordRegionRefBinding(sym *Symbol, value ast.Expr) {
	if a.currentRegionRefs == nil || sym == nil {
		return
	}
	if state, ok := a.regionRefStateForExpr(value); ok {
		a.currentRegionRefs[sym] = state
		return
	}
	delete(a.currentRegionRefs, sym)
}

func (a *Analyzer) recordRegionRefAssignment(target ast.Expr, value ast.Expr) {
	ident, ok := target.(*ast.Ident)
	if !ok || a.currentScope == nil {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return
	}
	a.recordRegionRefBinding(sym, value)
}

func (a *Analyzer) invalidateRegionRefs(region *Symbol, predicate func(regionRefState) bool, reason string) {
	if a.currentRegionRefs == nil || region == nil {
		return
	}
	for sym, state := range a.currentRegionRefs {
		if state.Region != region || !state.Valid {
			continue
		}
		if predicate != nil && !predicate(state) {
			continue
		}
		state.Valid = false
		state.InvalidatedBy = reason
		a.currentRegionRefs[sym] = state
	}
}

func (a *Analyzer) invalidateRegionMarks(region *Symbol, predicate func(regionMarkState) bool, reason string) {
	if a.currentRegionMarks == nil || region == nil {
		return
	}
	for sym, state := range a.currentRegionMarks {
		if state.Region != region || !state.Valid {
			continue
		}
		if predicate != nil && !predicate(state) {
			continue
		}
		state.Valid = false
		state.InvalidatedBy = reason
		a.currentRegionMarks[sym] = state
	}
}

func (a *Analyzer) refinedScopeForCondition(parent *Scope, cond ast.Expr, truthy bool) *Scope {
	scope := NewScope(parent)
	a.applyConditionRefinements(scope, cond, truthy)
	return scope
}

func (a *Analyzer) applyConditionRefinements(scope *Scope, expr ast.Expr, truthy bool) {
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			if truthy {
				a.applyConditionRefinements(scope, n.Left, true)
				a.applyConditionRefinements(scope, n.Right, true)
			}
		case lexer.TOKEN_OR:
			if !truthy {
				a.applyConditionRefinements(scope, n.Left, false)
				a.applyConditionRefinements(scope, n.Right, false)
			}
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
			targetExpr, state, ok := refinedExprNullState(n, truthy)
			if ok {
				a.shadowRefinedExpr(scope, targetExpr, state)
			}
		}
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			a.applyConditionRefinements(scope, n.Operand, !truthy)
		}
	case *ast.ParenExpr:
		a.applyConditionRefinements(scope, n.Inner, truthy)
	}
}

func refinedExprNullState(expr *ast.BinaryExpr, truthy bool) (ast.Expr, RefState, bool) {
	_, leftNull := expr.Left.(*ast.NullLit)
	_, rightNull := expr.Right.(*ast.NullLit)

	targetExpr := ast.Expr(nil)
	switch {
	case rightNull:
		targetExpr = expr.Left
	case leftNull:
		targetExpr = expr.Right
	default:
		return nil, RefStateNullable, false
	}

	if _, ok := exprRefinementKey(targetExpr); !ok {
		return nil, RefStateNullable, false
	}

	if expr.Op == lexer.TOKEN_EQEQ {
		if truthy {
			return targetExpr, RefStateNull, true
		}
		return targetExpr, RefStateNonNull, true
	}
	if truthy {
		return targetExpr, RefStateNonNull, true
	}
	return targetExpr, RefStateNull, true
}

func (a *Analyzer) shadowRefinedExpr(scope *Scope, expr ast.Expr, state RefState) {
	if scope == nil {
		return
	}
	key, ok := exprRefinementKey(expr)
	if !ok {
		return
	}
	baseType := a.analyzeExprInScope(expr, scope)
	ref, ok := baseType.(*RefType)
	if !ok {
		return
	}
	if !refinementCompatible(ref.State, state) {
		return
	}
	scope.Refinements[key] = cloneRefTypeWithState(ref, state)
}

func refinementCompatible(current, desired RefState) bool {
	switch desired {
	case RefStateNonNull:
		return current == RefStateNonNull || current == RefStateNullable
	case RefStateNull:
		return current == RefStateNull || current == RefStateNullable
	default:
		return true
	}
}

func exprRefinementKey(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return exprRefinementKey(n.Inner)
	case *ast.Ident:
		return n.Name, true
	case *ast.FieldExpr:
		base, ok := exprRefinementKey(n.Object)
		if !ok {
			return "", false
		}
		return base + "." + n.Field, true
	default:
		return "", false
	}
}

func (a *Analyzer) lookupRefinedExprType(expr ast.Expr) (Type, bool) {
	if a.currentScope == nil {
		return nil, false
	}
	key, ok := exprRefinementKey(expr)
	if !ok {
		return nil, false
	}
	return a.currentScope.LookupRefinement(key)
}

func (a *Analyzer) applyPostIfFallthroughRefinement(stmt *ast.IfStmt) {
	if a.currentScope == nil || len(stmt.Elifs) > 0 {
		return
	}
	if blockDefinitelyExits(stmt.Then) {
		a.applyConditionRefinements(a.currentScope, stmt.Cond, false)
	}
	if len(stmt.Else) > 0 && blockDefinitelyExits(stmt.Else) {
		a.applyConditionRefinements(a.currentScope, stmt.Cond, true)
	}
}

func blockDefinitelyExits(stmts []ast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	return stmtDefinitelyExits(stmts[len(stmts)-1])
}

func stmtDefinitelyExits(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt, *ast.PanicStmt, *ast.StaticErrorStmt:
		return true
	case *ast.ExprStmt:
		_, ok := n.Expr.(*ast.RaiseExpr)
		return ok
	case *ast.IfStmt:
		if !blockDefinitelyExits(n.Then) {
			return false
		}
		for _, elif := range n.Elifs {
			if !blockDefinitelyExits(elif.Body) {
				return false
			}
		}
		return len(n.Else) > 0 && blockDefinitelyExits(n.Else)
	case *ast.MatchStmt:
		if len(n.Arms) == 0 {
			return false
		}
		hasWildcard := false
		for _, arm := range n.Arms {
			if _, ok := arm.Pattern.(*ast.MatchWildcardPattern); ok {
				hasWildcard = true
			}
			if !blockDefinitelyExits(arm.Body) {
				return false
			}
		}
		return hasWildcard
	case *ast.StaticIfStmt:
		if !blockDefinitelyExits(n.Then) {
			return false
		}
		for _, elif := range n.Elifs {
			if !blockDefinitelyExits(elif.Body) {
				return false
			}
		}
		return len(n.Else) > 0 && blockDefinitelyExits(n.Else)
	default:
		return false
	}
}

func (a *Analyzer) recordAssignmentRefinement(target ast.Expr, targetType Type, valueType Type) {
	if a.currentScope == nil {
		return
	}
	key, ok := exprRefinementKey(target)
	if !ok {
		return
	}
	refined := assignedRefinementType(targetType, valueType)
	if refined == nil {
		delete(a.currentScope.Refinements, key)
		return
	}
	a.currentScope.Refinements[key] = refined
}

func assignedRefinementType(targetType Type, valueType Type) Type {
	targetRef, ok := targetType.(*RefType)
	if !ok {
		return nil
	}
	if IsNullType(valueType) {
		return cloneRefTypeWithState(targetRef, RefStateNull)
	}
	if valueRef, ok := valueType.(*RefType); ok {
		return cloneRefType(valueRef)
	}
	return targetRef
}

func assertedCondition(expr ast.Expr) (ast.Expr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident.Name != "assert" {
		return nil, false
	}
	return call.Args[0], true
}

func (a *Analyzer) analyzeExprInScope(expr ast.Expr, scope *Scope) Type {
	saved := a.currentScope
	a.currentScope = scope
	result := a.analyzeExpr(expr)
	a.currentScope = saved
	return result
}

func (a *Analyzer) analyzeCondExpr(expr ast.Expr) Type {
	return a.analyzeCondExprInScope(expr, a.currentScope)
}

func (a *Analyzer) analyzeCondExprInScope(expr ast.Expr, scope *Scope) Type {
	saved := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = saved }()

	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.analyzeCondExprInScope(n.Inner, scope)
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			operand := a.analyzeCondExprInScope(n.Operand, scope)
			if !IsBoolType(operand) {
				a.errorf(n.Pos(), "not operator requires bool operand")
			}
			return a.namedTypes["bool"]
		}
		return a.analyzeExpr(n)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			left := a.analyzeCondExprInScope(n.Left, scope)
			right := a.analyzeCondExprInScope(n.Right, a.refinedScopeForCondition(scope, n.Left, true))
			if !IsBoolType(left) || !IsBoolType(right) {
				a.errorf(n.Pos(), "logical operator requires bool operands")
			}
			return a.namedTypes["bool"]
		case lexer.TOKEN_OR:
			left := a.analyzeCondExprInScope(n.Left, scope)
			right := a.analyzeCondExprInScope(n.Right, a.refinedScopeForCondition(scope, n.Left, false))
			if !IsBoolType(left) || !IsBoolType(right) {
				a.errorf(n.Pos(), "logical operator requires bool operands")
			}
			return a.namedTypes["bool"]
		default:
			return a.analyzeExpr(n)
		}
	default:
		return a.analyzeExpr(expr)
	}
}
