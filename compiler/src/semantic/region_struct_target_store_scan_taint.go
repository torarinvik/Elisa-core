package semantic

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"elisacore/src/ast"
)

// storeScanTaintCallWrites taints each body local passed to call with the call's other
// arguments when the callee may write that local's parameter.
func (a *Analyzer) storeScanTaintCallWrites(decl *ast.FuncDecl, call *ast.CallExpr, params, locals map[string]bool, taint func(string, ast.Expr) bool, idx *storeScanDeclIndex, targets, holders map[string]bool, addHolder func(string) bool, header, content, views, scalars map[string]bool, addContent func(string) bool) bool {
	decl = a.readDecl(decl)
	changed := false
	for _, w := range a.storeScanCallWritePlan(decl, call, params, locals) {
		// Neither holder discovery nor taint can add a parameter, scalar, or
		// empty name. Avoid both recursive callee proofs for those entries.
		if w.local == "" || params[w.local] || scalars[w.local] {
			continue
		}
		shareChecked, shareReaches := false, false
		shares := func() bool {
			if w.callee == nil {
				return true
			}
			if !shareChecked {
				shareReaches = a.storeScanParamReaches(storeScanShareKey(w.callee, w.into, w.from))
				shareChecked = true
			}
			return shareReaches
		}
		// A holder: the callee may store (part of) the target place it is given into the local
		// (`stash(holder, table.deep.lists)`), whose stores then reach the target.
		if (len(targets) != 0 || len(holders) != 0) && !targets[w.local] && !holders[w.local] && (storeScanMayAliasPlace(idx, w.value, targets) || storeScanMayAliasPlace(idx, w.value, holders)) && a.storeScanHolderMayAlias(decl, w) &&
			shares() && addHolder(w.local) {
			changed = true
			if storeScanTrace {
				fmt.Fprintf(os.Stderr, "TGTHOLD %s src=%s local=%s at %s writer=%s\n", decl.Name, storeScanTraceOther, w.local, call.Pos(), storeScanParamWriterOf[storeScanShareKey(w.callee, w.into, w.from)])
			}
		}
		// Holder discovery above remains necessary, but no call summary can
		// add taint to an excluded or already saturated local.
		if content[w.local] && (header[w.local] || views[w.local]) {
			continue
		}
		// Header taint is always also content taint. For simple names and
		// projections, absence from content proves that neither taint path
		// below can change the local, without scanning the callee's body.
		if name, known := storeScanSimpleMentionName(w.value); known && !content[name] && !content[rootNameOfDotted(name)] {
			continue
		}
		if w.callee != nil && !a.storeScanParamMayBeWritten(w.callee, w.into) {
			continue // the resolved callee never writes through that parameter
		}
		if w.callee != nil && !shares() {
			// The callee never stores a value sharing parameter from's storage into into's: the
			// local receives at most what the callee reads out of the source (content), never
			// its header. (Share facts are per (into, from) pair.)
			if idx.mentions(w.value, content) && addContent(w.local) {
				changed = true
			}
			continue
		}
		if taint(w.local, w.value) {
			changed = true
			if storeScanTrace {
				fmt.Fprintf(os.Stderr, "TAINTW %s src=%s local=%s at %s\n", decl.Name, storeScanTraceOther, w.local, call.Pos())
			}
		}
	}
	return changed
}

// storeScanCallWrite: the callee of a call may write value into the body local.
type storeScanCallWrite struct {
	local string
	value ast.Expr
	// arg is the argument place the callee writes through (`local`, `local.items`).
	arg ast.Expr
	// callee (nil when unresolved) writes its parameter `from` (value) into `into` (local).
	callee     *ast.FuncDecl
	from, into int
}

