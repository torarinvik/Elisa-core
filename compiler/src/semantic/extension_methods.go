package semantic

import (
	"fmt"
	"strings"

	"elisacore/src/ast"
)

type ExtensionMethod struct {
	VisibleName string
	Receiver    Type
	Symbol      *Symbol
	Decl        ast.Node
}

func ExtensionMethodSymbolName(visibleName string, receiver Type, methodName string) string {
	return "__ext__" + sanitizeStaticInterfaceSymbolFragment(visibleName) + "__" + sanitizeStaticInterfaceSymbolFragment(TypeIdentityKey(receiver)) + "__" + sanitizeStaticInterfaceSymbolFragment(methodName)
}

func ReceiverOverloadSymbolName(visibleName string, receiver Type, methodName string) string {
	// Mangle the receiver by its type String() (the same clean scheme mangleGenericType
	// uses for type args), NOT TypeIdentityKey — TypeIdentityKey prepends the Go runtime
	// type (`%T`, e.g. `*semantic.RefType`), which leaked `semantic_RefType_...` into the
	// emitted symbol name. String() already uniquely identifies the receiver type.
	return "__ovl__" + sanitizeStaticInterfaceSymbolFragment(visibleName) + "__" + sanitizeStaticInterfaceSymbolFragment(receiver.String()) + "__" + sanitizeStaticInterfaceSymbolFragment(methodName)
}

func (a *Analyzer) validateExtensionMethodSignature(visibleName string, receiver Type, fnType *FuncType, decl ast.Node) bool {
	if a == nil || receiver == nil || fnType == nil || decl == nil {
		return false
	}
	if len(fnType.Params) == 0 {
		a.errorf(decl.Pos(), "extension method %q on %s must take the receiver as its first parameter", visibleName, receiver)
		return false
	}
	if !SameType(fnType.Params[0], receiver) {
		a.errorf(decl.Pos(), "extension method %q on %s must take %s as its first parameter, got %s", visibleName, receiver, receiver, fnType.Params[0])
		return false
	}
	return true
}

func (a *Analyzer) registerExtensionMethod(visibleName string, receiver Type, sym *Symbol, decl ast.Node, fnType *FuncType) {
	if a == nil || visibleName == "" || receiver == nil || sym == nil || decl == nil || fnType == nil {
		return
	}
	if !a.validateExtensionMethodSignature(visibleName, receiver, fnType, decl) {
		return
	}
	methods := a.extensionMethodsByName[visibleName]
	for _, existing := range methods {
		if existing == nil || existing.Receiver == nil {
			continue
		}
		if SameType(existing.Receiver, receiver) {
			a.errorf(decl.Pos(), "duplicate extension method %q on %s", visibleName, receiver)
			return
		}
	}
	a.extensionMethodsByName[visibleName] = append(methods, &ExtensionMethod{
		VisibleName: visibleName,
		Receiver:    receiver,
		Symbol:      sym,
		Decl:        decl,
	})
}

func (a *Analyzer) lookupVisibleExtensionMethod(name string, actualReceiver Type) (*ExtensionMethod, bool, error) {
	if a == nil || name == "" || actualReceiver == nil {
		return nil, false, nil
	}
	for _, candidate := range a.visibleNameCandidates(name) {
		var matched *ExtensionMethod
		for _, method := range a.extensionMethodsByName[candidate] {
			if method == nil || method.Symbol == nil || method.Receiver == nil {
				continue
			}
			if !AssignableTo(method.Receiver, actualReceiver) {
				continue
			}
			if matched != nil {
				return nil, false, fmt.Errorf("extension method %q on %s is ambiguous", name, diagnosticTypeString(actualReceiver))
			}
			matched = method
		}
		if matched != nil {
			return matched, true, nil
		}
	}
	return nil, false, nil
}

func (a *Analyzer) lookupVisibleUFCSFunction(name string, actualReceiver Type) (*Symbol, bool, error) {
	if a == nil || name == "" || actualReceiver == nil || a.globalScope == nil {
		return nil, false, nil
	}
	var (
		matched        *Symbol
		matchedName    string
		ambiguousNames []string
	)
	for _, candidate := range a.visibleNameCandidates(name) {
		candidates := a.ufcsFunctionsByName[candidate]
		if len(candidates) == 0 {
			if sym, ok := a.globalScope.Lookup(candidate); ok && sym != nil {
				candidates = append(candidates, sym)
			}
		}
		for _, sym := range candidates {
			if sym == nil {
				continue
			}
			if sym.Kind != SymbolFunc && sym.Kind != SymbolExternFunc {
				continue
			}
			fnType, ok := sym.Type.(*FuncType)
			if !ok || fnType == nil || len(fnType.Params) == 0 {
				continue
			}
			if !a.ufcsReceiverAssignableTo(fnType.Params[0], actualReceiver) {
				continue
			}
			if matched != nil {
				if len(ambiguousNames) == 0 {
					ambiguousNames = append(ambiguousNames, matchedName)
				}
				ambiguousNames = append(ambiguousNames, candidate)
				continue
			}
			matched = sym
			matchedName = candidate
		}
	}
	if len(ambiguousNames) != 0 {
		return nil, false, fmt.Errorf("UFCS call %q on %s is ambiguous: %s", name, diagnosticTypeString(actualReceiver), strings.Join(ambiguousNames, ", "))
	}
	if matched == nil {
		return nil, false, nil
	}
	return matched, true, nil
}

