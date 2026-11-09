package semantic

import (
	"elisacore/src/ast"
	"maps"
	"slices"
	"strings"
)

func (a *Analyzer) staticImplTypeParamBoundsSatisfied(impl *StaticImpl, subst map[string]Type) (bool, string) {
	if impl == nil {
		return false, "invalid static impl"
	}
	if impl.InvalidTypeParamBounds {
		return false, "an impl type-parameter interface bound could not be resolved"
	}
	for _, paramName := range slices.Sorted(maps.Keys(impl.TypeParamInterfaceBounds)) {
		boundName := impl.TypeParamInterfaceBounds[paramName]
		actual, ok := subst[paramName]
		if !ok || actual == nil {
			return false, "cannot infer impl type parameter " + paramName + " for bound " + boundName
		}
		if typeParam, ok := actual.(*TypeParamType); ok && typeParam != nil {
			bound, boundOK := a.lookupTypeParamInterface(typeParam.Name)
			if !boundOK || bound == nil || bound.Name != boundName {
				return false, "type parameter " + typeParam.Name + " does not satisfy impl bound " + boundName
			}
			continue
		}
		bound := a.staticInterfaces[boundName]
		if bound == nil {
			return false, "unknown impl type-parameter interface bound " + boundName
		}
		if !a.typeSatisfiesStaticInterface(actual, bound) {
			return false, "type " + actual.String() + " does not satisfy impl bound " + boundName
		}
	}
	return true, ""
}

func (a *Analyzer) staticImplTypeParamInterfaceBounds(params []ast.GenericParam) (map[string]string, bool) {
	bounds := make(map[string]string)
	invalid := false
	for _, param := range params {
		if param.Kind != ast.GenericParamType || param.InterfaceBound == "" {
			continue
		}
		_, name, ok := a.lookupVisibleStaticInterface(param.InterfaceBound)
		if !ok || name == "" {
			invalid = true
			continue
		}
		bounds[param.Name] = name
	}
	return bounds, invalid
}

func (a *Analyzer) staticImplsForReceiver(receiver Type) []*StaticImpl {
	if a == nil || receiver == nil || len(a.staticImpls) == 0 {
		return nil
	}
	receiver = unwrapReceiverRef(receiver)
	out := make([]*StaticImpl, 0, 2)
	for _, impl := range a.staticImpls {
		if impl == nil || impl.Receiver == nil {
			continue
		}
		if SameType(impl.Receiver, receiver) {
			out = append(out, impl)
		}
	}
	return out
}

func (a *Analyzer) resolveProjectedAssociatedType(named *ast.NamedType) (Type, bool) {
	if a == nil || named == nil {
		return nil, false
	}
	idx := strings.LastIndex(named.Name, ".")
	if idx <= 0 || idx+1 >= len(named.Name) {
		return nil, false
	}
	ownerName := named.Name[:idx]
	assocName := named.Name[idx+1:]
	if receiver, ok := a.lookupTypeParam(ownerName); ok {
		iface, ok := a.lookupTypeParamInterface(ownerName)
		if !ok || iface == nil {
			a.errorf(named.Pos(), "type parameter %q does not have an interface bound for associated type %q", ownerName, assocName)
			return invalidType, true
		}
		if _, ok := iface.AssociatedTypes[assocName]; !ok {
			a.errorf(named.Pos(), "interface %q has no associated type %q", iface.Name, assocName)
			return invalidType, true
		}
		projection := &AssociatedTypeProjection{Receiver: receiver, InterfaceName: iface.Name, Name: assocName}
		if _, stillAbstract := receiver.(*TypeParamType); !stillAbstract {
			if resolved, ok := ResolveAssociatedTypeProjection(projection, a.staticImpls); ok {
				return resolved, true
			}
		}
		return projection, true
	}
	receiver, _, ok := a.lookupVisibleType(ownerName)
	if !ok || receiver == nil {
		return nil, false
	}
	var matched Type
	matchCount := 0
	for _, impl := range a.staticImplsForReceiver(receiver) {
		if impl == nil {
			continue
		}
		assocType, ok := impl.AssociatedTypes[assocName]
		if !ok || assocType == nil {
			continue
		}
		matched = assocType
		matchCount++
	}
	if matchCount == 0 {
		return nil, false
	}
	if matchCount > 1 {
		a.errorf(named.Pos(), "associated type %q on %q is ambiguous across multiple impls", assocName, ownerName)
		return invalidType, true
	}
	return matched, true
}

