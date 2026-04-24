package semantic

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (a *Analyzer) collectContextBundles(decls []scopedDecl) {
	if a == nil {
		return
	}
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.ContextDecl)
		if !ok || decl == nil {
			continue
		}
		qualifiedName := joinQualifiedName(scoped.Namespace, decl.Name)
		if _, exists := a.contextBundles[qualifiedName]; exists {
			a.errorf(decl.Pos(), "duplicate implicit bundle %q", qualifiedName)
			continue
		}
		bundle := &ContextBundle{Name: qualifiedName, Decl: decl, Fields: make([]ContextBundleField, 0, len(decl.Fields))}
		seen := map[string]bool{}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			for _, field := range decl.Fields {
				if seen[field.Name] {
					a.errorf(field.Position, "duplicate field %q in implicit bundle %q", field.Name, qualifiedName)
					continue
				}
				seen[field.Name] = true
				bundle.Fields = append(bundle.Fields, ContextBundleField{Name: field.Name, Type: a.resolveType(field.Type), Mutable: field.Mutable, Decl: field})
			}
		})
		a.contextBundles[qualifiedName] = bundle
	}
}

func orderedImplicitSigItems(bundles []string, params []ast.ParamDecl, order []ast.ImplicitSigItem) []ast.ImplicitSigItem {
	if len(order) != 0 {
		return append([]ast.ImplicitSigItem(nil), order...)
	}
	items := make([]ast.ImplicitSigItem, 0, len(bundles)+len(params))
	for _, bundle := range bundles {
		items = append(items, ast.ImplicitSigItem{Bundle: bundle, IsBundle: true})
	}
	for _, param := range params {
		items = append(items, ast.ImplicitSigItem{Position: param.Position, Param: param})
	}
	return items
}

func (a *Analyzer) expandImplicitParamDecls(explicitParams []ast.ParamDecl, implicitParams []ast.ParamDecl, implicitBundles []string, itemOrder []ast.ImplicitSigItem, ownerName string) ([]ast.ParamDecl, []string) {
	items := orderedImplicitSigItems(implicitBundles, implicitParams, itemOrder)
	if len(items) == 0 {
		return nil, nil
	}
	explicitNames := make(map[string]bool, len(explicitParams))
	for _, param := range explicitParams {
		explicitNames[param.Name] = true
	}
	seenImplicit := map[string]bool{}
	expanded := make([]ast.ParamDecl, 0, len(items))
	implicitNames := make([]string, 0, len(items))
	appendParam := func(param ast.ParamDecl, pos lexer.Pos) {
		if explicitNames[param.Name] || seenImplicit[param.Name] {
			a.errorf(pos, "duplicate implicit parameter %q after implicit bundle expansion on %q", param.Name, ownerName)
			return
		}
		seenImplicit[param.Name] = true
		expanded = append(expanded, param)
		implicitNames = append(implicitNames, param.Name)
	}
	for _, item := range items {
		if item.IsBundle {
			bundle, qualifiedName, ok := a.lookupVisibleContextBundle(item.Bundle)
			if !ok || bundle == nil {
				a.errorf(item.Position, "unknown implicit bundle %q", item.Bundle)
				continue
			}
			for _, field := range bundle.Fields {
				appendParam(ast.ParamDecl{Position: field.Decl.Position, Name: field.Name, Mutable: field.Mutable, Type: field.Decl.Type}, implicitSigItemPositionOr(item, field.Decl.Position))
			}
			_ = qualifiedName
			continue
		}
		appendParam(item.Param, item.Param.Position)
	}
	return expanded, implicitNames
}

func implicitSigItemPositionOr(item ast.ImplicitSigItem, fallback lexer.Pos) lexer.Pos {
	if item.Position.IsZero() {
		return fallback
	}
	return item.Position
}
