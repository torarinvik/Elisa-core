package semantic

import (
	"elisacore/src/ast"
	"maps"
	"slices"
)

func (a *Analyzer) collectStaticInterfaces(decls []scopedDecl) {
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.InterfaceDecl)
		if !ok {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			defer a.beginProofContribution(decl, proofPhaseProtocolHeader, nil, "")()
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
			a.withGenericParams(decl.GenericParams, nil, func() {
				a.withInterfaceAssocTypes(assocBindings, func() {
					for _, member := range decl.Members {
						switch methodDecl := member.(type) {
						case *ast.ExternFuncDecl:
							if _, exists := iface.Methods[methodDecl.Name]; exists {
								a.errorf(methodDecl.Pos(), "duplicate interface method %q in interface %q", methodDecl.Name, decl.Name)
								continue
							}
							signature := a.funcTypeFromDecl(qualifiedName+"."+methodDecl.Name, methodDecl.TypeParams, methodDecl.GenericParams, methodDecl.RegionParams, methodDecl.PermissionParams, methodDecl.Permissions, methodDecl.Ensures, methodDecl.Requires, methodDecl.EnsureValues, methodDecl.Params, methodDecl.ReturnType, methodDecl.Variadic)
							iface.Methods[methodDecl.Name] = &StaticInterfaceMethod{Name: methodDecl.Name, Signature: signature, Decl: methodDecl}
						case *ast.FuncDecl:
							if methodDecl.IsLaw {
								// Protocol law: a universally-quantified predicate over Self. Recorded for
								// per-impl discharge; not a callable method, so it does not enter Methods.
								iface.Laws = append(iface.Laws, methodDecl)
								continue
							}
							// Default method: a protocol method carrying a body. Its signature is
							// typechecked exactly like a bodiless one; the body is recorded so a
							// conforming impl that omits the method inherits it (synthesized later).
							if _, exists := iface.Methods[methodDecl.Name]; exists {
								a.errorf(methodDecl.Pos(), "duplicate interface method %q in interface %q", methodDecl.Name, decl.Name)
								continue
							}
							signature := a.funcTypeFromDecl(qualifiedName+"."+methodDecl.Name, methodDecl.TypeParams, methodDecl.GenericParams, methodDecl.RegionParams, methodDecl.PermissionParams, methodDecl.Permissions, methodDecl.Ensures, methodDecl.Requires, methodDecl.EnsureValues, methodDecl.Params, methodDecl.ReturnType, false)
							iface.Methods[methodDecl.Name] = &StaticInterfaceMethod{Name: methodDecl.Name, Signature: signature, Decl: nil, Default: methodDecl}
						}
					}
				})
			})
		})
	}
}

