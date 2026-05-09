package semantic

import (
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) resolveFunctionCallArgs(expr *ast.CallExpr, ft *FuncType) ([]ast.Expr, bool) {
	if expr == nil || ft == nil {
		return nil, false
	}
	explicitParamCount := funcTypeExplicitParamCount(ft)
	if expr.ResolvedArgsValid && expr.ResolvedCommonArgs == nil {
		return expr.ResolvedArgs, true
	}
	if !expr.HasArgForward && len(expr.ParamPacks) == 0 && expr.NamedArgCount() == 0 {
		if len(expr.Args) > explicitParamCount {
			expr.ResolvedArgsValid = true
			expr.ResolvedArgs = expr.Args
			expr.ResolvedCommonArgs = nil
			return expr.Args, true
		}
		ordered := make([]ast.Expr, explicitParamCount)
		copy(ordered, expr.Args)
		filled := make([]bool, explicitParamCount)
		for i := range expr.Args {
			filled[i] = true
		}
		if !a.fillMissingDefaultCallArgs(expr, ft, ordered, filled, true) {
			return nil, false
		}
		expr.ResolvedArgsValid = true
		expr.ResolvedArgs = ordered
		expr.ResolvedCommonArgs = nil
		return ordered, true
	}
	if ft.Variadic {
		if expr.HasArgForward {
			a.errorf(expr.ArgForwardPos, "call argument forwarding `..` is not supported for variadic function %q", ft.Name)
		} else {
			a.errorf(expr.Pos(), "named arguments and explicit bundle applications are not supported for variadic function %q", ft.Name)
		}
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return nil, false
	}
	if len(ft.ExplicitParamNames) != explicitParamCount {
		a.errorf(expr.Pos(), "function %q does not expose parameter names for named argument calls", ft.Name)
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return nil, false
	}
	nameToIndex := make(map[string]int, len(ft.ExplicitParamNames))
	for i, name := range ft.ExplicitParamNames {
		if name == "" {
			a.errorf(expr.Pos(), "function %q does not expose parameter names for named argument calls", ft.Name)
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return nil, false
		}
		nameToIndex[name] = i
	}
	ordered := make([]ast.Expr, explicitParamCount)
	filled := make([]bool, explicitParamCount)
	sources := make([]callArgSource, explicitParamCount)
	if expr.HasArgForward {
		for i, name := range ft.ExplicitParamNames {
			if arg, ok := a.lookupCallForwardValueExpr(name); ok {
				ordered[i] = arg
				filled[i] = true
				sources[i] = callArgSourceForward
			}
		}
	}
	sawNamed := false
	sawPack := false
	nextPositional := 0
	ok := true
	for _, item := range orderedCallArgItems(expr) {
		if item.IsPack {
			if sawPack {
				a.errorf(item.Position, "call may specify at most one explicit bundle application")
				ok = false
			}
			if sawNamed {
				a.errorf(item.Position, "explicit bundle application must come before ordinary named arguments")
				ok = false
			}
			sawPack = true
			pack, values := a.expandParamPackUseValues(item.Pack, "argument")
			if pack == nil {
				ok = false
				continue
			}
			for _, field := range pack.Fields {
				valueExpr, exists := values[field.Name]
				if !exists || valueExpr == nil {
					continue
				}
				index, found := nameToIndex[field.Name]
				if !found {
					a.errorf(item.Pack.Position, "function %q has no parameter %q", ft.Name, field.Name)
					ok = false
					continue
				}
				switch sources[index] {
				case callArgSourceExplicit:
					a.errorf(item.Pack.Position, "function %q parameter %q is specified more than once", ft.Name, field.Name)
					ok = false
					continue
				case callArgSourcePack:
					a.errorf(item.Pack.Position, "function %q parameter %q is specified more than once", ft.Name, field.Name)
					ok = false
					continue
				}
				ordered[index] = valueExpr
				filled[index] = true
				sources[index] = callArgSourcePack
			}
			continue
		}
		arg := expr.Args[item.ArgIndex]
		name := expr.ArgName(item.ArgIndex)
		if name == "" {
			if expr.HasArgForward {
				a.errorf(arg.Pos(), "call argument forwarding `..` only supports named arguments")
				a.analyzeExpr(arg)
				ok = false
				continue
			}
			if sawNamed || sawPack {
				a.errorf(arg.Pos(), "function %q cannot use positional arguments after named arguments", ft.Name)
				a.analyzeExpr(arg)
				ok = false
				continue
			}
			if nextPositional >= explicitParamCount {
				a.errorf(arg.Pos(), "function %q expects %d arguments, got %d", ft.Name, explicitParamCount, len(expr.Args))
				a.analyzeExpr(arg)
				ok = false
				continue
			}
			if sources[nextPositional] == callArgSourceExplicit || sources[nextPositional] == callArgSourcePack {
				a.errorf(arg.Pos(), "function %q parameter %q is specified more than once", ft.Name, ft.ExplicitParamNames[nextPositional])
				a.analyzeExpr(arg)
				ok = false
				continue
			}
			ordered[nextPositional] = arg
			filled[nextPositional] = true
			sources[nextPositional] = callArgSourceExplicit
			nextPositional++
			continue
		}
		sawNamed = true
		index, found := nameToIndex[name]
		if !found {
			a.errorf(arg.Pos(), "function %q has no parameter %q", ft.Name, name)
			a.analyzeExpr(arg)
			ok = false
			continue
		}
		if sources[index] == callArgSourceExplicit {
			a.errorf(arg.Pos(), "function %q parameter %q is specified more than once", ft.Name, name)
			a.analyzeExpr(arg)
			ok = false
			continue
		}
		ordered[index] = a.resolveCallArgValueExpr(expr, item.ArgIndex, "argument")
		filled[index] = true
		sources[index] = callArgSourceExplicit
	}
	a.fillMissingAmbientExplicitCallArgs(ft, ordered, filled)
	if !a.fillMissingDefaultCallArgs(expr, ft, ordered, filled, false) {
		ok = false
	}
	if !ok {
		return nil, false
	}
	expr.ResolvedArgsValid = true
	expr.ResolvedArgs = ordered
	expr.ResolvedCommonArgs = nil
	return ordered, true
}

