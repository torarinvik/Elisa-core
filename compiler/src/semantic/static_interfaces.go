package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
)

const staticInterfaceSelfName = "Self"

type AssociatedTypeProjection struct {
	Receiver      Type
	InterfaceName string
	Name          string
}

type StaticInterface struct {
	Name            string
	AssociatedTypes map[string]*ast.AssociatedTypeDecl
	Methods         map[string]*StaticInterfaceMethod
	Decl            *ast.InterfaceDecl
	// Bases are the resolved (qualified) names of the protocols this protocol inherits from.
	// Their members are folded into Methods/AssociatedTypes so a single impl of this protocol
	// must satisfy the union of own + inherited members.
	Bases []string
	// Laws are the algebraic obligations declared on the protocol (docs/85 P3): a `law total(a:
	// Self, b: Self) = …` member is a universally-quantified predicate over `Self` that every
	// conforming impl must satisfy. They are discharged per impl (SMT or @property fuzz) and may
	// be assumed as facts in generic code bounded on the protocol.
	Laws []*ast.FuncDecl
}

type StaticInterfaceMethod struct {
	Name      string
	Signature *FuncType
	Decl      *ast.ExternFuncDecl
	// Default, when non-nil, is the default-method body (`def m(...) -> T: <body>`) declared on
	// the protocol. A conforming impl that omits this method inherits the default; one that
	// provides it overrides. Decl above is the bodiless signature view used for typechecking.
	Default *ast.FuncDecl
}

type StaticImpl struct {
	InterfaceName   string
	Receiver        Type
	AssociatedTypes map[string]Type
	// AssociatedTypeRefinements carries the refinement predicates an impl's associated-type binding
	// declares (P2: `type Field = u32 is InRange[0,127]`). The base type is representation-erased into
	// AssociatedTypes; the refinement lives here so a generic protocol call whose result is that
	// associated type can recover the static interval and feed the existing bounds-check elision.
	AssociatedTypeRefinements map[string][]ast.RefinementPredExpr
	Methods                   map[string]*Symbol
	Decl                      *ast.ImplDecl
	// TypeParams names the impl's own type parameters (from `impl[T] ... for Box[T]`).
	// When non-empty the impl is parametric: its Receiver pattern carries these as free
	// TypeParamType leaves, and a concrete receiver is matched by unifying against them.
	TypeParams               []string
	TypeParamInterfaceBounds map[string]string
	InvalidTypeParamBounds   bool
}

// implTypeParamNames returns the impl's type-parameter names, for use as the free-variable
// set when unifying a concrete receiver against the impl's Receiver pattern.
func (impl *StaticImpl) implTypeParamNames() map[string]bool {
	if impl == nil || len(impl.TypeParams) == 0 {
		return nil
	}
	free := make(map[string]bool, len(impl.TypeParams))
	for _, name := range impl.TypeParams {
		free[name] = true
	}
	return free
}

type InterfaceMethodRef struct {
	InterfaceName string
	MethodName    string
	// ImplKey pins a concrete receiver call to the impl selected by semantic analysis.
	// This keeps backend dispatch aligned with bound checks and ambiguity resolution.
	ImplKey string
	// Receiver is populated when a concrete receiver method call is rewritten
	// through a parametric impl. Unlike a qualified `Type.method(...)` call, the
	// original receiver expression is retained as the field object's expression;
	// this carries the concrete type to both semantic dispatch and code generation
	// without trying to reverse-render a generic type path such as darray[i32].
	Receiver Type
	// ExplicitTypeArgs carries method-level arguments from `value.method[T](...)`.
	// The field expression remains the callee so it can resolve through this ref;
	// SpecializeExpr only supports named global functions.
	ExplicitTypeArgs []ast.TypeExpr
}

func (*AssociatedTypeProjection) isType() {}

func (t *AssociatedTypeProjection) String() string {
	if t == nil || t.Receiver == nil {
		return "<invalid-associated-type>"
	}
	return t.Receiver.String() + "." + t.Name
}

func TypeIdentityKey(t Type) string {
	if t == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T:%s", t, t.String())
}

