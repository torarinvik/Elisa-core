//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) conditionTargetPattern(expr ast.Expr) (ast.MatchPattern, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.conditionTargetPattern(n.Inner)
	case *ast.IsAliasExpr:
		return s.conditionTargetPattern(n.Target)
	case *ast.StructTestExpr:
		if n != nil && n.Pattern != nil {
			return n.Pattern, true
		}
	case *ast.VariantTestExpr:
		if n != nil && n.Pattern != nil {
			return n.Pattern, true
		}
	}
	return nil, false
}
func (s *functionState) directConditionPattern(expr ast.Expr) (ast.Expr, ast.MatchPattern, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.directConditionPattern(n.Inner)
	case *ast.BinaryExpr:
		if n.Op != lexer.TOKEN_IS {
			return nil, nil, false
		}
		right := n.Right
		if alias, ok := right.(*ast.IsAliasExpr); ok && alias != nil {
			right = alias.Target
		}
		pattern, ok := s.conditionTargetPattern(right)
		if !ok || pattern == nil {
			return nil, nil, false
		}
		return n.Left, pattern, true
	default:
		return nil, nil, false
	}
}

func backendMatchableEnumType(actual semantic.Type) (*semantic.EnumType, bool) {
	switch tt := actual.(type) {
	case *semantic.EnumType:
		return tt, tt != nil
	case *semantic.PackedVariantViewType:
		if tt != nil && tt.Enum != nil {
			return tt.Enum, true
		}
	}
	return nil, false
}

func (s *functionState) conditionAliasBindingType(target ast.Expr, leftType semantic.Type) (string, semantic.Type, bool) {
	alias, ok := target.(*ast.IsAliasExpr)
	if !ok || alias == nil || alias.Alias == "" || alias.Alias == "_" {
		return "", nil, false
	}
	if enumType, variant, _, ok := s.enumIsTargetPattern(alias.Target); ok && enumType != nil && variant != nil {
		actualEnum, ok := backendMatchableEnumType(leftType)
		if ok && actualEnum != nil && actualEnum.Name == enumType.Name {
			return alias.Alias, variant.PackedViewType(actualEnum), true
		}
	}
	// docs/77 §2: `e is Statement s` — a bare-category target binds at the NARROWED category
	// type (same value, narrower static type), mirroring the analyzer's conditionAliasBindingType.
	if actualEnum, ok := backendMatchableEnumType(semantic.StripAggregateStateType(leftType)); ok {
		if category, ok := s.enumCategoryAliasTarget(actualEnum, alias.Target); ok {
			return alias.Alias, category, true
		}
	}
	return alias.Alias, leftType, true
}

// enumCategoryAliasTarget resolves a bare-category alias target (`is Statement s` / `is Statement
// as s`) against a hierarchy scrutinee enum: the target must name an enum that descends from the
// scrutinee (downward narrowing). Widening aliases keep the scrutinee type via the caller's
// fallthrough.
func (s *functionState) enumCategoryAliasTarget(enumType *semantic.EnumType, target ast.Expr) (*semantic.EnumType, bool) {
	if enumType == nil || (enumType.Parent == nil && len(enumType.Children) == 0) {
		return nil, false
	}
	name := ""
	switch e := target.(type) {
	case *ast.TypeExprExpr:
		named, ok := e.Type.(*ast.NamedType)
		if !ok || named == nil {
			return nil, false
		}
		name = named.Name
	case *ast.Ident:
		if e == nil {
			return nil, false
		}
		name = e.Name
	default:
		return nil, false
	}
	category, ok := s.g.namedType(name).(*semantic.EnumType)
	if !ok || category == nil || !semantic.EnumDescendsFrom(category, enumType) {
		return nil, false
	}
	return category, true
}

func backendConditionAliasNeedsValueSlot(typ semantic.Type) bool {
	switch semantic.StripAggregateStateType(typ).(type) {
	case *semantic.PackedVariantViewType:
		return false
	default:
		return true
	}
}