func (a *Analyzer) lookupCallForwardValueExpr(name string) (ast.Expr, bool) {
	if a == nil || name == "" || a.currentScope == nil {
		return nil, false
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope == a.globalScope {
			if sym, ok := scope.Symbols[name]; ok && sym != nil {
				return nil, false
			}
			break
		}
		sym, ok := scope.Symbols[name]
		if !ok || sym == nil {
			continue
		}
		root := symbolAliasRoot(sym)
		if root == nil {
			root = sym
		}
		if root.Kind != SymbolLocal && root.Kind != SymbolParam {
			return nil, false
		}
		return &ast.Ident{Position: lexer.Pos{}, Name: name}, true
	}
	return nil, false
}

func (a *Analyzer) fillMissingDefaultCallArgs(expr *ast.CallExpr, ft *FuncType, ordered []ast.Expr, filled []bool, preferGenericMissing bool) bool {
	if expr == nil || ft == nil {
		return false
	}
	explicitParamCount := funcTypeExplicitParamCount(ft)
	ok := true
	reportedGenericMissing := false
	for i := 0; i < explicitParamCount; i++ {
		if i < len(filled) && filled[i] {
			continue
		}
		if funcTypeExplicitParamHasDefault(ft, i) {
			defaultExpr := cloneDefaultArgExpr(funcTypeExplicitParamDefaultExpr(ft, i))
			if defaultExpr == nil {
				a.errorf(expr.Pos(), "internal error: unable to clone default argument for parameter %d on %q", i+1, ft.Name)
				ok = false
				continue
			}
			ordered[i] = defaultExpr
			if i < len(filled) {
				filled[i] = true
			}
			continue
		}
		if preferGenericMissing {
			if !reportedGenericMissing {
				a.errorf(expr.Pos(), "function %q expects %d arguments, got %d", ft.Name, explicitParamCount, len(expr.Args))
				reportedGenericMissing = true
			}
		} else if i < len(ft.ExplicitParamNames) && ft.ExplicitParamNames[i] != "" {
			a.errorf(expr.Pos(), "function %q is missing argument for parameter %q", ft.Name, ft.ExplicitParamNames[i])
		} else if !reportedGenericMissing {
			a.errorf(expr.Pos(), "function %q expects %d arguments, got %d", ft.Name, explicitParamCount, len(expr.Args))
			reportedGenericMissing = true
		}
		ok = false
	}
	return ok
}

type extensionMethodCallRewriteStatus int

const (
	extensionMethodCallRewriteNone extensionMethodCallRewriteStatus = iota
	extensionMethodCallRewriteApplied
	extensionMethodCallRewriteInvalid
)

