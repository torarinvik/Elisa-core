//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"strings"
)

func appendIsTargetExprsBackend(out []ast.Expr, expr ast.Expr) []ast.Expr {
	if expr == nil {
		return out
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return appendIsTargetExprsBackend(out, n.Inner)
	case *ast.IsAliasExpr:
		return appendIsTargetExprsBackend(out, n.Target)
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			out = appendIsTargetExprsBackend(out, target)
		}
		return out
	default:
		return append(out, expr)
	}
}
func flattenIsTargetExprsBackend(expr ast.Expr) []ast.Expr {
	return appendIsTargetExprsBackend(nil, expr)
}
func (s *functionState) emitComparableIsTargetTest(leftExpr ast.Expr, targetExpr ast.Expr) (llvmIRValueRef, semantic.Type, error) {
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(targetExpr)
	resultType := s.g.namedType("bool")
	synthetic := &ast.BinaryExpr{Position: leftExpr.Pos(), Op: lexer.TOKEN_EQEQ, Left: leftExpr, Right: targetExpr}
	if helperName, firstType, secondType, swap, ok := runtimeStringCompareInfo(leftType, rightType); ok {
		return s.emitRuntimeStringCompareExpr(synthetic, helperName, firstType, secondType, swap)
	}
	if value, actualType, handled, err := s.emitOptionalCompareExpr(synthetic, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitPointerCompareExpr(synthetic, leftType, rightType, resultType); handled {
		return value, actualType, err
	}
	operandType := s.binaryOperandType(lexer.TOKEN_EQEQ, leftType, rightType)
	left, _, err := s.emitExpr(leftExpr, operandType)
	if err != nil {
		return nil, nil, err
	}
	right, _, err := s.emitExpr(targetExpr, operandType)
	if err != nil {
		return nil, nil, err
	}
	if enumType, ok := operandType.(*semantic.EnumType); ok {
		return s.emitEnumCompareExpr(lexer.TOKEN_EQEQ, enumType, left, right, resultType)
	}
	if isFloatType(operandType) {
		return llvmBuildFCmp(s.builder, llvmRealOEQ, left, right, cStringFree("isvalue.eq")), resultType, nil
	}
	return llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), left, right, cStringFree("isvalue.eq")), resultType, nil
}
func (s *functionState) emitEnumIsTest(leftExpr ast.Expr, enumType *semantic.EnumType, variant *semantic.EnumVariant, pattern *ast.MatchVariantPattern) (llvmIRValueRef, semantic.Type, error) {
	storeBinding, err := s.resolvePackedMatchStoreBinding(enumType, leftExpr, nil)
	if err != nil {
		return nil, nil, err
	}
	enumValue, _, err := s.emitExpr(leftExpr, enumType)
	if err != nil {
		return nil, nil, err
	}
	if pattern != nil && len(pattern.Args) != 0 {
		successBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.variant.ok"))
		failureBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.variant.fail"))
		contBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.variant.cont"))
		if _, _, err := s.emitMatchPatternTest(pattern, enumValue, nil, enumType, storeBinding, leftExpr, nil, successBB, failureBB); err != nil {
			return nil, nil, err
		}

		llvmPositionBuilderAtEnd(s.builder, successBB)
		successValue := llvmConstInt(llvmInt1TypeInContext(s.g.context), 1, 0)
		llvmBuildBr(s.builder, contBB)
		successEnd := llvmGetInsertBlock(s.builder)

		llvmPositionBuilderAtEnd(s.builder, failureBB)
		failureValue := llvmConstInt(llvmInt1TypeInContext(s.g.context), 0, 0)
		llvmBuildBr(s.builder, contBB)
		failureEnd := llvmGetInsertBlock(s.builder)

		llvmPositionBuilderAtEnd(s.builder, contBB)
		phi := llvmBuildPhi(s.builder, llvmInt1TypeInContext(s.g.context), cStringFree("is.variant.result"))
		values := []llvmIRValueRef{successValue, failureValue}
		blocks := []llvmIRBlockRef{successEnd, failureEnd}
		llvmAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), llvmIRUnsigned(len(values)))
		return phi, s.g.namedType("bool"), nil
	}
	tagValue, err := s.extractEnumTagValue(enumValue, nil, enumType, storeBinding)
	if err != nil {
		return nil, nil, err
	}
	tagConst, err := s.enumTagConstant(enumType, variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	cmp := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), tagValue, tagConst, cStringFree("istag"))
	return cmp, s.g.namedType("bool"), nil
}

