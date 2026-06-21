package semantic

import (
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) collectValueSymbols(decls []scopedDecl) {
	for _, scoped := range decls {
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			switch n := scoped.Decl.(type) {
			case *ast.ConstDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				var declType Type = invalidType
				if n.Type != nil {
					declType = a.resolveType(n.Type)
				} else {
					declType = a.analyzeExprInScope(n.Value, a.globalScope)
					if IsInvalidType(declType) {
						if value, ok := a.evalConstExpr(n.Value); ok {
							switch value.Kind {
							case ConstInt:
								declType = a.namedTypes["int"]
							case ConstFloat:
								declType = a.namedTypes["f64"]
							case ConstBool:
								declType = a.namedTypes["bool"]
							case ConstString:
								declType = &RefType{Elem: a.namedTypes["u8"], State: RefStateNonNull, Storage: RefStorageStatic, ExplicitStorage: true}
							}
						}
					}
					if IsInvalidType(declType) {
						declType = a.inferLiteralType(n.Value)
					}
				}
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolConst, Type: declType, Node: n, Mutable: false, Private: scoped.Private}, n.Pos())
			case *ast.TokenSetDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				var expected Type
				if n.ElemType != nil {
					elemType := a.resolveType(n.ElemType)
					if !IsInvalidType(elemType) {
						qualifyTokenSetBareMembers(n.Value, n.ElemType)
						expected = &ArrayType{Elem: elemType, Size: strconv.Itoa(len(n.Value.Elems)), HasConstSize: true, ConstSize: int64(len(n.Value.Elems))}
					}
				}
				declType := a.analyzeListLitExprWithExpected(n.Value, expected)
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolConst, Type: declType, Node: n, Mutable: false, Private: scoped.Private}, n.Pos())
			case *ast.CharsetDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolConst, Type: invalidType, Node: n, Mutable: false, Private: scoped.Private}, n.Pos())
			case *ast.ConstEnumDecl:
			case *ast.GlobalDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				declType := a.resolveType(n.Type)
				if a.containsAffineHandleValues(declType, map[string]bool{}) {
					a.errorf(n.Pos(), "global %q cannot store linear handle values of type %s", n.Name, declType)
				}
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolGlobal, Type: declType, Node: n, Mutable: n.Mutable, Private: scoped.Private}, n.Pos())
			case *ast.FuncDecl:
				// A `contract` decl (docs/97) is not a callable value: it is a named bundle of clauses
				// consumed by expandUsesContracts, with no body and no return type. Do not register a
				// function symbol for it (a `uses` clause references it by name, not by call).
				if n.IsContract {
					return
				}
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				// A `def` whose name is a builtin scalar type (f32, i32, u8, …) is unreachable
				// by call: a call site `name(x)` resolves to the type cast, not to this function.
				// Warn at the definition rather than letting the function silently vanish.
				if t, isType := a.namedTypes[n.Name]; isType {
					switch t.(type) {
					case *BuiltinType, *BitIntType:
						a.warnf(n.Pos(), "function %q shadows the builtin type name %q; a call `%s(...)` resolves to the type cast, not this function, so this function can never be called", n.Name, n.Name, n.Name)
					}
				}
				fnType := a.funcTypeFromDeclWithFrame(qualifiedName, n.TypeParams, n.GenericParams, n.RegionParams, n.PermissionParams, n.Permissions, n.Ensures, n.Changes, n.Fulfills, n.Params, n.ReturnType, false, false)
				fnType.Static = n.Static
				initLookupName, constructorSugar := a.constructorDeclInitHookName(scoped.Namespace, n, fnType)
				symbolName := qualifiedName
				switch n.Name {
				case "__cast__":
					symbolName = castHookSymbolName(qualifiedName, fnType, n.Pos())
				default:
					if constructorSugar {
						symbolName = initHookSymbolName(qualifiedName, fnType, n.Pos())
					}
				}
				sym := &Symbol{Name: symbolName, Kind: SymbolFunc, Type: fnType, Node: n, Mutable: false, Private: scoped.Private, Deprecated: funcDeprecationMessage(n)}
				a.functionTypes[symbolName] = fnType
				a.funcDeclSymbols[n] = sym
				if !a.defineExternImplementationGlobal(qualifiedName, sym, n.Pos()) {
					a.defineReceiverOverloadGlobal(qualifiedName, sym, n.Pos())
				}
				a.functionTypes[sym.Name] = fnType
				switch n.Name {
				case "__cast__":
					a.registerCastHook(scoped.Namespace, n, fnType, sym)
				default:
					if constructorSugar {
						a.registerInitHook(scoped.Namespace, n, initLookupName, fnType, sym)
					}
				}
			case *ast.InterfaceDecl:
			case *ast.ImplDecl:
				a.withGenericParams(n.GenericParams, nil, func() {
					receiver := a.resolveType(n.ForType)
					if receiver == nil || IsInvalidType(receiver) {
						return
					}
					if n.IsExtension() {
						for _, member := range n.Members {
							switch fnDecl := member.(type) {
							case *ast.FuncDecl:
								visibleName := joinQualifiedName(scoped.Namespace, fnDecl.Name)
								qualifiedName := ExtensionMethodSymbolName(visibleName, receiver, fnDecl.Name)
								fnType := a.funcTypeFromDeclWithFrame(qualifiedName, fnDecl.TypeParams, fnDecl.GenericParams, fnDecl.RegionParams, fnDecl.PermissionParams, fnDecl.Permissions, fnDecl.Ensures, fnDecl.Changes, fnDecl.Fulfills, fnDecl.Params, fnDecl.ReturnType, false, false)
								sym := &Symbol{Name: qualifiedName, Kind: SymbolFunc, Type: fnType, Node: fnDecl, Mutable: false}
								a.functionTypes[qualifiedName] = fnType
								a.funcDeclSymbols[fnDecl] = sym
								a.defineGlobal(sym, fnDecl.Pos())
								a.registerExtensionMethod(visibleName, receiver, sym, fnDecl, fnType)
							case *ast.ExternFuncDecl:
								visibleName := joinQualifiedName(scoped.Namespace, fnDecl.Name)
								qualifiedName := ExtensionMethodSymbolName(visibleName, receiver, fnDecl.Name)
								fnType := a.funcTypeFromExternDecl(qualifiedName, fnDecl.TypeParams, fnDecl.GenericParams, fnDecl.RegionParams, fnDecl.PermissionParams, fnDecl.Permissions, fnDecl.Ensures, fnDecl.Params, fnDecl.ReturnType, fnDecl.Variadic)
								a.applyExternFuncAnnotations(fnDecl, fnType)
								a.markRawExternFuncType(fnDecl, fnType)
								linkName, _ := externLinkNameFromAnnotations(fnDecl.Annotations)
								a.validateExternLinkNameSignature(qualifiedName, linkName, fnType, fnDecl.Pos())
								sym := &Symbol{Name: qualifiedName, Kind: SymbolExternFunc, Type: fnType, Node: fnDecl, LinkName: linkName, Mutable: false}
								a.functionTypes[qualifiedName] = fnType
								a.defineGlobal(sym, fnDecl.Pos())
								a.registerExtensionMethod(visibleName, receiver, sym, fnDecl, fnType)
							}
						}
						return
					}
					_, interfaceName, ok := a.lookupVisibleStaticInterface(n.InterfaceName)
					if !ok {
						return
					}
					for _, member := range n.Members {
						switch fnDecl := member.(type) {
						case *ast.FuncDecl:
							qualifiedName := StaticImplMethodSymbolName(interfaceName, receiver, fnDecl.Name)
							fnType := a.funcTypeFromDeclWithFrame(qualifiedName, fnDecl.TypeParams, fnDecl.GenericParams, fnDecl.RegionParams, fnDecl.PermissionParams, fnDecl.Permissions, fnDecl.Ensures, fnDecl.Changes, fnDecl.Fulfills, fnDecl.Params, fnDecl.ReturnType, false, false)
							sym := &Symbol{Name: qualifiedName, Kind: SymbolFunc, Type: fnType, Node: fnDecl, Mutable: false}
							a.functionTypes[qualifiedName] = fnType
							a.funcDeclSymbols[fnDecl] = sym
							a.defineGlobal(sym, fnDecl.Pos())
						case *ast.ExternFuncDecl:
							qualifiedName := StaticImplMethodSymbolName(interfaceName, receiver, fnDecl.Name)
							fnType := a.funcTypeFromExternDecl(qualifiedName, fnDecl.TypeParams, fnDecl.GenericParams, fnDecl.RegionParams, fnDecl.PermissionParams, fnDecl.Permissions, fnDecl.Ensures, fnDecl.Params, fnDecl.ReturnType, fnDecl.Variadic)
							a.applyExternFuncAnnotations(fnDecl, fnType)
							a.markRawExternFuncType(fnDecl, fnType)
							linkName, _ := externLinkNameFromAnnotations(fnDecl.Annotations)
							a.validateExternLinkNameSignature(qualifiedName, linkName, fnType, fnDecl.Pos())
							sym := &Symbol{Name: qualifiedName, Kind: SymbolExternFunc, Type: fnType, Node: fnDecl, LinkName: linkName, Mutable: false}
							a.functionTypes[qualifiedName] = fnType
							a.defineGlobal(sym, fnDecl.Pos())
						}
					}
				})
			case *ast.ExternFuncDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				fnType := a.funcTypeFromExternDecl(qualifiedName, n.TypeParams, n.GenericParams, n.RegionParams, n.PermissionParams, n.Permissions, n.Ensures, n.Params, n.ReturnType, n.Variadic)
				a.applyExternFuncAnnotations(n, fnType)
				a.checkExternContractDiscipline(n)
				a.markRawExternFuncType(n, fnType)
				if !fnType.ReturnProvenanceKnown {
					fnType.ReturnProvenanceKnown = true
				}
				if !fnType.ReturnBorrowedOwnerRefsKnown {
					fnType.ReturnBorrowedOwnerRefsKnown = true
				}
				a.functionTypes[qualifiedName] = fnType
				linkName, _ := externLinkNameFromAnnotations(n.Annotations)
				a.validateExternLinkNameSignature(qualifiedName, linkName, fnType, n.Pos())
				sym := &Symbol{Name: qualifiedName, Kind: SymbolExternFunc, Type: fnType, Node: n, LinkName: linkName, Mutable: false, Private: scoped.Private}
				if !a.defineExternImplementationGlobal(qualifiedName, sym, n.Pos()) {
					a.defineReceiverOverloadGlobal(qualifiedName, sym, n.Pos())
				}
				a.functionTypes[sym.Name] = fnType
			case *ast.ExternVarDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				declType := a.resolveType(n.Type)
				if a.containsAffineHandleValues(declType, map[string]bool{}) {
					a.errorf(n.Pos(), "extern var %q cannot store linear handle values of type %s", n.Name, declType)
				}
				a.applyExternVarAnnotations(n)
				linkName, _ := externLinkNameFromAnnotations(n.Annotations)
				a.validateExternLinkNameSignatureString(qualifiedName, linkName, externLinkNameVarSignatureString(declType), n.Pos())
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolExternVar, Type: declType, Node: n, LinkName: linkName, Mutable: true, Private: scoped.Private}, n.Pos())
			case *ast.EnumDecl:
			case *ast.ErrorDecl:
			case *ast.PermissionDecl:
			case *ast.TypeAliasDecl, *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
			}
		})
	}
}