func (a *Analyzer) resolveInterfaceMethodExprType(expr *ast.FieldExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	// Concrete calls through parametric impls retain their receiver expression
	// instead of spelling a static type path. Resolve that pre-recorded reference
	// from the receiver type and specialize the impl signature with the same
	// substitution used later by LLVM lowering.
	if ref := a.interfaceMethodRefs[expr]; ref != nil && ref.Receiver != nil {
		var impl *StaticImpl
		var subst map[string]Type
		var ok bool
		if ref.ImplKey != "" {
			impl = a.staticImpls[ref.ImplKey]
			ok = impl != nil
			if ok && len(impl.TypeParams) != 0 {
				subst, ok = UnifyTypePattern(impl.Receiver, ref.Receiver, impl.implTypeParamNames())
				if !ok {
					impl = nil
				}
			}
		} else {
			impl, subst, ok = LookupStaticImplUnifying(a.staticImpls, ref.InterfaceName, ref.Receiver)
		}
		if !ok || impl == nil {
			a.errorf(expr.Pos(), "type %s does not implement protocol %s", ref.Receiver, ref.InterfaceName)
			return invalidType, true
		}
		sym := impl.Methods[ref.MethodName]
		if sym == nil {
			a.errorf(expr.Pos(), "impl of protocol %s for %s is missing method %s", ref.InterfaceName, ref.Receiver, ref.MethodName)
			return invalidType, true
		}
		signature, ok := sym.Type.(*FuncType)
		if !ok || signature == nil {
			return invalidType, true
		}
		specialized, ok := a.substituteType(signature, subst, nil, nil, nil).(*FuncType)
		if !ok || specialized == nil {
			return invalidType, true
		}
		if len(ref.ExplicitTypeArgs) != 0 {
			params := genericParamsForFuncType(specialized)
			if len(params) == 0 {
				a.errorf(expr.Pos(), "method %q on %s is not generic", ref.MethodName, ref.Receiver)
				return invalidType, true
			}
			if len(ref.ExplicitTypeArgs) != len(params) {
				a.errorf(expr.Pos(), "method %q expects %d %s, got %d", ref.MethodName, len(params), genericArgLabel(params), len(ref.ExplicitTypeArgs))
			}
			bindings := make(map[string]Type, len(params))
			limit := len(ref.ExplicitTypeArgs)
			if len(params) < limit {
				limit = len(params)
			}
			for i := 0; i < limit; i++ {
				bindings[params[i].Name] = a.resolveGenericArgForParam(ref.ExplicitTypeArgs[i], params[i])
			}
			specialized, ok = a.substituteType(specialized, bindings, nil, nil, nil).(*FuncType)
			if !ok || specialized == nil {
				return invalidType, true
			}
			specialized.TypeParams = nil
			specialized.GenericParams = nil
		}
		return specialized, true
	}
	ownerName, ok := qualifiedTypePathFromExpr(expr.Object)
	if !ok || ownerName == "" {
		return nil, false
	}
	if receiver, ok := a.lookupTypeParam(ownerName); ok {
		iface, ok := a.lookupTypeParamInterface(ownerName)
		if !ok || iface == nil {
			return nil, false
		}
		method, ok := iface.Methods[expr.Field]
		if !ok || method == nil || method.Signature == nil {
			return nil, false
		}
		a.interfaceMethodRefs[expr] = &InterfaceMethodRef{InterfaceName: iface.Name, MethodName: expr.Field}
		return a.specializeInterfaceMethodSignature(method.Signature, receiver), true
	}
	// Abstract effect operations are namespace-like calls (`Writer.write(x)`),
	// not receiver methods. Their signature is still recorded here so malformed
	// or unhandled calls get ordinary type diagnostics after the handler rewrite.
	if iface, _, ok := a.lookupVisibleStaticInterface(ownerName); ok && iface != nil && iface.Decl != nil && iface.Decl.IsEffect {
		method, ok := iface.Methods[expr.Field]
		if !ok || method == nil || method.Signature == nil {
			return nil, false
		}
		a.interfaceMethodRefs[expr] = &InterfaceMethodRef{InterfaceName: iface.Name, MethodName: expr.Field}
		return method.Signature, true
	}
	receiver, _, ok := a.lookupVisibleType(ownerName)
	if !ok || receiver == nil {
		return nil, false
	}
	var matchedImpl *StaticImpl
	var matchedSym *Symbol
	for _, impl := range a.staticImplsForReceiver(receiver) {
		if impl == nil {
			continue
		}
		sym, ok := impl.Methods[expr.Field]
		if !ok || sym == nil {
			continue
		}
		if matchedImpl != nil {
			a.errorf(expr.Pos(), "static method %q on %q is ambiguous across multiple impls", expr.Field, ownerName)
			return invalidType, true
		}
		matchedImpl = impl
		matchedSym = sym
	}
	if matchedImpl == nil || matchedSym == nil {
		return nil, false
	}
	sig, ok := matchedSym.Type.(*FuncType)
	if !ok || sig == nil {
		return invalidType, true
	}
	a.interfaceMethodRefs[expr] = &InterfaceMethodRef{InterfaceName: matchedImpl.InterfaceName, MethodName: expr.Field, ImplKey: StaticImplLookupKey(matchedImpl.InterfaceName, matchedImpl.Receiver)}
	return sig, true
}
