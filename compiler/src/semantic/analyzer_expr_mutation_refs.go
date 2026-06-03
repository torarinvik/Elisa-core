package semantic

import (
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) variantConstructorMoveReason(kind string, containerName string, variant *EnumVariant, index int) string {
	if variant == nil {
		return "move into " + kind + " constructor payload"
	}
	if label := variant.PayloadLabel(index); label != "" {
		return "move into " + kind + " payload " + strconv.Quote(containerName+"."+variant.Name+"."+label)
	}
	return "move into " + kind + " payload " + strconv.Quote(containerName+"."+variant.Name) + " argument " + strconv.Itoa(index+1)
}

func (a *Analyzer) enumConstructorMoveReason(enumName string, variant *EnumVariant, index int) string {
	return a.variantConstructorMoveReason("enum", enumName, variant, index)
}

func containsTypeParam(t Type) bool {
	switch n := t.(type) {
	case nil:
		return false
	case *TypeParamType:
		return true
	case *IDType:
		return containsTypeParam(n.Tag) || containsTypeParam(n.Storage)
	case *ErrorUnionType:
		return containsTypeParam(n.Value)
	case *OptionalType:
		return containsTypeParam(n.Value)
	case *RefType:
		return containsTypeParam(n.Elem)
	case *ArrayType:
		return containsTypeParam(n.Elem)
	case *DArrayType:
		return containsTypeParam(n.Elem)
	case *ViewType:
		return containsTypeParam(n.Elem)
	case *DArrayViewType:
		return containsTypeParam(n.Elem)
	case *TupleType:
		for _, field := range n.Fields {
			if containsTypeParam(field.Type) {
				return true
			}
		}
		return false
	case *GenericInstanceType:
		for _, arg := range n.Args {
			if containsTypeParam(arg) {
				return true
			}
		}
		return containsTypeParam(n.Base)
	case *AggregateStateType:
		return containsTypeParam(n.Base)
	case *FuncType:
		if len(n.GenericParams) != 0 {
			return true
		}
		for _, param := range n.Params {
			if containsTypeParam(param) {
				return true
			}
		}
		return containsTypeParam(n.Return)
	case *EnumType:
		for _, variant := range n.Variants {
			for _, payload := range variant.Payload {
				if containsTypeParam(payload) {
					return true
				}
			}
		}
		return false
	default:
		return false
	}
}

func (a *Analyzer) assignmentTargetType(expr ast.Expr) Type {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.assignmentTargetType(n.Inner)
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			if sym, _, ok = a.lookupVisibleGlobal(n.Name); !ok {
				a.errorf(n.Pos(), "undefined assignment target %q (use = to introduce a new local; <- requires an existing mutable target)", n.Name)
				return invalidType
			}
		}
		if !sym.Mutable {
			if ref, ok := sym.Type.(*RefType); ok {
				if !ref.Mutable {
					a.errorf(n.Pos(), "cannot assign through readonly ref %q", sym.Name)
					a.reportReadonlyRefMutationNote(n.Pos(), n, sym.Type)
					return invalidType
				}
				return ref.Elem
			}
			a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
			return sym.Type
		}
		if a.currentScope != nil {
			if current, exists := a.currentScope.Symbols[n.Name]; exists && current == sym && a.currentScope.Parent != nil {
				if parent, ok := a.currentScope.Parent.Lookup(n.Name); ok && parent.Node == sym.Node && parent.Kind == sym.Kind && parent.Mutable {
					return parent.Type
				}
			}
		}
		return sym.Type
	case *ast.FieldExpr:
		if n.Safe {
			a.errorf(n.Pos(), "optional chaining cannot be used as an assignment target")
			return invalidType
		}
		field, ok := a.lookupField(a.analyzeExpr(n.Object), n.Field, n.Pos())
		if !ok {
			return invalidType
		}
		if _, ok := a.treeSurfaceFieldExprInfo(n); ok {
			a.errorf(n.Pos(), "cannot assign directly to tree field %q; tree values are handle-lowered and must be changed with tree update syntax", n.Field)
			return field.Type
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q is immutable", n.Field)
		}
		a.requireWritableMutationPath(n.Object)
		return field.Type
	case *ast.IndexExpr:
		if n.Fallback != nil {
			a.errorf(n.Pos(), "safe index fallback cannot be used as an assignment target")
			return invalidType
		}
		targetType := a.analyzeIndexExpr(n)
		if kind, ok := valueOnlyIndexKind(a.exprTypes[n.Object]); ok {
			a.errorf(n.Pos(), "cannot assign to %s", kind)
			return invalidType
		}
		if facts, ok := a.exprFacts[n.Object]; ok && facts.ReadOnly {
			a.errorf(n.Pos(), "cannot assign to readonly view index result")
			return invalidType
		}
		a.requireWritableMutationPath(n.Object)
		return targetType
	default:
		a.errorf(expr.Pos(), "invalid assignment target")
		return invalidType
	}
}