// emitEnumCategoryIsTest lowers a bare-category `is` test (`e is Statement`, docs/77 §2) to the
// docs/81 range primitive against the category's dense leaf-tag range. A widening test (the
// scrutinee's static type already descends from the category) is constant true; an empty category
// is constant false. The scrutinee is still evaluated for effects in the constant cases.
func (s *functionState) emitEnumCategoryIsTest(leftExpr ast.Expr, scrutinee *semantic.EnumType, category *semantic.EnumType) (llvmIRValueRef, semantic.Type, error) {
	boolType := s.g.namedType("bool")
	storeBinding, err := s.resolvePackedMatchStoreBinding(scrutinee, leftExpr, nil)
	if err != nil {
		return nil, nil, err
	}
	enumValue, _, err := s.emitExpr(leftExpr, scrutinee)
	if err != nil {
		return nil, nil, err
	}
	if semantic.EnumDescendsFrom(scrutinee, category) {
		return llvmConstInt(llvmInt1TypeInContext(s.g.context), 1, 0), boolType, nil
	}
	if category.LeafTagCount == 0 {
		return llvmConstInt(llvmInt1TypeInContext(s.g.context), 0, 0), boolType, nil
	}
	tagValue, err := s.extractEnumTagValue(enumValue, nil, scrutinee, storeBinding)
	if err != nil {
		return nil, nil, err
	}
	return s.emitTagRangeTest(tagValue, scrutinee, category.LeafTagLo, category.LeafTagCount)
}

// emitTagRangeTest emits the docs/81 category-membership primitive: matches ⇔ tag - lo <u count.
// count==1 folds to plain equality (a leaf is a range of size 1), lo==0 skips the subtract.
func (s *functionState) emitTagRangeTest(tagValue llvmIRValueRef, enumType *semantic.EnumType, lo uint32, count uint32) (llvmIRValueRef, semantic.Type, error) {
	boolType := s.g.namedType("bool")
	if lo == 0 && uint64(count) == enumTagValueCapacity(enumType) {
		return llvmConstInt(llvmInt1TypeInContext(s.g.context), 1, 0), boolType, nil
	}
	loConst, err := s.enumTagConstant(enumType, lo)
	if err != nil {
		return nil, nil, err
	}
	if count == 1 {
		return llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), tagValue, loConst, cStringFree("istag")), boolType, nil
	}
	relative := tagValue
	if lo != 0 {
		relative = llvmBuildSub(s.builder, tagValue, loConst, cStringFree("istag.rel"))
	}
	countConst, err := s.enumTagConstant(enumType, count)
	if err != nil {
		return nil, nil, err
	}
	return llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), relative, countConst, cStringFree("istag.range")), boolType, nil
}

// enumCategoryArm resolves a bind-arm name to a sub-category of a hierarchy scrutinee (docs/77 §2),
// mirroring the analyzer's resolveEnumCategoryArm so the backend dispatches the same arms the
// analyzer accepted. A plain (non-category) bind name returns false and stays a catch-all bind.
func (s *functionState) enumCategoryArm(enumType *semantic.EnumType, name string) (*semantic.EnumType, bool) {
	if enumType == nil || name == "" || (enumType.Parent == nil && len(enumType.Children) == 0) {
		return nil, false
	}
	if category, ok := s.g.namedType(name).(*semantic.EnumType); ok && category != nil && semantic.EnumDescendsFrom(category, enumType) {
		return category, true
	}
	// A category-arm name (`Decl d:`) is written UNQUALIFIED, but a type declared inside a
	// module is keyed namespace-qualified in NamedTypes (`Ast.Decl`), so the raw lookup
	// above misses it. The semantic analyzer resolves it via the using/namespace-aware
	// lookupVisibleType; the backend has no scope here, so resolve against the scrutinee's
	// own hierarchy by simple (unqualified) name — which is also namespace-agnostic.
	if category := findEnumCategoryBySimpleName(enumType, name); category != nil {
		return category, true
	}
	return nil, false
}

// findEnumCategoryBySimpleName returns the scrutinee enum or one of its transitive
// sub-categories whose unqualified name matches `name`, or nil — the backend's
// namespace-agnostic resolution of an unqualified category-arm name to a hierarchy member.
func findEnumCategoryBySimpleName(root *semantic.EnumType, name string) *semantic.EnumType {
	if root == nil {
		return nil
	}
	if simpleEnumCategoryName(root.Name) == name {
		return root
	}
	for _, child := range root.Children {
		if found := findEnumCategoryBySimpleName(child, name); found != nil {
			return found
		}
	}
	return nil
}

