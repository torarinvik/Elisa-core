package semantic

import (
	"llcontext/src/ast"
)

func (a *Analyzer) populateConstEnumMembers(decls []scopedDecl) {
	for _, scoped := range decls {
		constEnumDecl, ok := scoped.Decl.(*ast.ConstEnumDecl)
		if !ok {
			continue
		}
		constEnumType, _ := a.namedTypes[joinQualifiedName(scoped.Namespace, constEnumDecl.Name)].(*ConstEnumType)
		if constEnumType == nil {
			continue
		}
		storageType := a.resolveType(constEnumDecl.Storage)
		constEnumType.Storage = storageType
		if !IsIntegralStorageType(storageType) {
			a.errorf(constEnumDecl.Storage.Pos(), "const enum %q storage type must be an explicit integer type, got %s", constEnumDecl.Name, storageType)
		}
		members := make([]*ConstEnumMember, 0, len(constEnumDecl.Members))
		nextValue := int64(0)
		for i := range constEnumDecl.Members {
			memberDecl := &constEnumDecl.Members[i]
			if _, exists := constEnumType.MemberMap[memberDecl.Name]; exists {
				a.errorf(memberDecl.Pos(), "duplicate const enum member %q in %q", memberDecl.Name, constEnumDecl.Name)
				continue
			}
			value := nextValue
			if memberDecl.Value != nil {
				resolved, ok := a.evalConstExpr(memberDecl.Value)
				if !ok || resolved.Kind != ConstInt {
					a.errorf(memberDecl.Value.Pos(), "const enum member %q.%q requires a compile-time integer value", constEnumDecl.Name, memberDecl.Name)
					continue
				}
				value = resolved.Int
			}
			member := &ConstEnumMember{Name: memberDecl.Name, Value: value, Decl: memberDecl}
			constEnumType.MemberMap[member.Name] = member
			members = append(members, member)
			nextValue = value + 1
		}
		constEnumType.Members = members
	}
}

func packedEnumStoreTypeName(enumName string) string {
	return enumName + ".Store"
}

func packedEnumTagTypeName(enumName string) string {
	return enumName + ".Tag"
}

func treeCategoryKindTypeName(categoryName string) string {
	return categoryName + ".Kind"
}

func treeNodeKindTypeName(nodeName string) string {
	return nodeName + ".Kind"
}

func treeMemberTypeName(treeName string, memberName string) string {
	return treeName + "." + memberName
}

func treeStoreTypeName(treeName string) string {
	return treeName + ".Store"
}

func treeMemberDeclName(member ast.TreeMemberDecl) string {
	if member == nil {
		return ""
	}
	switch n := member.(type) {
	case *ast.TreeCategoryDecl:
		return n.Name
	case *ast.TreeBlockDecl:
		return n.Name
	case *ast.TreeStructDecl:
		return n.Name
	default:
		return ""
	}
}

func cloneTreeCommonFields(fields map[string]Field) map[string]Field {
	if len(fields) == 0 {
		return map[string]Field{}
	}
	cloned := make(map[string]Field, len(fields))
	for name, field := range fields {
		cloned[name] = field
	}
	return cloned
}

