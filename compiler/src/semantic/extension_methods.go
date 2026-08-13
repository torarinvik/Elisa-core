package semantic

import (
	"fmt"
	"strings"

	"elisacore/src/ast"
)

type ExtensionMethod struct {
	VisibleName string
	Receiver    Type
	Symbol      *Symbol
	Decl        ast.Node
}

func ExtensionMethodSymbolName(visibleName string, receiver Type, methodName string) string {
	return "__ext__" + sanitizeStaticInterfaceSymbolFragment(visibleName) + "__" + sanitizeStaticInterfaceSymbolFragment(TypeSymbolFragment(receiver)) + "__" + sanitizeStaticInterfaceSymbolFragment(methodName)
}

func ReceiverOverloadSymbolName(visibleName string, receiver Type, methodName string) string {
	// Mangle the receiver by its type String() (the same clean scheme mangleGenericType
	// uses for type args), NOT TypeIdentityKey — TypeIdentityKey prepends the Go runtime
	// type (`%T`, e.g. `*semantic.RefType`), which leaked `semantic_RefType_...` into the
	// emitted symbol name. String() already uniquely identifies the receiver type.
	return "__ovl__" + sanitizeStaticInterfaceSymbolFragment(visibleName) + "__" + sanitizeStaticInterfaceSymbolFragment(receiver.String()) + "__" + sanitizeStaticInterfaceSymbolFragment(methodName)
}

func (a *Analyzer) validateExtensionMethodSignature(visibleName string, receiver Type, fnType *FuncType, decl ast.Node) bool {
	if a == nil || receiver == nil || fnType == nil || decl == nil {
		return false
	}
	if len(fnType.Params) == 0 {
		a.errorf(decl.Pos(), "extension method %q on %s must take the receiver as its first parameter", visibleName, receiver)
		return false
	}
	if !SameType(fnType.Params[0], receiver) {
		a.errorf(decl.Pos(), "extension method %q on %s must take %s as its first parameter, got %s", visibleName, receiver, receiver, fnType.Params[0])
		return false
	}
	return true
}

func (a *Analyzer) registerExtensionMethod(visibleName string, receiver Type, sym *Symbol, decl ast.Node, fnType *FuncType) {
	if a == nil || visibleName == "" || receiver == nil || sym == nil || decl == nil || fnType == nil {
		return
	}
	if !a.validateExtensionMethodSignature(visibleName, receiver, fnType, decl) {
		return
	}
	methods := a.extensionMethodsByName[visibleName]
	for _, existing := range methods {
		if existing == nil || existing.Receiver == nil {
			continue
		}
		if SameType(existing.Receiver, receiver) {
			a.errorf(decl.Pos(), "duplicate extension method %q on %s", visibleName, receiver)
			return
		}
	}
	a.extensionMethodsByName[visibleName] = append(methods, &ExtensionMethod{
		VisibleName: visibleName,
		Receiver:    receiver,
		Symbol:      sym,
		Decl:        decl,
	})
}

func (a *Analyzer) lookupVisibleExtensionMethod(name string, actualReceiver Type) (*ExtensionMethod, bool, error) {
	if a == nil || name == "" || actualReceiver == nil {
		return nil, false, nil
	}
	for _, candidate := range a.visibleNameCandidates(name) {
		var matched *ExtensionMethod
		for _, method := range a.extensionMethodsByName[candidate] {
			if method == nil || method.Symbol == nil || method.Receiver == nil {
				continue
			}
			if !AssignableTo(method.Receiver, actualReceiver) {
				continue
			}
			if matched != nil {
				return nil, false, fmt.Errorf("extension method %q on %s is ambiguous", name, diagnosticTypeString(actualReceiver))
			}
			matched = method
		}
		if matched != nil {
			return matched, true, nil
		}
	}
	return nil, false, nil
}

