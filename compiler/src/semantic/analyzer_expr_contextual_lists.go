package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
)

func (a *Analyzer) analyzeListLitExprWithExpected(expr *ast.ListLitExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	if expr.Brace {
		// `{k: v, ...}` is a dict literal: build a populated dict. Like `{}`/`[]` it is an
		// allocating literal, so it triggers auto-region inference and its key/value types are
		// either taken from the expected dict type or inferred from the first pair.
		if len(expr.Keys) > 0 {
			return a.analyzeDictLiteralExpr(expr, expected)
		}
		if setType, ok := contextualSetLiteralType(expected); ok {
			_ = setType
			return a.analyzeSetLiteralExpr(expr, expected)
		}
		// An empty `{}` against an expected dict type is an empty (zero-initialized) dict —
		// the dict analogue of `[]` for darray. It allocates its backing lazily on first
		// insert, and being a literal it triggers auto-region inference (functionBodyNeedsAutoRegion)
		// exactly like `[]`, so `d: dict[cstr, V] = {}; d.put(...)` needs no explicit region scope.
		// A non-empty `{a, b}` remains a set-membership literal (valid only on the RHS of `in`).
		if len(expr.Elems) == 0 {
			if dictType, ok := contextualDictLiteralType(expected); ok {
				a.recordAnalyzedExprType(expr, dictType)
				return dictType
			}
		}
		a.errorf(expr.Pos(), "brace membership set literals are only valid on the right-hand side of `in`")
		a.exprTypes[expr] = invalidType
		return invalidType
	}
	expectedArray, useExpectedArray := contextualArrayLiteralType(expected)
	expectedDArray, useExpectedDArray := contextualDArrayLiteralType(expected)
	hasSpread := false
	for _, spread := range expr.Spreads {
		if spread {
			hasSpread = true
			break
		}
	}
	if expr.Owner != nil {
		ownerType := a.analyzeRegionOwnerExpr(expr.Owner)
		arenaType := a.namedTypes["Arena"]
		isArena := arenaType != nil && AssignableTo(arenaType, ownerType)
		if !isArena {
			if refT, ok := ownerType.(*RefType); ok && refT != nil && arenaType != nil {
				isArena = AssignableTo(arenaType, refT.Elem)
			}
		}
		if !isArena {
			a.errorf(expr.Owner.Pos(), "darray literal owner must be an Arena")
		}
		if !useExpectedDArray {
			a.errorf(expr.Owner.Pos(), "list literal allocation owner requires an expected darray type")
		}
	}
	// A program-lived region (docs/128) is not named through an owner expression: a local of
	// the same name could shadow it. The backend resolves the darray type's region instead.
	if expr.Owner == nil && len(expr.Elems) > 0 && useExpectedDArray && expectedDArray.Region != "" && !a.lookupRegionParam(expectedDArray.Region) && !a.isProgramRegion(expectedDArray.Region) {
		expr.Owner = &ast.Ident{Position: expr.Position, Name: expectedDArray.Region}
	}
	// A region-less expected darray (e.g. a global's declared type) under the perm
	// ambient region allocates in perm: route the literal there explicitly, since the
	// expected type carries no region for the backend to resolve.
	if expr.Owner == nil && len(expr.Elems) > 0 && useExpectedDArray && expectedDArray.Region == "" && a.activeContainerRegionName() == "perm" {
		expr.Owner = &ast.Ident{Position: expr.Position, Name: "perm"}
	}
	if len(expr.Elems) == 0 {
		switch {
		case useExpectedArray:
			if expectedArray.HasConstSize && expectedArray.ConstSize != 0 {
				a.errorf(expr.Pos(), "array literal expects %d elements, got 0", expectedArray.ConstSize)
			}
			a.exprTypes[expr] = expectedArray
			return expectedArray
		case useExpectedDArray:
			a.exprTypes[expr] = expectedDArray
			return expectedDArray
		}
		a.errorf(expr.Pos(), "empty list literal requires an expected array or darray type")
		a.exprTypes[expr] = invalidType
		return invalidType
	}

	var elemType Type
	if useExpectedArray {
		elemType = expectedArray.Elem
		if hasSpread {
			a.errorf(expr.Pos(), "array literals do not support spread elements; use an expected darray type")
		}
		if expectedArray.HasConstSize && expectedArray.ConstSize != int64(len(expr.Elems)) {
			a.errorf(expr.Pos(), "array literal expects %d elements, got %d", expectedArray.ConstSize, len(expr.Elems))
		}
	} else if useExpectedDArray {
		elemType = expectedDArray.Elem
	} else if hasSpread {
		a.errorf(expr.Pos(), "spread list literal requires an expected darray type")
	}

	for i, elem := range expr.Elems {
		spread := i < len(expr.Spreads) && expr.Spreads[i]
		if spread {
			sourceType := a.analyzeValueExpr(elem, nil)
			if !useExpectedDArray {
				continue
			}
			if !builtinDArrayExtendSourceCompatible(expectedDArray.Elem, sourceType) {
				a.errorf(elem.Pos(), "spread darray literal element expects a compatible darray or array source of %s, got %s", expectedDArray.Elem, sourceType)
			}
			// A spread is a bulk copy, exactly like `extend`: it would duplicate every
			// linear handle in the source, so it gets the same rejection.
			if a.containsAffineHandleValues(expectedDArray.Elem, map[string]bool{}) {
				a.errorf(elem.Pos(), "spread darray literal does not support affine element type %s; push elements individually with explicit move", expectedDArray.Elem)
			}
			continue
		}
		itemType := a.analyzeValueExpr(elem, elemType)
		if useExpectedArray {
			if !AssignableTo(expectedArray.Elem, itemType) {
				a.errorf(elem.Pos(), "array literal element expects %s, got %s", expectedArray.Elem, itemType)
			}
			a.consumeAffineValueExpr(elem, expectedArray.Elem, "move into array literal element")
			continue
		}
		if useExpectedDArray {
			if !AssignableTo(expectedDArray.Elem, itemType) {
				a.errorf(elem.Pos(), "darray literal element expects %s, got %s", expectedDArray.Elem, itemType)
			}
			a.consumeAffineValueExpr(elem, expectedDArray.Elem, "move into darray literal element")
			continue
		}
		a.consumeAffineValueExpr(elem, itemType, "move into array literal element")
		if elemType == nil {
			elemType = itemType
			continue
		}
		merged := MergeTypes(elemType, itemType)
		if IsInvalidType(merged) {
			a.errorf(elem.Pos(), "array literal elements are incompatible: %s and %s", elemType, itemType)
			a.exprTypes[expr] = invalidType
			return invalidType
		}
		elemType = merged
	}

	if useExpectedArray {
		a.exprTypes[expr] = expectedArray
		return expectedArray
	}
	if useExpectedDArray {
		// A darray literal allocates, so it needs a region. dict and set literals have always
		// checked this in the analyzer; darray did not, so the only thing that caught it was
		// the backend's emitListLitExpr -- which reports through fmt.Errorf with NO source
		// position. `take([1, 2, 3])` therefore produced a bare "darray literal requires an
		// active in <arena>: scope" with no file, line or column, for the most common
		// container in the language. Same precondition as the dict site below.
		if a.constInitDepth == 0 && !a.regionAvailableForContainer(expectedDArray) && a.currentAllocExpr == nil {
			a.errorf(expr.Pos(), "%s", NoContainerRegionMessage("darray literal"))
		}
		a.exprTypes[expr] = expectedDArray
		return expectedDArray
	}
	if elemType == nil || IsInvalidType(elemType) {
		a.exprTypes[expr] = invalidType
		return invalidType
	}
	result := &ArrayType{Elem: elemType, Size: strconv.Itoa(len(expr.Elems)), HasConstSize: true, ConstSize: int64(len(expr.Elems))}
	a.exprTypes[expr] = result
	return result
}
func (a *Analyzer) analyzeListComprehensionExprWithExpected(expr *ast.ListComprehensionExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	expectedDArray, useExpectedDArray := contextualDArrayLiteralType(expected)
	expectedDict, useExpectedDict := contextualDictLiteralType(expected)
	expectedSet, useExpectedSet := contextualSetLiteralType(expected)
	regionAvailable := (useExpectedDArray && a.regionAvailableForContainer(expectedDArray)) || (useExpectedDict && a.regionAvailableForContainer(expectedDict)) || (useExpectedSet && a.regionAvailableForContainer(expectedSet))
	if expr.Owner != nil {
		ownerType := a.analyzeRegionOwnerExpr(expr.Owner)
		arenaType := a.namedTypes["Arena"]
		isArena := arenaType != nil && AssignableTo(arenaType, ownerType)
		if !isArena {
			if refT, ok := ownerType.(*RefType); ok && refT != nil && arenaType != nil {
				isArena = AssignableTo(arenaType, refT.Elem)
			}
		}
		if !isArena {
			a.errorf(expr.Owner.Pos(), "comprehension owner must be an Arena or mutable Arena&, got %s", ownerType)
		}
	} else if a.activeContainerRegionName() == "" && !regionAvailable {
		a.errorf(expr.Pos(), "%s", NoContainerRegionMessage("comprehension"))
	}
	var itemType Type
	loopScope := NewScope(a.currentScope)
	if expr.RangeEnd != nil {
		startType := a.analyzeExpr(expr.Source)
		endType := a.analyzeExpr(expr.RangeEnd)
		a.recordComprehensionEndUpperBound(expr)
		itemType = CommonNumericType(startType, endType)
		if !IsIntegralType(itemType) {
			a.errorf(expr.Pos(), "list comprehension range requires integral bounds, got %s and %s", startType, endType)
			itemType = invalidType
		}
		if expr.RangeStep != nil {
			stepType := a.analyzeExpr(expr.RangeStep)
			itemType = CommonNumericType(itemType, stepType)
			if !IsIntegralType(stepType) || !IsIntegralType(itemType) {
				a.errorf(expr.RangeStep.Pos(), "list comprehension range step must be integral, got %s", stepType)
				itemType = invalidType
			}
			if value, ok := a.evalConstExpr(expr.RangeStep); ok && value.Kind == ConstInt && value.Int == 0 {
				a.errorf(expr.RangeStep.Pos(), "list comprehension range step cannot be zero")
			}
		}
		if expr.RangeOp != lexer.TOKEN_RANGE && expr.RangeOp != lexer.TOKEN_RANGE_LT && expr.RangeOp != lexer.TOKEN_RANGE_GT {
			a.errorf(expr.Pos(), "list comprehension uses unsupported range operator %s", lexer.TokenName(expr.RangeOp))
		}
		if expr.SecondName != "" {
			a.errorf(expr.Pos(), "a range comprehension yields a single integer per step; a two-binder head `for %s, %s in ...` requires a tuple-yielding source (e.g. a dict)", expr.Name, expr.SecondName)
		}
		loopSym := &Symbol{Name: expr.Name, Kind: SymbolLocal, Type: itemType, Node: expr, Mutable: false}
		a.defineLocalInScope(loopScope, loopSym, expr.Pos())
	} else {
		sourceType := a.analyzeExpr(expr.Source)
		info, ok := a.resolveIterLoopSourceInfo(expr.Source, sourceType)
		if !ok {
			if !IsInvalidType(sourceType) {
				a.errorf(expr.Source.Pos(), "list comprehension currently requires an array, dynamic array, view, store.rows(), frozen tree row view, string-like iterable, ChunksExactView, source.enumerate(), children(node), or a projected tree attribute sequence, got %s", sourceType)
			}
			info.ItemType = invalidType
		}
		if a.containsAffineHandleValues(info.ItemType, map[string]bool{}) {
			a.errorf(expr.Pos(), "list comprehension value iteration does not support affine element type %s; use an explicit `for` loop (read-only iteration borrows affine elements automatically)", info.ItemType)
		}
		itemType = info.ItemType
		// A two-binder head `for k, v in src` destructures a tuple-yielding source
		// (dict entries, darray-of-tuples) exactly like the statement-level tuple loop.
		var pattern ast.MoveBindPattern = &ast.MoveBindNamePattern{Position: expr.Pos(), Name: expr.Name}
		if expr.SecondName != "" {
			pattern = &ast.MoveBindTuplePattern{Position: expr.Pos(), Args: []ast.MoveBindArg{
				{Position: expr.Pos(), Name: expr.Name},
				{Position: expr.Pos(), Name: expr.SecondName},
			}}
		}
		if a.returnBorrowSynthesizedBinders == nil {
			a.returnBorrowSynthesizedBinders = map[ast.Node]bool{}
		}
		a.returnBorrowSynthesizedBinders[pattern] = true
		a.bindIterLoopPattern(loopScope, pattern, ast.IterBindValue, info.ItemType, info.ItemFacts, info.HasItemFacts)
		// A binder is a by-value element copy of the source: it carries the element provenance
		// an index read would, so `[x for x in xs]` keeps what xs's elements point into.
		if a.iterBindingSources == nil {
			a.iterBindingSources = map[*Symbol]ast.Expr{}
		}
		state, hasState := a.iterBinderRegionRefState(expr.Source, sourceType, info.ItemType, regionRefState{}, false)
		for _, sym := range loopScope.Symbols {
			a.iterBindingSources[sym] = expr.Source
			if hasState {
				a.recordResolvedRegionRefBinding(sym, state)
			}
		}
	}
	// Everything below runs once per element (the source above runs once): a value declared outside
	// that is live here and consumed by the end is consumed on every iteration.
	comprehensionEntryAffine := a.cloneAffineValueStates()
	comprehensionOuterScope := a.currentScope
	defer func() {
		a.checkLoopRepeatedConsume(comprehensionEntryAffine, a.currentAffineValues, comprehensionOuterScope, expr.Pos())
	}()
	// Comma-head bindings: per-element `name [:T] = e` lets, declared in the loop scope
	// before the filter/value/key are checked. Later bindings (and the body/filter) see
	// earlier ones. Each is recomputed per iteration (the backend emits them in the loop body).
	if len(expr.Bindings) > 0 {
		savedScope := a.currentScope
		a.currentScope = loopScope
		for _, b := range expr.Bindings {
			vd, ok := b.(*ast.VarDeclStmt)
			if !ok {
				continue
			}
			var expectedT Type
			if vd.Type != nil {
				expectedT = a.resolveType(vd.Type)
			}
			valT := a.analyzeValueExpr(vd.Value, expectedT)
			declT := valT
			if expectedT != nil && !IsInvalidType(expectedT) {
				if !AssignableTo(expectedT, valT) {
					a.errorf(vd.Value.Pos(), "comprehension binding %s expects %s, got %s", vd.Name, expectedT, valT)
				}
				declT = expectedT
			}
			a.defineLocalInScope(loopScope, &Symbol{Name: vd.Name, Kind: SymbolLocal, Type: declT, Node: vd, Mutable: false}, vd.Position)
		}
		a.currentScope = savedScope
	}
	if expr.Filter != nil {
		condType := a.analyzeCondExprInScope(expr.Filter, loopScope)
		if !IsBoolType(condType) && !IsInvalidType(condType) {
			a.errorf(expr.Filter.Pos(), "comprehension filter must be bool, got %s", condType)
		}
	}
	if expr.Key != nil {
		// Dict comprehension: result is dict[KeyType, ValueType].
		var expectedKey, expectedVal Type
		if useExpectedDict {
			expectedKey = expectedDict.Key
			expectedVal = expectedDict.Value
		}
		savedScope := a.currentScope
		a.currentScope = loopScope
		keyType := a.analyzeValueExpr(expr.Key, expectedKey)
		valueType := a.analyzeValueExpr(expr.Value, expectedVal)
		a.currentScope = savedScope
		if useExpectedDict {
			if !AssignableTo(expectedDict.Key, keyType) {
				a.errorf(expr.Key.Pos(), "dict comprehension key expects %s, got %s", expectedDict.Key, keyType)
			}
			if !AssignableTo(expectedDict.Value, valueType) {
				a.errorf(expr.Value.Pos(), "dict comprehension value expects %s, got %s", expectedDict.Value, valueType)
			}
			a.consumeAffineValueExpr(expr.Key, expectedDict.Key, "move into dict comprehension key")
			a.consumeAffineValueExpr(expr.Value, expectedDict.Value, "move into dict comprehension value")
			a.exprTypes[expr] = expectedDict
			return expectedDict
		}
		if keyType == nil || IsInvalidType(keyType) || valueType == nil || IsInvalidType(valueType) {
			a.exprTypes[expr] = invalidType
			return invalidType
		}
		a.consumeAffineValueExpr(expr.Key, keyType, "move into dict comprehension key")
		a.consumeAffineValueExpr(expr.Value, valueType, "move into dict comprehension value")
		result, _ := a.stampContainerRegion(&DictType{Key: keyType, Value: valueType, SurfaceName: "dict"}).(*DictType)
		a.exprTypes[expr] = result
		return result
	}
	if expr.Set {
		// Set comprehension: result is set[ValueType].
		var expectedSetElem Type
		if useExpectedSet {
			expectedSetElem = expectedSet.Elem
		}
		savedScope := a.currentScope
		a.currentScope = loopScope
		valueType := a.analyzeValueExpr(expr.Value, expectedSetElem)
		a.currentScope = savedScope
		if useExpectedSet {
			if !AssignableTo(expectedSet.Elem, valueType) {
				a.errorf(expr.Value.Pos(), "set comprehension element expects %s, got %s", expectedSet.Elem, valueType)
			}
			a.consumeAffineValueExpr(expr.Value, expectedSet.Elem, "move into set comprehension element")
			a.exprTypes[expr] = expectedSet
			return expectedSet
		}
		if valueType == nil || IsInvalidType(valueType) {
			a.exprTypes[expr] = invalidType
			return invalidType
		}
		a.consumeAffineValueExpr(expr.Value, valueType, "move into set comprehension element")
		result, _ := a.stampContainerRegion(&SetType{Elem: valueType, SurfaceName: "set"}).(*SetType)
		a.exprTypes[expr] = result
		return result
	}
	var expectedElem Type
	if useExpectedDArray {
		expectedElem = expectedDArray.Elem
	}
	savedScope := a.currentScope
	a.currentScope = loopScope
	valueType := a.analyzeValueExpr(expr.Value, expectedElem)
	a.recordComprehensionElementState(expr)
	a.currentScope = savedScope
	if expr.Parallel {
		return a.lowerParallelListComprehension(expr, valueType, expectedDArray, useExpectedDArray)
	}
	if useExpectedDArray {
		if !AssignableTo(expectedDArray.Elem, valueType) {
			a.errorf(expr.Value.Pos(), "list comprehension element expects %s, got %s", expectedDArray.Elem, valueType)
		}
		a.consumeAffineValueExpr(expr.Value, expectedDArray.Elem, "move into list comprehension element")
		a.exprTypes[expr] = expectedDArray
		return expectedDArray
	}
	if valueType == nil || IsInvalidType(valueType) {
		a.exprTypes[expr] = invalidType
		return invalidType
	}
	a.consumeAffineValueExpr(expr.Value, valueType, "move into list comprehension element")
	result, _ := a.stampContainerRegion(&DArrayType{Elem: valueType, Shape: &WildcardShape{}, SurfaceName: "darray"}).(*DArrayType)
	a.exprTypes[expr] = result
	return result
}

