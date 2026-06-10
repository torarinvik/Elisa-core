package unparse

import (
	"elisacore/src/ast"
	"strings"
)

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
func formatNamedArg(arg ast.WithArg) string {
	if arg.Shorthand {
		return arg.Name + ":"
	}
	if arg.Value == nil {
		return arg.Name + ":"
	}
	return arg.Name + ": " + formatExpr(arg.Value)
}
func formatSignatureParamPackUse(pack ast.ParamPackUse) string {
	return "use " + pack.Name
}
func formatValueParamPackUse(pack ast.ParamPackUse) string {
	if pack.Bare && len(pack.Args) == 0 {
		return "use " + pack.Name
	}
	parts := make([]string, 0, len(pack.Args))
	for _, arg := range pack.Args {
		parts = append(parts, formatNamedArg(arg))
	}
	return "use " + pack.Name + "(" + strings.Join(parts, ", ") + ")"
}
func formatWithBundleUse(bundle ast.WithBundleUse) string {
	parts := make([]string, 0, len(bundle.Args)+1)
	if bundle.Spread {
		parts = append(parts, "..")
	}
	for _, arg := range bundle.Args {
		if arg.Shorthand {
			parts = append(parts, arg.Name+":")
			continue
		}
		parts = append(parts, arg.Name+" = "+formatExpr(arg.Value))
	}
	return bundle.Name + "(" + strings.Join(parts, ", ") + ")"
}
func formatNodeSugarValue(expr *ast.AllocExpr) string {
	if expr == nil || expr.NodeSpan == nil {
		return formatExpr(expr.Value)
	}
	call, ok := expr.Value.(*ast.CallExpr)
	if !ok || call == nil || len(call.Args) == 0 || call.ArgName(len(call.Args)-1) != "span" {
		return formatExpr(expr.Value)
	}
	trimmed := *call
	trimmed.Args = append([]ast.Expr(nil), call.Args[:len(call.Args)-1]...)
	if len(call.ArgNames) >= len(call.Args) {
		trimmed.ArgNames = append([]string(nil), call.ArgNames[:len(call.Args)-1]...)
	}
	if len(call.ArgShorthand) >= len(call.Args) {
		trimmed.ArgShorthand = append([]bool(nil), call.ArgShorthand[:len(call.Args)-1]...)
	}
	if len(call.ArgItemOrder) != 0 {
		items := make([]ast.CallArgItem, 0, len(call.ArgItemOrder))
		removedIndex := len(call.Args) - 1
		for _, item := range call.ArgItemOrder {
			if !item.IsPack && item.ArgIndex == removedIndex {
				continue
			}
			items = append(items, item)
		}
		trimmed.ArgItemOrder = items
	}
	if len(trimmed.Args) == 0 && len(trimmed.ParamPacks) == 0 && !trimmed.HasArgForward && len(trimmed.WithArgs) == 0 && len(trimmed.WithBundles) == 0 {
		return formatExpr(trimmed.Func)
	}
	return formatExpr(&trimmed)
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
func formatArgsScopeClause(packs []ast.ParamPackUse, args []ast.WithArg, order []ast.ArgsScopeItem) string {
	parts := make([]string, 0, len(packs)+len(args))
	if len(order) != 0 {
		for _, item := range order {
			if item.IsPack {
				parts = append(parts, formatValueParamPackUse(item.Pack))
				continue
			}
			parts = append(parts, formatNamedArg(item.Arg))
		}
		return strings.Join(parts, ", ")
	}
	for _, pack := range packs {
		parts = append(parts, formatValueParamPackUse(pack))
	}
	for _, arg := range args {
		parts = append(parts, formatNamedArg(arg))
	}
	return strings.Join(parts, ", ")
}
func formatPermissionRefs(refs []ast.PermissionRef) string {
	if len(refs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		parts = append(parts, formatPermissionRef(ref))
	}
	return " can[" + strings.Join(parts, ", ") + "]"
}
func formatPermissionRef(ref ast.PermissionRef) string {
	if ref.Member != "" {
		return ref.Name + "." + ref.Member
	}
	return ref.Name
}
func formatPermissionRefSurfaceList(refs []ast.PermissionRef) string {
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
	return strings.Join(parts, ", ")
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
		// Storage classes (heap/static/stack) stay as prefixes; region provenance
		// is emitted as the canonical `@r` suffix (docs/68 §5).
		prefix := ""
		if n.Explicit && n.Storage != ast.RefStorageAny {
			prefix = formatRefStorage(n.Storage) + " "
		}
		suffix := "&"
		switch n.State {
		case ast.RefStateNullable:
			suffix = "&?"
		case ast.RefStateNull:
			suffix = "!"
		}
		region := ""
		if n.Region != "" {
			region = " @" + n.Region
		}
		return prefix + formatTypeExpr(n.Elem) + suffix + region
	case *ast.RefStateLiteralTypeExpr:
		return ast.RefStateMarker(n.State)
	case *ast.RefStorageLiteralTypeExpr:
		return formatRefStorage(n.Storage)
	case *ast.GenericType:
		parts := make([]string, 0, len(n.Args))
		for _, arg := range n.Args {
			parts = append(parts, formatTypeExpr(arg))
		}
		result := n.Name + "[" + strings.Join(parts, ", ") + "]"
		if n.Region != "" {
			result += " @" + n.Region
		}
		return result
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
		parts := make([]string, 0, len(n.TypeArgs)+len(n.ValueArgs))
		for _, arg := range n.TypeArgs {
			parts = append(parts, formatTypeExpr(arg))
		}
		for _, arg := range n.ValueArgs {
			parts = append(parts, formatExpr(arg))
		}
		result := n.Name + "[" + strings.Join(parts, ", ") + "]"
		// Region provenance on a container is part of its type and must round-trip
		// (docs/68 §5); previously it was dropped here, silently losing `@r`.
		if n.Region != "" {
			result += " @" + n.Region
		}
		return result
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
		return ""
	}
}
