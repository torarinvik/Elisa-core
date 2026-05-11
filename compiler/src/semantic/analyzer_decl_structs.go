package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) populateStructFields(decls []scopedDecl) {
	for _, scoped := range decls {
		var stDecl *ast.StructDecl
		var storeDecl *ast.StoreDecl
		switch decl := scoped.Decl.(type) {
		case *ast.StructDecl:
			stDecl = decl
		case *ast.StoreDecl:
			storeDecl = decl
		default:
			continue
		}
		typeName := ""
		if stDecl != nil {
			typeName = stDecl.Name
		} else {
			typeName = storeDecl.Name
		}
		st, _ := a.namedTypes[joinQualifiedName(scoped.Namespace, typeName)].(*StructType)
		if st == nil {
			continue
		}
		if stDecl != nil && st.Builtin && isBuiltinRuntimeStructName(stDecl.Name) {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			if storeDecl != nil || (stDecl != nil && stDecl.Layout == ast.StructLayoutSOA) {
				if storeDecl == nil {
					storeDecl = synthesizedStoreDeclFromStruct(stDecl)
				}
				for _, field := range storeDecl.Fields {
					if _, exists := st.Fields[field.Name]; exists {
						typeKind := "store"
						if stDecl != nil {
							typeKind = "layout soa struct"
						}
						a.errorf(field.Position, "duplicate field %q in %s %q", field.Name, typeKind, storeDecl.Name)
						continue
					}
					fieldType := a.resolveType(field.Type)
					st.Fields[field.Name] = Field{
						Name:    field.Name,
						Type:    &DArrayType{Elem: fieldType, Shape: &WildcardShape{}, SurfaceName: "darray"},
						Mutable: true,
					}
					st.StoreFieldOrder = append(st.StoreFieldOrder, field.Name)
				}
				return
			}
			a.analyzeStructAnnotations(stDecl, st)
			a.withGenericParams(stDecl.GenericParams, nil, func() {
				a.withRegionParams(stDecl.RegionParams, func() {
					for _, field := range stDecl.Fields {
						if len(field.Annotations) != 0 {
							for _, annotation := range field.Annotations {
								a.errorf(annotation.Position, "field annotation @%s is only supported on packed enum common fields", annotation.Name)
							}
						}
						if _, exists := st.Fields[field.Name]; exists {
							a.errorf(field.Position, "duplicate field %q in struct %q", field.Name, stDecl.Name)
							continue
						}
						if field.BitGroup != nil {
							groupType := a.resolveBitGroupType(stDecl.Name+"."+field.Name, field.BitGroup)
							st.HasPackedGroups = true
							st.Fields[field.Name] = Field{
								Name: field.Name,
								Type: groupType,
							}
							continue
						}
						fieldType := a.resolveType(field.Type)
						if field.IsTail {
							fieldType = &RefType{Elem: fieldType, State: RefStateNonNull, Storage: RefStorageAny}
						}
						st.Fields[field.Name] = Field{
							Name:    field.Name,
							Type:    fieldType,
							Mutable: field.Mutable,
							IsTail:  field.IsTail,
						}
					}
					a.validateStructDerivedStates(stDecl, st)
				})
			})
		})
	}
}

func (a *Analyzer) resolveBitGroupType(name string, groupDecl *ast.BitGroupDecl) *BitGroupType {
	group := &BitGroupType{
		Name:      name,
		MemberMap: map[string]BitGroupMember{},
		Decl:      groupDecl,
	}
	if groupDecl == nil {
		return group
	}
	switch groupDecl.Kind {
	case ast.BitGroupBitset:
		group.Kind = BitGroupBitset
	case ast.BitGroupBitfield:
		group.Kind = BitGroupBitfield
	default:
		a.errorf(groupDecl.Position, "unknown packed group kind")
		group.Kind = BitGroupBitfield
	}
	offset := 0
	for i := range groupDecl.Members {
		memberDecl := &groupDecl.Members[i]
		if _, exists := group.MemberMap[memberDecl.Name]; exists {
			a.errorf(memberDecl.Position, "duplicate packed group member %q in %s", memberDecl.Name, name)
			continue
		}
		memberType := Type(&BuiltinType{Name: "bool"})
		width := 1
		signed := false
		if group.Kind == BitGroupBitfield {
			if memberDecl.Type == nil {
				a.errorf(memberDecl.Position, "bitfield member %q requires an explicit bit integer type", memberDecl.Name)
				continue
			}
			memberType = a.resolveType(memberDecl.Type)
			var ok bool
			signed, width, ok = BitIntInfo(memberType)
			if !ok {
				if storage, storageOK := ConstEnumStorageType(memberType); storageOK {
					signed, width, ok = BitIntInfo(storage)
				}
			}
			if !ok {
				a.errorf(memberDecl.Type.Pos(), "bitfield member %q requires a bit integer or const enum storage type, got %s", memberDecl.Name, memberType)
				continue
			}
		}
		if offset+width > 64 {
			a.errorf(memberDecl.Position, "packed group %s exceeds 64 bits", name)
			continue
		}
		member := BitGroupMember{Name: memberDecl.Name, Type: memberType, Offset: offset, Width: width, Signed: signed, Decl: memberDecl}
		group.Members = append(group.Members, member)
		group.MemberMap[member.Name] = member
		offset += width
	}
	group.BackingWidth = BitGroupBackingWidth(offset)
	return group
}

