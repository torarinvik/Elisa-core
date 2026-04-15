package unparse

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

const indentUnit = "    "

func FormatFile(file *ast.File) string {
	if file == nil {
		return ""
	}
	var f formatter
	for i, decl := range file.Decls {
		if i > 0 {
			f.blankLine()
		}
		f.writeDecl(0, decl)
	}
	return strings.TrimRight(f.builder.String(), "\n") + "\n"
}

func FormatDecl(decl ast.Decl) string {
	if decl == nil {
		return ""
	}
	var f formatter
	f.writeDecl(0, decl)
	return strings.TrimRight(f.builder.String(), "\n")
}

func FormatStmt(stmt ast.Stmt) string {
	if stmt == nil {
		return ""
	}
	var f formatter
	f.writeStmt(0, stmt)
	return strings.TrimRight(f.builder.String(), "\n")
}

func FormatType(typ ast.TypeExpr) string {
	return formatTypeExpr(typ)
}

func FormatExpr(expr ast.Expr) string {
	return formatExpr(expr)
}

type formatter struct {
	builder strings.Builder
}

func (f *formatter) blankLine() {
	f.builder.WriteByte('\n')
}

func (f *formatter) writeLine(level int, text string) {
	f.builder.WriteString(strings.Repeat(indentUnit, level))
	f.builder.WriteString(text)
	f.builder.WriteByte('\n')
}

func (f *formatter) writePrefixedMultiline(level int, prefix string, text string) {
	if text == "" {
		f.writeLine(level, prefix)
		return
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == len(lines)-1 && line == "" {
			continue
		}
		if i == 0 {
			f.writeLine(level, prefix+line)
			continue
		}
		f.writeLine(level, line)
	}
}

