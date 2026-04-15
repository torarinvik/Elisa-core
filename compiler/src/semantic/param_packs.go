package semantic

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (a *Analyzer) collectParamPacks(decls []scopedDecl) {
	if a == nil {
		return
	}
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.ParamsDecl)
		if !ok || decl == nil {
			continue
		}
		qualifiedName := joinQualifiedName(scoped.Namespace, decl.Name)
		if _, exists := a.paramPacks[qualifiedName]; exists {
			a.errorf(decl.Pos(), "duplicate parameter pack %q", qualifiedName)
			continue
		}
		pack := &ParamPack{
			Name:      qualifiedName,
			Decl:      decl,
			Namespace: scoped.Namespace,
			Usings:    append([]string(nil), scoped.Usings...),
			Fields:    make([]ParamPackField, 0, len(decl.Params)),
		}
		seen := map[string]bool{}
		paramTypes := make([]Type, 0, len(decl.Params))
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			for _, param := range decl.Params {
				resolvedType := a.resolveType(param.Type)
				paramTypes = append(paramTypes, resolvedType)
				if seen[param.Name] {
					a.errorf(param.Position, "duplicate parameter %q in parameter pack %q", param.Name, qualifiedName)
					continue
				}
				seen[param.Name] = true
				pack.Fields = append(pack.Fields, ParamPackField{Name: param.Name, Type: resolvedType, Mutable: param.Mutable, Decl: param})
			}
			a.validateFuncParamDefaults(qualifiedName, decl.Params, paramTypes)
		})
		a.paramPacks[qualifiedName] = pack
	}
}

func orderedExplicitSigItems(packs []ast.ParamPackUse, params []ast.ParamDecl, order []ast.ParamSigItem) []ast.ParamSigItem {
	if len(order) != 0 {
		return append([]ast.ParamSigItem(nil), order...)
	}
	items := make([]ast.ParamSigItem, 0, len(packs)+len(params))
	for _, pack := range packs {
		items = append(items, ast.ParamSigItem{Position: pack.Position, Pack: pack, IsPack: true})
	}
	for _, param := range params {
		items = append(items, ast.ParamSigItem{Position: param.Position, Param: param})
	}
	return items
}

type explicitParamSpec struct {
	Decl             ast.ParamDecl
	ResolvedType     Type
	HasResolvedType  bool
	DefaultNamespace string
	DefaultUsings    []string
}

func (a *Analyzer) expandExplicitParamSpecs(params []ast.ParamDecl, packs []ast.ParamPackUse, order []ast.ParamSigItem, ownerName string) []explicitParamSpec {
	items := orderedExplicitSigItems(packs, params, order)
	if len(items) == 0 {
		return nil
	}
	out := make([]explicitParamSpec, 0, len(items))
	seen := map[string]bool{}
	appendSpec := func(spec explicitParamSpec, pos lexer.Pos) {
		if spec.Decl.Name == "" {
			return
		}
		if seen[spec.Decl.Name] {
			a.errorf(pos, "duplicate explicit parameter %q after parameter-pack expansion on %q", spec.Decl.Name, ownerName)
			return
		}
		seen[spec.Decl.Name] = true
		out = append(out, spec)
	}
	for _, item := range items {
		if item.IsPack {
			pack, _, ok := a.lookupVisibleParamPack(item.Pack.Name)
			if !ok || pack == nil {
				a.errorf(item.Position, "unknown parameter pack %q", item.Pack.Name)
				continue
			}
			for _, field := range pack.Fields {
				appendSpec(explicitParamSpec{
					Decl:             field.Decl,
					ResolvedType:     field.Type,
					HasResolvedType:  true,
					DefaultNamespace: pack.Namespace,
					DefaultUsings:    append([]string(nil), pack.Usings...),
				}, explicitSigItemPositionOr(item, field.Decl.Position))
			}
			continue
		}
		appendSpec(explicitParamSpec{Decl: item.Param}, item.Param.Position)
	}
	return out
}

func explicitSigItemPositionOr(item ast.ParamSigItem, fallback lexer.Pos) lexer.Pos {
	if item.Position.IsZero() {
		return fallback
	}
	return item.Position
}

func explicitParamDeclsFromSpecs(specs []explicitParamSpec) []ast.ParamDecl {
	if len(specs) == 0 {
		return nil
	}
	decls := make([]ast.ParamDecl, 0, len(specs))
	for _, spec := range specs {
		decls = append(decls, spec.Decl)
	}
	return decls
}

func (a *Analyzer) expandedExplicitParamDecls(params []ast.ParamDecl, packs []ast.ParamPackUse, order []ast.ParamSigItem, ownerName string) []ast.ParamDecl {
	return explicitParamDeclsFromSpecs(a.expandExplicitParamSpecs(params, packs, order, ownerName))
}

func (a *Analyzer) expandedFuncDeclParams(fn *ast.FuncDecl) []ast.ParamDecl {
	if fn == nil {
		return nil
	}
	return a.expandedExplicitParamDecls(fn.Params, fn.ParamPacks, fn.ParamItemOrder, fn.Name)
}

func (a *Analyzer) expandedExternFuncDeclParams(fn *ast.ExternFuncDecl) []ast.ParamDecl {
	if fn == nil {
		return nil
	}
	return a.expandedExplicitParamDecls(fn.Params, fn.ParamPacks, fn.ParamItemOrder, fn.Name)
}
