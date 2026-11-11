//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"unsafe"
)

func (s *functionState) emitExpr(expr ast.Expr, expected semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("cannot emit nil expression")
	}

	actualType := s.exprType(expr)
	if expected != nil && isZeroedExpr(expr) {
		value, err := s.zeroValue(expected)
		return value, expected, err
	}
	// Materializing a reference (`T&`) from a value-typed lvalue -- e.g. `return s[h]`
	// in a `-> T&` function, or binding a value field/element to a `T&`. Take the
	// element's address rather than letting coerceValue reinterpret the loaded value
	// as a pointer (inttoptr), which would yield a reference to the *contents* of the
	// element instead of the element itself. Only applies when the expression is a
	// genuine addressable lvalue whose value type matches the reference's element type;
	// expressions already producing a reference (calls returning `T&`, ref-typed
	// idents) keep their existing pointer-to-pointer path.
	if refExpected, ok := expected.(*semantic.RefType); ok && actualType != nil {
		if _, actualIsRef := actualType.(*semantic.RefType); !actualIsRef &&
			semantic.SameType(actualType, refExpected.Elem) && backendIsAddressableLValue(expr) {
			if addr, _, aerr := s.emitAddress(expr); aerr == nil && addr != nil {
				return addr, expected, nil
			}
		}
	}

	if value, valueType, handled, err := s.emitExprWithMergedIndexChecks(expr, expected); handled {
		return value, valueType, err
	}

	var (
		value llvmIRValueRef
		err   error
	)

	switch n := expr.(type) {
	case *ast.EnumColumnExpr:
		// A column scan `Expr of .field` is only ever consumed as an iterable-for
		// source; its count/elements are produced directly from the packed store
		// (see emitIterLoopCount / emitIterLoopElementValue). This placeholder is
		// never read — it just satisfies the source-materialization step.
		if actualType == nil {
			return nil, nil, fmt.Errorf("missing type for column scan %s of .%s", n.Enum, n.Field)
		}
		llvmType, lerr := s.g.lowerType(actualType)
		if lerr != nil {
			return nil, nil, lerr
		}
		return llvmGetUndef(llvmType), actualType, nil
	case *ast.Ident:
		if s.isUnboundImplicitPackedStoreIdent(n) {
			storeType := actualType
			if _, typed := semantic.StripAggregateStateType(storeType).(*semantic.PackedEnumStoreType); !typed {
				// Backend-recovered implicit arguments have no expression-type entry.
				// The callee parameter retains the exact enum identity; the synthetic
				// spelling is sanitized and cannot safely recover qualified names.
				if _, expectedStore := semantic.StripAggregateStateType(expected).(*semantic.PackedEnumStoreType); expectedStore {
					storeType = expected
				}
			}
			value, actualType, err = s.emitImplicitPackedStoreIdent(n, storeType)
		} else {
			value, actualType, err = s.emitIdent(n)
		}
	case *ast.IntLit:
		value, actualType, err = s.emitIntLiteral(n)
	case *ast.FloatLit:
		value, actualType, err = s.emitFloatLiteral(n)
	case *ast.ShorthandMemberExpr:
		if constEnumType, member, ok := s.shorthandConstEnumMemberInfo(n); ok {
			value, actualType, err = s.emitConstEnumMemberExpr(constEnumType, member)
		} else {
			err = fmt.Errorf("unsupported shorthand member %q during LLVM lowering", shorthandMemberDisplayBackend(n))
		}
	case *ast.StringLit:
		value, actualType, err = s.emitStringLiteral(n, expected)
	case *ast.CharLit:
		value, actualType, err = s.emitCharLiteral(n)
	case *ast.BoolLit:
		value, actualType, err = s.emitBoolLiteral(n)
	case *ast.NullLit:
		value, actualType, err = s.emitNullLiteral()
	case *ast.ZeroedLit:
		return nil, nil, fmt.Errorf("zeroed requires an expected destination type")
	case *ast.ExprBlock:
		value, actualType, err = s.emitExprBlock(n, expected)
	case *ast.ListLitExpr:
		value, actualType, err = s.emitListLitExpr(n, expected)
	case *ast.ListComprehensionExpr:
		value, actualType, err = s.emitListComprehensionExpr(n)
	case *ast.QueryExpr:
		value, actualType, err = s.emitQueryExpr(n)
	case *ast.BinaryExpr:
		value, actualType, err = s.emitBinaryExpr(n)
	case *ast.UnaryExpr:
		value, actualType, err = s.emitUnaryExpr(n)
	case *ast.CallExpr:
		if s.oldCaptures != nil && ast.IsOldCall(n) {
			// `old(expr)` in an ensure clause -> the value captured at function entry.
			if cap, ok := s.oldCaptures[n]; ok {
				return cap.val, cap.typ, nil
			}
			return nil, nil, fmt.Errorf("old(...) outside a captured ensure context")
		}
		if n.Safe {
			value, actualType, err = s.emitSafeCallExpr(n)
		} else {
			value, actualType, err = s.emitCallExpr(n)
		}
	case *ast.FieldExpr:
		if n.Safe {
			value, actualType, err = s.emitSafeFieldExpr(n)
		} else if errorType, _, ok := s.errorTagInfo(n); ok {
			value, actualType, err = s.emitErrorTagExpr(n, errorType)
		} else if constEnumType, member, ok := s.constEnumMemberInfo(n); ok {
			value, actualType, err = s.emitConstEnumMemberExpr(constEnumType, member)
		} else if enumType, variant, ok := s.enumConstructorInfoFromField(n); ok && variant != nil && len(variant.Payload) == 0 {
			if enumType != nil && enumType.Packed {
				store, ok := s.lookupPackedStore(enumType)
				if !ok {
					err = fmt.Errorf("packed enum constructor %s.%s requires an active in %s.Store: scope or explicit new[%s.Store]", ast.ModulePathSpelling(enumType.Name), variant.Name, ast.ModulePathSpelling(enumType.Name), ast.ModulePathSpelling(enumType.Name))
				} else {
					value, actualType, err = s.emitPackedEnumConstructorAlloc(nil, store.value, enumType, variant, nil, nil)
				}
			} else {
				value, actualType, err = s.emitEnumConstructorValue(nil, enumType, variant, nil, nil)
			}
		} else {
			value, actualType, err = s.emitFieldExpr(n)
		}
	case *ast.RaiseExpr:
		value, actualType, err = s.emitRaiseExpr(n)
	case *ast.TryExpr:
		value, actualType, err = s.emitTryExpr(n)
	case *ast.CatchExpr:
		value, actualType, err = s.emitCatchExpr(n)
	case *ast.UnwrapElseExpr:
		value, actualType, err = s.emitUnwrapElseExpr(n)
	case *ast.GetExpr:
		value, actualType, err = s.emitGetExpr(n)
	case *ast.OptionalBindExpr:
		value, actualType, err = s.emitOptionalBindExpr(n)
	case *ast.AllocExpr:
		value, actualType, err = s.emitAllocExpr(n)
	case *ast.MatchExpr:
		value, actualType, err = s.emitMatchExpr(n)
	case *ast.MachineFromExpr:
		// docs/125 §5: the analyzer built the loop/mode/match desugar into Lowered.
		value, actualType, err = s.emitExpr(n.Lowered, expected)
	case *ast.FoldExpr:
		value, actualType, err = s.emitFoldExpr(n)
	case *ast.EmitExpr:
		value, actualType, err = s.emitSequenceRewriteEmitExpr(n)
	case *ast.IndexExpr:
		value, actualType, err = s.emitIndexExpr(n)
	case *ast.SliceExpr:
		value, actualType, err = s.emitSliceExpr(n)
	case *ast.CastExpr:
		value, actualType, err = s.emitCastExpr(n)
	case *ast.SizeofExpr:
		value, actualType, err = s.emitSizeofExpr(n)
	case *ast.AlignofExpr:
		value, actualType, err = s.emitAlignofExpr(n)
	case *ast.OffsetofExpr:
		value, actualType, err = s.emitOffsetofExpr(n)
	case *ast.TernaryExpr:
		value, actualType, err = s.emitTernaryExpr(n)
	case *ast.AddrOfExpr:
		value, actualType, err = s.emitAddrOfExpr(n)
	case *ast.MoveExpr:
		value, actualType, err = s.emitMoveExpr(n, expected)
	case *ast.SpecializeExpr:
		value, actualType, err = s.emitSpecializeExpr(n)
	case *ast.StructLitExpr:
		value, actualType, err = s.emitStructLitExpr(n)
	case *ast.RecordUpdateExpr:
		value, actualType, err = s.emitRecordUpdateExpr(n)
	case *ast.LambdaExpr:
		value, actualType, err = s.emitLambdaExpr(n)
	case *ast.TupleExpr:
		value, actualType, err = s.emitTupleExpr(n, expected)
	case *ast.ParenExpr:
		value, actualType, err = s.emitExpr(n.Inner, expected)
	case *ast.CanExpr:
		value, actualType, err = s.emitExpr(n.Expr, expected)
	case *ast.QuantifierExpr:
		value, err = s.emitQuantifierExpr(n)
	default:
		return nil, nil, fmt.Errorf("unsupported expression %T", expr)
	}
	if err != nil {
		return nil, nil, err
	}
	if expected != nil {
		coerced, err := s.coerceValue(value, actualType, expected)
		if err != nil {
			return nil, nil, err
		}
		return coerced, expected, nil
	}
	return value, actualType, nil
}
func (s *functionState) emitExprBlock(expr *ast.ExprBlock, expected semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("invalid expression block")
	}
	if expr.Value == nil {
		return s.emitDivergingExprBlock(expr, expected)
	}
	savedPackedStores := s.packedStores
	s.packedStores = s.clonePackedStores()
	s.pushScope()
	scope := s.scope
	defer func() {
		s.popScope()
		s.packedStores = savedPackedStores
	}()
	for _, stmt := range expr.Stmts {
		if s.currentBlockTerminated() {
			s.discardScopeCleanups(scope)
			return nil, nil, fmt.Errorf("expression block setup statements terminated control flow")
		}
		if err := s.emitStmt(stmt); err != nil {
			s.discardScopeCleanups(scope)
			return nil, nil, err
		}
	}
	if s.currentBlockTerminated() {
		s.discardScopeCleanups(scope)
		return nil, nil, fmt.Errorf("expression block setup statements terminated control flow")
	}
	value, actualType, err := s.emitExpr(expr.Value, expected)
	if err != nil {
		s.discardScopeCleanups(scope)
		return nil, nil, err
	}
	if s.currentBlockTerminated() {
		s.discardScopeCleanups(scope)
		return value, actualType, nil
	}
	if err := s.emitScopeCleanups(scope); err != nil {
		return nil, nil, err
	}
	return value, actualType, nil
}

