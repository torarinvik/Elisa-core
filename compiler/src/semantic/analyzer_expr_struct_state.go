package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
	"strings"
)

func (a *Analyzer) analyzeRecordUpdateExpr(expr *ast.RecordUpdateExpr) Type {
	return a.analyzeRecordUpdateExprWithTreeOwnerRequirement(expr, true)
}
func (a *Analyzer) analyzeRecordUpdateExprWithTreeOwnerRequirement(expr *ast.RecordUpdateExpr, requireTreeOwner bool) Type {
	if expr == nil || expr.Base == nil {
		return invalidType
	}
	baseType := a.analyzeExpr(expr.Base)
	resultType := baseType
	resolvedBaseType := baseType
	if exactType, ok := a.rewriteDefaultExactType(expr.Base); ok {
		resolvedBaseType = exactType
	}
	stripped := StripAggregateStateType(resolvedBaseType)
	switch stripped.(type) {
	case *StructType, *GenericInstanceType:
	default:
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		if !IsInvalidType(resolvedBaseType) {
			a.errorf(expr.Pos(), "record update requires a concrete struct or store-row value, got %s", resolvedBaseType)
		}
		return invalidType
	}
	fields, ok := a.resolvedStructFields(resolvedBaseType)
	if !ok {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "record update requires a concrete struct or store-row value, got %s", resolvedBaseType)
		return invalidType
	}
	if expr.NamedArgCount() != len(expr.Args) {
		a.errorf(expr.Pos(), "record update cannot mix positional and named fields")
		for i := range expr.Args {
			expr.Args[i], _ = a.analyzeCallLikeValueExpr(expr.Args[i], nil)
		}
		return resultType
	}
	ordered := make([]ast.Expr, len(fields))
	fieldIndexes := make(map[string]int, len(fields))
	for i := range fields {
		fieldIndexes[fields[i].Name] = i
	}
	seen := map[int]lexer.Pos{}
	ok = true
	for i := range expr.Args {
		name := expr.ArgName(i)
		a.checkPrivateFieldOfType(resolvedBaseType, name, expr.Args[i].Pos())
		index, exists := fieldIndexes[name]
		if !exists {
			a.errorf(expr.Args[i].Pos(), "record update has no field %q", name)
			expr.Args[i], _ = a.analyzeCallLikeValueExpr(expr.Args[i], nil)
			ok = false
			continue
		}
		expected := fields[index].Type
		arg, actual := a.analyzeCallLikeValueExpr(expr.Args[i], expected)
		expr.Args[i] = arg
		if !AssignableTo(expected, actual) {
			a.errorf(expr.Args[i].Pos(), "record update field %q expects %s, got %s", name, expected, actual)
		}
		a.consumeAffineValueExpr(expr.Args[i], expected, "move into record update field "+strconv.Quote(name))
		if prev, exists := seen[index]; exists {
			a.errorf(expr.Args[i].Pos(), "record update field %q is specified more than once (first at %s:%d:%d)", name, prev.File, prev.Line, prev.Col)
			ok = false
			continue
		}
		seen[index] = expr.Args[i].Pos()
		ordered[index] = expr.Args[i]
	}
	if ok {
		expr.ResolvedArgsValid = true
		expr.ResolvedArgs = ordered
		if base, hasStates := namedStateStructBase(resolvedBaseType); hasStates && !base.ProtocolStates {
			updates := make(map[string]ast.Expr, len(expr.Args))
			for i, value := range expr.Args {
				updates[expr.ArgName(i)] = value
			}
			if state := a.inferRecordUpdateNamedState(expr.Pos(), base, updates); state != nil {
				resultType = replaceTrackedNamedStateArg(resolvedBaseType, state)
				if resultType == nil {
					a.errorf(expr.Pos(), "record update cannot resolve derived state family %q", base.Name)
					resultType = invalidType
				}
			}
		}
	}
	a.consumeAffineValueExpr(expr.Base, resolvedBaseType, "record update")
	return resultType
}
func (a *Analyzer) structLiteralTargetType(expr *ast.StructLitExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	if len(expr.TypeArgs) != 0 {
		return a.resolveType(&ast.GenericType{Position: expr.Position, Name: expr.Name, Args: expr.TypeArgs})
	}
	if base, _, _, ok := structLiteralBaseAndBindings(expected); ok && structTypeMatchesLiteralName(base, expr.Name) {
		return expected
	}
	if t, _, ok := a.lookupVisibleType(expr.Name); ok {
		return DefaultStatefulType(t)
	}
	return invalidType
}
func structTypeMatchesLiteralName(base *StructType, name string) bool {
	if base == nil {
		return false
	}
	return base.Name == name || strings.HasSuffix(base.Name, "."+name)
}
func structLiteralBaseAndBindings(t Type) (*StructType, map[string]Type, map[string]string, bool) {
	t = StripAggregateStateType(t)
	switch tt := t.(type) {
	case *StructType:
		return tt, nil, nil, true
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		if !ok || base == nil {
			return nil, nil, nil, false
		}
		return base, genericBindingsForStructInstance(base, tt.Args), regionBindingsForStructInstance(base, tt.Args), true
	default:
		return nil, nil, nil, false
	}
}
func instantiateNamedStateStructLiteralType(base *StructType, template Type, state Type) Type {
	if base == nil || state == nil {
		return template
	}
	// Preserve the reference transport while specializing the logical named
	// state.  Flow refinement routinely receives `App&` here, whereas the
	// generic state argument lives on its `App[State]` element type.
	var refTemplate *RefType
	if ref, ok := template.(*RefType); ok && ref != nil {
		refTemplate = ref
		template = ref.Elem
	}
	template = StripAggregateStateType(template)
	gi, ok := template.(*GenericInstanceType)
	if !ok || gi == nil {
		if defaultGI, ok := DefaultNamedStateType(base).(*GenericInstanceType); ok {
			gi = defaultGI
		} else {
			return template
		}
	}
	idx := namedStateArgIndex(base)
	if idx < 0 || idx >= len(gi.Args) {
		return template
	}
	args := append([]Type(nil), gi.Args...)
	args[idx] = state
	instantiated := Type(&GenericInstanceType{Name: gi.Name, Base: gi.Base, Args: args})
	if refTemplate != nil {
		ref := cloneRefType(refTemplate)
		ref.Elem = instantiated
		return ref
	}
	return instantiated
}
func (a *Analyzer) inferStructLiteralNamedState(expr *ast.StructLitExpr, base *StructType) Type {
	if expr == nil || base == nil || len(base.NamedStateCases) == 0 {
		return nil
	}
	fieldValues, ok := structLiteralFieldValues(expr, base)
	if !ok {
		if len(base.NamedStateCases) == 1 {
			a.errorf(expr.Pos(), "struct literal %q cannot establish its only derived state from incomplete fields", expr.Name)
		}
		return unknownNamedStateType(base)
	}
	return a.inferNamedStateForFieldValues(expr.Pos(), "struct literal", expr.Name, base, fieldValues)
}

