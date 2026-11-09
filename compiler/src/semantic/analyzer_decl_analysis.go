package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (a *Analyzer) analyzeDecls(decls []scopedDecl) {
	if a.retained != nil {
		defer func() { a.retained.bodyEnd = len(a.diagnostics) }()
	}
	// TaskGroup join proofs affect interprocedural store-flow summaries. Record
	// them for the whole declaration set before source-order body analysis can ask
	// for a summary of a later-declared function.
	a.recordVerifiedTaskGroupSubmitsInDecls(decls)
	// An incremental session records per-body products as bodies finish and is not built
	// for concurrent writers, so it keeps the serial path. A retained analysis's first
	// analysis runs in parallel, its workers recording into shells merged after the join
	// (analyzeDeclsParallel). Its updates stay serial: a worker clones the whole retained
	// analyzer, which costs more than the few bodies an update re-analyzes (18 bodies on the
	// compiler tree: 45 s in parallel, 1 s serial).
	if workers := parallelBodyWorkers(); workers > 1 && a.incrementalBodies == nil && a.lexicalReads == nil && !a.retainedProofsScoped {
		a.analyzeDeclsParallel(decls, workers)
		return
	}
	for _, scoped := range decls {
		a.analyzeScopedDecl(scoped)
	}
}

func (a *Analyzer) analyzeScopedDecl(scoped scopedDecl) {
	{
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			defer a.beginProofContribution(scoped.Decl, proofPhaseDeclaration, nil, "")()
			restoreProofOwner := a.enterIndexProofOwner(scoped.Decl, false)
			defer restoreProofOwner()
			switch n := scoped.Decl.(type) {
			case *ast.ConstDecl:
				if sym, ok := a.globalScope.Lookup(joinQualifiedName(scoped.Namespace, n.Name)); ok {
					a.constInitDepth++
					valueType := a.analyzeValueExprInScope(n.Value, sym.Type, a.globalScope)
					a.constInitDepth--
					if !AssignableTo(sym.Type, valueType) {
						a.errorf(n.Pos(), "const %q expects %s, got %s", n.Name, sym.Type, valueType)
					}
					a.constEvalExpectedType = sym.Type
					value, ok := a.evalConstExpr(n.Value)
					a.constEvalExpectedType = nil
					if ok {
						a.constValues[joinQualifiedName(scoped.Namespace, n.Name)] = value
					} else {
						a.errorf(n.Value.Pos(), "const %q initializer must be a compile-time %s value", n.Name, sym.Type)
					}
				}
			case *ast.TokenSetDecl:
				if sym, ok := a.globalScope.Lookup(joinQualifiedName(scoped.Namespace, n.Name)); ok {
					valueType := a.analyzeValueExprInScope(n.Value, sym.Type, a.globalScope)
					if !AssignableTo(sym.Type, valueType) {
						a.errorf(n.Pos(), "tokenset %q expects %s, got %s", n.Name, sym.Type, valueType)
					}
				}
			case *ast.CharsetDecl:
				a.analyzeCharsetDecl(n)
			case *ast.GlobalDecl:
				if n.Value != nil {
					if sym, ok := a.globalScope.Lookup(joinQualifiedName(scoped.Namespace, n.Name)); ok {
						valueType := a.analyzeValueExprInScope(n.Value, sym.Type, a.globalScope)
						if !AssignableTo(sym.Type, valueType) {
							a.errorf(n.Pos(), "global %q expects %s, got %s", n.Name, sym.Type, valueType)
						}
					}
				}
			case *ast.FuncDecl:
				// A `contract` decl (docs/97) is a named bundle of clauses, not an executable function:
				// it has no body and is consumed by expandUsesContracts. Skip function analysis/codegen.
				if n.IsContract {
					a.validateContractDecl(n)
					return
				}
				a.analyzeFunctionAnnotations(n)
				a.rewriteEffectHandlers(n.Body)
				if n.Static {
					a.staticContextDepth++
					a.analyzeFunc(n)
					a.validateStaticFunctionTotality(n)
					a.staticContextDepth--
				} else {
					a.analyzeFunc(n)
				}
				if n.IsGhost {
					a.validateGhostFuncDecl(n)
				}
			case *ast.StaticAssertDecl:
				a.analyzeStaticAssert(n.Pos(), n.Cond, n.Message)
			case *ast.StaticAssertBlockDecl:
				for _, item := range n.Assertions {
					a.analyzeStaticAssert(item.Position, item.Cond, item.Message)
				}
			case *ast.StaticGenerateDecl:
			case *ast.InterfaceDecl:
			case *ast.ImplDecl:
				if n.IsHandler {
					for _, member := range n.Members {
						fnDecl, ok := member.(*ast.FuncDecl)
						if !ok {
							continue
						}
						a.analyzeFunctionAnnotations(fnDecl)
						a.analyzeFunc(fnDecl)
					}
					return
				}
				// `Self` inside an impl method body/signature stands for the concrete
				// implementing type; bind it (under the impl's generic params) so that
				// `def m(self: Self)` resolves to the receiver during body analysis,
				// mirroring the symbol-collection pass and the protocol decl.
				a.withGenericParams(n.GenericParams, nil, func() {
					var selfBindings map[string]Type
					if receiver := a.resolveType(n.ForType); receiver != nil && !IsInvalidType(receiver) {
						selfBindings = map[string]Type{staticInterfaceSelfName: receiver}
					}
					a.withInterfaceAssocTypes(selfBindings, func() {
						for _, member := range n.Members {
							fnDecl, ok := member.(*ast.FuncDecl)
							if !ok {
								continue
							}
							a.analyzeFunctionAnnotations(fnDecl)
							a.analyzeFunc(fnDecl)
						}
					})
				})
			case *ast.ConstEnumDecl:
			case *ast.EnumDecl:
			case *ast.ErrorDecl:
			case *ast.PermissionDecl:
			case *ast.TypeAliasDecl, *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
			}
		})
	}
}

