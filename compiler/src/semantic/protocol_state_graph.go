package semantic

import (
	"strings"

	"elisacore/src/ast"
)

// Only inline storage is visited: empty dynamic containers and absent optionals
// do not manufacture their element values. Bound recursive generic expansion.
func (a *Analyzer) zeroedContainsProtocolState(t Type, depth int) bool {
	if depth > 64 {
		return true // An unresolved recursive layout is not evidence of safety.
	}
	t = StripAggregateStateType(t)
	switch value := t.(type) {
	case *StructType:
		if value.ProtocolStates {
			return true
		}
		for _, field := range value.Fields {
			if !field.Ghost && !field.Phantom && a.zeroedContainsProtocolState(field.Type, depth+1) {
				return true
			}
		}
	case *GenericInstanceType:
		if base, ok := value.Base.(*StructType); ok {
			if base.ProtocolStates {
				return true
			}
			bindings := genericBindingsForStructInstance(base, value.Args)
			regions := regionBindingsForStructInstance(base, value.Args)
			for _, field := range base.Fields {
				if !field.Ghost && !field.Phantom && a.zeroedContainsProtocolState(a.substituteType(field.Type, bindings, nil, regions, nil), depth+1) {
					return true
				}
			}
		}
	case *ArrayType:
		return a.zeroedContainsProtocolState(value.Elem, depth+1)
	case *TupleType:
		for _, field := range value.Fields {
			if a.zeroedContainsProtocolState(field.Type, depth+1) {
				return true
			}
		}
	case *EnumType:
		for _, variant := range value.Variants {
			if variant.Tag == 0 {
				for _, payload := range variant.Payload {
					if a.zeroedContainsProtocolState(payload, depth+1) {
						return true
					}
				}
			}
		}
	}
	return false
}

func (a *Analyzer) validateProtocolStateGraph(decl *ast.StructDecl, st *StructType) {
	st.ProtocolStates = true
	st.StateTransitions = append([]ast.StateTransitionDecl(nil), decl.StateTransitions...)
	declared := make(map[string]bool, len(decl.NamedStateCases))
	for _, state := range decl.NamedStateCases {
		declared[state] = true
	}
	seen := map[[2]string]bool{}
	for _, edge := range decl.StateTransitions {
		if !declared[edge.From] || !declared[edge.To] {
			a.errorf(edge.Position, "protocol transition %s -> %s names an undeclared state of %q", edge.From, edge.To, st.Name)
		}
		key := [2]string{edge.From, edge.To}
		if seen[key] {
			a.errorf(edge.Position, "duplicate protocol transition %s -> %s in %q", edge.From, edge.To, st.Name)
		}
		seen[key] = true
	}
	for _, field := range decl.Fields {
		if field.Name == "__typestate" {
			a.errorf(field.Position, "protocol struct %q cannot declare the legacy __typestate discriminator", st.Name)
		}
	}
}

// This is the existing module privacy domain, not a function allowlist.
func (a *Analyzer) hasProtocolStateAuthority(st *StructType) bool {
	return st != nil && (a.currentNamespace == st.Namespace ||
		(st.Namespace != "" && strings.HasPrefix(a.currentNamespace, st.Namespace+".")))
}

func (a *Analyzer) protocolStructLiteralType(expr *ast.StructLitExpr, base *StructType, target Type) Type {
	if !a.hasProtocolStateAuthority(base) {
		a.errorf(expr.Pos(), "protocol construction of %q is private to its owning module", base.Name)
		return invalidType
	}
	initial := newNamedStateType(base.Name, base.NamedStateCases, base.NamedStateCases[:1])
	desired, ok := namedStateCurrentArg(target)
	if !ok || (!sameNamedStateType(desired, initial) && !sameNamedStateType(desired, fullNamedStateType(base))) {
		a.errorf(expr.Pos(), "protocol struct %q can only be constructed in initial state %s; use a legal consuming transition", base.Name, base.NamedStateCases[0])
		return invalidType
	}
	return instantiateNamedStateStructLiteralType(base, target, initial)
}

// transition[To](move owned) is a checked intrinsic, not a generated operation.
// Other functions (including user functions named transition) remain ordinary calls.
func (a *Analyzer) analyzeProtocolTransitionCall(call *ast.CallExpr) (Type, bool) {
	specialized, ok := call.Func.(*ast.SpecializeExpr)
	if !ok {
		return nil, false
	}
	name, ok := specialized.Operand.(*ast.Ident)
	if !ok || name.Name != "transition" {
		return nil, false
	}
	if _, _, found := a.lookupVisibleGlobal(name.Name); found {
		return nil, false
	}
	if a.currentScope != nil {
		if _, found := a.currentScope.Lookup(name.Name); found {
			return nil, false
		}
	}
	if len(specialized.TypeArgs) != 1 || len(call.Args) != 1 || (len(call.ArgNames) != 0 && call.ArgNames[0] != "") {
		a.errorf(call.Pos(), "transition[State] requires exactly one positional explicitly moved value")
		return invalidType, true
	}
	target, ok := specialized.TypeArgs[0].(*ast.NamedType)
	if !ok {
		a.errorf(call.Pos(), "transition target must be a declared protocol state")
		return invalidType, true
	}
	moved, ok := call.Args[0].(*ast.MoveExpr)
	if !ok {
		a.errorf(call.Pos(), "protocol transition requires explicit move of an owned value")
		return invalidType, true
	}
	// Start with a resolved owned local or parameter, not a borrowed projection.
	if _, ok := moved.Operand.(*ast.Ident); !ok {
		a.errorf(call.Pos(), "protocol transition requires an owned local or parameter")
		return invalidType, true
	}
	source := a.analyzeExpr(call.Args[0])
	instance, ok := source.(*GenericInstanceType)
	if !ok || instance == nil {
		a.errorf(call.Pos(), "protocol transition requires an owned state-qualified struct value")
		return invalidType, true
	}
	base, ok := instance.Base.(*StructType)
	if !ok || !base.ProtocolStates || !base.Affine {
		a.errorf(call.Pos(), "protocol transition requires a linear or affine predicate-free state family")
		return invalidType, true
	}
	if !a.hasProtocolStateAuthority(base) {
		a.errorf(call.Pos(), "protocol transition of %q is private to its owning module", base.Name)
		return invalidType, true
	}
	targetState := newNamedStateType(base.Name, base.NamedStateCases, []string{target.Name})
	if targetState == nil {
		a.errorf(call.Pos(), "unknown protocol state %q for %q", target.Name, base.Name)
		return invalidType, true
	}
	sourceState, _ := namedStateCurrentArg(source)
	cases, _, known := namedStateTypeCases(sourceState)
	if !known || len(cases) == 0 {
		a.errorf(call.Pos(), "protocol transition has no known source state")
		return invalidType, true
	}
	for _, from := range cases {
		legal := from == target.Name // Preservation does not need a self-edge.
		for _, edge := range base.StateTransitions {
			legal = legal || (edge.From == from && edge.To == target.Name)
		}
		if !legal {
			a.errorf(call.Pos(), "illegal protocol transition %s -> %s for %q", from, target.Name, base.Name)
			return invalidType, true
		}
	}
	index := namedStateArgIndex(base)
	if index < 0 || index >= len(instance.Args) {
		a.errorf(call.Pos(), "protocol transition has an unresolved state parameter")
		return invalidType, true
	}
	a.consumeAffineValueExpr(call.Args[0], source, "protocol state transition")
	a.recordCompilerBuiltinHelperCall(call, "transition")
	result := *instance
	result.Args = append([]Type(nil), instance.Args...)
	result.Args[index] = targetState
	return &result, true
}
