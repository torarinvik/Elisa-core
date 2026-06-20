package semantic

import (
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func cloneBorrowReturnAnnotationSteps(steps []borrowReturnAnnotationStep) []borrowReturnAnnotationStep {
	if len(steps) == 0 {
		return nil
	}
	cloned := make([]borrowReturnAnnotationStep, len(steps))
	for i, step := range steps {
		cloned[i] = cloneBorrowReturnAnnotationStep(step)
	}
	return cloned
}

func joinBorrowReturnAnnotationSteps(prefix []borrowReturnAnnotationStep, suffix []borrowReturnAnnotationStep) []borrowReturnAnnotationStep {
	if len(prefix) == 0 {
		return cloneBorrowReturnAnnotationSteps(suffix)
	}
	if len(suffix) == 0 {
		return cloneBorrowReturnAnnotationSteps(prefix)
	}
	joined := make([]borrowReturnAnnotationStep, 0, len(prefix)+len(suffix))
	joined = append(joined, cloneBorrowReturnAnnotationSteps(prefix)...)
	joined = append(joined, cloneBorrowReturnAnnotationSteps(suffix)...)
	return joined
}

func borrowReturnAnnotationPathEqual(left []borrowReturnAnnotationStep, right []borrowReturnAnnotationStep) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !borrowReturnAnnotationStepsEqual(left[i], right[i]) {
			return false
		}
	}
	return true
}