// storeScanCallWritePlan: the (local, value) writes a call may perform. It depends only on the
// declaration and the call (params/locals are the declaration's), so it is computed once.
func (a *Analyzer) storeScanCallWritePlan(decl *ast.FuncDecl, call *ast.CallExpr, params, locals map[string]bool) []storeScanCallWrite {
	decl = a.readDecl(decl)
	if plan, ok := a.storeScanCallWriteCache[call]; ok {
		return plan
	}
	var plan []storeScanCallWrite
	defer func() {
		if a.storeScanCallWriteCache == nil {
			a.storeScanCallWriteCache = map[*ast.CallExpr][]storeScanCallWrite{}
		}
		a.storeScanCallWriteCache[call] = plan
	}()
	args := returnBorrowCallArgs(call)
	if len(args) < 2 {
		return plan
	}
	if field, isMethod := call.Func.(*ast.FieldExpr); isMethod && field != nil && len(args) == len(call.Args) {
		if a.storeScanReceiverIsBuiltinDArray(decl, field.Object) || storeScanReadOnlyMethods[field.Field] {
			return plan
		}
	}
	callee, resolved := a.resolveReadOnlyScanCallee(call)
	resolved = resolved && callee != nil && storeFlowCallFitsParams(callee, call)
	for i, arg := range args {
		root := rootIdentExpr(stripAddrAndParens(arg))
		if root == nil || params[root.Name] || !locals[root.Name] {
			continue
		}
		if resolved && (!a.declParamIsWritableRef(callee, i) || a.declParamIsViewOrScalar(callee, i)) {
			continue // the callee cannot write through this parameter
		}
		if resolved && a.declParamIsProgramLived(callee, i) {
			// docs/128: what the callee writes through a program-lived parameter must itself be
			// program-lived (checked in the callee), so it carries no storage of this body's sources.
			continue
		}
		for j, other := range args {
			if j == i || (resolved && !a.declParamCarriesStorage(callee, j) && !a.declParamMayEncodeAddress(callee, j)) {
				continue
			}
			w := storeScanCallWrite{local: root.Name, value: other, arg: stripAddrAndParens(arg)}
			if resolved {
				w.callee, w.from, w.into = callee, j, i
			}
			plan = append(plan, w)
		}
	}
	return plan
}

// declParamIsViewOrScalar: decl's parameter index has a view-only or plain scalar type, which
// no callee can store into.
func (a *Analyzer) declParamIsViewOrScalar(decl *ast.FuncDecl, index int) bool {
	decl = a.readDecl(decl)
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || index < 0 || index >= len(fnType.Params) {
		return false
	}
	t := fnType.Params[index]
	// References are never view-only or scalar, regardless of their pointee.
	// Check the outer type before walking potentially large recursive payloads.
	if _, isRef := t.(*RefType); isRef {
		return false
	}
	if t == nil || a.containsTypeParamCached(t) {
		return false
	}
	if _, isOpaque := t.(*OpaqueType); isOpaque {
		// A by-value foreign handle (`extern LLVMValueRef`) is a copy: the callee cannot change
		// what the caller's local holds, and what it writes behind the handle is foreign memory.
		return true
	}
	return a.storeScanViewOnlyCached(t) || storeScanPlainScalarType(t)
}

// storeScanTaintBinders: a binding construct whose header mentions a tainted name taints its
// binders. Pattern binders of a match / `is` test project fields or payloads (content); only
// other binders (a rebind of a call result, a destructure) may be the whole.
func (a *Analyzer) storeScanTaintBinders(decl *ast.FuncDecl, idx *storeScanDeclIndex, n ast.Stmt, header, content map[string]bool, add func(map[string]bool, string) bool, changed *bool) {
	decl = a.readDecl(decl)
	if n == nil {
		return
	}
	bound := idx.boundNames(n)
	readsContent, readsHeader := idx.headerMentionsTwo(n, content, header)
	if m, isMatch := n.(*ast.MatchStmt); isMatch && m != nil {
		// A match's binders receive parts of its scrutinee only; its patterns NAME the binders
		// (a tainted `then_body` in `St.If(cond, then_body, ..)` is being bound, not read) and
		// its guards read without flowing into them.
		readsContent = idx.mentions(m.Value, content) || idx.mentions(m.Store, content)
		readsHeader = idx.mentions(m.Value, header) || idx.mentions(m.Store, header)
	}
	if !readsContent {
		return
	}
	whole := readsHeader && !storeScanProjectingStmt(n)
	for _, name := range bound {
		// A by-value loop capture whose element type cannot contain region storage does
		// not inherit taint from the container's header or contents. Opaque foreign
		// handles such as LLVMValueRef are copied values, not Elisa borrows.
		if elem, ok := a.storeScanLoopBinderElemType(decl, &ast.Ident{Name: name}); ok && elem != nil && !a.containsTypeParamCached(elem) && !a.storeScanTypeMayEncodeAddress(elem, map[Type]bool{}) && a.storeScanHoldsNoRegionStorage(elem) {
			continue
		}
		if add(content, name) {
			*changed = true
		}
		if whole && add(header, name) {
			*changed = true
		}
	}
}

