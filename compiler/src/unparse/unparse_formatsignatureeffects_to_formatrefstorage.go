package unparse

import (
	"elisacore/src/ast"
	"strings"
)

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
	case *ast.RefinementTypeExpr:
		parts := make([]string, 0, len(n.Preds))
		for _, pred := range n.Preds {
			part := pred.Name
			if len(pred.Args) != 0 {
				args := make([]string, 0, len(pred.Args))
				for _, arg := range pred.Args {
					args = append(args, formatExpr(arg))
				}
				part += "[" + strings.Join(args, ", ") + "]"
			}
			parts = append(parts, part)
		}
		return formatTypeExpr(n.Base) + " is " + strings.Join(parts, " and ")
	case *ast.WhereRefinementTypeExpr:
		return formatTypeExpr(n.Base) + " where " + formatExpr(n.Predicate)
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
		line := "fn(" + strings.Join(parts, ", ") + ")"
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
