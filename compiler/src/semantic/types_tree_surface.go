package semantic

import (
	"fmt"

	"elisacore/src/ast"
)

func TreeStructuralSequenceElemType(t Type) (Type, bool) {
	if inner, ok := UnwrapOptionalType(t); ok {
		return TreeStructuralSequenceElemType(inner)
	}
	switch tt := t.(type) {
	case *ArrayType:
		return tt.Elem, true
	case *DArrayType:
		return tt.Elem, true
	case *DArrayViewType:
		if tt.SurfaceName != "" && tt.SurfaceName != "view" {
			return nil, false
		}
		return tt.Elem, true
	default:
		return nil, false
	}
}

func TreeStructuralChildItemType(fieldType Type, relation ast.EnumPayloadRelation) (Type, bool) {
	baseType, _ := UnwrapOptionalType(fieldType)
	switch relation {
	case ast.EnumPayloadRelationChild:
		if baseType == nil {
			return nil, false
		}
		return baseType, true
	case ast.EnumPayloadRelationChildren:
		return TreeStructuralSequenceElemType(baseType)
	default:
		return nil, false
	}
}

func TreeRewriteResultTypeForValue(t Type) Type {
	switch tt := StripAggregateStateType(t).(type) {
	case *TreeVariantViewType:
		if tt != nil {
			return tt.Category
		}
	case *TreeCategoryType, *TreeBlockType, *TreeStructType, *TreeNodeType:
		return tt
	}
	return t
}

func TreeRewriteChildBindingType(fieldType Type, relation ast.EnumPayloadRelation) (Type, bool) {
	itemType, ok := TreeStructuralChildItemType(fieldType, relation)
	if !ok || itemType == nil {
		return nil, false
	}
	resultType := TreeRewriteResultTypeForValue(itemType)
	if resultType == nil {
		return nil, false
	}
	_, optional := UnwrapOptionalType(fieldType)
	switch relation {
	case ast.EnumPayloadRelationChild:
		if optional {
			return &OptionalType{Value: resultType}, true
		}
		return resultType, true
	case ast.EnumPayloadRelationChildren:
		viewType := &DArrayViewType{Elem: resultType, SurfaceName: "view"}
		if optional {
			return &OptionalType{Value: viewType}, true
		}
		return viewType, true
	default:
		return nil, false
	}
}

func OptionalTreeFoldChildBindingType(resultType Type) Type {
	if resultType == nil {
		return nil
	}
	return &OptionalType{Value: resultType}
}

func TreeStructuralSequenceViewType(t Type) (*DArrayViewType, bool) {
	darray, ok := t.(*DArrayType)
	if !ok || darray == nil {
		return nil, false
	}
	return &DArrayViewType{Elem: darray.Elem, SurfaceName: "view"}, true
}

func treeSurfaceSequenceType(t Type) (Type, bool) {
	switch tt := t.(type) {
	case *DArrayType:
		return &DArrayViewType{Elem: tt.Elem, SurfaceName: "view"}, true
	case *OptionalType:
		if tt == nil || tt.Value == nil {
			return nil, false
		}
		inner, ok := treeSurfaceSequenceType(tt.Value)
		if !ok {
			return nil, false
		}
		return &OptionalType{Value: inner}, true
	default:
		return nil, false
	}
}

func treeSurfaceSequenceField(field Field, relation ast.EnumPayloadRelation) Field {
	if relation != ast.EnumPayloadRelationChildren {
		return field
	}
	if surfaceType, ok := treeSurfaceSequenceType(field.Type); ok {
		field.Type = surfaceType
	}
	return field
}

func TreeVariantSurfaceFieldInfo(viewType *TreeVariantViewType, fieldName string) (Field, bool) {
	if viewType == nil || viewType.Category == nil || viewType.Variant == nil {
		return Field{}, false
	}
	field, ok := viewType.Field(fieldName)
	if !ok {
		return Field{}, false
	}
	if fieldName == "kind" {
		return field, true
	}
	if _, ok := viewType.Category.Common[fieldName]; ok {
		relation := TreeFieldStructuralRelation(viewType.Category.Family, field.Type)
		return treeSurfaceSequenceField(field, relation), true
	}
	if index, ok := viewType.Variant.PayloadIndex(fieldName); ok {
		relation := viewType.Variant.PayloadRelation(index)
		return treeSurfaceSequenceField(field, relation), true
	}
	return field, true
}