func (a *Analyzer) populateEnumVariants(decls []scopedDecl) {
	for _, scoped := range decls {
		enumDecl, ok := scoped.Decl.(*ast.EnumDecl)
		if !ok {
			continue
		}
		enumType, _ := a.namedTypes[joinQualifiedName(scoped.Namespace, enumDecl.Name)].(*EnumType)
		if enumType == nil {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			a.analyzeEnumAnnotations(enumDecl, enumType)
			if len(enumDecl.Common) > 0 && !enumDecl.Packed {
				a.errorf(enumDecl.Pos(), "enum %q only supports common: fields for packed enums", enumDecl.Name)
			}
			for _, commonDecl := range enumDecl.Common {
				storage := a.analyzePackedCommonFieldAnnotations(enumDecl, commonDecl)
				if commonDecl.Mutable {
					a.errorf(commonDecl.Position, "packed enum %q common field %q cannot be mutable in v1", enumDecl.Name, commonDecl.Name)
				}
				if commonDecl.IsTail {
					a.errorf(commonDecl.Position, "packed enum %q common field %q cannot be tail-allocated", enumDecl.Name, commonDecl.Name)
				}
				if _, exists := enumType.Common[commonDecl.Name]; exists {
					a.errorf(commonDecl.Position, "duplicate common field %q in enum %q", commonDecl.Name, enumDecl.Name)
					continue
				}
				commonType := a.resolveType(commonDecl.Type)
				enumType.Common[commonDecl.Name] = Field{Name: commonDecl.Name, Type: commonType, Mutable: false, PackedStorage: storage}
			}
			variants := make([]*EnumVariant, 0, len(enumDecl.Variants))
			for i := range enumDecl.Variants {
				variantDecl := &enumDecl.Variants[i]
				if _, exists := enumType.VariantMap[variantDecl.Name]; exists {
					a.errorf(variantDecl.Position, "duplicate variant %q in enum %q", variantDecl.Name, enumDecl.Name)
					continue
				}
				payload := make([]Type, 0, len(variantDecl.Payload))
				payloadNames := make([]string, 0, len(variantDecl.Payload))
				tailIndex := -1
				seenPayloadNames := map[string]bool{}
				hasNamedPayloads := false
				hasUnnamedPayloads := false
				for payloadIndex, payloadDecl := range variantDecl.Payload {
					if payloadDecl.Relation != ast.EnumPayloadRelationNone {
						a.errorf(payloadDecl.Position, "enum variant %q.%q does not support payload relation %q; child/link relations are only available on tree nodes", enumDecl.Name, variantDecl.Name, string(payloadDecl.Relation))
					}
					if payloadDecl.Name != "" {
						hasNamedPayloads = true
						if seenPayloadNames[payloadDecl.Name] {
							a.errorf(payloadDecl.Position, "duplicate payload field %q in enum variant %q.%q", payloadDecl.Name, enumDecl.Name, variantDecl.Name)
						}
						seenPayloadNames[payloadDecl.Name] = true
					} else {
						hasUnnamedPayloads = true
					}
					payloadType := a.resolveType(payloadDecl.Type)
					if tailExpr, ok := payloadDecl.Type.(*ast.TailType); ok {
						if !enumDecl.Packed {
							a.errorf(payloadDecl.Type.Pos(), "enum %q variant %q tail payloads are only supported for packed enums", enumDecl.Name, variantDecl.Name)
						} else {
							if tailIndex >= 0 {
								a.errorf(payloadDecl.Type.Pos(), "packed enum %q variant %q can only declare one tail payload", enumDecl.Name, variantDecl.Name)
							}
							tailElemType := a.resolveType(tailExpr.Elem)
							payloadType = &DArrayViewType{Elem: tailElemType, SurfaceName: "dview"}
							tailIndex = payloadIndex
						}
					}
					if !enumDecl.Packed && SameType(payloadType, enumType) {
						a.errorf(payloadDecl.Type.Pos(), "enum %q variant %q cannot contain %q by value; use a reference type instead", enumDecl.Name, variantDecl.Name, enumDecl.Name)
					}
					payload = append(payload, payloadType)
					payloadNames = append(payloadNames, payloadDecl.Name)
				}
				if hasNamedPayloads && hasUnnamedPayloads {
					a.errorf(variantDecl.Position, "enum variant %q.%q must name either all payload fields or none", enumDecl.Name, variantDecl.Name)
				}
				variant := &EnumVariant{Name: variantDecl.Name, Tag: uint32(i), Payload: payload, PayloadNames: payloadNames, TailIndex: tailIndex, Decl: variantDecl}
				enumType.VariantMap[variant.Name] = variant
				variants = append(variants, variant)
				if enumType.Packed && enumType.TagType != nil {
					member := &ConstEnumMember{Name: variant.Name, Value: int64(variant.Tag)}
					enumType.TagType.Members = append(enumType.TagType.Members, member)
					enumType.TagType.MemberMap[member.Name] = member
					a.constValues[enumType.TagType.Name+"."+member.Name] = ConstValue{Kind: ConstInt, Int: member.Value}
				}
			}
			enumType.Variants = variants
		})
	}
}