func (a *Analyzer) validateExternLinkNameSignature(symbolName string, linkName string, fnType *FuncType, pos lexer.Pos) {
	if a == nil || fnType == nil {
		return
	}
	a.validateExternLinkNameSignatureString(symbolName, linkName, externLinkNameSignatureString(fnType), pos)
}

func (a *Analyzer) validateExternLinkNameSignatureString(symbolName string, linkName string, signature string, pos lexer.Pos) {
	if a == nil {
		return
	}
	linkName = strings.TrimSpace(linkName)
	if linkName == "" {
		return
	}
	if a.externLinkNames == nil {
		a.externLinkNames = map[string]externLinkNameSignature{}
	}
	if previous, ok := a.externLinkNames[linkName]; ok {
		if previous.Signature != signature {
			a.errorf(pos, "extern link_name %q for %q has signature %s, but previous declaration %q at %s has signature %s; use one shared declaration or matching wrapper to avoid native symbol splitting", linkName, symbolName, signature, previous.SymbolName, previous.Pos.String(), previous.Signature)
		}
		return
	}
	a.externLinkNames[linkName] = externLinkNameSignature{SymbolName: symbolName, Signature: signature, Pos: pos}
}

func externLinkNameSignatureString(fnType *FuncType) string {
	if fnType == nil {
		return "<invalid>"
	}
	parts := []string{fnType.String()}
	if fnType.CallConv != "" {
		parts = append(parts, "callconv="+fnType.CallConv)
	}
	if fnType.IntrinsicName != "" {
		parts = append(parts, "intrinsic="+fnType.IntrinsicName)
	}
	return strings.Join(parts, " ")
}

