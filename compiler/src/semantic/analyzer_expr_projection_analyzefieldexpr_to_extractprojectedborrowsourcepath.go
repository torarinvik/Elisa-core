package semantic

import (
	"elisacore/src/ast"
	"strconv"
)

func (a *Analyzer) analyzeFieldExpr(expr *ast.FieldExpr) Type {
	if expr != nil && expr.Safe {
		return a.analyzeSafeFieldExpr(expr)
	}
	objType := a.analyzeExpr(expr.Object)
	if constType, ok := objType.(*ConstValueType); ok && constType != nil {
		if expr.Field == "count" && (constType.Value.Kind == ConstList || constType.Value.Kind == ConstTuple) {
			result := a.namedTypes["usize"]
			a.exprTypes[expr] = result
			return result
		}
		if expr.Field == "has_field" && constType.Value.Kind == ConstRecord {
			result := &FuncType{Name: "has_field", Params: []Type{&RefType{Elem: a.namedTypes["u8"], State: RefStateNonNull, Storage: RefStorageStatic, ExplicitStorage: true}}, Return: a.namedTypes["bool"]}
			a.exprTypes[expr] = result
			return result
		}
		if value, ok := ConstReflectionRecordField(constType.Value, expr.Field); ok {
			result := ConstValueStaticType(a.namedTypes, value)
			a.exprTypes[expr] = result
			return result
		}
	}
	if viewType, ok := a.lookupRefinedPackedVariantView(expr.Object); ok {
		if field, ok := viewType.Field(expr.Field); ok {
			field.Type = a.specializeProjectedFunctionFieldType(expr, field.Type)
			a.reportInvalidRegionUse(expr, field.Type)
			if state, ok := a.lookupAffineValueState(expr); ok && a.containsAffineHandleValues(field.Type, map[string]bool{}) {
				a.errorf(expr.Pos(), consumedFactUseMessage(affineHandleKind(field.Type), affineValueDisplayName(expr), state.ConsumedBy))
			}
			a.reportBorrowedOwnerRefUseAfterConsume(expr, field.Type)
			return field.Type
		}
	}
	if field, ok := cstrSyntheticField(objType, expr.Field); ok {
		field.Type = a.specializeProjectedFunctionFieldType(expr, field.Type)
		a.reportInvalidRegionUse(expr, field.Type)
		if state, ok := a.lookupAffineValueState(expr); ok && a.containsAffineHandleValues(field.Type, map[string]bool{}) {
			a.errorf(expr.Pos(), consumedFactUseMessage(affineHandleKind(field.Type), affineValueDisplayName(expr), state.ConsumedBy))
		}
		a.reportBorrowedOwnerRefUseAfterConsume(expr, field.Type)
		return field.Type
	}
	if attr, ok := a.lookupTreeAttribute(objType, expr.Field); ok {
		a.attributeFieldRefs[expr] = &AttributeFieldRef{Attribute: attr}
		a.recordImplicitTreeStoreUseForTreeAttribute(attr)
		attrType := a.specializeProjectedFunctionFieldType(expr, attr.ReturnType)
		a.reportInvalidRegionUse(expr, attrType)
		if state, ok := a.lookupAffineValueState(expr); ok && a.containsAffineHandleValues(attrType, map[string]bool{}) {
			a.errorf(expr.Pos(), consumedFactUseMessage(affineHandleKind(attrType), affineValueDisplayName(expr), state.ConsumedBy))
		}
		a.reportBorrowedOwnerRefUseAfterConsume(expr, attrType)
		return attrType
	}
	field, ok := a.lookupFieldWithDiagnostics(objType, expr.Field, expr.Pos(), false)
	if ok {
		field.Type = a.specializeProjectedFunctionFieldType(expr, field.Type)
		a.reportInvalidRegionUse(expr, field.Type)
		if state, ok := a.lookupAffineValueState(expr); ok && a.containsAffineHandleValues(field.Type, map[string]bool{}) {
			a.errorf(expr.Pos(), consumedFactUseMessage(affineHandleKind(field.Type), affineValueDisplayName(expr), state.ConsumedBy))
		}
		a.reportBorrowedOwnerRefUseAfterConsume(expr, field.Type)
		return field.Type
	}
	if projectedType, attr, ok := a.lookupProjectedTreeAttributeSequence(objType, expr.Field); ok {
		a.attributeFieldRefs[expr] = &AttributeFieldRef{Attribute: attr}
		a.recordImplicitTreeStoreUseForTreeAttribute(attr)
		a.reportInvalidRegionUse(expr, projectedType)
		a.reportBorrowedOwnerRefUseAfterConsume(expr, projectedType)
		return projectedType
	}
	a.lookupFieldWithDiagnostics(objType, expr.Field, expr.Pos(), true)
	return invalidType
}
func (a *Analyzer) resolveProjectedFieldValueExpr(objectExpr ast.Expr, field string) (ast.Expr, bool) {
	return a.resolveProjectedFieldValueExprAtPath(objectExpr, []borrowReturnAnnotationStep{{Field: field}})
}
func (a *Analyzer) resolveIndexedValueExpr(objectExpr ast.Expr, indexExpr ast.Expr) (ast.Expr, bool) {
	step, ok := a.resolveProjectedFieldIndexStep(indexExpr)
	if !ok {
		return nil, false
	}
	return a.resolveProjectedFieldValueExprAtPath(objectExpr, []borrowReturnAnnotationStep{step})
}
func (a *Analyzer) resolveProjectedFieldValueExprAtPath(objectExpr ast.Expr, path []borrowReturnAnnotationStep) (ast.Expr, bool) {
	if objectExpr == nil {
		return nil, false
	}
	if len(path) == 0 {
		return objectExpr, true
	}
	switch n := objectExpr.(type) {
	case *ast.ParenExpr:
		return a.resolveProjectedFieldValueExprAtPath(n.Inner, path)
	case *ast.CastExpr:
		return a.resolveProjectedFieldValueExprAtPath(n.Operand, path)
	case *ast.MoveExpr:
		return a.resolveProjectedFieldValueExprAtPath(n.Operand, path)
	case *ast.AllocExpr:
		return a.resolveProjectedFieldValueExprAtPath(n.Value, path)
	case *ast.CanExpr:
		return a.resolveProjectedFieldValueExprAtPath(n.Expr, path)
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return nil, false
		}
		step, ok := a.resolveProjectedFieldIndexStep(n.Index)
		if !ok {
			return nil, false
		}
		combinedPath := make([]borrowReturnAnnotationStep, 0, len(path)+1)
		combinedPath = append(combinedPath, step)
		combinedPath = append(combinedPath, path...)
		return a.resolveProjectedFieldValueExprAtPath(n.Object, combinedPath)
	case *ast.SliceExpr:
		step := path[0]
		if step.Index == nil || step.Field != "" || step.Wildcard {
			return nil, false
		}
		start, ok := a.resolveProjectedFieldConstIntExpr(n.Start)
		if !ok || start < 0 {
			return nil, false
		}
		index := start + *step.Index
		if index < 0 {
			return nil, false
		}
		if end, ok := a.resolveProjectedFieldConstIntExpr(n.End); ok && index >= end {
			return nil, false
		}
		indexCopy := index
		combinedPath := make([]borrowReturnAnnotationStep, 0, len(path))
		combinedPath = append(combinedPath, borrowReturnAnnotationStep{Index: &indexCopy})
		combinedPath = append(combinedPath, path[1:]...)
		return a.resolveProjectedFieldValueExprAtPath(n.Object, combinedPath)
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok {
				if sym.Mutable {
					return nil, false
				}
				if a.currentValueBindings != nil {
					if valueExpr, ok := a.currentValueBindings[sym]; ok && valueExpr != nil {
						return a.resolveProjectedFieldValueExprAtPath(valueExpr, path)
					}
					if root := symbolAliasRoot(sym); root != nil && root != sym {
						if valueExpr, ok := a.currentValueBindings[root]; ok && valueExpr != nil {
							return a.resolveProjectedFieldValueExprAtPath(valueExpr, path)
						}
					}
				}
				if valueExpr, ok := a.immutableValueExprForSymbol(sym); ok {
					return a.resolveProjectedFieldValueExprAtPath(valueExpr, path)
				}
				return nil, false
			}
		}
		sym, _, ok := a.lookupVisibleGlobal(n.Name)
		if !ok || sym.Mutable {
			return nil, false
		}
		valueExpr, ok := a.immutableValueExprForSymbol(sym)
		if !ok {
			return nil, false
		}
		return a.resolveProjectedFieldValueExprAtPath(valueExpr, path)
	case *ast.ListLitExpr:
		step := path[0]
		if step.Index == nil || step.Field != "" || step.Wildcard {
			return nil, false
		}
		index := int(*step.Index)
		if index < 0 || index >= len(n.Elems) {
			return nil, false
		}
		return a.resolveProjectedFieldValueExprAtPath(n.Elems[index], path[1:])
	case *ast.StructLitExpr:
		actual := a.exprTypes[n]
		if actual == nil {
			actual = a.analyzeExpr(n)
		}
		fields, ok := a.resolvedStructFields(actual)
		if !ok {
			return nil, false
		}
		args := n.LoweredArgs()
		step := path[0]
		if step.Field == "" {
			return nil, false
		}
		for i, resolved := range fields {
			if resolved.Name != step.Field {
				continue
			}
			if i >= len(args) || args[i] == nil {
				return nil, false
			}
			return a.resolveProjectedFieldValueExprAtPath(args[i], path[1:])
		}
		return nil, false
	case *ast.RecordUpdateExpr:
		actual := a.exprTypes[n]
		if actual == nil {
			actual = a.analyzeExpr(n)
		}
		fields, ok := a.resolvedStructFields(actual)
		if !ok {
			return nil, false
		}
		step := path[0]
		if step.Field == "" {
			return nil, false
		}
		args := n.LoweredArgs()
		for i, resolved := range fields {
			if resolved.Name != step.Field {
				continue
			}
			if i < len(args) && args[i] != nil {
				return a.resolveProjectedFieldValueExprAtPath(args[i], path[1:])
			}
			return a.resolveProjectedFieldValueExprAtPath(&ast.FieldExpr{Position: n.Position, Object: n.Base, Field: resolved.Name}, path[1:])
		}
		return nil, false
	case *ast.TupleExpr:
		actual := a.exprTypes[n]
		if actual == nil {
			actual = a.analyzeExpr(n)
		}
		fields, ok := a.resolvedStructFields(actual)
		if !ok {
			return nil, false
		}
		step := path[0]
		if step.Field == "" {
			return nil, false
		}
		for i, resolved := range fields {
			if resolved.Name != step.Field {
				continue
			}
			if i >= len(n.Elems) {
				return nil, false
			}
			return a.resolveProjectedFieldValueExprAtPath(n.Elems[i], path[1:])
		}
		return nil, false
	case *ast.CallExpr:
		return a.resolveProjectedFieldValueFromCallExpr(n, path)
	case *ast.FieldExpr:
		combinedPath := make([]borrowReturnAnnotationStep, 0, len(path)+1)
		combinedPath = append(combinedPath, borrowReturnAnnotationStep{Field: n.Field})
		combinedPath = append(combinedPath, path...)
		return a.resolveProjectedFieldValueExprAtPath(n.Object, combinedPath)
	default:
		return nil, false
	}
}
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
	switch optimizationHelperName(call.Func) {
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
		if annotation.Name != "borrows_return_field" && annotation.Name != "borrows_return_field_rebased" {
			continue
		}
		if len(annotation.Args) == 0 || len(annotation.Args)%2 != 0 {
			continue
		}
		for i := 0; i < len(annotation.Args); i += 2 {
			returnSteps, ok := parseExternReturnTargetPath(annotation.Args[i])
			wildcardCaptures, matched := borrowReturnAnnotationStepsMatchPrefix(path, returnSteps)
			if !ok || len(returnSteps) > len(path) || !matched {
				continue
			}
			if expr, ok := a.resolveProjectedFieldBorrowSourceExprFromCall(call, decl, annotation.Args[i+1], wildcardCaptures); ok {
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
