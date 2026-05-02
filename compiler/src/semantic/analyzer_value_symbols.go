package semantic

import (
	"strconv"

	"llcontext/src/ast"
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
				initLookupName, constructorSugar := a.constructorDeclInitHookName(scoped.Namespace, n, fnType)
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
				sym := &Symbol{Name: symbolName, Kind: SymbolFunc, Type: fnType, Node: n, Mutable: false}
				a.functionTypes[symbolName] = fnType
				a.funcDeclSymbols[n] = sym
				a.defineGlobal(sym, n.Pos())
				switch n.Name {
				case "__cast__":
					a.registerCastHook(scoped.Namespace, n, fnType, sym)
				case "__init__":
					a.registerInitHook(scoped.Namespace, n, "__init__", fnType, sym)
				default:
					if constructorSugar {
						a.registerInitHook(scoped.Namespace, n, initLookupName, fnType, sym)
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
							sym := &Symbol{Name: qualifiedName, Kind: SymbolExternFunc, Type: fnType, Node: fnDecl, Mutable: false}
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
						sym := &Symbol{Name: qualifiedName, Kind: SymbolExternFunc, Type: fnType, Node: fnDecl, Mutable: false}
						a.functionTypes[qualifiedName] = fnType
						a.defineGlobal(sym, fnDecl.Pos())
					}
				}
			case *ast.ExternFuncDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				fnType := a.funcTypeFromDecl(qualifiedName, n.TypeParams, n.RefStorageParams, n.RefStateParams, n.GenericParams, n.RegionParams, n.PermissionParams, n.EffectAliasPos, n.EffectAlias, n.Effects, n.Permissions, n.Ensures, n.Params, n.ParamPacks, n.ParamItemOrder, n.ImplicitParams, n.ImplicitBundles, n.ImplicitItemOrder, n.ReturnType, n.Variadic)
				a.applyExternFuncAnnotations(n, fnType)
				if !fnType.ReturnProvenanceKnown {
					fnType.ReturnProvenanceKnown = true
				}
				if !fnType.ReturnBorrowedOwnerRefsKnown {
					fnType.ReturnBorrowedOwnerRefsKnown = true
				}
				a.functionTypes[qualifiedName] = fnType
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolExternFunc, Type: fnType, Node: n, Mutable: false}, n.Pos())
			case *ast.ExternVarDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				declType := a.resolveType(n.Type)
				if a.containsAffineHandleValues(declType, map[string]bool{}) {
					a.errorf(n.Pos(), "extern var %q cannot store affine handle values of type %s", n.Name, declType)
				}
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolExternVar, Type: declType, Node: n, Mutable: true}, n.Pos())
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
