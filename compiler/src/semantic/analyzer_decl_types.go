package semantic

import (
	"llcontext/src/ast"
)

func (a *Analyzer) collectConstValues(decls []scopedDecl) {
	for _, scoped := range decls {
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			switch n := scoped.Decl.(type) {
			case *ast.ConstDecl:
				if value, ok := a.evalConstExpr(n.Value); ok {
					a.constValues[joinQualifiedName(scoped.Namespace, n.Name)] = value
				}
			case *ast.ConstEnumDecl:
				nextValue := int64(0)
				hasValue := false
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				for i := range n.Members {
					member := &n.Members[i]
					value := nextValue
					if member.Value != nil {
						resolved, ok := a.evalConstExpr(member.Value)
						if !ok || resolved.Kind != ConstInt {
							continue
						}
						value = resolved.Int
					}
					a.constValues[qualifiedName+"."+member.Name] = ConstValue{Kind: ConstInt, Int: value}
					nextValue = value + 1
					hasValue = true
				}
				if !hasValue {
					nextValue = 0
				}
			}
		})
	}
}

func (a *Analyzer) expandActiveDecls(decls []ast.Decl) []ast.Decl {
	out := make([]ast.Decl, 0, len(decls))
	for _, decl := range decls {
		if n, ok := decl.(*ast.StaticIfDecl); ok {
			out = append(out, a.expandActiveDecls(a.activeDeclBranch(n))...)
			continue
		}
		out = append(out, decl)
	}
	return out
}

func (a *Analyzer) activeDeclBranch(n *ast.StaticIfDecl) []ast.Decl {
	if selected, ok := a.evalConstBoolExpr(n.Cond); ok {
		if selected {
			return n.Then
		}
	} else {
		a.errorf(n.Pos(), "static if condition must be a compile-time bool")
		return n.Then
	}
	for _, elif := range n.Elifs {
		selected, ok := a.evalConstBoolExpr(elif.Cond)
		if !ok {
			a.errorf(elif.Position, "static elif condition must be a compile-time bool")
			continue
		}
		if selected {
			return elif.Body
		}
	}
	return n.Else
}

func (a *Analyzer) activeStmtBranch(n *ast.StaticIfStmt) []ast.Stmt {
	if selected, ok := a.evalConstBoolExpr(n.Cond); ok {
		if selected {
			return n.Then
		}
	} else {
		a.errorf(n.Pos(), "static if condition must be a compile-time bool")
		return n.Then
	}
	for _, elif := range n.Elifs {
		selected, ok := a.evalConstBoolExpr(elif.Cond)
		if !ok {
			a.errorf(elif.Position, "static elif condition must be a compile-time bool")
			continue
		}
		if selected {
			return elif.Body
		}
	}
	return n.Else
}

