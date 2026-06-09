package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) collectTreeAttributes(decls []scopedDecl) {
	for _, scoped := range decls {
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			decl, ok := scoped.Decl.(*ast.AttributeDecl)
			if !ok || decl == nil {
				return
			}
			receiver, ok := a.resolveTreeAttributeReceiverType(decl.Receiver, decl.Pos())
			if !ok {
				return
			}
			returnType := a.resolveType(decl.ReturnType)
			if IsInvalidType(returnType) {
				return
			}
			key := TypeIdentityKey(receiver)
			attrs := a.treeAttributes[key]
			if attrs == nil {
				attrs = map[string]*TreeAttribute{}
				a.treeAttributes[key] = attrs
			}
			if existing := attrs[decl.Name]; existing != nil {
				a.errorf(decl.Pos(), "duplicate attribute %q on %s", decl.Name, receiver)
				return
			}
			attrs[decl.Name] = &TreeAttribute{Name: decl.Name, Receiver: receiver, ReturnType: returnType, Decl: decl}
		})
	}
}

func (a *Analyzer) analyzeAttributeDecl(decl *ast.AttributeDecl) {
	if decl == nil {
		return
	}
	receiver, ok := a.resolveTreeAttributeReceiverType(decl.Receiver, decl.Pos())
	if !ok {
		return
	}
	root, ok := a.resolveVisitRootInfo(receiver, decl.Receiver, decl.Pos())
	if !ok {
		return
	}
	attribute, ok := a.lookupRegisteredTreeAttribute(receiver, decl.Name)
	if !ok || attribute == nil {
		return
	}
	resultType := attribute.ReturnType
	covered := map[string]bool{}
	hasWildcard := false
	armTypes := make([]Type, 0, len(decl.Arms))
	savedReturn := a.currentReturn
	savedReturnProv := a.currentReturnProvenance
	savedReturnBorrowed := a.currentReturnBorrowedOwnerRefs
	a.currentReturn = resultType
	a.currentReturnProvenance = regionRefState{}
	a.currentReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	defer func() {
		a.currentReturn = savedReturn
		a.currentReturnProvenance = savedReturnProv
		a.currentReturnBorrowedOwnerRefs = savedReturnBorrowed
	}()
	var mergedType Type
	for _, arm := range decl.Arms {
		armInfo, ok := a.resolveVisitArmInfo(root, arm)
		if !ok {
			continue
		}
		if armInfo.Arm.Wildcard {
			hasWildcard = true
		} else if armInfo.Key != "" {
			covered[armInfo.Key] = true
		}
		armScope := NewScope(a.currentScope)
		if childViewType := a.treeAttributeImplicitChildrenType(receiver, armInfo.BindType); childViewType != nil {
			a.defineLocalInScope(armScope, &Symbol{Name: "children", Kind: SymbolLocal, Type: childViewType, Mutable: false}, armInfo.Arm.Position)
		}
		armType, _, _ := a.analyzeVisitArmBody(armInfo, resultType, armScope, true, "attribute", true, nil)
		armTypes = append(armTypes, armType)
		if mergedType == nil {
			mergedType = armType
		} else if next := MergeTypes(mergedType, armType); !IsInvalidType(next) {
			mergedType = next
		} else {
			a.errorf(arm.Position, "attribute arms are incompatible: %s and %s", mergedType, armType)
			mergedType = invalidType
		}
	}
	a.reportNonExhaustiveVisit(decl.Pos(), root, covered, hasWildcard, "attribute")
	if len(armTypes) != 0 && !AssignableTo(resultType, mergedType) {
		a.errorf(decl.Pos(), "attribute %q expects %s, got %s", decl.Name, resultType, mergedType)
	}
}

func (a *Analyzer) resolveTreeAttributeReceiverType(expr ast.TypeExpr, pos lexer.Pos) (Type, bool) {
	receiver := a.resolveType(expr)
	if IsInvalidType(receiver) {
		return nil, false
	}
	switch StripAggregateStateType(receiver).(type) {
	case *TreeNodeType, *TreeCategoryType, *TreeVariantViewType, *TreeBlockType, *TreeStructType:
		return StripAggregateStateType(receiver), true
	default:
		a.errorf(pos, "attribute receiver expects a tree category, tree variant, tree member, or Family.Node type, got %s", receiver)
		return nil, false
	}
}

func (a *Analyzer) lookupRegisteredTreeAttribute(receiver Type, name string) (*TreeAttribute, bool) {
	if receiver == nil || name == "" {
		return nil, false
	}
	attrs := a.treeAttributes[TypeIdentityKey(receiver)]
	if attrs == nil {
		return nil, false
	}
	attr := attrs[name]
	return attr, attr != nil
}

