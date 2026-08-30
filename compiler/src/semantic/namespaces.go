package semantic

import (
	"sort"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
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

// validateModuleExtensions enforces the `module`/`extend` contract before symbol
// collection: a module has exactly one canonical `module Foo:` declaration, and
// every `extend Foo:` targets an existing module. Both checks are file-order
// independent — all declarations are collected first, then validated — matching
// the merge semantics (extend members already flatten into Foo's namespace).
func (a *Analyzer) validateModuleExtensions(decls []ast.Decl) {
	declared := map[string]lexer.Pos{}
	type extendSite struct {
		name string
		pos  lexer.Pos
	}
	var extends []extendSite
	var walk func(decls []ast.Decl, namespace string)
	walk = func(decls []ast.Decl, namespace string) {
		for _, decl := range decls {
			switch n := decl.(type) {
			case *ast.StaticIfDecl:
				walk(a.activeDeclBranch(n), namespace)
			case *ast.NamespaceDecl:
				full := joinQualifiedName(namespace, n.Name)
				if n.Extend {
					extends = append(extends, extendSite{name: full, pos: n.Position})
				} else if prev, ok := declared[full]; ok {
					a.errorf(n.Position, "module %q is already declared (at %s); use `extend %s:` to add to it", full, prev, full)
				} else {
					declared[full] = n.Position
				}
				walk(n.Decls, full)
			}
		}
	}
	walk(decls, "")
	for _, e := range extends {
		if _, ok := declared[e.name]; !ok {
			a.errorf(e.pos, "no module %q to extend; declare it with `module %s:` first (if this file is a fragment of a multi-file module, compile from the module root instead of this file alone)", e.name, e.name)
		}
	}
}

func (a *Analyzer) flattenScopedDeclsWithVisibility(decls []ast.Decl, namespace string, inheritedUsings []string, inheritedPrivate bool) []scopedDecl {
	blockUsings := make([]string, 0)
	for _, decl := range decls {
		usingDecl, ok := decl.(*ast.UsingDecl)
		if !ok {
			continue
		}
		switch {
		case usingDecl.Alias != "":
			a.registerModuleAlias(usingDecl, namespace)
		case usingDecl.Member != "":
			a.registerUsingMember(usingDecl, namespace)
		default:
			blockUsings = append(blockUsings, joinQualifiedName(namespace, usingDecl.Name))
		}
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

// registerUsingMember records `using Foo::bar` so the bare name `bar` resolves to
// `Foo.bar`, exactly like `from Foo import bar`. Program-level; only an extra
// resolution candidate (never shadows a local/namespace/wildcard-using name).
func (a *Analyzer) registerUsingMember(n *ast.UsingDecl, namespace string) {
	if a == nil || n == nil || n.Member == "" {
		return
	}
	if a.importAliases == nil {
		a.importAliases = make(map[string]string)
	}
	target := joinQualifiedName(joinQualifiedName(namespace, n.Name), n.Member)
	if existing, ok := a.importAliases[n.Member]; ok && existing != target {
		a.errorf(n.Pos(), "conflicting using: %q is already brought in as %q, cannot also bring it as %q", n.Member, existing, target)
		return
	}
	a.importAliases[n.Member] = target
}

// registerModuleAlias records `using Foo as F` so the qualifier `F` rewrites to the
// module `Foo`: `F::x` resolves to `Foo.x`. Qualification is kept, just shortened.
func (a *Analyzer) registerModuleAlias(n *ast.UsingDecl, namespace string) {
	if a == nil || n == nil || n.Alias == "" {
		return
	}
	if a.moduleAliases == nil {
		a.moduleAliases = make(map[string]string)
	}
	target := joinQualifiedName(namespace, n.Name)
	if existing, ok := a.moduleAliases[n.Alias]; ok && existing != target {
		a.errorf(n.Pos(), "conflicting alias: %q already aliases module %q, cannot also alias %q", n.Alias, existing, target)
		return
	}
	a.moduleAliases[n.Alias] = target
}

// reportUsingAmbiguity errors when a bare name is brought in by two or more
// wildcard `using` imports that resolve to distinct globals — the collision must
// be resolved by qualifying the reference (`Foo::bar`). A current-namespace or
// local binding takes precedence and is never ambiguous. Returns true if it erred.
func (a *Analyzer) reportUsingAmbiguity(name string, pos lexer.Pos) bool {
	if a == nil || name == "" || strings.Contains(name, ".") || len(a.currentUsings) < 2 {
		return false
	}
	namespace := a.currentNamespace
	if namespace == "" && a.currentFuncType != nil {
		namespace = privateOwnerNamespace(a.currentFuncType.Name)
	}
	if namespace != "" {
		if _, ok := a.globalScope.Lookup(joinQualifiedName(namespace, name)); ok {
			return false
		}
	}
	seen := map[string]bool{}
	matches := make([]string, 0, 2)
	for _, usingName := range a.currentUsings {
		cand := joinQualifiedName(usingName, name)
		if seen[cand] {
			continue
		}
		if sym, ok := a.globalScope.Lookup(cand); ok && sym != nil {
			if sym.Private && !a.canAccessPrivateName(cand) {
				continue
			}
			seen[cand] = true
			matches = append(matches, cand)
		}
	}
	if len(matches) > 1 {
		sort.Strings(matches)
		a.errorf(pos, "ambiguous reference %q is brought in by multiple `using` imports (%s); qualify it explicitly", name, strings.Join(matches, ", "))
		return true
	}
	return false
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
	} else if idx := strings.Index(name, "."); idx > 0 {
		// Module-alias qualifier rewrite: `F.x` (from `using Foo as F`) -> `Foo.x`.
		if canon, ok := a.moduleAliases[name[:idx]]; ok {
			candidates = append(candidates, canon+name[idx:])
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

// lookupLocalSymbol finds `name` in the scope chain WITHOUT reaching the global scope,
// so a local or parameter can be told apart from a top-level definition. Scope.Lookup
// walks all the way through the global scope, which is exactly the distinction the
// enclosing-namespace preference below needs.
func (a *Analyzer) lookupLocalSymbol(name string) (*Symbol, bool) {
	if a == nil {
		return nil, false
	}
	for cur := a.currentScope; cur != nil && cur != a.globalScope; cur = cur.Parent {
		if sym, ok := cur.Symbols[name]; ok {
			return sym, true
		}
	}
	return nil, false
}

// enclosingNamespaceMember resolves a BARE name to the enclosing module's own member.
//
// Inside `module M:`, `add(...)` means M's `add` -- that is what a namespace is for.
// Identifier resolution reaches the global scope through the ordinary scope chain, so a
// top-level or stdlib `add` was found there and returned before the enclosing module was
// ever consulted:
//
//	module Box:
//	    def add(s: mutable Sack&, v: i64): ...
//	    def fill(s: mutable Sack&):
//	        add(s, 7)      # argument 1 to "add" expects mutable Flags[T]&
//
// The self-hosted stage1 already prefers the enclosing module's member, so the two
// compilers disagreed about a module member calling its own sibling by its own name.
// Without this, module members must avoid every name the stdlib uses generically --
// push, add, get, count -- which is C-style prefixing again, wearing a module block.
//
// Locals and parameters still shadow: a local binding is found before the global scope
// is reached, and is checked for here explicitly so the preference never overrides one.
func (a *Analyzer) enclosingNamespaceMember(name string) (*Symbol, string, bool) {
	if a == nil || a.globalScope == nil || name == "" || strings.Contains(name, ".") {
		return nil, "", false
	}
	namespace := a.currentNamespace
	if namespace == "" && a.currentFuncType != nil {
		if idx := strings.LastIndex(a.currentFuncType.Name, "."); idx >= 0 {
			namespace = a.currentFuncType.Name[:idx]
		}
	}
	if namespace == "" {
		return nil, "", false
	}
	if _, shadowed := a.lookupLocalSymbol(name); shadowed {
		return nil, "", false
	}
	qualified := joinQualifiedName(namespace, name)
	sym, ok := a.globalScope.Lookup(qualified)
	if !ok || sym == nil {
		return nil, "", false
	}
	if sym.Private && !a.canAccessPrivateName(qualified) {
		return nil, "", false
	}
	return sym, qualified, true
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