func poststatePathsOverlap(left []borrowReturnAnnotationStep, right []borrowReturnAnnotationStep) bool {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		l := left[i]
		r := right[i]
		switch {
		case l.Field != "" || r.Field != "":
			if l.Field == "" || r.Field == "" || l.Field != r.Field {
				return false
			}
		case l.Wildcard || r.Wildcard:
			continue
		case l.Index != nil || r.Index != nil:
			if l.Index == nil || r.Index == nil || *l.Index != *r.Index {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (a *Analyzer) noteConservativeCallWidening(root *Symbol, steps []borrowReturnAnnotationStep, source string, sourcePos lexer.Pos, reason string, before string, after string) {
	if a == nil || a.currentConservativeCallWidenings == nil || root == nil {
		return
	}
	cloned := cloneBorrowReturnAnnotationSteps(steps)
	for _, existing := range a.currentConservativeCallWidenings[root] {
		if borrowReturnAnnotationPathEqual(existing.Path, cloned) && existing.Source == source && existing.Reason == reason && existing.Before == before && existing.After == after {
			return
		}
	}
	a.currentConservativeCallWidenings[root] = append(a.currentConservativeCallWidenings[root], conservativeCallWidening{Path: cloned, Source: source, SourcePos: sourcePos, Reason: reason, Before: before, After: after})
}

func conservativeCallWideningSource(call *ast.CallExpr, arg ast.Expr, paramIndex int) string {
	name := callIdentName(call)
	if name == "" {
		name = "call"
	}
	argText := flowLocationForExpr(arg)
	if argText == "" && paramIndex >= 0 {
		argText = "arg" + strconv.FormatInt(int64(paramIndex+1), 10)
	}
	if argText == "" {
		return name
	}
	return name + "(" + argText + ")"
}

func namedStateTargetDisplayName(root *Symbol, steps []borrowReturnAnnotationStep) string {
	if root == nil || root.Name == "" {
		return "<value>"
	}
	var b strings.Builder
	b.WriteString(root.Name)
	for _, step := range steps {
		switch {
		case step.Field != "":
			b.WriteString(".")
			b.WriteString(step.Field)
		case step.Index != nil:
			b.WriteString("[")
			b.WriteString(strconv.FormatInt(*step.Index, 10))
			b.WriteString("]")
		case step.Wildcard:
			b.WriteString("[*]")
		}
	}
	return b.String()
}

func namedStateTargetPathExpr(pos lexer.Pos, root *Symbol, steps []borrowReturnAnnotationStep) (ast.Expr, bool) {
	if root == nil || root.Name == "" {
		return nil, false
	}
	var expr ast.Expr = &ast.Ident{Position: pos, Name: root.Name}
	for _, step := range steps {
		switch {
		case step.Field != "":
			expr = &ast.FieldExpr{Position: pos, Object: expr, Field: step.Field}
		case step.Index != nil:
			expr = &ast.IndexExpr{Position: pos, Object: expr, Index: &ast.IntLit{Position: pos, Value: strconv.FormatInt(*step.Index, 10)}}
		default:
			return nil, false
		}
	}
	return expr, true
}

func (a *Analyzer) namedStateMutationTargetPath(expr ast.Expr) (*Symbol, []borrowReturnAnnotationStep, bool) {
	return a.namedStateMutationTargetPathDepth(expr, 0)
}

func (a *Analyzer) namedStateMutationTargetPathDepth(expr ast.Expr, depth int) (*Symbol, []borrowReturnAnnotationStep, bool) {
	if expr == nil {
		return nil, nil, false
	}
	if depth > 64 {
		return nil, nil, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.namedStateMutationTargetPathDepth(n.Inner, depth+1)
	case *ast.CastExpr:
		return a.namedStateMutationTargetPathDepth(n.Operand, depth+1)
	case *ast.MoveExpr:
		return a.namedStateMutationTargetPathDepth(n.Operand, depth+1)
	case *ast.CanExpr:
		return a.namedStateMutationTargetPathDepth(n.Expr, depth+1)
	case *ast.AddrOfExpr:
		return a.namedStateMutationTargetPathDepth(n.Operand, depth+1)
	case *ast.Ident:
		if a.currentScope == nil {
			return nil, nil, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil {
			return nil, nil, false
		}
		if _, isRef := sym.Type.(*RefType); isRef && a.currentValueBindings != nil {
			if valueExpr, ok := a.currentValueBindings[sym]; ok && valueExpr != nil {
				if root, steps, ok := a.namedStateMutationTargetPathDepth(valueExpr, depth+1); ok {
					return root, steps, true
				}
			}
			if root := symbolAliasRoot(sym); root != nil && root != sym {
				if valueExpr, ok := a.currentValueBindings[root]; ok && valueExpr != nil {
					if resolvedRoot, steps, ok := a.namedStateMutationTargetPathDepth(valueExpr, depth+1); ok {
						return resolvedRoot, steps, true
					}
				}
			}
		}
		return sym, nil, true
	case *ast.FieldExpr:
		root, steps, ok := a.namedStateMutationTargetPathDepth(n.Object, depth+1)
		if !ok || root == nil {
			return nil, nil, false
		}
		return root, append(steps, borrowReturnAnnotationStep{Field: n.Field}), true
	case *ast.IndexExpr:
		root, steps, ok := a.namedStateMutationTargetPathDepth(n.Object, depth+1)
		if !ok || root == nil {
			return nil, nil, false
		}
		step, ok := a.resolveProjectedFieldIndexStep(n.Index)
		if !ok {
			step = borrowReturnAnnotationStep{Wildcard: true}
		}
		return root, append(steps, step), true
	default:
		return nil, nil, false
	}
}

func namedStateAssignmentFieldPrefix(steps []borrowReturnAnnotationStep) []string {
	fields := make([]string, 0, len(steps))
	for _, step := range steps {
		if step.Field == "" {
			break
		}
		fields = append(fields, step.Field)
	}
	return fields
}

func namedStatePathsOverlap(left []string, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func namedStateDerivedExprDependsOnPath(expr ast.Expr, fields []string) bool {
	if len(fields) == 0 || expr == nil {
		return false
	}
	if path, ok := derivedStateSelfFieldPath(expr); ok {
		return namedStatePathsOverlap(path, fields)
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return namedStateDerivedExprDependsOnPath(n.Inner, fields)
	case *ast.UnaryExpr:
		return namedStateDerivedExprDependsOnPath(n.Operand, fields)
	case *ast.BinaryExpr:
		return namedStateDerivedExprDependsOnPath(n.Left, fields) || namedStateDerivedExprDependsOnPath(n.Right, fields)
	default:
		return false
	}
}

func namedStateAssignmentAffectsDerivedState(base *StructType, steps []borrowReturnAnnotationStep) bool {
	if base == nil || len(base.DerivedStates) == 0 {
		return false
	}
	if len(steps) == 0 {
		return true
	}
	fields := namedStateAssignmentFieldPrefix(steps)
	if len(fields) == 0 {
		return true
	}
	for _, state := range base.DerivedStates {
		if namedStateDerivedExprDependsOnPath(state.Condition, fields) {
			return true
		}
	}
	return false
}

func (a *Analyzer) inferDirectFieldAssignedNamedState(pos lexer.Pos, root *Symbol, structSteps []borrowReturnAnnotationStep, base *StructType, fieldName string, value ast.Expr, current Type) (Type, bool) {
	if a == nil || root == nil || base == nil || base.Decl == nil || len(base.NamedStateCases) == 0 {
		return nil, false
	}
	rootExpr, ok := namedStateTargetPathExpr(pos, root, structSteps)
	if !ok || rootExpr == nil {
		return nil, false
	}
	targetName := namedStateTargetDisplayName(root, structSteps)
	// Old-value reads for every field (`root.field`), used for the unchanged fields in the
	// post-assignment snapshot and for the entry hypothesis. exprTypes is seeded on each synthetic node
	// so the SMT translator can model the field as a numeric projection symbol (these nodes are never
	// analyzed, so their per-node type would otherwise be unset).
	oldFields := make(map[string]ast.Expr, len(base.Decl.Fields))
	for _, fieldDecl := range base.Decl.Fields {
		fe := &ast.FieldExpr{Position: pos, Object: rootExpr, Field: fieldDecl.Name}
		if f, ok := base.Fields[fieldDecl.Name]; ok && f.Type != nil {
			a.exprTypes[fe] = f.Type
		}
		oldFields[fieldDecl.Name] = fe
	}
	// Post-assignment snapshot: the written field takes the new value, the rest keep their old reads.
	fieldValues := make(map[string]ast.Expr, len(oldFields))
	for name, expr := range oldFields {
		fieldValues[name] = expr
	}
	fieldValues[fieldName] = value

	// Entry hypothesis: if the value is currently pinned to a SINGLE derived state, that state's
	// condition holds on the OLD field values — the fact that lets the prover see `health > 0 ⟹
	// health + 1 > 0` and keep the state narrow. A multi-state current type carries no usable fact.
	var hypExpr ast.Expr
	if cur, ok := trackedNamedStateCurrentArg(peelNamedStateRefs(current)); ok {
		if cases, _, ok := namedStateTypeCases(cur); ok && len(cases) == 1 {
			if derived := base.DerivedStateMap[cases[0]]; derived != nil && derived.Condition != nil {
				if h, ok := substituteDerivedStateFieldExpr(derived.Condition, oldFields); ok {
					hypExpr = h
				}
			}
		}
	}

	possible := make([]string, 0, len(base.NamedStateCases))
	constHolds := make([]string, 0, len(base.NamedStateCases))
	constDecidedAll := true
	for _, stateName := range base.NamedStateCases {
		proven, holds := a.evaluateDerivedStateForFields(base, stateName, fieldValues)
		if proven {
			if holds {
				possible = append(possible, stateName)
				constHolds = append(constHolds, stateName)
			}
			continue
		}
		constDecidedAll = false
		// Const-eval could not decide this state. Try to PROVE it impossible under the entry hypothesis;
		// a state we cannot exclude is kept (sound — the result set only ever over-approximates).
		if hypExpr != nil && a.derivedStateProvablyExcluded(base, stateName, fieldValues, hypExpr) {
			continue
		}
		possible = append(possible, stateName)
	}
	if constDecidedAll {
		// Every state decided by constant evaluation — the original exact semantics, including the
		// genuine "no state" / "multiple states" errors a known-value assignment can trigger.
		switch len(constHolds) {
		case 1:
			return newNamedStateType(base.Name, base.NamedStateCases, constHolds), true
		case 0:
			a.errorf(pos, "assignment to %q leaves %q in no derived state", fieldName, targetName)
			return fullNamedStateType(base), true
		default:
			a.errorf(pos, "assignment to %q leaves %q satisfying multiple derived states: %s", fieldName, targetName, strings.Join(constHolds, ", "))
			return fullNamedStateType(base), true
		}
	}
	// SMT refinement was involved: narrow only when exactly one state survives exclusion (sound by the
	// exhaustiveness of derived states — every other state proven impossible). Otherwise leave it to the
	// caller to widen (no error: an undecidable assignment is not a definite bug).
	if len(possible) == 1 {
		return newNamedStateType(base.Name, base.NamedStateCases, possible), true
	}
	return nil, false
}

// peelNamedStateRefs strips reference and aggregate-state wrappers so the underlying named-state type
// (and its current state cases) is reachable — the tracked type of a `mutable T[S]&` param is a ref.
func peelNamedStateRefs(t Type) Type {
	for {
		switch tt := t.(type) {
		case *RefType:
			if tt == nil {
				return t
			}
			t = tt.Elem
		case *AggregateStateType:
			if tt == nil {
				return t
			}
			t = tt.Base
		default:
			return t
		}
	}
}

// derivedStateProvablyExcluded proves that, under the entry hypothesis `hyp` (the current single state's
// condition on the old field values), the candidate state's condition CANNOT hold on the post-assignment
// field values — i.e. the assignment provably leaves that state behind. Only `unsat` of `hyp ∧ condition`
// concludes (sound; an undecidable state is never excluded). Off (declines) unless -smt.
func (a *Analyzer) derivedStateProvablyExcluded(base *StructType, stateName string, fieldValues map[string]ast.Expr, hyp ast.Expr) bool {
	derived := base.DerivedStateMap[stateName]
	if derived == nil || derived.Condition == nil {
		return false
	}
	condExpr, ok := substituteDerivedStateFieldExpr(derived.Condition, fieldValues)
	if !ok {
		return false
	}
	tr := a.newSMTTranslator(nil)
	condTerm, ok := tr.boolTerm(condExpr, nil)
	if !ok {
		return false
	}
	hypTerm, ok := tr.boolTerm(hyp, nil)
	if !ok {
		return false
	}
	// Prove `¬condition` under the hypothesis: smtCheckVC negates the obligation (asserting `condition`)
	// alongside `hyp`; unsat means `hyp ∧ condition` is contradictory, so the state is impossible after
	// the assignment.
	proven, _ := a.smtCheckVC(tr, "(not "+condTerm+")", "(assert "+hypTerm+")\n")
	return proven
}
