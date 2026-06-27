package semantic

import (
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// isUnhandledErrorUnionType reports whether t is an error union carrying at least one
// error variant. Such a value is affine in spirit: it must be consumed (handled or
// propagated), never silently dropped.
func isUnhandledErrorUnionType(t Type) bool {
	eu, ok := t.(*ErrorUnionType)
	return ok && eu != nil && eu.Errors != nil && len(eu.Errors.Tags) > 0
}

func isAffineHandleType(t Type) bool {
	switch tt := t.(type) {
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		if !ok {
			return false
		}
		return base.Affine
	case *StructType:
		return tt.Affine
	default:
		return false
	}
}

func isBorrowableAffineOwnerType(t Type) bool {
	// User-declared `linear struct` values are borrowable owners: a `&value`
	// borrow may be taken (e.g. to witness a held FsGuard at a guest call)
	// without consuming the value, and the existing borrowed-owner escape /
	// consume-ordering analysis keeps the must-consume obligation sound. The
	// builtin handle types (Thread/Task/MutexGuard) are intentionally excluded
	// (directProtocolLeakKind returns their specific kinds, not "linear value").
	if directProtocolLeakKind(t) == "linear value" {
		return true
	}
	return isBuiltinProtocolOwnerType(t, "ThreadPool") || isBuiltinProtocolOwnerType(t, "TaskGroup")
}

func affineHandleKind(t Type) string {
	if !isAffineHandleType(t) {
		return "value containing linear handles"
	}
	switch tt := t.(type) {
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			switch base.Name {
			case "Thread":
				return "thread handle"
			case "Task":
				return "task handle"
			case "MutexGuard":
				return "mutex guard"
			case "ThreadPool":
				return "thread pool owner"
			case "TaskGroup":
				return "task group owner"
			}
		}
	case *StructType:
		switch tt.Name {
		case "Thread":
			return "thread handle"
		case "Task":
			return "task handle"
		case "MutexGuard":
			return "mutex guard"
		case "ThreadPool":
			return "thread pool owner"
		case "TaskGroup":
			return "task group owner"
		}
	}
	return "linear value"
}

func (a *Analyzer) consumeAffineValueExpr(expr ast.Expr, expected Type, reason string) {
	if unionType, ok := expected.(*ErrorUnionType); ok && unionType != nil {
		expected = unionType.Value
	}
	if expr == nil || !a.containsAffineHandleValues(expected, map[string]bool{}) {
		return
	}
	if moved, ok := explicitMoveOperand(expr); ok {
		key, ok := a.lookupAffineValueKey(moved)
		if !ok {
			return
		}
		if a.currentAffineValues == nil {
			a.currentAffineValues = map[affineValueKey]affineValueState{}
		}
		if a.currentAffineValues[key].ScheduledForDefer {
			a.errorf(expr.Pos(), "linear value %q is already scheduled for deferred consumption by a `defer function` body; it must not be consumed again inline", affineValueDisplayName(moved))
			return
		}
		a.recordAffineConsumption(key, reason)
		return
	}
	if _, ok := a.lookupAffineValueKey(expr); ok {
		affineType := expected
		if actual := a.exprTypes[expr]; actual != nil && a.containsAffineHandleValues(actual, map[string]bool{}) {
			affineType = actual
		}
		a.errorf(expr.Pos(), "%s %q must be moved explicitly before %s", affineHandleKind(affineType), affineValueDisplayName(expr), reason)
	}
}

func (a *Analyzer) recordAffineConsumption(key affineValueKey, reason string) {
	if a.currentAffineValues == nil {
		a.currentAffineValues = map[affineValueKey]affineValueState{}
	}
	state := a.currentAffineValues[key]
	if state.ConsumedBy == "" {
		state.ConsumedBy = reason
	}
	state.LiveProtocolType = nil
	state.LiveProtocolDescription = ""
	a.currentAffineValues[key] = state
	for existingKey, existingState := range a.currentAffineValues {
		if existingKey == key || existingKey.Root != key.Root {
			continue
		}
		if key.Path != "" && !affinePathContains(key.Path, existingKey.Path) {
			continue
		}
		existingState.LiveProtocolType = nil
		existingState.LiveProtocolDescription = ""
		if existingState.ConsumedBy == "" {
			existingState.ConsumedBy = reason
		}
		a.currentAffineValues[existingKey] = existingState
	}
}