func externLinkNameVarSignatureString(typ Type) string {
	if typ == nil {
		return "var <invalid>"
	}
	return "var " + typ.String()
}

func (a *Analyzer) defineExternImplementationGlobal(visibleName string, sym *Symbol, pos lexer.Pos) bool {
	if a == nil || a.globalScope == nil || sym == nil {
		return false
	}
	existing, ok := a.globalScope.Symbols[visibleName]
	if !ok || existing == nil {
		return false
	}
	switch {
	case sym.Kind == SymbolExternFunc && existing.Kind == SymbolExternFunc:
		if !externDeclarationTypesCompatible(existing, sym) {
			if externFunctionsCanOverload(existing, sym) {
				return false
			}
			a.externDeclarationCompatible(existing, sym, pos)
			return true
		}
		return true
	case sym.Kind == SymbolFunc && existing.Kind == SymbolExternFunc:
		if externFunctionCanOverloadWithImplementation(existing, sym) {
			return false
		}
		if !externImplementationTypesCompatible(existing, sym) {
			a.externImplementationCompatible(existing, sym, pos)
			return true
		}
		a.mergeExternMetadataIntoImplementation(existing, sym)
		a.globalScope.Symbols[visibleName] = sym
		return true
	case sym.Kind == SymbolExternFunc && existing.Kind == SymbolFunc:
		if externFunctionCanOverloadWithImplementation(sym, existing) {
			return false
		}
		if !externImplementationTypesCompatible(sym, existing) {
			a.externImplementationCompatible(sym, existing, pos)
			return true
		}
		a.mergeExternMetadataIntoImplementation(sym, existing)
		a.functionTypes[existing.Name], _ = existing.Type.(*FuncType)
		return true
	default:
		return false
	}
}

