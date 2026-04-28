package semantic

import (
	"strconv"
	"strings"

	"llcontext/src/ast"
	"llcontext/src/lexer"
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
	if expr == nil {
		return nil, nil, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.namedStateMutationTargetPath(n.Inner)
	case *ast.CastExpr:
		return a.namedStateMutationTargetPath(n.Operand)
	case *ast.MoveExpr:
		return a.namedStateMutationTargetPath(n.Operand)
	case *ast.CanExpr:
		return a.namedStateMutationTargetPath(n.Expr)
	case *ast.AddrOfExpr:
		return a.namedStateMutationTargetPath(n.Operand)
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
				if root, steps, ok := a.namedStateMutationTargetPath(valueExpr); ok {
					return root, steps, true
				}
			}
			if root := symbolAliasRoot(sym); root != nil && root != sym {
				if valueExpr, ok := a.currentValueBindings[root]; ok && valueExpr != nil {
					if resolvedRoot, steps, ok := a.namedStateMutationTargetPath(valueExpr); ok {
						return resolvedRoot, steps, true
					}
				}
			}
		}
		return sym, nil, true
	case *ast.FieldExpr:
		root, steps, ok := a.namedStateMutationTargetPath(n.Object)
		if !ok || root == nil {
			return nil, nil, false
		}
		return root, append(steps, borrowReturnAnnotationStep{Field: n.Field}), true
	case *ast.IndexExpr:
		root, steps, ok := a.namedStateMutationTargetPath(n.Object)
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

func (a *Analyzer) inferDirectFieldAssignedNamedState(pos lexer.Pos, root *Symbol, structSteps []borrowReturnAnnotationStep, base *StructType, fieldName string, value ast.Expr) (Type, bool) {
	if a == nil || root == nil || base == nil || base.Decl == nil || len(base.NamedStateCases) == 0 {
		return nil, false
	}
	rootExpr, ok := namedStateTargetPathExpr(pos, root, structSteps)
	if !ok || rootExpr == nil {
		return nil, false
	}
	targetName := namedStateTargetDisplayName(root, structSteps)
	fieldValues := make(map[string]ast.Expr, len(base.Decl.Fields))
	for _, fieldDecl := range base.Decl.Fields {
		if fieldDecl.Name == fieldName {
			fieldValues[fieldDecl.Name] = value
			continue
		}
		fieldValues[fieldDecl.Name] = &ast.FieldExpr{Position: pos, Object: rootExpr, Field: fieldDecl.Name}
	}
	trueStates := make([]string, 0, len(base.NamedStateCases))
	for _, stateName := range base.NamedStateCases {
		proven, holds := a.evaluateDerivedStateForFields(base, stateName, fieldValues)
		if !proven {
			return nil, false
		}
		if holds {
			trueStates = append(trueStates, stateName)
		}
	}
	switch len(trueStates) {
	case 1:
		return newNamedStateType(base.Name, base.NamedStateCases, trueStates), true
	case 0:
		a.errorf(pos, "assignment to %q leaves %q in no derived state", fieldName, targetName)
		return fullNamedStateType(base), true
	default:
		a.errorf(pos, "assignment to %q leaves %q satisfying multiple derived states: %s", fieldName, targetName, strings.Join(trueStates, ", "))
		return fullNamedStateType(base), true
	}
}