func (a *Analyzer) lookupVisibleUFCSFunction(name string, actualReceiver Type) (*Symbol, bool, error) {
	return a.lookupVisibleUFCSFunctionWithArity(name, actualReceiver, -1)
}

// lookupVisibleUFCSFunctionWithArity resolves an overloaded receiver function by
// two combined criteria:
//   - arity: when requiredArity >= 0, a candidate with a fixed (non-variadic, no
//     default) parameter list whose explicit count differs from requiredArity is
//     excluded — e.g. a 1-arg `x.f()` never selects `f(a, b)`. Variadic/defaulted
//     candidates are conservatively kept (the normal arg-count check validates them).
//   - specificity ("most concrete wins"): among candidates whose first parameter
//     accepts the receiver, a concrete/structured receiver (e.g. `f64`, `IOFile&`,
//     `IndexMap[K,T]&`) outranks a bare type-parameter receiver (`T`, which matches
//     anything). The strictly most-specific candidate wins; a tie at the top rank is
//     reported as ambiguous.
func (a *Analyzer) lookupVisibleUFCSFunctionWithArity(name string, actualReceiver Type, requiredArity int) (*Symbol, bool, error) {
	if a == nil || name == "" || actualReceiver == nil || a.globalScope == nil {
		return nil, false, nil
	}
	type ranked struct {
		sym  *Symbol
		name string
	}
	var (
		best     []ranked
		bestRank int
		seen     = map[*Symbol]bool{}
	)
	for _, candidate := range a.visibleNameCandidates(name) {
		candidates := a.ufcsFunctionsByName[candidate]
		if len(candidates) == 0 {
			if sym, ok := a.globalScope.Lookup(candidate); ok && sym != nil {
				candidates = []*Symbol{sym}
			}
		}
		for _, sym := range candidates {
			if sym == nil || seen[sym] {
				continue
			}
			if sym.Kind != SymbolFunc && sym.Kind != SymbolExternFunc {
				continue
			}
			fnType, ok := sym.Type.(*FuncType)
			if !ok || fnType == nil || len(fnType.Params) == 0 {
				continue
			}
			if requiredArity >= 0 && !funcTypeAcceptsArgCount(fnType, requiredArity) {
				continue
			}
			rank := a.receiverMatchRank(fnType.Params[0], actualReceiver)
			if rank == 0 {
				continue
			}
			seen[sym] = true
			switch {
			case rank > bestRank:
				bestRank = rank
				best = []ranked{{sym: sym, name: candidate}}
			case rank == bestRank:
				best = append(best, ranked{sym: sym, name: candidate})
			}
		}
	}
	if len(best) > 1 {
		names := make([]string, 0, len(best))
		for _, b := range best {
			names = append(names, b.name)
		}
		return nil, false, fmt.Errorf("UFCS call %q on %s is ambiguous: %s", name, diagnosticTypeString(actualReceiver), strings.Join(names, ", "))
	}
	if len(best) == 1 {
		return best[0].sym, true, nil
	}
	return nil, false, nil
}

// funcTypeAcceptsArgCount reports whether ft can be called with n explicit
// arguments. It only excludes simple fixed-arity functions (no variadic, no
// defaults) whose explicit parameter count differs from n; variadic or defaulted
// signatures are conservatively accepted (the normal arg-count check validates
// them) so the overload arity filter never wrongly drops a legitimate candidate.
func funcTypeAcceptsArgCount(ft *FuncType, n int) bool {
	if ft == nil {
		return false
	}
	if ft.Variadic {
		return true
	}
	for _, hasDefault := range ft.ExplicitParamHasDefault {
		if hasDefault {
			return true
		}
	}
	return funcTypeExplicitParamCount(ft) == n
}