// Unchanged fields are intentionally absent: reading the base AST again after
// argument analysis could observe a later mutation rather than the captured
// input payload. Preserve the input state only when every predicate is unchanged.
func (a *Analyzer) inferRecordUpdateNamedState(pos lexer.Pos, base *StructType, updates map[string]ast.Expr) Type {
	if len(updates) == 0 {
		return nil
	}
	changed := false
	for _, derived := range base.DerivedStates {
		if recordUpdatePredicateMayChange(derived.Condition, updates, 0) {
			changed = true
			break
		}
	}
	if !changed && len(base.DerivedStates) != 0 {
		return nil
	}
	return a.inferNamedStateForFieldValues(pos, "record update", base.Name, base, updates)
}

func recordUpdatePredicateMayChange(expr ast.Expr, updates map[string]ast.Expr, depth int) bool {
	if expr == nil || depth >= 128 {
		return true
	}
	if _, field := expr.(*ast.FieldExpr); field {
		current := expr
		rootField := ""
		for remaining := 128 - depth; remaining > 0; remaining-- {
			switch node := current.(type) {
			case *ast.FieldExpr:
				rootField = node.Field
				current = node.Object
			case *ast.Ident:
				if node.Name != "self" || rootField == "" {
					return true
				}
				_, changed := updates[rootField]
				return changed
			default:
				return true
			}
		}
		return true
	}
	switch n := expr.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.CharLit, *ast.StringLit, *ast.NullLit:
		return false
	case *ast.ParenExpr:
		return recordUpdatePredicateMayChange(n.Inner, updates, depth+1)
	case *ast.UnaryExpr:
		return recordUpdatePredicateMayChange(n.Operand, updates, depth+1)
	case *ast.BinaryExpr:
		return recordUpdatePredicateMayChange(n.Left, updates, depth+1) || recordUpdatePredicateMayChange(n.Right, updates, depth+1)
	default:
		return true
	}
}