// TypeSymbolFragment names a type for an EMITTED SYMBOL. Unlike TypeIdentityKey it does
// NOT include Go's %T: that is this compiler's own implementation type
// (`*semantic.StructType`), and embedding it put `semantic.StructType_BuilderTag` inside
// public, C-visible symbols like `__impl__Builder__semantic.StructType_BuilderTag__make`.
//
// Two problems with that. It leaks an internal Go type name into the ABI, so renaming a
// Go struct here silently changes every emitted symbol; and it is unreproducible by any
// other implementation of this language — a second compiler would have to know THIS
// compiler's Go type names to agree on a symbol.
//
// Identity uses (impl-set keys, type equality) keep TypeIdentityKey and its %T
// disambiguation; only the naming sites use this.
func TypeSymbolFragment(t Type) string {
	if t == nil {
		return "<nil>"
	}
	return t.String()
}

func StaticImplLookupKey(interfaceName string, receiver Type) string {
	return interfaceName + "|" + TypeIdentityKey(receiver)
}

func LookupStaticImpl(impls map[string]*StaticImpl, interfaceName string, receiver Type) (*StaticImpl, bool) {
	if len(impls) == 0 || interfaceName == "" || receiver == nil {
		return nil, false
	}
	impl, ok := impls[StaticImplLookupKey(interfaceName, receiver)]
	return impl, ok && impl != nil
}

// UnifyTypePattern structurally matches a concrete type against a pattern type whose
// free variables are named in freeVars (the type params of a parametric impl), producing
// the substitution that makes them equal. Returns ok=false if the head constructors don't
// line up (so the impl does not apply). A free var bound twice must agree (SameType).
func UnifyTypePattern(pattern, actual Type, freeVars map[string]bool) (map[string]Type, bool) {
	subst := map[string]Type{}
	if !unifyTypePattern(pattern, actual, freeVars, subst) {
		return nil, false
	}
	return subst, true
}

func unifyTypePattern(pattern, actual Type, freeVars map[string]bool, subst map[string]Type) bool {
	if pattern == nil || actual == nil {
		return pattern == nil && actual == nil
	}
	if tp, ok := pattern.(*TypeParamType); ok && freeVars[tp.Name] {
		if existing, seen := subst[tp.Name]; seen {
			return SameType(existing, actual)
		}
		subst[tp.Name] = actual
		return true
	}
	switch p := pattern.(type) {
	case *GenericInstanceType:
		a, ok := actual.(*GenericInstanceType)
		if !ok || p.Name != a.Name || len(p.Args) != len(a.Args) {
			return false
		}
		for i := range p.Args {
			if !unifyTypePattern(p.Args[i], a.Args[i], freeVars, subst) {
				return false
			}
		}
		return true
	case *DArrayType:
		a, ok := actual.(*DArrayType)
		return ok && unifyTypePattern(p.Elem, a.Elem, freeVars, subst)
	case *DictType:
		a, ok := actual.(*DictType)
		return ok && unifyTypePattern(p.Key, a.Key, freeVars, subst) && unifyTypePattern(p.Value, a.Value, freeVars, subst)
	case *RefType:
		a, ok := actual.(*RefType)
		return ok && unifyTypePattern(p.Elem, a.Elem, freeVars, subst)
	default:
		return SameType(pattern, actual)
	}
}

// LookupStaticImplUnifying resolves an impl for a concrete receiver, trying an exact
// (concrete-impl) hit first, then matching any parametric impl by unifying its Receiver
// pattern against the concrete receiver. On a parametric match it returns the substitution
// {T: i64, …}; for an exact match the substitution is empty.
func LookupStaticImplUnifying(impls map[string]*StaticImpl, interfaceName string, receiver Type) (*StaticImpl, map[string]Type, bool) {
	if impl, ok := LookupStaticImpl(impls, interfaceName, receiver); ok {
		return impl, nil, true
	}
	for _, key := range slices.Sorted(maps.Keys(impls)) { // map: sort so overlapping parametric impls resolve the same way every run
		impl := impls[key]
		if impl == nil || impl.InterfaceName != interfaceName || len(impl.TypeParams) == 0 {
			continue
		}
		if subst, ok := UnifyTypePattern(impl.Receiver, receiver, impl.implTypeParamNames()); ok {
			return impl, subst, true
		}
	}
	return nil, nil, false
}

