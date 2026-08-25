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
	if !expr.HasArgForward && expr.NamedArgCount() == 0 {
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
	nextPositional := 0
	ok := true
	for _, item := range orderedCallArgItems(expr) {
		arg := expr.Args[item.ArgIndex]
		name := expr.ArgName(item.ArgIndex)
		if name == "" {
			if expr.HasArgForward {
				a.errorf(arg.Pos(), "call argument forwarding `..` only supports named arguments")
				a.analyzeExpr(arg)
				ok = false
				continue
			}
			if sawNamed {
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
			if sources[nextPositional] == callArgSourceExplicit {
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

// rewriteFreeCallReceiverOverload gives free-call argument-based overload
// resolution. `N(arg0, rest...)` is rewritten to the receiver form
// `arg0.N(rest...)` when `N` is receiver-overloaded, the primary global `N` does
// NOT accept arg0, and a UFCS overload of `N` does. The subsequent
// rewriteExtensionMethodCall then selects the right overload and threads the
// resolved symbol to codegen — so `open(door)` and `open("file")` both resolve to
// the correct overload (uniform call syntax), the same as `door.open()`.
func (a *Analyzer) rewriteFreeCallReceiverOverload(expr *ast.CallExpr) {
	if a == nil || expr == nil {
		return
	}
	// Accept both the plain `name(args)` and the explicitly-type-argged
	// `name[TypeArgs](args)` form (the latter parses as a SpecializeExpr wrapping
	// the ident). Capture the type args so the rewritten FieldExpr can carry them
	// — otherwise an overloaded free call with explicit type args never reaches
	// receiver-based disambiguation and latches onto the primary overload.
	ident, ok := expr.Func.(*ast.Ident)
	var callTypeArgs []ast.TypeExpr
	var specPos lexer.Pos
	if !ok {
		if spec, sok := expr.Func.(*ast.SpecializeExpr); sok && spec != nil {
			if id, iok := spec.Operand.(*ast.Ident); iok && id != nil {
				ident = id
				ok = true
				callTypeArgs = spec.TypeArgs
				specPos = spec.Position
			}
		}
	}
	if !ok || ident == nil || ident.Name == "" {
		return
	}
	// Every function is registered for UFCS, so only ">= 2 entries" signals an
	// actual overload set worth disambiguating by argument type.
	if len(a.ufcsFunctionsByName[ident.Name]) < 2 {
		return
	}
	if len(expr.Args) == 0 {
		return
	}
	// Keep to the simple all-positional case so the parallel arg metadata stays
	// consistent after dropping arg0; bail on named/shorthand/pack/forward args.
	if len(expr.ArgNames) != 0 || len(expr.ArgShorthand) != 0 || len(expr.ArgItemOrder) != 0 || expr.HasArgForward {
		return
	}
	// Probe arg0's type quietly: a context-dependent arg0 (a `.Variant` shorthand,
	// an untyped literal, …) needs an expected type to resolve and must be left to
	// normal resolution (which supplies the primary overload's param type).
	savedSuppress := a.suppressDiagnostics
	a.suppressDiagnostics = true
	arg0Type := a.analyzeExpr(expr.Args[0])
	a.suppressDiagnostics = savedSuppress
	if arg0Type == nil || IsInvalidType(arg0Type) {
		return
	}
	// Pick the best-matching overload for arg0 by arity + specificity (most concrete
	// wins). The call provides len(expr.Args) arguments to the resolved function.
	best, ok, _ := a.lookupVisibleUFCSFunctionWithArity(ident.Name, arg0Type, len(expr.Args))
	if !ok || best == nil {
		return
	}
	// If the best match IS the bare global primary, normal resolution already
	// selects it — leave it as a normal free call (e.g. `open("file")` keeps
	// resolving to the bare cstr overload, and a generic primary that is genuinely
	// the most specific match stays a direct call). Rewrite to UFCS only when a more
	// specific NON-primary overload should win.
	if primary, pok := a.globalScope.Lookup(ident.Name); pok && primary == best {
		return
	}
	receiver := expr.Args[0]
	field := &ast.FieldExpr{Position: ident.Position, Object: receiver, Field: ident.Name}
	if len(callTypeArgs) != 0 {
		// Re-wrap as `receiver.name[TypeArgs]` so rewriteExtensionMethodCall (which
		// runs next) re-applies the explicit type args to the resolved callee.
		expr.Func = &ast.SpecializeExpr{Position: specPos, Operand: field, TypeArgs: callTypeArgs}
	} else {
		expr.Func = field
	}
	expr.Args = expr.Args[1:]
}

// rewriteBoundTypeParamMethodCall handles `value.method(args...)` where `value` has a
// bare type-parameter type whose generic bound declares `method`. It rewrites the call
// in place to the static interface-method form `T.method(value, args...)`, prepending
// the receiver value as the first argument and pointing the callee at a FieldExpr whose
// object is the type-parameter name (which resolveInterfaceMethodExprType resolves).
// Returns extensionMethodCallRewriteNone when the receiver is not a bound type param or
// the bound protocol has no such method, leaving normal resolution untouched.
func (a *Analyzer) rewriteBoundTypeParamMethodCall(expr *ast.CallExpr, fieldExpr *ast.FieldExpr, receiverType Type, callTypeArgs []ast.TypeExpr) extensionMethodCallRewriteStatus {
	// A receiver value whose type is a bound type parameter may arrive wrapped in a
	// reference (`r: mutable R&`) or an aggregate-state wrapper; unwrap so the same
	// protocol-method dispatch fires for by-ref receivers as for by-value ones. The
	// prepended receiver arg (fieldExpr.Object) keeps its reference type and is
	// autoderef/autoref-matched to the protocol method's self parameter downstream.
	tp, ok := StripAggregateStateType(unwrapReceiverRef(receiverType)).(*TypeParamType)
	if !ok || tp == nil || tp.Name == "" {
		return extensionMethodCallRewriteNone
	}
	iface, ok := a.lookupTypeParamInterface(tp.Name)
	if !ok || iface == nil {
		return extensionMethodCallRewriteNone
	}
	method, ok := iface.Methods[fieldExpr.Field]
	if !ok || method == nil || method.Signature == nil {
		return extensionMethodCallRewriteNone
	}
	// The protocol method's first parameter is the receiver slot; require at least one
	// parameter so the receiver value has somewhere to bind.
	if len(method.Signature.Params) == 0 {
		return extensionMethodCallRewriteNone
	}
	typePathObject := &ast.Ident{Position: fieldExpr.Object.Pos(), Name: tp.Name}
	callee := ast.Expr(&ast.FieldExpr{Position: fieldExpr.Position, Object: typePathObject, Field: fieldExpr.Field})
	if len(callTypeArgs) != 0 {
		callee = &ast.SpecializeExpr{Position: fieldExpr.Position, Operand: callee, TypeArgs: callTypeArgs}
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
			item.ArgIndex++
			prependedItems = append(prependedItems, item)
		}
		expr.ArgItemOrder = prependedItems
	}
	expr.Func = callee
	// Behavioral-subtyping caller obligation (docs P1): when calling a protocol method through a
	// bounded type param, the CALLER must prove the protocol method's `requires` (substituting the
	// actual receiver/args) from the facts it has — knowing only the protocol's contract, not any
	// concrete impl. The protocol method's `requires`/params live on its bodiless decl. The
	// prepended args (receiver first) align positionally with the protocol method's params.
	if protoDecl := method.Decl; protoDecl != nil && len(protoDecl.Requires) > 0 {
		a.checkCalleeRequires(expr, iface.Name+"."+fieldExpr.Field, protoDecl.Requires, protoDecl.Params, expr.Args)
	} else if def := method.Default; def != nil && len(def.Requires) > 0 {
		a.checkCalleeRequires(expr, iface.Name+"."+fieldExpr.Field, def.Requires, def.Params, expr.Args)
	}
	return extensionMethodCallRewriteApplied
}

// rewriteConcreteImplMethodCall handles `value.method(args...)` where `value` has a concrete
// type that conforms to a protocol (via `impl Proto for T`) declaring `method` — including
// protocol default methods, which are synthesized into impls as ordinary `__impl__…` symbols.
// It rewrites the call in place to the qualified static interface-method form
// `T.method(value, args...)`, prepending the receiver value as the first argument and pointing
// the callee at a FieldExpr whose object is the receiver's type-path name (which
// resolveInterfaceMethodExprType resolves and records as an InterfaceMethodRef for dispatch).
// Returns extensionMethodCallRewriteNone when the receiver type has no conforming impl method,
// leaving normal resolution (extension methods / UFCS free functions) untouched.
func (a *Analyzer) rewriteConcreteImplMethodCall(expr *ast.CallExpr, fieldExpr *ast.FieldExpr, receiverType Type, callTypeArgs []ast.TypeExpr) extensionMethodCallRewriteStatus {
	if receiverType == nil || IsInvalidType(receiverType) {
		return extensionMethodCallRewriteNone
	}
	impl, ok := a.staticImplMethodForReceiver(receiverType, fieldExpr.Field, expr)
	if !ok || impl == nil {
		return extensionMethodCallRewriteNone
	}
	typeName, ok := a.typePathNameForReceiver(receiverType)
	if !ok || typeName == "" {
		return extensionMethodCallRewriteNone
	}
	typePathObject := &ast.Ident{Position: fieldExpr.Object.Pos(), Name: typeName}
	callee := ast.Expr(&ast.FieldExpr{Position: fieldExpr.Position, Object: typePathObject, Field: fieldExpr.Field})
	if len(callTypeArgs) != 0 {
		callee = &ast.SpecializeExpr{Position: fieldExpr.Position, Operand: callee, TypeArgs: callTypeArgs}
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
			item.ArgIndex++
			prependedItems = append(prependedItems, item)
		}
		expr.ArgItemOrder = prependedItems
	}
	expr.Func = callee
	return extensionMethodCallRewriteApplied
}

func (a *Analyzer) rewriteExtensionMethodCall(expr *ast.CallExpr) extensionMethodCallRewriteStatus {
	if a == nil || expr == nil {
		return extensionMethodCallRewriteNone
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	// `recv.method[TypeArgs](args)` parses as a SpecializeExpr wrapping the FieldExpr.
	// Capture the explicit type args and unwrap to the FieldExpr so UFCS resolution
	// proceeds; the resolved callee is re-wrapped with the type args below.
	var callTypeArgs []ast.TypeExpr
	if !ok {
		if spec, sok := expr.Func.(*ast.SpecializeExpr); sok && spec != nil {
			if fe, fok := spec.Operand.(*ast.FieldExpr); fok && fe != nil {
				fieldExpr = fe
				callTypeArgs = spec.TypeArgs
				ok = true
			}
		}
	}
	if !ok || fieldExpr == nil || fieldExpr.Object == nil || fieldExpr.Field == "" {
		return extensionMethodCallRewriteNone
	}
	withCallTypeArgs := func(fn ast.Expr) ast.Expr {
		if len(callTypeArgs) == 0 {
			return fn
		}
		return &ast.SpecializeExpr{Position: fieldExpr.Position, Operand: fn, TypeArgs: callTypeArgs}
	}
	if a.rewriteTypestateConstructorCall(expr, fieldExpr) {
		return extensionMethodCallRewriteApplied
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return extensionMethodCallRewriteNone
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	if receiverType == nil || IsInvalidType(receiverType) {
		return extensionMethodCallRewriteNone
	}
	// docs/126 §2: `value.drop()` is the explicit early release of a drop-typed value.
	// Lower it to the destructor call it stands for, moving the receiver in, so the
	// ordinary affine machinery both rejects a later use and elides the scope-exit drop.
	if a.rewriteExplicitDropCall(expr, fieldExpr, receiverType) {
		return extensionMethodCallRewriteApplied
	}
	if _, ok := a.lookupFieldNoError(receiverType, fieldExpr.Field); ok {
		return extensionMethodCallRewriteNone
	}
	// Bounded generic instance-method dispatch: when the receiver has a bare
	// type-parameter type `T` whose generic bound `T: Protocol` declares a method
	// named `fieldExpr.Field`, resolve `value.method(args...)` against the bound
	// protocol by rewriting it to the static interface-method form
	// `T.method(value, args...)` (the receiver value becomes the first argument).
	// resolveInterfaceMethodExprType already specializes the protocol signature with
	// the type-param receiver and records the InterfaceMethodRef for dispatch.
	if status := a.rewriteBoundTypeParamMethodCall(expr, fieldExpr, receiverType, callTypeArgs); status != extensionMethodCallRewriteNone {
		return status
	}
	if proofCarryingViewReceiverHelper(fieldExpr.Field) {
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
				item.ArgIndex++
				prependedItems = append(prependedItems, item)
			}
			expr.ArgItemOrder = prependedItems
		}
		expr.Func = withCallTypeArgs(&ast.Ident{Position: fieldExpr.Position, Name: fieldExpr.Field})
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
		ufcsSym, ufcsOK, ufcsErr := a.lookupVisibleUFCSFunctionWithArity(fieldExpr.Field, receiverType, 1+len(expr.Args))
		if ufcsErr != nil {
			a.errorf(expr.Pos(), "%s", ufcsErr.Error())
			for _, arg := range expr.Args {
				a.analyzeExpr(arg)
			}
			return extensionMethodCallRewriteInvalid
		}
		if !ufcsOK || ufcsSym == nil {
			// No extension method or free UFCS function matched: fall back to protocol-impl
			// UFCS dispatch (`value.method(args)` where the receiver's concrete type conforms
			// to a protocol — incl. default methods — declaring `method`).
			if status := a.rewriteConcreteImplMethodCall(expr, fieldExpr, receiverType, callTypeArgs); status != extensionMethodCallRewriteNone {
				return status
			}
			return extensionMethodCallRewriteNone
		}
		if ufcsSym.Deprecated != "" {
			a.deprecatedf(expr.Pos(), "%s", ufcsSym.Deprecated)
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
				item.ArgIndex++
				prependedItems = append(prependedItems, item)
			}
			expr.ArgItemOrder = prependedItems
		}
		expr.Func = withCallTypeArgs(&ast.Ident{Position: fieldExpr.Position, Name: ufcsSym.Name})
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
			item.ArgIndex++
			prependedItems = append(prependedItems, item)
		}
		expr.ArgItemOrder = prependedItems
	}
	expr.Func = withCallTypeArgs(&ast.Ident{Position: fieldExpr.Position, Name: method.Symbol.Name})
	return extensionMethodCallRewriteApplied
}

func (a *Analyzer) rewriteTypestateConstructorCall(expr *ast.CallExpr, fieldExpr *ast.FieldExpr) bool {
	if a == nil || expr == nil || fieldExpr == nil || fieldExpr.Field != "new" {
		return false
	}
	typeName, ok := fieldExpr.Object.(*ast.Ident)
	if !ok || typeName == nil || typeName.Name == "" || !a.exprResolvesToTypePath(fieldExpr.Object) {
		return false
	}
	hiddenName := "__typestate_" + typeName.Name + "_new"
	if _, _, found := a.lookupVisibleGlobal(hiddenName); !found {
		return false
	}
	expr.Func = &ast.Ident{Position: fieldExpr.Position, Name: hiddenName}
	return true
}

func proofCarryingViewReceiverHelper(name string) bool {
	switch name {
	case "enumerate",
		"any", "all", "column", "where_kind",
		"readonly", "split_at", "chunks_exact", "reduce_sum":
		return true
	default:
		return false
	}
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
