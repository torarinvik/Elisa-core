package semantic

import (
	"strings"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (a *Analyzer) populateTreeMembers(decls []scopedDecl) {
	for _, scoped := range decls {
		treeDecl, ok := scoped.Decl.(*ast.TreeDecl)
		if !ok {
			continue
		}
		treeQualifiedName := joinQualifiedName(scoped.Namespace, treeDecl.Name)
		treeType, _ := a.namedTypes[treeQualifiedName].(*TreeType)
		if treeType == nil {
			continue
		}
		a.withResolutionContext(treeQualifiedName, scoped.Usings, func() {
			for _, commonDecl := range treeDecl.Common {
				if _, exists := treeType.Common[commonDecl.Name]; exists {
					a.errorf(commonDecl.Position, "duplicate common field %q in tree %q", commonDecl.Name, treeDecl.Name)
					continue
				}
				treeType.Common[commonDecl.Name] = Field{
					Name:    commonDecl.Name,
					Type:    a.resolveType(commonDecl.Type),
					Mutable: commonDecl.Mutable,
					IsTail:  commonDecl.IsTail,
				}
			}
			a.populateTreeNodeKindType(treeType)
			for _, member := range treeDecl.Members {
				switch n := member.(type) {
				case *ast.TreeCategoryDecl:
					a.populateTreeCategoryDecl(treeType, n)
				case *ast.TreeBlockDecl:
					a.populateTreeBlockDecl(treeType, n)
				case *ast.TreeStructDecl:
					a.populateTreeStructDecl(treeType, n)
				}
			}
		})
	}
}

func (a *Analyzer) populateTreeNodeKindType(treeType *TreeType) {
	if treeType == nil || treeType.NodeType == nil || treeType.NodeType.KindType == nil || treeType.Decl == nil {
		return
	}
	kindType := treeType.NodeType.KindType
	for _, memberDecl := range treeType.Decl.Members {
		switch decl := memberDecl.(type) {
		case *ast.TreeCategoryDecl:
			if decl == nil {
				continue
			}
			nextExactTag := treeFamilyNextExactTag(treeType, decl.Name, "")
			for i := range decl.Variants {
				variantDecl := &decl.Variants[i]
				memberName := decl.Name + "." + variantDecl.Name
				if _, exists := kindType.MemberMap[memberName]; exists {
					a.errorf(variantDecl.Position, "duplicate tree root kind member %q in %q", memberName, kindType.Name)
					continue
				}
				member := &ConstEnumMember{Name: memberName, Value: int64(nextExactTag), Decl: nil}
				kindType.Members = append(kindType.Members, member)
				kindType.MemberMap[member.Name] = member
				a.constValues[kindType.Name+"."+member.Name] = ConstValue{Kind: ConstInt, Int: member.Value}
				nextExactTag++
			}
		case *ast.TreeBlockDecl:
			if decl == nil {
				continue
			}
			if _, exists := kindType.MemberMap[decl.Name]; exists {
				a.errorf(decl.Pos(), "duplicate tree root kind member %q in %q", decl.Name, kindType.Name)
				continue
			}
			tag := treeFamilyNextExactTag(treeType, "", decl.Name)
			member := &ConstEnumMember{Name: decl.Name, Value: int64(tag), Decl: nil}
			kindType.Members = append(kindType.Members, member)
			kindType.MemberMap[member.Name] = member
			a.constValues[kindType.Name+"."+member.Name] = ConstValue{Kind: ConstInt, Int: member.Value}
		case *ast.TreeStructDecl:
			if decl == nil {
				continue
			}
			if _, exists := kindType.MemberMap[decl.Name]; exists {
				a.errorf(decl.Pos(), "duplicate tree root kind member %q in %q", decl.Name, kindType.Name)
				continue
			}
			tag := treeFamilyNextExactTag(treeType, "", decl.Name)
			member := &ConstEnumMember{Name: decl.Name, Value: int64(tag), Decl: nil}
			kindType.Members = append(kindType.Members, member)
			kindType.MemberMap[member.Name] = member
			a.constValues[kindType.Name+"."+member.Name] = ConstValue{Kind: ConstInt, Int: member.Value}
		}
	}
}

func (a *Analyzer) populateTreeCategoryDecl(treeType *TreeType, categoryDecl *ast.TreeCategoryDecl) {
	if treeType == nil || categoryDecl == nil {
		return
	}
	categoryType, _ := treeType.Member(categoryDecl.Name)
	category, _ := categoryType.(*TreeCategoryType)
	if category == nil {
		return
	}
	category.Common = cloneTreeCommonFields(treeType.Common)
	variants := make([]*EnumVariant, 0, len(categoryDecl.Variants))
	nextExactTag := treeFamilyNextExactTag(treeType, categoryDecl.Name, "")
	for i := range categoryDecl.Variants {
		variantDecl := &categoryDecl.Variants[i]
		if _, exists := category.VariantMap[variantDecl.Name]; exists {
			a.errorf(variantDecl.Position, "duplicate variant %q in tree category %q", variantDecl.Name, category.Name)
			continue
		}
		payload := make([]Type, 0, len(variantDecl.Payload))
		payloadNames := make([]string, 0, len(variantDecl.Payload))
		payloadRelations := make([]ast.EnumPayloadRelation, 0, len(variantDecl.Payload))
		seenPayloadNames := map[string]bool{}
		hasNamedPayloads := false
		hasUnnamedPayloads := false
		for payloadIndex, payloadDecl := range variantDecl.Payload {
			if payloadDecl.Name != "" {
				hasNamedPayloads = true
				if seenPayloadNames[payloadDecl.Name] {
					a.errorf(payloadDecl.Position, "duplicate payload field %q in tree category variant %q.%q", payloadDecl.Name, category.Name, variantDecl.Name)
				}
				seenPayloadNames[payloadDecl.Name] = true
			} else {
				hasUnnamedPayloads = true
			}
			payloadType := a.resolveType(payloadDecl.Type)
			a.validateTreePayloadRelation(category, variantDecl, payloadIndex, payloadDecl, payloadType)
			payload = append(payload, payloadType)
			payloadNames = append(payloadNames, payloadDecl.Name)
			payloadRelations = append(payloadRelations, payloadDecl.Relation)
		}
		if hasNamedPayloads && hasUnnamedPayloads {
			a.errorf(variantDecl.Position, "tree category variant %q.%q must name either all payload fields or none", category.Name, variantDecl.Name)
		}
		_ = i
		variant := &EnumVariant{Name: variantDecl.Name, Tag: nextExactTag, Payload: payload, PayloadNames: payloadNames, PayloadRelations: payloadRelations, TailIndex: -1, Decl: variantDecl}
		nextExactTag++
		category.VariantMap[variant.Name] = variant
		variants = append(variants, variant)
		if category.KindType != nil {
			member := &ConstEnumMember{Name: variant.Name, Value: int64(variant.Tag)}
			category.KindType.Members = append(category.KindType.Members, member)
			category.KindType.MemberMap[member.Name] = member
			a.constValues[category.KindType.Name+"."+member.Name] = ConstValue{Kind: ConstInt, Int: member.Value}
		}
	}
	category.Variants = variants
}

func treeFamilyNextExactTag(treeType *TreeType, categoryName string, memberName string) uint32 {
	if treeType == nil || treeType.Decl == nil {
		return 0
	}
	tag := uint32(0)
	for _, member := range treeType.Decl.Members {
		switch decl := member.(type) {
		case *ast.TreeCategoryDecl:
			if decl == nil {
				continue
			}
			if categoryName != "" && decl.Name == categoryName {
				return tag
			}
			tag += uint32(len(decl.Variants))
		case *ast.TreeBlockDecl:
			if decl == nil {
				continue
			}
			if memberName != "" && decl.Name == memberName {
				return tag
			}
			tag++
		case *ast.TreeStructDecl:
			if decl == nil {
				continue
			}
			if memberName != "" && decl.Name == memberName {
				return tag
			}
			tag++
		}
	}
	return tag
}

func treePayloadTargetMemberType(t Type) (Type, *TreeType, bool) {
	switch tt := t.(type) {
	case *TreeCategoryType:
		if tt == nil || tt.Family == nil {
			return nil, nil, false
		}
		return tt, tt.Family, true
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
	default:
		return nil, nil, false
	}
}

func (a *Analyzer) validateTreePayloadRelation(category *TreeCategoryType, variantDecl *ast.EnumVariantDecl, payloadIndex int, payloadDecl ast.EnumPayloadDecl, payloadType Type) {
	if a == nil || category == nil || variantDecl == nil || payloadDecl.Relation == ast.EnumPayloadRelationNone {
		return
	}
	targetType := payloadType
	if unwrapped, ok := UnwrapOptionalType(targetType); ok {
		targetType = unwrapped
	}
	if payloadDecl.Relation == ast.EnumPayloadRelationChildren {
		elemType, ok := TreeStructuralSequenceElemType(payloadType)
		if !ok {
			a.errorf(payloadDecl.Position, "tree node payload relation %q on %q.%q expects an array-like payload, got %s", string(payloadDecl.Relation), category.Name, variantDecl.Name, payloadType)
			return
		}
		targetType = elemType
	}
	memberType, family, ok := treePayloadTargetMemberType(targetType)
	if !ok {
		a.errorf(payloadDecl.Position, "tree node payload relation %q on %q.%q expects a tree member type, got %s", string(payloadDecl.Relation), category.Name, variantDecl.Name, targetType)
		return
	}
	if family != category.Family {
		a.errorf(payloadDecl.Position, "tree node payload relation %q on %q.%q must stay within tree family %q, got %s", string(payloadDecl.Relation), category.Name, variantDecl.Name, category.Family.Name, memberType)
		return
	}
	if payloadDecl.Relation == ast.EnumPayloadRelationLink && payloadIndex < len(variantDecl.Payload) && payloadDecl.Name == "" {
		a.errorf(payloadDecl.Position, "tree node payload relation %q on %q.%q requires a named payload field", string(payloadDecl.Relation), category.Name, variantDecl.Name)
	}
}

func (a *Analyzer) treeCategoryRole(categoryDecl *ast.TreeCategoryDecl) string {
	if categoryDecl == nil || len(categoryDecl.Annotations) == 0 {
		return ""
	}
	role := ""
	var rolePos lexer.Pos
	seenRole := false
	for _, annotation := range categoryDecl.Annotations {
		if annotation.Name != "role" {
			continue
		}
		if seenRole {
			a.errorf(annotation.Position, "duplicate @role annotation on tree node %q (first seen at %s:%d:%d)", categoryDecl.Name, rolePos.File, rolePos.Line, rolePos.Col)
			continue
		}
		seenRole = true
		rolePos = annotation.Position
		if len(annotation.Args) != 1 {
			a.errorf(annotation.Position, "@role on tree node %q expects exactly one role argument", categoryDecl.Name)
			continue
		}
		if strings.TrimSpace(annotation.Args[0]) == "" {
			a.errorf(annotation.Position, "@role on tree node %q expects a non-empty role argument", categoryDecl.Name)
			continue
		}
		role = annotation.Args[0]
	}
	return role
}

func (a *Analyzer) populateTreeBlockDecl(treeType *TreeType, blockDecl *ast.TreeBlockDecl) {
	if treeType == nil || blockDecl == nil {
		return
	}
	blockType, _ := treeType.Member(blockDecl.Name)
	block, _ := blockType.(*TreeBlockType)
	if block == nil {
		return
	}
	block.ExactTag = treeFamilyNextExactTag(treeType, "", blockDecl.Name)
	for _, field := range blockDecl.Fields {
		if _, exists := block.Fields[field.Name]; exists {
			a.errorf(field.Position, "duplicate field %q in tree block %q", field.Name, block.Name)
			continue
		}
		block.Fields[field.Name] = Field{Name: field.Name, Type: a.resolveType(field.Type), Mutable: field.Mutable, IsTail: field.IsTail}
	}
}

func (a *Analyzer) populateTreeStructDecl(treeType *TreeType, structDecl *ast.TreeStructDecl) {
	if treeType == nil || structDecl == nil {
		return
	}
	structType, _ := treeType.Member(structDecl.Name)
	memberStruct, _ := structType.(*TreeStructType)
	if memberStruct == nil {
		return
	}
	memberStruct.ExactTag = treeFamilyNextExactTag(treeType, "", structDecl.Name)
	for _, field := range structDecl.Fields {
		if _, exists := memberStruct.Fields[field.Name]; exists {
			a.errorf(field.Position, "duplicate field %q in tree struct %q", field.Name, memberStruct.Name)
			continue
		}
		memberStruct.Fields[field.Name] = Field{Name: field.Name, Type: a.resolveType(field.Type), Mutable: field.Mutable, IsTail: field.IsTail}
	}
}