func (a *Analyzer) validateStructDerivedStates(stDecl *ast.StructDecl, st *StructType) {
	if stDecl == nil || st == nil {
		return
	}
	st.NamedStateCases = append([]string(nil), stDecl.NamedStateCases...)
	if len(stDecl.NamedStateCases) == 0 {
		if len(stDecl.DerivedStates) != 0 {
			a.errorf(stDecl.DerivedStates[0].Position, "derive state: requires a named struct state parameter like [state Alive | Dead]")
		}
		return
	}
	if len(stDecl.DerivedStates) == 0 {
		a.errorf(stDecl.Pos(), "struct %q declares named states but is missing a derive state: block", stDecl.Name)
		return
	}
	declared := make(map[string]bool, len(stDecl.NamedStateCases))
	for _, name := range stDecl.NamedStateCases {
		declared[name] = true
	}
	seen := map[string]bool{}
	savedScope := a.currentScope
	scope := NewScope(nil)
	scope.Define(&Symbol{Name: "self", Kind: SymbolLocal, Type: DefaultNamedStateType(st)})
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	st.DerivedStates = nil
	st.DerivedStateMap = map[string]*StructDerivedState{}
	for _, derived := range stDecl.DerivedStates {
		if !declared[derived.StateName] {
			a.errorf(derived.Position, "unknown derived state %q for struct %q", derived.StateName, stDecl.Name)
			continue
		}
		if seen[derived.StateName] {
			a.errorf(derived.Position, "duplicate derived state rule for %q in struct %q", derived.StateName, stDecl.Name)
			continue
		}
		seen[derived.StateName] = true
		if !a.isSupportedDerivedStateExpr(derived.Condition) {
			a.errorf(derived.Position, "derived state rule for %q must be a pure field-based expression over self", derived.StateName)
			continue
		}
		condType := a.analyzeExpr(derived.Condition)
		if !IsBoolType(condType) {
			a.errorf(derived.Condition.Pos(), "derived state rule for %q must evaluate to bool, got %s", derived.StateName, condType)
			continue
		}
		st.DerivedStates = append(st.DerivedStates, StructDerivedState{Name: derived.StateName, Condition: derived.Condition})
	}
	for _, name := range stDecl.NamedStateCases {
		if !seen[name] {
			a.errorf(stDecl.Pos(), "struct %q is missing a derived state rule for %q", stDecl.Name, name)
		}
	}
	for i := range st.DerivedStates {
		state := &st.DerivedStates[i]
		st.DerivedStateMap[state.Name] = state
	}
}

func (a *Analyzer) isSupportedDerivedStateExpr(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.isSupportedDerivedStateExpr(n.Inner)
	case *ast.Ident:
		return n.Name == "self"
	case *ast.FieldExpr:
		return a.isSupportedDerivedStateExpr(n.Object)
	case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.CharLit, *ast.BoolLit, *ast.NullLit:
		return true
	case *ast.UnaryExpr:
		switch n.Op {
		case lexer.TOKEN_NOT, lexer.TOKEN_MINUS, lexer.TOKEN_TILDE:
			return a.isSupportedDerivedStateExpr(n.Operand)
		default:
			return false
		}
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND, lexer.TOKEN_OR,
			lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ,
			lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_GT, lexer.TOKEN_GTEQ,
			lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT,
			lexer.TOKEN_PIPE, lexer.TOKEN_CARET, lexer.TOKEN_AMPERSAND,
			lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
			return a.isSupportedDerivedStateExpr(n.Left) && a.isSupportedDerivedStateExpr(n.Right)
		default:
			return false
		}
	default:
		return false
	}
}