func (a *Analyzer) ufcsReceiverAssignableTo(expected Type, actual Type) bool {
	if a == nil || expected == nil || actual == nil {
		return false
	}
	// A raw u8 string pointer (the type of a string literal, `static u8&`) serves
	// as a cstr / string-view receiver, mirroring the contextual string-literal
	// coercion used in ordinary argument position. This makes `"x".open()` resolve
	// to `def open(s: cstr)` the same way the free call `open("x")` already does.
	if _, ok := u8RuntimeRef(actual); ok {
		if _, ok := contextualStringLiteralType(expected); ok {
			return true
		}
	}
	// A bare integer literal has the untyped `int` type; let it serve as a receiver
	// of any integer-typed parameter (the literal is re-typed when prepended as the
	// argument), mirroring ordinary argument coercion so `7.inc()` works. Float
	// literals already get a concrete type (f64) and need no special case.
	if an, ok := builtinNumericTypeName(actual); ok && an == "int" {
		if en, ok := builtinNumericTypeName(expected); ok && en != "f32" && en != "f64" {
			return true
		}
	}
	expectedBase := expected
	if expectedRef, ok := expected.(*RefType); ok && expectedRef != nil {
		expectedBase = expectedRef.Elem
	}
	actualBase := actual
	if actualRef, ok := actual.(*RefType); ok && actualRef != nil {
		actualBase = actualRef.Elem
	}
	if !ufcsReceiverBaseCompatible(expectedBase, actualBase) {
		return false
	}
	if SameType(expected, actual) {
		return true
	}
	if AssignableTo(expected, actual) {
		return true
	}
	expectedRef, ok := expected.(*RefType)
	if !ok || expectedRef == nil {
		return false
	}
	if actualRef, ok := actual.(*RefType); ok && actualRef != nil {
		if ufcsReceiverBaseCompatible(expectedRef.Elem, actualRef.Elem) {
			return expectedRef.State == RefStateNonNull || expectedRef.State == RefStateNullable
		}
	}
	if upcastType, ok := implicitCallLikeRefUpcastType(expectedRef, actual); ok && SameType(upcastType, expected) {
		return true
	}
	if !ufcsReceiverBaseCompatible(expectedRef.Elem, actual) {
		return false
	}
	return expectedRef.State == RefStateNonNull || expectedRef.State == RefStateNullable
}

func ufcsReceiverBaseCompatible(expected Type, actual Type) bool {
	if expected == nil || actual == nil {
		return false
	}
	expected = StripAggregateStateType(expected)
	actual = StripAggregateStateType(actual)
	if SameType(expected, actual) {
		return true
	}
	if matchTypePattern(expected, actual) || assignableRuntimeCompatible(expected, actual) {
		return true
	}
	switch exp := expected.(type) {
	case *GenericInstanceType:
		act, ok := actual.(*GenericInstanceType)
		if !ok || act == nil {
			return false
		}
		return exp.Name == act.Name && SameType(exp.Base, act.Base)
	case *StructType:
		switch act := actual.(type) {
		case *StructType:
			return SameType(exp, act)
		case *GenericInstanceType:
			return SameType(exp, act.Base)
		}
	}
	return TypeIdentityKey(expected) == TypeIdentityKey(actual)
}

func receiverOverloadType(sym *Symbol) (Type, bool) {
	if sym == nil {
		return nil, false
	}
	if sym.Kind != SymbolFunc && sym.Kind != SymbolExternFunc {
		return nil, false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || len(fnType.Params) == 0 {
		return nil, false
	}
	return fnType.Params[0], true
}

func (a *Analyzer) registerUFCSFunction(visibleName string, sym *Symbol) {
	if a == nil || visibleName == "" || sym == nil {
		return
	}
	if _, ok := receiverOverloadType(sym); !ok {
		return
	}
	methods := a.ufcsFunctionsByName[visibleName]
	for _, existing := range methods {
		if existing == sym {
			return
		}
	}
	a.ufcsFunctionsByName[visibleName] = append(methods, sym)
}
