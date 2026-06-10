package semantic

import (
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func resolveMatchableEnumType(actual Type) (*EnumType, *PackedVariantViewType, bool) {
	switch tt := actual.(type) {
	case *EnumType:
		if tt == nil {
			return nil, nil, false
		}
		return tt, nil, true
	case *PackedVariantViewType:
		if tt == nil || tt.Enum == nil {
			return nil, nil, false
		}
		return tt.Enum, tt, true
	default:
		return nil, nil, false
	}
}

func resolveMatchableConstEnumType(actual Type) (*ConstEnumType, bool) {
	constEnumType, ok := StripAggregateStateType(actual).(*ConstEnumType)
	return constEnumType, ok && constEnumType != nil
}

func resolveMatchableErrorSetType(actual Type) (*ErrorSetType, bool) {
	errorSetType, ok := StripAggregateStateType(actual).(*ErrorSetType)
	return errorSetType, ok && errorSetType != nil
}

func isStringMatchableType(actual Type) bool {
	if actual == nil {
		return false
	}
	literalType := &RefType{Elem: &BuiltinType{Name: "u8"}, State: RefStateNonNull, Storage: RefStorageStatic, ExplicitStorage: true}
	return runtimeStringComparable(actual, literalType)
}

func (a *Analyzer) validateMatchStore(pos lexer.Pos, valueExpr ast.Expr, actual Type, enumType *EnumType, storeExpr ast.Expr) {
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
		if a.canInferPackedStoreFromValue(valueExpr, actual, enumType) {
			return
		}
		if _, ok := a.lookupPackedStore(enumType); ok {
			return
		}
		a.errorf(pos, "packed enum match over %q requires an in %s clause", enumType.Name, packedEnumStoreTypeName(enumType.Name))
		return
	}
	storeType := a.analyzeExpr(storeExpr)
	packedStore, ok := storeType.(*PackedEnumStoreType)
	if !ok {
		a.errorf(storeExpr.Pos(), "packed enum match over %q requires store type %q, got %s", enumType.Name, packedEnumStoreTypeName(enumType.Name), storeType)
		return
	}
	if packedStore.Enum != enumType {
		a.errorf(storeExpr.Pos(), "packed enum match over %q requires store type %q, got %s", enumType.Name, packedEnumStoreTypeName(enumType.Name), storeType)
	}
}

func (a *Analyzer) validateTreeMatchStore(treeType *TreeCategoryType, storeExpr ast.Expr) {
	if treeType == nil || storeExpr == nil {
		return
	}
	a.errorf(storeExpr.Pos(), "tree match over %q does not take an in-store clause", treeType.Name)
}

// silentEnumMatchPatternVariant resolves a variant arm against a hierarchy scrutinee without
// emitting diagnostics (the erroring twin is resolveEnumMatchPatternCategory) — used by the
// reachability/coverage walk, which must not double-report.
func (a *Analyzer) silentEnumMatchPatternVariant(enumType *EnumType, pattern *ast.MatchVariantPattern) (*EnumVariant, bool) {
	owner := enumType
	if pattern.EnumName != enumType.Name {
		base, _, ok := a.lookupVisibleType(pattern.EnumName)
		if !ok {
			return nil, false
		}
		resolved, ok := StripAggregateStateType(base).(*EnumType)
		if !ok || resolved == nil || !enumDescendsFrom(resolved, enumType) {
			return nil, false
		}
		owner = resolved
	}
	variant, ok := owner.Variant(pattern.Variant)
	if !ok || variant == nil {
		return nil, false
	}
	return variant, true
}

func (a *Analyzer) matchPatternShadowedByPrevious(pattern ast.MatchPattern, expected Type, prior []ast.MatchPattern) bool {
	for _, prev := range prior {
		if a.matchPatternCovers(prev, pattern, expected) {
			return true
		}
	}
	return false
}

func (a *Analyzer) stringMatchPatternShadowedByPrevious(pattern ast.MatchPattern, prior []ast.MatchPattern) bool {
	for _, prev := range prior {
		switch p := prev.(type) {
		case *ast.MatchWildcardPattern:
			return true
		case *ast.MatchStringLiteralPattern:
			curr, ok := pattern.(*ast.MatchStringLiteralPattern)
			if ok && curr.Value == p.Value {
				return true
			}
		case *ast.MatchLiteralPattern:
			curr, ok := pattern.(*ast.MatchLiteralPattern)
			if ok && a.matchLiteralPatternEquals(p.Value, curr.Value) {
				return true
			}
		}
	}
	return false
}