// calleeReturnModeOf: what may the callee RETURN from parameter `other` -- nothing, values read
// out of it, or the value itself? Judged on its return statements (and an expression-bodied
// tail), with calls in a returned expression recursed into. Unknown means the value itself.
func (a *Analyzer) calleeReturnModeOf(decl *ast.FuncDecl, other int, seen map[storeScanKey]bool) storeMode {
	decl = a.readDecl(decl)
	if decl == nil || other < 0 || other >= len(decl.Params) || len(decl.Body) == 0 {
		return storeHeader
	}
	// A scalar/pointer-free result cannot retain any parameter storage. This
	// applies before body typing as well: reading a container's count must not
	// make the resulting integer borrow that container. Parameter writes remain
	// independently visible through calleeStoreModeIntoBody.
	if symbol := a.funcDeclSymbols[decl]; symbol != nil {
		if function, ok := symbol.Type.(*FuncType); ok && function != nil {
			if a.scalarReturnHasNoReferenceProvenance(function) {
				return storeNone
			}
			// A completely program-lived result (including a record whose fields
			// carry those contracts) cannot retain shorter-lived parameter storage.
			// The body pass still validates every field and return independently;
			// cold read-only copies must preserve the resolved result type.
			if !returnRegionListContains(function.RegionParams, function.ReturnRegion) &&
				a.storeScanTypeIsProgramLivedForStoreProof(function.Return) {
				return storeNone
			}
		}
	}
	if a.declParamIsProgramLived(decl, other) {
		return storeNone // docs/128: a program-lived parameter's parts are program-lived
	}
	key := storeScanKey{decl, other, -1}
	if mode, ok := a.lookupStoreScanMode(key); ok {
		return mode
	}
	if seen[key] {
		a.storeScanOpenCycle(key)
		return storeNone
	}
	if mode, ok := a.storeScanReuseProvisional(key); ok {
		return mode
	}
	seen[key] = true
	defer delete(seen, key) // the DFS stack (see calleeStoreModeIntoBody)
	saved := a.storeScanCycleHit
	a.storeScanCycleHit = false
	mark := a.storeScanPushFrame(key)
	scanIdx := a.storeScanIndex(decl)
	_, header, content, _, views := a.storeScanTaint(decl, other, "", seen)
	filled := a.lazyFilledLocals(decl, other, content, views, seen)
	mode := storeNone
	sawReturn := len(scanIdx.returns) != 0
	if !sawReturn && !a.declReturnsVoid(decl) {
		// A value produced without a `return` (an expression tail) is not modelled.
		mode = storeHeader
	}
	var returnedMode func(ast.Expr) storeMode
	returnedMode = func(expr ast.Expr) storeMode {
		value := storeScanUnwrap(expr)
		// The selected value carries storage; its predicate only controls the
		// selection. Store effects of calls in the predicate are scanned by
		// calleeStoreModeIntoBody independently from this return summary.
		if choice, ok := value.(*ast.TernaryExpr); ok && choice != nil {
			first := returnedMode(choice.Value)
			if first == storeHeader {
				return first
			}
			return maxStoreMode(first, returnedMode(choice.Alt))
		}
		if len(a.programRegions) != 0 {
			t, known := a.lookupExprType(value)
			if !known {
				t, known = a.storeScanDeclTypeOf(decl, value)
			}
			if known && a.storeScanTypeIsProgramLivedForStoreProof(t) {
				return storeNone
			}
		}
		if call, isCall := value.(*ast.CallExpr); isCall && call != nil {
			if callee, ok := a.resolveReadOnlyScanCallee(call); ok && callee != nil && storeFlowCallFitsParams(callee, call) && !funcHasArenaParam(callee) {
				callMode := storeNone
				for i, arg := range returnBorrowCallArgs(call) {
					argMode := storeNone
					if storeScanMentionsHeader(arg, header) {
						argMode = storeHeader
					} else if storeScanReturnMentions(arg, content) {
						argMode = storeElements
					}
					if argMode != storeNone {
						callMode = maxStoreMode(callMode, narrowStoreMode(argMode, a.calleeReturnModeOf(callee, i, seen)))
						if callMode == storeHeader {
							return callMode
						}
					}
				}
				return callMode
			}
		}
		if !storeScanReturnMentions(value, content) {
			return storeNone
		}
		if views != nil && (a.storeScanValueIsView(decl, value, views) || a.storeScanDeclReturnsViewOnly(decl) || storeScanRootedAtFilled(value, filled)) {
			return storeViews
		}
		if storeScanMentionsHeader(value, header) {
			return storeHeader
		}
		return storeElements
	}
	for _, ret := range scanIdx.returns {
		if mode == storeHeader {
			break
		}
		if ret == nil || ret.Value == nil {
			continue
		}
		mode = maxStoreMode(mode, returnedMode(ret.Value))
	}
	final := a.storeScanPopFrame(key, mark, mode)
	if final {
		a.cacheStoreScan(key, mode)
	}
	a.storeScanCycleHit = saved || (a.storeScanCycleHit && !final)
	return mode
}