// lowerParallelListComprehension desugars a `[ value for name in src by par ]` map into a single
// call to the `par_map_collect` combinator and stashes it on expr.LoweredParallel (the backend emits
// it in place of the sequential comprehension). The desugar is just:
//
//	par_map_collect(slice(&src), lambda(name) => value)
//
// par_map_collect builds the result darray in its own inferred region and returns it; the caller
// adopts that region (region-return inference), so it lands in the comprehension's enclosing region
// — no caller-side scratch, no presize/header threading. (This replaced an earlier inline
// `{ out=[]; out.resize(...); par_map(slice(&src), slice(&out), ...); out }` block that existed only
// because a function couldn't build-and-return an owned container; now one can.) Gated to a no-filter
// map over a plain darray identifier with no head bindings;
// otherwise a clear diagnostic.
func (a *Analyzer) lowerParallelListComprehension(expr *ast.ListComprehensionExpr, elemType Type, expectedDArray *DArrayType, useExpectedDArray bool) Type {
	result := Type(invalidType)
	if useExpectedDArray {
		result = expectedDArray
	} else if elemType != nil && !IsInvalidType(elemType) {
		result, _ = a.stampContainerRegion(&DArrayType{Elem: elemType, Shape: &WildcardShape{}, SurfaceName: "darray"}).(*DArrayType)
	}
	fail := func(pos lexer.Pos, reason string) Type {
		a.errorf(pos, "`by par` map %s; it must be `[ f(%s) for %s in <darray> by par ]` (no filter or head bindings and a darray source)", reason, expr.Name, expr.Name)
		a.exprTypes[expr] = result
		return result
	}
	if expr.Filter != nil {
		return fail(expr.Pos(), "does not support an `if` filter")
	}
	if expr.SecondName != "" {
		return fail(expr.Pos(), "does not support a two-binder head")
	}
	if len(expr.Bindings) > 0 {
		return fail(expr.Pos(), "does not support head bindings")
	}
	if expr.RangeEnd != nil {
		return fail(expr.Pos(), "requires a darray source, not a range")
	}
	srcIdent, ok := expr.Source.(*ast.Ident)
	if !ok {
		return fail(expr.Source.Pos(), "source must be a plain darray variable")
	}
	if elemType == nil || IsInvalidType(elemType) {
		a.exprTypes[expr] = result
		return result
	}
	// Pass the comprehension result as the expected return type below. The ordinary generic-call
	// path then binds par_map_collect's U directly from darray[U] -> result, and uses that binding
	// to contextually type the synthesized lambda. Do not reconstruct a surface TypeExpr here:
	// that was only needed by the old inline out-buffer lowering and incorrectly rejected valid
	// element types (notably generic instances) even though the current call lowering is fully
	// structural.
	pos := expr.Position
	transform := &ast.LambdaExpr{Position: pos, Keyword: "lambda", UsesShorthandParams: true,
		Params: []ast.ParamDecl{{Position: pos, Name: expr.Name}}, BodyExpr: expr.Value}
	// `slice` wants a reference to the darray. A source that is ALREADY a reference (a
	// `darray[T]&` parameter) is passed as-is; taking its address again produced a
	// reference-to-reference that no `slice` overload accepts.
	// The map only READS its source. An owned darray is borrowed as a Slice through
	// `slice(&src)`; a source that is already a reference is passed as-is (taking its
	// address again gave a reference-to-reference nothing accepts), and an IMMUTABLE
	// reference goes through `read_view` -- the ReadView overload of par_map_collect --
	// because `slice` would hand the workers a writable view the caller never granted.
	viewName := "slice"
	var srcArg ast.Expr = &ast.AddrOfExpr{Position: pos, Operand: srcIdent}
	if ref, isRef := a.analyzeExpr(srcIdent).(*RefType); isRef {
		srcArg = srcIdent
		if !ref.Mutable {
			viewName = "read_view"
		}
	}
	srcSlice := &ast.CallExpr{Position: pos, Func: &ast.Ident{Position: pos, Name: viewName},
		Args: []ast.Expr{srcArg}}
	lowered := &ast.CallExpr{Position: pos, Func: &ast.Ident{Position: pos, Name: "par_map_collect"},
		Args: []ast.Expr{srcSlice, transform}}

	loweredType := a.analyzeValueExpr(lowered, result)
	if IsInvalidType(loweredType) {
		a.exprTypes[expr] = result
		return result
	}
	expr.LoweredParallel = lowered
	a.exprTypes[expr] = result
	return result
}