func (s *functionState) bindConditionAliasValue(expr *ast.BinaryExpr) error {
	if expr == nil || expr.Op != lexer.TOKEN_IS {
		return nil
	}
	name, typ, ok := s.conditionAliasBindingType(expr.Right, s.exprType(expr.Left))
	if !ok || name == "" || typ == nil || !backendConditionAliasNeedsValueSlot(typ) {
		return nil
	}
	binding, ok := s.lookupBinding(name)
	if !ok || binding.ptr == nil {
		return nil
	}
	value, _, err := s.emitExpr(expr.Left, typ)
	if err != nil {
		return err
	}
	llvmBuildStore(s.builder, value, binding.ptr)
	return nil
}
func (s *functionState) optionalBindSourceType(expr *ast.OptionalBindExpr) semantic.Type {
	if expr == nil {
		return nil
	}
	// Generic bodies are emitted once per concrete instantiation. The analyzer's
	// OptionalBindSourceTypes table is populated by the template pass, so it can
	// still contain `T?` while this body is being lowered as `i64?`. Prefer the
	// monomorphized expression overlay; otherwise extractOptionalPayload lowers
	// the opaque type parameter as a pointer and feeds the wrong ABI to callbacks.
	if len(s.specializedExprTypes) != 0 {
		if valueType, ok := s.specializedExprTypes[expr]; ok && valueType != nil {
			return valueType
		}
	}
	if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnOptionalBindSourceTypesMap() {
		if valueType, ok := s.g.semanticReadView().LookupOptionalBindSourceTypes(expr); ok && valueType != nil {
			return substituteType(valueType, s.typeMap, s.g.result.StaticImpls)
		}
	}
	return s.exprType(expr.Value)
}

// backendOptionalBindBoundType mirrors the analyzer's optionalBindBoundType: a
// slice operand (`if arr[a:b] is s:`) binds the bounded view, so its slot is
// allocated with the view type; other operands use the optional/nullable unwrap.
func (s *functionState) backendOptionalBindBoundType(expr *ast.OptionalBindExpr) (semantic.Type, bool) {
	if _, ok := unwrapToSliceForBind(expr.Value); ok {
		viewType := s.optionalBindSourceType(expr)
		if opt, isOpt := viewType.(*semantic.OptionalType); isOpt && opt != nil {
			return opt.Value, true
		}
		return viewType, true
	}
	return backendConditionOptionalBindType(s.optionalBindSourceType(expr))
}

func (s *functionState) emitOptionalBindSourceValue(expr ast.Expr, sourceType semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	if expr == nil || sourceType == nil {
		return nil, nil, fmt.Errorf("invalid let condition source")
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.emitOptionalBindSourceValue(n.Inner, sourceType)
	}
	if ptr, storedType, err := s.emitAddress(expr); err == nil && storedType != nil && semantic.SameType(storedType, sourceType) {
		value, loadErr := s.loadValue(ptr, sourceType, "cond.let.source")
		return value, sourceType, loadErr
	}
	return s.emitExpr(expr, sourceType)
}
func (s *functionState) emitOptionalBindTest(expr *ast.OptionalBindExpr) (llvmIRValueRef, llvmIRValueRef, semantic.Type, error) {
	if expr == nil || expr.Value == nil {
		return nil, nil, nil, fmt.Errorf("invalid let condition")
	}
	// `if arr[a:b] is s:` — a bounds-checked slice. "Present" means the bounds
	// are within the source; the bound value is the bounded view.
	if slice, ok := unwrapToSliceForBind(expr.Value); ok {
		usizeType := s.g.namedType("usize")
		startValue, _, err := s.emitExpr(slice.Start, usizeType)
		if err != nil {
			return nil, nil, nil, err
		}
		endValue, _, err := s.emitExpr(slice.End, usizeType)
		if err != nil {
			return nil, nil, nil, err
		}
		lenValue, err := s.emitSliceSourceLength(slice)
		if err != nil {
			return nil, nil, nil, err
		}
		startLEEnd := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULE), startValue, endValue, cStringFree("cond.slice.start_le_end"))
		endLELen := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULE), endValue, lenValue, cStringFree("cond.slice.end_le_len"))
		presentValue := llvmBuildAnd(s.builder, startLEEnd, endLELen, cStringFree("cond.slice.in_range"))
		viewValue, viewType, err := s.emitSliceExpr(slice)
		if err != nil {
			return nil, nil, nil, err
		}
		return presentValue, viewValue, viewType, nil
	}
	valueType := s.optionalBindSourceType(expr)
	if optionalType, ok := valueType.(*semantic.OptionalType); ok {
		fallibleValue, _, err := s.emitOptionalBindSourceValue(expr.Value, valueType)
		if err != nil {
			return nil, nil, nil, err
		}
		presentValue, err := s.extractOptionalPresent(fallibleValue, optionalType)
		if err != nil {
			return nil, nil, nil, err
		}
		payloadValue, err := s.extractOptionalPayload(fallibleValue, optionalType)
		if err != nil {
			return nil, nil, nil, err
		}
		return presentValue, payloadValue, optionalType.Value, nil
	}
	if refType, ok := valueType.(*semantic.RefType); ok && refType.State == semantic.RefStateNullable {
		refValue, _, err := s.emitExpr(expr.Value, valueType)
		if err != nil {
			return nil, nil, nil, err
		}
		nullValue := llvmConstNull(llvmTypeOf(refValue))
		presentValue := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntNE), refValue, nullValue, cStringFree("cond.let.present"))
		boundType, _ := backendConditionOptionalBindType(valueType)
		return presentValue, refValue, boundType, nil
	}
	return nil, nil, nil, fmt.Errorf("let condition requires an optional or nullable reference")
}
func unwrapToSliceForBind(expr ast.Expr) (*ast.SliceExpr, bool) {
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.SliceExpr:
			return n, true
		default:
			return nil, false
		}
	}
}