func (a *Analyzer) optionalAssignmentTargetType(expr ast.Expr) Type {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.optionalAssignmentTargetType(n.Inner)
	case *ast.Ident:
		valueType := a.analyzeExpr(n)
		boundType, ok := conditionOptionalBindType(valueType)
		if !ok {
			a.errorf(n.Pos(), "?= requires a nullable reference target, got %s", valueType)
			return invalidType
		}
		refType, ok := boundType.(*RefType)
		if !ok || refType == nil {
			a.errorf(n.Pos(), "?= requires a nullable reference target, got %s", valueType)
			return invalidType
		}
		if !a.requireWritableMutationPath(n) {
			return invalidType
		}
		return refType.Elem
	case *ast.FieldExpr:
		if !n.Safe {
			a.errorf(n.Pos(), "?= requires a nullable reference target; use <- for ordinary assignment")
			return invalidType
		}
		receiverType := a.analyzeExpr(n.Object)
		boundType, ok := conditionOptionalBindType(receiverType)
		if !ok {
			a.errorf(n.Pos(), "?= requires a nullable reference receiver, got %s", receiverType)
			return invalidType
		}
		refType, ok := boundType.(*RefType)
		if !ok || refType == nil {
			a.errorf(n.Pos(), "?= requires a nullable reference receiver, got %s", receiverType)
			return invalidType
		}
		field, ok := a.lookupField(refType, n.Field, n.Pos())
		if !ok {
			return invalidType
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q is immutable", n.Field)
		}
		a.requireWritableMutationPath(n.Object)
		return field.Type
	default:
		a.errorf(expr.Pos(), "invalid ?= target")
		return invalidType
	}
}

func stripMutationTargetExpr(expr ast.Expr) ast.Expr {
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.MoveExpr:
			expr = n.Operand
		case *ast.CanExpr:
			expr = n.Expr
		default:
			return expr
		}
	}
}

func (a *Analyzer) mutationPathWritable(expr ast.Expr) bool {
	stripped := stripMutationTargetExpr(expr)
	if stripped == nil {
		return true
	}
	objType, ok := a.exprTypes[stripped]
	if !ok || objType == nil {
		objType = a.analyzeExpr(stripped)
	}
	if ref, ok := objType.(*RefType); ok {
		return a.refExprAllowsMutation(stripped, ref)
	}
	// A slice-derived bounded view is writable only when its source was writable
	// (recorded at bind time). Writing through a view of an immutable source is
	// rejected; untracked views keep their existing behavior.
	if ident, ok := stripped.(*ast.Ident); ok && ident != nil {
		if mut, tracked := a.currentViewMutable[ident.Name]; tracked {
			return mut
		}
	}
	switch n := stripped.(type) {
	case *ast.FieldExpr:
		return a.mutationPathWritable(n.Object)
	case *ast.IndexExpr:
		return a.mutationPathWritable(n.Object)
	case *ast.SliceExpr:
		return a.mutationPathWritable(n.Object)
	default:
		return true
	}
}