func (a *Analyzer) consumeHandledErrorUnionExpr(expr ast.Expr, unionType *ErrorUnionType, reason string) {
	if expr == nil || unionType == nil || !a.containsAffineHandleValues(unionType, map[string]bool{}) {
		return
	}
	key, ok := a.lookupAffineValueKey(expr)
	if !ok {
		return
	}
	a.recordAffineConsumption(key, reason)
}

func (a *Analyzer) lookupProtocolTargetKey(expr ast.Expr) (affineValueKey, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.lookupProtocolTargetKey(n.Inner)
	case *ast.MoveExpr:
		return a.lookupProtocolTargetKey(n.Operand)
	case *ast.CastExpr:
		return a.lookupProtocolTargetKey(n.Operand)
	case *ast.AddrOfExpr:
		if key, ok := a.lookupAffineValueKey(n.Operand); ok {
			return key, true
		}
		return a.lookupBorrowedOwnerRefKey(n.Operand)
	default:
		if key, ok := a.lookupBorrowedOwnerRefKey(expr); ok {
			return key, true
		}
		return a.lookupAffineValueKey(expr)
	}
}

func (a *Analyzer) clearAffineValueTarget(expr ast.Expr) {
	key, ok := a.lookupAffineValueKey(expr)
	if !ok || a.currentAffineValues == nil {
		return
	}
	for existing := range a.currentAffineValues {
		if existing.Root != key.Root {
			continue
		}
		if key.Path == "" {
			delete(a.currentAffineValues, existing)
			continue
		}
		if existing.Path == key.Path || strings.HasPrefix(existing.Path, key.Path+".") {
			delete(a.currentAffineValues, existing)
		}
	}
}

func (a *Analyzer) lookupAffineValueState(expr ast.Expr) (affineValueState, bool) {
	key, ok := a.lookupAffineValueKey(expr)
	if !ok {
		return affineValueState{}, false
	}
	return a.lookupAffineValueStateForKey(key)
}

func (a *Analyzer) lookupAffineValueStateForKey(key affineValueKey) (affineValueState, bool) {
	if a.currentAffineValues == nil {
		return affineValueState{}, false
	}
	state, ok := a.currentAffineValues[key]
	if ok && state.ConsumedBy != "" {
		return state, true
	}
	for existing, existingState := range a.currentAffineValues {
		if existing.Root != key.Root {
			continue
		}
		if existingState.ConsumedBy != "" && (affinePathContains(existing.Path, key.Path) || affinePathContains(key.Path, existing.Path)) {
			return existingState, true
		}
	}
	return affineValueState{}, false
}

func (a *Analyzer) lookupAffineValueKey(expr ast.Expr) (affineValueKey, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.lookupAffineValueKey(n.Inner)
	case *ast.MoveExpr:
		return a.lookupAffineValueKey(n.Operand)
	case *ast.CastExpr:
		return a.lookupAffineValueKey(n.Operand)
	case *ast.Ident:
		if a.currentScope == nil {
			return affineValueKey{}, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok {
			return affineValueKey{}, false
		}
		if sym.Kind != SymbolLocal && sym.Kind != SymbolParam {
			return affineValueKey{}, false
		}
		return affineValueKey{Root: sym}, true
	case *ast.FieldExpr:
		base, ok := a.lookupAffineValueKey(n.Object)
		if !ok {
			return affineValueKey{}, false
		}
		objType := a.exprTypes[n.Object]
		if objType == nil {
			objType = a.analyzeExpr(n.Object)
		}
		field, ok := a.lookupField(objType, n.Field, n.Pos())
		if !ok || !a.containsTrackedProtocolCarrierValues(field.Type, map[string]bool{}) {
			return affineValueKey{}, false
		}
		if base.Path == "" {
			base.Path = n.Field
		} else {
			base.Path = base.Path + "." + n.Field
		}
		return base, true
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return affineValueKey{}, false
		}
		base, ok := a.lookupAffineValueKey(n.Object)
		if !ok {
			return affineValueKey{}, false
		}
		objType := a.exprTypes[n.Object]
		if objType == nil {
			objType = a.analyzeExpr(n.Object)
		}
		if elemType, ok := affineIndexedElemType(objType); ok && a.containsTrackedProtocolCarrierValues(elemType, map[string]bool{}) {
			return base, true
		}
		return affineValueKey{}, false
	default:
		return affineValueKey{}, false
	}
}