func (s *functionState) emitOptionalBindExpr(expr *ast.OptionalBindExpr) (llvmIRValueRef, semantic.Type, error) {
	presentValue, _, _, err := s.emitOptionalBindTest(expr)
	if err != nil {
		return nil, nil, err
	}
	return presentValue, s.g.namedType("bool"), nil
}
func (s *functionState) emitOptionalBindCondition(expr *ast.OptionalBindExpr, trueBB llvmIRBlockRef, falseBB llvmIRBlockRef, hint ast.BranchHint) error {
	presentValue, boundValue, _, err := s.emitOptionalBindTest(expr)
	if err != nil {
		return err
	}
	if expr.Name == "" || expr.Name == "_" {
		s.buildCondBrWithHint(presentValue, trueBB, falseBB, hint)
		return nil
	}
	bindBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("cond.let.bind"))
	s.buildCondBrWithHint(presentValue, bindBB, falseBB, hint)
	llvmPositionBuilderAtEnd(s.builder, bindBB)
	if binding, ok := s.lookupBinding(expr.Name); ok && binding.ptr != nil {
		llvmBuildStore(s.builder, boundValue, binding.ptr)
	}
	llvmBuildBr(s.builder, trueBB)
	return nil
}
func stripOptionalAssignTargetExpr(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok || paren == nil {
			return expr
		}
		expr = paren.Inner
	}
}
func (s *functionState) emitOptionalAssignThroughNullableRef(refValue llvmIRValueRef, elemType semantic.Type, valueExpr ast.Expr, name string) error {
	if refValue == nil || elemType == nil {
		return fmt.Errorf("invalid ?= target")
	}
	nullValue := llvmConstNull(llvmTypeOf(refValue))
	presentValue := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntNE), refValue, nullValue, cStringFree(name+".present"))
	assignBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".assign"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".merge"))
	llvmBuildCondBr(s.builder, presentValue, assignBB, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, assignBB)
	value, _, err := s.emitExpr(valueExpr, elemType)
	if err != nil {
		return err
	}
	llvmBuildStore(s.builder, value, refValue)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	return nil
}
func (s *functionState) emitOptionalAssignStmt(stmt *ast.AssignStmt) error {
	if stmt == nil || stmt.Target == nil || stmt.Value == nil {
		return fmt.Errorf("invalid ?= statement")
	}
	s.invalidatePackedEnumStorageExpr(stmt.Target)
	s.invalidatePackedEnumStoreOriginExpr(stmt.Target)
	s.invalidatePackedCommonFieldValuesExpr(stmt.Target)
	s.invalidatePackedVariantViewExpr(stmt.Target)
	s.invalidatePackedReadCaches()
	switch target := stripOptionalAssignTargetExpr(stmt.Target).(type) {
	case *ast.Ident:
		targetType := s.exprType(target)
		refType, ok := targetType.(*semantic.RefType)
		if !ok || refType == nil || refType.State != semantic.RefStateNullable {
			return fmt.Errorf("?= requires a nullable reference target")
		}
		refValue, _, err := s.emitExpr(target, targetType)
		if err != nil {
			return err
		}
		return s.emitOptionalAssignThroughNullableRef(refValue, refType.Elem, stmt.Value, "opt.assign.ident")
	case *ast.FieldExpr:
		if !target.Safe {
			return fmt.Errorf("?= requires optional chaining on field targets")
		}
		presentValue, receiverValue, receiverType, err := s.emitSafeChainReceiverValue(target.Object)
		if err != nil {
			return err
		}
		assignBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("opt.assign.field.assign"))
		mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("opt.assign.field.merge"))
		llvmBuildCondBr(s.builder, presentValue, assignBB, mergeBB)

		llvmPositionBuilderAtEnd(s.builder, assignBB)
		fieldPtr, fieldType, err := s.emitFieldAddressFromObjectValue(receiverValue, receiverType, target.Field, "opt.assign.field")
		if err != nil {
			return err
		}
		value, _, err := s.emitExpr(stmt.Value, fieldType)
		if err != nil {
			return err
		}
		llvmBuildStore(s.builder, value, fieldPtr)
		llvmBuildBr(s.builder, mergeBB)

		llvmPositionBuilderAtEnd(s.builder, mergeBB)
		return nil
	default:
		return fmt.Errorf("invalid ?= target")
	}
}