// receiverMatchRank scores how specifically a receiver-parameter type matches an
// actual receiver. 0 = no match. Otherwise the score is 1 + typeSpecificityScore,
// so a bare type parameter (`T`, wildcard) scores lowest (1), a concrete leaf
// (`f64`, a struct) scores 2, and a generic instance scores higher the more of its
// type arguments are themselves concrete — e.g. for a `Box[i64,i64]` receiver,
// `Box[i64,i64]&` (fully bound) outranks `Box[A,B]&` (both args generic). Higher is
// more specific, so "most concrete wins" overload resolution prefers it.
func (a *Analyzer) receiverMatchRank(expected, actual Type) int {
	if !a.ufcsReceiverAssignableTo(expected, actual) {
		return 0
	}
	eBase := StripAggregateStateType(unwrapReceiverRef(expected))
	// Precision refinement for generic-instance receivers: ufcsReceiverAssignableTo
	// matches by base name alone, so `Box[i64,i64]&` is accepted for a `Box[f64,i64]`
	// actual. Require the type arguments to structurally match (matchTypePattern,
	// which treats the receiver's own type params as wildcards) so a non-fitting
	// specialization scores 0 instead of out-ranking the real generic match. Scoped
	// to same-base generic instances so autoref/literal coercions are untouched.
	if eInst, ok := eBase.(*GenericInstanceType); ok {
		aBase := StripAggregateStateType(unwrapReceiverRef(actual))
		if aInst, ok := aBase.(*GenericInstanceType); ok && eInst.Name == aInst.Name {
			if !matchTypePattern(eBase, aBase) {
				return 0
			}
		}
	}
	rank := 1 + typeSpecificityScore(eBase)
	if expectedRef, ok := expected.(*RefType); ok && expectedRef != nil {
		if actualRef, ok := actual.(*RefType); ok && actualRef != nil && expectedRef.Mutable == actualRef.Mutable {
			rank++
		}
	}
	return rank
}

func unwrapReceiverRef(t Type) Type {
	if ref, ok := t.(*RefType); ok && ref != nil {
		return ref.Elem
	}
	return t
}

// typeSpecificityScore measures how concrete a type is, for "most concrete wins"
// overload ranking. A bare type parameter (wildcard) scores 0; a concrete leaf
// scores 1; a structured type scores 1 plus the score of its components, so a fully
// bound generic instance (`Box[i64,i64]`, score 3) outscores a partially generic one
// (`Box[i64,B]`, score 2) which outscores a fully generic one (`Box[A,B]`, score 1).
func typeSpecificityScore(t Type) int {
	switch n := t.(type) {
	case nil:
		return 0
	case *TypeParamType:
		return 0
	case *RefType:
		return typeSpecificityScore(n.Elem)
	case *AggregateStateType:
		return typeSpecificityScore(n.Base)
	case *OptionalType:
		return 1 + typeSpecificityScore(n.Value)
	case *ArrayType:
		return 1 + typeSpecificityScore(n.Elem)
	case *DArrayType:
		return 1 + typeSpecificityScore(n.Elem)
	case *ViewType:
		return 1 + typeSpecificityScore(n.Elem)
	case *TupleType:
		score := 1
		for _, field := range n.Fields {
			score += typeSpecificityScore(field.Type)
		}
		return score
	case *GenericInstanceType:
		score := 1
		for _, arg := range n.Args {
			score += typeSpecificityScore(arg)
		}
		return score
	default:
		return 1
	}
}

