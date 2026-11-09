package semantic

import (
	"elisacore/src/ast"
	"strconv"
)

func (a *Analyzer) resolveProjectedFieldIndexStep(indexExpr ast.Expr) (borrowReturnAnnotationStep, bool) {
	if a == nil || indexExpr == nil {
		return borrowReturnAnnotationStep{}, false
	}
	index, ok := a.resolveProjectedFieldConstIntExpr(indexExpr)
	if !ok || index < 0 {
		return borrowReturnAnnotationStep{}, false
	}
	return borrowReturnAnnotationStep{Index: &index}, true
}
func (a *Analyzer) resolveProjectedFieldConstIntExpr(expr ast.Expr) (int64, bool) {
	if a == nil || expr == nil {
		return 0, false
	}
	value, ok := a.evalConstExpr(expr)
	if !ok || value.Kind != ConstInt {
		return 0, false
	}
	return value.Int, true
}
func (a *Analyzer) resolveProjectedFieldValueThroughIndexOffset(base ast.Expr, offset int64, path []borrowReturnAnnotationStep) (ast.Expr, bool) {
	if a == nil || base == nil || len(path) == 0 {
		return nil, false
	}
	step := path[0]
	if step.Index == nil || step.Field != "" || step.Wildcard {
		return nil, false
	}
	index := offset + *step.Index
	if index < 0 {
		return nil, false
	}
	indexCopy := index
	combinedPath := make([]borrowReturnAnnotationStep, 0, len(path))
	combinedPath = append(combinedPath, borrowReturnAnnotationStep{Index: &indexCopy})
	combinedPath = append(combinedPath, path[1:]...)
	return a.resolveProjectedFieldValueExprAtPath(base, combinedPath)
}
func (a *Analyzer) resolveProjectedFieldValueFromBuiltinViewHelperCall(call *ast.CallExpr, path []borrowReturnAnnotationStep) (ast.Expr, bool) {
	if call == nil || len(path) == 0 {
		return nil, false
	}
	helperName := optimizationHelperName(call.Func)
	if !a.isCompilerBuiltinHelperCall(call, helperName) {
		return nil, false
	}
	switch helperName {
	case "arena_da_from_view":
		if len(call.Args) < 2 {
			return nil, false
		}
		return a.resolveProjectedFieldValueThroughIndexOffset(call.Args[1], 0, path)
	case "arena_da_view", "arena_da_view_slice":
		if len(call.Args) < 3 {
			return nil, false
		}
		offset, ok := a.resolveProjectedFieldConstIntExpr(call.Args[1])
		if !ok {
			return nil, false
		}
		return a.resolveProjectedFieldValueThroughIndexOffset(call.Args[0], offset, path)
	case "arena_da_view_prefix":
		if len(call.Args) < 2 {
			return nil, false
		}
		return a.resolveProjectedFieldValueThroughIndexOffset(call.Args[0], 0, path)
	case "arena_da_view_suffix":
		if len(call.Args) < 2 {
			return nil, false
		}
		offset, ok := a.resolveProjectedFieldConstIntExpr(call.Args[1])
		if !ok {
			return nil, false
		}
		return a.resolveProjectedFieldValueThroughIndexOffset(call.Args[0], offset, path)
	case "split_at":
		if len(call.Args) < 2 || len(path) < 2 {
			return nil, false
		}
		fieldStep := path[0]
		if fieldStep.Index != nil || fieldStep.Wildcard || fieldStep.Field == "" {
			return nil, false
		}
		switch fieldStep.Field {
		case "left":
			return a.resolveProjectedFieldValueThroughIndexOffset(call.Args[0], 0, path[1:])
		case "right":
			offset, ok := a.resolveProjectedFieldConstIntExpr(call.Args[1])
			if !ok {
				return nil, false
			}
			return a.resolveProjectedFieldValueThroughIndexOffset(call.Args[0], offset, path[1:])
		default:
			return nil, false
		}
	case "chunks_exact":
		if len(call.Args) < 2 || len(path) < 2 {
			return nil, false
		}
		chunkStep := path[0]
		if chunkStep.Index == nil || chunkStep.Field != "" || chunkStep.Wildcard {
			return nil, false
		}
		chunkSize, ok := a.resolveProjectedFieldConstIntExpr(call.Args[1])
		if !ok || chunkSize < 0 {
			return nil, false
		}
		offset := (*chunkStep.Index) * chunkSize
		return a.resolveProjectedFieldValueThroughIndexOffset(call.Args[0], offset, path[1:])
	default:
		return nil, false
	}
}
func (a *Analyzer) resolveProjectedFieldValueFromCallExpr(call *ast.CallExpr, path []borrowReturnAnnotationStep) (ast.Expr, bool) {
	if call == nil || len(path) == 0 {
		return nil, false
	}
	if expr, ok := a.resolveProjectedFieldValueFromBuiltinViewHelperCall(call, path); ok {
		return expr, true
	}
	decl, ok := a.resolveProjectedFieldExternFuncDecl(call.Func)
	if !ok || decl == nil {
		return nil, false
	}
	for _, annotation := range decl.Annotations {
		if annotation.Name != "borrows_return" {
			continue
		}
		fieldMode, _, fieldArgs := splitBorrowsReturnFlags(annotation.Args)
		if !fieldMode {
			continue
		}
		if len(fieldArgs) == 0 || len(fieldArgs)%2 != 0 {
			continue
		}
		for i := 0; i < len(fieldArgs); i += 2 {
			returnSteps, ok := parseExternReturnTargetPath(fieldArgs[i])
			wildcardCaptures, matched := borrowReturnAnnotationStepsMatchPrefix(path, returnSteps)
			if !ok || len(returnSteps) > len(path) || !matched {
				continue
			}
			if expr, ok := a.resolveProjectedFieldBorrowSourceExprFromCall(call, decl, fieldArgs[i+1], wildcardCaptures); ok {
				return a.resolveProjectedFieldValueExprAtPath(expr, path[len(returnSteps):])
			}
		}
	}
	return nil, false
}
func borrowReturnAnnotationStepsMatchPrefix(path []borrowReturnAnnotationStep, prefix []borrowReturnAnnotationStep) ([]borrowReturnAnnotationStep, bool) {
	if len(prefix) > len(path) {
		return nil, false
	}
	wildcardCaptures := make([]borrowReturnAnnotationStep, 0, len(prefix))
	for i := range prefix {
		capture, captured, ok := borrowReturnAnnotationStepMatches(path[i], prefix[i])
		if !ok {
			return nil, false
		}
		if captured {
			wildcardCaptures = append(wildcardCaptures, capture)
		}
	}
	return wildcardCaptures, true
}
func borrowReturnAnnotationStepsEqual(left, right borrowReturnAnnotationStep) bool {
	switch {
	case left.Field != "" || right.Field != "":
		return left.Field != "" && right.Field != "" && left.Field == right.Field
	case left.Wildcard || right.Wildcard:
		return left.Wildcard && right.Wildcard
	case left.Index != nil || right.Index != nil:
		return left.Index != nil && right.Index != nil && *left.Index == *right.Index
	default:
		return false
	}
}
func borrowReturnAnnotationStepMatches(actual, pattern borrowReturnAnnotationStep) (borrowReturnAnnotationStep, bool, bool) {
	switch {
	case pattern.Wildcard:
		if actual.Wildcard {
			return cloneBorrowReturnAnnotationStep(actual), true, true
		}
		if actual.Index != nil {
			return cloneBorrowReturnAnnotationStep(actual), true, true
		}
		return borrowReturnAnnotationStep{}, false, false
	default:
		return borrowReturnAnnotationStep{}, false, borrowReturnAnnotationStepsEqual(actual, pattern)
	}
}
func cloneBorrowReturnAnnotationStep(step borrowReturnAnnotationStep) borrowReturnAnnotationStep {
	clone := step
	if step.Index != nil {
		index := *step.Index
		clone.Index = &index
	}
	return clone
}
func substituteBorrowReturnWildcardSteps(steps []borrowReturnAnnotationStep, wildcardCaptures []borrowReturnAnnotationStep) ([]borrowReturnAnnotationStep, bool) {
	if len(steps) == 0 {
		return nil, true
	}
	out := make([]borrowReturnAnnotationStep, 0, len(steps))
	captureIndex := 0
	for _, step := range steps {
		if !step.Wildcard {
			out = append(out, cloneBorrowReturnAnnotationStep(step))
			continue
		}
		if captureIndex >= len(wildcardCaptures) {
			return nil, false
		}
		out = append(out, cloneBorrowReturnAnnotationStep(wildcardCaptures[captureIndex]))
		captureIndex++
	}
	return out, true
}
func (a *Analyzer) resolveProjectedFieldExternFuncDecl(fnExpr ast.Expr) (*ast.ExternFuncDecl, bool) {
	if fnExpr == nil {
		return nil, false
	}
	switch n := fnExpr.(type) {
	case *ast.ParenExpr:
		return a.resolveProjectedFieldExternFuncDecl(n.Inner)
	case *ast.SpecializeExpr:
		return a.resolveProjectedFieldExternFuncDecl(n.Operand)
	case *ast.Ident:
		if a.globalScope == nil {
			return nil, false
		}
		sym, _, ok := a.lookupVisibleGlobal(n.Name)
		if !ok {
			return nil, false
		}
		decl, ok := sym.Node.(*ast.ExternFuncDecl)
		return decl, ok
	default:
		return nil, false
	}
}
func (a *Analyzer) resolveProjectedFieldBorrowSourceExprFromCall(call *ast.CallExpr, decl *ast.ExternFuncDecl, pathText string, wildcardCaptures []borrowReturnAnnotationStep) (ast.Expr, bool) {
	if call == nil || decl == nil {
		return nil, false
	}
	paramName, steps, ok := parseBorrowReturnAnnotationPath(pathText)
	if !ok || paramName == "" {
		return nil, false
	}
	steps, ok = substituteBorrowReturnWildcardSteps(steps, wildcardCaptures)
	if !ok {
		return nil, false
	}
	current, ok := resolveProjectedFieldCallArgByParamName(call, decl, paramName)
	if !ok || current == nil {
		return nil, false
	}
	for _, step := range steps {
		switch {
		case step.Field != "":
			current = &ast.FieldExpr{Position: call.Position, Object: current, Field: step.Field}
		case step.Index != nil:
			current = &ast.IndexExpr{Position: call.Position, Object: current, Index: &ast.IntLit{Position: call.Position, Value: strconv.FormatInt(*step.Index, 10)}}
		default:
			return nil, false
		}
	}
	if normalized, ok := a.normalizeProjectedBorrowSourceExpr(current); ok {
		return normalized, true
	}
	return current, true
}
func (a *Analyzer) normalizeProjectedBorrowSourceExpr(expr ast.Expr) (ast.Expr, bool) {
	if expr == nil {
		return nil, false
	}
	root, path, ok := a.extractProjectedBorrowSourcePath(expr)
	if !ok || len(path) == 0 {
		return expr, true
	}
	resolved, ok := a.resolveProjectedFieldValueExprAtPath(root, path)
	if !ok || resolved == nil {
		return expr, true
	}
	return resolved, true
}
func (a *Analyzer) extractProjectedBorrowSourcePath(expr ast.Expr) (ast.Expr, []borrowReturnAnnotationStep, bool) {
	if expr == nil {
		return nil, nil, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.extractProjectedBorrowSourcePath(n.Inner)
	case *ast.CastExpr:
		return a.extractProjectedBorrowSourcePath(n.Operand)
	case *ast.MoveExpr:
		return a.extractProjectedBorrowSourcePath(n.Operand)
	case *ast.CanExpr:
		return a.extractProjectedBorrowSourcePath(n.Expr)
	case *ast.AllocExpr:
		return a.extractProjectedBorrowSourcePath(n.Value)
	case *ast.FieldExpr:
		root, path, ok := a.extractProjectedBorrowSourcePath(n.Object)
		if !ok {
			return nil, nil, false
		}
		path = append(path, borrowReturnAnnotationStep{Field: n.Field})
		return root, path, true
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return nil, nil, false
		}
		step, ok := a.resolveProjectedFieldIndexStep(n.Index)
		if !ok {
			return nil, nil, false
		}
		root, path, ok := a.extractProjectedBorrowSourcePath(n.Object)
		if !ok {
			return nil, nil, false
		}
		path = append(path, step)
		return root, path, true
	default:
		return expr, nil, true
	}
}

// resolveProjectedFieldValueThroughSymbol follows sym's bound value expression, refusing to
// re-enter a symbol already being followed. `h = h.r` binds h to an expression that projects
// out of h itself; without the guard every lap prepended another `.r` step to the path and the
// analysis never terminated (the path slice grew until memory ran out).
func (a *Analyzer) resolveProjectedFieldValueThroughSymbol(sym *Symbol, valueExpr ast.Expr, path []borrowReturnAnnotationStep) (ast.Expr, bool) {
	if a.projectedFieldResolveInProgress == nil {
		a.projectedFieldResolveInProgress = map[*Symbol]bool{}
	}
	if a.projectedFieldResolveInProgress[sym] {
		return nil, false
	}
	a.projectedFieldResolveInProgress[sym] = true
	defer delete(a.projectedFieldResolveInProgress, sym)
	return a.resolveProjectedFieldValueExprAtPath(valueExpr, path)
}
