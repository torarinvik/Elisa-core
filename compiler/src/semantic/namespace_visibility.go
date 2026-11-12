package semantic

import (
	"slices"
	"strconv"
	"strings"
)

// visibleCandidateCache memoizes visibleNameCandidates. The candidate list is a pure function of
// (lexical namespace, currentUsings, importAliases, moduleAliases, name). The first two form the
// resolution context, checked on every call; the alias maps only grow through the register*
// functions above, which bump aliasGen. Lists are stored and returned with cap == len, so a
// caller's append copies instead of writing into the shared array.
type visibleCandidateCache struct {
	namespace string
	usings    []string
	aliasGen  int
	byName    map[string][]string
	// byContext keeps per-context tables across function switches (keyed by namespace, usings
	// and aliasGen), so returning to an earlier context does not rebuild its lists.
	byContext map[string]map[string][]string
}

func (a *Analyzer) visibleCandidateTable(namespace string) map[string][]string {
	c := &a.visibleCandidates
	if c.byName != nil && c.namespace == namespace && c.aliasGen == a.visibleAliasGen && slices.Equal(c.usings, a.currentUsings) {
		return c.byName
	}
	if c.byContext == nil {
		c.byContext = map[string]map[string][]string{}
	}
	var key strings.Builder
	key.WriteString(strconv.Itoa(a.visibleAliasGen))
	key.WriteByte(0)
	key.WriteString(namespace)
	for _, u := range a.currentUsings {
		key.WriteByte(0)
		key.WriteString(u)
	}
	table := c.byContext[key.String()]
	if table == nil {
		table = map[string][]string{}
		c.byContext[key.String()] = table
	}
	c.namespace, c.usings, c.aliasGen, c.byName = namespace, a.currentUsings, a.visibleAliasGen, table
	return table
}

func (a *Analyzer) visibleNameCandidates(name string) []string {
	if name == "" {
		return nil
	}
	namespace := a.currentNamespace
	if namespace == "" && a.currentFuncType != nil {
		if idx := strings.LastIndex(a.currentFuncType.Name, "."); idx >= 0 {
			namespace = a.currentFuncType.Name[:idx]
		}
	}
	table := a.visibleCandidateTable(namespace)
	if cached, ok := table[name]; ok {
		return cached
	}
	built := a.buildVisibleNameCandidates(namespace, name)
	built = built[:len(built):len(built)]
	table[name] = built
	return built
}

func (a *Analyzer) buildVisibleNameCandidates(namespace, name string) []string {
	candidates := make([]string, 0, len(a.currentUsings)+2)
	for lexical := namespace; lexical != ""; {
		candidates = append(candidates, joinQualifiedName(lexical, name))
		if dot := strings.LastIndex(lexical, "."); dot >= 0 {
			lexical = lexical[:dot]
		} else {
			lexical = ""
		}
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
		if canon, ok := a.lookupModuleAliasTarget(namespace, name[:idx]); ok {
			candidates = append(candidates, canon+name[idx:])
		}
	}
	candidates = append(candidates, name)
	return dedupeQualifiedNames(candidates)
}

func (a *Analyzer) typeNameIsVisible(name string) bool {
	return !(a.privateTypeNames[name] && !a.canAccessPrivateName(name)) && a.privateModuleOnPath(name) == ""
}

func (a *Analyzer) lookupVisibleType(name string) (Type, string, bool) {
	var reads []lookupCandidateRead
	if a.lookupTrace != nil {
		defer func() { a.lookupTrace.record(a, "type", name, reads, false) }()
	}
	for _, candidate := range a.visibleNameCandidates(name) {
		t, present := a.namedTypes[candidate]
		visible := present && a.typeNameIsVisible(candidate)
		if a.lookupTrace != nil {
			reads = appendLookupCandidate(reads, lookupCandidateRead{candidate, present, visible})
		}
		if visible {
			return t, candidate, true
		}
	}
	return nil, "", false
}

