package semantic

import (
	"strings"

	"elisacore/src/ast"
)

type scopedDecl struct {
	Decl      ast.Decl
	Namespace string
	Usings    []string
}

func joinQualifiedName(namespace string, name string) string {
	if namespace == "" {
		return name
	}
	if name == "" {
		return namespace
	}
	return namespace + "." + name
}

func dedupeQualifiedNames(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (a *Analyzer) flattenScopedDecls(decls []ast.Decl, namespace string, inheritedUsings []string) []scopedDecl {
	blockUsings := make([]string, 0)
	for _, decl := range decls {
		usingDecl, ok := decl.(*ast.UsingDecl)
		if !ok {
			continue
		}
		blockUsings = append(blockUsings, joinQualifiedName(namespace, usingDecl.Name))
	}
	effectiveUsings := dedupeQualifiedNames(append(append([]string(nil), inheritedUsings...), blockUsings...))
	out := make([]scopedDecl, 0, len(decls))
	for _, decl := range decls {
		switch n := decl.(type) {
		case *ast.StaticIfDecl:
			out = append(out, a.flattenScopedDecls(a.activeDeclBranch(n), namespace, effectiveUsings)...)
		case *ast.NamespaceDecl:
			childNamespace := joinQualifiedName(namespace, n.Name)
			out = append(out, a.flattenScopedDecls(n.Decls, childNamespace, effectiveUsings)...)
		case *ast.UsingDecl:
			continue
		default:
			a.primeScopedConstValues(decl, namespace, effectiveUsings)
			out = append(out, scopedDecl{Decl: decl, Namespace: namespace, Usings: append([]string(nil), effectiveUsings...)})
		}
	}
	return out
}

func (a *Analyzer) primeScopedConstValues(decl ast.Decl, namespace string, usings []string) {
	if a == nil || decl == nil {
		return
	}
	a.withResolutionContext(namespace, usings, func() {
		switch n := decl.(type) {
		case *ast.ConstDecl:
			if value, ok := a.evalConstExpr(n.Value); ok {
				a.constValues[joinQualifiedName(namespace, n.Name)] = value
			}
		case *ast.ConstEnumDecl:
			nextValue := int64(0)
			qualifiedName := joinQualifiedName(namespace, n.Name)
			for i := range n.Members {
				member := &n.Members[i]
				value := nextValue
				if member.Value != nil {
					resolved, ok := a.evalConstExpr(member.Value)
					if !ok || resolved.Kind != ConstInt {
						continue
					}
					value = resolved.Int
				}
				a.constValues[qualifiedName+"."+member.Name] = ConstValue{Kind: ConstInt, Int: value}
				nextValue = value + 1
			}
		}
	})
}

func (a *Analyzer) withResolutionContext(namespace string, usings []string, fn func()) {
	savedNamespace := a.currentNamespace
	savedUsings := a.currentUsings
	a.currentNamespace = namespace
	a.currentUsings = append([]string(nil), usings...)
	fn()
	a.currentNamespace = savedNamespace
	a.currentUsings = savedUsings
}

func (a *Analyzer) visibleNameCandidates(name string) []string {
	if name == "" {
		return nil
	}
	candidates := make([]string, 0, len(a.currentUsings)+2)
	if a.currentNamespace != "" {
		candidates = append(candidates, joinQualifiedName(a.currentNamespace, name))
	}
	if !strings.Contains(name, ".") {
		for _, usingName := range a.currentUsings {
			candidates = append(candidates, joinQualifiedName(usingName, name))
		}
	}
	candidates = append(candidates, name)
	return dedupeQualifiedNames(candidates)
}

func (a *Analyzer) lookupVisibleType(name string) (Type, string, bool) {
	for _, candidate := range a.visibleNameCandidates(name) {
		if t, ok := a.namedTypes[candidate]; ok {
			return t, candidate, true
		}
	}
	return nil, "", false
}

func (a *Analyzer) lookupVisiblePermission(name string) (*PermissionSet, string, bool) {
	for _, candidate := range a.visibleNameCandidates(name) {
		if permission, ok := a.permissions[candidate]; ok {
			return permission, candidate, true
		}
	}
	return nil, "", false
}

func (a *Analyzer) lookupVisibleContextBundle(name string) (*ContextBundle, string, bool) {
	for _, candidate := range a.visibleNameCandidates(name) {
		if bundle, ok := a.contextBundles[candidate]; ok {
			return bundle, candidate, true
		}
	}
	return nil, "", false
}

func (a *Analyzer) lookupVisibleParamPack(name string) (*ParamPack, string, bool) {
	if a != nil && !strings.Contains(name, ".") && len(a.currentLocalParamPackScopes) != 0 {
		frame := a.currentLocalParamPackScopes[len(a.currentLocalParamPackScopes)-1]
		if pack, ok := frame[name]; ok {
			return pack, name, true
		}
	}
	for _, candidate := range a.visibleNameCandidates(name) {
		if pack, ok := a.paramPacks[candidate]; ok {
			return pack, candidate, true
		}
	}
	return nil, "", false
}

func (a *Analyzer) lookupVisibleGlobal(name string) (*Symbol, string, bool) {
	for _, candidate := range a.visibleNameCandidates(name) {
		if sym, ok := a.globalScope.Lookup(candidate); ok {
			return sym, candidate, true
		}
	}
	return nil, "", false
}

func (a *Analyzer) lookupVisibleConst(name string) (ConstValue, bool) {
	for _, candidate := range a.visibleNameCandidates(name) {
		if value, ok := a.constValues[candidate]; ok {
			return value, true
		}
	}
	return ConstValue{}, false
}
