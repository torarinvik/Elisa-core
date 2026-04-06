package semantic

import (
	"fmt"

	"llcontext/src/ast"
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
		a.errorf(decl.Pos(), "extension method %q on %s must take the receiver as its first parameter", visibleName, receiver.String())
		return false
	}
	if !SameType(fnType.Params[0], receiver) {
		a.errorf(decl.Pos(), "extension method %q on %s must take %s as its first parameter, got %s", visibleName, receiver.String(), receiver.String(), fnType.Params[0].String())
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
			a.errorf(decl.Pos(), "duplicate extension method %q on %s", visibleName, receiver.String())
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
				return nil, false, fmt.Errorf("extension method %q on %s is ambiguous", name, actualReceiver.String())
			}
			matched = method
		}
		if matched != nil {
			return matched, true, nil
		}
	}
	return nil, false, nil
}