func (a *Analyzer) matchPatternCovers(prev ast.MatchPattern, current ast.MatchPattern, expected Type) bool {
	switch p := prev.(type) {
	case *ast.MatchWildcardPattern:
		return true
	case *ast.MatchBindPattern:
		// docs/77 §2: on a hierarchy scrutinee a bind arm naming a sub-category covers exactly
		// that category's leaf range, not everything. A plain (non-category) bind stays catch-all.
		if enumType, ok := StripAggregateStateType(expected).(*EnumType); ok && enumIsHierarchical(enumType) {
			if prevCategory, ok := a.resolveEnumCategoryArm(enumType, p.Name); ok {
				switch curr := current.(type) {
				case *ast.MatchVariantPattern:
					variant, ok := a.silentEnumMatchPatternVariant(enumType, curr)
					if !ok {
						return false
					}
					return variant.Tag-prevCategory.LeafTagLo < prevCategory.LeafTagCount
				case *ast.MatchBindPattern:
					currCategory, ok := a.resolveEnumCategoryArm(enumType, curr.Name)
					if !ok {
						// a plain catch-all bind is only covered if the category spans the scrutinee
						return enumDescendsFrom(enumType, prevCategory)
					}
					return enumDescendsFrom(currCategory, prevCategory)
				case *ast.MatchWildcardPattern:
					return enumDescendsFrom(enumType, prevCategory)
				default:
					return false
				}
			}
		}
		return true
	case *ast.MatchStringLiteralPattern:
		switch curr := current.(type) {
		case *ast.MatchStringLiteralPattern:
			return curr.Value == p.Value
		case *ast.MatchLiteralPattern:
			return a.matchLiteralPatternEquals(&ast.StringLit{Position: p.Position, Value: p.Value}, curr.Value)
		default:
			return false
		}
	case *ast.MatchLiteralPattern:
		switch curr := current.(type) {
		case *ast.MatchLiteralPattern:
			return a.matchLiteralPatternEquals(p.Value, curr.Value)
		case *ast.MatchStringLiteralPattern:
			return a.matchLiteralPatternEquals(p.Value, &ast.StringLit{Position: curr.Position, Value: curr.Value})
		default:
			return false
		}
	case *ast.MatchOrPattern:
		for _, option := range p.Options {
			if a.matchPatternCovers(option, current, expected) {
				return true
			}
		}
		return false
	case *ast.MatchTuplePattern:
		currTuple, ok := current.(*ast.MatchTuplePattern)
		if !ok {
			return false
		}
		tupleType, ok := StripAggregateStateType(expected).(*TupleType)
		if !ok || tupleType == nil {
			return false
		}
		if len(p.Elems) != len(tupleType.Fields) || len(currTuple.Elems) != len(tupleType.Fields) {
			return false
		}
		for i := range tupleType.Fields {
			if !a.matchPatternCovers(p.Elems[i], currTuple.Elems[i], tupleType.Fields[i].Type) {
				return false
			}
		}
		return true
	case *ast.MatchListPattern:
		currList, ok := current.(*ast.MatchListPattern)
		if !ok || len(p.Elems) != len(currList.Elems) {
			return false
		}
		if matchListPatternHasRest(p) || matchListPatternHasRest(currList) {
			return false
		}
		elemType, ok := SequenceMatchElementType(expected)
		if !ok {
			return false
		}
		for i := range p.Elems {
			if !a.matchPatternCovers(p.Elems[i], currList.Elems[i], elemType) {
				return false
			}
		}
		return true
	case *ast.MatchVariantPattern:
		currVariant, ok := current.(*ast.MatchVariantPattern)
		if !ok {
			return false
		}
		switch variantBase := expected.(type) {
		case *EnumType:
			if variantBase == nil || p.EnumName != variantBase.Name || currVariant.EnumName != variantBase.Name || p.Variant != currVariant.Variant {
				return false
			}
			variant, ok := variantBase.Variant(p.Variant)
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
		case *ConstEnumType:
			if variantBase == nil || p.EnumName != variantBase.Name || currVariant.EnumName != variantBase.Name || p.Variant != currVariant.Variant {
				return false
			}
			return true
		case *ErrorSetType:
			if variantBase == nil || p.EnumName != variantBase.Name || currVariant.EnumName != variantBase.Name || p.Variant != currVariant.Variant {
				return false
			}
			return true
		case *TreeCategoryType:
			if variantBase == nil || p.EnumName != currVariant.EnumName || p.Variant != currVariant.Variant {
				return false
			}
			patternCategory, variant, ok := a.resolveTreeMatchPatternCategory(variantBase, p)
			if !ok {
				return false
			}
			currCategory, currConcrete, ok := a.resolveTreeMatchPatternCategory(variantBase, currVariant)
			if !ok || currCategory != patternCategory || currConcrete != variant {
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
	case *ast.MatchStructPattern:
		currStruct, ok := current.(*ast.MatchStructPattern)
		if !ok {
			return false
		}
		fields, ok := a.resolvedStructFields(expected)
		if !ok {
			return false
		}
		if !structPatternMatchesType(p, expected) || !structPatternMatchesType(currStruct, expected) {
			return false
		}
		prevArgs, ok := orderedStructMatchPatternArgs(p, fields)
		if !ok {
			return false
		}
		currArgs, ok := orderedStructMatchPatternArgs(currStruct, fields)
		if !ok {
			return false
		}
		for i := range prevArgs {
			if prevArgs[i] == nil {
				continue
			}
			if currArgs[i] == nil {
				return false
			}
			if !a.matchPatternCovers(prevArgs[i].Pattern, currArgs[i].Pattern, fields[i].Type) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func structPatternMatchesType(pattern *ast.MatchStructPattern, expected Type) bool {
	if pattern == nil {
		return false
	}
	switch tt := StripAggregateStateType(expected).(type) {
	case *StructType:
		return tt != nil && (pattern.TypeName == "" || tt.Name == pattern.TypeName) && tt.Decl != nil
	case *GenericInstanceType:
		base, _ := tt.Base.(*StructType)
		return base != nil && (pattern.TypeName == "" || base.Name == pattern.TypeName) && base.Decl != nil
	case *TreeBlockType:
		return tt != nil && (pattern.TypeName == "" || tt.Name == pattern.TypeName)
	case *TreeStructType:
		return tt != nil && (pattern.TypeName == "" || tt.Name == pattern.TypeName)
	default:
		return false
	}
}

func matchListPatternHasRest(pattern *ast.MatchListPattern) bool {
	if pattern == nil {
		return false
	}
	for _, elem := range pattern.Elems {
		if _, ok := elem.(*ast.MatchRestPattern); ok {
			return true
		}
	}
	return false
}

func orderedStructMatchPatternArgs(pattern *ast.MatchStructPattern, fields []moveBindResolvedField) ([]*ast.MatchPatternArg, bool) {
	if pattern == nil {
		return nil, false
	}
	ordered := make([]*ast.MatchPatternArg, len(fields))
	if len(pattern.Args) == 0 {
		return ordered, true
	}
	fieldIndexes := make(map[string]int, len(fields))
	for i := range fields {
		fieldIndexes[fields[i].Name] = i
	}
	seen := make([]bool, len(fields))
	for i := range pattern.Args {
		arg := &pattern.Args[i]
		if arg.Name == "" {
			return nil, false
		}
		index, ok := fieldIndexes[arg.Name]
		if !ok || seen[index] {
			return nil, false
		}
		seen[index] = true
		ordered[index] = arg
	}
	return ordered, true
}

func (a *Analyzer) matchLiteralPatternEquals(left ast.Expr, right ast.Expr) bool {
	if a == nil || left == nil || right == nil {
		return false
	}
	leftValue, leftOK := a.evalConstExpr(left)
	rightValue, rightOK := a.evalConstExpr(right)
	if leftOK && rightOK {
		equal, ok := a.evalConstEquality(leftValue, rightValue, true)
		return ok && equal.Kind == ConstBool && equal.Bool
	}
	_, leftNull := left.(*ast.NullLit)
	_, rightNull := right.(*ast.NullLit)
	return leftNull && rightNull
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
		if p.Binder != "" {
			return p.Name + " " + p.Binder
		}
		return p.Name
	case *ast.MatchStringLiteralPattern:
		return p.Value
	case *ast.MatchLiteralPattern:
		return matchLiteralPatternSummary(p.Value)
	case *ast.MatchTuplePattern:
		parts := make([]string, 0, len(p.Elems))
		for _, elem := range p.Elems {
			parts = append(parts, matchPatternSummary(elem))
		}
		return strings.Join(parts, ", ")
	case *ast.MatchListPattern:
		parts := make([]string, 0, len(p.Elems))
		for _, elem := range p.Elems {
			parts = append(parts, matchPatternSummary(elem))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ast.MatchOrPattern:
		parts := make([]string, 0, len(p.Options))
		for _, option := range p.Options {
			parts = append(parts, matchPatternSummary(option))
		}
		return strings.Join(parts, " | ")
	case *ast.MatchRestPattern:
		return "..."
	case *ast.MatchStructPattern:
		parts := make([]string, 0, len(p.Args))
		for _, arg := range p.Args {
			part := matchPatternSummary(arg.Pattern)
			if arg.Name != "" {
				part = arg.Name + ": " + part
			}
			parts = append(parts, part)
		}
		if len(parts) == 0 {
			return p.TypeName + "()"
		}
		return p.TypeName + "(" + strings.Join(parts, ", ") + ")"
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

func matchLiteralPatternSummary(expr ast.Expr) string {
	if expr == nil {
		return "<literal>"
	}
	switch n := expr.(type) {
	case *ast.IntLit:
		if n.Suffix != "" {
			return n.Value + n.Suffix
		}
		return n.Value
	case *ast.FloatLit:
		if n.Suffix != "" {
			return n.Value + n.Suffix
		}
		return n.Value
	case *ast.StringLit:
		return strconv.Quote(n.Value)
	case *ast.CharLit:
		return n.Value
	case *ast.BoolLit:
		if n.Value {
			return "true"
		}
		return "false"
	case *ast.NullLit:
		return "null"
	case *ast.UnaryExpr:
		return lexer.TokenName(n.Op) + matchLiteralPatternSummary(n.Operand)
	case *ast.ParenExpr:
		return "(" + matchLiteralPatternSummary(n.Inner) + ")"
	default:
		return "<literal>"
	}
}
