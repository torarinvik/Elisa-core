package semantic

import (
	"strings"

	"elisacore/src/ast"
)

type scopedDecl struct {
	Decl      ast.Decl
	Namespace string
	Usings    []string
	Private   bool
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

func (a *Analyzer) declIsPrivate(decl ast.Decl, inheritedPrivate bool) bool {
	if a == nil || decl == nil {
		return inheritedPrivate
	}
	if ns, ok := decl.(*ast.NamespaceDecl); ok && ns.Private {
		return true
	}
	// An explicit per-decl mark (a `public`/`private` prefix or an enclosing
	// `public:`/`private:` section) wins over the enclosing module's default, so a
	// `public:` section inside a `private module` re-exports its members. Unmarked
	// decls inherit the module default (private module => private members).
	if a.declVisibility != nil {
		switch a.declVisibility[decl] {
		case "private":
			return true
		case "public":
			return false
		}
	}
	return inheritedPrivate
}

func (a *Analyzer) flattenScopedDecls(decls []ast.Decl, namespace string, inheritedUsings []string) []scopedDecl {
	return a.flattenScopedDeclsWithVisibility(decls, namespace, inheritedUsings, false)
}

func (a *Analyzer) flattenScopedDeclsWithVisibility(decls []ast.Decl, namespace string, inheritedUsings []string, inheritedPrivate bool) []scopedDecl {
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
			out = append(out, a.flattenScopedDeclsWithVisibility(a.activeDeclBranch(n), namespace, effectiveUsings, inheritedPrivate)...)
		case *ast.NamespaceDecl:
			childNamespace := joinQualifiedName(namespace, n.Name)
			childPrivate := a.declIsPrivate(n, inheritedPrivate)
			out = append(out, a.flattenScopedDeclsWithVisibility(n.Decls, childNamespace, effectiveUsings, childPrivate)...)
		case *ast.UsingDecl:
			continue
		case *ast.ImportDecl:
			a.registerImportAliases(n, namespace)
			continue
		default:
			a.primeScopedConstValues(decl, namespace, effectiveUsings)
			out = append(out, scopedDecl{Decl: decl, Namespace: namespace, Usings: append([]string(nil), effectiveUsings...), Private: a.declIsPrivate(decl, inheritedPrivate)})
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

// registerImportAliases records `from Module import a, b` so that the bare names
// resolve to Module.a / Module.b. The Module is resolved relative to the enclosing
// namespace, mirroring how `using` qualifies its target. Aliases are program-level
// (consulted in visibleNameCandidates) and never shadow a same-named declaration in
// the current namespace, a `using` target, or a bare global — they only add an
// extra resolution candidate.
func (a *Analyzer) registerImportAliases(n *ast.ImportDecl, namespace string) {
	if a == nil || n == nil {
		return
	}
	if a.importAliases == nil {
		a.importAliases = make(map[string]string)
	}
	module := joinQualifiedName(namespace, n.Module)
	for _, name := range n.Names {
		if name == "" {
			continue
		}
		target := joinQualifiedName(module, name)
		if existing, ok := a.importAliases[name]; ok && existing != target {
			a.errorf(n.Pos(), "conflicting import: %q is already imported as %q, cannot also import it as %q", name, existing, target)
			continue
		}
		a.importAliases[name] = target
	}
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
	namespace := a.currentNamespace
	if namespace == "" && a.currentFuncType != nil {
		if idx := strings.LastIndex(a.currentFuncType.Name, "."); idx >= 0 {
			namespace = a.currentFuncType.Name[:idx]
		}
	}
	if namespace != "" {
		candidates = append(candidates, joinQualifiedName(namespace, name))
	}
	if !strings.Contains(name, ".") {
		for _, usingName := range a.currentUsings {
			candidates = append(candidates, joinQualifiedName(usingName, name))
		}
		if target, ok := a.importAliases[name]; ok {
			candidates = append(candidates, target)
		}
	}
	candidates = append(candidates, name)
	return dedupeQualifiedNames(candidates)
}

func (a *Analyzer) lookupVisibleType(name string) (Type, string, bool) {
	for _, candidate := range a.visibleNameCandidates(name) {
		if t, ok := a.namedTypes[candidate]; ok {
			if a.privateTypeNames[candidate] && !a.canAccessPrivateName(candidate) {
				continue
			}
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

// identNameResolvesAsValue reports whether a bare name is bound as a value in the
// current scope or as a visible global value/symbol (used to distinguish a real
// value receiver from a namespace prefix in `Name.Field`).
func (a *Analyzer) identNameResolvesAsValue(name string) bool {
	if a == nil || name == "" {
		return false
	}
	if a.currentScope != nil {
		if _, ok := a.currentScope.Lookup(name); ok {
			return true
		}
	}
	if _, _, ok := a.lookupVisibleGlobal(name); ok {
		return true
	}
	return false
}

func (a *Analyzer) lookupVisibleGlobal(name string) (*Symbol, string, bool) {
	for _, candidate := range a.visibleNameCandidates(name) {
		if sym, ok := a.globalScope.Lookup(candidate); ok {
			if sym != nil && sym.Private && !a.canAccessPrivateName(candidate) {
				continue
			}
			return sym, candidate, true
		}
	}
	return nil, "", false
}

func privateOwnerNamespace(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[:idx]
	}
	return ""
}

func (a *Analyzer) canAccessPrivateName(name string) bool {
	owner := privateOwnerNamespace(name)
	if owner == "" {
		return true
	}
	current := a.currentNamespace
	if current == "" && a.currentFuncType != nil {
		current = privateOwnerNamespace(a.currentFuncType.Name)
	}
	return current == owner || strings.HasPrefix(current, owner+".")
}

// canonicalizeMatchEnumName resolves a match/is pattern's enum name through the
// visible-name candidates (current namespace, `using`, imports), returning the
// canonical qualified name when it resolves to the expected type name. This lets
// `Form.Circle(...)` match a scrutinee of namespaced type `Shapes.Form` under
// `using Shapes` — the strict name compares downstream all see the resolved name.
func (a *Analyzer) canonicalizeMatchEnumName(name string, expectedName string) string {
	if a == nil || name == "" || name == expectedName || expectedName == "" {
		return name
	}
	expectedType, _, expectedOK := a.lookupVisibleType(expectedName)
	for _, candidate := range a.visibleNameCandidates(name) {
		if candidate == expectedName {
			if _, ok := a.namedTypes[candidate]; ok {
				return candidate
			}
		}
		if expectedOK {
			if candidateType, ok := a.namedTypes[candidate]; ok && SameType(candidateType, expectedType) {
				return expectedName
			}
		}
	}
	return name
}

// inaccessiblePrivateName reports whether a failed lookup of name in fact found a
// private declaration the current namespace may not access, returning the qualified
// name and its owning module so the diagnostic can say "private", not "undefined".
func (a *Analyzer) inaccessiblePrivateName(name string) (string, string, bool) {
	if a == nil || name == "" {
		return "", "", false
	}
	for _, candidate := range a.visibleNameCandidates(name) {
		if a.canAccessPrivateName(candidate) {
			continue
		}
		if a.privateTypeNames[candidate] {
			return candidate, privateOwnerNamespace(candidate), true
		}
		if sym, ok := a.globalScope.Lookup(candidate); ok && sym != nil && sym.Private {
			return candidate, privateOwnerNamespace(candidate), true
		}
	}
	return "", "", false
}

func (a *Analyzer) lookupVisibleConst(name string) (ConstValue, bool) {
	for _, candidate := range a.visibleNameCandidates(name) {
		if sym, ok := a.globalScope.Lookup(candidate); ok && sym != nil && sym.Private && !a.canAccessPrivateName(candidate) {
			continue
		}
		if value, ok := a.constValues[candidate]; ok {
			return value, true
		}
	}
	return ConstValue{}, false
}