func TreeCategorySurfaceFieldInfo(categoryType *TreeCategoryType, fieldName string) (Field, bool) {
	if categoryType == nil {
		return Field{}, false
	}
	if fieldName == "kind" {
		return TreeKindFieldInfo(categoryType)
	}
	field, ok := categoryType.Common[fieldName]
	if ok {
		relation := TreeFieldStructuralRelation(categoryType.Family, field.Type)
		return treeSurfaceSequenceField(field, relation), true
	}
	var found Field
	foundAny := false
	for _, variant := range categoryType.Variants {
		if variant == nil {
			continue
		}
		index, ok := variant.PayloadIndex(fieldName)
		if !ok || index >= len(variant.Payload) {
			continue
		}
		current := Field{Name: fieldName, Type: variant.Payload[index]}
		if foundAny && !SameType(found.Type, current.Type) {
			return Field{}, false
		}
		found = current
		foundAny = true
	}
	if !foundAny {
		return Field{}, false
	}
	relation := TreeFieldStructuralRelation(categoryType.Family, found.Type)
	return treeSurfaceSequenceField(found, relation), true
}

func TreeExactSurfaceFieldInfo(member Type, fieldName string) (Field, bool) {
	field, ok := TreeExactFieldInfo(member, fieldName)
	if !ok {
		return Field{}, false
	}
	family, ok := TreeFamilyForMemberType(member)
	if !ok || family == nil {
		return field, true
	}
	relation := TreeFieldStructuralRelation(family, field.Type)
	return treeSurfaceSequenceField(field, relation), true
}

func TreeFamilyForMemberType(t Type) (*TreeType, bool) {
	switch tt := StripAggregateStateType(t).(type) {
	case *TreeCategoryType:
		return tt.Family, tt != nil && tt.Family != nil
	case *TreeVariantViewType:
		if tt == nil || tt.Category == nil {
			return nil, false
		}
		return tt.Category.Family, tt.Category.Family != nil
	case *TreeBlockType:
		return tt.Family, tt != nil && tt.Family != nil
	case *TreeStructType:
		return tt.Family, tt != nil && tt.Family != nil
	case *TreeNodeType:
		return tt.Family, tt != nil && tt.Family != nil
	default:
		return nil, false
	}
}

func TreeCommonFieldDeclsForFamily(treeType *TreeType) []ast.FieldDecl {
	if treeType == nil || treeType.Decl == nil {
		return nil
	}
	return treeType.Decl.Common
}

func TreeBlockFieldDeclsWithCommon(blockType *TreeBlockType) []ast.FieldDecl {
	if blockType == nil {
		return nil
	}
	out := append([]ast.FieldDecl(nil), TreeCommonFieldDeclsForFamily(blockType.Family)...)
	if blockType.Decl != nil {
		out = append(out, blockType.Decl.Fields...)
	}
	return out
}

func TreeStructFieldDeclsWithCommon(structType *TreeStructType) []ast.FieldDecl {
	if structType == nil {
		return nil
	}
	out := append([]ast.FieldDecl(nil), TreeCommonFieldDeclsForFamily(structType.Family)...)
	if structType.Decl != nil {
		out = append(out, structType.Decl.Fields...)
	}
	return out
}

func TreeVariantFieldDeclsWithCommon(viewType *TreeVariantViewType) []ast.FieldDecl {
	if viewType == nil || viewType.Category == nil || viewType.Variant == nil {
		return nil
	}
	out := append([]ast.FieldDecl(nil), TreeCommonFieldDeclsForFamily(viewType.Category.Family)...)
	for i := range viewType.Variant.Payload {
		name := viewType.Variant.PayloadLabel(i)
		if name == "" {
			name = fmt.Sprintf("payload%d", i)
		}
		out = append(out, ast.FieldDecl{Name: name})
	}
	return out
}

func TreeExactFieldInfo(member Type, fieldName string) (Field, bool) {
	switch tt := StripAggregateStateType(member).(type) {
	case *TreeVariantViewType:
		if tt == nil || tt.Category == nil || tt.Variant == nil {
			return Field{}, false
		}
		if field, ok := tt.Category.Common[fieldName]; ok {
			return field, true
		}
		index, ok := tt.Variant.PayloadIndex(fieldName)
		if !ok || index < 0 || index >= len(tt.Variant.Payload) {
			return Field{}, false
		}
		return Field{Type: tt.Variant.Payload[index]}, true
	case *TreeBlockType:
		if tt == nil {
			return Field{}, false
		}
		if field, ok := tt.Family.Common[fieldName]; ok {
			return field, true
		}
		field, ok := tt.Fields[fieldName]
		return field, ok
	case *TreeStructType:
		if tt == nil {
			return Field{}, false
		}
		if field, ok := tt.Family.Common[fieldName]; ok {
			return field, true
		}
		field, ok := tt.Fields[fieldName]
		return field, ok
	default:
		return Field{}, false
	}
}

func TreeExactTag(t Type) (uint32, bool) {
	switch tt := StripAggregateStateType(t).(type) {
	case *TreeVariantViewType:
		if tt == nil || tt.Variant == nil {
			return 0, false
		}
		return tt.Variant.Tag, true
	case *TreeBlockType:
		if tt == nil {
			return 0, false
		}
		return tt.ExactTag, true
	case *TreeStructType:
		if tt == nil {
			return 0, false
		}
		return tt.ExactTag, true
	default:
		return 0, false
	}
}

