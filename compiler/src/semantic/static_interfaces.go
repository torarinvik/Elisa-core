package semantic

import (
	"fmt"
	"strings"
	"unicode"

	"elisacore/src/ast"
)

const staticInterfaceSelfName = "Self"

type AssociatedTypeProjection struct {
	Receiver      Type
	InterfaceName string
	Name          string
}

type StaticInterface struct {
	Name            string
	AssociatedTypes map[string]*ast.AssociatedTypeDecl
	Methods         map[string]*StaticInterfaceMethod
	Decl            *ast.InterfaceDecl
}

type StaticInterfaceMethod struct {
	Name      string
	Signature *FuncType
	Decl      *ast.ExternFuncDecl
}

type StaticImpl struct {
	InterfaceName   string
	Receiver        Type
	AssociatedTypes map[string]Type
	Methods         map[string]*Symbol
	Decl            *ast.ImplDecl
}

type InterfaceMethodRef struct {
	InterfaceName string
	MethodName    string
}

func (*AssociatedTypeProjection) isType() {}

func (t *AssociatedTypeProjection) String() string {
	if t == nil || t.Receiver == nil {
		return "<invalid-associated-type>"
	}
	return t.Receiver.String() + "." + t.Name
}

func TypeIdentityKey(t Type) string {
	if t == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T:%s", t, t.String())
}

func StaticImplLookupKey(interfaceName string, receiver Type) string {
	return interfaceName + "|" + TypeIdentityKey(receiver)
}

func LookupStaticImpl(impls map[string]*StaticImpl, interfaceName string, receiver Type) (*StaticImpl, bool) {
	if len(impls) == 0 || interfaceName == "" || receiver == nil {
		return nil, false
	}
	impl, ok := impls[StaticImplLookupKey(interfaceName, receiver)]
	return impl, ok && impl != nil
}

func ResolveAssociatedTypeProjection(proj *AssociatedTypeProjection, impls map[string]*StaticImpl) (Type, bool) {
	if proj == nil || proj.Receiver == nil || proj.InterfaceName == "" || proj.Name == "" {
		return nil, false
	}
	impl, ok := LookupStaticImpl(impls, proj.InterfaceName, proj.Receiver)
	if !ok || impl == nil {
		return nil, false
	}
	resolved, ok := impl.AssociatedTypes[proj.Name]
	return resolved, ok && resolved != nil
}

func sanitizeStaticInterfaceSymbolFragment(value string) string {
	if value == "" {
		return "anon"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "anon"
	}
	return out
}

func StaticImplMethodSymbolName(interfaceName string, receiver Type, methodName string) string {
	return "__impl__" + sanitizeStaticInterfaceSymbolFragment(interfaceName) + "__" + sanitizeStaticInterfaceSymbolFragment(TypeIdentityKey(receiver)) + "__" + sanitizeStaticInterfaceSymbolFragment(methodName)
}

func (a *Analyzer) lookupVisibleStaticInterface(name string) (*StaticInterface, string, bool) {
	for _, candidate := range a.visibleNameCandidates(name) {
		if iface, ok := a.staticInterfaces[candidate]; ok {
			return iface, candidate, true
		}
	}
	return nil, "", false
}

func (a *Analyzer) lookupTypeParamInterface(name string) (*StaticInterface, bool) {
	for i := len(a.typeParamInterfaceScopes) - 1; i >= 0; i-- {
		if iface, ok := a.typeParamInterfaceScopes[i][name]; ok {
			return iface, iface != nil
		}
	}
	return nil, false
}

func (a *Analyzer) lookupInterfaceAssocType(name string) (Type, bool) {
	for i := len(a.interfaceAssocTypeScopes) - 1; i >= 0; i-- {
		if t, ok := a.interfaceAssocTypeScopes[i][name]; ok {
			return t, ok
		}
	}
	return nil, false
}

func (a *Analyzer) withInterfaceAssocTypes(bindings map[string]Type, fn func()) {
	if len(bindings) == 0 {
		fn()
		return
	}
	a.interfaceAssocTypeScopes = append(a.interfaceAssocTypeScopes, bindings)
	fn()
	a.interfaceAssocTypeScopes = a.interfaceAssocTypeScopes[:len(a.interfaceAssocTypeScopes)-1]
}