func (a *Analyzer) collectStaticImpls(decls []scopedDecl) {
	a.incrementalOperators.beginCollection()
	defer a.incrementalOperators.endCollection()
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.ImplDecl)
		if !ok {
			continue
		}
		if decl.IsHandler {
			continue
		}
		if decl.IsExtension() {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			defer a.beginProofContribution(decl, proofPhaseProtocolImpl, nil, "")()
			iface, interfaceName, ok := a.lookupVisibleStaticInterface(decl.InterfaceName)
			if !ok || iface == nil {
				a.errorf(decl.Pos(), "%s", UnknownInterfaceMessage(decl.InterfaceName))
				return
			}
			a.addProofInterfaceOrigins(iface)
			for _, method := range implMethodBodies(decl) {
				a.addProofReportOrigin(method)
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
				typeParamBounds, invalidTypeParamBounds := a.staticImplTypeParamInterfaceBounds(decl.GenericParams)
				impl := &StaticImpl{
					InterfaceName:             interfaceName,
					Receiver:                  receiver,
					TypeParams:                implTypeParamNamesFromDecl(decl.GenericParams),
					TypeParamInterfaceBounds:  typeParamBounds,
					InvalidTypeParamBounds:    invalidTypeParamBounds,
					AssociatedTypes:           map[string]Type{},
					AssociatedTypeRefinements: map[string][]ast.RefinementPredExpr{},
					Methods:                   map[string]*Symbol{},
					Decl:                      decl,
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
						if preds := refinementPredsOfTypeExpr(n.Type); len(preds) > 0 {
							impl.AssociatedTypeRefinements[n.Name] = preds
						}
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
				// Inherit any protocol associated-type DEFAULT the impl omitted, BEFORE the method
				// signatures are checked: an associated-type projection (`Self.Field`) in a method
				// signature resolves through impl.AssociatedTypes, so the default binding (and its
				// refinement) must already be present when the signatures are compared.
				for name, assocDecl := range iface.AssociatedTypes {
					if _, ok := impl.AssociatedTypes[name]; ok {
						continue
					}
					if assocDecl != nil && assocDecl.DefaultType != nil {
						impl.AssociatedTypes[name] = a.resolveType(assocDecl.DefaultType)
						if preds := refinementPredsOfTypeExpr(assocDecl.DefaultType); len(preds) > 0 {
							impl.AssociatedTypeRefinements[name] = preds
						}
					}
				}
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
					// Structural signature match ignores the effect/error channels (they are checked
					// with SUBSET variance below, not invariant equality) so a conforming impl that
					// uses a strict subset of the protocol's capabilities / error modes still matches.
					// It also ignores PARAMETER NAMES: `def __sub__(self, other)` in the protocol is
					// satisfied by `def __sub__(self, o)` in the impl — names are not part of a
					// function's type, so the impl is free to rename its parameters.
					if !sameSignatureModuloParamNamesOrBorrowRegion(stripEffectChannels(expectedSig), stripEffectChannels(actualSig)) {
						a.errorf(pos.Pos(), "impl method %q for interface %q expects %s, got %s", name, interfaceName, expectedSig, actualSig)
						continue
					}
					// P4: effect / error / frame (`changes`) subset variance (impl ⊆ protocol). Kept
					// in a dedicated function so the merge with P1's requires/ensure variance is trivial.
					var protoDecl ast.Node
					if methodInfo.Default != nil {
						protoDecl = methodInfo.Default
					} else if methodInfo.Decl != nil {
						protoDecl = methodInfo.Decl
					}
					a.checkImplEffectErrorFrameVariance(interfaceName, name, expectedSig, actualSig, protoDecl, member, pos.Pos())
					impl.Methods[name] = sym
					// Behavioral-subtyping (Liskov–Wing) variance check on value contracts:
					// the impl method's `requires` must be entailed by the protocol's (contravariant)
					// and the impl's `ensure` must imply the protocol's (covariant). docs P1.
					a.checkProtocolImplContractVariance(methodInfo, member, interfaceName, receiver, name)
				}
				// Sorted: iface.AssociatedTypes and iface.Methods are maps, and a map walk
				// reported several missing members in a different order on every run.
				for _, name := range slices.Sorted(maps.Keys(iface.AssociatedTypes)) {
					// Defaults were already inherited above; a still-missing associated type has no
					// default and the impl must bind it explicitly.
					if _, ok := impl.AssociatedTypes[name]; !ok {
						a.errorf(decl.Pos(), "impl of interface %q for %s is missing associated type %q", interfaceName, receiver, name)
					}
				}
				for _, name := range slices.Sorted(maps.Keys(iface.Methods)) {
					if _, ok := impl.Methods[name]; !ok {
						a.errorf(decl.Pos(), "impl of interface %q for %s is missing method %q", interfaceName, receiver, name)
					}
				}
				// Protocol laws (docs/85 P3): discharge each of the protocol's algebraic laws against this
				// concrete impl. A proven law costs nothing at runtime; an unproven one is auto-lowered to a
				// per-impl @property fuzz harness so it stays honest in debug/CI.
				a.dischargeProtocolLawsForImpl(iface, impl, decl)
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
