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
	// Bases are the resolved (qualified) names of the protocols this protocol inherits from.
	// Their members are folded into Methods/AssociatedTypes so a single impl of this protocol
	// must satisfy the union of own + inherited members.
	Bases []string
}

type StaticInterfaceMethod struct {
	Name      string
	Signature *FuncType
	Decl      *ast.ExternFuncDecl
	// Default, when non-nil, is the default-method body (`def m(...) -> T: <body>`) declared on
	// the protocol. A conforming impl that omits this method inherits the default; one that
	// provides it overrides. Decl above is the bodiless signature view used for typechecking.
	Default *ast.FuncDecl
}

type StaticImpl struct {
	InterfaceName   string
	Receiver        Type
	AssociatedTypes map[string]Type
	Methods         map[string]*Symbol
	Decl            *ast.ImplDecl
	// TypeParams names the impl's own type parameters (from `impl[T] ... for Box[T]`).
	// When non-empty the impl is parametric: its Receiver pattern carries these as free
	// TypeParamType leaves, and a concrete receiver is matched by unifying against them.
	TypeParams []string
}

// implTypeParamNames returns the impl's type-parameter names, for use as the free-variable
// set when unifying a concrete receiver against the impl's Receiver pattern.
func (impl *StaticImpl) implTypeParamNames() map[string]bool {
	if impl == nil || len(impl.TypeParams) == 0 {
		return nil
	}
	free := make(map[string]bool, len(impl.TypeParams))
	for _, name := range impl.TypeParams {
		free[name] = true
	}
	return free
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

// UnifyTypePattern structurally matches a concrete type against a pattern type whose
// free variables are named in freeVars (the type params of a parametric impl), producing
// the substitution that makes them equal. Returns ok=false if the head constructors don't
// line up (so the impl does not apply). A free var bound twice must agree (SameType).
func UnifyTypePattern(pattern, actual Type, freeVars map[string]bool) (map[string]Type, bool) {
	subst := map[string]Type{}
	if !unifyTypePattern(pattern, actual, freeVars, subst) {
		return nil, false
	}
	return subst, true
}

func unifyTypePattern(pattern, actual Type, freeVars map[string]bool, subst map[string]Type) bool {
	if pattern == nil || actual == nil {
		return pattern == nil && actual == nil
	}
	if tp, ok := pattern.(*TypeParamType); ok && freeVars[tp.Name] {
		if existing, seen := subst[tp.Name]; seen {
			return SameType(existing, actual)
		}
		subst[tp.Name] = actual
		return true
	}
	switch p := pattern.(type) {
	case *GenericInstanceType:
		a, ok := actual.(*GenericInstanceType)
		if !ok || p.Name != a.Name || len(p.Args) != len(a.Args) {
			return false
		}
		for i := range p.Args {
			if !unifyTypePattern(p.Args[i], a.Args[i], freeVars, subst) {
				return false
			}
		}
		return true
	case *DArrayType:
		a, ok := actual.(*DArrayType)
		return ok && unifyTypePattern(p.Elem, a.Elem, freeVars, subst)
	case *DictType:
		a, ok := actual.(*DictType)
		return ok && unifyTypePattern(p.Key, a.Key, freeVars, subst) && unifyTypePattern(p.Value, a.Value, freeVars, subst)
	case *RefType:
		a, ok := actual.(*RefType)
		return ok && unifyTypePattern(p.Elem, a.Elem, freeVars, subst)
	default:
		return SameType(pattern, actual)
	}
}

// LookupStaticImplUnifying resolves an impl for a concrete receiver, trying an exact
// (concrete-impl) hit first, then matching any parametric impl by unifying its Receiver
// pattern against the concrete receiver. On a parametric match it returns the substitution
// {T: i64, …}; for an exact match the substitution is empty.
func LookupStaticImplUnifying(impls map[string]*StaticImpl, interfaceName string, receiver Type) (*StaticImpl, map[string]Type, bool) {
	if impl, ok := LookupStaticImpl(impls, interfaceName, receiver); ok {
		return impl, nil, true
	}
	for _, impl := range impls {
		if impl == nil || impl.InterfaceName != interfaceName || len(impl.TypeParams) == 0 {
			continue
		}
		if subst, ok := UnifyTypePattern(impl.Receiver, receiver, impl.implTypeParamNames()); ok {
			return impl, subst, true
		}
	}
	return nil, nil, false
}

// substituteTypeParamsByName replaces free TypeParamType leaves named in subst, copying
// composite types field-by-field so unrelated fields (shape, region, storage) are kept.
// It is a small self-contained substituter for the free static-interface resolvers, which
// run without an *Analyzer.
func substituteTypeParamsByName(t Type, subst map[string]Type) Type {
	if t == nil || len(subst) == 0 {
		return t
	}
	switch tt := t.(type) {
	case *TypeParamType:
		if r, ok := subst[tt.Name]; ok && r != nil {
			return r
		}
		return t
	case *GenericInstanceType:
		clone := *tt
		clone.Args = make([]Type, len(tt.Args))
		for i, a := range tt.Args {
			clone.Args[i] = substituteTypeParamsByName(a, subst)
		}
		return &clone
	case *DArrayType:
		clone := *tt
		clone.Elem = substituteTypeParamsByName(tt.Elem, subst)
		return &clone
	case *DictType:
		clone := *tt
		clone.Key = substituteTypeParamsByName(tt.Key, subst)
		clone.Value = substituteTypeParamsByName(tt.Value, subst)
		return &clone
	case *RefType:
		clone := *tt
		clone.Elem = substituteTypeParamsByName(tt.Elem, subst)
		return &clone
	default:
		return t
	}
}

func ResolveAssociatedTypeProjection(proj *AssociatedTypeProjection, impls map[string]*StaticImpl) (Type, bool) {
	if proj == nil || proj.Receiver == nil || proj.InterfaceName == "" || proj.Name == "" {
		return nil, false
	}
	impl, subst, ok := LookupStaticImplUnifying(impls, proj.InterfaceName, proj.Receiver)
	if !ok || impl == nil {
		return nil, false
	}
	resolved, ok := impl.AssociatedTypes[proj.Name]
	if !ok || resolved == nil {
		return nil, false
	}
	return substituteTypeParamsByName(resolved, subst), true
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

// typePathNameForReceiver reverse-resolves a concrete receiver type back to a visible
// type-path name (e.g. the struct's qualified name), so a `value.method()` UFCS call can be
// rewritten to the qualified `TypeName.method(value, …)` form that resolveInterfaceMethodExprType
// (analysis) and resolveStaticInterfaceMethod (backend, via NamedTypes) both understand.
func (a *Analyzer) typePathNameForReceiver(receiver Type) (string, bool) {
	if a == nil || receiver == nil {
		return "", false
	}
	for name, t := range a.namedTypes {
		if t != nil && SameType(t, receiver) {
			return name, true
		}
	}
	return "", false
}

// staticImplMethodForReceiver finds the protocol-impl (or synthesized default-method) impl that
// provides a method named methodName for a concrete receiver type. It returns the matched impl so
// a UFCS call can be dispatched. Ambiguity across multiple conforming protocols is reported.
func (a *Analyzer) staticImplMethodForReceiver(receiver Type, methodName string, pos ast.Node) (*StaticImpl, bool) {
	if a == nil || receiver == nil || methodName == "" {
		return nil, false
	}
	var matched *StaticImpl
	for _, impl := range a.staticImplsForReceiver(receiver) {
		if impl == nil {
			continue
		}
		if sym, ok := impl.Methods[methodName]; !ok || sym == nil {
			continue
		}
		if matched != nil {
			if pos != nil {
				a.errorf(pos.Pos(), "method %q on %s is ambiguous across multiple protocol impls", methodName, receiver.String())
			}
			return nil, false
		}
		matched = impl
	}
	if matched == nil {
		return nil, false
	}
	return matched, true
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
				Bases:           append([]string(nil), decl.Bases...),
			}
			a.staticInterfaces[qualifiedName] = iface
			assocBindings := map[string]Type{}
			// `Self` in a method signature is a stand-in for the implementing type; it
			// resolves to a placeholder type param here and is substituted to the concrete
			// receiver by specializeInterfaceMethodSignature at the use/impl site.
			assocBindings[staticInterfaceSelfName] = &TypeParamType{Name: staticInterfaceSelfName}
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
					switch methodDecl := member.(type) {
					case *ast.ExternFuncDecl:
						if _, exists := iface.Methods[methodDecl.Name]; exists {
							a.errorf(methodDecl.Pos(), "duplicate interface method %q in interface %q", methodDecl.Name, decl.Name)
							continue
						}
						signature := a.funcTypeFromDecl(qualifiedName+"."+methodDecl.Name, methodDecl.TypeParams, methodDecl.GenericParams, methodDecl.RegionParams, methodDecl.PermissionParams, methodDecl.Permissions, methodDecl.Ensures, methodDecl.Params, methodDecl.ReturnType, methodDecl.Variadic)
						iface.Methods[methodDecl.Name] = &StaticInterfaceMethod{Name: methodDecl.Name, Signature: signature, Decl: methodDecl}
					case *ast.FuncDecl:
						// Default method: a protocol method carrying a body. Its signature is
						// typechecked exactly like a bodiless one; the body is recorded so a
						// conforming impl that omits the method inherits it (synthesized later).
						if _, exists := iface.Methods[methodDecl.Name]; exists {
							a.errorf(methodDecl.Pos(), "duplicate interface method %q in interface %q", methodDecl.Name, decl.Name)
							continue
						}
						signature := a.funcTypeFromDecl(qualifiedName+"."+methodDecl.Name, methodDecl.TypeParams, methodDecl.GenericParams, methodDecl.RegionParams, methodDecl.PermissionParams, methodDecl.Permissions, methodDecl.Ensures, methodDecl.Params, methodDecl.ReturnType, false)
						iface.Methods[methodDecl.Name] = &StaticInterfaceMethod{Name: methodDecl.Name, Signature: signature, Decl: nil, Default: methodDecl}
					}
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
			a.withGenericParams(decl.GenericParams, nil, func() {
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
					TypeParams:      implTypeParamNamesFromDecl(decl.GenericParams),
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
					// Behavioral-subtyping (Liskov–Wing) variance check on value contracts:
					// the impl method's `requires` must be entailed by the protocol's (contravariant)
					// and the impl's `ensure` must imply the protocol's (covariant). docs P1.
					a.checkProtocolImplContractVariance(methodInfo, member, interfaceName, receiver, name)
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
		})
	}
}

// implTypeParamNamesFromDecl extracts the plain type-parameter names declared on an impl
// (`impl[T, U] ...`), used to mark the impl parametric and as the unification free-var set.
func implTypeParamNamesFromDecl(params []ast.GenericParam) []string {
	var names []string
	for _, param := range params {
		if param.Kind == ast.GenericParamType {
			names = append(names, param.Name)
		}
	}
	return names
}