func (a *Analyzer) withTypeParamInterfaces(bindings map[string]*StaticInterface, fn func()) {
	if len(bindings) == 0 {
		fn()
		return
	}
	a.typeParamInterfaceScopes = append(a.typeParamInterfaceScopes, bindings)
	fn()
	a.typeParamInterfaceScopes = a.typeParamInterfaceScopes[:len(a.typeParamInterfaceScopes)-1]
}

func (a *Analyzer) specializeInterfaceMethodSignature(signature *FuncType, receiver Type) *FuncType {
	if a == nil || signature == nil || receiver == nil {
		return signature
	}
	bindings := map[string]Type{staticInterfaceSelfName: receiver}
	specialized, _ := a.substituteType(signature, bindings, nil, nil, nil).(*FuncType)
	if specialized == nil {
		return signature
	}
	return specialized
}

func (a *Analyzer) staticImplsForReceiver(receiver Type) []*StaticImpl {
	if a == nil || receiver == nil || len(a.staticImpls) == 0 {
		return nil
	}
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
		return &AssociatedTypeProjection{Receiver: receiver, InterfaceName: iface.Name, Name: assocName}, true
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
	a.interfaceMethodRefs[expr] = &InterfaceMethodRef{InterfaceName: matchedImpl.InterfaceName, MethodName: expr.Field}
	return sig, true
}

func (a *Analyzer) collectStaticInterfaces(decls []scopedDecl) {
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.InterfaceDecl)
		if !ok {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			qualifiedName := joinQualifiedName(scoped.Namespace, decl.Name)
			if _, exists := a.staticInterfaces[qualifiedName]; exists {
				a.errorf(decl.Pos(), "duplicate interface %q", qualifiedName)
				return
			}
			iface := &StaticInterface{
				Name:            qualifiedName,
				AssociatedTypes: map[string]*ast.AssociatedTypeDecl{},
				Methods:         map[string]*StaticInterfaceMethod{},
				Decl:            decl,
			}
			a.staticInterfaces[qualifiedName] = iface
			assocBindings := map[string]Type{}
			for _, member := range decl.Members {
				assocDecl, ok := member.(*ast.AssociatedTypeDecl)
				if !ok {
					continue
				}
				if _, exists := iface.AssociatedTypes[assocDecl.Name]; exists {
					a.errorf(assocDecl.Pos(), "duplicate associated type %q in interface %q", assocDecl.Name, decl.Name)
					continue
				}
				iface.AssociatedTypes[assocDecl.Name] = assocDecl
				assocBindings[assocDecl.Name] = &AssociatedTypeProjection{Receiver: &TypeParamType{Name: staticInterfaceSelfName}, InterfaceName: qualifiedName, Name: assocDecl.Name}
			}
			a.withInterfaceAssocTypes(assocBindings, func() {
				for _, member := range decl.Members {
					methodDecl, ok := member.(*ast.ExternFuncDecl)
					if !ok {
						continue
					}
					if _, exists := iface.Methods[methodDecl.Name]; exists {
						a.errorf(methodDecl.Pos(), "duplicate interface method %q in interface %q", methodDecl.Name, decl.Name)
						continue
					}
					signature := a.funcTypeFromDecl(qualifiedName+"."+methodDecl.Name, methodDecl.TypeParams, methodDecl.RefStorageParams, methodDecl.RefStateParams, methodDecl.GenericParams, methodDecl.RegionParams, methodDecl.PermissionParams, methodDecl.EffectAliasPos, methodDecl.EffectAlias, methodDecl.Effects, methodDecl.Permissions, methodDecl.Ensures, methodDecl.Params, methodDecl.ParamPacks, methodDecl.ParamItemOrder, methodDecl.ImplicitParams, methodDecl.ImplicitBundles, methodDecl.ImplicitItemOrder, methodDecl.ReturnType, methodDecl.Variadic)
					iface.Methods[methodDecl.Name] = &StaticInterfaceMethod{Name: methodDecl.Name, Signature: signature, Decl: methodDecl}
				}
			})
		})
	}
}

