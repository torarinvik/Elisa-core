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
							typeKind = "struct ... layout(soa)"
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
					concreteFields := stDecl.Fields[:0:0]
					for _, field := range stDecl.Fields {
						if field.Ghost {
							// A ghost field is verification-only model state: record its type in st.Fields so
							// contracts can resolve `self.<field>`, but DROP it from the concrete field list so
							// codegen never lays it out (zero impact on real layout/size/offsets). Reads outside
							// a contract/ghost context are rejected at field-access analysis (Field.Ghost).
							if field.BitGroup != nil || field.IsTail {
								a.errorf(field.Position, "ghost field %q cannot be a packed bit group or tail field", field.Name)
								continue
							}
							if field.DefaultValue != nil {
								a.errorf(field.Position, "ghost field %q cannot have a default value: it is verification-only and erased, so it has no runtime initializer; assign it in a `ghost`/contract context instead", field.Name)
							}
							if _, exists := st.Fields[field.Name]; exists {
								a.errorf(field.Position, "duplicate field %q in struct %q", field.Name, stDecl.Name)
								continue
							}
							fieldType := a.resolveType(field.Type)
							st.Fields[field.Name] = Field{Name: field.Name, Type: fieldType, Mutable: true, Ghost: true}
							st.GhostFieldOrder = append(st.GhostFieldOrder, field.Name)
							continue
						}
						concreteFields = append(concreteFields, field)
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
						a.analyzeStructFieldWhereRefinement(field, fieldType, concreteFields[:len(concreteFields)-1])
					}
					// Drop ghost fields from the concrete field list so codegen (and constructor
					// arity) never see them — guaranteeing zero layout/size/offset impact.
					if len(st.GhostFieldOrder) != 0 {
						stDecl.Fields = concreteFields
					}
					// Mark typestate state fields for erasure: if this struct has a `state` generic
					// parameter, drop the __typestate field from the concrete field list so codegen never
					// lays it out (docs/111 S0). This ensures the typestate-annotated struct has the same
					// runtime layout as the non-typestate version.
					a.markTypestateStateFieldsForErasure(stDecl, st)
					a.validateStructDerivedStates(stDecl, st)
					a.analyzeStructInvariants(stDecl, st)
					a.checkPointerGraphStruct(stDecl, st)
				})
			})
		})
	}
	// After every struct's fields are resolved, run the program-wide pointer-graph cycle
	// pass (docs/70): mutual-recursion raw-ref loops (A→B→A) that the per-struct direct
	// self-reference check cannot see.
	a.checkPointerGraphCycles(decls)
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

// analyzeStructInvariants type-checks a struct's field invariants (`invariant <bool-expr>` in the
// struct body) in a scope binding `self` to the struct, so they reference `self.field`. Each must be
// bool. The backend emits the checks after construction and after each `s.field <- ...` store.
func (a *Analyzer) analyzeStructInvariants(stDecl *ast.StructDecl, st *StructType) {
	if stDecl == nil || len(stDecl.Invariants) == 0 {
		return
	}
	saved := a.currentScope
	scope := NewScope(nil)
	scope.Define(&Symbol{Name: "self", Kind: SymbolLocal, Type: DefaultNamedStateType(st)})
	a.currentScope = scope
	defer func() { a.currentScope = saved }()
	// A struct invariant is a contract position: ghost model fields (`ghost name: T`) are readable
	// here so an invariant can relate concrete representation to the abstract model.
	a.ghostReadAllowed++
	defer func() { a.ghostReadAllowed-- }()
	// An invariant that reads a ghost model field is verification-only: the ghost is erased from
	// codegen, so the invariant has no runtime representation and must be dropped from the backend's
	// debug-recheck set (kept only for static discharge, where it is seeded as a method-entry fact).
	runtimeInvariants := stDecl.Invariants[:0:0]
	for _, inv := range stDecl.Invariants {
		if inv == nil {
			continue
		}
		a.ghostReadSeen = false
		t := a.analyzeExpr(inv)
		if t != nil && !IsBoolType(t) {
			a.errorf(inv.Pos(), "struct invariant must be bool, got %s", t)
		}
		if !a.ghostReadSeen {
			runtimeInvariants = append(runtimeInvariants, inv)
		}
	}
	// Preserve the full invariant list on the semantic type for static discharge (method-entry
	// assumption seeding); strip ghost-referencing ones from the AST so the backend never emits a
	// runtime check that would dereference an erased field.
	st.Invariants = append([]ast.Expr(nil), stDecl.Invariants...)
	stDecl.Invariants = runtimeInvariants
}

