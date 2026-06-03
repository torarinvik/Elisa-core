package main

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func aggregateStateExprMarkers(states []ast.RefState, fallback ast.RefState) string {
	if len(states) == 0 {
		return "[" + ast.RefStateMarker(fallback) + "]"
	}
	parts := make([]string, 0, len(states))
	for _, state := range states {
		parts = append(parts, ast.RefStateMarker(state))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
func refStorageStr(storage ast.RefStorage) string {
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
func printDecl(w io.Writer, d ast.Decl, level int) {
	prefix := ind(level)
	switch n := d.(type) {
	case *ast.PermissionDecl:
		fmt.Fprintf(w, "%spermission %s: (%d members)\n", prefix, n.Name, len(n.Members))
	case *ast.EffectsDecl:
		parts := make([]string, 0, 2)
		if n.ErrorEffects != nil {
			parts = append(parts, typeStr(n.ErrorEffects))
		}
		if can := formatPermissionRefs(n.Permissions); can != "" {
			parts = append(parts, strings.TrimSpace(can))
		}
		fmt.Fprintf(w, "%seffectalias %s = %s\n", prefix, n.Name, strings.Join(parts, " "))
	case *ast.NamespaceDecl:
		keyword := "namespace"
		if n.Module {
			keyword = "module"
		}
		if n.Const && n.Module {
			keyword = "const module"
		}
		fmt.Fprintf(w, "%s%s %s: (%d decls)\n", prefix, keyword, n.Name, len(n.Decls))
		for _, decl := range n.Decls {
			printDecl(w, decl, level+1)
		}
	case *ast.UsingDecl:
		fmt.Fprintf(w, "%susing %s\n", prefix, n.Name)
	case *ast.ConstDecl:
		fmt.Fprintf(w, "%sconst %s = %s\n", prefix, n.Name, exprStr(n.Value))
	case *ast.TokenSetDecl:
		fmt.Fprintf(w, "%stokenset %s = %s\n", prefix, n.Name, exprStr(n.Value))
	case *ast.CharsetDecl:
		fmt.Fprintf(w, "%scharset %s: (%d terms)\n", prefix, n.Name, len(n.Terms))
	case *ast.KeywordMapDecl:
		fmt.Fprintf(w, "%skeywordmap %s: (%d entries)\n", prefix, n.Name, len(n.Entries))
	case *ast.ConstEnumDecl:
		fmt.Fprintf(w, "%sconst enum %s of %s: (%d members)\n", prefix, n.Name, typeStr(n.Storage), len(n.Members))
	case *ast.GlobalDecl:
		mut := ""
		if n.Mutable {
			mut = "mutable "
		}
		fmt.Fprintf(w, "%sglobal %s%s: %s\n", prefix, mut, n.Name, typeStr(n.Type))
	case *ast.TreeDecl:
		for _, annotation := range n.Annotations {
			fmt.Fprintf(w, "%s%s\n", prefix, formatAnnotation(annotation))
		}
		fmt.Fprintf(w, "%stree %s: (%d common fields, %d members)\n", prefix, n.Name, len(n.Common), len(n.Members))
		for _, member := range n.Members {
			printTreeMember(w, member, level+1)
		}
	case *ast.GrammarEnvDecl:
		fmt.Fprintf(w, "%sgrammarenv %s\n", prefix, n.Name)
	case *ast.StructDecl:
		affine := ""
		if n.Affine {
			affine = "affine "
		}
		layoutPrefix := ""
		switch n.Layout {
		case ast.StructLayoutAOS:
			layoutPrefix = "layout aos "
		case ast.StructLayoutSOA:
			layoutPrefix = "layout soa "
		}
		regionParams := n.RegionParams
		if n.RegionOwner != "" {
			regionParams = nil
		}
		tparams := formatFuncGenericParams(n.GenericParams, n.TypeParams, n.RefStorageParams, regionParams, nil)
		if n.RegionOwner != "" {
			tparams += " in " + n.RegionOwner
		}
		stateParamCount := n.StateParamCount
		if stateParamCount == 0 && n.HasStateParam {
			stateParamCount = 1
		}
		if stateParamCount > 0 {
			tparams += aggregateStatePlaceholders(stateParamCount)
		}
		fmt.Fprintf(w, "%s%s%sstruct %s%s (%d fields)\n", prefix, affine, layoutPrefix, n.Name, tparams, len(n.Fields))
	case *ast.InterfaceDecl:
		kind := "static interface"
		if n.Protocol {
			kind = "protocol"
		}
		fmt.Fprintf(w, "%s%s %s: (%d members)\n", prefix, kind, n.Name, len(n.Members))
	case *ast.ImplDecl:
		for _, annotation := range n.Annotations {
			fmt.Fprintf(w, "%s%s\n", prefix, formatAnnotation(annotation))
		}
		if n.IsExtension() {
			fmt.Fprintf(w, "%simpl %s: (%d members)\n", prefix, typeStr(n.ForType), len(n.Members))
		} else {
			fmt.Fprintf(w, "%simpl %s for %s: (%d members)\n", prefix, n.InterfaceName, typeStr(n.ForType), len(n.Members))
		}
	case *ast.FuncDecl:
		for _, annotation := range n.Annotations {
			fmt.Fprintf(w, "%s%s\n", prefix, formatAnnotation(annotation))
		}
		tparams := formatFuncGenericParams(n.GenericParams, n.TypeParams, n.RefStorageParams, n.RegionParams, n.PermissionParams)
		ret := ""
		if n.ReturnType != nil {
			ret = " -> " + typeStr(n.ReturnType)
		}
		fmt.Fprintf(w, "%sdef %s%s(%d params)%s%s%s (%d stmts)\n", prefix, n.Name, tparams, len(n.Params), ret, formatMainEffects(n.EffectAlias, n.Effects), formatMainPermissionRefs(n.EffectAlias, n.Effects, n.Permissions), len(n.Body))
	case *ast.ExternFuncDecl:
		for _, annotation := range n.Annotations {
			fmt.Fprintf(w, "%s%s\n", prefix, formatAnnotation(annotation))
		}
		tparams := formatFuncGenericParams(n.GenericParams, n.TypeParams, n.RefStorageParams, n.RegionParams, n.PermissionParams)
		ret := ""
		if n.ReturnType != nil {
			ret = " -> " + typeStr(n.ReturnType)
		}
		fmt.Fprintf(w, "%sextern %s%s(%d params)%s%s%s\n", prefix, n.Name, tparams, len(n.Params), ret, formatMainEffects(n.EffectAlias, n.Effects), formatMainPermissionRefs(n.EffectAlias, n.Effects, n.Permissions))
	case *ast.ExternVarDecl:
		fmt.Fprintf(w, "%sextern %s: %s\n", prefix, n.Name, typeStr(n.Type))
	case *ast.ExternTypeDecl:
		fmt.Fprintf(w, "%sextern type %s\n", prefix, n.Name)
	case *ast.TypeAliasDecl:
		fmt.Fprintf(w, "%stype %s = %s\n", prefix, n.Name, typeStr(n.Target))
	case *ast.ExportTypeDecl:
		fmt.Fprintf(w, "%sexport type %s as %s\n", prefix, typeStr(n.ExportedType), n.Alias)
	case *ast.ExportFuncDecl:
		ret := ""
		if n.ReturnType != nil {
			ret = " -> " + typeStr(n.ReturnType)
		}
		target := n.TargetName
		if len(n.TargetTypeArgs) > 0 {
			parts := make([]string, 0, len(n.TargetTypeArgs))
			for _, arg := range n.TargetTypeArgs {
				parts = append(parts, typeStr(arg))
			}
			target += "[" + strings.Join(parts, ", ") + "]"
		}
		fmt.Fprintf(w, "%sexport func %s(%d params)%s = %s\n", prefix, n.Name, len(n.Params), ret, target)
	case *ast.ExportGlobalDecl:
		fmt.Fprintf(w, "%sexport global %s as %s\n", prefix, n.TargetName, n.Alias)
	case *ast.StaticIfDecl:
		fmt.Fprintf(w, "%sstatic if %s: (%d then, %d elifs)\n", prefix, exprStr(n.Cond), len(n.Then), len(n.Elifs))
		for _, then := range n.Then {
			printDecl(w, then, level+1)
		}
	case *ast.StaticAssertDecl:
		fmt.Fprintf(w, "%sstatic assert %s\n", prefix, exprStr(n.Cond))
	case *ast.StaticAssertBlockDecl:
		fmt.Fprintf(w, "%sstatic assert:\n", prefix)
		for _, item := range n.Assertions {
			fmt.Fprintf(w, "%s    %s\n", prefix, exprStr(item.Cond))
		}
	case *ast.StaticGenerateDecl:
		fmt.Fprintf(w, "%sstatic generate: (%d statement(s))\n", prefix, len(n.Body))
	}
}
func printTreeMember(w io.Writer, member ast.TreeMemberDecl, level int) {
	prefix := ind(level)
	switch n := member.(type) {
	case *ast.TreeCategoryDecl:
		for _, annotation := range n.Annotations {
			fmt.Fprintf(w, "%s%s\n", prefix, formatAnnotation(annotation))
		}
		fmt.Fprintf(w, "%snode %s: (%d variants, %d nested)\n", prefix, treeCategoryPrintName(n.Name), len(n.Variants), len(n.Nested))
		for i := range n.Nested {
			printTreeMember(w, &n.Nested[i], level+1)
		}
	case *ast.TreeBlockDecl:
		for _, annotation := range n.Annotations {
			fmt.Fprintf(w, "%s%s\n", prefix, formatAnnotation(annotation))
		}
		fmt.Fprintf(w, "%sblock %s: (%d fields)\n", prefix, n.Name, len(n.Fields))
	case *ast.TreeStructDecl:
		for _, annotation := range n.Annotations {
			fmt.Fprintf(w, "%s%s\n", prefix, formatAnnotation(annotation))
		}
		fmt.Fprintf(w, "%sstruct %s: (%d fields)\n", prefix, n.Name, len(n.Fields))
	}
}
func treeCategoryPrintName(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 && idx+1 < len(name) {
		return name[idx+1:]
	}
	return name
}
func typeStr(t ast.TypeExpr) string {
	if t == nil {
		return "<nil>"
	}
	switch n := t.(type) {
	case *ast.NamedType:
		return n.Name
	case *ast.RefType:
		s := typeStr(n.Elem)
		prefix := ""
		if n.StorageParam != "" {
			prefix = n.StorageParam + " "
		} else if n.Region != "" {
			prefix = n.Region + " "
		} else if n.Storage != ast.RefStorageAny {
			switch n.Storage {
			case ast.RefStorageHeap:
				prefix = "heap "
			case ast.RefStorageStack:
				prefix = "stack "
			case ast.RefStorageStatic:
				prefix = "static "
			}
		}
		switch n.State {
		case ast.RefStateNullable:
			return prefix + s + "&?"
		case ast.RefStateNull:
			return prefix + s + "!"
		default:
			return prefix + s + "&"
		}
	case *ast.GenericType:
		var args []string
		for _, a := range n.Args {
			args = append(args, typeStr(a))
		}
		return n.Name + "[" + strings.Join(args, ", ") + "]"
	case *ast.GenericValueArgTypeExpr:
		return exprStr(n.Value)
	case *ast.AggregateStateTypeExpr:
		return typeStr(n.Base) + aggregateStateExprMarkers(n.States, n.State)
	case *ast.RefStateLiteralTypeExpr:
		return ast.RefStateMarker(n.State)
	case *ast.RefStorageLiteralTypeExpr:
		return refStorageStr(n.Storage)
	case *ast.MutableType:
		return "mutable " + typeStr(n.Elem)
	case *ast.TailType:
		return "tail " + typeStr(n.Elem)
	case *ast.ArrayType:
		return typeStr(n.Elem) + "[" + exprStr(n.Size) + "]"
	case *ast.BuiltinTypeExpr:
		parts := make([]string, 0, len(n.TypeArgs)+len(n.ValueArgs))
		for _, arg := range n.TypeArgs {
			parts = append(parts, typeStr(arg))
		}
		for _, arg := range n.ValueArgs {
			parts = append(parts, exprStr(arg))
		}
		return n.Name + "[" + strings.Join(parts, ", ") + "]"
	case *ast.FuncTypeExpr:
		parts := make([]string, 0, len(n.Params))
		for _, param := range n.Params {
			parts = append(parts, typeStr(param))
		}
		if n.Variadic {
			parts = append(parts, "...")
		}
		ret := ""
		withClause := formatMainWithSignatureClause(n.ImplicitBundles, n.ImplicitParams)
		if n.Return != nil {
			ret = " -> " + typeStr(n.Return)
		}
		return "func(" + strings.Join(parts, ", ") + ")" + withClause + ret + formatMainEffects(n.EffectAlias, n.Effects) + formatMainPermissionRefs(n.EffectAlias, n.Effects, n.Permissions)
	case *ast.ErrorSetExpr:
		parts := make([]string, 0, len(n.Tags)+1)
		for _, tag := range n.Tags {
			if tag.Tag == "" {
				parts = append(parts, tag.SetName)
				continue
			}
			parts = append(parts, tag.SetName+"."+tag.Tag)
		}
		if n.HasEllipsis {
			parts = append(parts, "...")
		}
		return "error[" + strings.Join(parts, ", ") + "]"
	case *ast.ErrorUnionTypeExpr:
		return typeStr(n.Value) + " " + typeStr(n.Errors)
	case *ast.OptionalTypeExpr:
		return typeStr(n.Value) + "?"
	default:
		return "<type>"
	}
}
func formatMainParamDecl(param ast.ParamDecl) string {
	line := ""
	if param.Mutable {
		line += "mutable "
	}
	line += param.Name + ": " + typeStr(param.Type)
	return line
}
func formatMainWithSignatureClause(bundles []string, params []ast.ParamDecl) string {
	parts := make([]string, 0, len(bundles)+len(params))
	parts = append(parts, bundles...)
	for _, param := range params {
		parts = append(parts, formatMainParamDecl(param))
	}
	if len(parts) == 0 {
		return ""
	}
	return " with " + strings.Join(parts, ", ")
}
func formatMainEffects(alias string, effects []ast.SignatureEffectItem) string {
	if len(effects) == 0 {
		if alias == "" {
			return ""
		}
		return " effects[" + alias + "]"
	}
	parts := make([]string, 0, len(effects)+1)
	if alias != "" {
		parts = append(parts, alias)
	}
	for _, effect := range effects {
		if effect.ErrorEffects != nil {
			parts = append(parts, typeStr(effect.ErrorEffects))
			continue
		}
		if effect.Permission != nil {
			parts = append(parts, formatPermissionRef(*effect.Permission))
			continue
		}
		if effect.Alias != "" {
			parts = append(parts, effect.Alias)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " effects[" + strings.Join(parts, ", ") + "]"
}
func formatMainPermissionRefs(effectAlias string, effects []ast.SignatureEffectItem, permissions []ast.PermissionRef) string {
	if effectAlias != "" || len(effects) != 0 {
		return ""
	}
	return formatPermissionRefs(permissions)
}
func formatMainWithValueClause(bundles []ast.WithBundleUse, args []ast.WithArg) string {
	parts := make([]string, 0, len(bundles)+len(args))
	for _, bundle := range bundles {
		bundleParts := make([]string, 0, len(bundle.Args)+1)
		if bundle.Spread {
			bundleParts = append(bundleParts, "..")
		}
		for _, arg := range bundle.Args {
			bundleParts = append(bundleParts, arg.Name+" = "+exprStr(arg.Value))
		}
		parts = append(parts, bundle.Name+"("+strings.Join(bundleParts, ", ")+")")
	}
	for _, arg := range args {
		if arg.Shorthand {
			parts = append(parts, arg.Name)
			continue
		}
		parts = append(parts, arg.Name+" = "+exprStr(arg.Value))
	}
	return strings.Join(parts, ", ")
}
func exprStr(e ast.Expr) string {
	if e == nil {
		return "<nil>"
	}
	switch n := e.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.IntLit:
		s := n.Value
		if n.Suffix != "" {
			s += n.Suffix
		}
		return s
	case *ast.StringLit:
		return fmt.Sprintf("%q", n.Value)
	case *ast.CharLit:
		if len(n.Value) == 1 {
			return strconv.QuoteRuneToASCII(rune(n.Value[0]))
		}
		return "'<invalid-char>'"
	case *ast.BoolLit:
		if n.Value {
			return "true"
		}
		return "false"
	case *ast.NullLit:
		return "null"
	case *ast.ZeroedLit:
		return "zeroed"
	case *ast.ListLitExpr:
		var elems []string
		for _, elem := range n.Elems {
			elems = append(elems, exprStr(elem))
		}
		if n.Brace {
			return fmt.Sprintf("{%s}", strings.Join(elems, ", "))
		}
		return fmt.Sprintf("[%s]", strings.Join(elems, ", "))
	case *ast.BinaryExpr:
		return fmt.Sprintf("(%s %s %s)", exprStr(n.Left), lexer.TokenName(n.Op), exprStr(n.Right))
	case *ast.UnaryExpr:
		return fmt.Sprintf("(%s %s)", lexer.TokenName(n.Op), exprStr(n.Operand))
	case *ast.CallExpr:
		var args []string
		for i, a := range n.Args {
			if name := n.ArgName(i); name != "" {
				args = append(args, name+": "+exprStr(a))
				continue
			}
			args = append(args, exprStr(a))
		}
		funcText := exprStr(n.Func)
		if n.Safe && n.SafeReceiver != nil {
			funcText = fmt.Sprintf("%s?.(%s)", exprStr(n.SafeReceiver), exprStr(n.Func))
			if len(n.Args) == 0 {
				return funcText
			}
		}
		line := fmt.Sprintf("%s(%s)", funcText, strings.Join(args, ", "))
		if len(n.WithArgs) != 0 || len(n.WithBundles) != 0 {
			line += " with " + formatMainWithValueClause(n.WithBundles, n.WithArgs)
		}
		return line
	case *ast.AllocExpr:
		if n.Owner == nil {
			return fmt.Sprintf("new %s", exprStr(n.Value))
		}
		return fmt.Sprintf("new[%s] %s", exprStr(n.Owner), exprStr(n.Value))
	case *ast.FieldExpr:
		return fmt.Sprintf("%s.%s", exprStr(n.Object), n.Field)
	case *ast.IndexExpr:
		line := fmt.Sprintf("%s[%s]", exprStr(n.Object), exprStr(n.Index))
		if n.Fallback != nil {
			line += " else " + exprStr(n.Fallback)
		}
		return line
	case *ast.SliceExpr:
		return fmt.Sprintf("%s[%s:%s]", exprStr(n.Object), exprStr(n.Start), exprStr(n.End))
	case *ast.CastExpr:
		if n.Origin == ast.CastExprOriginPostfixShorthand {
			if target, ok := formatPostfixShorthandCastTarget(n.Target); ok {
				return fmt.Sprintf("%s.%s()", exprStr(n.Operand), target)
			}
		}
		if n.Origin == ast.CastExprOriginToSyntax || n.Origin == ast.CastExprOriginAsSyntax {
			return fmt.Sprintf("%s as %s", exprStr(n.Operand), typeStr(n.Target))
		}
		return fmt.Sprintf("%s.cast[%s]", exprStr(n.Operand), typeStr(n.Target))
	case *ast.CascadeExpr:
		return fmt.Sprintf("cascade %s => %s", exprStr(n.Target), exprStr(n.Value))
	case *ast.CatchExpr:
		lines := []string{fmt.Sprintf("catch %s:", exprStr(n.Value))}
		formatArm := func(arm ast.CatchArm) {
			prefix := ""
			if arm.ErrorBinding {
				prefix = "error "
			}
			lines = append(lines, "    "+prefix+arm.Name+":")
			for _, stmt := range arm.Body {
				lines = append(lines, "        "+strings.ReplaceAll(unparse.FormatStmt(stmt), "\n", "\n        "))
			}
		}
		formatArm(n.Success)
		for _, arm := range n.Arms {
			formatArm(arm)
		}
		return strings.Join(lines, "\n")
	case *ast.LambdaExpr:
		keyword := n.Keyword
		if keyword == "" {
			keyword = "lambda"
		}
		params := make([]string, 0, len(n.Params))
		if n.UsesShorthandParams {
			for _, param := range n.Params {
				params = append(params, param.Name)
			}
		} else {
			for _, param := range n.Params {
				part := ""
				if param.Mutable {
					part += "mutable "
				}
				part += param.Name + ": " + typeStr(param.Type)
				params = append(params, part)
			}
		}
		line := keyword + " "
		if n.UsesShorthandParams {
			line += strings.Join(params, ", ")
		} else {
			line += "(" + strings.Join(params, ", ") + ")"
		}
		if n.ReturnType != nil {
			line += " -> " + typeStr(n.ReturnType)
		}
		if n.BodyExpr != nil {
			return line + ": " + exprStr(n.BodyExpr)
		}
		return line + ": ..."
	case *ast.SizeofExpr:
		return fmt.Sprintf("size_of(%s)", typeStr(n.Type))
	case *ast.AlignofExpr:
		return fmt.Sprintf("align_of(%s)", typeStr(n.Type))
	case *ast.OffsetofExpr:
		return fmt.Sprintf("offset_of(%s, %s)", typeStr(n.Type), n.Field)
	case *ast.TernaryExpr:
		return fmt.Sprintf("(%s if %s else %s)", exprStr(n.Value), exprStr(n.Cond), exprStr(n.Alt))
	case *ast.AddrOfExpr:
		return fmt.Sprintf("&%s", exprStr(n.Operand))
	case *ast.MoveExpr:
		return fmt.Sprintf("move %s", exprStr(n.Operand))
	case *ast.SpecializeExpr:
		typeArgs := make([]string, 0, len(n.TypeArgs))
		for _, arg := range n.TypeArgs {
			typeArgs = append(typeArgs, typeStr(arg))
		}
		return fmt.Sprintf("%s[%s]", exprStr(n.Operand), strings.Join(typeArgs, ", "))
	case *ast.StructLitExpr:
		var args []string
		for i, a := range n.Args {
			part := exprStr(a)
			if name := n.ArgName(i); name != "" {
				part = fmt.Sprintf("%s: %s", name, part)
			}
			args = append(args, part)
		}
		if n.Brace {
			return fmt.Sprintf("%s{%s}", n.Name, strings.Join(args, ", "))
		}
		return fmt.Sprintf("%s(%s)", n.Name, strings.Join(args, ", "))
	case *ast.RecordUpdateExpr:
		var args []string
		for i, a := range n.Args {
			part := exprStr(a)
			if name := n.ArgName(i); name != "" {
				part = fmt.Sprintf("%s = %s", name, part)
			}
			args = append(args, part)
		}
		return fmt.Sprintf("%s{%s}", exprStr(n.Base), strings.Join(args, ", "))
	case *ast.ParenExpr:
		return fmt.Sprintf("(%s)", exprStr(n.Inner))
	case *ast.OptionalBindExpr:
		return fmt.Sprintf("let %s = %s", n.Name, exprStr(n.Value))
	case *ast.CanExpr:
		return exprStr(n.Expr) + formatPermissionRefs(n.Permissions)
	default:
		return "<expr>"
	}
}
