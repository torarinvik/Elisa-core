package semantic

import "llcontext/src/ast"

func (a *Analyzer) collectExportTypeAliases(decls []ast.Decl) {
	for _, decl := range decls {
		exportDecl, ok := decl.(*ast.ExportTypeDecl)
		if !ok {
			continue
		}
		if _, exists := a.namedTypes[exportDecl.Alias]; exists {
			a.errorf(exportDecl.Pos(), "duplicate type %q", exportDecl.Alias)
			continue
		}
		resolved := a.resolveType(exportDecl.ExportedType)
		if IsInvalidType(resolved) {
			continue
		}
		if containsTypeParam(resolved) {
			a.errorf(exportDecl.Pos(), "export type %q must name a concrete C-ABI-compatible struct or concrete instantiation", exportDecl.Alias)
			continue
		}
		if !exportedNamedTypeAllowed(resolved) {
			a.errorf(exportDecl.Pos(), "export type %q must name a concrete C-ABI-compatible struct or concrete instantiation", exportDecl.Alias)
			continue
		}
		if !isCABICompatibleType(resolved) {
			a.errorf(exportDecl.Pos(), "export type %q is not C-ABI-compatible", exportDecl.Alias)
			continue
		}
		a.namedTypes[exportDecl.Alias] = resolved
		a.exportedTypes = append(a.exportedTypes, &ExportedType{PublicName: exportDecl.Alias, Type: resolved, Decl: exportDecl})
	}
}

func (a *Analyzer) analyzeExports(decls []ast.Decl) {
	seenPublicNames := map[string]bool{}
	for _, exported := range a.exportedTypes {
		if exported != nil {
			seenPublicNames[exported.PublicName] = true
		}
	}
	for _, decl := range decls {
		switch exportDecl := decl.(type) {
		case *ast.ExportFuncDecl:
			a.analyzeExportFunc(exportDecl, seenPublicNames)
		case *ast.ExportGlobalDecl:
			a.analyzeExportGlobal(exportDecl, seenPublicNames)
		}
	}
}

func (a *Analyzer) analyzeExportFunc(decl *ast.ExportFuncDecl, seenPublicNames map[string]bool) {
	if seenPublicNames[decl.Name] {
		a.errorf(decl.Pos(), "duplicate export name %q", decl.Name)
		return
	}
	if existing, ok := a.globalScope.Lookup(decl.Name); ok {
		a.errorf(decl.Pos(), "exported symbol %q collides with existing %s", decl.Name, existing.Kind)
		return
	}

	signature := a.funcTypeFromDecl(decl.Name, nil, nil, nil, nil, nil, nil, nil, decl.Params, decl.ReturnType, false)
	if !isCABICompatibleFuncType(signature) {
		a.errorf(decl.Pos(), "export func %q is not C-ABI-compatible", decl.Name)
		return
	}

	targetSym, ok := a.globalScope.Lookup(decl.TargetName)
	if !ok {
		a.errorf(decl.Pos(), "export target %q is undefined", decl.TargetName)
		return
	}
	targetBase, ok := targetSym.Type.(*FuncType)
	if !ok {
		a.errorf(decl.Pos(), "export target %q is not a function", decl.TargetName)
		return
	}

	bindings := map[string]Type{}
	targetSpecialized := targetBase
	targetGenericDecl, _ := targetSym.Node.(*ast.FuncDecl)
	params := genericParamsForFuncType(targetBase)
	if len(params) > 0 {
		if targetGenericDecl == nil {
			a.errorf(decl.Pos(), "export target %q does not support generic specialization", decl.TargetName)
			return
		}
		if len(decl.TargetTypeArgs) != len(params) {
			a.errorf(decl.Pos(), "export target %q expects %d %s, got %d", decl.TargetName, len(params), genericArgLabel(params), len(decl.TargetTypeArgs))
			return
		}
		for i, arg := range decl.TargetTypeArgs {
			resolved := a.resolveGenericArgForParam(arg, params[i])
			if IsInvalidType(resolved) {
				return
			}
			if containsTypeParam(resolved) {
				a.errorf(arg.Pos(), "export target specialization arguments must be concrete")
				return
			}
			bindings[params[i].Name] = resolved
		}
		targetSpecialized = specializeExportFuncType(a, targetBase, bindings)
	} else if len(decl.TargetTypeArgs) > 0 {
		a.errorf(decl.Pos(), "export target %q is not generic", decl.TargetName)
		return
	}

	if !sameExportSignature(signature, targetSpecialized) {
		a.errorf(decl.Pos(), "export func %q signature %s does not match target %q as %s", decl.Name, signature.String(), decl.TargetName, targetSpecialized.String())
		return
	}

	seenPublicNames[decl.Name] = true
	a.exportedFuncs = append(a.exportedFuncs, &ExportedFunc{
		PublicName:        decl.Name,
		Signature:         signature,
		TargetName:        decl.TargetName,
		TargetBase:        targetBase,
		TargetSpecialized: targetSpecialized,
		TargetGenericDecl: targetGenericDecl,
		TargetBindings:    copyTypeBindings(bindings),
		Decl:              decl,
	})
}

