package semantic

import (
	"strings"

	"elisacore/src/ast"
)

// resolveEnumParent wires the `enum Child is Parent:` sealed-refinement relation (docs/77). The
// parent must be an enum; cycles are rejected. Child <: Parent is then established in assignability
// (enumDescendsFrom). Runs in populateEnumVariants, after all enum skeletons exist, so a parent may be
// declared in any order or another file.
func (a *Analyzer) resolveEnumParent(enumDecl *ast.EnumDecl, enumType *EnumType) {
	if enumDecl.Parent == "" {
		return
	}
	base, _, ok := a.lookupVisibleType(enumDecl.Parent)
	if !ok {
		a.errorf(enumDecl.Pos(), "enum %q refines unknown type %q", enumDecl.Name, enumDecl.Parent)
		return
	}
	parentEnum, ok := base.(*EnumType)
	if !ok || parentEnum == nil {
		a.errorf(enumDecl.Pos(), "enum %q can only refine another enum with `is`, but %q is %s", enumDecl.Name, enumDecl.Parent, base)
		return
	}
	if parentEnum == enumType {
		a.errorf(enumDecl.Pos(), "enum %q cannot refine itself", enumDecl.Name)
		return
	}
	for ancestor := parentEnum; ancestor != nil; ancestor = ancestor.Parent {
		if ancestor == enumType {
			a.errorf(enumDecl.Pos(), "enum %q refinement cycle through %q", enumDecl.Name, enumDecl.Parent)
			return
		}
	}
	enumType.Parent = parentEnum
	parentEnum.Children = append(parentEnum.Children, enumType)
}

// enumDescendsFrom reports whether src is dst or a (transitive) refinement of dst — the sealed
// subtype walk for `enum Child is Parent:` (docs/77), mirroring treeCategoryDescendsFrom.
func enumDescendsFrom(src *EnumType, dst *EnumType) bool {
	for current := src; current != nil; current = current.Parent {
		if SameType(current, dst) {
			return true
		}
	}
	return false
}

// assignHierarchyEnumTags assigns each hierarchy root a dense, hierarchy-grouped leaf-tag space
// (docs/77 Phase 2 groundwork). Leaves are numbered in pre-order (a node's own leaves before its
// refinements'), so every category occupies a contiguous, NESTED range [LeafTagLo, +LeafTagCount):
// `is X` becomes one unsigned range check and an upcast is a no-op. Each root gets its own space
// starting at 0 (one store per root). Plain enums (no hierarchy) are left untouched. Roots are
// processed in declaration order for determinism; per-root numbering is order-independent.
func (a *Analyzer) assignHierarchyEnumTags(decls []scopedDecl) {
	for _, scoped := range decls {
		enumDecl, ok := scoped.Decl.(*ast.EnumDecl)
		if !ok {
			continue
		}
		root, _ := a.namedTypes[joinQualifiedName(scoped.Namespace, enumDecl.Name)].(*EnumType)
		if root == nil || root.Parent != nil || len(root.Children) == 0 {
			continue // only roots of a hierarchy
		}
		next := uint32(0)
		var visit func(n *EnumType)
		visit = func(n *EnumType) {
			lo := next
			for _, variant := range n.Variants {
				variant.Tag = next
				next++
			}
			for _, child := range n.Children {
				visit(child)
			}
			n.LeafTagLo = lo
			n.LeafTagCount = next - lo
		}
		visit(root)
	}
}

// enumIsHierarchical reports whether the enum participates in a sealed hierarchy (docs/77) — it is a
// refinement of another enum, or has its own refinements. Plain standalone enums are not hierarchical
// and keep the original flat match/exhaustiveness behavior.
func enumIsHierarchical(e *EnumType) bool {
	return e != nil && (e.Parent != nil || len(e.Children) > 0)
}

// enumLeafItem is a leaf variant reachable from a hierarchy node, with the concrete enum that owns it.
type enumLeafItem struct {
	Owner     *EnumType
	Variant   *EnumVariant
	Qualified string // "Owner.Variant"
}

// enumDescendantLeaves returns every leaf variant of root and all its (transitive) refinements, in
// declaration order — the full matchable case-set of a hierarchy node (leaves flow up, docs/77).
func enumDescendantLeaves(root *EnumType) []enumLeafItem {
	if root == nil {
		return nil
	}
	var items []enumLeafItem
	var visit func(e *EnumType)
	visit = func(e *EnumType) {
		for _, variant := range e.Variants {
			items = append(items, enumLeafItem{Owner: e, Variant: variant, Qualified: e.Name + "." + variant.Name})
		}
		for _, child := range e.Children {
			visit(child)
		}
	}
	visit(root)
	return items
}

// resolveEnumMatchPatternCategory resolves a match arm's `Owner.Variant` against a hierarchy scrutinee
// of type expected: the arm may name expected itself or any descendant enum (docs/77), mirroring
// resolveTreeMatchPatternCategory. Returns the owning enum and the variant.
func (a *Analyzer) resolveEnumMatchPatternCategory(expected *EnumType, pattern *ast.MatchVariantPattern) (*EnumType, *EnumVariant, bool) {
	owner := expected
	if pattern.EnumName != expected.Name {
		base, _, ok := a.lookupVisibleType(pattern.EnumName)
		if !ok {
			a.errorf(pattern.Pos(), "match arm expects enum %q or a refinement of it, got %q", expected.Name, pattern.EnumName)
			return nil, nil, false
		}
		resolved, ok := StripAggregateStateType(base).(*EnumType)
		if !ok || resolved == nil || !enumDescendsFrom(resolved, expected) {
			a.errorf(pattern.Pos(), "match arm expects enum %q or a refinement of it, got %q", expected.Name, pattern.EnumName)
			return nil, nil, false
		}
		owner = resolved
	}
	variant, ok := owner.Variant(pattern.Variant)
	if !ok {
		a.errorf(pattern.Pos(), "enum %q has no variant %q", owner.Name, pattern.Variant)
		return nil, nil, false
	}
	return owner, variant, true
}

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
		members := make([]*ConstEnumMember, 0, len(constEnumDecl.Members))
		values := make([]int64, 0, len(constEnumDecl.Members))
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
			values = append(values, value)
			nextValue = value + 1
		}
		var storageType Type
		if constEnumDecl.Storage != nil {
			storageType = a.resolveType(constEnumDecl.Storage)
			if !IsIntegralStorageType(storageType) {
				a.errorf(constEnumDecl.Storage.Pos(), "const enum %q storage type must be an explicit integer type, got %s", constEnumDecl.Name, storageType)
			}
		} else {
			storageType = InferBitIntTypeForValues(values)
		}
		constEnumType.Storage = storageType
		for _, member := range members {
			if !IntegerTypeFitsValue(storageType, member.Value) {
				a.errorf(member.Decl.Pos(), "const enum member %q.%q value %d does not fit storage type %s", constEnumDecl.Name, member.Name, member.Value, storageType)
			}
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

func treeCategoryParentMemberName(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx <= 0 {
		return ""
	}
	return name[:idx]
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
			a.resolveEnumParent(enumDecl, enumType)
			a.validateEnumLayout(enumDecl)
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