func TreeFieldStructuralRelation(family *TreeType, fieldType Type) ast.EnumPayloadRelation {
	if family == nil || fieldType == nil {
		return ast.EnumPayloadRelationNone
	}
	baseType, _ := UnwrapOptionalType(fieldType)
	if memberFamily, ok := TreeFamilyForMemberType(baseType); ok && memberFamily == family {
		switch StripAggregateStateType(baseType).(type) {
		case *TreeCategoryType, *TreeVariantViewType, *TreeBlockType, *TreeStructType:
			return ast.EnumPayloadRelationChild
		}
	}
	if elemType, ok := TreeStructuralSequenceElemType(baseType); ok {
		if memberFamily, ok := TreeFamilyForMemberType(elemType); ok && memberFamily == family {
			switch StripAggregateStateType(elemType).(type) {
			case *TreeCategoryType, *TreeVariantViewType, *TreeBlockType, *TreeStructType:
				return ast.EnumPayloadRelationChildren
			}
		}
	}
	return ast.EnumPayloadRelationNone
}

type TreeStructuralChildBinding struct {
	Name     string
	Type     Type
	Relation ast.EnumPayloadRelation
}

func TreeStructuralChildBindings(t Type) []TreeStructuralChildBinding {
	switch tt := StripAggregateStateType(t).(type) {
	case *TreeVariantViewType:
		if tt == nil || tt.Variant == nil {
			return nil
		}
		out := make([]TreeStructuralChildBinding, 0, len(tt.Variant.Payload))
		for i, payloadType := range tt.Variant.Payload {
			relation := tt.Variant.PayloadRelation(i)
			if relation != ast.EnumPayloadRelationChild && relation != ast.EnumPayloadRelationChildren {
				continue
			}
			name := tt.Variant.PayloadLabel(i)
			if name == "" {
				continue
			}
			out = append(out, TreeStructuralChildBinding{Name: name, Type: payloadType, Relation: relation})
		}
		return out
	case *TreeBlockType:
		return treeExactStructuralChildBindings(tt, TreeBlockFieldDeclsWithCommon(tt))
	case *TreeStructType:
		return treeExactStructuralChildBindings(tt, TreeStructFieldDeclsWithCommon(tt))
	default:
		return nil
	}
}

func treeExactStructuralChildBindings(member Type, fieldDecls []ast.FieldDecl) []TreeStructuralChildBinding {
	family, ok := TreeFamilyForMemberType(member)
	if !ok || family == nil {
		return nil
	}
	out := make([]TreeStructuralChildBinding, 0, len(fieldDecls))
	for _, fieldDecl := range fieldDecls {
		field, ok := TreeExactFieldInfo(member, fieldDecl.Name)
		if !ok {
			continue
		}
		relation := TreeFieldStructuralRelation(family, field.Type)
		if relation != ast.EnumPayloadRelationChild && relation != ast.EnumPayloadRelationChildren {
			continue
		}
		if fieldDecl.Name == "" {
			continue
		}
		out = append(out, TreeStructuralChildBinding{Name: fieldDecl.Name, Type: field.Type, Relation: relation})
	}
	return out
}

func TreeFamilyExactMembersInTagOrder(treeType *TreeType) []Type {
	if treeType == nil || treeType.Decl == nil {
		return nil
	}
	count := uint32(0)
	for _, member := range flattenTreeMemberDecls(treeType.Decl.Members) {
		switch decl := member.(type) {
		case *ast.TreeCategoryDecl:
			count += uint32(len(decl.Variants))
		case *ast.TreeBlockDecl, *ast.TreeStructDecl:
			count++
		}
	}
	if count == 0 {
		return nil
	}
	out := make([]Type, count)
	for _, member := range flattenTreeMemberDecls(treeType.Decl.Members) {
		switch decl := member.(type) {
		case *ast.TreeCategoryDecl:
			memberType, ok := treeType.Member(decl.Name)
			if !ok {
				continue
			}
			category, _ := memberType.(*TreeCategoryType)
			if category == nil {
				continue
			}
			for _, variant := range category.Variants {
				if int(variant.Tag) < len(out) {
					out[variant.Tag] = category.VariantViewType(variant)
				}
			}
		case *ast.TreeBlockDecl:
			memberType, ok := treeType.Member(decl.Name)
			if !ok {
				continue
			}
			blockType, _ := memberType.(*TreeBlockType)
			if blockType == nil {
				continue
			}
			if int(blockType.ExactTag) < len(out) {
				out[blockType.ExactTag] = blockType
			}
		case *ast.TreeStructDecl:
			memberType, ok := treeType.Member(decl.Name)
			if !ok {
				continue
			}
			structType, _ := memberType.(*TreeStructType)
			if structType == nil {
				continue
			}
			if int(structType.ExactTag) < len(out) {
				out[structType.ExactTag] = structType
			}
		}
	}
	filtered := make([]Type, 0, len(out))
	for _, member := range out {
		if member != nil {
			filtered = append(filtered, member)
		}
	}
	return filtered
}