func (a *Analyzer) ufcsReceiverAssignableTo(expected Type, actual Type) bool {
	if a == nil || expected == nil || actual == nil {
		return false
	}
	// A raw u8 string pointer (the type of a string literal, `static u8&`) serves
	// as a cstr / string-view receiver, mirroring the contextual string-literal
	// coercion used in ordinary argument position. This makes `"x".open()` resolve
	// to `def open(s: cstr)` the same way the free call `open("x")` already does.
	if _, ok := u8RuntimeRef(actual); ok {
		if _, ok := contextualStringLiteralType(expected); ok {
			return true
		}
	}
	// A bare integer literal has the untyped `int` type; let it serve as a receiver
	// of any integer-typed parameter (the literal is re-typed when prepended as the
	// argument), mirroring ordinary argument coercion so `7.inc()` works. Float
	// literals already get a concrete type (f64) and need no special case.
	if an, ok := builtinNumericTypeName(actual); ok && an == "int" {
		if en, ok := builtinNumericTypeName(expected); ok && en != "f32" && en != "f64" {
			return true
		}
	}
	expectedBase := expected
	if expectedRef, ok := expected.(*RefType); ok && expectedRef != nil {
		expectedBase = expectedRef.Elem
	}
	actualBase := actual
	if actualRef, ok := actual.(*RefType); ok && actualRef != nil {
		actualBase = actualRef.Elem
	}
	if !ufcsReceiverBaseCompatible(expectedBase, actualBase) {
		return false
	}
	if SameType(expected, actual) {
		return true
	}
	if AssignableTo(expected, actual) {
		return true
	}
	expectedRef, ok := expected.(*RefType)
	if !ok || expectedRef == nil {
		return false
	}
	if actualRef, ok := actual.(*RefType); ok && actualRef != nil {
		if ufcsReceiverBaseCompatible(expectedRef.Elem, actualRef.Elem) {
			if expectedRef.Mutable && !actualRef.Mutable {
				return false
			}
			return expectedRef.State == RefStateNonNull || expectedRef.State == RefStateNullable
		}
	}
	if upcastType, ok := implicitCallLikeRefUpcastType(expectedRef, actual); ok && SameType(upcastType, expected) {
		return true
	}
	if !ufcsReceiverBaseCompatible(expectedRef.Elem, actual) {
		return false
	}
	return expectedRef.State == RefStateNonNull || expectedRef.State == RefStateNullable
}

func ufcsReceiverBaseCompatible(expected Type, actual Type) bool {
	if expected == nil || actual == nil {
		return false
	}
	expected = StripAggregateStateType(expected)
	actual = StripAggregateStateType(actual)
	if SameType(expected, actual) {
		return true
	}
	if matchTypePattern(expected, actual) || assignableRuntimeCompatible(expected, actual) {
		return true
	}
	switch exp := expected.(type) {
	case *GenericInstanceType:
		act, ok := actual.(*GenericInstanceType)
		if !ok || act == nil {
			return false
		}
		return exp.Name == act.Name && SameType(exp.Base, act.Base)
	case *StructType:
		switch act := actual.(type) {
		case *StructType:
			return SameType(exp, act)
		case *GenericInstanceType:
			return SameType(exp, act.Base)
		}
	}
	return TypeIdentityKey(expected) == TypeIdentityKey(actual)
}

func receiverOverloadType(sym *Symbol) (Type, bool) {
	if sym == nil {
		return nil, false
	}
	if sym.Kind != SymbolFunc && sym.Kind != SymbolExternFunc {
		return nil, false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || len(fnType.Params) == 0 {
		return nil, false
	}
	return fnType.Params[0], true
}

func (a *Analyzer) registerUFCSFunction(visibleName string, sym *Symbol) {
	if a == nil || visibleName == "" || sym == nil {
		return
	}
	// Track every function under its visible name, INCLUDING zero-param functions.
	// Zero-param functions are not UFCS-callable (lookupVisibleUFCSFunction skips
	// entries without a receiver), but they must still count toward the overload set
	// so that a free `f()` coexisting with a method `f(x)` registers as an overload
	// (the >= 2 entries signal) and `f(arg)` reaches receiver-based disambiguation
	// instead of latching onto the zero-param bare global.
	if sym.Kind != SymbolFunc && sym.Kind != SymbolExternFunc {
		return
	}
	if _, ok := sym.Type.(*FuncType); !ok {
		return
	}
	methods := a.ufcsFunctionsByName[visibleName]
	for _, existing := range methods {
		if existing == sym {
			return
		}
	}
	a.ufcsFunctionsByName[visibleName] = append(methods, sym)
}