func externFunctionsCanOverload(left, right *Symbol) bool {
	leftReceiver, leftOK := receiverOverloadType(left)
	rightReceiver, rightOK := receiverOverloadType(right)
	return leftOK && rightOK && !SameType(leftReceiver, rightReceiver)
}

func externDeclarationTypesCompatible(existingSym, newSym *Symbol) bool {
	existingFn, existingOK := existingSym.Type.(*FuncType)
	newFn, newOK := newSym.Type.(*FuncType)
	if !existingOK || !newOK || existingFn == nil || newFn == nil {
		return false
	}
	return externFuncABICompatible(existingFn, newFn)
}

func (a *Analyzer) externDeclarationCompatible(existingSym, newSym *Symbol, pos lexer.Pos) bool {
	existingFn, existingOK := existingSym.Type.(*FuncType)
	newFn, newOK := newSym.Type.(*FuncType)
	if !existingOK || !newOK || existingFn == nil || newFn == nil {
		a.errorf(pos, "extern function %q declaration does not match implementation", existingSym.Name)
		return false
	}
	if !externFuncABICompatible(existingFn, newFn) {
		a.errorf(pos, "extern function %q declaration does not match implementation", existingSym.Name)
		return false
	}
	return true
}

func externImplementationTypesCompatible(externSym, implSym *Symbol) bool {
	externFn, externOK := externSym.Type.(*FuncType)
	implFn, implOK := implSym.Type.(*FuncType)
	if !externOK || !implOK || externFn == nil || implFn == nil {
		return false
	}
	if externFn.IntrinsicName != "" {
		return false
	}
	// Compare ABI signature, not full type identity: an `extern` declaration
	// carries an implicit `can[Unsafe]` effect (calling foreign code is unsafe)
	// that a matching Elisa `def` reimplementation does not. That effect
	// difference must not block reconciling the forward declaration with its
	// implementation, so match on params/return/callconv/variadic only.
	return externFuncABICompatible(externFn, implFn)
}