func (a *Analyzer) lookupVisiblePermission(name string) (*PermissionSet, string, bool) {
	var reads []lookupCandidateRead
	if a.lookupTrace != nil {
		defer func() { a.lookupTrace.record(a, "permission", name, reads, false) }()
	}
	for _, candidate := range a.visibleNameCandidates(name) {
		permission, present := a.permissions[candidate]
		if a.lookupTrace != nil {
			reads = appendLookupCandidate(reads, lookupCandidateRead{candidate, present, present})
		}
		if present {
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
		if a.lookupTrace != nil {
			a.lookupTrace.record(a, "enclosing", name, nil, true)
		}
		return nil, "", false
	}
	qualified := joinQualifiedName(namespace, name)
	sym, ok := a.globalScope.Lookup(qualified)
	visible := ok && sym != nil && a.globalNameIsVisible(sym, qualified)
	if a.lookupTrace != nil {
		a.lookupTrace.record(a, "enclosing", name, []lookupCandidateRead{{qualified, ok, visible}}, false)
	}
	if !visible {
		return nil, "", false
	}
	return sym, qualified, true
}

func (a *Analyzer) lookupVisibleGlobal(name string) (*Symbol, string, bool) {
	var reads []lookupCandidateRead
	if a.lookupTrace != nil {
		defer func() { a.lookupTrace.record(a, "value", name, reads, false) }()
	}
	for _, candidate := range a.visibleNameCandidates(name) {
		sym, present := a.globalScope.Lookup(candidate)
		visible := present && (sym == nil || a.globalNameIsVisible(sym, candidate))
		if a.lookupTrace != nil {
			reads = appendLookupCandidate(reads, lookupCandidateRead{candidate, present, visible})
		}
		if visible {
			return sym, candidate, true
		}
	}
	return nil, "", false
}

// visibleCallableName removes internal overload and extension dispatch suffixes
// when presenting a callable name in a diagnostic.
func visibleCallableName(name string) string {
	for _, prefix := range [...]string{"__ovl__", "__ext__"} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			if prefix == "__ovl__" {
				// Receiver-overload symbols repeat the visible name at the end.
				// Use that repetition to distinguish separators from `__` in a
				// user-written name.
				best := ""
				for at := strings.Index(rest, "__"); at > 0; {
					candidate := rest[:at]
					if strings.HasSuffix(rest, "__"+candidate) {
						best = candidate
					}
					next := strings.Index(rest[at+2:], "__")
					if next < 0 {
						break
					}
					at += next + 2
				}
				if best != "" {
					return best
				}
			}
			if end := strings.Index(rest, "__"); end > 0 {
				return rest[:end]
			}
			break
		}
	}
	return name
}

func privateOwnerNamespace(name string) string {
	// The last dot of a mangled dispatch symbol may sit inside its suffix.
	name = visibleCallableName(name)
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[:idx]
	}
	return ""
}

// privateModuleOnPath returns the INNERMOST module along `qualified`'s path that is
// declared private and out of reach from the current namespace, or "" when the whole
// path is visible. Module visibility gates everything underneath it: `public` grants a
// member exactly the reach of the module that declares it, never more. A module
// private at file scope has no enclosing module to be private to, so it gates nothing.
func (a *Analyzer) privateModuleOnPath(qualified string) string {
	if a == nil || len(a.privateModules) == 0 {
		return ""
	}
	blocked := ""
	for index := 0; index < len(qualified); index++ {
		if qualified[index] != '.' {
			continue
		}
		prefix := qualified[:index]
		if a.privateModules[prefix] && !a.canAccessPrivateName(prefix) {
			blocked = prefix
		}
	}
	return blocked
}

// globalNameIsVisible reports whether a name reached at `qualified` may be referenced
// from the current namespace: its own `private:` mark AND every module on its path.
func (a *Analyzer) globalNameIsVisible(sym *Symbol, qualified string) bool {
	if sym != nil && sym.Private && !a.canAccessPrivateName(qualified) {
		return false
	}
	return a.privateModuleOnPath(qualified) == ""
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
			candidateType, ok := a.namedTypes[candidate]
			if !ok {
				continue
			}
			// A refinement of the scrutinee's hierarchy (`Ui::Container.Box` on a
			// `Ui.Widget`) is the same store but NOT the same case set: keep the
			// child's own canonical name so its variants resolve, exactly as the
			// unqualified `Container.Box` does.
			if childEnum, isEnum := candidateType.(*EnumType); isEnum && childEnum.Parent != nil && candidate != expectedName {
				if expectedEnum, expectedIsEnum := expectedType.(*EnumType); expectedIsEnum && childEnum.Root() == expectedEnum.Root() {
					return candidate
				}
			}
			if SameType(candidateType, expectedType) {
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
		// A private module on the path hides the name wholesale, however public the name
		// itself is: report the MODULE, since that is what the caller has to be let into.
		if blocked := a.privateModuleOnPath(candidate); blocked != "" {
			if _, ok := a.namedTypes[candidate]; ok {
				return blocked, privateOwnerNamespace(blocked), true
			}
			if _, ok := a.globalScope.Lookup(candidate); ok {
				return blocked, privateOwnerNamespace(blocked), true
			}
		}
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
		if sym, ok := a.globalScope.Lookup(candidate); ok && sym != nil && !a.globalNameIsVisible(sym, candidate) {
			continue
		}
		if a.privateModuleOnPath(candidate) != "" {
			continue
		}
		if value, ok := a.constValues[candidate]; ok {
			return value, true
		}
	}
	return ConstValue{}, false
}
