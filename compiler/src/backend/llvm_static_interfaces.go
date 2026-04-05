//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"strings"

	"llcontext/src/ast"
	"llcontext/src/semantic"
)

func (s *functionState) interfaceMethodRef(expr *ast.FieldExpr) (*semantic.InterfaceMethodRef, bool) {
	if s == nil || s.g == nil || s.g.result == nil || expr == nil || s.g.result.InterfaceMethodRefs == nil {
		return nil, false
	}
	ref, ok := s.g.result.InterfaceMethodRefs[expr]
	return ref, ok && ref != nil
}

func (s *functionState) resolveStaticInterfaceMethod(expr *ast.FieldExpr) (*semantic.Symbol, *semantic.FuncType, bool, error) {
	ref, ok := s.interfaceMethodRef(expr)
	if !ok || ref == nil {
		return nil, nil, false, nil
	}
	receiver := s.exprType(expr.Object)
	if receiver == nil {
		if ownerName, ok := backendQualifiedTypePath(expr.Object); ok && ownerName != "" {
			if bound, ok := s.typeMap[ownerName]; ok && bound != nil {
				receiver = bound
			} else if named, ok := s.g.result.NamedTypes[ownerName]; ok && named != nil {
				receiver = named
			}
		}
	}
	if receiver == nil {
		return nil, nil, true, fmt.Errorf("missing receiver type for static interface method %s.%s", ref.InterfaceName, ref.MethodName)
	}
	impl, ok := semantic.LookupStaticImpl(s.g.result.StaticImpls, ref.InterfaceName, receiver)
	if !ok || impl == nil {
		return nil, nil, true, fmt.Errorf("type %s does not implement interface %s", receiver.String(), ref.InterfaceName)
	}
	sym, ok := impl.Methods[ref.MethodName]
	if !ok || sym == nil {
		return nil, nil, true, fmt.Errorf("impl of interface %s for %s is missing method %s", ref.InterfaceName, receiver.String(), ref.MethodName)
	}
	fnType, ok := sym.Type.(*semantic.FuncType)
	if !ok || fnType == nil {
		return nil, nil, true, fmt.Errorf("static interface method %s.%s does not resolve to a function type", ref.InterfaceName, ref.MethodName)
	}
	return sym, fnType, true, nil
}

func (s *functionState) emitStaticInterfaceMethodExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	sym, fnType, handled, err := s.resolveStaticInterfaceMethod(expr)
	if !handled {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, true, err
	}
	callee, err := s.g.ensureFunctionDeclared(sym.Name, fnType)
	if err != nil {
		return nil, nil, true, err
	}
	return callee, fnType, true, nil
}

func (s *functionState) resolveStaticAssociatedType(name string) (semantic.Type, bool, error) {
	if s == nil || s.g == nil || s.g.result == nil || name == "" {
		return nil, false, nil
	}
	idx := strings.LastIndex(name, ".")
	if idx <= 0 || idx+1 >= len(name) {
		return nil, false, nil
	}
	ownerName := name[:idx]
	assocName := name[idx+1:]
	var receiver semantic.Type
	if bound, ok := s.typeMap[ownerName]; ok && bound != nil {
		receiver = bound
	} else if named, ok := s.g.result.NamedTypes[ownerName]; ok && named != nil {
		receiver = named
	} else {
		return nil, false, nil
	}
	var matched semantic.Type
	matchCount := 0
	for _, impl := range s.g.result.StaticImpls {
		if impl == nil || impl.Receiver == nil || !semantic.SameType(impl.Receiver, receiver) {
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
		return nil, false, nil
	}
	if matchCount > 1 {
		return nil, true, fmt.Errorf("associated type %s on %s is ambiguous during LLVM lowering", assocName, ownerName)
	}
	return matched, true, nil
}

func backendQualifiedTypePath(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil || n.Name == "" {
			return "", false
		}
		return n.Name, true
	case *ast.FieldExpr:
		if n == nil || n.Field == "" {
			return "", false
		}
		prefix, ok := backendQualifiedTypePath(n.Object)
		if !ok || prefix == "" {
			return "", false
		}
		return prefix + "." + n.Field, true
	case *ast.ParenExpr:
		if n == nil {
			return "", false
		}
		return backendQualifiedTypePath(n.Inner)
	default:
		return "", false
	}
}