func (a *Analyzer) analyzeExportGlobal(decl *ast.ExportGlobalDecl, seenPublicNames map[string]bool) {
	publicName := decl.Alias
	if publicName == "" {
		publicName = decl.TargetName
	}
	if seenPublicNames[publicName] {
		a.errorf(decl.Pos(), "duplicate export name %q", publicName)
		return
	}
	if existing, ok := a.globalScope.Lookup(publicName); ok {
		if publicName != decl.TargetName || existing.Kind != SymbolGlobal {
			a.errorf(decl.Pos(), "exported symbol %q collides with existing %s", publicName, existing.Kind)
			return
		}
	}

	targetSym, ok := a.globalScope.Lookup(decl.TargetName)
	if !ok {
		a.errorf(decl.Pos(), "export target %q is undefined", decl.TargetName)
		return
	}
	if targetSym.Kind != SymbolGlobal {
		a.errorf(decl.Pos(), "export global %q must target a global, got %s", publicName, targetSym.Kind)
		return
	}
	if !isCABICompatibleType(targetSym.Type) {
		a.errorf(decl.Pos(), "export global %q is not C-ABI-compatible", publicName)
		return
	}

	seenPublicNames[publicName] = true
	a.exportedGlobals = append(a.exportedGlobals, &ExportedGlobal{
		PublicName: publicName,
		Type:       targetSym.Type,
		TargetName: decl.TargetName,
		TargetKind: targetSym.Kind,
		Mutable:    targetSym.Mutable,
		Decl:       decl,
	})
}

func specializeExportFuncType(a *Analyzer, base *FuncType, bindings map[string]Type) *FuncType {
	if base == nil {
		return nil
	}
	specialized, _ := a.substituteType(base, bindings, nil, nil, nil).(*FuncType)
	if specialized == nil {
		return base
	}
	return &FuncType{
		Name:                   specialized.Name,
		TypeParams:             nil,
		RefStorageParams:       nil,
		RefStateParams:         nil,
		RegionParams:           append([]string(nil), specialized.RegionParams...),
		GenericParams:          nil,
		Permissions:            append([]string(nil), specialized.Permissions...),
		ShapeParams:            append([]string(nil), specialized.ShapeParams...),
		FreshReturnShapeParams: append([]string(nil), specialized.FreshReturnShapeParams...),
		InlineMode:             specialized.InlineMode,
		HasInlineMode:          specialized.HasInlineMode,
		TemperatureMode:        specialized.TemperatureMode,
		HasTemperatureMode:     specialized.HasTemperatureMode,
		Params:                 append([]Type(nil), specialized.Params...),
		Return:                 specialized.Return,
		Variadic:               specialized.Variadic,
	}
}

func sameExportSignature(left *FuncType, right *FuncType) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.Variadic != right.Variadic || len(left.Params) != len(right.Params) {
		return false
	}
	for i := range left.Params {
		if !SameType(left.Params[i], right.Params[i]) {
			return false
		}
	}
	return SameType(left.Return, right.Return)
}

func exportedNamedTypeAllowed(t Type) bool {
	switch tt := t.(type) {
	case *StructType:
		return tt.ReprC && len(genericParamsForStructType(tt)) == 0
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		return ok && base.ReprC && len(genericParamsForStructType(base)) == len(tt.Args)
	default:
		return false
	}
}