func (a *Analyzer) analyzeFunctionAnnotations(fn *ast.FuncDecl) {
	if len(fn.Annotations) == 0 {
		return
	}

	valid := make([]ast.Annotation, 0, len(fn.Annotations))
	seen := make(map[string]lexer.Pos, len(fn.Annotations))
	for _, annotation := range fn.Annotations {
		if prev, exists := seen[annotation.Name]; exists {
			a.errorf(annotation.Position, "duplicate @%s annotation on function %q (first seen at line %d:%d)", annotation.Name, fn.Name, prev.Line, prev.Col)
			continue
		}
		seen[annotation.Name] = annotation.Position
		if hint, removed := removedAnnotationMigration(annotation.Name); removed {
			a.errorf(annotation.Position, "@%s has been removed; %s", annotation.Name, hint)
			continue
		}
		if !isSupportedFunctionAnnotation(annotation.Name) {
			a.errorf(annotation.Position, "unknown function annotation @%s on %q", annotation.Name, fn.Name)
			continue
		}
		valid = append(valid, annotation)
	}

	if len(valid) == 0 {
		return
	}

	var signature *FuncType
	if sym, ok := a.symbolForFuncDecl(fn); ok {
		signature, _ = sym.Type.(*FuncType)
	}
	accepted := make([]ast.Annotation, 0, len(valid))
	for _, annotation := range valid {
		if a.validateFunctionAnnotation(annotation, fn, signature) {
			switch annotation.Name {
			case "inline":
				a.applyFunctionInlineAnnotation(annotation, fn, signature)
			case "norecurse":
				a.applyFunctionNoRecurseAnnotation(annotation, fn, signature)
			case "hot", "cold":
				a.applyFunctionTemperatureAnnotation(annotation, fn, signature)
			case "fast_math":
				signature.FastMath = true
			case "callconv":
				a.applyFunctionCallConvAnnotation(annotation, fn, signature)
			case "guard_nonnull", "guard_variant":
				a.applyFunctionGuardAnnotation(annotation, fn, signature)
			case "boundary_pointer_args":
				a.applyFunctionBoundaryPointerArgsAnnotation(annotation, fn, signature)
			case "async_entry", "segment_agnostic", "segment_establishing", "segment_transition", "reentrant_safe":
				a.applyFunctionSegmentSafetyAnnotation(annotation, fn, signature)
			case "internal":
				// Interface visibility marker only; it is not a runtime/test annotation.
			case "init":
				// Constructor hook marker; registered during value-symbol collection.
			default:
				accepted = append(accepted, annotation)
			}
		}
	}
	if len(accepted) == 0 {
		return
	}
	a.annotatedFuncs = append(a.annotatedFuncs, &AnnotatedFunc{
		Name:        fn.Name,
		Annotations: accepted,
		Signature:   signature,
		Decl:        fn,
	})
}