func (a *Analyzer) lookupTreeAttribute(objType Type, fieldName string) (*TreeAttribute, bool) {
	if fieldName == "" {
		return nil, false
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			return nil, false
		}
		objType = ref.Elem
	}
	objType = StripAggregateStateType(objType)
	for _, receiver := range treeAttributeLookupReceivers(objType) {
		if attr, ok := a.lookupRegisteredTreeAttribute(receiver, fieldName); ok {
			return attr, true
		}
	}
	return nil, false
}

func treeAttributeProjectionSourceType(sourceType Type) (Type, Type, bool) {
	if sourceType == nil {
		return nil, nil, false
	}
	switch tt := sourceType.(type) {
	case *ArrayType:
		if tt == nil || tt.Elem == nil {
			return nil, nil, false
		}
		return sourceType, tt.Elem, true
	case *DArrayType:
		if tt == nil || tt.Elem == nil {
			return nil, nil, false
		}
		return sourceType, tt.Elem, true
	case *DArrayViewType:
		if tt == nil || tt.Elem == nil || (tt.SurfaceName != "" && tt.SurfaceName != "view") {
			return nil, nil, false
		}
		return sourceType, tt.Elem, true
	case *GenericInstanceType:
		if itemType, ok := TreeChildrenItemType(tt); ok && itemType != nil {
			return sourceType, itemType, true
		}
		if itemType, ok := TreeAttributeSequenceItemType(tt); ok && itemType != nil {
			return sourceType, itemType, true
		}
		return nil, nil, false
	case *RefType:
		if tt == nil || tt.State != RefStateNonNull {
			return nil, nil, false
		}
		_, itemType, ok := treeAttributeProjectionSourceType(tt.Elem)
		if !ok {
			return nil, nil, false
		}
		return sourceType, itemType, true
	default:
		return nil, nil, false
	}
}

func (a *Analyzer) lookupProjectedTreeAttributeSequence(objType Type, fieldName string) (Type, *TreeAttribute, bool) {
	if a == nil || objType == nil || fieldName == "" {
		return nil, nil, false
	}
	sourceType, itemType, ok := treeAttributeProjectionSourceType(objType)
	if !ok || itemType == nil {
		return nil, nil, false
	}
	attr, ok := a.lookupTreeAttribute(itemType, fieldName)
	if !ok || attr == nil {
		return nil, nil, false
	}
	base, ok := a.namedTypes["TreeAttributeSeq"].(*StructType)
	if !ok || base == nil {
		return invalidType, attr, true
	}
	return &GenericInstanceType{Name: "TreeAttributeSeq", Base: base, Args: []Type{sourceType, attr.ReturnType}}, attr, true
}

func treeAttributeLookupReceivers(objType Type) []Type {
	switch tt := objType.(type) {
	case *TreeVariantViewType:
		if tt == nil || tt.Category == nil {
			return nil
		}
		out := []Type{tt, tt.Category}
		if tt.Category.Family != nil && tt.Category.Family.NodeType != nil {
			out = append(out, tt.Category.Family.NodeType)
		}
		return out
	case *TreeCategoryType:
		if tt == nil {
			return nil
		}
		out := []Type{tt}
		if tt.Family != nil && tt.Family.NodeType != nil {
			out = append(out, tt.Family.NodeType)
		}
		return out
	case *TreeBlockType:
		if tt == nil {
			return nil
		}
		out := []Type{tt}
		if tt.Family != nil && tt.Family.NodeType != nil {
			out = append(out, tt.Family.NodeType)
		}
		return out
	case *TreeStructType:
		if tt == nil {
			return nil
		}
		out := []Type{tt}
		if tt.Family != nil && tt.Family.NodeType != nil {
			out = append(out, tt.Family.NodeType)
		}
		return out
	case *TreeNodeType:
		if tt == nil {
			return nil
		}
		return []Type{tt}
	default:
		return nil
	}
}

func (a *Analyzer) treeAttributeImplicitChildrenType(receiver Type, bindType Type) Type {
	if a == nil {
		return nil
	}
	if receiver == nil || bindType == nil {
		return nil
	}
	var candidates []Type
	appendTreeStructuralChildCandidates(&candidates, bindType)
	if len(candidates) == 0 {
		return nil
	}
	for _, candidate := range candidates {
		if candidate == nil || !AssignableTo(receiver, candidate) {
			return nil
		}
	}
	base, ok := a.namedTypes["TreeChildren"].(*StructType)
	if !ok || base == nil {
		return invalidType
	}
	return &GenericInstanceType{Name: "TreeChildren", Base: base, Args: []Type{bindType, receiver}}
}
