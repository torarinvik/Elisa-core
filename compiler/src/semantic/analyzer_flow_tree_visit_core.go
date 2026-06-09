package semantic

import (
	"sort"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type treeVisitRootKind int

const (
	treeVisitRootKindCategory treeVisitRootKind = iota
	treeVisitRootKindExact
	treeVisitRootKindFamily
)

type treeVisitRootInfo struct {
	Kind     treeVisitRootKind
	Family   *TreeType
	Category *TreeCategoryType
	Exact    Type
}

type treeVisitArmInfo struct {
	Arm      ast.VisitArm
	Key      string
	BindType Type
	Category *TreeCategoryType
	Variant  *EnumVariant
	Exact    Type
}

func resolveTreeVisitSourceType(actual Type) (Type, *TreeType, bool) {
	actual = StripAggregateStateType(actual)
	switch tt := actual.(type) {
	case *TreeCategoryType:
		if tt == nil || tt.Family == nil {
			return nil, nil, false
		}
		return tt, tt.Family, true
	case *TreeVariantViewType:
		if tt == nil || tt.Category == nil || tt.Category.Family == nil {
			return nil, nil, false
		}
		return tt, tt.Category.Family, true
	case *TreeBlockType:
		if tt == nil || tt.Family == nil {
			return nil, nil, false
		}
		return tt, tt.Family, true
	case *TreeStructType:
		if tt == nil || tt.Family == nil {
			return nil, nil, false
		}
		return tt, tt.Family, true
	case *TreeNodeType:
		if tt == nil || tt.Family == nil {
			return nil, nil, false
		}
		return tt, tt.Family, true
	default:
		return nil, nil, false
	}
}

func visitDomainKeys(root treeVisitRootInfo) []string {
	switch root.Kind {
	case treeVisitRootKindCategory:
		if root.Category == nil {
			return nil
		}
		keys := make([]string, 0, len(root.Category.Variants))
		for _, variant := range root.Category.Variants {
			keys = append(keys, root.Category.Name+"."+variant.Name)
		}
		return keys
	case treeVisitRootKindExact:
		if root.Exact == nil {
			return nil
		}
		return []string{root.Exact.String()}
	case treeVisitRootKindFamily:
		if root.Family == nil {
			return nil
		}
		keys := make([]string, 0)
		memberNames := make([]string, 0, len(root.Family.MemberTypes))
		for name := range root.Family.MemberTypes {
			if name == "Node" || name == "Store" {
				continue
			}
			memberNames = append(memberNames, name)
		}
		sort.Strings(memberNames)
		for _, name := range memberNames {
			member := root.Family.MemberTypes[name]
			if category, ok := member.(*TreeCategoryType); ok {
				for _, variant := range category.Variants {
					keys = append(keys, category.Name+"."+variant.Name)
				}
			} else {
				keys = append(keys, member.String())
			}
		}
		return keys
	default:
		return nil
	}
}

func (a *Analyzer) reportNonExhaustiveVisit(pos lexer.Pos, root treeVisitRootInfo, covered map[string]bool, hasWildcard bool, keyword string) {
	if hasWildcard {
		return
	}
	if keyword == "" {
		keyword = "visit"
	}
	missing := make([]string, 0)
	for _, key := range visitDomainKeys(root) {
		if !covered[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	label := "visit"
	switch root.Kind {
	case treeVisitRootKindCategory:
		if root.Category != nil {
			label = root.Category.String()
		}
	case treeVisitRootKindExact:
		if root.Exact != nil {
			label = root.Exact.String()
		}
	case treeVisitRootKindFamily:
		if root.Family != nil && root.Family.NodeType != nil {
			label = root.Family.NodeType.String()
		}
	}
	a.errorf(pos, "non-exhaustive %s over %s; missing %s", keyword, label, strings.Join(missing, ", "))
}

func (a *Analyzer) resolveVisitRootInfo(valueType Type, rootExpr ast.TypeExpr, pos lexer.Pos) (treeVisitRootInfo, bool) {
	sourceMember, sourceFamily, ok := resolveTreeVisitSourceType(valueType)
	if !ok || sourceFamily == nil {
		a.errorf(pos, "visit/fold/rewrite expects a tree node source, got %s", valueType)
		return treeVisitRootInfo{}, false
	}
	if rootExpr == nil {
		if category, _, ok := resolveMatchableTreeCategoryType(valueType); ok {
			return treeVisitRootInfo{Kind: treeVisitRootKindCategory, Family: category.Family, Category: category}, true
		}
		switch tt := sourceMember.(type) {
		case *TreeBlockType:
			return treeVisitRootInfo{Kind: treeVisitRootKindExact, Family: tt.Family, Exact: tt}, true
		case *TreeStructType:
			return treeVisitRootInfo{Kind: treeVisitRootKindExact, Family: tt.Family, Exact: tt}, true
		default:
			a.errorf(pos, "visit/fold requires an explicit `as Family.Node` root for %s", valueType)
			return treeVisitRootInfo{}, false
		}
	}
	rootType := a.resolveType(rootExpr)
	switch tt := StripAggregateStateType(rootType).(type) {
	case *TreeNodeType:
		if tt == nil || tt.Family == nil {
			break
		}
		if sourceFamily != tt.Family {
			a.errorf(rootExpr.Pos(), "visit/fold root %s does not match source family %s", tt, sourceFamily.Name)
			return treeVisitRootInfo{}, false
		}
		return treeVisitRootInfo{Kind: treeVisitRootKindFamily, Family: tt.Family}, true
	case *TreeCategoryType:
		category, _, matchable := resolveMatchableTreeCategoryType(valueType)
		if !matchable || category != tt {
			a.errorf(rootExpr.Pos(), "visit/fold root %s requires a %s source, got %s", tt, tt, valueType)
			return treeVisitRootInfo{}, false
		}
		return treeVisitRootInfo{Kind: treeVisitRootKindCategory, Family: tt.Family, Category: tt}, true
	case *TreeBlockType:
		if !SameType(sourceMember, tt) {
			a.errorf(rootExpr.Pos(), "visit/fold root %s requires a %s source, got %s", tt, tt, valueType)
			return treeVisitRootInfo{}, false
		}
		return treeVisitRootInfo{Kind: treeVisitRootKindExact, Family: tt.Family, Exact: tt}, true
	case *TreeStructType:
		if !SameType(sourceMember, tt) {
			a.errorf(rootExpr.Pos(), "visit/fold root %s requires a %s source, got %s", tt, tt, valueType)
			return treeVisitRootInfo{}, false
		}
		return treeVisitRootInfo{Kind: treeVisitRootKindExact, Family: tt.Family, Exact: tt}, true
	}
	a.errorf(rootExpr.Pos(), "visit/fold root expects a tree category, tree member, or Family.Node type, got %s", rootType)
	return treeVisitRootInfo{}, false
}

func (a *Analyzer) resolveVisitArmInfo(root treeVisitRootInfo, arm ast.VisitArm) (treeVisitArmInfo, bool) {
	if arm.Wildcard {
		return treeVisitArmInfo{Arm: arm}, true
	}
	namedTarget := &ast.NamedType{Position: arm.Position, Name: arm.TargetName}
	if category, variant, ok := a.treeVariantTargetFromNamedType(namedTarget); ok {
		switch root.Kind {
		case treeVisitRootKindCategory:
			if category != root.Category {
				a.errorf(arm.Position, "visit arm %q is outside the %s visit domain", arm.TargetName, root.Category)
				return treeVisitArmInfo{}, false
			}
		case treeVisitRootKindFamily:
			if category == nil || category.Family != root.Family {
				a.errorf(arm.Position, "visit arm %q is outside the %s visit domain", arm.TargetName, root.Family.NodeType)
				return treeVisitArmInfo{}, false
			}
		default:
			a.errorf(arm.Position, "visit arm %q cannot appear when visiting exact member %s", arm.TargetName, root.Exact)
			return treeVisitArmInfo{}, false
		}
		viewType := category.VariantViewType(variant)
		return treeVisitArmInfo{Arm: arm, Key: viewType.String(), BindType: viewType, Category: category, Variant: variant}, true
	}
	resolved, _, ok := a.lookupVisibleType(arm.TargetName)
	if !ok {
		a.errorf(arm.Position, "unknown visit arm target %q", arm.TargetName)
		return treeVisitArmInfo{}, false
	}
	switch tt := resolved.(type) {
	case *TreeBlockType:
		switch root.Kind {
		case treeVisitRootKindFamily:
			if tt.Family != root.Family {
				a.errorf(arm.Position, "visit arm %q is outside the %s visit domain", arm.TargetName, root.Family.NodeType)
				return treeVisitArmInfo{}, false
			}
		case treeVisitRootKindExact:
			if !SameType(tt, root.Exact) {
				a.errorf(arm.Position, "visit arm %q is outside the %s visit domain", arm.TargetName, root.Exact)
				return treeVisitArmInfo{}, false
			}
		default:
			a.errorf(arm.Position, "visit arm %q cannot appear in a %s visit", arm.TargetName, root.Category)
			return treeVisitArmInfo{}, false
		}
		return treeVisitArmInfo{Arm: arm, Key: tt.String(), BindType: tt, Exact: tt}, true
	case *TreeStructType:
		switch root.Kind {
		case treeVisitRootKindFamily:
			if tt.Family != root.Family {
				a.errorf(arm.Position, "visit arm %q is outside the %s visit domain", arm.TargetName, root.Family.NodeType)
				return treeVisitArmInfo{}, false
			}
		case treeVisitRootKindExact:
			if !SameType(tt, root.Exact) {
				a.errorf(arm.Position, "visit arm %q is outside the %s visit domain", arm.TargetName, root.Exact)
				return treeVisitArmInfo{}, false
			}
		default:
			a.errorf(arm.Position, "visit arm %q cannot appear in a %s visit", arm.TargetName, root.Category)
			return treeVisitArmInfo{}, false
		}
		return treeVisitArmInfo{Arm: arm, Key: tt.String(), BindType: tt, Exact: tt}, true
	case *TreeCategoryType:
		a.errorf(arm.Position, "visit arm %q must name a concrete tree variant or exact tree member", arm.TargetName)
		return treeVisitArmInfo{}, false
	default:
		a.errorf(arm.Position, "visit arm %q is not a tree visit target", arm.TargetName)
		return treeVisitArmInfo{}, false
	}
}

func (a *Analyzer) analyzeVisitArmBody(armInfo treeVisitArmInfo, resultType Type, scope *Scope, forFold bool, foldKeyword string, forRewrite bool, childResultsElemType Type) (Type, affineFlowSnapshot, bool) {
	savedRewriteDefault := a.currentRewriteDefault
	if forRewrite {
		ctx := &rewriteDefaultContext{Message: "default is only allowed inside an exact tree rewrite arm"}
		if _, exact := TreeExactTag(armInfo.BindType); exact {
			ctx.Allowed = true
			ctx.ExactType = armInfo.BindType
			ctx.ResultType = TreeRewriteResultTypeForValue(armInfo.BindType)
		}
		a.currentRewriteDefault = ctx
		defer func() {
			a.currentRewriteDefault = savedRewriteDefault
		}()
	}
	if armInfo.Arm.BindName != "" && armInfo.BindType != nil {
		a.defineLocalInScope(scope, &Symbol{Name: armInfo.Arm.BindName, Kind: SymbolLocal, Type: armInfo.BindType, Mutable: false}, armInfo.Arm.Position)
	}
	if forFold && armInfo.Arm.ChildResultsName != "" && resultType != nil {
		elemType := resultType
		if forRewrite && childResultsElemType != nil {
			elemType = childResultsElemType
		}
		childViewType := &ViewType{Elem: elemType, SurfaceName: "view"}
		a.defineLocalInScope(scope, &Symbol{Name: armInfo.Arm.ChildResultsName, Kind: SymbolLocal, Type: childViewType, Mutable: false}, armInfo.Arm.Position)
	}
	if len(armInfo.Arm.ChildBindings) != 0 {
		if !forFold {
			a.errorf(armInfo.Arm.Position, "visit arm %q cannot bind fold child results", armInfo.Arm.TargetName)
		} else {
			if foldKeyword == "" {
				foldKeyword = "fold"
			}
			bindingTypes := treeFoldArmChildBindingTypes(armInfo.BindType, resultType)
			if forRewrite {
				bindingTypes = treeRewriteArmChildBindingTypes(armInfo.BindType)
			}
			seenFields := map[string]bool{}
			for _, binding := range armInfo.Arm.ChildBindings {
				if binding.FieldName == "" || binding.BindName == "" {
					continue
				}
				if seenFields[binding.FieldName] {
					a.errorf(binding.Position, "%s arm %q binds child result %q more than once", foldKeyword, armInfo.Arm.TargetName, binding.FieldName)
					continue
				}
				seenFields[binding.FieldName] = true
				bindingType, ok := bindingTypes[binding.FieldName]
				if !ok {
					a.errorf(binding.Position, "%s arm %q has no structural child result named %q", foldKeyword, armInfo.Arm.TargetName, binding.FieldName)
					continue
				}
				a.defineLocalInScope(scope, &Symbol{Name: binding.BindName, Kind: SymbolLocal, Type: bindingType, Mutable: false}, binding.Position)
			}
		}
	}
	bodyScope := scope
	guardFallthrough := affineFlowSnapshot{}
	hasGuard := armInfo.Arm.Guard != nil
	if hasGuard {
		guardType, guardSnapshot := a.analyzeExprInAffineScope(armInfo.Arm.Guard, scope)
		if !IsBoolType(guardType) {
			a.errorf(armInfo.Arm.Guard.Pos(), "visit arm guard must be bool, got %s", guardType)
		}
		guardFallthrough = guardSnapshot
		bodyScope = a.refinedScopeForCondition(scope, armInfo.Arm.Guard, true)
	}
	bodyType, bodySnapshot := a.analyzeMatchExprArmBodyWithAffineSnapshot(armInfo.Arm.Body, bodyScope)
	canFallthrough := hasGuard || !blockDefinitelyExits(armInfo.Arm.Body)
	if hasGuard {
		if blockDefinitelyExits(armInfo.Arm.Body) {
			return bodyType, guardFallthrough, true
		}
		bodySnapshot.Affine = mergeAffineValueStates(bodySnapshot.Affine, guardFallthrough.Affine)
		bodySnapshot.BorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(bodySnapshot.BorrowedOwnerRefs, guardFallthrough.BorrowedOwnerRefs)
		bodySnapshot.FunctionValues = a.mergeFunctionValueBindings(bodySnapshot.FunctionValues, guardFallthrough.FunctionValues)
		bodySnapshot.SpecializedValueTypes = a.mergeSpecializedValueTypeBindings(bodySnapshot.SpecializedValueTypes, guardFallthrough.SpecializedValueTypes)
	}
	return bodyType, bodySnapshot, canFallthrough
}

func treeRewriteArmChildBindingTypes(bindType Type) map[string]Type {
	if bindType == nil {
		return nil
	}
	out := make(map[string]Type)
	for _, binding := range TreeStructuralChildBindings(bindType) {
		if binding.Name == "" {
			continue
		}
		if bindingType, ok := TreeRewriteChildBindingType(binding.Type, binding.Relation); ok {
			out[binding.Name] = bindingType
		}
	}
	return out
}

func treeFoldArmChildBindingTypes(bindType Type, resultType Type) map[string]Type {
	if bindType == nil || resultType == nil {
		return nil
	}
	childViewType := &ViewType{Elem: resultType, SurfaceName: "view"}
	out := make(map[string]Type)
	for _, binding := range TreeStructuralChildBindings(bindType) {
		if binding.Name == "" {
			continue
		}
		switch binding.Relation {
		case ast.EnumPayloadRelationChild:
			if _, optional := UnwrapOptionalType(binding.Type); optional {
				out[binding.Name] = OptionalTreeFoldChildBindingType(resultType)
			} else {
				out[binding.Name] = resultType
			}
		case ast.EnumPayloadRelationChildren:
			if _, optional := UnwrapOptionalType(binding.Type); optional {
				out[binding.Name] = &OptionalType{Value: childViewType}
			} else {
				out[binding.Name] = childViewType
			}
		}
	}
	return out
}
