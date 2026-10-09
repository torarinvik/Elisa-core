package semantic

import (
	"maps"
	"slices"

	"elisacore/src/ast"
)

// concreteImplCastCall resolves the target-named postfix cast `value.Target()`
// through a concrete protocol impl's `__cast__` method. Exact global cast hooks
// and type-parameter-bound casts are handled earlier. The requested target filters
// candidates before ambiguity is checked, so distinct return-type overloads remain
// usable through their target type names.
func (a *Analyzer) concreteImplCastCall(cast *ast.CastExpr, source, target Type) (*ast.CallExpr, bool) {
	if a == nil || cast == nil || cast.Operand == nil || source == nil || target == nil || IsInvalidType(source) || IsInvalidType(target) {
		return nil, false
	}
	receiver := unwrapReceiverRef(source)
	if receiver == nil || IsInvalidType(receiver) {
		return nil, false
	}
	type candidate struct {
		impl *StaticImpl
	}
	matches := make([]candidate, 0, 1)
	for _, key := range slices.Sorted(maps.Keys(a.staticImpls)) {
		impl := a.staticImpls[key]
		if impl == nil || impl.Receiver == nil {
			continue
		}
		sym := impl.Methods["__cast__"]
		if sym == nil || a.isSelfCastHook(sym) {
			// A cast method must not resolve its own target-named conversion back
			// to itself; let the ordinary conversion path handle that expression.
			continue
		}
		var subst map[string]Type
		if len(impl.TypeParams) != 0 {
			var ok bool
			subst, ok = UnifyTypePattern(impl.Receiver, receiver, impl.implTypeParamNames())
			if !ok {
				continue
			}
			if ok, _ := a.staticImplTypeParamBoundsSatisfied(impl, subst); !ok {
				continue
			}
		} else if !SameType(impl.Receiver, receiver) {
			continue
		}
		signature, ok := sym.Type.(*FuncType)
		if !ok || signature == nil || funcTypeExplicitParamCount(signature) != 1 || len(signature.ImplicitParamNames) != 0 || len(genericParamsForFuncType(signature)) != 0 || len(signature.RegionParams) != 0 || len(signature.PermissionParams) != 0 || signature.Variadic {
			continue
		}
		specialized, ok := a.substituteType(signature, subst, nil, nil, nil).(*FuncType)
		if !ok || specialized == nil || specialized.Return == nil || !SameType(specialized.Return, target) {
			continue
		}
		matches = append(matches, candidate{impl: impl})
	}
	if len(matches) == 0 {
		return nil, false
	}
	if len(matches) > 1 {
		a.errorf(cast.Pos(), "postfix cast from %s to %s is ambiguous across multiple protocol impls", receiver, target)
		return nil, true
	}
	impl := matches[0].impl
	field := &ast.FieldExpr{Position: cast.Position, Object: cast.Operand, Field: "__cast__"}
	a.interfaceMethodRefs[field] = &InterfaceMethodRef{
		InterfaceName: impl.InterfaceName,
		MethodName:    "__cast__",
		ImplKey:       StaticImplLookupKey(impl.InterfaceName, impl.Receiver),
		Receiver:      receiver,
	}
	return &ast.CallExpr{
		Position: cast.Position,
		Func:     field,
		Args:     []ast.Expr{cast.Operand},
	}, true
}
