package unparse

import (
	"elisacore/src/ast"
	"strings"
)

func unparseCallName(call *ast.CallExpr) string {
	if call == nil {
		return ""
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil {
		return ""
	}
	return ident.Name
}

func iterForWhereSource(expr ast.Expr) (ast.Expr, ast.Expr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil || len(call.Args) != 2 {
		return nil, nil, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident.Name != "where" {
		return nil, nil, false
	}
	return call.Args[0], call.Args[1], true
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
	args := make([]string, len(annotation.Args))
	for i, arg := range annotation.Args {
		args[i] = formatAnnotationArg(arg)
	}
	return "@" + annotation.Name + "(" + strings.Join(args, ", ") + ")"
}

// formatAnnotationArg re-quotes an annotation argument that needs it so the rendered form
// round-trips through the parser. Bare single-token args (e.g. `winapi`, `pthread.h`) and
// already-quoted string literals are left as-is; an arg containing whitespace (e.g. a
// `@deprecated("...")` message) is wrapped in a string literal.
func formatAnnotationArg(arg string) string {
	if len(arg) >= 2 && strings.HasPrefix(arg, "\"") && strings.HasSuffix(arg, "\"") {
		return arg
	}
	if strings.ContainsAny(arg, " \t") {
		return "\"" + strings.ReplaceAll(arg, "\"", "\\\"") + "\""
	}
	return arg
}
func formatGenericParams(genericParams []ast.GenericParam, typeParams []string, regionParams []string, permissionParams []string) string {
	parts := make([]string, 0, len(genericParams)+len(typeParams)+len(regionParams)+len(permissionParams))
	if len(genericParams) != 0 {
		seenRegion := map[string]bool{}
		seenPermission := map[string]bool{}
		for _, param := range genericParams {
			switch param.Kind {
			case ast.GenericParamRegion:
				seenRegion[param.Name] = true
				parts = append(parts, "@"+param.Name)
			case ast.GenericParamPermission:
				seenPermission[param.Name] = true
				parts = append(parts, "permission "+param.Name)
			default:
				if param.InterfaceBound != "" {
					parts = append(parts, param.Name+": "+ast.ModulePathSpelling(param.InterfaceBound))
				} else {
					parts = append(parts, param.Name)
				}
			}
		}
		for _, name := range regionParams {
			if !seenRegion[name] {
				parts = append(parts, "@"+name)
			}
		}
		for _, name := range permissionParams {
			if !seenPermission[name] {
				parts = append(parts, "permission "+name)
			}
		}
	} else {
		parts = append(parts, typeParams...)
		for _, name := range regionParams {
			parts = append(parts, "@"+name)
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
func formatFuncHeader(name string, genericParams []ast.GenericParam, typeParams []string, regionParams []string, permissionParams []string, params []ast.ParamDecl, retType ast.TypeExpr, permissions []ast.PermissionRef, ensures []ast.EnsuresClause, variadic bool) string {
	line := formatImplMethodHeader(name, genericParams, typeParams, regionParams, permissionParams, params, retType, permissions, ensures, variadic)
	line += ":"
	return line
}
func formatImplMethodHeader(name string, genericParams []ast.GenericParam, typeParams []string, regionParams []string, permissionParams []string, params []ast.ParamDecl, retType ast.TypeExpr, permissions []ast.PermissionRef, ensures []ast.EnsuresClause, variadic bool) string {
	line := "def " + name
	line += formatGenericParams(genericParams, typeParams, regionParams, permissionParams)
	line += "(" + formatExplicitParamList(params, variadic) + ")"
	if retType != nil {
		line += " -> " + formatTypeExpr(retType)
	}
	line += formatPermissionRefs(permissions)
	line += formatEnsuresClauses(ensures)
	return line
}
func formatExternFuncHeader(name string, genericParams []ast.GenericParam, typeParams []string, regionParams []string, permissionParams []string, params []ast.ParamDecl, retType ast.TypeExpr, permissions []ast.PermissionRef, ensures []ast.EnsuresClause, variadic bool) string {
	line := "extern " + name
	line += formatGenericParams(genericParams, typeParams, regionParams, permissionParams)
	line += "(" + formatExplicitParamList(params, variadic) + ")"
	if retType != nil {
		line += " -> " + formatTypeExpr(retType)
	}
	line += formatPermissionRefs(permissions)
	line += formatEnsuresClauses(ensures)
	return line
}

// formatExportTargetName renders an export target's internal dotted qualified
// name back to source `::` form so the unparsed text round-trips (the parser
// consumes `::`, not `.`, in export-target position).
func formatExportTargetName(name string) string {
	return strings.ReplaceAll(name, ".", "::")
}

func formatExportFuncHeader(n *ast.ExportFuncDecl) string {
	line := "export fn " + n.Name + "(" + formatExplicitParamList(n.Params, false) + ")"
	if n.ReturnType != nil {
		line += " -> " + formatTypeExpr(n.ReturnType)
	}
	line += " = " + formatExportTargetName(n.TargetName)
	if len(n.TargetTypeArgs) > 0 {
		parts := make([]string, 0, len(n.TargetTypeArgs))
		for _, arg := range n.TargetTypeArgs {
			parts = append(parts, formatTypeExpr(arg))
		}
		line += "[" + strings.Join(parts, ", ") + "]"
	}
	return line
}
func formatExplicitParamList(params []ast.ParamDecl, variadic bool) string {
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
func formatParamList(params []ast.ParamDecl) string {
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, formatParamDecl(param))
	}
	return strings.Join(parts, ", ")
}
