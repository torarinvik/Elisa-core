package semantic

import (
	"elisacore/src/ast"
	"strings"
)

func poststateTargetDisplayName(rootName string, steps []borrowReturnAnnotationStep) string {
	if rootName == "" {
		return "<value>"
	}
	return rootName + borrowAnnotationPathSuffix(steps)
}

func poststateNamedStateCurrentType(t Type) (Type, bool) {
	switch tt := t.(type) {
	case *AggregateStateType:
		return poststateNamedStateCurrentType(tt.Base)
	case *RefType:
		return poststateNamedStateCurrentType(tt.Elem)
	default:
		if _, ok := namedStateStructBase(t); ok {
			return t, true
		}
		return nil, false
	}
}

func (a *Analyzer) currentFuncParamSymbol(index int) (*Symbol, bool) {
	if a == nil || a.currentFuncDecl == nil || a.currentScope == nil || index < 0 {
		return nil, false
	}
	params := append([]ast.ParamDecl(nil), a.currentFuncDecl.Params...)
	if index >= len(params) {
		return nil, false
	}
	name := params[index].Name
	if name == "" {
		return nil, false
	}
	sym, ok := a.currentScope.Lookup(name)
	return sym, ok && sym != nil
}

func (a *Analyzer) validateCurrentFuncPoststates() {
	a.validateCurrentFuncPoststatesForOutcome(false, false)
	// The synthetic exit (value-less return / fall-through) also discharges boolean `ensure` clauses
	// over params — the explicit-return path handles those via dischargeEnsureBooleans.
	if a.currentFuncDecl != nil {
		a.dischargeEnsureBooleansAtVoidExit(a.currentFuncDecl.Pos())
		// All discharge (explicit-return + void-exit) has now run, so ghost-field-referencing
		// contract clauses can be dropped from the codegen-visible set without losing the proof.
		a.stripGhostFieldContractsForRuntime()
	}
}

func funcPoststatesHaveConditional(poststates []FuncPoststate) bool {
	for _, poststate := range poststates {
		if poststate.Condition.Kind != FuncPoststateConditionAlways {
			return true
		}
	}
	return false
}

func funcPoststateAppliesForOutcome(poststate FuncPoststate, outcomeKnown bool, outcomeValue bool) bool {
	switch poststate.Condition.Kind {
	case FuncPoststateConditionReturnBool:
		return outcomeKnown && poststate.Condition.ReturnBool == outcomeValue
	default:
		return true
	}
}

func (a *Analyzer) validateCurrentFuncPoststatesForReturnValue(value ast.Expr) {
	if a == nil || a.currentFuncType == nil || !funcPoststatesHaveConditional(a.currentFuncType.Poststates) {
		a.validateCurrentFuncPoststates()
		return
	}
	outcomeValue, outcomeKnown := a.evalConstBoolExpr(value)
	if !outcomeKnown {
		a.errorf(value.Pos(), "functions with branch-sensitive ensures must return literal true or false on each return path")
	}
	a.validateCurrentFuncPoststatesForOutcome(outcomeKnown, outcomeValue)
}

func (a *Analyzer) validateCurrentFuncPoststatesForOutcome(outcomeKnown bool, outcomeValue bool) {
	if a == nil || a.suppressDiagnostics || a.currentFuncDecl == nil || a.currentFuncType == nil || len(a.currentFuncType.Poststates) == 0 {
		return
	}
	for _, poststate := range a.currentFuncType.Poststates {
		if !funcPoststateAppliesForOutcome(poststate, outcomeKnown, outcomeValue) {
			continue
		}
		a.validateCurrentFuncPoststate(poststate)
	}
}

func (a *Analyzer) validateCurrentFuncPoststate(poststate FuncPoststate) {
	sym, ok := a.currentFuncParamSymbol(poststate.ParamIndex)
	if !ok || sym == nil {
		return
	}
	targetName := poststateTargetDisplayName(sym.Name, poststate.Path)
	currentTarget, ok := a.projectTrackedValueTypeAtPath(a.currentTrackedValueType(sym), poststate.Path)
	if !ok || currentTarget == nil {
		a.errorf(poststate.Position, ensureTargetUnresolvedMessage(targetName, a.currentFuncDecl.Name))
		return
	}
	switch poststate.Kind {
	case FuncPoststateKindPreserve:
		for _, widening := range a.currentConservativeCallWidenings[sym] {
			if poststatePathsOverlap(widening.Path, poststate.Path) {
				if poststate.Implicit {
					a.errorf(poststate.Position, implicitPreserveWidenedMessage(targetName, a.currentFuncDecl.Name, widening.Source))
					return
				}
				a.errorf(poststate.Position, ensurePreserveWidenedMessage(targetName, a.currentFuncDecl.Name, widening.Source))
				return
			}
		}
		originalTarget, ok := a.projectTrackedValueTypeAtPath(sym.Type, poststate.Path)
		if !ok || originalTarget == nil {
			a.errorf(poststate.Position, ensureIncomingTargetUnresolvedMessage(targetName, a.currentFuncDecl.Name))
			return
		}
		if !SameType(currentTarget, originalTarget) {
			if poststate.Implicit {
				a.errorf(poststate.Position, implicitPreserveMismatchMessage(targetName, a.currentFuncDecl.Name, originalTarget.String(), currentTarget.String()))
				return
			}
			a.errorf(poststate.Position, ensurePreserveMismatchMessage(targetName, a.currentFuncDecl.Name, currentTarget.String()))
		}
	case FuncPoststateKindNamedState:
		actualTarget, ok := poststateNamedStateCurrentType(currentTarget)
		if !ok || actualTarget == nil {
			a.errorf(poststate.Position, ensureNamedStateTargetMessage(targetName, a.currentFuncDecl.Name))
			return
		}
		base, ok := namedStateStructBase(actualTarget)
		if !ok || base == nil {
			a.errorf(poststate.Position, ensureNamedStateTargetMessage(targetName, a.currentFuncDecl.Name))
			return
		}
		desired := newNamedStateType(base.Name, base.NamedStateCases, poststate.StateCases)
		actualState, ok := namedStateCurrentArg(actualTarget)
		if desired == nil || !ok || actualState == nil || !namedStateTypeAssignable(desired, actualState) {
			a.errorf(poststate.Position, ensureNamedStateMismatchMessage(targetName, strings.Join(poststate.StateCases, " | "), a.currentFuncDecl.Name, actualTarget.String()))
			return
		}
		a.recordProofWithClass(poststate.Position, targetName, "=> "+strings.Join(poststate.StateCases, " | "), ProofProvenContract, ProofClassTypestate, nil, "")
	case FuncPoststateKindRefState:
		actualRef, ok := poststateRefTargetType(currentTarget)
		if !ok || actualRef == nil {
			a.errorf(poststate.Position, ensureRefStateTargetMessage(targetName, a.currentFuncDecl.Name))
			return
		}
		if !refStateAssignable(poststate.RefState, actualRef.State) {
			a.errorf(poststate.Position, ensureRefStateMismatchMessage(targetName, ast.RefStateMarker(ast.RefState(poststate.RefState)), a.currentFuncDecl.Name, currentTarget.String()))
		}
	}
}

func isVoidType(t Type) bool {
	builtin, ok := t.(*BuiltinType)
	return ok && builtin.Name == "void"
}
