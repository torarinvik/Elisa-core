package semantic

import (
	"strconv"
	"strings"

	"elisacore/src/ast"
)

type borrowReturnAnnotationStep struct {
	Field    string
	Index    *int64
	Wildcard bool
}

func parseBorrowReturnAnnotationPath(text string) (string, []borrowReturnAnnotationStep, bool) {
	if text == "" {
		return "", nil, false
	}
	rootEnd := strings.IndexAny(text, ".[")
	if rootEnd < 0 {
		return text, nil, true
	}
	root := text[:rootEnd]
	if root == "" {
		return "", nil, false
	}
	rest := text[rootEnd:]
	steps := make([]borrowReturnAnnotationStep, 0, 2)
	for len(rest) != 0 {
		switch rest[0] {
		case '.':
			rest = rest[1:]
			next := strings.IndexAny(rest, ".[")
			field := rest
			if next >= 0 {
				field = rest[:next]
				rest = rest[next:]
			} else {
				rest = ""
			}
			if field == "" {
				return "", nil, false
			}
			steps = append(steps, borrowReturnAnnotationStep{Field: field})
		case '[':
			end := strings.IndexByte(rest, ']')
			if end <= 1 {
				return "", nil, false
			}
			token := rest[1:end]
			rest = rest[end+1:]
			if token == "*" {
				steps = append(steps, borrowReturnAnnotationStep{Wildcard: true})
				continue
			}
			value, err := strconv.ParseInt(token, 10, 64)
			if err != nil {
				return "", nil, false
			}
			valueCopy := value
			steps = append(steps, borrowReturnAnnotationStep{Index: &valueCopy})
		default:
			return "", nil, false
		}
	}
	return root, steps, true
}

func projectBorrowReturnAnnotationState(state regionRefState, steps []borrowReturnAnnotationStep) (regionRefState, bool) {
	current := state
	ok := true
	for _, step := range steps {
		switch {
		case step.Field != "":
			current, ok = projectRegionFieldState(current, step.Field)
		case step.Wildcard:
			current, ok = projectRegionIndexKeyState(current, regionAnyIndexFieldKey())
		case step.Index != nil:
			current, ok = projectRegionIndexKeyState(current, regionIndexFieldKey(*step.Index))
		default:
			return regionRefState{}, false
		}
		if !ok {
			return regionRefState{}, false
		}
	}
	return current, true
}

func parseExternReturnTargetPath(text string) ([]borrowReturnAnnotationStep, bool) {
	if text == "" {
		return nil, false
	}
	_, steps, ok := parseBorrowReturnAnnotationPath("ret." + text)
	if !ok || len(steps) == 0 {
		return nil, false
	}
	return steps, true
}

func assignRegionRefStateAtPath(dst regionRefState, steps []borrowReturnAnnotationStep, value regionRefState) regionRefState {
	if len(steps) == 0 {
		if merged, ok := mergeRegionRefStates(dst, value); ok {
			return merged
		}
		return value
	}
	key := regionFieldKeyForBorrowStep(steps[0])
	nextFields := cloneRegionRefFields(dst.Fields)
	if nextFields == nil {
		nextFields = map[string]regionRefState{}
	}
	child := nextFields[key]
	nextFields[key] = assignRegionRefStateAtPath(child, steps[1:], value)
	dst.Fields = nextFields
	dst.PackedStoreSummaryKnown = false
	return withPackedStoreProvenanceSummary(dst)
}

func regionFieldKeyForBorrowStep(step borrowReturnAnnotationStep) string {
	switch {
	case step.Field != "":
		return step.Field
	case step.Wildcard:
		return regionAnyIndexFieldKey()
	case step.Index != nil:
		return regionIndexFieldKey(*step.Index)
	default:
		return ""
	}
}

func (a *Analyzer) resolveExternReturnTargetPath(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation, pathText string) ([]borrowReturnAnnotationStep, bool) {
	steps, ok := parseExternReturnTargetPath(pathText)
	if !ok {
		a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, pathText, "has invalid return field path %q", pathText)
		return nil, false
	}
	current := fnType.Return
	for _, step := range steps {
		next, ok := a.projectExternReturnTargetType(current, step)
		if !ok {
			a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, pathText, "references unknown return field path %q in %s", pathText, fnType.Return.String())
			return nil, false
		}
		current = next
	}
	return steps, true
}