func (a *Analyzer) applyFunctionInlineAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) {
	if signature == nil || len(annotation.Args) != 1 {
		return
	}
	mode, ok := normalizeInlineAnnotationArg(annotation.Args[0])
	if !ok {
		return
	}
	signature.InlineMode = mode
	signature.HasInlineMode = true
}

func (a *Analyzer) applyFunctionNoRecurseAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) {
	if signature == nil || len(annotation.Args) != 0 {
		return
	}
	signature.HasNoRecurse = true
}

func (a *Analyzer) applyFunctionSegmentSafetyAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) {
	if signature == nil {
		return
	}
	switch annotation.Name {
	case "async_entry":
		if len(annotation.Args) != 0 {
			return
		}
		signature.HasAsyncEntry = true
	case "segment_agnostic":
		if len(annotation.Args) != 0 {
			return
		}
		signature.HasSegmentAgnostic = true
	case "segment_establishing":
		if len(annotation.Args) != 0 {
			return
		}
		signature.HasSegmentEstablishing = true
	case "reentrant_safe":
		if len(annotation.Args) != 0 {
			return
		}
		signature.HasReentrantSafe = true
	case "segment_transition":
		if len(annotation.Args) != 1 {
			return
		}
		transition, ok := normalizeSegmentTransitionAnnotationArg(annotation.Args[0])
		if !ok {
			return
		}
		signature.SegmentTransition = transition
	}
}

func (a *Analyzer) applyFunctionTemperatureAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) {
	if signature == nil || len(annotation.Args) != 0 {
		return
	}
	mode, ok := temperatureModeForAnnotationName(annotation.Name)
	if !ok {
		return
	}
	if signature.HasTemperatureMode {
		if signature.TemperatureMode != mode {
			a.errorf(annotation.Position, "@%s on function %q conflicts with existing @%s annotation", annotation.Name, fn.Name, string(signature.TemperatureMode))
		}
		return
	}
	signature.TemperatureMode = mode
	signature.HasTemperatureMode = true
}

func (a *Analyzer) applyFunctionCallConvAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) {
	if signature == nil {
		return
	}
	switch annotation.Name {
	case "callconv":
		if len(annotation.Args) != 1 {
			return
		}
		callConv, ok := normalizeExternCallConvAnnotationArg(annotation.Args[0])
		if !ok {
			return
		}
		signature.CallConv = callConv
	}
}

func (a *Analyzer) applyFunctionGuardAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) {
	if signature == nil {
		return
	}
	switch annotation.Name {
	case "guard_nonnull":
		paramIndex, ok := a.functionAnnotationParamIndex(fn, annotation.Args[0])
		if !ok {
			return
		}
		signature.GuardEffects = append(signature.GuardEffects, FuncGuardEffect{Kind: FuncGuardKindNonNull, ParamIndex: paramIndex})
	case "guard_variant":
		paramIndex, ok := a.functionAnnotationParamIndex(fn, annotation.Args[0])
		if !ok {
			return
		}
		enumName, variantName, ok := parseFunctionGuardVariantPath(annotation.Args[1])
		if !ok {
			return
		}
		base, _, ok := a.lookupVisibleType(enumName)
		if !ok {
			return
		}
		switch variantBase := base.(type) {
		case *EnumType:
			if variantBase == nil {
				return
			}
			signature.GuardEffects = append(signature.GuardEffects, FuncGuardEffect{Kind: FuncGuardKindPackedVariant, ParamIndex: paramIndex, EnumName: variantBase.Name, VariantName: variantName})
		}
	}
}

