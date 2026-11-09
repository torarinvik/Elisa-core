package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"slices"
	"sort"
	"strings"
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
	out := make([]string, 0, len(values))
	if len(values) <= 32 {
		// Name-resolution candidate lists are short (lexical namespaces + usings): a linear scan
		// gives the same first-occurrence order without a map allocation per lookup (the q7
		// profile put this function's map at ~2.7 s of a stage1 build).
		for _, value := range values {
			if value == "" || slices.Contains(out, value) {
				continue
			}
			out = append(out, value)
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	seen := make(map[string]bool, len(values))
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
	a.indexUnconditionalModuleNames(decls, namespace)
	result := a.flattenScopedDeclsWithVisibility(decls, namespace, inheritedUsings, false)
	for key, path := range a.moduleAliasPaths {
		a.moduleAliases[key] = a.resolveModuleAliasPath(path)
	}
	if len(a.moduleAliasPaths) != 0 {
		a.visibleAliasGen++
	}
	a.moduleAliasPaths = nil
	return result
}

// Declaration selection and extension validation must see aliases from the
// same lexical module when evaluating a static branch condition.
func (a *Analyzer) activeDeclBranchInNamespace(decl *ast.StaticIfDecl, namespace string) []ast.Decl {
	var branch []ast.Decl
	a.withResolutionContext(namespace, a.currentUsings, func() {
		branch = a.activeDeclBranch(decl)
	})
	return branch
}

// validateModuleExtensions enforces the `module`/`extend` contract before symbol
// collection: a module has exactly one canonical `module Foo:` declaration, and
// every `extend Foo:` targets an existing module. Both checks are file-order
// independent — all declarations are collected first, then validated — matching
// the merge semantics (extend members already flatten into Foo's namespace).
func (a *Analyzer) validateModuleExtensions(decls []ast.Decl) {
	declared := map[string]lexer.Pos{}
	if a.declaredModules == nil {
		a.declaredModules = map[string]bool{}
	}
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
				walk(a.activeDeclBranchInNamespace(n, namespace), namespace)
			case *ast.NamespaceDecl:
				full := joinQualifiedName(namespace, n.Name)
				if n.Extend {
					extends = append(extends, extendSite{name: full, pos: n.Position})
				} else if prev, ok := declared[full]; ok {
					a.errorf(n.Position, "module %q is already declared (at %s); use `extend %s:` to add to it", ast.ModulePathSpelling(full), prev, ast.ModulePathSpelling(full))
				} else {
					declared[full] = n.Position
				}
				a.declaredModules[full] = true
				walk(n.Decls, full)
			}
		}
	}
	walk(decls, "")
	for _, e := range extends {
		if _, ok := declared[e.name]; !ok {
			a.errorf(e.pos, "no module %q to extend; declare it with `module %s:` first (if this file is a fragment of a multi-file module, compile from the module root instead of this file alone)", ast.ModulePathSpelling(e.name), ast.ModulePathSpelling(e.name))
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
			branch := a.activeDeclBranchInNamespace(n, namespace)
			a.indexUnconditionalModuleNames(branch, namespace)
			out = append(out, a.flattenScopedDeclsWithVisibility(branch, namespace, effectiveUsings, inheritedPrivate)...)
		case *ast.NamespaceDecl:
			childNamespace := joinQualifiedName(namespace, n.Name)
			// A private module hides the MODULE, not its members: its body starts at the
			// default (public) and the name itself is gated instead. Pushing the mark into
			// the body made every member private TO THE MODULE, which locked the parent out
			// of its own nested module (`canAccessPrivateName` grants the owning namespace
			// and its descendants, never its ancestors).
			if a.declIsPrivate(n, false) {
				if a.privateModules == nil {
					a.privateModules = map[string]bool{}
				}
				a.privateModules[childNamespace] = true
			}
			out = append(out, a.flattenScopedDeclsWithVisibility(n.Decls, childNamespace, effectiveUsings, false)...)
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
		a.visibleAliasGen++
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
	a.visibleAliasGen++
}

type moduleAliasPath struct {
	scope string
	name  string
}

func (a *Analyzer) indexUnconditionalModuleNames(decls []ast.Decl, namespace string) {
	for _, decl := range decls {
		module, ok := decl.(*ast.NamespaceDecl)
		if !ok {
			continue
		}
		name := joinQualifiedName(namespace, module.Name)
		if a.declaredModules == nil {
			a.declaredModules = map[string]bool{}
		}
		if !module.Extend && !a.declaredModules[name] {
			a.declaredModules[name] = true
			a.visibleAliasGen++
		}
		a.indexUnconditionalModuleNames(module.Decls, name)
	}
}

func moduleAliasKey(aliases map[string]string, namespace, name string) (string, bool) {
	if len(aliases) == 0 {
		return "", false
	}
	for namespace != "" {
		if _, found := aliases[namespace+"."+name]; found {
			return namespace + "." + name, true
		}
		namespace = privateOwnerNamespace(namespace)
	}
	_, found := aliases[name]
	return name, found
}

// ScopedModuleAliasTarget finds the closest module alias visible from namespace.
func ScopedModuleAliasTarget(aliases map[string]string, namespace, name string) (string, bool) {
	key, ok := moduleAliasKey(aliases, namespace, name)
	return aliases[key], ok
}

func (a *Analyzer) resolveModuleAliasPath(path moduleAliasPath) string {
	for scope := path.scope; scope != ""; scope = privateOwnerNamespace(scope) {
		if name := joinQualifiedName(scope, path.name); a.declaredModules[name] {
			return name
		}
	}
	if a.declaredModules[path.name] {
		return path.name
	}
	return joinQualifiedName(path.scope, path.name)
}

func (a *Analyzer) lookupModuleAliasTarget(namespace, name string) (string, bool) {
	key, ok := moduleAliasKey(a.moduleAliases, namespace, name)
	if !ok {
		return "", false
	}
	if path, pending := a.moduleAliasPaths[key]; pending {
		return a.resolveModuleAliasPath(path), true
	}
	return a.moduleAliases[key], true
}

// registerModuleAlias retains the original lexical target until active module
// declarations are known. Alias chains preserve the scope of their first target.
func (a *Analyzer) registerModuleAlias(n *ast.UsingDecl, namespace string) {
	if a == nil || n == nil || n.Alias == "" {
		return
	}
	if a.moduleAliases == nil {
		a.moduleAliases = make(map[string]string)
	}
	path := moduleAliasPath{scope: namespace, name: n.Name}
	root, suffix, qualified := strings.Cut(n.Name, ".")
	if key, ok := moduleAliasKey(a.moduleAliases, namespace, root); ok {
		if pending, exists := a.moduleAliasPaths[key]; exists {
			path = pending
		} else {
			path = moduleAliasPath{name: a.moduleAliases[key]}
		}
		if qualified {
			path.name = joinQualifiedName(path.name, suffix)
		}
	}
	key := joinQualifiedName(namespace, n.Alias)
	target := a.resolveModuleAliasPath(path)
	if existing, ok := a.moduleAliases[key]; ok {
		if pending, exists := a.moduleAliasPaths[key]; exists {
			existing = a.resolveModuleAliasPath(pending)
		}
		if existing != target {
			a.errorf(n.Pos(), "conflicting alias: %q already aliases module %q, cannot also alias %q", n.Alias, existing, target)
			return
		}
	}
	if a.moduleAliasPaths == nil {
		a.moduleAliasPaths = make(map[string]moduleAliasPath)
	}
	a.moduleAliasPaths[key] = path
	a.moduleAliases[key] = target
	a.visibleAliasGen++
}

// reportUsingAmbiguity errors when a bare name is brought in by two or more
// wildcard `using` imports that resolve to distinct globals — the collision must
// be resolved by qualifying the reference (`Foo::bar`). A current-namespace or
// local binding takes precedence and is never ambiguous. Returns true if it erred.
func (a *Analyzer) reportUsingAmbiguity(name string, pos lexer.Pos) bool {
	matches := a.usingAmbiguityMatches(name)
	if len(matches) < 2 {
		return false
	}
	a.errorf(pos, "ambiguous reference %q is brought in by multiple `using` imports (%s); qualify it explicitly", name, strings.Join(matches, ", "))
	return true
}

func (a *Analyzer) usingAmbiguityMatches(name string) []string {
	if a == nil || name == "" || strings.Contains(name, ".") || len(a.currentUsings) < 2 {
		return nil
	}
	namespace := a.currentNamespace
	if namespace == "" && a.currentFuncType != nil {
		namespace = privateOwnerNamespace(a.currentFuncType.Name)
	}
	if namespace != "" {
		if _, ok := a.globalScope.Lookup(joinQualifiedName(namespace, name)); ok {
			return nil
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
			if !a.globalNameIsVisible(sym, cand) {
				continue
			}
			seen[cand] = true
			matches = append(matches, cand)
		}
	}
	if len(matches) > 1 {
		sort.Strings(matches)
		return matches
	}
	return nil
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