func (a *Analyzer) projectExternReturnTargetType(current Type, step borrowReturnAnnotationStep) (Type, bool) {
	switch {
	case step.Field != "":
		fields, ok := a.resolvedStructFields(current)
		if !ok {
			return nil, false
		}
		for _, field := range fields {
			if field.Name == step.Field {
				return field.Type, true
			}
		}
		return nil, false
	case step.Wildcard || step.Index != nil:
		switch tt := current.(type) {
		case *ArrayType:
			return tt.Elem, true
		case *DArrayType:
			return tt.Elem, true
		case *ViewType:
			return tt.Elem, true
		case *DArrayViewType:
			return tt.Elem, true
		default:
			return nil, false
		}
	default:
		return nil, false
	}
}

func (a *Analyzer) resolveExternBorrowAnnotationPath(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation, pathText string, rebased bool, returnField string) (regionRefState, borrowedOwnerRefSummary, bool) {
	name, steps, ok := parseBorrowReturnAnnotationPath(pathText)
	if !ok {
		a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, returnField, "has invalid path %q", pathText)
		return regionRefState{}, borrowedOwnerRefSummary{}, false
	}
	index := -1
	for i, param := range a.expandedExternFuncDeclParams(fn) {
		if param.Name == name {
			index = i
			break
		}
	}
	if index < 0 {
		a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, returnField, "references unknown parameter %q", name)
		return regionRefState{}, borrowedOwnerRefSummary{}, false
	}
	if index >= len(fnType.Params) {
		return regionRefState{}, borrowedOwnerRefSummary{}, false
	}
	paramType := fnType.Params[index]
	regionState, regionOK := a.abstractParamRegionRefState(paramType, index, map[string]bool{})
	ownerParam := &Symbol{Name: name, Kind: SymbolParam, Type: paramType, ParamIndex: index, Mutable: false}
	ownerState, ownerStateOK := a.abstractParamBorrowedOwnerRefState(paramType, affineValueKey{Root: ownerParam}, map[string]bool{})
	if !regionOK && !ownerStateOK {
		if returnField == "" {
			a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, returnField, "cannot borrow from parameter %q of type %s", name, paramType.String())
		} else {
			a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, returnField, "cannot borrow field %q from parameter %q of type %s", returnField, name, paramType.String())
		}
		return regionRefState{}, borrowedOwnerRefSummary{}, false
	}
	if len(steps) != 0 {
		if regionOK {
			regionState, regionOK = projectBorrowReturnAnnotationState(regionState, steps)
		}
		if ownerStateOK {
			ownerState, ownerStateOK = projectBorrowedOwnerRefStateAtSteps(ownerState, steps)
		}
		if !regionOK && !ownerStateOK {
			a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, returnField, "cannot project path %q from parameter %q of type %s", pathText, name, fnType.Params[index].String())
			return regionRefState{}, borrowedOwnerRefSummary{}, false
		}
	}
	if rebased {
		if regionOK {
			regionState, regionOK = summarizeRegionIndexStates(regionState)
		}
		if ownerStateOK {
			ownerState, ownerStateOK = summarizeBorrowedOwnerRefIndexStates(ownerState)
		}
	}
	ownerSummary := borrowedOwnerRefSummary{}
	ownerOK := false
	if ownerStateOK {
		ownerSummary, ownerOK = abstractParamOnlyBorrowedOwnerRefSummary(ownerState)
	}
	if !regionOK && !ownerOK {
		return regionRefState{}, borrowedOwnerRefSummary{}, false
	}
	if !regionOK {
		regionState = regionRefState{}
	}
	if !ownerOK {
		ownerSummary = borrowedOwnerRefSummary{}
	}
	return regionState, ownerSummary, true
}

func (a *Analyzer) errorExternBorrowAnnotationPathError(fn *ast.ExternFuncDecl, annotation ast.Annotation, pathText string, returnField string, format string, args ...interface{}) {
	switch annotation.Name {
	case "borrows_return":
		a.errorf(annotation.Position, "@borrows_return on extern function %q "+format, append([]interface{}{fn.Name}, args...)...)
	case "borrows_return_field":
		a.errorf(annotation.Position, "@borrows_return_field on extern function %q "+format, append([]interface{}{fn.Name}, args...)...)
	case "borrows_return_rebased":
		a.errorf(annotation.Position, "@borrows_return_rebased on extern function %q "+format, append([]interface{}{fn.Name}, args...)...)
	case "borrows_return_field_rebased":
		a.errorf(annotation.Position, "@borrows_return_field_rebased on extern function %q "+format, append([]interface{}{fn.Name}, args...)...)
	default:
		a.errorf(annotation.Position, "@%s on extern function %q "+format, append([]interface{}{annotation.Name, fn.Name}, args...)...)
	}
}
