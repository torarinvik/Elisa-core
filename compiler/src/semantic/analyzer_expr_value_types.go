package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) analyzeValueTypeExpr(expr ast.Expr) (result Type) {
	switch n := expr.(type) {
	case *ast.CastExpr:
		// Postfix-shorthand `recv.Name()` parses as a cast to type `Name` (this is
		// how `x.u64()` works). But `recv.Name()` is meant to read as `Name(recv)`:
		// when `Name` is a type it is a constructor/cast, and otherwise it is a
		// function — including a UFCS method. When the target does NOT resolve to
		// a type, re-interpret the cast as the call `Name(recv)` so PascalCase method
		// names work the same as the lowercase / multi-arg forms already do.
		if n.Origin == ast.CastExprOriginPostfixShorthand {
			if named, ok := n.Target.(*ast.NamedType); ok && named != nil && named.Name != "" {
				// Quietly probe whether the target resolves to a real type (this
				// covers builtins like sview/cstr/dstr that resolveType handles). If
				// it does not, `recv.Name()` is the call `Name(recv)` instead.
				savedSuppress := a.suppressDiagnostics
				restoreReportProbe1 := a.beginProofReportProbe()
				defer restoreReportProbe1.restore()
				a.suppressDiagnostics = true
				probe := a.resolveType(named)
				a.suppressDiagnostics = savedSuppress
				restoreReportProbe1.restore()
				if IsInvalidType(probe) {
					synth := &ast.CallExpr{
						Position: n.Position,
						Func:     &ast.FieldExpr{Position: n.Position, Object: n.Operand, Field: named.Name},
					}
					a.postfixShorthandCalls[n] = synth
					result = a.analyzeExpr(synth)
					a.exprTypes[n] = result
					return
				}
			}
		}
		dst := a.resolveType(n.Target)
		var src Type
		if _, ok := n.Operand.(*ast.ZeroedLit); ok {
			src = a.analyzeValueExpr(n.Operand, dst)
		} else {
			src = a.analyzeExpr(n.Operand)
		}
		if n.Origin == ast.CastExprOriginPostfixShorthand {
			// `"lit".sview()`: a bare string literal has no hook of its own (`static u8&`), but it is a
			// NUL-terminated C string, so it takes the `cstr` cast hooks — the literal is re-typed as
			// `cstr`, exactly as `c: cstr = "lit"` contextually types it.
			if lit, ok := n.Operand.(*ast.StringLit); ok && lit != nil {
				if _, exact := a.lookupVisibleCastHook(src, dst); !exact {
					if cstrType := a.resolveType(&ast.NamedType{Position: lit.Position, Name: "cstr"}); cstrType != nil && !IsInvalidType(cstrType) {
						if _, ok := a.lookupVisibleCastHook(cstrType, dst); ok {
							a.analyzeContextualStringValueExpr(n.Operand, cstrType)
							src = cstrType
						}
					}
				}
			}
			if hookSym, ok := a.lookupVisibleCastHook(src, dst); ok && !a.isSelfCastHook(hookSym) {
				a.resolvedCastHooks[n] = hookSym
				if fnType, ok := hookSym.Type.(*FuncType); ok {
					a.recordFunctionPermissionRefs(functionPermissionRefs(a.privateFuncType(nil, fnType)))
				}
				result = dst
				return
			}
			if call, ok := a.boundTypeParamCastCall(n, src, dst); ok {
				// A target-named cast on a constrained type param is a protocol
				// dispatch to that protocol's matching __cast__ method. Reuse the
				// synthetic-call lowering used for non-type postfix names so effect
				// inference, local grant checks, and backend monomorphization all
				// follow the ordinary static-interface call path.
				a.postfixShorthandCalls[n] = call
				result = a.analyzeExpr(call)
				return
			}
			if call, handled := a.concreteImplCastCall(n, src, dst); handled {
				if call == nil {
					result = invalidType
					return
				}
				a.postfixShorthandCalls[n] = call
				result = a.analyzeExpr(call)
				return
			}
		}
		if a.rejectForeignEnumReinterpret(n.Pos(), src, dst) {
			result = dst
			return
		}
		// The value-conversion forms (`r.i32()`, `as`, `to`) read a scalar reference's
		// referent. A `T&?` has no referent until it is unwrapped, so reading it here was
		// a load through a possibly-null pointer. `.cast[T]` (an address reinterpret) and
		// `.uintptr()` (the address idiom) never read the referent and stay valid.
		if n.Origin != ast.CastExprOriginExplicitCast && n.Origin != ast.CastExprOriginIndirectCall && nullableScalarRefValueConversion(src, dst) {
			a.errorf(n.Pos(), "value conversion requires proven non-null reference, got %s", src)
			result = dst
			return
		}
		if n.Origin == ast.CastExprOriginExplicitCast && a.isValueConversion(src, dst) {
			// `.cast[T]` is the canonical reinterpret/bitcast; numeric/enum value conversions go
			// through a constructor (`T(x)` / `x.T()`).
			a.errorf(n.Pos(), "`.cast[%s]` is a value conversion, not a reinterpret; use a constructor `%s(x)` or `x.%s()`", dst, dst, dst)
		} else if !a.validCast(src, dst) {
			a.errorf(n.Pos(), "invalid cast from %s to %s", src, dst)
		} else if n.Origin == ast.CastExprOriginExplicitCast && !IsInvalidType(src) && SameType(src, dst) {
			// A reinterpret `.cast[T]` whose operand already HAS type T does nothing — the bits,
			// mutability, region, and storage all match (SameType is exact, so a genuine
			// mutability/region/lifetime-changing cast is not flagged here). Almost always left
			// over after a refactor (e.g. the operand was narrowed or its type changed). Lint it.
			a.warnOncef(n.Pos(), "redundant `.cast[%s]`: the operand already has type %s; remove the cast", dst, dst)
		}
		// A cast is the explicit way to truncate, so runtime narrowing stays silent. But a
		// cast of a compile-time constant that cannot fit the (sub-64-bit) target type
		// silently changes its value — almost always a bug. Warn-on-lossy. (64-bit targets
		// are skipped: a u64 const above int64 is stored as a negative bit pattern.)
		if _, width, ok := BitIntInfo(dst); ok && width < 64 {
			if value, vok := a.evalConstExpr(n.Operand); vok && value.Kind == ConstInt && !IntegerTypeFitsValue(dst, value.Int) {
				a.warnf(n.Pos(), "constant %d does not fit in %s and is truncated by this cast", value.Int, dst)
			}
		}
		// The `x.ref[T]` reference shorthand has been removed in favor of the explicit
		// `&x` (borrow) / `(&x).cast[T]` (reinterpret) forms. Reject every use.
		// Classify so the message points at the right replacement: BORROW (-> `&x`)
		// iff matching pointee (ignoring mutability), the target is not the universal
		// `void&` type-erasure pointer, and `&x`'s mutability suffices — `&x`'s real
		// mutability is exactly `exprCanYieldWritableRef(x)` (the predicate AddrOf
		// uses), so a mutable target on a non-writable place is a mutability-forcing
		// reinterpret, not a borrow. Everything else -> `(&x).cast[T]`.
		if n.RefShorthand {
			replacement := "`(&x).cast[T]` to reinterpret"
			if addr, ok := n.Operand.(*ast.AddrOfExpr); ok {
				voidElem := a.namedTypes["void"]
				if srcRef, sok := src.(*RefType); sok {
					if dstRef, dok := dst.(*RefType); dok && !SameType(dstRef.Elem, voidElem) && SameType(srcRef.Elem, dstRef.Elem) {
						if !dstRef.Mutable || a.exprCanYieldWritableRef(addr.Operand) {
							replacement = "`&x` to borrow"
						}
					}
				}
			}
			a.errorf(n.Pos(), "`x.ref[T]` reference shorthand has been removed; use %s", replacement)
		}
		// `value.cast[T&]` where the value's own type IS T reinterprets the value's bits as a
		// pointer (a wild pointer), not a reference to it — `.cast` does not take an address.
		// Almost always a mistake for `&value`. A genuine int->ptr cast targets a DIFFERENT
		// pointee type (e.g. `addr.cast[u8&]`), so it is not flagged.
		if n.Origin == ast.CastExprOriginExplicitCast {
			if dstRef, ok := dst.(*RefType); ok {
				_, srcIsRef := src.(*RefType)
				// Exclude pointer-width address types (uintptr/usize): a value of that type IS an
				// address, so `addr.cast[uintptr&]` is a legitimate int->ptr, not a missed `&`.
				srcIsAddress := SameType(src, a.namedTypes["uintptr"]) || SameType(src, a.namedTypes["usize"])
				if !srcIsRef && !srcIsAddress && !IsInvalidType(src) && SameType(src, dstRef.Elem) {
					a.warnf(n.Pos(), "casting a value to a reference of its own type reinterprets it as a raw pointer, not a reference to it; use the `&` operator to take a reference (e.g. `&x`)")
				}
			}
		}
		// Const-correctness: `const_place.ref[mutable T&]` parses to a cast of a
		// freshly-taken borrow (AddrOfExpr) to a mutable ref. A const lives in
		// read-only storage, so handing out a mutable pointer into it would
		// crash on write. Reject mutable borrows rooted in a const. (Non-const
		// places — including non-`mutable` stack locals borrowed for init — and
		// reinterpret casts of plain pointer values are unaffected.)
		if dstRef, ok := dst.(*RefType); ok && dstRef.Mutable {
			operand := n.Operand
			for {
				if paren, ok := operand.(*ast.ParenExpr); ok {
					operand = paren.Inner
					continue
				}
				break
			}
			if addr, ok := operand.(*ast.AddrOfExpr); ok && a.borrowPlaceRootsInConst(addr.Operand) {
				a.errorf(n.Pos(), "cannot take a mutable reference to a const (it lives in read-only storage); use a read-only reference instead")
			}
		}
		if a.enforceUnsafePermissions && n.Origin != ast.CastExprOriginIndirectCall && a.castRequiresUnsafeGuestHostPointerCast(n, dst) {
			a.recordFunctionPermissionRefs(unsafeGuestHostPointerCastRefs(n.Position))
		} else if a.enforceUnsafePermissions && n.Origin != ast.CastExprOriginIndirectCall && castRequiresUnsafePointerCast(src, dst) {
			a.recordFunctionPermissionRefs(unsafePointerCastRefs(n.Position))
		}
		if srcRef, ok := src.(*RefType); ok {
			if dstRef, ok := dst.(*RefType); ok && srcRef.Mutable && !dstRef.Mutable {
				cloned := cloneRefType(dstRef)
				cloned.Mutable = true
				dst = cloned
			}
		}
		a.checkBufferReinterpretCast(n, dst)
		if a.enforceUnsafePermissions && a.unsafeBufferReinterpretCasts[n] {
			a.recordFunctionPermissionRefs(unsafeBufferReinterpretRefs(n.Position))
		}
		a.recordUnsafeLifetimeWiden(n, src, dst)
		// S4 Stage 3: a reinterpret cast to a region-LESS reference inherits the operand's pointee
		// region — a reborrow `(&x).cast[T&]` or a darray-element ref (`xs[i].cast[T&]`) aliases the
		// same allocation, so the result lives in the same region. Sound: a reinterpret cannot change
		// where the pointee lives, so it cannot forge a longer-lived region (it only recovers the
		// region the cast would otherwise erase); an explicit `@r` on the target is respected (only a
		// region-less target inherits). This lets region-poly threading survive the loader's pervasive
		// `(&self).cast[mutable Module&]` reborrow idiom instead of forcing an explicit `in perm:`.
		if dstRef, ok := dst.(*RefType); ok && dstRef.Region == "" {
			if region := a.returnBorrowedRegion(n.Operand); region != "" {
				cloned := cloneRefType(dstRef)
				cloned.Region = region
				dst = cloned
			}
		}
		result = dst
		return
	case *ast.SizeofExpr:
		a.resolveType(n.Type)
		result = a.namedTypes["usize"]
		return
	case *ast.AlignofExpr:
		a.resolveType(n.Type)
		result = a.namedTypes["usize"]
		return
	case *ast.OffsetofExpr:
		t := a.resolveType(n.Type)
		a.lookupField(t, n.Field, n.Pos())
		result = a.namedTypes["usize"]
		return
	case *ast.QuantifierExpr:
		// A spec-only bound quantifier (docs/90 brick 90-4). Binders range over the integers; the body
		// must be bool. Type-checked in a child scope so the binders are in scope for the body and gone
		// after. Never compiled to a runtime check (suppressed at discharge) — provable only by SMT.
		var sourceType Type
		if n.In != nil {
			sourceType = a.analyzeExpr(n.In)
		}
		saved := a.currentScope
		scope := NewScope(saved)
		intType := a.namedTypes["i64"]
		binderTypes := make([]Type, len(n.Vars))
		for i := range binderTypes {
			binderTypes[i] = intType
		}
		if n.In != nil {
			switch ct := stripRefForBounds(sourceType).(type) {
			case *ArrayType:
				if len(n.Vars) == 1 {
					binderTypes[0] = ct.Elem
				}
			case *DArrayType:
				if len(n.Vars) == 1 {
					binderTypes[0] = ct.Elem
				}
			case *SetType:
				if len(n.Vars) == 1 {
					binderTypes[0] = ct.Elem
				}
			case *DictType:
				if len(n.Vars) == 2 {
					binderTypes[0] = ct.Key
					binderTypes[1] = ct.Value
				}
			}
		}
		for i, v := range n.Vars {
			scope.Define(&Symbol{Name: v, Kind: SymbolLocal, Type: binderTypes[i]})
		}
		a.currentScope = scope
		bodyType := a.analyzeExpr(n.Body)
		a.currentScope = saved
		if bodyType != nil && !IsBoolType(bodyType) {
			a.errorf(n.Body.Pos(), "quantifier body must be bool, got %s", bodyType)
		}
		boolType := a.namedTypes["bool"]
		a.exprTypes[n] = boolType
		return boolType
	case *ast.TernaryExpr:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) && !IsInvalidType(condType) {
			a.errorf(n.Pos(), "ternary condition must be bool, got %s", condType)
		}
		mergedAffine := a.cloneAffineValueStates()
		mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
		left, leftSnapshot := a.analyzeExprInConditionAffineScope(n.Value, a.currentScope, n.Cond, true)
		right, rightSnapshot := a.analyzeExprInConditionAffineScope(n.Alt, a.currentScope, n.Cond, false)
		mergedAffine = mergeAffineValueStates(mergedAffine, leftSnapshot.Affine)
		mergedAffine = mergeAffineValueStates(mergedAffine, rightSnapshot.Affine)
		mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, leftSnapshot.BorrowedOwnerRefs)
		mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, rightSnapshot.BorrowedOwnerRefs)
		a.currentAffineValues = mergedAffine
		a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
		if mergedFunctionValues, ok := a.intersectFunctionValueFlows(leftSnapshot.FunctionValues, rightSnapshot.FunctionValues); ok {
			a.currentFunctionValues = mergedFunctionValues
		}
		merged := MergeTypes(left, right)
		if IsInvalidType(merged) {
			// Literal-aware fallback, mirroring mergeMatchExprArmTypes: a bare
			// string-LITERAL branch adapts to a string-view branch even without a
			// contextual expected type (e.g. a ternary nested inside a match arm).
			// Non-literal `static u8&` values stay incompatible.
			merged = mergeTernaryBranchTypes(left, right, n.Value, n.Alt)
		}
		if IsInvalidType(merged) {
			a.errorf(n.Pos(), "ternary branches are incompatible: %s and %s", left, right)
		}
		result = merged
		return
	default:
		return invalidType
	}
}