func (a *Analyzer) rewriteExtensionMethodCall(expr *ast.CallExpr) extensionMethodCallRewriteStatus {
	if a == nil || expr == nil {
		return extensionMethodCallRewriteNone
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil || fieldExpr.Field == "" {
		return extensionMethodCallRewriteNone
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return extensionMethodCallRewriteNone
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	if receiverType == nil || IsInvalidType(receiverType) {
		return extensionMethodCallRewriteNone
	}
	if _, ok := a.lookupFieldNoError(receiverType, fieldExpr.Field); ok {
		return extensionMethodCallRewriteNone
	}
	if fieldExpr.Field == "where" {
		prependedArgs := make([]ast.Expr, 0, len(expr.Args)+1)
		prependedArgs = append(prependedArgs, fieldExpr.Object)
		prependedArgs = append(prependedArgs, expr.Args...)
		expr.Args = prependedArgs
		if len(expr.ArgNames) != 0 {
			prependedNames := make([]string, 0, len(expr.ArgNames)+1)
			prependedNames = append(prependedNames, "")
			prependedNames = append(prependedNames, expr.ArgNames...)
			expr.ArgNames = prependedNames
		}
		if len(expr.ArgShorthand) != 0 {
			prependedShorthand := make([]bool, 0, len(expr.ArgShorthand)+1)
			prependedShorthand = append(prependedShorthand, false)
			prependedShorthand = append(prependedShorthand, expr.ArgShorthand...)
			expr.ArgShorthand = prependedShorthand
		}
		if len(expr.ArgItemOrder) != 0 {
			prependedItems := make([]ast.CallArgItem, 0, len(expr.ArgItemOrder)+1)
			prependedItems = append(prependedItems, ast.CallArgItem{Position: fieldExpr.Object.Pos(), ArgIndex: 0})
			for _, item := range expr.ArgItemOrder {
				if item.IsPack {
					prependedItems = append(prependedItems, item)
					continue
				}
				item.ArgIndex++
				prependedItems = append(prependedItems, item)
			}
			expr.ArgItemOrder = prependedItems
		}
		expr.Func = &ast.Ident{Position: fieldExpr.Position, Name: fieldExpr.Field}
		return extensionMethodCallRewriteApplied
	}
	method, ok, err := a.lookupVisibleExtensionMethod(fieldExpr.Field, receiverType)
	if err != nil {
		a.errorf(expr.Pos(), "%s", err.Error())
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return extensionMethodCallRewriteInvalid
	}
	if !ok || method == nil || method.Symbol == nil {
		ufcsSym, ufcsOK, ufcsErr := a.lookupVisibleUFCSFunction(fieldExpr.Field, receiverType)
		if ufcsErr != nil {
			a.errorf(expr.Pos(), "%s", ufcsErr.Error())
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return extensionMethodCallRewriteInvalid
		}
		if !ufcsOK || ufcsSym == nil {
			return extensionMethodCallRewriteNone
		}
		ufcsType, _ := ufcsSym.Type.(*FuncType)
		receiverArg := fieldExpr.Object
		if ufcsType != nil && len(ufcsType.Params) != 0 {
			receiverArg = a.prepareUFCSReceiverArg(fieldExpr.Object, receiverType, ufcsType.Params[0])
		}
		prependedArgs := make([]ast.Expr, 0, len(expr.Args)+1)
		prependedArgs = append(prependedArgs, receiverArg)
		prependedArgs = append(prependedArgs, expr.Args...)
		expr.Args = prependedArgs
		if len(expr.ArgNames) != 0 {
			prependedNames := make([]string, 0, len(expr.ArgNames)+1)
			prependedNames = append(prependedNames, "")
			prependedNames = append(prependedNames, expr.ArgNames...)
			expr.ArgNames = prependedNames
		}
		if len(expr.ArgShorthand) != 0 {
			prependedShorthand := make([]bool, 0, len(expr.ArgShorthand)+1)
			prependedShorthand = append(prependedShorthand, false)
			prependedShorthand = append(prependedShorthand, expr.ArgShorthand...)
			expr.ArgShorthand = prependedShorthand
		}
		if len(expr.ArgItemOrder) != 0 {
			prependedItems := make([]ast.CallArgItem, 0, len(expr.ArgItemOrder)+1)
			prependedItems = append(prependedItems, ast.CallArgItem{Position: receiverArg.Pos(), ArgIndex: 0})
			for _, item := range expr.ArgItemOrder {
				if item.IsPack {
					prependedItems = append(prependedItems, item)
					continue
				}
				item.ArgIndex++
				prependedItems = append(prependedItems, item)
			}
			expr.ArgItemOrder = prependedItems
		}
		expr.Func = &ast.Ident{Position: fieldExpr.Position, Name: ufcsSym.Name}
		return extensionMethodCallRewriteApplied
	}
	prependedArgs := make([]ast.Expr, 0, len(expr.Args)+1)
	prependedArgs = append(prependedArgs, fieldExpr.Object)
	prependedArgs = append(prependedArgs, expr.Args...)
	expr.Args = prependedArgs
	if len(expr.ArgNames) != 0 {
		prependedNames := make([]string, 0, len(expr.ArgNames)+1)
		prependedNames = append(prependedNames, "")
		prependedNames = append(prependedNames, expr.ArgNames...)
		expr.ArgNames = prependedNames
	}
	if len(expr.ArgShorthand) != 0 {
		prependedShorthand := make([]bool, 0, len(expr.ArgShorthand)+1)
		prependedShorthand = append(prependedShorthand, false)
		prependedShorthand = append(prependedShorthand, expr.ArgShorthand...)
		expr.ArgShorthand = prependedShorthand
	}
	if len(expr.ArgItemOrder) != 0 {
		prependedItems := make([]ast.CallArgItem, 0, len(expr.ArgItemOrder)+1)
		prependedItems = append(prependedItems, ast.CallArgItem{Position: fieldExpr.Object.Pos(), ArgIndex: 0})
		for _, item := range expr.ArgItemOrder {
			if item.IsPack {
				prependedItems = append(prependedItems, item)
				continue
			}
			item.ArgIndex++
			prependedItems = append(prependedItems, item)
		}
		expr.ArgItemOrder = prependedItems
	}
	expr.Func = &ast.Ident{Position: fieldExpr.Position, Name: method.Symbol.Name}
	return extensionMethodCallRewriteApplied
}

func (a *Analyzer) prepareUFCSReceiverArg(receiver ast.Expr, receiverType Type, expected Type) ast.Expr {
	if a == nil || receiver == nil || receiverType == nil || expected == nil {
		return receiver
	}
	if AssignableTo(expected, receiverType) {
		return receiver
	}
	expectedRef, ok := expected.(*RefType)
	if !ok || expectedRef == nil {
		return receiver
	}
	if _, ok := implicitCallLikeRefUpcastType(expectedRef, receiverType); ok {
		return receiver
	}
	if !AssignableTo(expectedRef.Elem, receiverType) {
		return receiver
	}
	autoref := ast.Expr(&ast.AddrOfExpr{Position: receiver.Pos(), Operand: receiver})
	if expectedRef.State == RefStateNonNull {
		return autoref
	}
	return &ast.CastExpr{
		Position: receiver.Pos(),
		Operand:  autoref,
		Target:   astTypeExprForBuiltinMethodRewrite(receiver.Pos(), expectedRef),
		Origin:   ast.CastExprOriginGeneral,
	}
}

func ufcsPreparedReceiverType(actual Type, expected Type) Type {
	if actual == nil || expected == nil {
		return actual
	}
	if AssignableTo(expected, actual) {
		return actual
	}
	expectedRef, ok := expected.(*RefType)
	if !ok || expectedRef == nil {
		return actual
	}
	if upcastType, ok := implicitCallLikeRefUpcastType(expectedRef, actual); ok {
		return upcastType
	}
	if AssignableTo(expectedRef.Elem, actual) {
		return expected
	}
	return actual
}

func (a *Analyzer) exprResolvesToTypePath(expr ast.Expr) bool {
	if a == nil || expr == nil {
		return false
	}
	name, ok := qualifiedTypePathFromExpr(expr)
	if !ok || name == "" {
		return false
	}
	if _, ok := a.lookupTypeParam(name); ok {
		return true
	}
	_, _, ok = a.lookupVisibleType(name)
	return ok
}

func (a *Analyzer) analyzeTreeConstructorCallExpr(expr *ast.CallExpr, treeType *TreeCategoryType, variant *EnumVariant) Type {
	if expr == nil || treeType == nil {
		return invalidType
	}
	if variant == nil {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	orderedArgs, commonArgs, ok := a.resolveTreeConstructorArgs(expr, treeType, variant)
	if !ok {
		return treeType
	}
	if len(orderedArgs) != len(variant.Payload) {
		a.errorf(expr.Pos(), "tree constructor %q expects %d arguments, got %d", treeType.Name+"."+variant.Name, len(variant.Payload), len(expr.Args))
	}
	limit := len(orderedArgs)
	if len(variant.Payload) < limit {
		limit = len(variant.Payload)
	}
	for i := 0; i < len(orderedArgs); i++ {
		if i < limit {
			var actual Type
			orderedArgs[i], actual = a.analyzeCallLikeValueExpr(orderedArgs[i], variant.Payload[i])
			if !AssignableTo(variant.Payload[i], actual) {
				label := variant.PayloadLabel(i)
				if label != "" {
					a.errorf(orderedArgs[i].Pos(), "tree constructor argument %d (%s) to %q expects %s, got %s", i+1, label, treeType.Name+"."+variant.Name, variant.Payload[i], actual)
				} else {
					a.errorf(orderedArgs[i].Pos(), "tree constructor argument %d to %q expects %s, got %s", i+1, treeType.Name+"."+variant.Name, variant.Payload[i], actual)
				}
			}
			a.consumeAffineValueExpr(orderedArgs[i], variant.Payload[i], a.variantConstructorMoveReason("tree", treeType.Name, variant, i))
		} else {
			a.analyzeExpr(orderedArgs[i])
		}
	}
	for commonName, field := range treeType.Common {
		arg, ok := commonArgs[commonName]
		if !ok {
			continue
		}
		var actual Type
		commonArgs[commonName], actual = a.analyzeCallLikeValueExpr(arg, field.Type)
		if !AssignableTo(field.Type, actual) {
			a.errorf(commonArgs[commonName].Pos(), "tree common field %q for %q expects %s, got %s", commonName, treeType.Name+"."+variant.Name, field.Type, actual)
		}
		a.consumeAffineValueExpr(commonArgs[commonName], field.Type, "move into tree common field "+strconv.Quote(commonName))
	}
	return treeType
}

func (a *Analyzer) analyzeTreeExactMemberConstructorCallExpr(expr *ast.CallExpr, memberType Type) Type {
	if expr == nil || memberType == nil {
		return invalidType
	}
	fieldDecls := treeExactMemberFieldDecls(memberType)
	orderedArgs, ok := a.resolveTreeExactMemberConstructorArgs(expr, memberType)
	if !ok {
		return memberType
	}
	if len(orderedArgs) != len(fieldDecls) {
		a.errorf(expr.Pos(), "tree constructor %q expects %d arguments, got %d", memberType, len(fieldDecls), len(expr.Args))
	}
	limit := len(orderedArgs)
	if len(fieldDecls) < limit {
		limit = len(fieldDecls)
	}
	for i := 0; i < len(orderedArgs); i++ {
		if i >= limit {
			a.analyzeExpr(orderedArgs[i])
			continue
		}
		field, ok := TreeExactFieldInfo(memberType, fieldDecls[i].Name)
		if !ok {
			a.analyzeExpr(orderedArgs[i])
			continue
		}
		var actual Type
		orderedArgs[i], actual = a.analyzeCallLikeValueExpr(orderedArgs[i], field.Type)
		if !AssignableTo(field.Type, actual) {
			a.errorf(orderedArgs[i].Pos(), "tree constructor field %q for %q expects %s, got %s", fieldDecls[i].Name, memberType, field.Type, actual)
		}
		a.consumeAffineValueExpr(orderedArgs[i], field.Type, "move into tree constructor field "+strconv.Quote(fieldDecls[i].Name))
	}
	return memberType
}

func (a *Analyzer) tryConsumeSinkCallArg(funcExpr ast.Expr, fnType *FuncType, index int, arg ast.Expr, expected Type) bool {
	if a == nil || fnType == nil || arg == nil || !a.funcParamAllowsImplicitSink(funcExpr, fnType, index) {
		return false
	}
	if _, moved := explicitMoveOperand(arg); moved {
		return false
	}
	if !a.containsAffineHandleValues(expected, map[string]bool{}) {
		return false
	}
	key, ok := a.lookupAffineValueKey(arg)
	if !ok {
		return false
	}
	a.recordAffineConsumption(key, "argument to call "+strconv.Quote(fnType.Name))
	return true
}

func (a *Analyzer) funcParamAllowsImplicitSink(funcExpr ast.Expr, fnType *FuncType, index int) bool {
	if a == nil || fnType == nil || index < 0 {
		return false
	}
	if decl, resolvedType, ok := a.resolveSinkFuncDecl(funcExpr); ok && resolvedType != nil {
		if !resolvedType.SinkParamsKnown && decl != nil {
			a.inferFuncSinkParams(decl, resolvedType)
		}
		if resolvedType.SinkParamsKnown {
			if resolvedType != fnType {
				fnType.SinkParams = append([]bool(nil), resolvedType.SinkParams...)
				fnType.SinkParamsKnown = true
			}
			return index < len(resolvedType.SinkParams) && resolvedType.SinkParams[index]
		}
	}
	if !fnType.SinkParamsKnown {
		a.inferFuncSinkParamsForExpr(funcExpr, fnType)
	}
	return fnType.SinkParamsKnown && index < len(fnType.SinkParams) && fnType.SinkParams[index]
}