func (a *Analyzer) collectStaticImpls(decls []scopedDecl) {
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.ImplDecl)
		if !ok {
			continue
		}
		if decl.IsExtension() {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			iface, interfaceName, ok := a.lookupVisibleStaticInterface(decl.InterfaceName)
			if !ok || iface == nil {
				a.errorf(decl.Pos(), "%s", UnknownInterfaceMessage(decl.InterfaceName))
				return
			}
			receiver := a.resolveType(decl.ForType)
			if receiver == nil || IsInvalidType(receiver) {
				return
			}
			key := StaticImplLookupKey(interfaceName, receiver)
			if _, exists := a.staticImpls[key]; exists {
				a.errorf(decl.Pos(), "duplicate impl of interface %q for %s", interfaceName, receiver.String())
				return
			}
			impl := &StaticImpl{
				InterfaceName:   interfaceName,
				Receiver:        receiver,
				AssociatedTypes: map[string]Type{},
				Methods:         map[string]*Symbol{},
				Decl:            decl,
			}
			seenAssoc := map[string]bool{}
			seenMethods := map[string]bool{}
			methodDecls := make([]ast.Node, 0, len(decl.Members))
			for _, member := range decl.Members {
				switch n := member.(type) {
				case *ast.ImplAssociatedTypeDecl:
					if seenAssoc[n.Name] {
						a.errorf(n.Pos(), "duplicate impl associated type %q in impl of %q", n.Name, interfaceName)
						continue
					}
					seenAssoc[n.Name] = true
					if _, ok := iface.AssociatedTypes[n.Name]; !ok {
						a.errorf(n.Pos(), "interface %q has no associated type %q", interfaceName, n.Name)
						continue
					}
					impl.AssociatedTypes[n.Name] = a.resolveType(n.Type)
				case *ast.FuncDecl:
					methodDecls = append(methodDecls, n)
					if seenMethods[n.Name] {
						a.errorf(n.Pos(), "duplicate impl method %q in impl of %q", n.Name, interfaceName)
						continue
					}
					seenMethods[n.Name] = true
				case *ast.ExternFuncDecl:
					methodDecls = append(methodDecls, n)
					if seenMethods[n.Name] {
						a.errorf(n.Pos(), "duplicate impl method %q in impl of %q", n.Name, interfaceName)
						continue
					}
					seenMethods[n.Name] = true
				}
			}
			a.staticImpls[key] = impl
			for _, member := range methodDecls {
				var name string
				var pos ast.Node
				switch n := member.(type) {
				case *ast.FuncDecl:
					name = n.Name
					pos = n
				case *ast.ExternFuncDecl:
					name = n.Name
					pos = n
				default:
					continue
				}
				methodInfo, ok := iface.Methods[name]
				if !ok || methodInfo == nil {
					a.errorf(pos.Pos(), "interface %q has no method %q", interfaceName, name)
					continue
				}
				symbolName := StaticImplMethodSymbolName(interfaceName, receiver, name)
				sym, ok := a.globalScope.Lookup(symbolName)
				if !ok || sym == nil {
					a.errorf(pos.Pos(), "internal error: missing impl method symbol %q", symbolName)
					continue
				}
				actualSig, ok := sym.Type.(*FuncType)
				if !ok || actualSig == nil {
					a.errorf(pos.Pos(), "impl method %q does not resolve to a function signature", name)
					continue
				}
				expectedSig := a.specializeInterfaceMethodSignature(methodInfo.Signature, receiver)
				if !SameType(expectedSig, actualSig) {
					a.errorf(pos.Pos(), "impl method %q for interface %q expects %s, got %s", name, interfaceName, expectedSig, actualSig)
					continue
				}
				impl.Methods[name] = sym
			}
			for name := range iface.AssociatedTypes {
				if _, ok := impl.AssociatedTypes[name]; !ok {
					a.errorf(decl.Pos(), "impl of interface %q for %s is missing associated type %q", interfaceName, receiver, name)
				}
			}
			for name := range iface.Methods {
				if _, ok := impl.Methods[name]; !ok {
					a.errorf(decl.Pos(), "impl of interface %q for %s is missing method %q", interfaceName, receiver, name)
				}
			}
		})
	}
}
