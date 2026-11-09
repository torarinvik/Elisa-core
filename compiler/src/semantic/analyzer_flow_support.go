package semantic

import (
	"elisacore/src/ast"
)

// ghostInitIsErasureSafe reports whether a ghost variable's initializer is a side-effect-free
// expression that is safe to erase from codegen. It permits reads (idents, literals), arithmetic and
// comparisons, parenthesization, field/index access, and the contract pseudo-call `old(...)`. A
// general call is rejected, since it might mutate real state whose effect erasure would silently
// drop. This mirrors the "verification code must be pure" property the lemma machinery relies on.
// markGhostContract flags a contract condition that reads a ghost variable, so codegen erases its
// runtime check (a ghost has no runtime storage; the obligation is already discharged statically).
func (a *Analyzer) markGhostContract(cond ast.Expr) {
	if cond == nil {
		return
	}
	if a.ghostContracts == nil {
		a.ghostContracts = map[ast.Expr]bool{}
	}
	a.ghostContracts[cond] = true
}

func ghostInitIsErasureSafe(expr ast.Expr) bool {
	switch n := expr.(type) {
	case nil:
		return true
	case *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.CharLit, *ast.StringLit, *ast.NullLit:
		return true
	case *ast.ParenExpr:
		return ghostInitIsErasureSafe(n.Inner)
	case *ast.UnaryExpr:
		return ghostInitIsErasureSafe(n.Operand)
	case *ast.BinaryExpr:
		return ghostInitIsErasureSafe(n.Left) && ghostInitIsErasureSafe(n.Right)
	case *ast.FieldExpr:
		return ghostInitIsErasureSafe(n.Object)
	case *ast.IndexExpr:
		return ghostInitIsErasureSafe(n.Object) && ghostInitIsErasureSafe(n.Index)
	case *ast.QuantifierExpr:
		return ghostInitIsErasureSafe(n.Body)
	case *ast.CallExpr:
		// Only the contract pseudo-call `old(expr)` is allowed; it has no runtime effect (the backend
		// snapshots its argument at entry for `ensure`, and it is itself ghost here).
		if id, ok := n.Func.(*ast.Ident); ok && id.Name == "old" {
			for _, arg := range n.Args {
				if !ghostInitIsErasureSafe(arg) {
					return false
				}
			}
			return true
		}
		return false
	default:
		return false
	}
}

func concreteBorrowedViewBindingType(declared Type, actual Type) (Type, bool) {
	declaredView, ok := declared.(*ViewType)
	if ok {
		actualView, actualOK := actual.(*ViewType)
		if !actualOK || !SameType(declaredView.Elem, actualView.Elem) {
			return nil, false
		}
		// Specialize to the actual slice's concrete bounds (for indexed bounds-checking), but keep the
		// DECLARED mutability: `v: view[T] = mutableSlice` narrows to read-only, and `v: mutable view[T]`
		// stays writable. (The declaration-site AssignableTo already rejected `mutable view[T]` from a
		// read-only source, so a declared-mutable binding here is always backed by a mutable source.)
		specialized := *actualView
		specialized.Mutable = declaredView.Mutable
		return &specialized, true
	}
	declaredSView, ok := declared.(*SViewType)
	if !ok || declaredSView == nil || declaredSView.Region != "" {
		return nil, false
	}
	actualSView, ok := actual.(*SViewType)
	if !ok || actualSView == nil || actualSView.Region == "" {
		return nil, false
	}
	// A bare `sview` annotation intentionally leaves the lifetime to inference. Keep the source
	// region on the local's type so aliases and field/index uses remain tied to their live backing
	// storage; otherwise `view: sview = bytes.as_sview()` erased the only static proof that the
	// pointee survives until the last use of `view`.
	specialized := *declaredSView
	specialized.Region = actualSView.Region
	return &specialized, true
}

