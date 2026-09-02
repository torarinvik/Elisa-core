package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
	"strings"
)

func (a *Analyzer) analyzeStructLiteralExpr(expr *ast.StructLitExpr, expected Type) Type {
	// `Name[TypeArgs](args)` is parsed as a generic struct constructor, but when
	// `Name` resolves to a generic *function* (which is visible even through
	// transitive includes), it is actually a specialized call. Route it before
	// type resolution so the type-args are preserved and no spurious
	// "unknown type" is reported for the function name.
	if call, ok := a.structLiteralAsGenericFunctionCall(expr); ok {
		a.loweredInitCalls[expr] = call
		result := a.analyzeCallExpr(call)
		a.exprTypes[expr] = result
		return result
	}
	targetType := a.structLiteralTargetType(expr, expected)
	base, bindings, regionBindings, ok := structLiteralBaseAndBindings(targetType)
	if !ok || base == nil {
		// `Event(code)` on a non-struct target is the PREFIX type-constructor cast
		// (docs/18: `T(x)` and `x.T()` share one `__cast__` hook lookup). The parser
		// reads any `Name(args)` as a struct literal, so an enum/alias target never
		// reached the cast path and reported "is not a struct". Only claim it when a
		// visible hook actually exists, so a genuine misuse keeps its own diagnostic.
		if castCall, castOK := a.structLiteralAsTypeConstructorCast(expr, targetType); castOK {
			a.loweredInitCalls[expr] = castCall
			result := a.analyzeCallExpr(castCall)
			a.exprTypes[expr] = result
			return result
		}
		if call, callOK := a.structLiteralAsPascalCaseFunctionCall(expr); callOK {
			a.loweredInitCalls[expr] = call
			result := a.analyzeCallExpr(call)
			a.exprTypes[expr] = result
			return result
		}
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		if !IsInvalidType(targetType) {
			a.errorf(expr.Pos(), "type %q is not a struct", targetType)
		} else {
			a.errorf(expr.Pos(), "unknown struct %q", expr.Name)
		}
		return invalidType
	}
	if !expr.Brace {
		if resultType, handled := a.analyzeInitHookStructConstructor(expr, targetType); handled {
			return resultType
		}
		// Paren-form `Name(...)` is reserved for constructors. Plain field-wise
		// construction must use the brace form `Name{...}`, so a reader can tell
		// default construction (braces — guaranteed to just pack fields) from a
		// custom constructor (parens — a `def Name(...) -> Name` that may run logic)
		// at a glance. We only reach here when no constructor overload matched, so
		// reject the implicit positional all-fields form rather than silently
		// accepting it.
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "positional construction %s(...) is not allowed; use the brace form %s{...} for default field initialization, or define a constructor `def %s(...) -> %s`", expr.Name, expr.Name, expr.Name, base.Name)
		return targetType
	}
	for i, spreadExpr := range expr.Spreads {
		spread, actual := a.analyzeCallLikeValueExpr(spreadExpr, targetType)
		expr.Spreads[i] = spread
		if !AssignableTo(targetType, actual) {
			a.errorf(spread.Pos(), "struct literal spread expects %s, got %s", targetType, actual)
		}
		a.consumeAffineValueExpr(spread, targetType, "move into struct literal spread base")
	}
	a.analyzeStructLiteralArgs(expr, base, bindings, regionBindings)
	if len(base.NamedStateCases) == 0 {
		return targetType
	}
	desiredState, ok := namedStateCurrentArg(targetType)
	if !ok || desiredState == nil {
		return targetType
	}
	fullState := fullNamedStateType(base)
	if sameNamedStateType(desiredState, fullState) {
		inferredState := a.inferStructLiteralNamedState(expr, base)
		if inferredState != nil && !sameNamedStateType(inferredState, fullState) {
			return instantiateNamedStateStructLiteralType(base, targetType, inferredState)
		}
		return targetType
	}
	if !a.proveStructLiteralNamedState(expr, base, desiredState) {
		a.errorf(expr.Pos(), "struct literal %q does not satisfy derived state %s", expr.Name, desiredState)
	}
	return targetType
}