func (a *Analyzer) requireWritableMutationPath(expr ast.Expr) bool {
	if a.mutationPathWritable(expr) {
		return true
	}
	stripped := stripMutationTargetExpr(expr)
	if stripped == nil {
		return false
	}
	a.errorf(stripped.Pos(), "cannot mutate through readonly ref")
	a.reportReadonlyRefMutationNote(stripped.Pos(), stripped, nil)
	return false
}

func mutableRefSuggestionString(t Type) (string, bool) {
	ref, ok := t.(*RefType)
	if !ok || ref == nil {
		return "", false
	}
	cloned := cloneRefType(ref)
	cloned.Mutable = true
	return cloned.String(), true
}

func writableRefAssignableIgnoringMutability(expected Type, actual Type) bool {
	expectedRef, ok := expected.(*RefType)
	if !ok || expectedRef == nil || !expectedRef.Mutable {
		return false
	}
	actualRef, ok := actual.(*RefType)
	if !ok || actualRef == nil || actualRef.Mutable {
		return false
	}
	expectedReadonly := cloneRefType(expectedRef)
	expectedReadonly.Mutable = false
	actualReadonly := cloneRefType(actualRef)
	actualReadonly.Mutable = false
	return AssignableTo(expectedReadonly, actualReadonly)
}

func (a *Analyzer) writableRefSuggestionForExpr(expr ast.Expr) (string, bool) {
	stripped := stripMutationTargetExpr(expr)
	if stripped == nil {
		return "", false
	}
	t := a.exprTypes[stripped]
	if t == nil {
		t = a.analyzeExpr(stripped)
	}
	if suggestion, ok := mutableRefSuggestionString(t); ok {
		return suggestion, true
	}
	switch n := stripped.(type) {
	case *ast.FieldExpr:
		return a.writableRefSuggestionForExpr(n.Object)
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return "", false
		}
		return a.writableRefSuggestionForExpr(n.Object)
	case *ast.SliceExpr:
		return a.writableRefSuggestionForExpr(n.Object)
	default:
		return "", false
	}
}

func (a *Analyzer) reportReadonlyRefMutationNote(pos lexer.Pos, expr ast.Expr, t Type) {
	if a == nil {
		return
	}
	if suggestion, ok := mutableRefSuggestionString(t); ok {
		a.errorf(pos, "note: plain refs T& are readonly; use %s if this reference should allow writes", suggestion)
		return
	}
	if suggestion, ok := a.writableRefSuggestionForExpr(expr); ok {
		a.errorf(pos, "note: plain refs T& are readonly; use %s if this reference should allow writes", suggestion)
		return
	}
	a.errorf(pos, "note: plain refs T& are readonly; use mutable T& if this reference should allow writes")
}

func (a *Analyzer) reportMutableRefArgumentNote(pos lexer.Pos, expected Type, actual Type) {
	if a == nil || !writableRefAssignableIgnoringMutability(expected, actual) {
		return
	}
	if suggestion, ok := mutableRefSuggestionString(expected); ok {
		a.errorf(pos, "note: use %s here if the callee should be allowed to write through it", suggestion)
	}
}

func (a *Analyzer) refExprAllowsMutation(expr ast.Expr, ref *RefType) bool {
	if ref == nil {
		return false
	}
	if ref.Mutable {
		return true
	}
	stripped := stripMutationTargetExpr(expr)
	switch n := stripped.(type) {
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			if sym, _, ok = a.lookupVisibleGlobal(n.Name); !ok {
				return false
			}
		}
		return sym.Mutable
	case *ast.FieldExpr:
		if n.Safe {
			return false
		}
		field, ok := a.lookupFieldNoError(a.analyzeExpr(n.Object), n.Field)
		if !ok {
			return false
		}
		return field.Mutable
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return false
		}
		return a.mutationPathWritable(n.Object)
	default:
		return false
	}
}