// substituteTypeParamsByName replaces free TypeParamType leaves named in subst, copying
// composite types field-by-field so unrelated fields (shape, region, storage) are kept.
// It is a small self-contained substituter for the free static-interface resolvers, which
// run without an *Analyzer.
func substituteTypeParamsByName(t Type, subst map[string]Type) Type {
	if t == nil || len(subst) == 0 {
		return t
	}
	switch tt := t.(type) {
	case *TypeParamType:
		if r, ok := subst[tt.Name]; ok && r != nil {
			return r
		}
		return t
	case *GenericInstanceType:
		clone := *tt
		clone.Args = make([]Type, len(tt.Args))
		for i, a := range tt.Args {
			clone.Args[i] = substituteTypeParamsByName(a, subst)
		}
		return &clone
	case *DArrayType:
		clone := *tt
		clone.Elem = substituteTypeParamsByName(tt.Elem, subst)
		return &clone
	case *DictType:
		clone := *tt
		clone.Key = substituteTypeParamsByName(tt.Key, subst)
		clone.Value = substituteTypeParamsByName(tt.Value, subst)
		return &clone
	case *RefType:
		clone := *tt
		clone.Elem = substituteTypeParamsByName(tt.Elem, subst)
		return &clone
	default:
		return t
	}
}

func ResolveAssociatedTypeProjection(proj *AssociatedTypeProjection, impls map[string]*StaticImpl) (Type, bool) {
	if proj == nil || proj.Receiver == nil || proj.InterfaceName == "" || proj.Name == "" {
		return nil, false
	}
	impl, subst, ok := LookupStaticImplUnifying(impls, proj.InterfaceName, proj.Receiver)
	if !ok || impl == nil {
		return nil, false
	}
	resolved, ok := impl.AssociatedTypes[proj.Name]
	if !ok || resolved == nil {
		return nil, false
	}
	return substituteTypeParamsByName(resolved, subst), true
}

func sanitizeStaticInterfaceSymbolFragment(value string) string {
	if value == "" {
		return "anon"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "anon"
	}
	return out
}

func StaticImplMethodSymbolName(interfaceName string, receiver Type, methodName string) string {
	return "__impl__" + sanitizeStaticInterfaceSymbolFragment(interfaceName) + "__" + sanitizeStaticInterfaceSymbolFragment(TypeSymbolFragment(receiver)) + "__" + sanitizeStaticInterfaceSymbolFragment(methodName)
}

func (a *Analyzer) lookupVisibleStaticInterface(name string) (*StaticInterface, string, bool) {
	for _, candidate := range a.visibleNameCandidates(name) {
		if iface, ok := a.staticInterfaces[candidate]; ok {
			return iface, candidate, true
		}
	}
	return nil, "", false
}