// A simple identifier or field chain has no resolver-populated children.
// Compound return expressions can gain resolved call arguments during analysis,
// so keep the live walk for them rather than reusing earlier token snapshots.
func storeScanReturnMentions(expr ast.Expr, names map[string]bool) bool {
	if name, simple := storeScanSimpleMentionName(expr); simple {
		if names[name] {
			return true
		}
		root := rootNameOfDotted(name)
		return root != name && names[root]
	}
	return storeScanMentions(expr, names)
}

// storeScanUnwrap strips value-preserving wrappers (parens, an inline `can ...` grant, `move`)
// so a wrapped call is still judged as a call.
func storeScanUnwrap(expr ast.Expr) ast.Expr {
	for {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			expr = e.Inner
		case *ast.CanExpr:
			if e == nil {
				return expr
			}
			expr = e.Expr
		case *ast.MoveExpr:
			if e == nil {
				return expr
			}
			expr = e.Operand
		default:
			return expr
		}
	}
}

// declReturnsVoid reports whether a declared function's resolved return type is void.
func (a *Analyzer) declReturnsVoid(decl *ast.FuncDecl) bool {
	decl = a.readDecl(decl)
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil {
		return false
	}
	builtin, ok := fnType.Return.(*BuiltinType)
	return fnType.Return == nil || ok && builtin != nil && builtin.Name == "void"
}

// declParamIsWritableRef reports whether a declared parameter is a mutable reference (including
// `lmut`), the only way a callee can write into caller-visible storage. Unknown means it is.
func (a *Analyzer) declParamIsWritableRef(decl *ast.FuncDecl, index int) bool {
	decl = a.readDecl(decl)
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return true
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || index < 0 || index >= len(fnType.Params) {
		return true
	}
	switch p := fnType.Params[index].(type) {
	case *RefType:
		return p == nil || p.Mutable
	case *ViewType:
		return p == nil || p.Mutable
	case *TypeParamType:
		return true
	}
	// A by-value parameter is a copy, but a copied darray/dict/set header (or a struct, tuple,
	// enum payload holding one, or a reference) SHARES its indirect storage with the caller:
	// `p[i] <- x` / `p.items[0] <- x` writes caller-visible memory. Only a by-value type that
	// reaches no shared indirect storage is exempt.
	return a.containsTypeParamCached(fnType.Params[index]) || a.storeScanSharesCached(fnType.Params[index])
}

