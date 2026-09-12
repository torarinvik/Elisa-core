package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

// nameIsModulePath reports whether a dot-flattened key names a declared module,
// either absolutely, relative to the enclosing namespace chain, or through a
// `using M as A` alias.
func (a *Analyzer) nameIsModulePath(key string) bool {
	if a == nil || key == "" {
		return false
	}
	if a.declaredModules[key] {
		return true
	}
	if _, ok := a.moduleAliases[key]; ok {
		return true
	}
	namespace := a.currentNamespace
	for namespace != "" {
		if a.declaredModules[joinQualifiedName(namespace, key)] {
			return true
		}
		if i := strings.LastIndex(namespace, "."); i >= 0 {
			namespace = namespace[:i]
		} else {
			namespace = ""
		}
	}
	return false
}

// rejectDotModulePath enforces `::` as the only module-path separator in a TYPE
// name. spelling is the name as written (`Pack.Item`, `Pack::Kind.A`, `Expr.Store`);
// a `.` is legal only when its left side is a type (an enum whose variant is a
// first-class type), never when it is a module. Reports true when it errored.
func (a *Analyzer) rejectDotModulePath(pos lexer.Pos, spelling string) bool {
	// An empty Spelling is a synthesized node (a struct-literal type, a rewrite); only
	// a name parsed from source separators can be judged.
	if !strings.Contains(spelling, ".") {
		return false
	}
	// A parameter or field type is resolved more than once (signature collection, then
	// body analysis); the same position and spelling is the same defect.
	if a.reportedDotModuleTypes == nil {
		a.reportedDotModuleTypes = map[string]bool{}
	}
	reportKey := pos.String() + "|" + spelling
	if a.reportedDotModuleTypes[reportKey] {
		return true
	}
	segments := []string{}
	separators := []string{}
	rest := spelling
	for {
		dot := strings.Index(rest, ".")
		scope := strings.Index(rest, "::")
		if dot < 0 && scope < 0 {
			segments = append(segments, rest)
			break
		}
		if scope >= 0 && (dot < 0 || scope < dot) {
			segments = append(segments, rest[:scope])
			separators = append(separators, "::")
			rest = rest[scope+2:]
			continue
		}
		segments = append(segments, rest[:dot])
		separators = append(separators, ".")
		rest = rest[dot+1:]
	}
	offending := ""
	corrected := segments[0]
	key := segments[0]
	for i, sep := range separators {
		if sep == "." && a.nameIsModulePath(key) {
			if offending == "" {
				offending = key
			}
			sep = "::"
		}
		corrected += sep + segments[i+1]
		key += "." + segments[i+1]
	}
	if offending == "" {
		return false
	}
	a.reportedDotModuleTypes[reportKey] = true
	a.errorf(pos, "%q is a namespace; write %s (`.` accesses value members, `::` accesses namespaces)", offending, corrected)
	return true
}

// rejectDotModulePathExpr is the VALUE-position twin of rejectDotModulePath: a
// `Pack.Kind.A` / `Pack.Item{...}` chain whose leading segments name a module.
// It runs before the enum-variant / packed-tag / const-enum resolvers, which all
// flatten the chain to a dotted key and would otherwise accept the mis-spelled
// path silently. Reports true when it errored (one diagnostic, at the offending dot).
func (a *Analyzer) rejectDotModulePathExpr(expr *ast.FieldExpr) bool {
	if a == nil || expr == nil {
		return false
	}

	links := []*ast.FieldExpr{}
	var object ast.Expr = expr
	for {
		field, ok := object.(*ast.FieldExpr)
		if !ok {
			break
		}
		links = append([]*ast.FieldExpr{field}, links...)
		object = field.Object
	}
	head, ok := object.(*ast.Ident)
	if !ok || head == nil || head.Name == "" {
		return false
	}
	// Cheap map probes first; the scope lookup that rules out a shadowing local runs
	// only once a module prefix has actually matched (this arm sees every field access).
	key := head.Name
	shadowChecked := false
	for _, link := range links {
		if a.nameIsModulePath(key) {
			if !shadowChecked {
				shadowChecked = true
				if a.identNameResolvesAsValue(head.Name) {
					return false
				}
			}
			// Call analysis re-analyzes a callee expression on each resolution attempt;
			// report the mis-spelled path once per node.
			if a.reportedDotModulePaths == nil {
				a.reportedDotModulePaths = map[*ast.FieldExpr]bool{}
			}
			if a.reportedDotModulePaths[link] {
				return true
			}
			a.reportedDotModulePaths[link] = true
			a.errorf(link.Pos(), "%q is a namespace; write %s::%s (`.` accesses value members, `::` accesses namespaces)", ast.ModulePathSpelling(key), ast.ModulePathSpelling(key), link.Field)
			return true
		}
		key += "." + link.Field
	}
	return false
}

// rejectDotModulePathIsTarget applies the module-path spelling rule to an `is`
// target, which the parser hands over either as a value expression (`Pack.Kind.A`)
// or as a type expression (`Pack.Kind.A` parsed as a NamedType).
func (a *Analyzer) rejectDotModulePathIsTarget(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		if n == nil {
			return false
		}
		return a.rejectDotModulePathIsTarget(n.Inner)
	case *ast.IsAliasExpr:
		if n == nil {
			return false
		}
		return a.rejectDotModulePathIsTarget(n.Target)
	case *ast.FieldExpr:
		return a.rejectDotModulePathExpr(n)
	case *ast.CallExpr:
		// `s is Pack.Kind.A(payload)` -- the constructor-shaped target.
		if n == nil {
			return false
		}
		if field, ok := n.Func.(*ast.FieldExpr); ok {
			return a.rejectDotModulePathExpr(field)
		}
	case *ast.TypeExprExpr:
		if n == nil {
			return false
		}
		if named, ok := n.Type.(*ast.NamedType); ok && named != nil {
			return a.rejectDotModulePath(named.Pos(), named.Spelling)
		}
	}
	return false
}