func (a *Analyzer) validateFunctionBoundaryPointerArgsAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) bool {
	if len(annotation.Args) == 0 {
		a.errorf(annotation.Position, "@boundary_pointer_args on function %q expects at least one parameter name", fn.Name)
		return false
	}
	ok := true
	seen := map[int]bool{}
	for _, raw := range annotation.Args {
		paramIndex, found := a.functionAnnotationParamIndex(fn, strings.TrimSpace(raw))
		if !found {
			ok = false
			continue
		}
		if seen[paramIndex] {
			a.errorf(annotation.Position, "@boundary_pointer_args on function %q lists parameter %q more than once", fn.Name, raw)
			ok = false
			continue
		}
		seen[paramIndex] = true
		if paramIndex < 0 || paramIndex >= len(signature.Params) {
			ok = false
			continue
		}
		if !isBoundaryPointerCarrierType(signature.Params[paramIndex]) {
			a.errorf(annotation.Position, "@boundary_pointer_args on function %q marks %q as an address-space pointer, but its type is %s; use a typed address-space carrier such as GuestVAddr[T], HostPtr[T], or NativeMappedGuestPtr[T] at the unsafe boundary and resolve before host dereference", fn.Name, strings.TrimSpace(raw), signature.Params[paramIndex])
			ok = false
		}
	}
	return ok
}

func (a *Analyzer) applyFunctionBoundaryPointerArgsAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) {
	if signature == nil {
		return
	}
	for _, raw := range annotation.Args {
		paramIndex, ok := a.functionAnnotationParamIndex(fn, strings.TrimSpace(raw))
		if !ok {
			continue
		}
		if !intSliceContains(signature.BoundaryPointerParamIndices, paramIndex) {
			signature.BoundaryPointerParamIndices = append(signature.BoundaryPointerParamIndices, paramIndex)
		}
	}
}

func isBoundaryPointerCarrierType(t Type) bool {
	switch tt := t.(type) {
	case *AddressSpaceType:
		return tt.Space == "guest" || tt.Space == "host" || tt.Space == "native_mapped_guest"
	case *InvalidType:
		return true
	default:
		return false
	}
}