func (a *Analyzer) collectNamedTypes(decls []scopedDecl) {
	for _, scoped := range decls {
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			switch n := scoped.Decl.(type) {
			case *ast.StructDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if existing, exists := a.namedTypes[qualifiedName]; exists {
					if st, ok := existing.(*StructType); ok && st.Builtin && isBuiltinRuntimeStructName(n.Name) {
						return
					}
					a.errorf(n.Pos(), "%s", DuplicateTypeMessage(qualifiedName))
					return
				}
				st := &StructType{
					Name:             qualifiedName,
					TypeParams:       append([]string(nil), n.TypeParams...),
					RefStorageParams: append([]string(nil), n.RefStorageParams...),
					RefStateParams:   append([]string(nil), n.RefStateParams...),
					GenericParams:    append([]ast.GenericParam(nil), n.GenericParams...),
					NamedStateCases:  append([]string(nil), n.NamedStateCases...),
					Fields:           map[string]Field{},
					Affine:           n.Affine,
					ReprC:            n.ReprC,
					Decl:             n,
				}
				a.namedTypes[qualifiedName] = st
			case *ast.StoreDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if _, exists := a.namedTypes[qualifiedName]; exists {
					a.errorf(n.Pos(), "%s", DuplicateTypeMessage(qualifiedName))
					return
				}
				storeFields := make([]ast.FieldDecl, 0, len(n.Fields))
				for _, field := range n.Fields {
					storeFields = append(storeFields, ast.FieldDecl{
						Position:    field.Position,
						Annotations: append([]ast.Annotation(nil), field.Annotations...),
						Name:        field.Name,
						Mutable:     true,
						Type: &ast.GenericType{
							Position: field.Position,
							Name:     "darray",
							Args:     []ast.TypeExpr{field.Type},
						},
					})
				}
				st := &StructType{
					Name:            qualifiedName,
					Fields:          map[string]Field{},
					Decl:            &ast.StructDecl{Position: n.Position, Annotations: append([]ast.Annotation(nil), n.Annotations...), Name: n.Name, ReprC: true, Fields: storeFields},
					StoreDecl:       n,
					Store:           true,
					StoreFieldOrder: make([]string, 0, len(n.Fields)),
					ReprC:           true,
				}
				a.namedTypes[qualifiedName] = st
			case *ast.ConstEnumDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if _, exists := a.namedTypes[qualifiedName]; exists {
					a.errorf(n.Pos(), "%s", DuplicateTypeMessage(qualifiedName))
					return
				}
				a.namedTypes[qualifiedName] = &ConstEnumType{Name: qualifiedName, MemberMap: map[string]*ConstEnumMember{}, Decl: n}
			case *ast.EnumDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if _, exists := a.namedTypes[qualifiedName]; exists {
					a.errorf(n.Pos(), "%s", DuplicateTypeMessage(qualifiedName))
					return
				}
				enumType := &EnumType{Name: qualifiedName, Packed: n.Packed, Common: map[string]Field{}, VariantMap: map[string]*EnumVariant{}, Decl: n}
				a.namedTypes[qualifiedName] = enumType
				if n.Packed {
					tagName := packedEnumTagTypeName(qualifiedName)
					if _, exists := a.namedTypes[tagName]; exists {
						a.errorf(n.Pos(), "%s", DuplicateTypeMessage(tagName))
						return
					}
					tagType := &ConstEnumType{Name: tagName, Storage: a.namedTypes["u32"], MemberMap: map[string]*ConstEnumMember{}}
					enumType.TagType = tagType
					a.namedTypes[tagName] = tagType
					storeName := packedEnumStoreTypeName(qualifiedName)
					if _, exists := a.namedTypes[storeName]; exists {
						a.errorf(n.Pos(), "%s", DuplicateTypeMessage(storeName))
						return
					}
					storeType := &PackedEnumStoreType{Name: storeName, Enum: enumType}
					enumType.StoreType = storeType
					a.namedTypes[storeName] = storeType
				}
			case *ast.TreeDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if _, exists := a.namedTypes[qualifiedName]; exists {
					a.errorf(n.Pos(), "%s", DuplicateTypeMessage(qualifiedName))
					return
				}
				treeType := &TreeType{Name: qualifiedName, Common: map[string]Field{}, MemberTypes: map[string]Type{}, Decl: n}
				a.namedTypes[qualifiedName] = treeType
				nodeQualifiedName := treeMemberTypeName(qualifiedName, "Node")
				if _, exists := a.namedTypes[nodeQualifiedName]; exists {
					a.errorf(n.Pos(), "%s", DuplicateTypeMessage(nodeQualifiedName))
					return
				}
				nodeType := &TreeNodeType{Name: nodeQualifiedName, Family: treeType}
				kindName := treeNodeKindTypeName(nodeQualifiedName)
				if _, exists := a.namedTypes[kindName]; exists {
					a.errorf(n.Pos(), "%s", DuplicateTypeMessage(kindName))
					return
				}
				kindType := &ConstEnumType{Name: kindName, Storage: a.namedTypes["u32"], MemberMap: map[string]*ConstEnumMember{}}
				nodeType.KindType = kindType
				treeType.NodeType = nodeType
				a.namedTypes[nodeQualifiedName] = nodeType
				a.namedTypes[kindName] = kindType
				treeType.MemberTypes["Node"] = nodeType
				storeName := treeStoreTypeName(qualifiedName)
				if _, exists := a.namedTypes[storeName]; exists {
					a.errorf(n.Pos(), "%s", DuplicateTypeMessage(storeName))
					return
				}
				storeType := &TreeStoreType{Name: storeName, Family: treeType}
				treeType.StoreType = storeType
				a.namedTypes[storeName] = storeType
				treeType.MemberTypes["Store"] = storeType
				a.registerTreeMemberTypes(qualifiedName, treeType, n.Members)
			case *ast.ExternTypeDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if _, exists := a.namedTypes[qualifiedName]; exists {
					a.errorf(n.Pos(), "%s", DuplicateTypeMessage(qualifiedName))
					return
				}
				a.namedTypes[qualifiedName] = &OpaqueType{Name: qualifiedName}
			case *ast.ErrorDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if _, exists := a.namedTypes[qualifiedName]; exists {
					a.errorf(n.Pos(), "%s", DuplicateTypeMessage(qualifiedName))
					return
				}
				seenTags := map[string]bool{}
				resolvedTags := make([]string, 0, len(n.Tags))
				for _, tag := range n.Tags {
					if seenTags[tag] {
						a.errorf(n.Pos(), "duplicate error tag %q in error set %q", tag, n.Name)
						continue
					}
					seenTags[tag] = true
					resolvedTags = append(resolvedTags, QualifyErrorTag(qualifiedName, tag))
				}
				a.namedTypes[qualifiedName] = &ErrorSetType{Name: qualifiedName, Tags: resolvedTags}
			case *ast.PermissionDecl:
			case *ast.EffectDecl:
			case *ast.TypeAliasDecl, *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
			}
		})
	}
}
