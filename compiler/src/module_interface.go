package main

import (
	"path/filepath"
	"strings"

	"llcontext/src/ast"
	"llcontext/src/unparse"
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
		return &ast.ExternFuncDecl{
			Position:         n.Position,
			Annotations:      append([]ast.Annotation(nil), n.Annotations...),
			Name:             n.Name,
			TypeParams:       append([]string(nil), n.TypeParams...),
			RefStorageParams: append([]string(nil), n.RefStorageParams...),
			RefStateParams:   append([]string(nil), n.RefStateParams...),
			PermissionParams: append([]string(nil), n.PermissionParams...),
			GenericParams:    append([]ast.GenericParam(nil), n.GenericParams...),
			RegionParams:     append([]string(nil), n.RegionParams...),
			Permissions:      append([]ast.PermissionRef(nil), n.Permissions...),
			Ensures:          append([]ast.EnsuresClause(nil), n.Ensures...),
			Params:           append([]ast.ParamDecl(nil), n.Params...),
			ReturnType:       n.ReturnType,
		}
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
	default:
		return decl
	}
}