// callIsProtocolBoundMethod reports whether a call is `S.method(...)` where `S` names a
// type parameter bound to a protocol that declares `method`.
//
// The builtin collection chain in analyzeCallExpr keys on the FIELD NAME alone -- `push`,
// `clear`, `reserve`, `rows`, `truncate`, `valid`, and the dict/set `insert` rewrite -- so a
// protocol whose method happened to share one of those names had its call claimed by the
// builtin path. That path then evaluated the type parameter as a VALUE and reported
// `undefined identifier "S"`, naming something plainly in scope. `Surface.clear` is the
// motivating case; the same trap sits on every protocol built around a collection-shaped
// vocabulary. Protocol dispatch wins, and the ordinary path reaches
// resolveInterfaceMethodExprType as it already does for every non-colliding name.
func (a *Analyzer) callIsProtocolBoundMethod(expr *ast.CallExpr) bool {
	if a == nil || expr == nil {
		return false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Safe || fieldExpr.Field == "" {
		return false
	}
	recvIdent, ok := fieldExpr.Object.(*ast.Ident)
	if !ok || recvIdent == nil {
		return false
	}
	// A real value receiver keeps the builtin meaning: a local named `xs` whose type is a
	// darray still gets `xs.push(v)` even inside a function with a bound type parameter.
	if a.identNameResolvesAsValue(recvIdent.Name) {
		return false
	}
	iface, ok := a.lookupTypeParamInterface(recvIdent.Name)
	if !ok || iface == nil {
		return false
	}
	method, ok := iface.Methods[fieldExpr.Field]
	return ok && method != nil
}

func (a *Analyzer) lookupTypeParamInterface(name string) (*StaticInterface, bool) {
	for i := len(a.typeParamInterfaceScopes) - 1; i >= 0; i-- {
		if iface, ok := a.typeParamInterfaceScopes[i][name]; ok {
			return iface, iface != nil
		}
	}
	return nil, false
}

func (a *Analyzer) lookupInterfaceAssocType(name string) (Type, bool) {
	for i := len(a.interfaceAssocTypeScopes) - 1; i >= 0; i-- {
		if t, ok := a.interfaceAssocTypeScopes[i][name]; ok {
			return t, ok
		}
	}
	return nil, false
}

func (a *Analyzer) withInterfaceAssocTypes(bindings map[string]Type, fn func()) {
	a.withSourceInterfaceAssocTypes(nil, bindings, fn)
}

func (a *Analyzer) withSourceInterfaceAssocTypes(owner ast.Node, bindings map[string]Type, fn func()) {
	if len(bindings) == 0 {
		fn()
		return
	}
	a.interfaceAssocTypeScopes = append(a.interfaceAssocTypeScopes, bindings)
	ownership := a.lexicalBinders
	ownership.push("assoc-type", owner, bindings)
	fn()
	ownership.pop("assoc-type")
	a.interfaceAssocTypeScopes = a.interfaceAssocTypeScopes[:len(a.interfaceAssocTypeScopes)-1]
}

func (a *Analyzer) withTypeParamInterfaces(bindings map[string]*StaticInterface, fn func()) {
	a.withSourceTypeParamInterfaces(nil, bindings, fn)
}

func (a *Analyzer) withSourceTypeParamInterfaces(owner ast.Node, bindings map[string]*StaticInterface, fn func()) {
	if len(bindings) == 0 {
		fn()
		return
	}
	a.typeParamInterfaceScopes = append(a.typeParamInterfaceScopes, bindings)
	ownership := a.lexicalBinders
	ownership.push("param-interface", owner, bindings)
	fn()
	ownership.pop("param-interface")
	a.typeParamInterfaceScopes = a.typeParamInterfaceScopes[:len(a.typeParamInterfaceScopes)-1]
}

func (a *Analyzer) specializeInterfaceMethodSignature(signature *FuncType, receiver Type) *FuncType {
	if a == nil || signature == nil || receiver == nil {
		return signature
	}
	bindings := map[string]Type{staticInterfaceSelfName: receiver}
	specialized, _ := a.substituteType(signature, bindings, nil, nil, nil).(*FuncType)
	if specialized == nil {
		return signature
	}
	// A protocol's `Self& @r` is unresolved when its declaration is analyzed, so
	// resolveType cannot apply the normal container rule that moves @r from the
	// reference wrapper onto a concrete darray/dict/set type. Apply that same rule
	// after Self substitution, for both call-site dispatch and impl conformance.
	// Without this normalization a darray implementation appears to drop its owner
	// region even though its concrete signature carries it on DArrayType.Region.
	for i, param := range specialized.Params {
		specialized.Params[i] = normalizeSpecializedContainerRefRegion(param)
	}
	specialized.Return = normalizeSpecializedContainerRefRegion(specialized.Return)
	return specialized
}

func normalizeSpecializedContainerRefRegion(t Type) Type {
	ref, ok := t.(*RefType)
	if !ok || ref == nil || ref.Region == "" {
		return t
	}
	stamped, ok := stampContainerRegion(ref.Elem, ref.Region)
	if !ok {
		return t
	}
	clone := *ref
	clone.Elem = stamped
	clone.Region = ""
	return &clone
}

// typePathNameForReceiver reverse-resolves a concrete receiver type back to a visible
// type-path name (e.g. the struct's qualified name), so a `value.method()` UFCS call can be
// rewritten to the qualified `TypeName.method(value, …)` form that resolveInterfaceMethodExprType
// (analysis) and resolveStaticInterfaceMethod (backend, via NamedTypes) both understand.
func (a *Analyzer) typePathNameForReceiver(receiver Type) (string, bool) {
	if a == nil || receiver == nil {
		return "", false
	}
	receiver = unwrapReceiverRef(receiver)
	// Several names may denote one type (export aliases, tag/store names); take the smallest so
	// the rewritten callee / harness type name is the same every run.
	best, found := "", false
	for name, t := range a.namedTypes {
		if t != nil && SameType(t, receiver) && (!found || name < best) {
			best, found = name, true
		}
	}
	return best, found
}

// staticImplMethodForReceiver finds the protocol-impl (or synthesized default-method) impl that
// provides a method named methodName for a concrete receiver type. It returns the matched impl so
// a UFCS call can be dispatched. Ambiguity across multiple conforming protocols is reported.
func (a *Analyzer) staticImplMethodForReceiver(receiver Type, methodName string, pos ast.Node) (*StaticImpl, bool) {
	if a == nil || receiver == nil || methodName == "" {
		return nil, false
	}
	var matched *StaticImpl
	for _, impl := range a.staticImplsForReceiver(receiver) {
		if impl == nil {
			continue
		}
		if sym, ok := impl.Methods[methodName]; !ok || sym == nil {
			continue
		}
		if matched != nil {
			if pos != nil {
				a.errorf(pos.Pos(), "method %q on %s is ambiguous across multiple protocol impls", methodName, receiver.String())
			}
			return nil, false
		}
		matched = impl
	}
	if matched == nil {
		return nil, false
	}
	return matched, true
}

// staticImplMethodMatchForReceiver resolves a method on either an exact receiver
// impl or a parametric impl whose receiver pattern unifies with the concrete type.
// It is used by concrete receiver-call rewriting; unlike the older exact-only
// helper, it preserves the substitution for the analyzer/backend's shared static
// interface dispatch path. Multiple protocols exposing the same method remain
// ambiguous, even when one match is parametric and another is exact.
func (a *Analyzer) staticImplMethodMatchForReceiver(receiver Type, methodName string, pos ast.Node) (*StaticImpl, map[string]Type, bool, string) {
	if a == nil || receiver == nil || methodName == "" || len(a.staticImpls) == 0 {
		return nil, nil, false, ""
	}
	receiver = unwrapReceiverRef(receiver)
	var matched *StaticImpl
	var matchedSubst map[string]Type
	boundFailure := ""
	for _, key := range slices.Sorted(maps.Keys(a.staticImpls)) {
		impl := a.staticImpls[key]
		if impl == nil || impl.Receiver == nil || impl.Methods[methodName] == nil {
			continue
		}
		var subst map[string]Type
		if len(impl.TypeParams) != 0 {
			var ok bool
			subst, ok = UnifyTypePattern(impl.Receiver, receiver, impl.implTypeParamNames())
			if !ok {
				continue
			}
			if ok, reason := a.staticImplTypeParamBoundsSatisfied(impl, subst); !ok {
				if boundFailure == "" {
					boundFailure = reason
				}
				continue
			}
		} else if !SameType(impl.Receiver, receiver) {
			continue
		}
		if matched != nil {
			if pos != nil {
				a.errorf(pos.Pos(), "method %q on %s is ambiguous across multiple protocol impls", methodName, receiver.String())
			}
			return nil, nil, false, ""
		}
		matched = impl
		matchedSubst = subst
	}
	if matched == nil {
		return nil, nil, false, boundFailure
	}
	return matched, matchedSubst, true, ""
}