// concreteContainerRegionBindingType preserves the storage region of a
// region-carrying container returned by an initializer when the written local
// type leaves its region unspecified. The annotation constrains the container
// shape and element type; it does not relocate the returned backing buffer.
// Without this specialization, stampContainerRegion could incorrectly assign
// the local's ambient auto region, making later reads appear shorter-lived than
// the actual returned container (or, worse, losing a lifetime constraint).
func concreteContainerRegionBindingType(declared Type, actual Type) (Type, bool) {
	declaredArray, ok := declared.(*DArrayType)
	if !ok || declaredArray == nil || declaredArray.Region != "" {
		return nil, false
	}
	actualArray, ok := actual.(*DArrayType)
	if !ok || actualArray == nil || actualArray.Region == "" || !SameType(declaredArray.Elem, actualArray.Elem) {
		return nil, false
	}
	specialized := *declaredArray
	specialized.Region = actualArray.Region
	return &specialized, true
}

func (a *Analyzer) analyzeMoveBindStmt(stmt *ast.MoveBindStmt) {
	if stmt == nil {
		return
	}
	valueType := a.analyzeExpr(stmt.Value)
	valueState, hasValueState := a.regionRefStateForExpr(stmt.Value)
	borrowedOwnerState, hasBorrowedOwnerState := a.borrowedOwnerRefStateForExpr(stmt.Value)
	switch p := stmt.Pattern.(type) {
	case *ast.MoveBindNamePattern:
		if p.Name != "_" {
			sym := &Symbol{Name: p.Name, Kind: SymbolLocal, Type: valueType, Node: p, Mutable: false}
			a.defineLocal(sym, p.Pos())
			a.recordValueBinding(sym, stmt.Value)
			a.recordFunctionValueBinding(sym, stmt.Value)
			a.recordImmutableSymbolOptimizationFacts(sym, stmt.Value)
			if hasBorrowedOwnerState {
				a.currentBorrowedOwnerRefs[sym] = borrowedOwnerState
			}
			if hasValueState {
				a.recordResolvedRegionRefBinding(sym, valueState)
			} else {
				a.recordRegionRefBinding(sym, stmt.Value)
			}
		}
	case *ast.MoveBindStructPattern:
		fields, ok := a.resolveMoveBindStructPattern(p, valueType)
		if !ok {
			return
		}
		for i, arg := range p.Args {
			if i >= len(fields) || arg.Name == "_" {
				continue
			}
			sym := &Symbol{Name: arg.Name, Kind: SymbolLocal, Type: fields[i].Type, Node: p, Mutable: false}
			a.defineLocal(sym, arg.Position)
			fieldExpr := &ast.FieldExpr{Position: arg.Position, Object: stmt.Value, Field: fields[i].Name}
			a.recordValueBinding(sym, fieldExpr)
			a.recordFunctionValueBinding(sym, fieldExpr)
			a.recordImmutableSymbolOptimizationFacts(sym, fieldExpr)
			if hasBorrowedOwnerState {
				if fieldState, ok := projectBorrowedOwnerRefFieldState(borrowedOwnerState, fields[i].Name); ok {
					a.currentBorrowedOwnerRefs[sym] = fieldState
				}
			}
			if !hasValueState {
				continue
			}
			if fieldState, ok := projectRegionFieldState(valueState, fields[i].Name); ok {
				a.recordResolvedRegionRefBinding(sym, fieldState)
			}
		}
	case *ast.MoveBindVariantPattern:
		payloads, enumType, packedStoreState, ok := a.resolveMoveBindVariantPattern(stmt, p, valueType)
		if !ok {
			return
		}
		_ = enumType
		a.bindResolvedMoveBindVariantFields(payloads, stmt.Value, p, p, valueState, hasValueState, borrowedOwnerState, hasBorrowedOwnerState, packedStoreState)
	default:
		a.errorf(stmt.Pos(), "unsupported move-as pattern %T", stmt.Pattern)
		return
	}
	a.consumeAffineValueExpr(stmt.Value, valueType, "move-as destructure")
}