// emitDivergingExprBlock emits a value-less branch block whose last statement leaves
// (docs/119 §4.1: `else: return -1`). There is no value: the block ends terminated and
// the enclosing ternary leaves this arm out of its phi.
func (s *functionState) emitDivergingExprBlock(expr *ast.ExprBlock, expected semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	savedPackedStores := s.packedStores
	s.packedStores = s.clonePackedStores()
	s.pushScope()
	scope := s.scope
	defer func() {
		s.popScope()
		s.packedStores = savedPackedStores
	}()
	for _, stmt := range expr.Stmts {
		if s.currentBlockTerminated() {
			s.discardScopeCleanups(scope)
			return nil, nil, fmt.Errorf("expression block setup statements terminated control flow")
		}
		if err := s.emitStmt(stmt); err != nil {
			s.discardScopeCleanups(scope)
			return nil, nil, err
		}
	}
	if !s.currentBlockTerminated() {
		s.discardScopeCleanups(scope)
		return nil, nil, fmt.Errorf("diverging expression block did not terminate control flow")
	}
	s.discardScopeCleanups(scope)
	return nil, expected, nil
}
func (s *functionState) emitMoveExpr(expr *ast.MoveExpr, expected semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	return s.emitMovedValue(expr.Operand, expected)
}
func (s *functionState) emitMovedValue(operand ast.Expr, expected semantic.Type) (llvmIRValueRef, semantic.Type, error) {
	if binding, ok := s.lookupScopedMoveBinding(operand); ok {
		value, err := s.loadValue(binding.ptr, binding.typ, binding.name)
		if err != nil {
			return nil, nil, err
		}
		zero, err := s.zeroValue(binding.typ)
		if err != nil {
			return nil, nil, err
		}
		if err := s.storeValue(binding.ptr, zero, binding.typ, binding.name+".move.zero"); err != nil {
			return nil, nil, err
		}
		// docs/126 §2: moving a drop-typed value transfers its destructor obligation to
		// the destination, so this scope must no longer run it.
		if err := s.clearDropLiveFlag(binding); err != nil {
			return nil, nil, err
		}
		return value, binding.typ, nil
	}
	return s.emitExpr(operand, expected)
}
func (s *functionState) lookupScopedMoveBinding(expr ast.Expr) (scopedCleanupBinding, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return s.lookupScopedMoveBinding(n.Inner)
	case *ast.Ident:
		binding, ok := s.lookupBinding(n.Name)
		if !ok {
			return scopedCleanupBinding{}, false
		}
		for i := len(s.scopedCleanups) - 1; i >= 0; i-- {
			if s.scopedCleanups[i].ptr == binding.ptr {
				return s.scopedCleanups[i], true
			}
		}
	}
	return scopedCleanupBinding{}, false
}
func (s *functionState) buildCall(llvmFnType llvmIRTypeRef, callee llvmIRValueRef, args []llvmIRValueRef, name string) llvmIRValueRef {
	argCount := len(args)
	argPtr := llvmValueSlicePtr(args)
	nameC := cString(name)
	defer llvmFree(unsafe.Pointer(nameC))
	return llvmBuildCall2(s.builder, llvmFnType, callee, argPtr, llvmIRUnsigned(argCount), nameC)
}

