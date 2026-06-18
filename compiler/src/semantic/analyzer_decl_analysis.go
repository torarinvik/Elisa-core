package semantic

import (
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) analyzeDecls(decls []scopedDecl) {
	for _, scoped := range decls {
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			switch n := scoped.Decl.(type) {
			case *ast.ConstDecl:
				if sym, ok := a.globalScope.Lookup(joinQualifiedName(scoped.Namespace, n.Name)); ok {
					valueType := a.analyzeValueExprInScope(n.Value, sym.Type, a.globalScope)
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
				a.analyzeFunctionAnnotations(n)
				if n.Static {
					a.staticContextDepth++
					a.analyzeFunc(n)
					a.validateStaticFunctionTotality(n)
					a.staticContextDepth--
				} else {
					a.analyzeFunc(n)
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
				for _, member := range n.Members {
					fnDecl, ok := member.(*ast.FuncDecl)
					if !ok {
						continue
					}
					a.analyzeFunctionAnnotations(fnDecl)
					a.analyzeFunc(fnDecl)
				}
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
			a.errorf(annotation.Position, "duplicate @%s annotation on function %q (first seen at %s:%d:%d)", annotation.Name, fn.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[annotation.Name] = annotation.Position
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
			case "callconv", "c_abi", "stdcall":
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
	case "callconv", "c_abi":
		if len(annotation.Args) != 1 {
			return
		}
		callConv, ok := normalizeExternCallConvAnnotationArg(annotation.Args[0])
		if !ok {
			return
		}
		signature.CallConv = callConv
	case "stdcall":
		if len(annotation.Args) != 0 {
			return
		}
		signature.CallConv = "stdcall"
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
	if annotation.Name == "skip" || annotation.Name == "ignore" {
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
	if annotation.Name == "fast_math" {
		if len(annotation.Args) != 0 {
			a.errorf(annotation.Position, "@fast_math on function %q does not take arguments", fn.Name)
			return false
		}
		return true
	}
	if annotation.Name == "callconv" || annotation.Name == "c_abi" {
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
	if annotation.Name == "stdcall" {
		if len(annotation.Args) != 0 {
			a.errorf(annotation.Position, "@stdcall on function %q does not take arguments", fn.Name)
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
	if annotation.Name == "property" {
		return a.validateFunctionPropertyAnnotation(annotation, fn, signature)
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

// PropertyParamTypeName reports the canonical builtin name of a property-test
// parameter type for which the harness can synthesize random inputs, and whether
// the type is supported. Supported set: bool, the fixed-width integers, and the
// floating-point types.
func PropertyParamTypeName(t Type) (string, bool) {
	b, ok := t.(*BuiltinType)
	if !ok {
		return "", false
	}
	switch b.Name {
	case "bool", "int", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "f32", "f64":
		return b.Name, true
	default:
		return "", false
	}
}

// PropertyCaseCount parses a @property case-count argument (a bare positive
// integer literal such as "1000") and reports the value and whether it parsed.
func PropertyCaseCount(arg string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil {
		return 0, false
	}
	return n, true
}

func (a *Analyzer) validateFunctionPropertyAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) bool {
	if len(annotation.Args) > 1 {
		a.errorf(annotation.Position, "@property on function %q takes at most one argument (the case count, e.g. @property(1000))", fn.Name)
		return false
	}
	if len(annotation.Args) == 1 {
		if n, ok := PropertyCaseCount(annotation.Args[0]); !ok || n <= 0 {
			a.errorf(annotation.Position, "@property on function %q expects a positive integer case count, got %q", fn.Name, annotation.Args[0])
			return false
		}
	}
	if len(signature.TypeParams) > 0 || len(signature.RegionParams) > 0 || len(signature.ShapeParams) > 0 || len(signature.GenericParams) > 0 {
		a.errorf(annotation.Position, "@property function %q must not be generic; got %s", fn.Name, signature)
		return false
	}
	if !annotationAllowsDeclaredPermissions("property", signature) {
		a.errorf(annotation.Position, "@property function %q must not require permissions; got %s", fn.Name, signature)
		return false
	}
	if signature.Variadic {
		a.errorf(annotation.Position, "@property function %q must not be variadic", fn.Name)
		return false
	}
	if !IsBoolType(signature.Return) {
		a.errorf(annotation.Position, "@property function %q must return bool, got %s", fn.Name, signature.Return)
		return false
	}
	if len(signature.Params) == 0 {
		a.errorf(annotation.Position, "@property function %q must take at least one generated parameter", fn.Name)
		return false
	}
	for i, p := range signature.Params {
		if _, ok := PropertyParamTypeName(p); !ok {
			name := ""
			if i < len(signature.ExplicitParamNames) {
				name = signature.ExplicitParamNames[i]
			}
			a.errorf(annotation.Position, "@property function %q parameter %q has type %s, which the harness cannot generate (supported: bool, int, i8/i16/i32/i64, u8/u16/u32/u64, f32/f64)", fn.Name, name, p)
			return false
		}
	}
	return true
}

func (a *Analyzer) validateFunctionInitAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) bool {
	if len(annotation.Args) > 1 {
		a.errorf(annotation.Position, "@init on function %q expects zero arguments or one return-type name", fn.Name)
		return false
	}
	if len(signature.ImplicitParamNames) != 0 {
		a.errorf(annotation.Position, "@init function %q must not declare implicit parameters", fn.Name)
		return false
	}
	if len(signature.TypeParams) != 0 || len(signature.RegionParams) != 0 || len(signature.PermissionParams) != 0 || len(signature.GenericParams) != 0 {
		a.errorf(annotation.Position, "@init function %q must not be generic", fn.Name)
		return false
	}
	if signature.Variadic {
		a.errorf(annotation.Position, "@init function %q must not be variadic", fn.Name)
		return false
	}
	if signature.Return == nil || isVoidType(signature.Return) {
		a.errorf(annotation.Position, "@init function %q must return a concrete struct type", fn.Name)
		return false
	}
	base, _, _, ok := structLiteralBaseAndBindings(signature.Return)
	if !ok || base == nil {
		a.errorf(annotation.Position, "@init function %q must return a concrete struct type, got %s", fn.Name, signature.Return)
		return false
	}
	if len(annotation.Args) == 1 {
		targetName := strings.TrimSpace(annotation.Args[0])
		if targetName == "" {
			a.errorf(annotation.Position, "@init on function %q expects a non-empty return-type name", fn.Name)
			return false
		}
		targetType, _, ok := a.lookupVisibleType(targetName)
		if !ok {
			a.errorf(annotation.Position, "@init on function %q references unknown type %q", fn.Name, targetName)
			return false
		}
		targetBase, _, _, ok := structLiteralBaseAndBindings(targetType)
		if !ok || targetBase == nil {
			a.errorf(annotation.Position, "@init on function %q target %q is not a concrete struct type", fn.Name, targetName)
			return false
		}
		if targetBase.Name != base.Name {
			a.errorf(annotation.Position, "@init on function %q targets %s but returns %s", fn.Name, targetType, signature.Return)
			return false
		}
	}
	return true
}

func (a *Analyzer) validateFunctionGuardSignature(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) bool {
	if signature.Return == nil || !IsBoolType(signature.Return) {
		a.errorf(annotation.Position, "@%s function %q must return bool, got %s", annotation.Name, fn.Name, signature.Return)
		return false
	}
	if signature.Variadic {
		a.errorf(annotation.Position, "@%s function %q must not be variadic", annotation.Name, fn.Name)
		return false
	}
	if len(signature.Permissions) != 0 {
		a.errorf(annotation.Position, "@%s function %q must not require permissions; got %s", annotation.Name, fn.Name, signature)
		return false
	}
	return true
}

func (a *Analyzer) validateFunctionGuardNonNullAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) bool {
	if !a.validateFunctionGuardSignature(annotation, fn, signature) {
		return false
	}
	if len(annotation.Args) != 1 {
		a.errorf(annotation.Position, "@guard_nonnull on function %q expects exactly one parameter name", fn.Name)
		return false
	}
	paramIndex, ok := a.functionAnnotationParamIndex(fn, annotation.Args[0])
	if !ok || paramIndex >= len(signature.Params) {
		a.errorf(annotation.Position, "@guard_nonnull on function %q references unknown parameter %q", fn.Name, annotation.Args[0])
		return false
	}
	if !guardNonNullParamType(signature.Params[paramIndex]) {
		a.errorf(annotation.Position, "@guard_nonnull on function %q requires a nullable reference or optional parameter, got %s", fn.Name, signature.Params[paramIndex])
		return false
	}
	return true
}

func (a *Analyzer) validateFunctionGuardVariantAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) bool {
	if !a.validateFunctionGuardSignature(annotation, fn, signature) {
		return false
	}
	if len(annotation.Args) != 2 {
		a.errorf(annotation.Position, "@guard_variant on function %q expects a parameter name and VariantType.Variant path", fn.Name)
		return false
	}
	paramIndex, ok := a.functionAnnotationParamIndex(fn, annotation.Args[0])
	if !ok || paramIndex >= len(signature.Params) {
		a.errorf(annotation.Position, "@guard_variant on function %q references unknown parameter %q", fn.Name, annotation.Args[0])
		return false
	}
	enumName, variantName, ok := parseFunctionGuardVariantPath(annotation.Args[1])
	if !ok {
		a.errorf(annotation.Position, "@guard_variant on function %q expects a VariantType.Variant path, got %q", fn.Name, annotation.Args[1])
		return false
	}
	base, _, ok := a.lookupVisibleType(enumName)
	if !ok {
		a.errorf(annotation.Position, "@guard_variant on function %q references unknown variant type %q", fn.Name, enumName)
		return false
	}
	switch variantBase := base.(type) {
	case *EnumType:
		if variantBase == nil {
			return false
		}
		if !variantBase.Packed {
			a.errorf(annotation.Position, "@guard_variant on function %q currently requires a packed enum variant path, got %q", fn.Name, annotation.Args[1])
			return false
		}
		if _, ok := variantBase.Variant(variantName); !ok {
			a.errorf(annotation.Position, "enum %q has no variant %q", variantBase.Name, variantName)
			return false
		}
		paramEnum, _, ok := resolveMatchableEnumType(signature.Params[paramIndex])
		if !ok || paramEnum == nil || !paramEnum.Packed {
			a.errorf(annotation.Position, "@guard_variant on function %q requires a packed enum parameter, got %s", fn.Name, signature.Params[paramIndex])
			return false
		}
		if paramEnum.Name != variantBase.Name {
			a.errorf(annotation.Position, "@guard_variant on function %q expects parameter %q to use variant type %q, got %q", fn.Name, annotation.Args[0], variantBase.Name, paramEnum.Name)
			return false
		}
	default:
		a.errorf(annotation.Position, "@guard_variant on function %q expects a packed enum variant path, got %q", fn.Name, annotation.Args[1])
		return false
	}
	return true
}

func guardNonNullParamType(t Type) bool {
	switch tt := StripAggregateStateType(t).(type) {
	case *RefType:
		return tt != nil && tt.State == RefStateNullable
	case *OptionalType:
		return tt != nil
	default:
		return false
	}
}

func (a *Analyzer) functionAnnotationParamIndex(fn *ast.FuncDecl, name string) (int, bool) {
	if a == nil || fn == nil || name == "" {
		return 0, false
	}
	for i, param := range a.expandedFuncDeclParams(fn) {
		if param.Name == name {
			return i, true
		}
	}
	return 0, false
}

func parseFunctionGuardVariantPath(text string) (string, string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", "", false
	}
	split := strings.LastIndex(trimmed, ".")
	if split <= 0 || split >= len(trimmed)-1 {
		return "", "", false
	}
	return trimmed[:split], trimmed[split+1:], true
}

func annotationAllowsDeclaredPermissions(annotationName string, signature *FuncType) bool {
	if signature == nil || len(signature.Permissions) == 0 {
		return true
	}
	return annotationName == "test"
}

func funcHasAnnotation(fn *ast.FuncDecl, name string) bool {
	if fn == nil || name == "" {
		return false
	}
	return annotationsHave(fn.Annotations, name)
}

// funcDeprecationMessage returns the `@deprecated("...")` message for a function decl,
// or "" if it carries no @deprecated annotation. A bare `@deprecated` yields a default.
func funcDeprecationMessage(fn *ast.FuncDecl) string {
	if fn == nil {
		return ""
	}
	for _, ann := range fn.Annotations {
		if ann.Name != "deprecated" {
			continue
		}
		if len(ann.Args) > 0 {
			msg := strings.Trim(strings.TrimSpace(ann.Args[0]), "\"")
			if msg != "" {
				return msg
			}
		}
		return "this function is deprecated"
	}
	return ""
}

func externFuncHasAnnotation(fn *ast.ExternFuncDecl, name string) bool {
	if fn == nil || name == "" {
		return false
	}
	return annotationsHave(fn.Annotations, name)
}

func annotationsHave(annotations []ast.Annotation, name string) bool {
	for _, annotation := range annotations {
		if annotation.Name == name {
			return true
		}
	}
	return false
}

func isSupportedFunctionAnnotation(name string) bool {
	switch name {
	case "test", "bench", "property", "fixture", "skip", "ignore", "inline", "fast_math", "norecurse", "hot", "cold", "callconv", "c_abi", "stdcall", "guard_nonnull", "guard_variant", "internal", "main_thread", "init", "async_entry", "segment_agnostic", "segment_establishing", "segment_transition", "reentrant_safe", "deprecated":
		return true
	case "boundary_pointer_args":
		return true
	default:
		return false
	}
}

func poststateTargetDisplayName(rootName string, steps []borrowReturnAnnotationStep) string {
	if rootName == "" {
		return "<value>"
	}
	return rootName + borrowAnnotationPathSuffix(steps)
}

func poststateNamedStateCurrentType(t Type) (Type, bool) {
	switch tt := t.(type) {
	case *AggregateStateType:
		return poststateNamedStateCurrentType(tt.Base)
	case *RefType:
		return poststateNamedStateCurrentType(tt.Elem)
	default:
		if _, ok := namedStateStructBase(t); ok {
			return t, true
		}
		return nil, false
	}
}

func (a *Analyzer) currentFuncParamSymbol(index int) (*Symbol, bool) {
	if a == nil || a.currentFuncDecl == nil || a.currentScope == nil || index < 0 {
		return nil, false
	}
	params := append([]ast.ParamDecl(nil), a.currentFuncDecl.Params...)
	if index >= len(params) {
		return nil, false
	}
	name := params[index].Name
	if name == "" {
		return nil, false
	}
	sym, ok := a.currentScope.Lookup(name)
	return sym, ok && sym != nil
}

func (a *Analyzer) validateCurrentFuncPoststates() {
	a.validateCurrentFuncPoststatesForOutcome(false, false)
}

func funcPoststatesHaveConditional(poststates []FuncPoststate) bool {
	for _, poststate := range poststates {
		if poststate.Condition.Kind != FuncPoststateConditionAlways {
			return true
		}
	}
	return false
}

func funcPoststateAppliesForOutcome(poststate FuncPoststate, outcomeKnown bool, outcomeValue bool) bool {
	switch poststate.Condition.Kind {
	case FuncPoststateConditionReturnBool:
		return outcomeKnown && poststate.Condition.ReturnBool == outcomeValue
	default:
		return true
	}
}

func (a *Analyzer) validateCurrentFuncPoststatesForReturnValue(value ast.Expr) {
	if a == nil || a.currentFuncType == nil || !funcPoststatesHaveConditional(a.currentFuncType.Poststates) {
		a.validateCurrentFuncPoststates()
		return
	}
	outcomeValue, outcomeKnown := a.evalConstBoolExpr(value)
	if !outcomeKnown {
		a.errorf(value.Pos(), "functions with branch-sensitive ensures must return literal true or false on each return path")
	}
	a.validateCurrentFuncPoststatesForOutcome(outcomeKnown, outcomeValue)
}

func (a *Analyzer) validateCurrentFuncPoststatesForOutcome(outcomeKnown bool, outcomeValue bool) {
	if a == nil || a.suppressDiagnostics || a.currentFuncDecl == nil || a.currentFuncType == nil || len(a.currentFuncType.Poststates) == 0 {
		return
	}
	for _, poststate := range a.currentFuncType.Poststates {
		if !funcPoststateAppliesForOutcome(poststate, outcomeKnown, outcomeValue) {
			continue
		}
		a.validateCurrentFuncPoststate(poststate)
	}
}

func (a *Analyzer) validateCurrentFuncPoststate(poststate FuncPoststate) {
	sym, ok := a.currentFuncParamSymbol(poststate.ParamIndex)
	if !ok || sym == nil {
		return
	}
	targetName := poststateTargetDisplayName(sym.Name, poststate.Path)
	currentTarget, ok := a.projectTrackedValueTypeAtPath(a.currentTrackedValueType(sym), poststate.Path)
	if !ok || currentTarget == nil {
		a.errorf(poststate.Position, ensureTargetUnresolvedMessage(targetName, a.currentFuncDecl.Name))
		return
	}
	switch poststate.Kind {
	case FuncPoststateKindPreserve:
		for _, widening := range a.currentConservativeCallWidenings[sym] {
			if poststatePathsOverlap(widening.Path, poststate.Path) {
				a.errorf(poststate.Position, ensurePreserveWidenedMessage(targetName, a.currentFuncDecl.Name, widening.Source))
				return
			}
		}
		originalTarget, ok := a.projectTrackedValueTypeAtPath(sym.Type, poststate.Path)
		if !ok || originalTarget == nil {
			a.errorf(poststate.Position, ensureIncomingTargetUnresolvedMessage(targetName, a.currentFuncDecl.Name))
			return
		}
		if !SameType(currentTarget, originalTarget) {
			a.errorf(poststate.Position, ensurePreserveMismatchMessage(targetName, a.currentFuncDecl.Name, currentTarget.String()))
		}
	case FuncPoststateKindNamedState:
		actualTarget, ok := poststateNamedStateCurrentType(currentTarget)
		if !ok || actualTarget == nil {
			a.errorf(poststate.Position, ensureNamedStateTargetMessage(targetName, a.currentFuncDecl.Name))
			return
		}
		base, ok := namedStateStructBase(actualTarget)
		if !ok || base == nil {
			a.errorf(poststate.Position, ensureNamedStateTargetMessage(targetName, a.currentFuncDecl.Name))
			return
		}
		desired := newNamedStateType(base.Name, base.NamedStateCases, poststate.StateCases)
		actualState, ok := namedStateCurrentArg(actualTarget)
		if desired == nil || !ok || actualState == nil || !namedStateTypeAssignable(desired, actualState) {
			a.errorf(poststate.Position, ensureNamedStateMismatchMessage(targetName, strings.Join(poststate.StateCases, " | "), a.currentFuncDecl.Name, actualTarget.String()))
		}
	case FuncPoststateKindRefState:
		actualRef, ok := poststateRefTargetType(currentTarget)
		if !ok || actualRef == nil {
			a.errorf(poststate.Position, ensureRefStateTargetMessage(targetName, a.currentFuncDecl.Name))
			return
		}
		if !refStateAssignable(poststate.RefState, actualRef.State) {
			a.errorf(poststate.Position, ensureRefStateMismatchMessage(targetName, ast.RefStateMarker(ast.RefState(poststate.RefState)), a.currentFuncDecl.Name, currentTarget.String()))
		}
	}
}

func isVoidType(t Type) bool {
	builtin, ok := t.(*BuiltinType)
	return ok && builtin.Name == "void"
}