func (a *Analyzer) inferNamedStateForFieldValues(pos lexer.Pos, kind, name string, base *StructType, fieldValues map[string]ast.Expr) Type {
	trueStates := make([]string, 0, len(base.NamedStateCases))
	for _, stateName := range base.NamedStateCases {
		proven, value := a.evaluateDerivedStateForFields(base, stateName, fieldValues)
		if !proven {
			if len(base.NamedStateCases) == 1 {
				a.errorf(pos, "%s %q cannot establish its only derived state from an unknown predicate", kind, name)
			}
			return unknownNamedStateType(base)
		}
		if value {
			trueStates = append(trueStates, stateName)
		}
	}
	if len(trueStates) == 1 {
		return newNamedStateType(base.Name, base.NamedStateCases, trueStates)
	}
	if len(trueStates) == 0 {
		a.errorf(pos, "%s %q does not satisfy any derived state", kind, name)
		return fullNamedStateType(base)
	}
	a.errorf(pos, "%s %q satisfies multiple derived states: %s", kind, name, strings.Join(trueStates, ", "))
	return fullNamedStateType(base)
}
func (a *Analyzer) proveStructLiteralNamedState(expr *ast.StructLitExpr, base *StructType, desired Type) bool {
	if expr == nil || base == nil || desired == nil {
		return false
	}
	desiredCases, _, ok := namedStateTypeCases(desired)
	if !ok {
		return false
	}
	if len(desiredCases) != 1 {
		return sameNamedStateType(desired, fullNamedStateType(base))
	}
	fieldValues, ok := structLiteralFieldValues(expr, base)
	if !ok {
		return false
	}
	proven, value := a.evaluateDerivedStateForFields(base, desiredCases[0], fieldValues)
	if !proven || !value {
		return false
	}
	for _, other := range base.NamedStateCases {
		if other == desiredCases[0] {
			continue
		}
		if otherProven, otherValue := a.evaluateDerivedStateForFields(base, other, fieldValues); otherProven && otherValue {
			a.errorf(expr.Pos(), "struct literal %q satisfies multiple derived states including %q and %q", expr.Name, desiredCases[0], other)
			return false
		}
	}
	return true
}
func structLiteralFieldValues(expr *ast.StructLitExpr, base *StructType) (map[string]ast.Expr, bool) {
	args := expr.LoweredArgs()
	if expr == nil || base == nil || base.Decl == nil || len(args) != len(base.Decl.Fields) {
		return nil, false
	}
	fieldValues := make(map[string]ast.Expr, len(base.Decl.Fields))
	for i, fieldDecl := range base.Decl.Fields {
		if args[i] == nil {
			return nil, false
		}
		fieldValues[fieldDecl.Name] = args[i]
	}
	return fieldValues, true
}
func (a *Analyzer) evaluateDerivedStateForFields(base *StructType, stateName string, fieldValues map[string]ast.Expr) (bool, bool) {
	if base == nil || fieldValues == nil || base.DerivedStateMap == nil {
		return false, false
	}
	derived := base.DerivedStateMap[stateName]
	if derived == nil || derived.Condition == nil {
		return false, false
	}
	substituted, ok := substituteDerivedStateFieldExpr(derived.Condition, fieldValues)
	if !ok {
		return false, false
	}
	value, ok := a.evalConstBoolExpr(substituted)
	return ok, value
}
func substituteDerivedStateFieldExpr(expr ast.Expr, fieldValues map[string]ast.Expr) (ast.Expr, bool) {
	if path, ok := derivedStateSelfFieldPath(expr); ok {
		if len(path) == 0 {
			return nil, false
		}
		baseExpr, ok := fieldValues[path[0]]
		if !ok || baseExpr == nil {
			return nil, false
		}
		out := cloneDerivedStateExpr(baseExpr)
		for _, field := range path[1:] {
			out = &ast.FieldExpr{Position: expr.Pos(), Object: out, Field: field}
		}
		return out, true
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		inner, ok := substituteDerivedStateFieldExpr(n.Inner, fieldValues)
		if !ok {
			return nil, false
		}
		return &ast.ParenExpr{Position: n.Position, Inner: inner}, true
	case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.CharLit, *ast.BoolLit, *ast.NullLit:
		return cloneDerivedStateExpr(expr), true
	case *ast.UnaryExpr:
		operand, ok := substituteDerivedStateFieldExpr(n.Operand, fieldValues)
		if !ok {
			return nil, false
		}
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: operand}, true
	case *ast.BinaryExpr:
		left, ok := substituteDerivedStateFieldExpr(n.Left, fieldValues)
		if !ok {
			return nil, false
		}
		right, ok := substituteDerivedStateFieldExpr(n.Right, fieldValues)
		if !ok {
			return nil, false
		}
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: left, Right: right}, true
	default:
		return nil, false
	}
}

// structLiteralAsTypeConstructorCast rewrites a one-argument `T(x)` whose target is not a
// struct into the equivalent constructor CALL, but only when a `__cast__` hook from the
// argument's type to T is visible. The call path (analyzeTypeConstructorCall) then resolves
// and records the hook exactly as the postfix `x.T()` spelling already does.
func (a *Analyzer) structLiteralAsTypeConstructorCast(expr *ast.StructLitExpr, targetType Type) (*ast.CallExpr, bool) {
	if a == nil || expr == nil || expr.Name == "" || expr.Brace || targetType == nil || IsInvalidType(targetType) {
		return nil, false
	}
	if len(expr.Args) != 1 || len(expr.ArgNames) != 0 {
		return nil, false
	}
	savedSuppress := a.suppressDiagnostics
	restoreReportProbe1 := a.beginProofReportProbe()
	defer restoreReportProbe1.restore()
	a.suppressDiagnostics = true
	sourceType := a.analyzeExpr(expr.Args[0])
	a.suppressDiagnostics = savedSuppress
	restoreReportProbe1.restore()
	hookSym, ok := a.lookupVisibleCastHook(sourceType, targetType)
	if !ok || a.isSelfCastHook(hookSym) {
		return nil, false
	}
	return &ast.CallExpr{
		Position: expr.Position,
		Func:     &ast.Ident{Position: expr.Position, Name: expr.Name},
		Args:     []ast.Expr{expr.Args[0]},
	}, true
}
