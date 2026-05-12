package main

import (
	"path/filepath"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

func generateModuleInterface(file *ast.File) string {
	if file == nil {
		return ""
	}
	iface := &ast.File{
		Filename: interfaceFilenameFor(file.Filename),
		Decls:    interfaceDeclList(file.Decls),
	}
	return unparse.FormatFile(iface)
}

func interfaceFilenameFor(filename string) string {
	trimmed := strings.TrimSpace(filename)
	if trimmed == "" {
		return interfaceExtension
	}
	base := strings.TrimSuffix(trimmed, filepath.Ext(trimmed))
	return base + interfaceExtension
}

func interfaceDeclList(decls []ast.Decl) []ast.Decl {
	out := make([]ast.Decl, 0, len(decls))
	for _, decl := range decls {
		if iface := interfaceizeDecl(decl); iface != nil {
			out = append(out, iface)
		}
	}
	return out
}

func interfaceizeDecl(decl ast.Decl) ast.Decl {
	switch n := decl.(type) {
	case *ast.FuncDecl:
		if n.Static {
			return nil
		}
		return &ast.ExternFuncDecl{
			Position:         n.Position,
			Annotations:      append([]ast.Annotation(nil), n.Annotations...),
			Override:         n.Override,
			Name:             n.Name,
			TypeParams:       append([]string(nil), n.TypeParams...),
			RefStorageParams: append([]string(nil), n.RefStorageParams...),
			RefStateParams:   append([]string(nil), n.RefStateParams...),
			PermissionParams: append([]string(nil), n.PermissionParams...),
			GenericParams:    append([]ast.GenericParam(nil), n.GenericParams...),
			RegionParams:     append([]string(nil), n.RegionParams...),
			EffectAliasPos:   n.EffectAliasPos,
			EffectAlias:      n.EffectAlias,
			Effects:          append([]ast.SignatureEffectItem(nil), n.Effects...),
			Permissions:      append([]ast.PermissionRef(nil), n.Permissions...),
			Ensures:          append([]ast.EnsuresClause(nil), n.Ensures...),
			Params:           append([]ast.ParamDecl(nil), n.Params...),
			ReturnType:       n.ReturnType,
		}
	case *ast.InterfaceDecl:
		members := make([]ast.InterfaceMember, 0, len(n.Members))
		for _, member := range n.Members {
			switch m := member.(type) {
			case *ast.AssociatedTypeDecl:
				members = append(members, &ast.AssociatedTypeDecl{Position: m.Position, Name: m.Name})
			case *ast.ExternFuncDecl:
				members = append(members, &ast.ExternFuncDecl{Position: m.Position, Annotations: append([]ast.Annotation(nil), m.Annotations...), Name: m.Name, TypeParams: append([]string(nil), m.TypeParams...), RefStorageParams: append([]string(nil), m.RefStorageParams...), RefStateParams: append([]string(nil), m.RefStateParams...), PermissionParams: append([]string(nil), m.PermissionParams...), GenericParams: append([]ast.GenericParam(nil), m.GenericParams...), RegionParams: append([]string(nil), m.RegionParams...), EffectAliasPos: m.EffectAliasPos, EffectAlias: m.EffectAlias, Effects: append([]ast.SignatureEffectItem(nil), m.Effects...), Permissions: append([]ast.PermissionRef(nil), m.Permissions...), Ensures: append([]ast.EnsuresClause(nil), m.Ensures...), Params: append([]ast.ParamDecl(nil), m.Params...), ReturnType: m.ReturnType, Variadic: m.Variadic})
			}
		}
		return &ast.InterfaceDecl{Position: n.Position, Name: n.Name, Protocol: n.Protocol, Members: members}
	case *ast.ImplDecl:
		members := make([]ast.ImplMember, 0, len(n.Members))
		for _, member := range n.Members {
			switch m := member.(type) {
			case *ast.ImplAssociatedTypeDecl:
				members = append(members, &ast.ImplAssociatedTypeDecl{Position: m.Position, Name: m.Name, Type: m.Type})
			case *ast.FuncDecl:
				if m.Static {
					continue
				}
				members = append(members, &ast.ExternFuncDecl{Position: m.Position, Annotations: append([]ast.Annotation(nil), m.Annotations...), Override: m.Override, Name: m.Name, TypeParams: append([]string(nil), m.TypeParams...), RefStorageParams: append([]string(nil), m.RefStorageParams...), RefStateParams: append([]string(nil), m.RefStateParams...), PermissionParams: append([]string(nil), m.PermissionParams...), GenericParams: append([]ast.GenericParam(nil), m.GenericParams...), RegionParams: append([]string(nil), m.RegionParams...), EffectAliasPos: m.EffectAliasPos, EffectAlias: m.EffectAlias, Effects: append([]ast.SignatureEffectItem(nil), m.Effects...), Permissions: append([]ast.PermissionRef(nil), m.Permissions...), Ensures: append([]ast.EnsuresClause(nil), m.Ensures...), Params: append([]ast.ParamDecl(nil), m.Params...), ReturnType: m.ReturnType})
			case *ast.ExternFuncDecl:
				members = append(members, &ast.ExternFuncDecl{Position: m.Position, Annotations: append([]ast.Annotation(nil), m.Annotations...), Override: m.Override, Name: m.Name, TypeParams: append([]string(nil), m.TypeParams...), RefStorageParams: append([]string(nil), m.RefStorageParams...), RefStateParams: append([]string(nil), m.RefStateParams...), PermissionParams: append([]string(nil), m.PermissionParams...), GenericParams: append([]ast.GenericParam(nil), m.GenericParams...), RegionParams: append([]string(nil), m.RegionParams...), EffectAliasPos: m.EffectAliasPos, EffectAlias: m.EffectAlias, Effects: append([]ast.SignatureEffectItem(nil), m.Effects...), Permissions: append([]ast.PermissionRef(nil), m.Permissions...), Ensures: append([]ast.EnsuresClause(nil), m.Ensures...), Params: append([]ast.ParamDecl(nil), m.Params...), ReturnType: m.ReturnType, Variadic: m.Variadic})
			}
		}
		return &ast.ImplDecl{Position: n.Position, Annotations: append([]ast.Annotation(nil), n.Annotations...), InterfaceName: n.InterfaceName, ForType: n.ForType, Members: members}
	case *ast.GlobalDecl:
		return &ast.ExternVarDecl{Position: n.Position, Name: n.Name, Type: n.Type}
	case *ast.NamespaceDecl:
		return &ast.NamespaceDecl{Position: n.Position, Name: n.Name, Decls: interfaceDeclList(n.Decls)}
	case *ast.StaticIfDecl:
		elifs := make([]ast.StaticElifDecl, 0, len(n.Elifs))
		for _, elif := range n.Elifs {
			elifs = append(elifs, ast.StaticElifDecl{Position: elif.Position, Cond: elif.Cond, Body: interfaceDeclList(elif.Body)})
		}
		return &ast.StaticIfDecl{Position: n.Position, Cond: n.Cond, Then: interfaceDeclList(n.Then), Elifs: elifs, Else: interfaceDeclList(n.Else)}
	case *ast.StaticAssertDecl:
		return nil
	case *ast.StaticAssertBlockDecl:
		return nil
	default:
		return decl
	}
}