// checkIndexedElementStoreEscape routes `container[i] <- v` through the same element-store
// escape rule as `container.push(v)` / `dict.put(k, v)`.
func (a *Analyzer) checkIndexedElementStoreEscape(target, value ast.Expr, valueType Type) {
	idx, ok := target.(*ast.IndexExpr)
	if !ok || idx == nil || idx.Index2 != nil {
		return
	}
	objType := a.exprType(idx.Object)
	for {
		ref, isRef := objType.(*RefType)
		if !isRef || ref == nil {
			break
		}
		objType = ref.Elem
	}
	switch ct := objType.(type) {
	case *DArrayType:
		if ct != nil && ct.Region == "" {
			// A region-less container parameter that is only index-stored (never grown) gets no
			// inferred `__rg_<param>` region; its storage is still caller-owned and outlives every
			// local region, so name it the same way the grown form is named for the check.
			if name := a.indexedStoreParamRoot(idx.Object); name != "" {
				copied := *ct
				copied.Region = inferredRegionParamName(a.currentFuncDecl, name)
				ct = &copied
				// This parameter's storage is caller-owned, but its lifetime is unrelated
				// to every source-declared or inferred region parameter in the function.
				a.regionParamScopes = append(a.regionParamScopes, map[string]bool{copied.Region: true})
				a.regionUnrelatedParamScopes = append(a.regionUnrelatedParamScopes, map[string]bool{copied.Region: true})
				defer func() {
					a.regionParamScopes = a.regionParamScopes[:len(a.regionParamScopes)-1]
					a.regionUnrelatedParamScopes = a.regionUnrelatedParamScopes[:len(a.regionUnrelatedParamScopes)-1]
				}()
			}
		}
		if ct != nil {
			a.checkNestedRegionElementStoreEscape(value, ct, ct.Elem, valueType)
			// Fresh producers (struct/list literals, ternaries) and by-value copies hide their
			// interior region behind their own type; mirror push's interior checks.
			a.checkFreshProducerElementEscape(idx.Object, ct, value)
			if !isFreshContainerProducer(value) {
				a.checkInteriorRegionAgainstTarget(idx.Object, containerRegion(ct), value, "by-value copy")
			}
		}
	case *DictType:
		if ct != nil {
			a.checkNestedRegionElementStoreEscape(value, ct, ct.Value, valueType)
		}
	}
}

// indexedStoreParamRoot returns the parameter name when expr is a plain identifier naming (not
// shadowing) a parameter of the current function, else "".
func (a *Analyzer) indexedStoreParamRoot(expr ast.Expr) string {
	id, ok := expr.(*ast.Ident)
	if !ok || id == nil || a.currentFuncDecl == nil || a.currentScope == nil {
		return ""
	}
	sym, ok := a.currentScope.Lookup(id.Name)
	if !ok || sym == nil {
		return ""
	}
	for i := range a.currentFuncDecl.Params {
		if a.currentFuncDecl.Params[i].Name != id.Name {
			continue
		}
		if paramSym, ok := a.currentFuncParamSymbol(i); ok && paramSym == sym {
			return id.Name
		}
	}
	return ""
}

// valueTypeCarriesClosure reports whether a value of type t holds a function value by value: the
// function itself, or one nested in a tuple, optional, fixed array or struct field. Such a value
// carries its captures' container headers, which no threaded region parameter keeps alive.
func valueTypeCarriesClosure(t Type, seen map[Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *FuncType:
		return true
	case *TupleType:
		for _, field := range tt.Fields {
			if valueTypeCarriesClosure(field.Type, seen) {
				return true
			}
		}
	case *OptionalType:
		return valueTypeCarriesClosure(tt.Value, seen)
	case *ArrayType:
		return valueTypeCarriesClosure(tt.Elem, seen)
	case *StructType:
		for _, field := range tt.Fields {
			if valueTypeCarriesClosure(field.Type, seen) {
				return true
			}
		}
	}
	return false
}