func (a *Analyzer) validateStructDerivedStates(stDecl *ast.StructDecl, st *StructType) {
	if stDecl == nil || st == nil {
		return
	}
	st.NamedStateCases = append([]string(nil), stDecl.NamedStateCases...)
	st.TerminalStateCases = append([]string(nil), stDecl.TerminalStateCases...)
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
	seenTerminal := map[string]bool{}
	for _, name := range stDecl.TerminalStateCases {
		if !declared[name] {
			a.errorf(stDecl.Pos(), "struct %q declares unknown terminal state %q", stDecl.Name, name)
			continue
		}
		if seenTerminal[name] {
			a.errorf(stDecl.Pos(), "struct %q declares duplicate terminal state %q", stDecl.Name, name)
			continue
		}
		seenTerminal[name] = true
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
		// Qualified enum variants are immutable constants and are therefore safe
		// operands in a derived-state predicate, e.g. `self.phase == Phase.Cold`.
		// Keep this check side-effect free; analyzeExpr below remains responsible
		// for reporting an unknown enum or variant.
		if a.isDerivedStateEnumVariantExpr(n) {
			return true
		}
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

func (a *Analyzer) isDerivedStateEnumVariantExpr(expr *ast.FieldExpr) bool {
	if expr == nil {
		return false
	}
	baseName, ok := qualifiedTypePathFromExpr(expr.Object)
	if !ok {
		return false
	}
	base, _, ok := a.lookupVisibleType(baseName)
	if !ok {
		return false
	}
	enumType, ok := base.(*EnumType)
	if !ok || enumType == nil {
		return false
	}
	_, ok = enumType.Variant(expr.Field)
	return ok
}

// analyzeStructFieldWhereRefinement validates the where predicate on a struct field declaration.
// The predicate is analyzed in a scope where the field name and any earlier-declared sibling field
// names are bound to their base types, so that cross-field predicates like `hi >= lo` resolve
// correctly. References to later fields or unknown names produce clear diagnostics.
// The field's runtime type is the BASE type (erasure is already done by resolveType); this only
// records the predicate for later discharge at construction sites.
//
// earlierFields contains the concrete fields that precede this field in declaration order (not
// including the field itself); ghost and bit-group fields are excluded by the caller.
func (a *Analyzer) analyzeStructFieldWhereRefinement(field ast.FieldDecl, baseType Type, earlierFields []ast.FieldDecl) {
	if field.Type == nil {
		return
	}
	ft := field.Type
	if mt, ok := ft.(*ast.MutableType); ok && mt != nil {
		ft = mt.Elem
	}
	wt, ok := whereRefinementTypeExpr(ft)
	if !ok || wt == nil || wt.Predicate == nil {
		return
	}
	// Build the set of allowed names: the field itself + earlier siblings.
	earlierSet := make(map[string]bool, len(earlierFields)+1)
	earlierSet[field.Name] = true
	for _, ef := range earlierFields {
		earlierSet[ef.Name] = true
	}
	// Validate that every identifier in the predicate is either the field itself, an earlier
	// sibling, a known type name, or a boolean literal. References to later fields are rejected
	// with a diagnostic that names the specific offending identifier.
	for _, name := range exprIdentNames(wt.Predicate) {
		if name == "true" || name == "false" || earlierSet[name] {
			continue
		}
		if _, isType := a.namedTypes[name]; isType {
			continue
		}
		// Unknown name — determine if it looks like a later field (for a better diagnostic).
		a.errorf(wt.Predicate.Pos(), "struct field where refinement on %q may only reference the field itself or earlier-declared fields; %q is not available here (later fields are not in scope at this point)", field.Name, name)
		return
	}
	// Analyze the predicate with the field name and all earlier sibling names bound to their
	// respective base types.
	saved := a.currentScope
	scope := NewScope(saved)
	scope.Define(&Symbol{Name: field.Name, Kind: SymbolLocal, Type: baseType})
	for _, ef := range earlierFields {
		efType := a.resolveType(ef.Type)
		scope.Define(&Symbol{Name: ef.Name, Kind: SymbolLocal, Type: efType})
	}
	a.currentScope = scope
	a.ghostReadAllowed++
	defer func() {
		a.currentScope = saved
		a.ghostReadAllowed--
	}()
	a.analyzeWhereBoolPredicate(wt.Predicate)
}