func (a *Analyzer) externImplementationCompatible(externSym, implSym *Symbol, pos lexer.Pos) bool {
	externFn, externOK := externSym.Type.(*FuncType)
	implFn, implOK := implSym.Type.(*FuncType)
	if !externOK || !implOK || externFn == nil || implFn == nil {
		a.errorf(pos, "extern function %q declaration does not match implementation", externSym.Name)
		return false
	}
	if externFn.IntrinsicName != "" {
		a.errorf(pos, "extern intrinsic function %q cannot be implemented in Elisa", externSym.Name)
		return false
	}
	if !SameType(externFn, implFn) {
		a.errorf(pos, "extern function %q declaration does not match implementation", externSym.Name)
		return false
	}
	return true
}

func externFunctionCanOverloadWithImplementation(externSym, implSym *Symbol) bool {
	externFn, externOK := externSym.Type.(*FuncType)
	implFn, implOK := implSym.Type.(*FuncType)
	if !externOK || !implOK || externFn == nil || implFn == nil {
		return false
	}
	// ABI-compatible extern/impl are the same function (the extern's implicit
	// can[Unsafe] effect aside), not distinct overloads — so they must reconcile
	// rather than be treated as an overload set. Only genuinely different ABI
	// signatures are overload candidates.
	if externFuncABICompatible(externFn, implFn) {
		return false
	}
	_, externReceiver := receiverOverloadType(externSym)
	implReceiverType, implReceiver := receiverOverloadType(implSym)
	if !externReceiver || !implReceiver {
		return false
	}
	_, implRefReceiver := implReceiverType.(*RefType)
	return implRefReceiver
}

func externFuncABICompatible(left, right *FuncType) bool {
	if left == nil || right == nil {
		return left == right
	}
	if len(left.Params) != len(right.Params) || left.Variadic != right.Variadic {
		return false
	}
	if left.CallConv != "" && right.CallConv != "" && left.CallConv != right.CallConv {
		return false
	}
	if !externTypeABICompatible(left.Return, right.Return) {
		return false
	}
	for i := range left.Params {
		if !externTypeABICompatible(left.Params[i], right.Params[i]) {
			return false
		}
	}
	return true
}

func externTypeABICompatible(left, right Type) bool {
	if SameType(left, right) {
		return true
	}
	leftRef, leftRefOK := left.(*RefType)
	rightRef, rightRefOK := right.(*RefType)
	if leftRefOK && rightRefOK {
		if leftRef.State != rightRef.State {
			return false
		}
		if isVoidType(leftRef.Elem) {
			return true
		}
		if isVoidType(rightRef.Elem) {
			return true
		}
		return SameType(leftRef.Elem, rightRef.Elem)
	}
	return false
}

func (a *Analyzer) mergeExternMetadataIntoImplementation(externSym, implSym *Symbol) {
	if externSym == nil || implSym == nil {
		return
	}
	if implSym.LinkName == "" {
		implSym.LinkName = externSym.LinkName
	}
	externFn, externOK := externSym.Type.(*FuncType)
	implFn, implOK := implSym.Type.(*FuncType)
	if !externOK || !implOK || externFn == nil || implFn == nil {
		return
	}
	if implFn.CallConv == "" {
		implFn.CallConv = externFn.CallConv
	}
}

func qualifyTokenSetBareMembers(list *ast.ListLitExpr, elemType ast.TypeExpr) {
	if list == nil || elemType == nil {
		return
	}
	typeName, ok := tokenSetBareMemberQualifierName(elemType)
	if !ok || typeName == "" {
		return
	}
	for i, elem := range list.Elems {
		ident, ok := elem.(*ast.Ident)
		if !ok || ident == nil || ident.Name == "" {
			continue
		}
		list.Elems[i] = &ast.FieldExpr{
			Position: ident.Position,
			Object:   &ast.Ident{Position: elemType.Pos(), Name: typeName},
			Field:    ident.Name,
		}
	}
}

func tokenSetBareMemberQualifierName(elemType ast.TypeExpr) (string, bool) {
	switch n := elemType.(type) {
	case *ast.NamedType:
		if n == nil || n.Name == "" {
			return "", false
		}
		return n.Name, true
	default:
		return "", false
	}
}