func (s *functionState) buildTypedCall(llvmFnType llvmIRTypeRef, callee llvmIRValueRef, args []llvmIRValueRef, name string, fn *semantic.FuncType) llvmIRValueRef {
	call := s.buildCall(llvmFnType, callee, args, name)
	if s != nil && s.g != nil {
		if callConv, ok := s.g.llvmCallConvForFunc(fn); ok {
			llvmSetInstructionCallConv(call, callConv)
		}
	}
	return call
}
func (s *functionState) emitIdent(expr *ast.Ident) (llvmIRValueRef, semantic.Type, error) {
	if actualType := s.exprType(expr); semantic.IsNullType(actualType) {
		return s.emitNullLiteral()
	} else if viewType, ok := actualType.(*semantic.PackedVariantViewType); ok {
		if binding, ok := s.lookupPackedVariantView(expr.Name); ok {
			if viewType.Enum != nil && !viewType.Enum.Packed && binding.ptr != nil {
				value, err := s.loadValue(binding.ptr, viewType.Enum, expr.Name)
				return value, viewType.Enum, err
			}
			return s.materializePackedVariantViewValue(binding)
		}
		if ptr, valueType, err := s.emitIdentValueAddress(expr); err == nil {
			if enumType, ok := valueType.(*semantic.EnumType); ok && enumType == viewType.Enum && enumType.Packed {
				handle, err := s.loadValue(ptr, valueType, expr.Name)
				if err != nil {
					return nil, nil, err
				}
				var store *packedStoreBinding
				if enumType.StoreType != nil {
					resolvedStore, ok := s.lookupPackedStore(enumType)
					if ok {
						store = &resolvedStore
					}
				}
				value, err := s.buildPackedVariantViewValue(viewType, handle, store)
				if err != nil {
					return nil, nil, err
				}
				if store != nil {
					s.bindPackedVariantView(expr.Name, viewType, nil, handle, *store, packedPayloadValueCache{})
				} else {
					s.bindPackedVariantView(expr.Name, viewType, nil, handle, packedStoreBinding{}, packedPayloadValueCache{})
				}
				// The memoized handle load is only reusable from this block — a sibling
				// branch is not dominated by it. lookupPackedVariantView treats the binding
				// as absent elsewhere, so other blocks re-load from the alloca.
				s.markPackedVariantViewHandleBlock(expr.Name)
				return value, viewType, nil
			}
		}
	}
	if ptr, valueType, err := s.emitIdentValueAddress(expr); err == nil {
		value, loadErr := s.loadValue(ptr, valueType, expr.Name)
		return value, valueType, loadErr
	}
	if binding, ok := s.lookupBinding(expr.Name); ok {
		value, err := s.loadValue(binding.ptr, binding.typ, expr.Name)
		return value, binding.typ, err
	}
	if arena := s.regionArenaPointer(expr.Name); arena != nil {
		// A reference to an explicit `[@owner]` parameter by name yields the threaded
		// hidden Arena& parameter as a value — so `owner` can be passed where an arena value is
		// expected (a runtime container constructor like `indexmap.new(owner)`), the no-Arena-field
		// replacement for an `owner: mutable Arena&` state field. The arena param is already a
		// `mutable Arena&` pointer value, so it is returned directly with no load.
		arenaType := s.g.namedType("Arena")
		refType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, Mutable: true}
		return arena, refType, nil
	}
	if sym, resolvedName, ok := s.lookupVisibleGlobalSymbol(expr.Name); ok {
		switch sym.Kind {
		case semantic.SymbolFunc, semantic.SymbolExternFunc:
			fnType, ok := sym.Type.(*semantic.FuncType)
			if !ok {
				return nil, nil, fmt.Errorf("global function %s is missing function type", expr.Name)
			}
			value, err := s.g.ensureFunctionDeclared(resolvedName, fnType)
			return value, fnType, err
		case semantic.SymbolGlobal, semantic.SymbolExternVar:
			global, err := s.g.ensureGlobalDeclared(resolvedName, sym.Type, sym.Kind == semantic.SymbolExternVar)
			if err != nil {
				return nil, nil, err
			}
			value, err := s.loadValue(global, sym.Type, expr.Name)
			return value, sym.Type, err
		case semantic.SymbolConst:
			// lookupVisibleGlobalSymbol may resolve an unqualified imported name
			// through a unique module suffix. Use that canonical name here too;
			// looking the source spelling up again loses the `using` resolution
			// and lets a semantically valid constant fail during LLVM lowering.
			if value, _, ok := s.visibleConstValue(resolvedName); ok {
				llvmValue, llvmType, err := s.emitConstValueWithType(value, sym.Type)
				return llvmValue, llvmType, err
			}
		}
	}
	if value, _, ok := s.visibleConstValue(expr.Name); ok {
		llvmValue, llvmType, err := s.emitConstValue(value)
		return llvmValue, llvmType, err
	}
	if s.typeMap != nil {
		if bound, ok := s.typeMap[expr.Name]; ok {
			if value, valueOK := bound.(*semantic.ConstValueType); valueOK {
				llvmValue, llvmType, err := s.emitConstValue(value.Value)
				return llvmValue, llvmType, err
			}
		}
	}
	fnName := "<unknown>"
	if s.decl != nil {
		fnName = s.decl.Name
	}
	position := expr.Pos()
	if !position.IsZero() {
		return nil, nil, fmt.Errorf("unknown identifier %q during LLVM lowering in %s at %s:%d:%d", expr.Name, fnName, position.File, position.Line, position.Col)
	}
	return nil, nil, fmt.Errorf("unknown identifier %q during LLVM lowering in %s", expr.Name, fnName)
}
func (s *functionState) errorTagInfo(expr *ast.FieldExpr) (*semantic.ErrorSetType, string, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, "", false
	}
	base, ok := s.g.lookupNamedType(s.errorSetQualifierName(ident))
	if !ok {
		return nil, "", false
	}
	errorType, ok := base.(*semantic.ErrorSetType)
	if !ok || !errorType.HasQualifiedTag(errorType.Name, expr.Field) {
		return nil, "", false
	}
	return errorType, semantic.QualifyErrorTag(errorType.Name, expr.Field), true
}

// errorSetQualifierName is the canonical name of the error set an author spelled
// unqualified. NamedTypes is keyed by the QUALIFIED name, so `EntityIdError` written
// inside `module EntityId` finds nothing; the analyzer records what it resolved to.
func (s *functionState) errorSetQualifierName(ident *ast.Ident) string {
	if ident == nil {
		return ""
	}
	if s != nil && s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnResolvedValueNamesMap() {
		if canonical, ok := s.g.semanticReadView().LookupResolvedValueNames(ident); ok && canonical != "" {
			return canonical
		}
	}
	return ident.Name
}
func (s *functionState) constEnumMemberInfo(expr *ast.FieldExpr) (*semantic.ConstEnumType, *semantic.ConstEnumMember, bool) {
	constEnumType, _, member, ok := s.constEnumMemberInfoForExpr(expr)
	if !ok {
		return nil, nil, false
	}
	return constEnumType, member, member != nil
}