func affinePathContains(ancestor, descendant string) bool {
	if ancestor == "" {
		return true
	}
	if descendant == ancestor {
		return true
	}
	if strings.HasPrefix(descendant, ancestor+".") {
		return true
	}
	if strings.HasPrefix(descendant, ancestor+"[") {
		return true
	}
	return false
}

// rejectAffineIndexOverwrite catches `c[i] <- v` where c's element type contains affine
// handles: the OLD element at index i is overwritten (dropped) without being consumed. Since
// affine containers are tracked at root granularity, the dropped element silently escapes its
// must-consume obligation. (Compiler-internal index-fill lowerings live in the backend and are
// never re-analyzed here, so this only constrains user-written indexed assignment.)
func (a *Analyzer) rejectAffineIndexOverwrite(target ast.Expr) {
	idx, ok := target.(*ast.IndexExpr)
	if !ok || idx.Object == nil || idx.Fallback != nil {
		return
	}
	objType := a.exprTypes[idx.Object]
	if objType == nil {
		objType = a.analyzeExpr(idx.Object)
	}
	elemType, ok := affineIndexedElemType(objType)
	if !ok {
		return
	}
	a.rejectAffineElementDrop(idx.Object, elemType, "indexed assignment")
}

func affineIndexedElemType(t Type) (Type, bool) {
	switch tt := t.(type) {
	case *ArrayType:
		return tt.Elem, true
	case *DArrayType:
		return tt.Elem, true
	case *ViewType:
		return tt.Elem, true
	case *RefType:
		return affineIndexedElemType(tt.Elem)
	default:
		return nil, false
	}
}

func affineValueDisplayName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return affineValueDisplayName(n.Inner)
	case *ast.MoveExpr:
		return affineValueDisplayName(n.Operand)
	case *ast.CastExpr:
		return affineValueDisplayName(n.Operand)
	case *ast.Ident:
		return n.Name
	case *ast.FieldExpr:
		base := affineValueDisplayName(n.Object)
		if base == "" {
			return n.Field
		}
		return base + "." + n.Field
	default:
		return "<value>"
	}
}

func explicitMoveOperand(expr ast.Expr) (ast.Expr, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return explicitMoveOperand(n.Inner)
	case *ast.MoveExpr:
		return n.Operand, true
	default:
		return nil, false
	}
}

func (a *Analyzer) analyzeCondExpr(expr ast.Expr) Type {
	return a.analyzeCondExprInScope(expr, a.currentScope)
}

func (a *Analyzer) analyzeCondExprInScope(expr ast.Expr, scope *Scope) Type {
	saved := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = saved }()

	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.analyzeCondExprInScope(n.Inner, scope)
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			operand := a.analyzeCondExprInScope(n.Operand, scope)
			if !IsBoolType(operand) {
				a.errorf(n.Pos(), "not operator requires bool operand")
			}
			return a.namedTypes["bool"]
		}
		return a.analyzeExpr(n)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			left := a.analyzeCondExprInScope(n.Left, scope)
			right := a.analyzeCondExprInScope(n.Right, a.refinedScopeForCondition(scope, n.Left, true))
			if !IsBoolType(left) || !IsBoolType(right) {
				a.errorf(n.Pos(), "logical operator requires bool operands")
			}
			return a.namedTypes["bool"]
		case lexer.TOKEN_OR:
			left := a.analyzeCondExprInScope(n.Left, scope)
			right := a.analyzeCondExprInScope(n.Right, a.refinedScopeForCondition(scope, n.Left, false))
			if !IsBoolType(left) || !IsBoolType(right) {
				a.errorf(n.Pos(), "logical operator requires bool operands")
			}
			return a.namedTypes["bool"]
		default:
			return a.analyzeExpr(n)
		}
	default:
		return a.analyzeExpr(expr)
	}
}