// structLiteralAsGenericFunctionCall detects `Name[TypeArgs](args)` where Name
// is a generic function (not a type) and rewrites it to a specialized call,
// preserving the type arguments. Brace literals and non-generic / type names
// are left for the normal struct-literal path.
func (a *Analyzer) structLiteralAsGenericFunctionCall(expr *ast.StructLitExpr) (*ast.CallExpr, bool) {
	if a == nil || expr == nil || expr.Brace || expr.Name == "" || len(expr.TypeArgs) == 0 {
		return nil, false
	}
	var sym *Symbol
	if a.currentScope != nil {
		if localSym, ok := a.currentScope.Lookup(expr.Name); ok {
			sym = localSym
		}
	}
	if sym == nil {
		if globalSym, _, ok := a.lookupVisibleGlobal(expr.Name); ok {
			sym = globalSym
		}
	}
	if sym == nil || (sym.Kind != SymbolFunc && sym.Kind != SymbolExternFunc) {
		return nil, false
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || len(genericParamsForFuncType(ft)) == 0 {
		return nil, false
	}
	return &ast.CallExpr{
		Position: expr.Position,
		Func: &ast.SpecializeExpr{
			Position: expr.Position,
			Operand:  &ast.Ident{Position: expr.Position, Name: expr.Name},
			TypeArgs: append([]ast.TypeExpr(nil), expr.TypeArgs...),
		},
		Args:     append([]ast.Expr(nil), expr.Args...),
		ArgNames: append([]string(nil), expr.ArgNames...),
	}, true
}

func (a *Analyzer) structLiteralAsPascalCaseFunctionCall(expr *ast.StructLitExpr) (*ast.CallExpr, bool) {
	if a == nil || expr == nil || expr.Brace || expr.Name == "" {
		return nil, false
	}
	var sym *Symbol
	if a.currentScope != nil {
		if localSym, ok := a.currentScope.Lookup(expr.Name); ok {
			sym = localSym
		}
	}
	if sym == nil {
		if globalSym, _, ok := a.lookupVisibleGlobal(expr.Name); ok {
			sym = globalSym
		}
	}
	if sym == nil {
		return nil, false
	}
	if sym.Kind != SymbolFunc && sym.Kind != SymbolExternFunc {
		return nil, false
	}
	return &ast.CallExpr{
		Position: expr.Position,
		Func:     &ast.Ident{Position: expr.Position, Name: expr.Name},
		Args:     append([]ast.Expr(nil), expr.Args...),
		ArgNames: append([]string(nil), expr.ArgNames...),
	}, true
}

func (a *Analyzer) analyzeInitHookStructConstructor(expr *ast.StructLitExpr, targetType Type) (Type, bool) {
	hooks := a.lookupVisibleInitHooks(targetType)
	if len(hooks) == 0 {
		return nil, false
	}
	approxTypes := make([]Type, len(expr.Args))
	knownTypes := make([]bool, len(expr.Args))
	for i, arg := range expr.Args {
		if approx := a.approximateInitHookArgType(arg); approx != nil && !IsInvalidType(approx) {
			approxTypes[i] = approx
			knownTypes[i] = true
		}
	}
	type initHookMatch struct {
		sym             *Symbol
		orderedArgs     []ast.Expr
		defaultsUsed    int
		knownTypeChecks int
	}
	matches := make([]initHookMatch, 0, len(hooks))
	for _, sym := range hooks {
		fnType, ok := sym.Type.(*FuncType)
		if !ok || fnType == nil {
			continue
		}
		orderedArgs, sourceIndexes, defaultsUsed, ok := a.resolveInitHookArgs(expr, fnType)
		if !ok {
			continue
		}
		knownChecks := 0
		valid := true
		for paramIndex, sourceIndex := range sourceIndexes {
			if sourceIndex < 0 || sourceIndex >= len(approxTypes) || !knownTypes[sourceIndex] {
				continue
			}
			knownChecks++
			if paramIndex >= len(fnType.Params) || !initHookArgFits(fnType.Params[paramIndex], approxTypes[sourceIndex]) {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		matches = append(matches, initHookMatch{sym: sym, orderedArgs: orderedArgs, defaultsUsed: defaultsUsed, knownTypeChecks: knownChecks})
	}
	if len(matches) == 0 {
		// No custom constructor overload matches these args. Don't claim the
		// construction — fall back to the implicit all-fields positional form so a
		// custom `def Type(...)` constructor COEXISTS with `Type(a, b, ...)` instead
		// of shadowing it. (A genuine mismatch is then reported by the all-fields
		// construction path.)
		return nil, false
	}
	bestDefaults := matches[0].defaultsUsed
	for _, match := range matches[1:] {
		if match.defaultsUsed < bestDefaults {
			bestDefaults = match.defaultsUsed
		}
	}
	filtered := matches[:0]
	for _, match := range matches {
		if match.defaultsUsed == bestDefaults {
			filtered = append(filtered, match)
		}
	}
	bestKnownChecks := filtered[0].knownTypeChecks
	for _, match := range filtered[1:] {
		if match.knownTypeChecks > bestKnownChecks {
			bestKnownChecks = match.knownTypeChecks
		}
	}
	best := filtered[:0]
	for _, match := range filtered {
		if match.knownTypeChecks == bestKnownChecks {
			best = append(best, match)
		}
	}
	if len(best) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "ambiguous __init__ overload for %s", targetType)
		return targetType, true
	}
	selected := best[0]
	call := &ast.CallExpr{
		Position:          expr.Pos(),
		Func:              &ast.Ident{Position: expr.Pos(), Name: selected.sym.Name},
		Args:              append([]ast.Expr(nil), expr.Args...),
		ResolvedArgsValid: true,
		ResolvedArgs:      append([]ast.Expr(nil), selected.orderedArgs...),
	}
	resultType := a.analyzeCallExpr(call)
	a.exprTypes[call] = resultType
	if a.loweredInitCalls != nil {
		a.loweredInitCalls[expr] = call
	}
	return resultType, true
}

// initHookArgFits is AssignableTo plus the literal-to-view adaptation, for deciding
// whether a constructor overload is a candidate.
//
// A string literal approximates to `u8& static` (inferLiteralType), and that is NOT
// AssignableTo an `sview` parameter -- so `def Row(k: i64, s: sview) -> Row` was
// discarded as a candidate and `Row(3, "x")` fell through to the all-fields positional
// path, which rejected it while ASKING FOR THE CONSTRUCTOR DEFINED THREE LINES ABOVE:
//
//	positional construction Row(...) is not allowed; ... or define a constructor
//	`def Row(...) -> Row`
//
// The ordinary call path accepts the same literal for the same parameter, so this was
// the overload FILTER disagreeing with the language, not a real type error. Measured:
// passing an `sview` LOCAL instead of the literal compiled fine, and the struct having
// an sview FIELD had nothing to do with it.
//
// isStringViewType/isStaticStringLiteralRefType are the pair already used for this same
// equivalence in match-arm folding.
func initHookArgFits(param Type, arg Type) bool {
	if AssignableTo(param, arg) {
		return true
	}
	return isStringViewType(param) && isStaticStringLiteralRefType(arg)
}

func (a *Analyzer) approximateInitHookArgType(expr ast.Expr) Type {
	if expr == nil {
		return nil
	}
	if actual, ok := a.exprTypes[expr]; ok && actual != nil && !IsInvalidType(actual) {
		return actual
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.approximateInitHookArgType(n.Inner)
	case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.CharLit, *ast.BoolLit, *ast.NullLit, *ast.ZeroedLit:
		return a.inferLiteralType(expr)
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok && sym != nil {
				return sym.Type
			}
		}
		if sym, _, ok := a.lookupVisibleGlobal(n.Name); ok && sym != nil {
			return sym.Type
		}
	case *ast.CastExpr:
		return a.resolveType(n.Target)
	case *ast.StructLitExpr:
		if t := a.structLiteralTargetType(n, nil); t != nil && !IsInvalidType(t) {
			return t
		}
	case *ast.SpecializeExpr:
		if ident, ok := n.Operand.(*ast.Ident); ok {
			if sym, _, ok := a.lookupVisibleGlobal(ident.Name); ok && sym != nil {
				return sym.Type
			}
		}
	}
	return nil
}
func (a *Analyzer) resolveInitHookArgs(expr *ast.StructLitExpr, ft *FuncType) ([]ast.Expr, []int, int, bool) {
	if expr == nil || ft == nil {
		return nil, nil, 0, false
	}
	explicitParamCount := funcTypeExplicitParamCount(ft)
	ordered := make([]ast.Expr, explicitParamCount)
	sourceIndexes := make([]int, explicitParamCount)
	for i := range sourceIndexes {
		sourceIndexes[i] = -1
	}
	if expr.NamedArgCount() == 0 {
		if len(expr.Args) > explicitParamCount {
			return nil, nil, 0, false
		}
		copy(ordered, expr.Args)
		for i := range expr.Args {
			sourceIndexes[i] = i
		}
		defaultsUsed := 0
		for i := len(expr.Args); i < explicitParamCount; i++ {
			if i >= len(ft.ExplicitParamHasDefault) || !ft.ExplicitParamHasDefault[i] || i >= len(ft.ExplicitParamDefaultExprs) {
				return nil, nil, 0, false
			}
			ordered[i] = cloneDefaultArgExpr(ft.ExplicitParamDefaultExprs[i])
			if ordered[i] == nil {
				return nil, nil, 0, false
			}
			defaultsUsed++
		}
		return ordered, sourceIndexes, defaultsUsed, true
	}
	if len(ft.ExplicitParamNames) != explicitParamCount {
		return nil, nil, 0, false
	}
	nameToIndex := make(map[string]int, len(ft.ExplicitParamNames))
	for i, name := range ft.ExplicitParamNames {
		if name == "" {
			return nil, nil, 0, false
		}
		nameToIndex[name] = i
	}
	filled := make([]bool, explicitParamCount)
	sawNamed := false
	nextPositional := 0
	for argIndex, arg := range expr.Args {
		name := expr.ArgName(argIndex)
		if name == "" {
			if sawNamed || nextPositional >= explicitParamCount || filled[nextPositional] {
				return nil, nil, 0, false
			}
			ordered[nextPositional] = arg
			sourceIndexes[nextPositional] = argIndex
			filled[nextPositional] = true
			nextPositional++
			continue
		}
		sawNamed = true
		paramIndex, ok := nameToIndex[name]
		if !ok || filled[paramIndex] {
			return nil, nil, 0, false
		}
		ordered[paramIndex] = arg
		sourceIndexes[paramIndex] = argIndex
		filled[paramIndex] = true
	}
	defaultsUsed := 0
	for i := 0; i < explicitParamCount; i++ {
		if filled[i] {
			continue
		}
		if i >= len(ft.ExplicitParamHasDefault) || !ft.ExplicitParamHasDefault[i] || i >= len(ft.ExplicitParamDefaultExprs) {
			return nil, nil, 0, false
		}
		ordered[i] = cloneDefaultArgExpr(ft.ExplicitParamDefaultExprs[i])
		if ordered[i] == nil {
			return nil, nil, 0, false
		}
		defaultsUsed++
	}
	return ordered, sourceIndexes, defaultsUsed, true
}
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
		return fullNamedStateType(base)
	}
	trueStates := make([]string, 0, len(base.NamedStateCases))
	for _, stateName := range base.NamedStateCases {
		proven, value := a.evaluateDerivedStateForFields(base, stateName, fieldValues)
		if !proven {
			return fullNamedStateType(base)
		}
		if value {
			trueStates = append(trueStates, stateName)
		}
	}
	if len(trueStates) == 1 {
		return newNamedStateType(base.Name, base.NamedStateCases, trueStates)
	}
	if len(trueStates) == 0 {
		a.errorf(expr.Pos(), "struct literal %q does not satisfy any derived state", expr.Name)
		return fullNamedStateType(base)
	}
	a.errorf(expr.Pos(), "struct literal %q satisfies multiple derived states: %s", expr.Name, strings.Join(trueStates, ", "))
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
	a.suppressDiagnostics = true
	sourceType := a.analyzeExpr(expr.Args[0])
	a.suppressDiagnostics = savedSuppress
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