func intSliceContains(values []int, needle int) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func (a *Analyzer) validateFunctionAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) bool {
	if signature == nil {
		a.errorf(annotation.Position, "cannot resolve signature for @%s function %q", annotation.Name, fn.Name)
		return false
	}
	if annotation.Name == "guard_nonnull" {
		return a.validateFunctionGuardNonNullAnnotation(annotation, fn, signature)
	}
	if annotation.Name == "guard_variant" {
		return a.validateFunctionGuardVariantAnnotation(annotation, fn, signature)
	}
	if annotation.Name == "skip" {
		return true
	}
	if annotation.Name == "link_name" {
		if len(annotation.Args) != 1 || strings.TrimSpace(annotation.Args[0]) == "" {
			a.errorf(annotation.Position, "@link_name on function %q expects exactly one non-empty symbol name", fn.Name)
			return false
		}
		return true
	}
	if annotation.Name == "internal" {
		if len(annotation.Args) != 0 {
			a.errorf(annotation.Position, "@internal on function %q does not take arguments", fn.Name)
			return false
		}
		return true
	}
	if annotation.Name == "main_thread" {
		if len(annotation.Args) != 0 {
			a.errorf(annotation.Position, "@main_thread on function %q does not take arguments", fn.Name)
			return false
		}
		return true
	}
	if annotation.Name == "init" {
		return a.validateFunctionInitAnnotation(annotation, fn, signature)
	}
	if annotation.Name == "boundary_pointer_args" {
		return a.validateFunctionBoundaryPointerArgsAnnotation(annotation, fn, signature)
	}
	if annotation.Name == "async_entry" || annotation.Name == "segment_agnostic" || annotation.Name == "segment_establishing" || annotation.Name == "reentrant_safe" {
		if len(annotation.Args) != 0 {
			a.errorf(annotation.Position, "@%s on function %q does not take arguments", annotation.Name, fn.Name)
			return false
		}
		return true
	}
	if annotation.Name == "segment_transition" {
		if len(annotation.Args) != 1 {
			a.errorf(annotation.Position, "@segment_transition on function %q expects exactly one target owner: host or guest", fn.Name)
			return false
		}
		if _, ok := normalizeSegmentTransitionAnnotationArg(annotation.Args[0]); !ok {
			a.errorf(annotation.Position, "@segment_transition on function %q uses unsupported target %q (expected host or guest)", fn.Name, strings.TrimSpace(annotation.Args[0]))
			return false
		}
		return true
	}
	if annotation.Name == "inline" {
		if len(annotation.Args) != 1 {
			a.errorf(annotation.Position, "@inline on function %q expects exactly one mode argument", fn.Name)
			return false
		}
		if _, ok := normalizeInlineAnnotationArg(annotation.Args[0]); !ok {
			a.errorf(annotation.Position, "@inline on function %q uses unsupported mode %q (expected always or never)", fn.Name, annotation.Args[0])
			return false
		}
		return true
	}
	if annotation.Name == "norecurse" {
		if len(annotation.Args) != 0 {
			a.errorf(annotation.Position, "@norecurse on function %q does not take arguments", fn.Name)
			return false
		}
		return true
	}
	if annotation.Name == "hot" {
		// @hot is the strict fast-path contract and forbids allocation by default.
		// @hot(alloc) is the explicit opt-in for allocation-bearing hot code.
		for _, arg := range annotation.Args {
			if strings.ToLower(strings.TrimSpace(arg)) != "alloc" {
				a.errorf(annotation.Position, "@hot on function %q only accepts the optional `alloc` argument", fn.Name)
				return false
			}
		}
		return true
	}
	if annotation.Name == "cold" {
		if len(annotation.Args) != 0 {
			a.errorf(annotation.Position, "@cold on function %q does not take arguments", fn.Name)
			return false
		}
		return true
	}
	if annotation.Name == "noalloc" || annotation.Name == "inbounds" || annotation.Name == "nolock" {
		// Enforceable performance contracts (docs/70, the perf analog of -strict). They apply to any
		// function — including generic/permissioned ones — and take no arguments. Enforcement runs after
		// the body is analyzed in checkPerfContracts (the transitive permission/guarded-index sets are
		// final there).
		if len(annotation.Args) != 0 {
			a.errorf(annotation.Position, "@%s on function %q does not take arguments", annotation.Name, fn.Name)
			return false
		}
		return true
	}
	if annotation.Name == "fast_math" {
		if len(annotation.Args) != 0 {
			a.errorf(annotation.Position, "@fast_math on function %q does not take arguments", fn.Name)
			return false
		}
		return true
	}
	if annotation.Name == "callconv" {
		if len(annotation.Args) != 1 || strings.TrimSpace(annotation.Args[0]) == "" {
			a.errorf(annotation.Position, "@%s on function %q expects exactly one calling convention name", annotation.Name, fn.Name)
			return false
		}
		if _, ok := normalizeExternCallConvAnnotationArg(annotation.Args[0]); !ok {
			a.errorf(annotation.Position, "unsupported calling convention %q on function %q", strings.TrimSpace(annotation.Args[0]), fn.Name)
			return false
		}
		return true
	}
	if annotation.Name == "deprecated" {
		// Pure metadata: applies to any function (generic, permissioned) — just records
		// a use-site warning. Optional single string message.
		if len(annotation.Args) > 1 {
			a.errorf(annotation.Position, "@deprecated on function %q takes at most one message argument", fn.Name)
			return false
		}
		return true
	}
	if annotation.Name == "property" || annotation.Name == "differential" {
		return a.validateFunctionPropertyAnnotation(annotation, fn, signature)
	}
	if annotation.Name == "lockstep" {
		return a.validateFunctionLockstepAnnotation(annotation, fn, signature)
	}
	if len(signature.TypeParams) > 0 || len(signature.RegionParams) > 0 || len(signature.ShapeParams) > 0 {
		a.errorf(annotation.Position, "@%s function %q must not have type or shape parameters; got %s", annotation.Name, fn.Name, signature)
		return false
	}
	if !annotationAllowsDeclaredPermissions(annotation.Name, signature) {
		a.errorf(annotation.Position, "@%s function %q must not require permissions; got %s", annotation.Name, fn.Name, signature)
		return false
	}
	if signature.Variadic {
		a.errorf(annotation.Position, "@%s function %q must not be variadic", annotation.Name, fn.Name)
		return false
	}
	if len(signature.Params) > 0 {
		a.errorf(annotation.Position, "@%s function %q must not take parameters; got %s", annotation.Name, fn.Name, signature)
		return false
	}
	switch annotation.Name {
	case "test", "bench":
		if !isVoidType(signature.Return) {
			a.errorf(annotation.Position, "@%s function %q must return void, got %s", annotation.Name, fn.Name, signature.Return)
			return false
		}
	}
	return true
}
