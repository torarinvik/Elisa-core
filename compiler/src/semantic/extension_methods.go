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
		sym, ok := a.globalScope.Lookup(candidate)
		if !ok || sym == nil {
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
	if AssignableTo(expected, actual) {
		return true
	}
	expectedRef, ok := expected.(*RefType)
	if !ok || expectedRef == nil {
		return false
	}
	if _, ok := implicitCallLikeRefUpcastType(expectedRef, actual); ok {
		return true
	}
	if !AssignableTo(expectedRef.Elem, actual) {
		return false
	}
	return expectedRef.State == RefStateNonNull || expectedRef.State == RefStateNullable
}
