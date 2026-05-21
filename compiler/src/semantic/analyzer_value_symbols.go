package semantic

import (
	"strconv"

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
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolConst, Type: declType, Node: n, Mutable: false}, n.Pos())
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
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolConst, Type: declType, Node: n, Mutable: false}, n.Pos())
			case *ast.CharsetDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolConst, Type: invalidType, Node: n, Mutable: false}, n.Pos())
			case *ast.ConstEnumDecl:
			case *ast.GlobalDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				declType := a.resolveType(n.Type)
				if a.containsAffineHandleValues(declType, map[string]bool{}) {
					a.errorf(n.Pos(), "global %q cannot store affine handle values of type %s", n.Name, declType)
				}
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolGlobal, Type: declType, Node: n, Mutable: n.Mutable}, n.Pos())
			case *ast.FuncDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				fnType := a.funcTypeFromDecl(qualifiedName, n.TypeParams, n.RefStorageParams, n.RefStateParams, n.GenericParams, n.RegionParams, n.PermissionParams, n.EffectAliasPos, n.EffectAlias, n.Effects, n.Permissions, n.Ensures, n.Params, n.ParamPacks, n.ParamItemOrder, n.ImplicitParams, n.ImplicitBundles, n.ImplicitItemOrder, n.ReturnType, false)
				fnType.Static = n.Static
				initLookupName, constructorSugar := a.constructorDeclInitHookName(scoped.Namespace, n, fnType)
				explicitInit := funcHasInitAnnotation(n)
				symbolName := qualifiedName
				switch n.Name {
				case "__cast__":
					symbolName = castHookSymbolName(qualifiedName, fnType, n.Pos())
				case "__init__":
					symbolName = initHookSymbolName(qualifiedName, fnType, n.Pos())
				default:
					if constructorSugar {
						symbolName = initHookSymbolName(qualifiedName, fnType, n.Pos())
					}
				}
				sym := &Symbol{Name: symbolName, Kind: SymbolFunc, Type: fnType, Node: n, Mutable: false, UFCSOnly: funcHasAnnotation(n, "ufcs_only")}
				a.functionTypes[symbolName] = fnType
				a.funcDeclSymbols[n] = sym
				if !a.defineExternImplementationGlobal(qualifiedName, sym, n.Pos()) {
					a.defineReceiverOverloadGlobal(qualifiedName, sym, n.Pos())
				}
				a.functionTypes[sym.Name] = fnType
				switch n.Name {
				case "__cast__":
					a.registerCastHook(scoped.Namespace, n, fnType, sym)
				case "__init__":
					a.registerInitHook(scoped.Namespace, n, "__init__", fnType, sym)
				default:
					if constructorSugar {
						a.registerInitHook(scoped.Namespace, n, initLookupName, fnType, sym)
					} else if explicitInit {
						a.registerInitHook(scoped.Namespace, n, "__init__", fnType, sym)
					}
				}
			case *ast.AttributeDecl:
			case *ast.InterfaceDecl:
			case *ast.ImplDecl:
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
							fnType := a.funcTypeFromDecl(qualifiedName, fnDecl.TypeParams, fnDecl.RefStorageParams, fnDecl.RefStateParams, fnDecl.GenericParams, fnDecl.RegionParams, fnDecl.PermissionParams, fnDecl.EffectAliasPos, fnDecl.EffectAlias, fnDecl.Effects, fnDecl.Permissions, fnDecl.Ensures, fnDecl.Params, fnDecl.ParamPacks, fnDecl.ParamItemOrder, fnDecl.ImplicitParams, fnDecl.ImplicitBundles, fnDecl.ImplicitItemOrder, fnDecl.ReturnType, false)
							sym := &Symbol{Name: qualifiedName, Kind: SymbolFunc, Type: fnType, Node: fnDecl, Mutable: false}
							a.functionTypes[qualifiedName] = fnType
							a.funcDeclSymbols[fnDecl] = sym
							a.defineGlobal(sym, fnDecl.Pos())
							a.registerExtensionMethod(visibleName, receiver, sym, fnDecl, fnType)
						case *ast.ExternFuncDecl:
							visibleName := joinQualifiedName(scoped.Namespace, fnDecl.Name)
							qualifiedName := ExtensionMethodSymbolName(visibleName, receiver, fnDecl.Name)
							fnType := a.funcTypeFromDecl(qualifiedName, fnDecl.TypeParams, fnDecl.RefStorageParams, fnDecl.RefStateParams, fnDecl.GenericParams, fnDecl.RegionParams, fnDecl.PermissionParams, fnDecl.EffectAliasPos, fnDecl.EffectAlias, fnDecl.Effects, fnDecl.Permissions, fnDecl.Ensures, fnDecl.Params, fnDecl.ParamPacks, fnDecl.ParamItemOrder, fnDecl.ImplicitParams, fnDecl.ImplicitBundles, fnDecl.ImplicitItemOrder, fnDecl.ReturnType, fnDecl.Variadic)
							a.applyExternFuncAnnotations(fnDecl, fnType)
							a.markRawExternFuncType(fnDecl, fnType)
							linkName, _ := externLinkNameFromAnnotations(fnDecl.Annotations)
							sym := &Symbol{Name: qualifiedName, Kind: SymbolExternFunc, Type: fnType, Node: fnDecl, LinkName: linkName, Mutable: false, UFCSOnly: externFuncHasAnnotation(fnDecl, "ufcs_only")}
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
						fnType := a.funcTypeFromDecl(qualifiedName, fnDecl.TypeParams, fnDecl.RefStorageParams, fnDecl.RefStateParams, fnDecl.GenericParams, fnDecl.RegionParams, fnDecl.PermissionParams, fnDecl.EffectAliasPos, fnDecl.EffectAlias, fnDecl.Effects, fnDecl.Permissions, fnDecl.Ensures, fnDecl.Params, fnDecl.ParamPacks, fnDecl.ParamItemOrder, fnDecl.ImplicitParams, fnDecl.ImplicitBundles, fnDecl.ImplicitItemOrder, fnDecl.ReturnType, false)
						sym := &Symbol{Name: qualifiedName, Kind: SymbolFunc, Type: fnType, Node: fnDecl, Mutable: false}
						a.functionTypes[qualifiedName] = fnType
						a.funcDeclSymbols[fnDecl] = sym
						a.defineGlobal(sym, fnDecl.Pos())
					case *ast.ExternFuncDecl:
						qualifiedName := StaticImplMethodSymbolName(interfaceName, receiver, fnDecl.Name)
						fnType := a.funcTypeFromDecl(qualifiedName, fnDecl.TypeParams, fnDecl.RefStorageParams, fnDecl.RefStateParams, fnDecl.GenericParams, fnDecl.RegionParams, fnDecl.PermissionParams, fnDecl.EffectAliasPos, fnDecl.EffectAlias, fnDecl.Effects, fnDecl.Permissions, fnDecl.Ensures, fnDecl.Params, fnDecl.ParamPacks, fnDecl.ParamItemOrder, fnDecl.ImplicitParams, fnDecl.ImplicitBundles, fnDecl.ImplicitItemOrder, fnDecl.ReturnType, fnDecl.Variadic)
						a.applyExternFuncAnnotations(fnDecl, fnType)
						a.markRawExternFuncType(fnDecl, fnType)
						linkName, _ := externLinkNameFromAnnotations(fnDecl.Annotations)
						sym := &Symbol{Name: qualifiedName, Kind: SymbolExternFunc, Type: fnType, Node: fnDecl, LinkName: linkName, Mutable: false, UFCSOnly: externFuncHasAnnotation(fnDecl, "ufcs_only")}
						a.functionTypes[qualifiedName] = fnType
						a.defineGlobal(sym, fnDecl.Pos())
					}
				}
			case *ast.ExternFuncDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				fnType := a.funcTypeFromDecl(qualifiedName, n.TypeParams, n.RefStorageParams, n.RefStateParams, n.GenericParams, n.RegionParams, n.PermissionParams, n.EffectAliasPos, n.EffectAlias, n.Effects, n.Permissions, n.Ensures, n.Params, n.ParamPacks, n.ParamItemOrder, n.ImplicitParams, n.ImplicitBundles, n.ImplicitItemOrder, n.ReturnType, n.Variadic)
				a.applyExternFuncAnnotations(n, fnType)
				a.markRawExternFuncType(n, fnType)
				if !fnType.ReturnProvenanceKnown {
					fnType.ReturnProvenanceKnown = true
				}
				if !fnType.ReturnBorrowedOwnerRefsKnown {
					fnType.ReturnBorrowedOwnerRefsKnown = true
				}
				a.functionTypes[qualifiedName] = fnType
				linkName, _ := externLinkNameFromAnnotations(n.Annotations)
				sym := &Symbol{Name: qualifiedName, Kind: SymbolExternFunc, Type: fnType, Node: n, LinkName: linkName, Mutable: false, UFCSOnly: externFuncHasAnnotation(n, "ufcs_only")}
				if !a.defineExternImplementationGlobal(qualifiedName, sym, n.Pos()) {
					a.defineReceiverOverloadGlobal(qualifiedName, sym, n.Pos())
				}
				a.functionTypes[sym.Name] = fnType
			case *ast.ExternVarDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				declType := a.resolveType(n.Type)
				if a.containsAffineHandleValues(declType, map[string]bool{}) {
					a.errorf(n.Pos(), "extern var %q cannot store affine handle values of type %s", n.Name, declType)
				}
				a.applyExternVarAnnotations(n)
				linkName, _ := externLinkNameFromAnnotations(n.Annotations)
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolExternVar, Type: declType, Node: n, LinkName: linkName, Mutable: true}, n.Pos())
			case *ast.TreeDecl:
			case *ast.EnumDecl:
			case *ast.ErrorDecl:
			case *ast.EffectsDecl:
			case *ast.EffectDecl:
			case *ast.PermissionDecl:
			case *ast.TypeAliasDecl, *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
			}
		})
	}
}

func (a *Analyzer) defineExternImplementationGlobal(visibleName string, sym *Symbol, pos lexer.Pos) bool {
	if a == nil || a.globalScope == nil || sym == nil || sym.UFCSOnly {
		return false
	}
	existing, ok := a.globalScope.Symbols[visibleName]
	if !ok || existing == nil || existing.UFCSOnly {
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
	return SameType(externFn, implFn)
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
	if SameType(externFn, implFn) {
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
