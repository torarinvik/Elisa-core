package semantic

import "elisacore/src/ast"

// Keep declaration effects together: incremental initializer typing must still
// create current symbols and run conversion, binding, borrow and drop checks.
func (a *Analyzer) analyzeVariableDeclaration(n *ast.VarDeclStmt) {
	// Expand a named refinement alias used in this local's binder (`x: Positive = ...`) into the
	// equivalent anonymous `Base where Pred` in place, before its type is resolved, so the existing
	// local where discharge handles it with no parallel path.
	a.rewriteLocalRefineAlias(n)
	if n.Ghost {
		// A ghost decl is verification-only: record it for codegen erasure, and analyze its
		// initializer in a ghost-read-allowed context (a ghost may be defined in terms of another
		// ghost). The bound symbol is flagged Ghost so any later READ outside a contract is rejected.
		if a.ghostDecls == nil {
			a.ghostDecls = map[*ast.VarDeclStmt]bool{}
		}
		a.ghostDecls[n] = true
		a.ghostReadAllowed++
		defer func() { a.ghostReadAllowed-- }()
		// SOUNDNESS of erasure: a ghost initializer is dropped from codegen, so it must have no
		// observable runtime effect. Restrict it to a side-effect-free expression grammar (reads,
		// arithmetic, field/index access, `old(...)`). A general call could mutate real state, which
		// erasing would silently change — reject it.
		if n.Value != nil && !ghostInitIsErasureSafe(n.Value) {
			a.errorf(n.Value.Pos(), "ghost variable %q initializer must be side-effect-free (it is erased from codegen): use reads, arithmetic, field/index access, or `old(...)`, not a general call or mutation", n.Name)
		}
	}
	var declType Type
	var valueType Type
	if n.Type != nil {
		declType = a.resolveType(n.Type)
		// A `mutable`-declared view local is a WRITABLE view (`mutable view[T]`). In a var-decl the
		// leading `mutable` is parsed as the binding's mutability (n.Mutable), not folded into the
		// type the way a param's `mutable view[T]` is — so bridge it here: `mutable` means "I can
		// mutate this", which for a view is write-through (exactly as it means push for a `mutable
		// darray`). A read-only view keeps `v: view[T]`. AssignableTo still forbids backing a
		// mutable view with a read-only (immutable-source) slice.
		if n.Mutable && !n.BindingExplicit {
			if view, ok := declType.(*ViewType); ok && !view.Mutable {
				cloned := *view
				cloned.Mutable = true
				declType = &cloned
			}
		}
	}
	if n.Value != nil {
		if n.Type == nil && a.ambiguousDArrayBuilders != nil && a.ambiguousDArrayBuilders[n] {
			a.errorf(n.Pos(), "cannot infer element type for empty darray builder %q; add a type declaration (for example `%s: T[] = []` or `%s: mutable darray[T] = []`) or push/extend typed values before type-dependent darray operations", n.Name, n.Name, n.Name)
			declType = invalidType
			valueType = invalidType
			a.exprTypes[n.Value] = invalidType
		} else {
			previousZeroedPlaceholder := a.zeroedPlaceholderExpected
			if isZeroedInitializer(n.Value) {
				a.zeroedPlaceholderExpected = declType
			}
			if lambda, isLambda := stripOptimizationParens(n.Value).(*ast.LambdaExpr); isLambda {
				a.pendingClosureBinding = lambda
			}
			valueType = a.analyzeLocalInitializer(n, declType)
			a.pendingClosureBinding = nil
			a.zeroedPlaceholderExpected = previousZeroedPlaceholder
		}
		if a.checkVoidBinding(n.Pos(), n.Name, declType, valueType, true) {
			declType = invalidType
			valueType = invalidType
		} else if declType == nil {
			declType = valueType
		} else if !AssignableTo(declType, valueType) {
			if a.isDictIndexPayloadProjection(n.Value, declType) {
				// Parity with stage1 (check_value_against): `d[k]` is a fallible
				// `V&?` lookup, never the payload itself.
				a.errorf(n.Pos(), "variable %q expects %s, got optional reference to dictionary value", n.Name, declType)
			} else {
				a.errorf(n.Pos(), "variable %q expects %s, got %s", n.Name, declType, valueType)
				a.reportShapeMismatchNotes(n.Pos(), declType, valueType)
			}
		}
		// Nested-region escape: binding an inner-@r value into a variable whose
		// declared type names an outer region dangles once the inner region is
		// freed (the var-decl analogue of the assignment/push check). Decided by
		// the region outlives-lattice.
		// The binding being initialized is not yet available through name lookup.
		// Use its actual declaration scope so an outer declaration with the same
		// name cannot make this fresh local look like a longer-lived store target.
		a.checkNestedRegionStoreEscapeAtScope(&ast.Ident{Position: n.Position, Name: n.Name}, n.Value, declType, valueType, a.currentScope)
		a.checkFreshContainerStoreEscape(&ast.Ident{Position: n.Position, Name: n.Name}, declType, n.Value)
	} else if declType == nil {
		a.errorf(n.Pos(), "variable %q requires a type or initializer", n.Name)
		declType = invalidType
	} else if a.checkVoidBinding(n.Pos(), n.Name, declType, nil, false) {
		declType = invalidType
	}
	bindingType := declType
	// `mutable T&` is parsed with mutability on the binding declaration while
	// the resolved reference type may still be read-only.  Promote the stored
	// binding type so a later `slot <- value` is analyzed as a write through
	// the reference rather than as an attempted replacement of the reference
	// itself -- but only when the initializer is itself writable. The legacy
	// spelling also means "rebindable" (`name: mutable static u8& = "zz"` is a
	// re-pointable string), and promoting a read-only source made its referent
	// writable: a string literal's static bytes (SIGBUS on the store), an
	// immutable local, a `T&` parameter. Such a binding stays rebindable and
	// read-only. (A desugared binding with no written type keeps the promotion,
	// and so does an initializer a `mutable T&` parameter would accept: legacyRefInitIsWritable.)
	if n.Mutable && !n.BindingExplicit {
		if ref, ok := bindingType.(*RefType); ok && ref != nil && !ref.Mutable {
			cloned := cloneRefType(ref)
			cloned.Mutable = true
			if n.Type == nil || n.Value == nil || AssignableTo(cloned, valueType) || a.legacyRefInitIsWritable(n.Value) {
				bindingType = cloned
			}
		}
	}
	// A DECLARED READ-ONLY REFERENCE STAYS READ-ONLY.
	//
	// There used to be a block here that copied the SOURCE's mutability onto
	// the binding whenever the initializer happened to be a `mutable T&` --
	// discarding the one thing the programmer wrote down. It arrived with
	// "Make many APIs use mutable refs" as a migration convenience and was
	// dormant while `new[r] x` produced a non-mutable ref. The moment
	// allocations became mutable by construction (correctly: fresh storage is
	// writable, and the binding is what narrows it) every
	// `value: T& @r = new[r] ...` silently became writable and
	// `cannot mutate through readonly ref` stopped firing on it.
	//
	// This path only ever runs when a type WAS written: with no declared type
	// bindingType is already valueType, so there is nothing to widen. The
	// `n.Mutable` promotion above remains -- there the programmer asked.
	if specializedViewType, ok := concreteBorrowedViewBindingType(bindingType, valueType); ok {
		bindingType = specializedViewType
	}
	if specializedContainerType, ok := concreteContainerRegionBindingType(bindingType, valueType); ok {
		bindingType = specializedContainerType
	}
	if !n.Mutable {
		if specializedType, ok := a.specializeCallbackCarryingType(bindingType, valueType); ok {
			bindingType = specializedType
		}
	}
	// Region-parameterized containers: a container local declared inside an
	// `in <region>:` scope carries that region in its type; escape checks and
	// codegen arena routing consult it (see REGION_CONTAINERS_DESIGN.md).
	bindingType = a.stampContainerRegion(bindingType)
	// A region-polymorphic call's result is allocated in the caller's threaded
	// arena even though its declared return type is regionless. Preserve that
	// owner on a local container binding so a later store through the local does
	// not lose the hidden `__region_auto` dependency.
	if n.Value != nil && containerOrEntryRegion(bindingType) == "" && a.exprIsRegionPolyResultCarryingRegionData(n.Value, valueType) {
		region := ""
		if call, ok := unwrapParenForRegionPoly(n.Value).(*ast.CallExpr); ok && call != nil && a.destinationAllocated != nil {
			region = a.destinationAllocated[call]
		}
		if region == "" {
			if regionArg, found := a.regionPolymorphicCallerRegionArg(); found {
				if ident, ok := stripParenExpr(regionArg).(*ast.Ident); ok && ident != nil {
					region = ident.Name
				}
			}
		}
		if stamped, applied := stampContainerRegion(bindingType, region); applied {
			bindingType = stamped
		}
	}
	if a.hasMutableBorrowCapability(bindingType) && !a.suppressDiagnostics {
		if refs := a.mutableGlobalBorrowTransferRefs(n.Value, bindingType); len(refs) != 0 {
			if a.mutableGlobalBorrowRefs == nil {
				a.mutableGlobalBorrowRefs = make(map[*ast.VarDeclStmt][]ast.PermissionRef)
			}
			a.mutableGlobalBorrowRefs[n] = refs
		}
	}
	sym := &Symbol{Name: n.Name, Kind: SymbolLocal, Type: bindingType, Node: n, Mutable: n.Mutable, BindingMutabilityExplicit: n.BindingExplicit, Ghost: n.Ghost}
	if n.Value != nil {
		a.noteImplicitIntConversion(n.Value, a.exprTypes[n.Value], bindingType)
	}
	a.defineLocal(sym, n.Pos())
	if len(a.loopAffineFrames) != 0 {
		if base, named := trackedNamedStateStructBase(sym.Type); named && base != nil && !base.ProtocolStates {
			if a.derivedLoopLocals == nil {
				a.derivedLoopLocals = map[*ast.VarDeclStmt]*Symbol{}
			}
			a.derivedLoopLocals[n] = sym
		}
	}
	a.bindOneShotClosure(sym, n.Value)
	// A freshly-constructed struct local whose container fields are backed by the ambient
	// region gets that region recorded, so a call site can thread it into a callee's
	// struct-ref region param (see region_struct_local.go).
	a.recordStructLocalAllocRegion(sym, bindingType, n.Value, valueType)
	a.recordStructLocalHomeRegion(sym)
	a.recordLocalContainerElementInit(sym, n.Value)
	a.recordRefinementChecks(n)
	a.seedWhereRefinementFact(n)
	if (n.Value == nil || isZeroedInitializer(n.Value)) && a.typeHasInvalidZeroValue(bindingType) {
		a.markInvalidUninitialized(sym)
	}
	if n.Value != nil && isZeroedInitializer(n.Value) {
		// Track zeroed struct locals for `ensure result.f` postcondition proofs: an unwritten field
		// on a zeroed local is definitively zero until the root is invalidated.
		if _, ok := stripRefForBounds(bindingType).(*StructType); ok {
			if a.zeroedStructLocals == nil {
				a.zeroedStructLocals = map[string]bool{}
			}
			a.zeroedStructLocals[n.Name] = true
		}
	}
	if n.Mutable && n.Value != nil {
		a.recordSMTAssignmentFact(&ast.Ident{Position: n.Position, Name: n.Name}, n.Value)
		a.recordConstAssignmentRangeFact(&ast.Ident{Position: n.Position, Name: n.Name}, n.Value)
		a.recordCastRangeFact(&ast.Ident{Position: n.Position, Name: n.Name}, n.Value)
		a.recordMatchExprRangeFact(n.Name, n.Value, bindingType)
	}
	// Transfer range facts through value-preserving widening cast chains for immutable integer
	// locals (`y: u64 = x.u64()` where `x` has range [lo,hi]). Mutable targets are excluded
	// (their range can be invalidated by reassignment; the check inside no-ops on mutable=true).
	if n.Value != nil {
		a.recordWideningCastRangeFact(&ast.Ident{Position: n.Position, Name: n.Name}, n.Value, n.Mutable)
	}
	a.recordSpecializedValueTypeBinding(sym, valueType)
	// An immutable local with a compile-time-constant initializer (`k: i32 = 5`) is pinned to
	// that value for its whole lifetime — record it as a written-constant so the interval prover
	// (boundAffine) and refinement discharge can treat it as an exact value. Sound: immutable, so
	// never invalidated. recordWrittenConstForTarget no-ops for a non-const RHS.
	if !n.Mutable && n.Value != nil {
		a.recordWrittenConstForTarget(&ast.Ident{Position: n.Position, Name: n.Name}, n.Value)
		a.recordCastRangeFact(&ast.Ident{Position: n.Position, Name: n.Name}, n.Value)
		// An immutable integer local assigned from a match expression in value position gets a
		// range fact that is the JOIN of all arm-body ranges, so downstream refinement discharge
		// (tryProveRefinementByFlow) can prove `x is SomeLaw` without a runtime check (docs/85).
		a.recordMatchExprRangeFact(n.Name, n.Value, bindingType)
		// An immutable integer binding to a length-ish expression (`n = xs.count`) makes `n`
		// provably equal to that expression for the binding's lifetime — a cross-variable bound
		// equality (docs/86 brick 86-6) that lets `for i in 0..<n: xs[i]` discharge. Invalidated if
		// the referenced container is mutated (shared with the index-bound invalidation sites).
		if IsNumericType(bindingType) && !IsFloatType(bindingType) {
			a.recordBoundEqual(n.Name, optimizationExprString(n.Value))
		}
		// docs/90 brick 90-7: when the initializer is a direct call to a function with a refined
		// return type (`-> i64 is Bounded[..]`), assume that postcondition as a flow fact on the
		// binding. The callee proves its return refinement; the caller now uses it without
		// re-deriving it from the body (modular verification).
		a.seedReturnRefinementFacts(n.Name, n.Value, bindingType)
		// docs P1 (protocol-contract composition): when the initializer is a generic call to a
		// protocol method through a bounded type param, assume the PROTOCOL's `ensure` (with `result`
		// bound to this immutable binding) as a flow fact — the caller half of behavioral subtyping.
		// Sound for an immutable binding: the fact is never invalidated by a later reassignment.
		a.seedProtocolEnsureFacts(n.Name, n.Value)
		// Parametric alias call-site discharge: an immutable darray initialised from a list
		// literal has a statically-known element count.  Record it so `xs.count` resolves to
		// that constant in affineOf / substitutedAffine when a callee's where-predicate
		// (produced by expanding e.g. `IndexOf[xs]`) references `xs.count`.
		if list, ok := unwrapParen(n.Value).(*ast.ListLitExpr); ok && list != nil {
			a.recordWrittenListCount(n.Name, int64(len(list.Elems)))
		}
		// If the initializer is a ternary/if-expression over integer values, seed the binding's
		// range fact with the union of the two branch ranges (docs/85 ternary range-merge).
		// Sound: immutable binding is never invalidated, so the joined range holds for the
		// binding's whole lifetime.
		if IsNumericType(bindingType) && !IsFloatType(bindingType) {
			if tern, isTern := unwrapParen(n.Value).(*ast.TernaryExpr); isTern {
				if r, derived := a.tryDeriveTernaryRange(tern); derived {
					if a.currentScope.rangeFacts == nil {
						a.currentScope.rangeFacts = map[string]numRange{}
					}
					a.currentScope.rangeFacts[n.Name] = r
				}
			}
		}
	}
	a.recordValueBinding(sym, n.Value)
	a.recordViewStaticLenBinding(n.Name, n.Value, bindingType)
	a.markCreatedProtocolSymbol(sym, n.Value)
	a.markReceivedOwnedRegion(sym, n.Value)
	if isOwnedTypeExpr(n.Type) {
		// An owned region is move-only: rebinding it to another `owned`
		// binding must transfer it (consume the source), never copy the
		// allocator handle into a second owner (which would double-free).
		if srcOwner, ok := a.returnedRegionOwner(n.Value); ok {
			a.transferRegionOwnerOut(srcOwner, "moved into binding")
		} else if srcOwner, ok := a.regionOwnerIdent(n.Value); ok {
			a.errorf(n.Value.Pos(), "owned region %q is move-only: use `move %s` to transfer ownership to %q", srcOwner.Name, srcOwner.Name, n.Name)
		}
		a.registerOwnedStoreOwner(sym)
	}
	a.recordBorrowedOwnerRefBinding(sym, n.Value)
	a.recordFunctionValueBinding(sym, n.Value)
	a.recordImmutableSymbolOptimizationFacts(sym, n.Value)
	a.recordRegionRefBinding(sym, n.Value)
	// Propagate interior-region taint through `let dest = src` so a copied
	// region-less aggregate carries its dangling interior field forward.
	a.recordStructInteriorRegionTaint(&ast.Ident{Position: n.Position, Name: n.Name}, n.Value, valueType)
	a.recordStorageViewBinding(sym, n.Value)
	a.recordStorageContainerAlias(sym, n.Value, valueType)
	a.dischargeLocalWhereRefinement(n)
	aliasAccessType := declType
	if n.Type == nil || typeExprHasExplicitMutableRef(n.Type) {
		aliasAccessType = bindingType
	} else if _, ok := aliasAccessType.(*RefType); !ok {
		aliasAccessType = bindingType
	}
	if n.Type != nil && (typeExprHasExplicitMutableRef(n.Type) || (n.Mutable && aliasExprIsExplicitBorrow(n.Value))) {
		if ref, ok := aliasAccessType.(*RefType); ok && !ref.Mutable {
			cloned := cloneRefType(ref)
			cloned.Mutable = true
			aliasAccessType = cloned
		}
	}
	a.recordLocalRefAliasBinding(n, sym, n.Value, aliasAccessType)
	a.noteGlobalStorageAliasLocal(sym, n.Value, bindingType)
	if from, fromType, ok := a.freezeMovedPackedStoreSource(n.Value); ok {
		a.remapPackedStoreDependencies(from, sym, PackedEnumStoreWithState(fromType, a.namedTypes["Frozen"]))
	}
	if n.Value != nil && AssignableTo(bindingType, valueType) {
		a.bindActivePackedStoreType(bindingType)
	}
	a.consumeAffineValueExpr(n.Value, bindingType, "move into local "+strconvQuote(n.Name))
	// docs/126 D1: a drop-typed local owns a resource from here until it is moved or
	// its scope ends. Registering it AFTER the initializer's own consumption keeps
	// `f: File = g` (a move out of g) from re-registering g's obligation on f's slot.
	a.noteDropLocalDeclaration(n, sym, bindingType)
}
