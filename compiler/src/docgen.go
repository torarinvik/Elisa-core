package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

func generateReferenceDoc(sourcePath string, file *ast.File) string {
	var b strings.Builder
	base := filepath.Base(sourcePath)
	fmt.Fprintf(&b, "# Reference: %s\n\n", base)
	fmt.Fprintf(&b, "Generated from `%s`.\n\n", filepath.ToSlash(sourcePath))
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- top-level declarations: %d\n", len(file.Decls))
	fmt.Fprintf(&b, "- formatter source: structural AST rendering (comments are not preserved yet)\n\n")
	for i, decl := range file.Decls {
		if i > 0 {
			b.WriteByte('\n')
		}
		writeDeclReference(&b, decl, 2, "")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func writeDeclReference(b *strings.Builder, decl ast.Decl, headingLevel int, namespace string) {
	if decl == nil {
		return
	}
	headingPrefix := strings.Repeat("#", headingLevel)
	switch n := decl.(type) {
	case *ast.NamespaceDecl:
		qualified := qualifyDocName(namespace, n.Name)
		fmt.Fprintf(b, "%s Namespace `%s`\n\n", headingPrefix, qualified)
		fmt.Fprintf(b, "- declaration: `%s`\n\n", declarationHeadline(unparse.FormatDecl(n)))
		for i, nested := range n.Decls {
			if i > 0 {
				b.WriteByte('\n')
			}
			writeDeclReference(b, nested, minHeading(headingLevel+1), qualified)
		}
	case *ast.PermissionDecl:
		writeSimpleReferenceSection(b, headingPrefix, "Permission", qualifyDocName(namespace, n.Name), declarationHeadline(unparse.FormatDecl(n)), func() {
			fmt.Fprintf(b, "- members:\n")
			for _, member := range n.Members {
				fmt.Fprintf(b, "  - `%s`\n", member)
			}
		})
	case *ast.ErrorDecl:
		writeSimpleReferenceSection(b, headingPrefix, "Error set", qualifyDocName(namespace, n.Name), declarationHeadline(unparse.FormatDecl(n)), func() {
			fmt.Fprintf(b, "- tags:\n")
			for _, tag := range n.Tags {
				fmt.Fprintf(b, "  - `%s`\n", tag.Name)
			}
		})
	case *ast.UsingDecl:
		writeSimpleReferenceSection(b, headingPrefix, "Using", qualifyDocName(namespace, n.Name), declarationHeadline(unparse.FormatDecl(n)), nil)
	case *ast.ConstDecl:
		writeSimpleReferenceSection(b, headingPrefix, "Constant", qualifyDocName(namespace, n.Name), declarationHeadline(unparse.FormatDecl(n)), nil)
	case *ast.ConstEnumDecl:
		writeSimpleReferenceSection(b, headingPrefix, "Const enum", qualifyDocName(namespace, n.Name), declarationHeadline(unparse.FormatDecl(n)), func() {
			fmt.Fprintf(b, "- storage: `%s`\n", unparse.FormatType(n.Storage))
			fmt.Fprintf(b, "- members:\n")
			for _, member := range n.Members {
				line := member.Name
				if member.Value != nil {
					line += " = " + unparse.FormatExpr(member.Value)
				}
				fmt.Fprintf(b, "  - `%s`\n", line)
			}
		})
	case *ast.GlobalDecl:
		writeSimpleReferenceSection(b, headingPrefix, "Global", qualifyDocName(namespace, n.Name), declarationHeadline(unparse.FormatDecl(n)), nil)
	case *ast.StructDecl:
		writeSimpleReferenceSection(b, headingPrefix, "Struct", qualifyDocName(namespace, n.Name), declarationHeadline(unparse.FormatDecl(n)), func() {
			writeAnnotationsList(b, n.Annotations)
			fmt.Fprintf(b, "- fields:\n")
			for _, field := range n.Fields {
				fmt.Fprintf(b, "  - `%s`\n", formatDocField(field))
			}
		})
	case *ast.EnumDecl:
		kind := "Enum"
		if n.Packed {
			kind = "Packed enum"
		}
		writeSimpleReferenceSection(b, headingPrefix, kind, qualifyDocName(namespace, n.Name), declarationHeadline(unparse.FormatDecl(n)), func() {
			writeAnnotationsList(b, n.Annotations)
			if len(n.Common) > 0 {
				fmt.Fprintf(b, "- common fields:\n")
				for _, field := range n.Common {
					fmt.Fprintf(b, "  - `%s`\n", formatDocField(field))
				}
			}
			fmt.Fprintf(b, "- variants:\n")
			for _, variant := range n.Variants {
				fmt.Fprintf(b, "  - `%s`\n", formatDocEnumVariant(variant))
			}
		})
	case *ast.FuncDecl:
		writeSimpleReferenceSection(b, headingPrefix, "Function", qualifyDocName(namespace, n.Name), declarationHeadline(unparse.FormatDecl(n)), func() {
			writeAnnotationsList(b, n.Annotations)
			fmt.Fprintf(b, "- body statements: %d\n", len(n.Body))
		})
	case *ast.ExternFuncDecl:
		writeSimpleReferenceSection(b, headingPrefix, "Extern function", qualifyDocName(namespace, n.Name), declarationHeadline(unparse.FormatDecl(n)), func() {
			writeAnnotationsList(b, n.Annotations)
		})
	case *ast.ExternVarDecl:
		writeSimpleReferenceSection(b, headingPrefix, "Extern variable", qualifyDocName(namespace, n.Name), declarationHeadline(unparse.FormatDecl(n)), nil)
	case *ast.ExternTypeDecl:
		writeSimpleReferenceSection(b, headingPrefix, "Extern type", qualifyDocName(namespace, n.Name), declarationHeadline(unparse.FormatDecl(n)), nil)
	case *ast.ExportTypeDecl:
		writeSimpleReferenceSection(b, headingPrefix, "Exported type", qualifyDocName(namespace, n.Alias), declarationHeadline(unparse.FormatDecl(n)), nil)
	case *ast.ExportFuncDecl:
		writeSimpleReferenceSection(b, headingPrefix, "Exported function", qualifyDocName(namespace, n.Name), declarationHeadline(unparse.FormatDecl(n)), nil)
	case *ast.ExportGlobalDecl:
		name := n.Alias
		if name == "" {
			name = n.TargetName
		}
		writeSimpleReferenceSection(b, headingPrefix, "Exported global", qualifyDocName(namespace, name), declarationHeadline(unparse.FormatDecl(n)), nil)
	case *ast.StaticIfDecl:
		fmt.Fprintf(b, "%s Static conditional\n\n", headingPrefix)
		fmt.Fprintf(b, "- condition: `%s`\n", unparse.FormatExpr(n.Cond))
		fmt.Fprintf(b, "- then declarations: %d\n", len(n.Then))
		fmt.Fprintf(b, "- elif declarations: %d\n", len(n.Elifs))
		fmt.Fprintf(b, "- else declarations: %d\n", len(n.Else))
	default:
		fmt.Fprintf(b, "%s Declaration\n\n", headingPrefix)
		fmt.Fprintf(b, "- surface: `%s`\n", declarationHeadline(unparse.FormatDecl(decl)))
	}
}

func writeSimpleReferenceSection(b *strings.Builder, headingPrefix string, kind string, name string, declaration string, extra func()) {
	fmt.Fprintf(b, "%s %s `%s`\n\n", headingPrefix, kind, name)
	fmt.Fprintf(b, "- declaration: `%s`\n", declaration)
	if extra != nil {
		extra()
	}
	b.WriteByte('\n')
}

func writeAnnotationsList(b *strings.Builder, annotations []ast.Annotation) {
	if len(annotations) == 0 {
		return
	}
	fmt.Fprintf(b, "- annotations:\n")
	for _, annotation := range annotations {
		fmt.Fprintf(b, "  - `%s`\n", formatDocAnnotation(annotation))
	}
}

func formatDocField(field ast.FieldDecl) string {
	line := field.Name + ": "
	if field.Mutable {
		line += "mutable "
	}
	if field.IsTail {
		line += "tail "
	}
	line += unparse.FormatType(field.Type)
	return line
}

func formatDocEnumVariant(variant ast.EnumVariantDecl) string {
	line := variant.Name
	if len(variant.Payload) == 0 {
		return line
	}
	parts := make([]string, 0, len(variant.Payload))
	for _, payload := range variant.Payload {
		if payload.Name != "" {
			parts = append(parts, payload.Name+": "+unparse.FormatType(payload.Type))
		} else {
			parts = append(parts, unparse.FormatType(payload.Type))
		}
	}
	return line + "(" + strings.Join(parts, ", ") + ")"
}

func formatDocAnnotation(annotation ast.Annotation) string {
	if len(annotation.Args) == 0 {
		return "@" + annotation.Name
	}
	return "@" + annotation.Name + "(" + strings.Join(annotation.Args, ", ") + ")"
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return text[:idx]
	}
	return text
}

func declarationHeadline(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "@") {
			continue
		}
		return line
	}
	return firstLine(trimmed)
}

func qualifyDocName(namespace string, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "." + name
}

func minHeading(level int) int {
	if level > 6 {
		return 6
	}
	return level
}
