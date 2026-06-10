package semantic

import (
	"elisacore/src/ast"
)

func synthesizedStoreDeclFromStruct(n *ast.StructDecl) *ast.StoreDecl {
	if n == nil || n.Layout != ast.StructLayoutSOA {
		return nil
	}
	return &ast.StoreDecl{
		Position:    n.Position,
		Annotations: append([]ast.Annotation(nil), n.Annotations...),
		Name:        n.Name,
		Soa:         true,
		Fields:      append([]ast.FieldDecl(nil), n.Fields...),
	}
}

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
		if n, ok := decl.(*ast.StaticAssertDecl); ok {
			a.analyzeStaticAssert(n.Pos(), n.Cond, n.Message)
			out = append(out, decl)
			continue
		}
		if n, ok := decl.(*ast.StaticAssertBlockDecl); ok {
			for _, item := range n.Assertions {
				a.analyzeStaticAssert(item.Position, item.Cond, item.Message)
			}
			out = append(out, decl)
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
	// docs/76 Phase 3: an enum is a recursive AST node not only when it references ITSELF by value,
	// but when it can reach itself through a chain of by-value enum payload references (mutual
	// recursion, e.g. Tree↔Forest, Expr↔Stmt). Promote every enum in such a cycle. Computed once over
	// all enum decls before the type-creation loop so each enum's Packed/RecursivePlain flags are set
	// consistently when its EnumType is built below.
	recursiveEnums := computeRecursiveEnumSet(decls)
	for _, scoped := range decls {
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			markPrivate := func(name string) {
				if scoped.Private {
					a.privateTypeNames[name] = true
				}
			}
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
				storeDecl := synthesizedStoreDeclFromStruct(n)
				st := &StructType{
					Name:       qualifiedName,
					Namespace:  scoped.Namespace,
					Usings:     append([]string(nil), scoped.Usings...),
					TypeParams: append([]string(nil), n.TypeParams...), RegionParams: append([]string(nil), n.RegionParams...),
					RegionOwner:     n.RegionOwner,
					GenericParams:   append([]ast.GenericParam(nil), n.GenericParams...),
					NamedStateCases: append([]string(nil), n.NamedStateCases...),
					Fields:          map[string]Field{},
					Affine:          n.Affine,
					Droppable:       n.Droppable,
					ReprC:           n.ReprC,
					Layout:          n.Layout,
					PackedLayout:    n.Layout == ast.StructLayoutPacked,
					Decl:            n,
					StoreDecl:       storeDecl,
					Store:           storeDecl != nil,
					StoreFieldOrder: make([]string, 0, len(n.Fields)),
				}
				a.namedTypes[qualifiedName] = st
				markPrivate(qualifiedName)
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
					Namespace:       scoped.Namespace,
					Usings:          append([]string(nil), scoped.Usings...),
					Fields:          map[string]Field{},
					Decl:            &ast.StructDecl{Position: n.Position, Annotations: append([]ast.Annotation(nil), n.Annotations...), Name: n.Name, ReprC: true, Fields: storeFields},
					StoreDecl:       n,
					Store:           true,
					StoreFieldOrder: make([]string, 0, len(n.Fields)),
					ReprC:           true,
				}
				a.namedTypes[qualifiedName] = st
				markPrivate(qualifiedName)
			case *ast.ConstEnumDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if _, exists := a.namedTypes[qualifiedName]; exists {
					a.errorf(n.Pos(), "%s", DuplicateTypeMessage(qualifiedName))
					return
				}
				a.namedTypes[qualifiedName] = &ConstEnumType{Name: qualifiedName, MemberMap: map[string]*ConstEnumMember{}, Decl: n}
				markPrivate(qualifiedName)
			case *ast.EnumDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if _, exists := a.namedTypes[qualifiedName]; exists {
					a.errorf(n.Pos(), "%s", DuplicateTypeMessage(qualifiedName))
					return
				}
				// docs/76 Phase 3: a plain `enum` whose variant references the enum by value is a
				// recursive AST node — promote it to the region-backed machinery (set the AST's Packed
				// flag here, before any consumer runs, so the whole pipeline sees it consistently and
				// the order-dependent type-graph below is built once). The default storage is AoS.
				recursivePlain := false
				if !n.Packed && recursiveEnums[n.Name] {
					n.Packed = true
					recursivePlain = true
				}
				enumType := &EnumType{Name: qualifiedName, Packed: n.Packed, Common: map[string]Field{}, VariantMap: map[string]*EnumVariant{}, Decl: n, Layout: n.Layout, LayoutSet: n.LayoutSet, LayoutSparse: n.LayoutSparse, IndexWidth: n.IndexWidth, RecursivePlain: recursivePlain}
				a.namedTypes[qualifiedName] = enumType
				markPrivate(qualifiedName)
				if n.Packed {
					tagName := packedEnumTagTypeName(qualifiedName)
					if _, exists := a.namedTypes[tagName]; exists {
						a.errorf(n.Pos(), "%s", DuplicateTypeMessage(tagName))
						return
					}
					tagType := &ConstEnumType{Name: tagName, Storage: a.namedTypes["u32"], MemberMap: map[string]*ConstEnumMember{}}
					enumType.TagType = tagType
					a.namedTypes[tagName] = tagType
					markPrivate(tagName)
					storeName := packedEnumStoreTypeName(qualifiedName)
					if _, exists := a.namedTypes[storeName]; exists {
						a.errorf(n.Pos(), "%s", DuplicateTypeMessage(storeName))
						return
					}
					storeType := &PackedEnumStoreType{Name: storeName, Enum: enumType}
					enumType.StoreType = storeType
					a.namedTypes[storeName] = storeType
					markPrivate(storeName)
				}
			case *ast.ExternTypeDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if _, exists := a.namedTypes[qualifiedName]; exists {
					a.errorf(n.Pos(), "%s", DuplicateTypeMessage(qualifiedName))
					return
				}
				opaque := &OpaqueType{Name: qualifiedName}
				a.applyExternTypeAnnotations(n, opaque)
				a.namedTypes[qualifiedName] = opaque
				markPrivate(qualifiedName)
			case *ast.ErrorDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if _, exists := a.namedTypes[qualifiedName]; exists {
					a.errorf(n.Pos(), "%s", DuplicateTypeMessage(qualifiedName))
					return
				}
				seenTags := map[string]bool{}
				resolvedTags := make([]string, 0, len(n.Tags))
				payloads := map[string][]Type{}
				for _, tag := range n.Tags {
					if seenTags[tag.Name] {
						a.errorf(tag.Position, "duplicate error tag %q in error set %q", tag.Name, n.Name)
						continue
					}
					seenTags[tag.Name] = true
					qualifiedTag := QualifyErrorTag(qualifiedName, tag.Name)
					resolvedTags = append(resolvedTags, qualifiedTag)
					if len(tag.Payload) != 0 {
						payloadTypes := make([]Type, 0, len(tag.Payload))
						for _, payload := range tag.Payload {
							payloadTypes = append(payloadTypes, a.resolveType(payload.Type))
						}
						payloads[qualifiedTag] = payloadTypes
					}
				}
				a.namedTypes[qualifiedName] = &ErrorSetType{Name: qualifiedName, Tags: resolvedTags, Payloads: payloads}
				markPrivate(qualifiedName)
			case *ast.PermissionDecl:
			case *ast.TypeAliasDecl, *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
			}
		})
	}
}