// simpleEnumCategoryName strips a namespace qualifier (`Ast.Decl` / `Ast::Decl` -> `Decl`).
func simpleEnumCategoryName(qualified string) string {
	if i := strings.LastIndexAny(qualified, ".:"); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

// enumCategoryIsTarget recognizes a bare enum-category `is` target (docs/77): the target names an
// enum TYPE (no `.Variant`) and the scrutinee is a hierarchical enum related to it. Mirrors the
// analyzer's resolveEnumCategoryIsTarget gating so flat-enum `is` lowering is unchanged.
func (s *functionState) enumCategoryIsTarget(leftExpr ast.Expr, target ast.Expr) (*semantic.EnumType, *semantic.EnumType, bool) {
	scrutinee, ok := semantic.StripAggregateStateType(s.exprType(leftExpr)).(*semantic.EnumType)
	if !ok || scrutinee == nil || (scrutinee.Parent == nil && len(scrutinee.Children) == 0) {
		return nil, nil, false
	}
	name := ""
	switch e := target.(type) {
	case *ast.ParenExpr:
		if e == nil {
			return nil, nil, false
		}
		return s.enumCategoryIsTarget(leftExpr, e.Inner)
	case *ast.TypeExprExpr:
		if e == nil {
			return nil, nil, false
		}
		named, ok := e.Type.(*ast.NamedType)
		if !ok || named == nil || strings.Contains(named.Name, ".") {
			return nil, nil, false // dotted ⇒ a variant target, handled elsewhere
		}
		name = named.Name
	case *ast.Ident:
		if e == nil {
			return nil, nil, false
		}
		name = e.Name
	default:
		return nil, nil, false
	}
	category, ok := s.g.namedType(name).(*semantic.EnumType)
	if !ok || category == nil {
		return nil, nil, false
	}
	if !semantic.EnumDescendsFrom(category, scrutinee) && !semantic.EnumDescendsFrom(scrutinee, category) {
		return nil, nil, false
	}
	return scrutinee, category, true
}
func (s *functionState) structIsTargetPattern(expr ast.Expr) (*ast.MatchStructPattern, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return s.structIsTargetPattern(paren.Inner)
	}
	if alias, ok := expr.(*ast.IsAliasExpr); ok && alias != nil {
		return s.structIsTargetPattern(alias.Target)
	}
	if testExpr, ok := expr.(*ast.StructTestExpr); ok && testExpr != nil && testExpr.Pattern != nil {
		return testExpr.Pattern, true
	}
	return nil, false
}
func (s *functionState) emitStructIsTest(leftExpr ast.Expr, pattern *ast.MatchStructPattern) (llvmIRValueRef, semantic.Type, error) {
	leftType := s.exprType(leftExpr)
	value, _, err := s.emitExpr(leftExpr, leftType)
	if err != nil {
		return nil, nil, err
	}
	successBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.struct.ok"))
	failureBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.struct.fail"))
	contBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.struct.cont"))
	if _, _, err := s.emitMatchPatternTest(pattern, value, nil, leftType, nil, leftExpr, nil, successBB, failureBB); err != nil {
		return nil, nil, err
	}

	llvmPositionBuilderAtEnd(s.builder, successBB)
	successValue := llvmConstInt(llvmInt1TypeInContext(s.g.context), 1, 0)
	llvmBuildBr(s.builder, contBB)
	successEnd := llvmGetInsertBlock(s.builder)

	llvmPositionBuilderAtEnd(s.builder, failureBB)
	failureValue := llvmConstInt(llvmInt1TypeInContext(s.g.context), 0, 0)
	llvmBuildBr(s.builder, contBB)
	failureEnd := llvmGetInsertBlock(s.builder)

	llvmPositionBuilderAtEnd(s.builder, contBB)
	phi := llvmBuildPhi(s.builder, llvmInt1TypeInContext(s.g.context), cStringFree("is.struct.result"))
	values := []llvmIRValueRef{successValue, failureValue}
	blocks := []llvmIRBlockRef{successEnd, failureEnd}
	llvmAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), llvmIRUnsigned(len(values)))
	return phi, s.g.namedType("bool"), nil
}
func (s *functionState) enumIsTargetPattern(expr ast.Expr) (*semantic.EnumType, *semantic.EnumVariant, *ast.MatchVariantPattern, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return s.enumIsTargetPattern(paren.Inner)
	}
	if alias, ok := expr.(*ast.IsAliasExpr); ok && alias != nil {
		return s.enumIsTargetPattern(alias.Target)
	}
	if testExpr, ok := expr.(*ast.VariantTestExpr); ok {
		if testExpr == nil || testExpr.Pattern == nil {
			return nil, nil, nil, false
		}
		// Resolve through MODULE VISIBILITY, not the raw NamedTypes map: a pattern
		// records the enum name as WRITTEN (`Expr`), while a module-scoped enum is keyed
		// by its qualified name (`Ast.Expr`). The raw lookup therefore missed for every
		// `module M:` enum, this function returned false, and emitIsExpr fell through to
		// the comparable path — which tried to emit the PATTERN as a value and failed with
		// "unsupported expression *ast.VariantTestExpr". Only bit in a VALUE position
		// (`flag: bool = e is M.E.V(a, b)`, `return e is ...`); a condition never reaches
		// here because directConditionPattern intercepts it first, which is why the
		// compiler's own pervasive `if x is T.V(...)` never showed the bug.
		// enumIsTarget, the sibling below, already resolved names this way.
		base, _, ok := s.lookupVisibleNamedType(testExpr.Pattern.EnumName)
		if !ok {
			return nil, nil, nil, false
		}
		enumType, ok := base.(*semantic.EnumType)
		if !ok || enumType == nil {
			return nil, nil, nil, false
		}
		variant, ok := enumType.Variant(testExpr.Pattern.Variant)
		if !ok || variant == nil {
			return nil, nil, nil, false
		}
		return enumType, variant, testExpr.Pattern, true
	}
	enumType, variant, ok := s.enumIsTarget(expr)
	if !ok || enumType == nil || variant == nil {
		return nil, nil, nil, false
	}
	pattern := &ast.MatchVariantPattern{Position: expr.Pos(), EnumName: enumType.Name, Variant: variant.Name}
	return enumType, variant, pattern, true
}