func (a *Analyzer) asRefTargetType(expr ast.Expr, asKind string) Type {
	switch n := expr.(type) {
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			if sym, _, ok = a.lookupVisibleGlobal(n.Name); !ok {
				a.errorf(n.Pos(), "undefined assignment target %q (use = to introduce a new local; <- requires an existing mutable target)", n.Name)
				return invalidType
			}
		}
		if !sym.Mutable {
			if ref, ok := sym.Type.(*RefType); ok {
				if !ref.Mutable {
					a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
					return a.refTypeWithAsKind(sym.Type, asKind)
				}
				return a.refTypeWithAsKind(sym.Type, asKind)
			}
			a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
		}
		return a.refTypeWithAsKind(sym.Type, asKind)
	case *ast.FieldExpr:
		if n.Safe {
			a.errorf(n.Pos(), "optional chaining cannot be used as a reference target")
			return invalidType
		}
		field, ok := a.lookupField(a.analyzeExpr(n.Object), n.Field, n.Pos())
		if !ok {
			return invalidType
		}
		if _, ok := a.treeSurfaceFieldExprInfo(n); ok {
			a.errorf(n.Pos(), "cannot bind tree field %q as a reference; tree values are handle-lowered and fields are value-only", n.Field)
			return a.refTypeWithAsKind(field.Type, asKind)
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q is immutable", n.Field)
		}
		a.requireWritableMutationPath(n.Object)
		return a.refTypeWithAsKind(field.Type, asKind)
	case *ast.IndexExpr:
		if n.Fallback != nil {
			a.errorf(n.Pos(), "cannot take a reference to a safe index fallback expression")
			return invalidType
		}
		targetType := a.analyzeIndexExpr(n)
		if kind, ok := valueOnlyIndexKind(a.exprTypes[n.Object]); ok {
			a.errorf(n.Pos(), "cannot take a reference to %s", kind)
			return invalidType
		}
		if facts, ok := a.exprFacts[n.Object]; ok && facts.ReadOnly {
			a.errorf(n.Pos(), "cannot take a reference to readonly view index result")
			return invalidType
		}
		a.requireWritableMutationPath(n.Object)
		return a.refTypeWithAsKind(targetType, asKind)
	default:
		a.errorf(expr.Pos(), "invalid assignment target")
		return invalidType
	}
}

func (a *Analyzer) refTypeWithAsKind(t Type, asKind string) Type {
	ref, ok := t.(*RefType)
	if !ok {
		return t
	}
	switch asKind {
	case "&":
		return cloneRefTypeWithState(ref, RefStateNonNull)
	case "!":
		return cloneRefTypeWithState(ref, RefStateNull)
	default:
		return t
	}
}

func (a *Analyzer) inferAddrOfStorage(expr ast.Expr) RefStorage {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.inferAddrOfStorage(n.Inner)
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok {
				switch sym.Kind {
				case SymbolLocal, SymbolParam, SymbolRegion:
					return RefStorageStack
				case SymbolGlobal:
					return RefStorageStatic
				}
			}
		}
		if sym, _, ok := a.lookupVisibleGlobal(n.Name); ok && sym.Kind == SymbolGlobal {
			return RefStorageStatic
		}
	case *ast.FieldExpr:
		if objType, ok := a.exprTypes[n.Object].(*RefType); ok {
			return objType.Storage
		}
		return a.inferAddrOfStorage(n.Object)
	case *ast.IndexExpr:
		switch objType := a.exprTypes[n.Object].(type) {
		case *RefType:
			if _, ok := objType.Elem.(*ArrayType); ok {
				return objType.Storage
			}
			return RefStorageAny
		case *ArrayType:
			return a.inferAddrOfStorage(n.Object)
		}
	}
	return RefStorageAny
}