func isCABICompatibleFuncType(fn *FuncType) bool {
	if fn == nil || fn.Variadic || len(genericParamsForFuncType(fn)) > 0 || len(fn.RegionParams) > 0 || len(fn.Permissions) > 0 {
		return false
	}
	for _, param := range fn.Params {
		if !isCABIFunctionBoundaryType(param) {
			return false
		}
	}
	return isCABIFunctionBoundaryType(fn.Return)
}

func isCABIFunctionBoundaryType(t Type) bool {
	if _, ok := t.(*ArrayType); ok {
		return false
	}
	return isCABICompatibleType(t)
}

func isCABICompatibleType(t Type) bool {
	switch tt := t.(type) {
	case nil:
		return true
	case *BuiltinType:
		switch tt.Name {
		case "void", "char", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "f32", "f64", "int", "isize", "usize", "uintptr":
			return true
		default:
			return false
		}
	case *RefType:
		return true
	case *ArrayType:
		return tt.HasConstSize && isCABICompatibleType(tt.Elem)
	case *StructType:
		if !tt.ReprC || len(genericParamsForStructType(tt)) > 0 || tt.Decl == nil {
			return false
		}
		for _, fieldDecl := range tt.Decl.Fields {
			field, ok := tt.Fields[fieldDecl.Name]
			if !ok || !isCABICompatibleType(field.Type) {
				return false
			}
		}
		return true
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		params := genericParamsForStructType(base)
		if !ok || !base.ReprC || base.Decl == nil || len(params) != len(tt.Args) {
			return false
		}
		bindings := genericBindingsForParams(params, tt.Args)
		for _, fieldDecl := range base.Decl.Fields {
			field, ok := base.Fields[fieldDecl.Name]
			if !ok || !isCABICompatibleType(substituteExportType(field.Type, bindings)) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func substituteExportType(t Type, bindings map[string]Type) Type {
	if len(bindings) == 0 {
		return t
	}
	switch tt := t.(type) {
	case *TypeParamType:
		if resolved, ok := bindings[tt.Name]; ok {
			return resolved
		}
		return tt
	case *RefStorageParamType:
		if resolved, ok := bindings[tt.Name]; ok {
			return resolved
		}
		return tt
	case *RefStateParamType:
		if resolved, ok := bindings[tt.Name]; ok {
			return resolved
		}
		return tt
	case *RefType:
		state := tt.State
		stateParam := tt.StateParam
		if stateParam != "" {
			if resolved, ok := bindings[stateParam]; ok {
				switch resolved := resolved.(type) {
				case *RefStateValueType:
					state = resolved.State
					stateParam = ""
				case *RefStateParamType:
					stateParam = resolved.Name
				}
			}
		}
		storage := tt.Storage
		storageParam := tt.StorageParam
		if storageParam != "" {
			if resolved, ok := bindings[storageParam]; ok {
				switch resolved := resolved.(type) {
				case *RefStorageValueType:
					storage = resolved.Storage
					storageParam = ""
				case *RefStorageParamType:
					storageParam = resolved.Name
				}
			}
		}
		return &RefType{Elem: substituteExportType(tt.Elem, bindings), State: state, StateParam: stateParam, Storage: storage, StorageParam: storageParam, Region: tt.Region, ExplicitStorage: tt.ExplicitStorage}
	case *ArrayType:
		return &ArrayType{Elem: substituteExportType(tt.Elem, bindings), Size: tt.Size, HasConstSize: tt.HasConstSize, ConstSize: tt.ConstSize, SurfaceName: tt.SurfaceName}
	case *GenericInstanceType:
		args := make([]Type, 0, len(tt.Args))
		for _, arg := range tt.Args {
			args = append(args, substituteExportType(arg, bindings))
		}
		return &GenericInstanceType{Name: tt.Name, Base: tt.Base, Args: args}
	default:
		return t
	}
}

func copyTypeBindings(bindings map[string]Type) map[string]Type {
	if len(bindings) == 0 {
		return nil
	}
	out := make(map[string]Type, len(bindings))
	for key, value := range bindings {
		out[key] = value
	}
	return out
}