// typeSharesIndirectStorage reports whether a by-value copy of t can still reach storage it
// shares with the original (container buffers, references, mutable views). Read-only views
// (sview, cstr) cannot be written through. Unknown types are assumed to share.
func typeSharesIndirectStorage(t Type, seen map[Type]bool) bool {
	if t == nil {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	switch tt := StripAggregateStateType(t).(type) {
	case *BuiltinType, *BitIntType, *SViewType, *CStrType, *ConstEnumType:
		return false
	case *DArrayType, *DictType, *SetType, *RefType, *ViewType:
		return true
	case *StructType:
		if tt == nil {
			return true
		}
		for _, field := range tt.Fields {
			if typeSharesIndirectStorage(field.Type, seen) {
				return true
			}
		}
		return false
	case *TupleType:
		for _, field := range tt.Fields {
			if typeSharesIndirectStorage(field.Type, seen) {
				return true
			}
		}
		return false
	case *ArrayType:
		return typeSharesIndirectStorage(tt.Elem, seen)
	case *OptionalType:
		return typeSharesIndirectStorage(tt.Value, seen)
	case *EnumType:
		if tt == nil {
			return true
		}
		for _, variant := range tt.Variants {
			if variant == nil {
				continue
			}
			for _, payload := range variant.Payload {
				if typeSharesIndirectStorage(payload, seen) {
					return true
				}
			}
		}
		for _, field := range tt.Common {
			if typeSharesIndirectStorage(field.Type, seen) {
				return true
			}
		}
		return false
	}
	return true
}

// storeScanMayAliasPlace reports whether binding a local to value may make it a REFERENCE into
// target storage: a place rooted at a target (`t`, `t.f`, `t.xs[i]`, `&t.f`), a conditional
// choosing one, or a call that may return one. A fresh literal or comprehension that merely
// reads the target (`Scope{names: [n for n in t.names]}`) owns new storage and is no alias.
func storeScanMayAliasPlace(idx *storeScanDeclIndex, value ast.Expr, targets map[string]bool) bool {
	// These value forms cannot be aliases under this proof. Check their direct
	// form before building or consulting mention tokens.
	switch value.(type) {
	case *ast.StructLitExpr, *ast.ListLitExpr, *ast.ListComprehensionExpr, *ast.TupleExpr, *ast.StringLit, *ast.BinaryExpr:
		return false
	}
	if value == nil || !idx.mentions(value, targets) {
		return false
	}
	switch stripParenExpr(value).(type) {
	case *ast.StructLitExpr, *ast.ListLitExpr, *ast.ListComprehensionExpr, *ast.TupleExpr, *ast.StringLit, *ast.BinaryExpr:
		return false
	}
	return true
}

// storeScanProjectingStmt reports statements whose binders are projections of their subject
// (match arms, `if x is P(...)`, `while ... is`): never the subject value itself.
func storeScanProjectingStmt(stmt ast.Stmt) bool {
	name := reflect.TypeOf(stmt).String()
	return strings.Contains(name, "Match") || strings.Contains(name, "If") || strings.Contains(name, "While") || strings.Contains(name, "Guard")
}

// storeScanQualifierFields are pattern string fields that name a type, variant or struct field,
// never a bound value.
var storeScanQualifierFields = map[string]bool{"EnumName": true, "Variant": true, "TypeName": true, "Field": true, "Value": true}

var storeScanPatternArgType = reflect.TypeOf(ast.MatchPatternArg{})

// storeScanReceiverIsValue reports whether a method-call receiver root names a value that could
// hold what the call stores: a local or binder of the function, or a global variable. A type or
// enum name qualifying a constructor (`Expr.Field(..)`, `Pattern.Struct(..)`) names none.
func (a *Analyzer) storeScanReceiverIsValue(name string, locals map[string]bool) bool {
	if locals[name] {
		return true
	}
	if a.globalScope == nil {
		return true
	}
	sym, ok := a.globalScope.Lookup(name)
	if !ok || sym == nil {
		_, isType := a.namedTypes[name]
		return !isType && !a.storeScanLooksLikeTypeName(name)
	}
	switch sym.Kind {
	case SymbolStruct, SymbolExternType, SymbolFunc, SymbolExternFunc:
		return false
	}
	return true
}

// storeScanLooksLikeTypeName: an unresolved capitalized root that is no local, parameter or
// global symbol is a (module-qualified) type name at a constructor call; values are lowercase or
// resolve through the global scope.
func (a *Analyzer) storeScanLooksLikeTypeName(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}
