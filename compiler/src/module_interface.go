package main

import (
	"path/filepath"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
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
		if n.Static || hasInterfaceInternalAnnotation(n.Annotations) {
			return nil
		}
		return &ast.ExternFuncDecl{
			Position:         n.Position,
			Annotations:      append([]ast.Annotation(nil), n.Annotations...),
			Override:         n.Override,
			Name:             n.Name,
			TypeParams:       append([]string(nil), n.TypeParams...),
			PermissionParams: append([]string(nil), n.PermissionParams...),
			GenericParams:    append([]ast.GenericParam(nil), n.GenericParams...),
			RegionParams:     append([]string(nil), n.RegionParams...),
			Permissions:      append([]ast.PermissionRef(nil), n.Permissions...),
			Ensures:          append([]ast.EnsuresClause(nil), n.Ensures...),
			Params:           append([]ast.ParamDecl(nil), n.Params...),
			ReturnType:       n.ReturnType,
		}
	case *ast.ExternFuncDecl:
		if hasInterfaceInternalAnnotation(n.Annotations) {
			return nil
		}
		return n
	case *ast.InterfaceDecl:
		members := make([]ast.InterfaceMember, 0, len(n.Members))
		for _, member := range n.Members {
			switch m := member.(type) {
			case *ast.AssociatedTypeDecl:
				members = append(members, &ast.AssociatedTypeDecl{Position: m.Position, Name: m.Name})
			case *ast.ExternFuncDecl:
				members = append(members, &ast.ExternFuncDecl{Position: m.Position, Annotations: append([]ast.Annotation(nil), m.Annotations...), Name: m.Name, TypeParams: append([]string(nil), m.TypeParams...), PermissionParams: append([]string(nil), m.PermissionParams...), GenericParams: append([]ast.GenericParam(nil), m.GenericParams...), RegionParams: append([]string(nil), m.RegionParams...), Permissions: append([]ast.PermissionRef(nil), m.Permissions...), Ensures: append([]ast.EnsuresClause(nil), m.Ensures...), Params: append([]ast.ParamDecl(nil), m.Params...), ReturnType: m.ReturnType, Variadic: m.Variadic})
			case *ast.FuncDecl:
				// Default method: a downstream module needs the body to inherit the default, so
				// the FuncDecl (signature + body) is carried into the public interface verbatim.
				members = append(members, &ast.FuncDecl{Position: m.Position, Annotations: append([]ast.Annotation(nil), m.Annotations...), Name: m.Name, TypeParams: append([]string(nil), m.TypeParams...), PermissionParams: append([]string(nil), m.PermissionParams...), GenericParams: append([]ast.GenericParam(nil), m.GenericParams...), RegionParams: append([]string(nil), m.RegionParams...), Permissions: append([]ast.PermissionRef(nil), m.Permissions...), Ensures: append([]ast.EnsuresClause(nil), m.Ensures...), Params: append([]ast.ParamDecl(nil), m.Params...), ReturnType: m.ReturnType, Body: m.Body})
			}
		}
		return &ast.InterfaceDecl{Position: n.Position, Name: n.Name, IsEffect: n.IsEffect, GenericParams: append([]ast.GenericParam(nil), n.GenericParams...), Bases: append([]string(nil), n.Bases...), Members: members}
	case *ast.ImplDecl:
		if n.IsHandler {
			// Handlers are local static realizations. An interface file exports the
			// abstract effect contract, not an implementation choice or its captures.
			return nil
		}
		members := make([]ast.ImplMember, 0, len(n.Members))
		for _, member := range n.Members {
			switch m := member.(type) {
			case *ast.ImplAssociatedTypeDecl:
				members = append(members, &ast.ImplAssociatedTypeDecl{Position: m.Position, Name: m.Name, Type: m.Type})
			case *ast.FuncDecl:
				if m.Static || hasInterfaceInternalAnnotation(m.Annotations) {
					continue
				}
				members = append(members, &ast.ExternFuncDecl{Position: m.Position, Annotations: append([]ast.Annotation(nil), m.Annotations...), Override: m.Override, Name: m.Name, TypeParams: append([]string(nil), m.TypeParams...), PermissionParams: append([]string(nil), m.PermissionParams...), GenericParams: append([]ast.GenericParam(nil), m.GenericParams...), RegionParams: append([]string(nil), m.RegionParams...), Permissions: append([]ast.PermissionRef(nil), m.Permissions...), Ensures: append([]ast.EnsuresClause(nil), m.Ensures...), Params: append([]ast.ParamDecl(nil), m.Params...), ReturnType: m.ReturnType})
			case *ast.ExternFuncDecl:
				if hasInterfaceInternalAnnotation(m.Annotations) {
					continue
				}
				members = append(members, &ast.ExternFuncDecl{Position: m.Position, Annotations: append([]ast.Annotation(nil), m.Annotations...), Override: m.Override, Name: m.Name, TypeParams: append([]string(nil), m.TypeParams...), PermissionParams: append([]string(nil), m.PermissionParams...), GenericParams: append([]ast.GenericParam(nil), m.GenericParams...), RegionParams: append([]string(nil), m.RegionParams...), Permissions: append([]ast.PermissionRef(nil), m.Permissions...), Ensures: append([]ast.EnsuresClause(nil), m.Ensures...), Params: append([]ast.ParamDecl(nil), m.Params...), ReturnType: m.ReturnType, Variadic: m.Variadic})
			}
		}
		return &ast.ImplDecl{Position: n.Position, Annotations: append([]ast.Annotation(nil), n.Annotations...), InterfaceName: n.InterfaceName, GenericParams: append([]ast.GenericParam(nil), n.GenericParams...), ForType: n.ForType, Members: members}
	case *ast.GlobalDecl:
		return &ast.ExternVarDecl{Position: n.Position, Name: n.Name, Type: n.Type}
	case *ast.NamespaceDecl:
		decls := interfaceDeclList(n.Decls)
		if len(decls) == 0 {
			return nil
		}
		// Extend is CARRIED: dropping it turned every `extend Foo:` block in the source into a
		// second `module Foo:` in the generated interface, which the analyzer then rejects as a
		// redeclaration -- an .elisai the compiler could not read back.
		return &ast.NamespaceDecl{Position: n.Position, Name: n.Name, Decls: decls, Module: n.Module, Const: n.Const, Extend: n.Extend}
	case *ast.StaticIfDecl:
		thenBody := interfaceDeclList(n.Then)
		elifs := make([]ast.StaticElifDecl, 0, len(n.Elifs))
		for _, elif := range n.Elifs {
			elifs = append(elifs, ast.StaticElifDecl{Position: elif.Position, Cond: elif.Cond, Body: interfaceDeclList(elif.Body)})
		}
		elseBody := interfaceDeclList(n.Else)
		if len(thenBody) == 0 && len(elseBody) == 0 {
			empty := true
			for _, elif := range elifs {
				if len(elif.Body) != 0 {
					empty = false
					break
				}
			}
			if empty {
				return nil
			}
		}
		// A conditionally-private branch still affects which later branch is active.
		// Keep its position with a harmless assertion so the generated interface stays
		// syntactically valid without exporting a placeholder symbol.
		if len(thenBody) == 0 {
			thenBody = interfaceEmptyBranch(n.Position)
		}
		for i := range elifs {
			if len(elifs[i].Body) == 0 {
				elifs[i].Body = interfaceEmptyBranch(elifs[i].Position)
			}
		}
		return &ast.StaticIfDecl{Position: n.Position, Cond: n.Cond, Then: thenBody, Elifs: elifs, Else: elseBody}
	case *ast.StaticAssertDecl:
		return nil
	case *ast.StaticAssertBlockDecl:
		return nil
	case *ast.StaticGenerateDecl:
		return nil
	default:
		return decl
	}
}

func interfaceEmptyBranch(pos lexer.Pos) []ast.Decl {
	return []ast.Decl{&ast.StaticAssertDecl{Position: pos, Cond: &ast.BoolLit{Position: pos, Value: true}}}
}

func hasInterfaceInternalAnnotation(annotations []ast.Annotation) bool {
	for _, annotation := range annotations {
		if annotation.Name == "internal" {
			return true
		}
	}
	return false
}