func indentMultilineText(text string, prefix string) string {
	if text == "" {
		return prefix
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == len(lines)-1 && line == "" {
			lines = lines[:len(lines)-1]
			break
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func (f *formatter) writeAnnotations(level int, annotations []ast.Annotation) {
	for _, annotation := range annotations {
		f.writeLine(level, formatAnnotation(annotation))
	}
}

func (f *formatter) writeDecl(level int, decl ast.Decl) {
	if decl == nil {
		return
	}
	switch n := decl.(type) {
	case *ast.PermissionDecl:
		f.writeLine(level, "permission "+n.Name+":")
		for _, member := range n.Members {
			f.writeLine(level+1, member)
		}
	case *ast.ContextDecl:
		f.writeLine(level, "context "+n.Name+":")
		for _, field := range n.Fields {
			f.writeLine(level+1, formatParamDecl(field))
		}
	case *ast.NamespaceDecl:
		f.writeLine(level, "namespace "+n.Name+":")
		for i, nested := range n.Decls {
			if i > 0 {
				f.blankLine()
			}
			f.writeDecl(level+1, nested)
		}
	case *ast.UsingDecl:
		f.writeLine(level, "using "+n.Name)
	case *ast.ConstDecl:
		line := "const " + n.Name
		if n.Type != nil {
			line += ": " + formatTypeExpr(n.Type)
		}
		line += " = " + formatExpr(n.Value)
		f.writePrefixedMultiline(level, "", line)
	case *ast.ConstEnumDecl:
		f.writeLine(level, "const enum "+n.Name+" of "+formatTypeExpr(n.Storage)+":")
		for _, member := range n.Members {
			line := member.Name
			if member.Value != nil {
				line += " = " + formatExpr(member.Value)
			}
			f.writeLine(level+1, line)
		}
	case *ast.ErrorDecl:
		f.writeLine(level, "error "+n.Name+":")
		for _, tag := range n.Tags {
			f.writeLine(level+1, tag)
		}
	case *ast.GlobalDecl:
		line := "global "
		if n.Mutable {
			line += "mutable "
		}
		line += n.Name + ": " + formatTypeExpr(n.Type)
		if n.Value != nil {
			line += " = " + formatExpr(n.Value)
		}
		f.writePrefixedMultiline(level, "", line)
	case *ast.EnumDecl:
		f.writeAnnotations(level, n.Annotations)
		header := ""
		if n.Packed {
			header += "packed "
		}
		header += "enum " + n.Name + ":"
		f.writeLine(level, header)
		if len(n.Common) > 0 {
			f.writeLine(level+1, "common:")
			for _, field := range n.Common {
				f.writeField(level+2, field)
			}
		}
		for _, variant := range n.Variants {
			f.writeLine(level+1, formatEnumVariantDecl(variant))
		}
	case *ast.TreeDecl:
		f.writeAnnotations(level, n.Annotations)
		f.writeLine(level, "tree "+n.Name+":")
		if len(n.Common) > 0 {
			f.writeLine(level+1, "common:")
			for _, field := range n.Common {
				f.writeField(level+2, field)
			}
		}
		for _, member := range n.Members {
			f.writeTreeMember(level+1, member)
		}
	case *ast.StructDecl:
		f.writeAnnotations(level, n.Annotations)
		header := ""
		if n.Affine {
			header += "affine "
		}
		header += "struct " + n.Name
		header += formatGenericParams(n.GenericParams, n.TypeParams, n.RefStorageParams, n.RefStateParams, nil, nil)
		header += formatAggregateStateSuffix(n.HasStateParam, n.StateParamCount)
		header += ":"
		f.writeLine(level, header)
		for _, field := range n.Fields {
			f.writeField(level+1, field)
		}
	case *ast.StoreDecl:
		f.writeAnnotations(level, n.Annotations)
		f.writeLine(level, "store "+n.Name+":")
		for _, field := range n.Fields {
			f.writeField(level+1, field)
		}
	case *ast.InterfaceDecl:
		f.writeLine(level, "interface "+n.Name+":")
		for _, member := range n.Members {
			switch m := member.(type) {
			case *ast.AssociatedTypeDecl:
				f.writeLine(level+1, "type "+m.Name)
			case *ast.ExternFuncDecl:
				f.writeLine(level+1, formatImplMethodHeader(m.Name, m.GenericParams, m.TypeParams, m.RefStorageParams, m.RefStateParams, m.RegionParams, m.PermissionParams, m.Params, m.ImplicitParams, m.ImplicitBundles, m.ImplicitItemOrder, m.ReturnType, m.Permissions, m.Ensures, m.Variadic))
			}
		}
	case *ast.ImplDecl:
		f.writeAnnotations(level, n.Annotations)
		header := ""
		if n.IsExtension() {
			header = "impl " + formatTypeExpr(n.ForType) + ":"
		} else {
			header = "impl " + n.InterfaceName + " for " + formatTypeExpr(n.ForType) + ":"
		}
		f.writeLine(level, header)
		for _, member := range n.Members {
			switch m := member.(type) {
			case *ast.ImplAssociatedTypeDecl:
				f.writeLine(level+1, "type "+m.Name+" = "+formatTypeExpr(m.Type))
			case *ast.FuncDecl:
				f.writeAnnotations(level+1, m.Annotations)
				header := formatFuncHeader(m.Name, m.GenericParams, m.TypeParams, m.RefStorageParams, m.RefStateParams, m.RegionParams, m.PermissionParams, m.Params, m.ImplicitParams, m.ImplicitBundles, m.ImplicitItemOrder, m.ReturnType, m.Permissions, m.Ensures, false)
				if m.Override {
					header = "override " + header
				}
				f.writeLine(level+1, header)
				for _, stmt := range m.Body {
					f.writeStmt(level+2, stmt)
				}
			case *ast.ExternFuncDecl:
				f.writeAnnotations(level+1, m.Annotations)
				header := formatImplMethodHeader(m.Name, m.GenericParams, m.TypeParams, m.RefStorageParams, m.RefStateParams, m.RegionParams, m.PermissionParams, m.Params, m.ImplicitParams, m.ImplicitBundles, m.ImplicitItemOrder, m.ReturnType, m.Permissions, m.Ensures, m.Variadic)
				if m.Override {
					header = "override " + header
				}
				f.writeLine(level+1, header)
			}
		}
	case *ast.FuncDecl:
		f.writeAnnotations(level, n.Annotations)
		f.writeLine(level, formatFuncHeader(n.Name, n.GenericParams, n.TypeParams, n.RefStorageParams, n.RefStateParams, n.RegionParams, n.PermissionParams, n.Params, n.ImplicitParams, n.ImplicitBundles, n.ImplicitItemOrder, n.ReturnType, n.Permissions, n.Ensures, false))
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.ExternFuncDecl:
		f.writeAnnotations(level, n.Annotations)
		f.writeLine(level, formatExternFuncHeader(n.Name, n.GenericParams, n.TypeParams, n.RefStorageParams, n.RefStateParams, n.RegionParams, n.PermissionParams, n.Params, n.ImplicitParams, n.ImplicitBundles, n.ImplicitItemOrder, n.ReturnType, n.Permissions, n.Ensures, n.Variadic))
	case *ast.ExternVarDecl:
		f.writeLine(level, "extern "+n.Name+": "+formatTypeExpr(n.Type))
	case *ast.ExternTypeDecl:
		f.writeLine(level, "extern "+n.Name)
	case *ast.ExportTypeDecl:
		f.writeLine(level, "export type "+formatTypeExpr(n.ExportedType)+" as "+n.Alias)
	case *ast.ExportFuncDecl:
		f.writeLine(level, formatExportFuncHeader(n))
	case *ast.ExportGlobalDecl:
		line := "export global " + n.TargetName
		if n.Alias != "" && n.Alias != n.TargetName {
			line += " as " + n.Alias
		}
		f.writeLine(level, line)
	case *ast.StaticIfDecl:
		f.writeLine(level, "static if "+formatExpr(n.Cond)+":")
		for _, nested := range n.Then {
			f.writeDecl(level+1, nested)
		}
		for _, elif := range n.Elifs {
			f.writeLine(level, "static elif "+formatExpr(elif.Cond)+":")
			for _, nested := range elif.Body {
				f.writeDecl(level+1, nested)
			}
		}
		if len(n.Else) > 0 {
			f.writeLine(level, "static else:")
			for _, nested := range n.Else {
				f.writeDecl(level+1, nested)
			}
		}
	}
}

func (f *formatter) writeTreeMember(level int, member ast.TreeMemberDecl) {
	if member == nil {
		return
	}
	switch n := member.(type) {
	case *ast.TreeCategoryDecl:
		f.writeAnnotations(level, n.Annotations)
		f.writeLine(level, "node "+n.Name+":")
		for _, variant := range n.Variants {
			f.writeLine(level+1, formatEnumVariantDecl(variant))
		}
	case *ast.TreeBlockDecl:
		f.writeAnnotations(level, n.Annotations)
		f.writeLine(level, "block "+n.Name+":")
		for _, field := range n.Fields {
			f.writeField(level+1, field)
		}
	case *ast.TreeStructDecl:
		f.writeAnnotations(level, n.Annotations)
		f.writeLine(level, "struct "+n.Name+":")
		for _, field := range n.Fields {
			f.writeField(level+1, field)
		}
	}
}

func (f *formatter) writeField(level int, field ast.FieldDecl) {
	f.writeAnnotations(level, field.Annotations)
	line := field.Name + ": "
	if field.Mutable {
		line += "mutable "
	}
	if field.IsTail {
		line += "tail "
	}
	line += formatTypeExpr(field.Type)
	f.writeLine(level, line)
}

func (f *formatter) writeStmt(level int, stmt ast.Stmt) {
	if stmt == nil {
		return
	}
	switch n := stmt.(type) {
	case *ast.AssignStmt:
		f.writePrefixedMultiline(level, "", formatExpr(n.Target)+" <- "+formatExpr(n.Value))
	case *ast.AugAssignStmt:
		f.writePrefixedMultiline(level, "", formatExpr(n.Target)+" "+lexer.TokenName(n.Op)+" "+formatExpr(n.Value))
	case *ast.AsRefAssignStmt:
		line := formatExpr(n.Target) + " as"
		if n.AsKind != "" {
			line += " " + n.AsKind
		}
		line += " <- " + formatExpr(n.Value)
		f.writePrefixedMultiline(level, "", line)
	case *ast.VarDeclStmt:
		if n.Type == nil {
			f.writePrefixedMultiline(level, "", n.Name+" = "+formatExpr(n.Value))
			return
		}
		line := n.Name + ": "
		if n.Mutable {
			line += "mutable "
		}
		line += formatTypeExpr(n.Type)
		if n.Value != nil {
			line += " = " + formatExpr(n.Value)
		}
		f.writePrefixedMultiline(level, "", line)
	case *ast.TupleBindStmt:
		names := make([]string, 0, len(n.Names))
		for _, name := range n.Names {
			names = append(names, name.Name)
		}
		op := " <- "
		if n.Declare {
			op = " = "
		}
		f.writePrefixedMultiline(level, "", strings.Join(names, ", ")+op+formatExpr(n.Value))
	case *ast.MoveBindStmt:
		line := formatExpr(n.Value)
		if _, ok := n.Value.(*ast.MoveExpr); !ok {
			line = "move " + line
		}
		if n.Store != nil {
			line += " in " + formatExpr(n.Store)
		}
		line += " as " + formatMoveBindPattern(n.Pattern)
		f.writePrefixedMultiline(level, "", line)
	case *ast.OpenStmt:
		line := "open " + formatExpr(n.Value)
		if n.Store != nil {
			line += " in " + formatExpr(n.Store)
		}
		line += " as " + formatMoveBindVariantPattern(n.Pattern) + ":"
		f.writeLine(level, line)
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.ViewStmt:
		line := "view " + formatExpr(n.Value)
		if n.Store != nil {
			line += " in " + formatExpr(n.Store)
		}
		line += " as " + formatViewBindPattern(n.Pattern) + ":"
		f.writeLine(level, line)
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.DeferStmt:
		mode := "block"
		if n.Mode == ast.DeferModeFunction {
			mode = "function"
		}
		f.writeLine(level, "defer "+mode+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.ReturnStmt:
		if n.Value == nil {
			f.writeLine(level, "return")
			return
		}
		f.writePrefixedMultiline(level, "return ", formatExpr(n.Value))
	case *ast.IfStmt:
		f.writeLine(level, formatIfHeader("if", n.Hint, n.Cond))
		for _, stmt := range n.Then {
			f.writeStmt(level+1, stmt)
		}
		for _, elif := range n.Elifs {
			f.writeLine(level, formatIfHeader("elif", elif.Hint, elif.Cond))
			for _, stmt := range elif.Body {
				f.writeStmt(level+1, stmt)
			}
		}
		if len(n.Else) > 0 {
			f.writeLine(level, "else:")
			for _, stmt := range n.Else {
				f.writeStmt(level+1, stmt)
			}
		}
	case *ast.WhileStmt:
		f.writeLine(level, formatIfHeader("while", n.Hint, n.Cond))
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.ForStmt:
		line := "for "
		if n.Reverse {
			line += "rev "
		}
		line += n.Name + " in " + formatExpr(n.Start) + " " + lexer.TokenName(n.Op) + " " + formatExpr(n.End)
		if n.Step != nil {
			line += " .. " + formatExpr(n.Step)
		}
		line += ":"
		f.writeLine(level, line)
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.IterForStmt:
		line := "for "
		switch n.Mode {
		case ast.IterBindRef:
			line += "ref "
		case ast.IterBindMutableRef:
			line += "mutable ref "
		}
		sourceText := formatExpr(n.Source)
		if n.Reverse {
			sourceText = "rev(" + sourceText + ")"
		}
		line += formatMoveBindPattern(n.Pattern) + " in " + sourceText + ":"
		f.writeLine(level, line)
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.ParallelForStmt:
		line := "parallel for " + n.Name
		if n.IndexName != "" {
			line += " at " + n.IndexName
		}
		line += " in " + formatExpr(n.Source) + ":"
		f.writeLine(level, line)
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.MatchStmt:
		f.writeLine(level, formatMatchHeader("match", n.Value, n.Store))
		f.writeMatchArms(level, n.Arms)
	case *ast.InStoreStmt:
		f.writeLine(level, "in "+formatExpr(n.Store)+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.CanStmt:
		f.writeLine(level, "can"+formatPermissionRefs(n.Permissions)+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.WithStmt:
		f.writeLine(level, "with "+formatWithValueClause(n.Bundles, n.Args, n.WithItemOrder)+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.ScopeStmt:
		f.writeLine(level, "scope "+formatExpr(n.Guard)+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.PoolStmt:
		f.writeLine(level, "pool "+n.Name+"("+formatExpr(n.Workers)+"):")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.LockStmt:
		f.writeLine(level, "lock "+formatExpr(n.Mutex)+" as "+n.GuardName+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.PassStmt:
		f.writeLine(level, "pass")
	case *ast.PanicStmt:
		f.writePrefixedMultiline(level, "", "panic("+formatExpr(n.Message)+")")
	case *ast.ExprStmt:
		f.writePrefixedMultiline(level, "", formatExpr(n.Expr))
	case *ast.StaticIfStmt:
		f.writeLine(level, "static if "+formatExpr(n.Cond)+":")
		for _, stmt := range n.Then {
			f.writeStmt(level+1, stmt)
		}
		for _, elif := range n.Elifs {
			f.writeLine(level, "static elif "+formatExpr(elif.Cond)+":")
			for _, stmt := range elif.Body {
				f.writeStmt(level+1, stmt)
			}
		}
		if len(n.Else) > 0 {
			f.writeLine(level, "static else:")
			for _, stmt := range n.Else {
				f.writeStmt(level+1, stmt)
			}
		}
	case *ast.StaticErrorStmt:
		f.writePrefixedMultiline(level, "", "static error("+formatExpr(n.Message)+")")
	case *ast.DiscardStmt:
		f.writePrefixedMultiline(level, "", "_ = "+formatExpr(n.Value))
	case *ast.RegionStmt:
		line := "region " + n.Name
		if n.Capacity != nil {
			line += "(" + formatExpr(n.Capacity) + ")"
		}
		f.writeLine(level, line)
	case *ast.DestroyStmt:
		f.writeLine(level, "destroy "+n.Name)
	case *ast.MarkStmt:
		f.writeLine(level, "mark "+n.RegionName+" as "+n.Name)
	case *ast.CheckpointStmt:
		line := "checkpoint " + n.Name + " = " + formatExpr(n.Target)
		if len(n.Body) != 0 {
			line += ":"
		}
		f.writeLine(level, line)
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.GroupedCheckpointStmt:
		parts := make([]string, 0, len(n.Targets))
		for _, target := range n.Targets {
			parts = append(parts, formatExpr(target))
		}
		f.writeLine(level, "checkpoint "+strings.Join(parts, ", ")+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.RestoreStmt:
		f.writeLine(level, "restore "+n.RegionName+" from "+n.MarkName)
	case *ast.RestoreCheckpointStmt:
		f.writeLine(level, "restore "+n.Name)
	case *ast.ResetStmt:
		f.writeLine(level, "reset "+n.Name)
	}
}

func (f *formatter) writeMatchArms(level int, arms []ast.MatchArm) {
	for _, arm := range arms {
		f.writeLine(level+1, formatMatchPattern(arm.Pattern)+":")
		for _, stmt := range arm.Body {
			f.writeStmt(level+2, stmt)
		}
	}
}

func formatAnnotation(annotation ast.Annotation) string {
	if len(annotation.Args) == 0 {
		return "@" + annotation.Name
	}
	return "@" + annotation.Name + "(" + strings.Join(annotation.Args, ", ") + ")"
}

func formatGenericParams(genericParams []ast.GenericParam, typeParams []string, refStorageParams []string, refStateParams []string, regionParams []string, permissionParams []string) string {
	parts := make([]string, 0, len(genericParams)+len(typeParams)+len(refStorageParams)+len(refStateParams)+len(regionParams)+len(permissionParams))
	if len(genericParams) != 0 {
		for _, param := range genericParams {
			switch param.Kind {
			case ast.GenericParamRefStorage:
				parts = append(parts, "refstorage "+param.Name)
			case ast.GenericParamRefState:
				parts = append(parts, "refstate "+param.Name)
			case ast.GenericParamRegion:
				parts = append(parts, "region "+param.Name)
			case ast.GenericParamPermission:
				parts = append(parts, "permission "+param.Name)
			default:
				if param.InterfaceBound != "" {
					parts = append(parts, param.Name+": "+param.InterfaceBound)
				} else {
					parts = append(parts, param.Name)
				}
			}
		}
	} else {
		parts = append(parts, typeParams...)
		for _, name := range refStorageParams {
			parts = append(parts, "refstorage "+name)
		}
		for _, name := range refStateParams {
			parts = append(parts, "refstate "+name)
		}
		for _, name := range regionParams {
			parts = append(parts, "region "+name)
		}
		for _, name := range permissionParams {
			parts = append(parts, "permission "+name)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func formatAggregateStateSuffix(hasStateParam bool, stateParamCount int) string {
	count := stateParamCount
	if count == 0 && hasStateParam {
		count = 1
	}
	if count <= 0 {
		return ""
	}
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "?"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func formatFuncHeader(name string, genericParams []ast.GenericParam, typeParams []string, refStorageParams []string, refStateParams []string, regionParams []string, permissionParams []string, params []ast.ParamDecl, implicitParams []ast.ParamDecl, implicitBundles []string, implicitItemOrder []ast.ImplicitSigItem, retType ast.TypeExpr, permissions []ast.PermissionRef, ensures []ast.EnsuresClause, variadic bool) string {
	line := formatImplMethodHeader(name, genericParams, typeParams, refStorageParams, refStateParams, regionParams, permissionParams, params, implicitParams, implicitBundles, implicitItemOrder, retType, permissions, ensures, variadic)
	line += ":"
	return line
}

func formatImplMethodHeader(name string, genericParams []ast.GenericParam, typeParams []string, refStorageParams []string, refStateParams []string, regionParams []string, permissionParams []string, params []ast.ParamDecl, implicitParams []ast.ParamDecl, implicitBundles []string, implicitItemOrder []ast.ImplicitSigItem, retType ast.TypeExpr, permissions []ast.PermissionRef, ensures []ast.EnsuresClause, variadic bool) string {
	line := "def " + name
	line += formatGenericParams(genericParams, typeParams, refStorageParams, refStateParams, regionParams, permissionParams)
	line += "(" + formatParamList(params, variadic) + ")"
	line += formatWithSignatureClause(implicitBundles, implicitParams, implicitItemOrder)
	if retType != nil {
		line += " -> " + formatTypeExpr(retType)
	}
	line += formatPermissionRefs(permissions)
	line += formatEnsuresClauses(ensures)
	return line
}

func formatExternFuncHeader(name string, genericParams []ast.GenericParam, typeParams []string, refStorageParams []string, refStateParams []string, regionParams []string, permissionParams []string, params []ast.ParamDecl, implicitParams []ast.ParamDecl, implicitBundles []string, implicitItemOrder []ast.ImplicitSigItem, retType ast.TypeExpr, permissions []ast.PermissionRef, ensures []ast.EnsuresClause, variadic bool) string {
	line := "extern " + name
	line += formatGenericParams(genericParams, typeParams, refStorageParams, refStateParams, regionParams, permissionParams)
	line += "(" + formatParamList(params, variadic) + ")"
	line += formatWithSignatureClause(implicitBundles, implicitParams, implicitItemOrder)
	if retType != nil {
		line += " -> " + formatTypeExpr(retType)
	}
	line += formatPermissionRefs(permissions)
	line += formatEnsuresClauses(ensures)
	return line
}

func formatExportFuncHeader(n *ast.ExportFuncDecl) string {
	line := "export func " + n.Name + "(" + formatParamList(n.Params, false) + ")"
	if n.ReturnType != nil {
		line += " -> " + formatTypeExpr(n.ReturnType)
	}
	line += " = " + n.TargetName
	if len(n.TargetTypeArgs) > 0 {
		parts := make([]string, 0, len(n.TargetTypeArgs))
		for _, arg := range n.TargetTypeArgs {
			parts = append(parts, formatTypeExpr(arg))
		}
		line += "[" + strings.Join(parts, ", ") + "]"
	}
	return line
}

func formatParamList(params []ast.ParamDecl, variadic bool) string {
	parts := make([]string, 0, len(params)+1)
	for _, param := range params {
		parts = append(parts, formatParamDecl(param))
	}
	if variadic {
		parts = append(parts, "...")
	}
	return strings.Join(parts, ", ")
}

func formatParamDecl(param ast.ParamDecl) string {
	line := ""
	if param.Mutable {
		line += "mutable "
	}
	line += param.Name + ": " + formatTypeExpr(param.Type)
	if param.DefaultValue != nil {
		line += " = " + formatExpr(param.DefaultValue)
	}
	return line
}

func formatWithSignatureClause(bundles []string, params []ast.ParamDecl, order []ast.ImplicitSigItem) string {
	parts := make([]string, 0, len(bundles)+len(params))
	if len(order) != 0 {
		for _, item := range order {
			if item.IsBundle {
				parts = append(parts, item.Bundle)
				continue
			}
			parts = append(parts, formatParamDecl(item.Param))
		}
	} else {
		parts = append(parts, bundles...)
		for _, param := range params {
			parts = append(parts, formatParamDecl(param))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " with " + strings.Join(parts, ", ")
}

func formatWithArg(arg ast.WithArg) string {
	if arg.Shorthand {
		return arg.Name
	}
	if arg.Value == nil {
		return arg.Name
	}
	return arg.Name + " = " + formatExpr(arg.Value)
}

func formatWithBundleUse(bundle ast.WithBundleUse) string {
	parts := make([]string, 0, len(bundle.Args)+1)
	if bundle.Spread {
		parts = append(parts, "..")
	}
	for _, arg := range bundle.Args {
		parts = append(parts, arg.Name+" = "+formatExpr(arg.Value))
	}
	return bundle.Name + "(" + strings.Join(parts, ", ") + ")"
}

func formatWithValueClause(bundles []ast.WithBundleUse, args []ast.WithArg, order []ast.WithItem) string {
	parts := make([]string, 0, len(bundles)+len(args))
	if len(order) != 0 {
		for _, item := range order {
			if item.IsBundle {
				parts = append(parts, formatWithBundleUse(item.Bundle))
				continue
			}
			parts = append(parts, formatWithArg(item.Arg))
		}
	} else {
		for _, bundle := range bundles {
			parts = append(parts, formatWithBundleUse(bundle))
		}
		for _, arg := range args {
			parts = append(parts, formatWithArg(arg))
		}
	}
	return strings.Join(parts, ", ")
}

func formatPermissionRefs(refs []ast.PermissionRef) string {
	if len(refs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Member != "" {
			parts = append(parts, ref.Name+"."+ref.Member)
		} else {
			parts = append(parts, ref.Name)
		}
	}
	return " can[" + strings.Join(parts, ", ") + "]"
}

func formatEnsuresClauses(clauses []ast.EnsuresClause) string {
	if len(clauses) == 0 {
		return ""
	}
	parts := make([]string, 0, len(clauses))
	for _, clause := range clauses {
		parts = append(parts, formatEnsuresClause(clause))
	}
	return " ensures " + strings.Join(parts, ", ")
}

func formatEnsuresClause(clause ast.EnsuresClause) string {
	left := formatEnsuresPath(clause.Target)
	switch clause.Kind {
	case ast.EnsuresKindPreserve:
		return left + " => preserve"
	case ast.EnsuresKindRefState:
		return left + " => " + formatEnsuresRefState(clause.RefState)
	default:
		return left + " => " + strings.Join(clause.StateCases, " | ")
	}
}

func formatEnsuresPath(path ast.EnsuresPath) string {
	if len(path.Fields) == 0 {
		return path.Root
	}
	return path.Root + "." + strings.Join(path.Fields, ".")
}

func formatEnsuresRefState(state ast.RefState) string {
	switch state {
	case ast.RefStateNullable:
		return "&?"
	case ast.RefStateNull:
		return "!"
	default:
		return "&"
	}
}

func formatEnumVariantDecl(variant ast.EnumVariantDecl) string {
	line := variant.Name
	if len(variant.Payload) == 0 {
		return line
	}
	parts := make([]string, 0, len(variant.Payload))
	for _, payload := range variant.Payload {
		part := ""
		if payload.Relation != ast.EnumPayloadRelationNone {
			part = string(payload.Relation) + " "
		}
		if payload.Name != "" {
			if optionalType, ok := payload.Type.(*ast.OptionalTypeExpr); ok && optionalType != nil {
				part += payload.Name + "?: " + formatTypeExpr(optionalType.Value)
			} else {
				part += payload.Name + ": " + formatTypeExpr(payload.Type)
			}
		} else {
			part += formatTypeExpr(payload.Type)
		}
		parts = append(parts, part)
	}
	return line + "(" + strings.Join(parts, ", ") + ")"
}

func formatIfHeader(keyword string, hint ast.BranchHint, cond ast.Expr) string {
	line := keyword
	switch hint {
	case ast.BranchHintLikely:
		line += " likely"
	case ast.BranchHintUnlikely:
		line += " unlikely"
	}
	line += " " + formatExpr(cond) + ":"
	return line
}

func formatMatchHeader(keyword string, value ast.Expr, store ast.Expr) string {
	line := keyword + " " + formatExpr(value)
	if store != nil {
		line += " in " + formatExpr(store)
	}
	line += ":"
	return line
}

func formatTypeExpr(typ ast.TypeExpr) string {
	if typ == nil {
		return "void"
	}
	switch n := typ.(type) {
	case *ast.NamedType:
		return n.Name
	case *ast.RefType:
		prefix := ""
		switch {
		case n.StorageParam != "":
			prefix = n.StorageParam + " "
		case n.Region != "":
			prefix = n.Region + " "
		case n.Explicit:
			prefix = formatRefStorage(n.Storage) + " "
		}
		suffix := "&"
		switch n.State {
		case ast.RefStateNullable:
			suffix = "&?"
		case ast.RefStateNull:
			suffix = "!"
		default:
			if n.StateParam != "" {
				suffix = "&[" + n.StateParam + "]"
			}
		}
		return prefix + formatTypeExpr(n.Elem) + suffix
	case *ast.RefStateLiteralTypeExpr:
		return ast.RefStateMarker(n.State)
	case *ast.RefStorageLiteralTypeExpr:
		return formatRefStorage(n.Storage)
	case *ast.GenericType:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			parts = append(parts, formatTypeExpr(arg))
		}
		return n.Name + "[" + strings.Join(parts, ", ") + "]"
	case *ast.AggregateStateTypeExpr:
		parts := make([]string, 0, len(n.States))
		if len(n.States) != 0 {
			for _, state := range n.States {
				parts = append(parts, ast.RefStateMarker(state))
			}
		} else {
			parts = append(parts, ast.RefStateMarker(n.State))
		}
		return formatTypeExpr(n.Base) + "[" + strings.Join(parts, ", ") + "]"
	case *ast.MutableType:
		return "mutable " + formatTypeExpr(n.Elem)
	case *ast.TailType:
		return "tail " + formatTypeExpr(n.Elem)
	case *ast.ArrayType:
		return formatTypeExpr(n.Elem) + "[" + formatExpr(n.Size) + "]"
	case *ast.BuiltinTypeExpr:
		if n.Name == "treeview" && len(n.TypeArgs) == 1 && len(n.ValueArgs) == 0 {
			return formatTypeExpr(n.TypeArgs[0])
		}
		parts := make([]string, 0, len(n.TypeArgs)+len(n.ValueArgs))
		for _, arg := range n.TypeArgs {
			parts = append(parts, formatTypeExpr(arg))
		}
		for _, arg := range n.ValueArgs {
			parts = append(parts, formatExpr(arg))
		}
		return n.Name + "[" + strings.Join(parts, ", ") + "]"
	case *ast.FuncTypeExpr:
		parts := make([]string, 0, len(n.Params)+1)
		for _, param := range n.Params {
			parts = append(parts, formatTypeExpr(param))
		}
		if n.Variadic {
			parts = append(parts, "...")
		}
		line := "func(" + strings.Join(parts, ", ") + ")"
		line += formatWithSignatureClause(n.ImplicitBundles, n.ImplicitParams, n.ImplicitItemOrder)
		if n.Return != nil {
			line += " -> " + formatTypeExpr(n.Return)
		}
		line += formatPermissionRefs(n.Permissions)
		return line
	case *ast.ErrorSetExpr:
		parts := make([]string, 0, len(n.Tags)+1)
		for _, tag := range n.Tags {
			parts = append(parts, formatErrorTagExpr(tag))
		}
		if n.HasEllipsis {
			parts = append(parts, "...")
		}
		return "error[" + strings.Join(parts, ", ") + "]"
	case *ast.ErrorUnionTypeExpr:
		return formatTypeExpr(n.Value) + " " + formatTypeExpr(n.Errors)
	case *ast.OptionalTypeExpr:
		return formatTypeExpr(n.Value) + "?"
	case *ast.TupleTypeExpr:
		parts := make([]string, 0, len(n.Fields))
		for _, field := range n.Fields {
			parts = append(parts, field.Name+": "+formatTypeExpr(field.Type))
		}
		return "(" + strings.Join(parts, ", ") + ")"
	default:
		return "<type>"
	}
}

func formatErrorTagExpr(tag ast.ErrorTagExpr) string {
	if tag.Tag == "" {
		return tag.SetName
	}
	return tag.SetName + "." + tag.Tag
}

func formatRefStorage(storage ast.RefStorage) string {
	switch storage {
	case ast.RefStorageHeap:
		return "heap"
	case ast.RefStorageStack:
		return "stack"
	case ast.RefStorageStatic:
		return "static"
	default:
		return "any"
	}
}

func formatExpr(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.IntLit:
		if n.Suffix != "" {
			return n.Value + n.Suffix
		}
		return n.Value
	case *ast.FloatLit:
		if n.Suffix != "" {
			return n.Value + n.Suffix
		}
		return n.Value
	case *ast.StringLit:
		return strconv.Quote(n.Value)
	case *ast.CharLit:
		return formatCharLiteral(n.Value)
	case *ast.BoolLit:
		if n.Value {
			return "true"
		}
		return "false"
	case *ast.NullLit:
		return "null"
	case *ast.ZeroedLit:
		return "zeroed"
	case *ast.BinaryExpr:
		return "(" + formatExpr(n.Left) + " " + lexer.TokenName(n.Op) + " " + formatExpr(n.Right) + ")"
	case *ast.UnaryExpr:
		op := lexer.TokenName(n.Op)
		if op == "not" {
			return "(not " + formatExpr(n.Operand) + ")"
		}
		return "(" + op + formatExpr(n.Operand) + ")"
	case *ast.MoveExpr:
		return "move " + formatExpr(n.Operand)
	case *ast.CallExpr:
		funcText := formatExpr(n.Func)
		if n.Safe {
			if fieldExpr, ok := n.Func.(*ast.FieldExpr); ok && fieldExpr != nil {
				funcText = formatExpr(fieldExpr.Object) + "?." + fieldExpr.Field
			}
		}
		partCapacity := len(n.Args)
		if n.HasArgForward {
			partCapacity++
		}
		parts := make([]string, 0, partCapacity)
		multiline := strings.Contains(funcText, "\n")
		if n.HasArgForward {
			parts = append(parts, "..")
		}
		for i, arg := range n.Args {
			argText := formatExpr(arg)
			if strings.Contains(argText, "\n") {
				multiline = true
				argText = indentMultilineText(argText, indentUnit)
			}
			if name := n.ArgName(i); name != "" {
				parts = append(parts, name+": "+argText)
			} else {
				parts = append(parts, argText)
			}
		}
		line := ""
		if multiline {
			line = funcText + "(\n" + strings.Join(parts, ",\n") + "\n)"
		} else {
			line = funcText + "(" + strings.Join(parts, ", ") + ")"
		}
		if len(n.WithArgs) != 0 || len(n.WithBundles) != 0 {
			line += " with " + formatWithValueClause(n.WithBundles, n.WithArgs, n.WithItemOrder)
		}
		return line
	case *ast.FieldExpr:
		if n.Safe {
			return formatExpr(n.Object) + "?." + n.Field
		}
		return formatExpr(n.Object) + "." + n.Field
	case *ast.ShorthandMemberExpr:
		return "." + strings.Join(n.Parts, ".")
	case *ast.IndexExpr:
		return formatExpr(n.Object) + "[" + formatExpr(n.Index) + "]"
	case *ast.SliceExpr:
		return formatExpr(n.Object) + "[" + formatExpr(n.Start) + ":" + formatExpr(n.End) + "]"
	case *ast.ListLitExpr:
		parts := make([]string, 0, len(n.Elems))
		for _, elem := range n.Elems {
			parts = append(parts, formatExpr(elem))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ast.CastExpr:
		if n.Origin == ast.CastExprOriginToSyntax {
			return formatExpr(n.Operand) + " to " + formatTypeExpr(n.Target)
		}
		if n.Origin == ast.CastExprOriginPostfixShorthand && !n.LegacySyntax {
			if named, ok := n.Target.(*ast.NamedType); ok {
				return formatExpr(n.Operand) + "." + named.Name + "()"
			}
		}
		return formatExpr(n.Operand) + ".cast[" + formatTypeExpr(n.Target) + "]"
	case *ast.SizeofExpr:
		return "sizeof(" + formatTypeExpr(n.Type) + ")"
	case *ast.TernaryExpr:
		return "(" + formatExpr(n.Value) + " if " + formatExpr(n.Cond) + " else " + formatExpr(n.Alt) + ")"
	case *ast.AddrOfExpr:
		return "&" + formatExpr(n.Operand)
	case *ast.SpecializeExpr:
		parts := make([]string, 0, len(n.TypeArgs))
		for _, arg := range n.TypeArgs {
			parts = append(parts, formatTypeExpr(arg))
		}
		return formatExpr(n.Operand) + "[" + strings.Join(parts, ", ") + "]"
	case *ast.StructLitExpr:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			parts = append(parts, formatExpr(arg))
		}
		return n.Name + "(" + strings.Join(parts, ", ") + ")"
	case *ast.TupleExpr:
		parts := make([]string, 0, len(n.Elems))
		for _, elem := range n.Elems {
			parts = append(parts, formatExpr(elem))
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case *ast.ExprBlock:
		lines := []string{"do:"}
		for _, stmt := range n.Stmts {
			formatted := strings.TrimRight(FormatStmt(stmt), "\n")
			for _, line := range strings.Split(formatted, "\n") {
				lines = append(lines, indentUnit+line)
			}
		}
		for _, line := range strings.Split(strings.TrimRight(formatExpr(n.Value), "\n"), "\n") {
			lines = append(lines, indentUnit+line)
		}
		return strings.Join(lines, "\n")
	case *ast.VariantTestExpr:
		if n.Pattern == nil {
			return "<variant-test>"
		}
		return formatMatchPattern(n.Pattern)
	case *ast.StructTestExpr:
		if n.Pattern == nil {
			return "<struct-test>"
		}
		return formatMatchPattern(n.Pattern)
	case *ast.IsPatternExpr:
		parts := make([]string, 0, len(n.Targets))
		for _, target := range n.Targets {
			parts = append(parts, formatExpr(target))
		}
		return strings.Join(parts, " | ")
	case *ast.ParenExpr:
		return "(" + formatExpr(n.Inner) + ")"
	case *ast.RaiseExpr:
		return "raise " + formatExpr(n.Error)
	case *ast.TryExpr:
		line := "try " + formatExpr(n.Value)
		if n.Fallback != nil {
			line += " else " + formatExpr(n.Fallback)
		}
		return line
	case *ast.UnwrapElseExpr:
		return formatExpr(n.Value) + " else " + formatExpr(n.Fallback)
	case *ast.OptionalBindExpr:
		return "let " + n.Name + " = " + formatExpr(n.Value)
	case *ast.AllocExpr:
		if n.Owner != nil {
			return "new[" + formatExpr(n.Owner) + "] " + formatExpr(n.Value)
		}
		return "new " + formatExpr(n.Value)
	case *ast.CanExpr:
		return formatExpr(n.Expr) + formatPermissionRefs(n.Permissions)
	case *ast.MatchExpr:
		return formatMatchExpr(n)
	case *ast.VisitExpr:
		return formatVisitExpr(n)
	case *ast.FoldExpr:
		return formatFoldExpr(n)
	default:
		return "<expr>"
	}
}

func formatMatchExpr(expr *ast.MatchExpr) string {
	if expr == nil {
		return "match <nil>:"
	}
	var builder strings.Builder
	builder.WriteString(formatMatchHeader("match", expr.Value, expr.Store))
	for _, arm := range expr.Arms {
		builder.WriteByte('\n')
		builder.WriteString(indentUnit)
		builder.WriteString(formatMatchPattern(arm.Pattern))
		builder.WriteString(":")
		for _, stmt := range arm.Body {
			builder.WriteByte('\n')
			stmtText := indentMultiline(FormatStmt(stmt), 2)
			builder.WriteString(strings.TrimRight(stmtText, "\n"))
		}
	}
	return builder.String()
}

func formatVisitExpr(expr *ast.VisitExpr) string {
	if expr == nil {
		return "visit <nil>:"
	}
	var builder strings.Builder
	builder.WriteString("visit ")
	builder.WriteString(formatExpr(expr.Value))
	if expr.Root != nil {
		builder.WriteString(" as ")
		builder.WriteString(formatTypeExpr(expr.Root))
	}
	builder.WriteString(":")
	formatVisitArmsInto(&builder, expr.Arms)
	return builder.String()
}

func formatFoldExpr(expr *ast.FoldExpr) string {
	if expr == nil {
		return "fold <nil> as <type> into <type>:"
	}
	var builder strings.Builder
	builder.WriteString("fold ")
	builder.WriteString(formatExpr(expr.Value))
	builder.WriteString(" as ")
	builder.WriteString(formatTypeExpr(expr.Root))
	builder.WriteString(" into ")
	builder.WriteString(formatTypeExpr(expr.ResultType))
	builder.WriteString(":")
	formatVisitArmsInto(&builder, expr.Arms)
	return builder.String()
}

func formatVisitArmsInto(builder *strings.Builder, arms []ast.VisitArm) {
	for _, arm := range arms {
		builder.WriteByte('\n')
		builder.WriteString(indentUnit)
		builder.WriteString(formatVisitArm(arm))
		builder.WriteString(":")
		for _, stmt := range arm.Body {
			builder.WriteByte('\n')
			stmtText := indentMultiline(FormatStmt(stmt), 2)
			builder.WriteString(strings.TrimRight(stmtText, "\n"))
		}
	}
}

func formatVisitArm(arm ast.VisitArm) string {
	if arm.Wildcard {
		line := "_"
		if arm.Guard != nil {
			line += " when " + formatExpr(arm.Guard)
		}
		return line
	}
	line := arm.TargetName
	if arm.BindName == "" && arm.ChildResultsName == "" && len(arm.ChildBindings) == 0 {
		if arm.Guard != nil {
			line += " when " + formatExpr(arm.Guard)
		}
		return line
	}
	line += "("
	if arm.BindName != "" {
		line += arm.BindName
	}
	if arm.ChildResultsName != "" {
		if arm.BindName != "" {
			line += ", "
		}
		line += arm.ChildResultsName
	} else if len(arm.ChildBindings) != 0 {
		for i, binding := range arm.ChildBindings {
			if arm.BindName != "" || i != 0 {
				line += ", "
			}
			line += binding.FieldName
			if binding.BindName != "" && binding.BindName != binding.FieldName {
				line += ": "
				line += binding.BindName
			}
		}
	}
	line += ")"
	if arm.Guard != nil {
		line += " when " + formatExpr(arm.Guard)
	}
	return line
}

func formatMatchPattern(pattern ast.MatchPattern) string {
	switch n := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return "_"
	case *ast.MatchBindPattern:
		return n.Name
	case *ast.MatchStringLiteralPattern:
		return strconv.Quote(n.Value)
	case *ast.MatchLiteralPattern:
		return formatExpr(n.Value)
	case *ast.MatchStructPattern:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			part := formatMatchPattern(arg.Pattern)
			if arg.Name != "" {
				part = arg.Name + ": " + part
			}
			parts = append(parts, part)
		}
		if len(parts) == 0 {
			return n.TypeName + "()"
		}
		return n.TypeName + "(" + strings.Join(parts, ", ") + ")"
	case *ast.MatchVariantPattern:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			if arg.Name != "" {
				parts = append(parts, arg.Name+": "+formatMatchPattern(arg.Pattern))
			} else {
				parts = append(parts, formatMatchPattern(arg.Pattern))
			}
		}
		line := n.EnumName + "." + n.Variant
		if len(parts) != 0 {
			line += "(" + strings.Join(parts, ", ") + ")"
		}
		return line
	default:
		return "<pattern>"
	}
}

func formatMoveBindPattern(pattern ast.MoveBindPattern) string {
	switch n := pattern.(type) {
	case *ast.MoveBindNamePattern:
		return n.Name
	case *ast.MoveBindStructPattern:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			parts = append(parts, arg.Name)
		}
		return n.TypeName + "(" + strings.Join(parts, ", ") + ")"
	case *ast.MoveBindTuplePattern:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			parts = append(parts, arg.Name)
		}
		return strings.Join(parts, ", ")
	case *ast.MoveBindVariantPattern:
		return formatMoveBindVariantPattern(n)
	default:
		return "<move-pattern>"
	}
}

func formatMoveBindVariantPattern(pattern *ast.MoveBindVariantPattern) string {
	if pattern == nil {
		return "<move-variant-pattern>"
	}
	parts := make([]string, 0, len(pattern.Args))
	for _, arg := range pattern.Args {
		if arg.Name != "" {
			parts = append(parts, arg.Name+": "+formatMatchPattern(arg.Pattern))
		} else {
			parts = append(parts, formatMatchPattern(arg.Pattern))
		}
	}
	line := pattern.EnumName + "." + pattern.Variant
	if len(parts) != 0 {
		line += "(" + strings.Join(parts, ", ") + ")"
	}
	return line
}

func formatViewBindPattern(pattern *ast.ViewBindPattern) string {
	if pattern == nil {
		return "<view-pattern>"
	}
	if pattern.Name != "" {
		return pattern.EnumName + "." + pattern.Variant + "(" + pattern.Name + ")"
	}
	parts := make([]string, 0, len(pattern.Args))
	for _, arg := range pattern.Args {
		if arg.Name != "" {
			parts = append(parts, arg.Name+": "+formatMatchPattern(arg.Pattern))
		} else {
			parts = append(parts, formatMatchPattern(arg.Pattern))
		}
	}
	line := pattern.EnumName + "." + pattern.Variant
	if len(parts) != 0 {
		line += "(" + strings.Join(parts, ", ") + ")"
	}
	return line
}

func indentMultiline(text string, level int) string {
	if text == "" {
		return ""
	}
	indent := strings.Repeat(indentUnit, level)
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n") + "\n"
}

func formatCharLiteral(value string) string {
	if value == "" {
		return "'\\0'"
	}
	if utf8.ValidString(value) {
		r, size := utf8.DecodeRuneInString(value)
		if r != utf8.RuneError && size == len(value) {
			return strconv.QuoteRuneToASCII(r)
		}
	}
	if len(value) == 1 {
		return "'" + escapeSingleQuotedByte(value[0]) + "'"
	}
	return "'\\x00'"
}

func escapeSingleQuotedByte(b byte) string {
	switch b {
	case '\\':
		return "\\\\"
	case '\'':
		return "\\'"
	case '\n':
		return "\\n"
	case '\r':
		return "\\r"
	case '\t':
		return "\\t"
	case 0:
		return "\\0"
	default:
		if b < 0x20 || b >= 0x7f {
			hex := strings.ToUpper(strconv.FormatInt(int64(b), 16))
			if len(hex) < 2 {
				hex = "0" + hex
			}
			return "\\x" + hex
		}
		return string([]byte{b})
	}
}
