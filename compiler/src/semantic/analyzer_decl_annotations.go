package semantic

import (
	"elisacore/src/ast"
	"strconv"
	"strings"
)

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

// validateFunctionPropertyAnnotation validates both @property and @differential.
// They share the same shape requirements (non-generic, permission-free, bool return,
// generatable scalar parameters); a @differential function is simply a @property whose
// body compares an implementation call against a reference call, fuzzed identically.
func (a *Analyzer) validateFunctionPropertyAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) bool {
	kind := annotation.Name
	if len(annotation.Args) > 1 {
		a.errorf(annotation.Position, "@%s on function %q takes at most one argument (the case count, e.g. @%s(1000))", kind, fn.Name, kind)
		return false
	}
	if len(annotation.Args) == 1 {
		if n, ok := PropertyCaseCount(annotation.Args[0]); !ok || n <= 0 {
			a.errorf(annotation.Position, "@%s on function %q expects a positive integer case count, got %q", kind, fn.Name, annotation.Args[0])
			return false
		}
	}
	if len(signature.TypeParams) > 0 || len(signature.RegionParams) > 0 || len(signature.ShapeParams) > 0 || len(signature.GenericParams) > 0 {
		a.errorf(annotation.Position, "@%s function %q must not be generic; got %s", kind, fn.Name, signature)
		return false
	}
	if !annotationAllowsDeclaredPermissions("property", signature) {
		a.errorf(annotation.Position, "@%s function %q must not require permissions; got %s", kind, fn.Name, signature)
		return false
	}
	if signature.Variadic {
		a.errorf(annotation.Position, "@%s function %q must not be variadic", kind, fn.Name)
		return false
	}
	if !IsBoolType(signature.Return) {
		a.errorf(annotation.Position, "@%s function %q must return bool, got %s", kind, fn.Name, signature.Return)
		return false
	}
	if len(signature.Params) == 0 {
		a.errorf(annotation.Position, "@%s function %q must take at least one generated parameter", kind, fn.Name)
		return false
	}
	for i, p := range signature.Params {
		if _, ok := PropertyParamTypeName(p); !ok {
			name := ""
			if i < len(signature.ExplicitParamNames) {
				name = signature.ExplicitParamNames[i]
			}
			a.errorf(annotation.Position, "@%s function %q parameter %q has type %s, which the harness cannot generate (supported: bool, int, i8/i16/i32/i64, u8/u16/u32/u64, f32/f64)", kind, fn.Name, name, p)
			return false
		}
	}
	return true
}

// LockstepStepParamName is the required name of a @lockstep stepper's final
// parameter: the number of steps to advance the state before projecting.
const LockstepStepParamName = "step"

// LockstepRefSuffix is appended to a @lockstep function name to form the name of
// its paired reference stepper. The harness drives both in lockstep and reports the
// first step index at which their projections diverge.
const LockstepRefSuffix = "__ref"

// validateFunctionLockstepAnnotation validates a @lockstep state-level differential
// oracle (docs: state-level lockstep). A @lockstep function is a *step-and-project*
// function: given fuzzed scalar seed inputs plus a final `step: <int>` parameter, it
// builds a state from the seeds, advances it `step` times, and returns an i64
// projection of the observable state. The harness pairs it with a same-signature
// reference function named `<name>__ref`, steps both from step 0 upward, and reports
// the first diverging step index (with shrunk seeds) via the @property machinery.
//
// This keeps the analyzer ordering-independent: only the lockstep function's own
// shape is checked here; the existence/signature of the `<name>__ref` partner is
// enforced naturally when the generated driver (which calls both) is compiled.
func (a *Analyzer) validateFunctionLockstepAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) bool {
	kind := "lockstep"
	if len(annotation.Args) > 1 {
		a.errorf(annotation.Position, "@%s on function %q takes at most one argument (the step count, e.g. @%s(64))", kind, fn.Name, kind)
		return false
	}
	if len(annotation.Args) == 1 {
		if n, ok := PropertyCaseCount(annotation.Args[0]); !ok || n <= 0 {
			a.errorf(annotation.Position, "@%s on function %q expects a positive integer step count, got %q", kind, fn.Name, annotation.Args[0])
			return false
		}
	}
	if len(signature.TypeParams) > 0 || len(signature.RegionParams) > 0 || len(signature.ShapeParams) > 0 || len(signature.GenericParams) > 0 {
		a.errorf(annotation.Position, "@%s function %q must not be generic; got %s", kind, fn.Name, signature)
		return false
	}
	if !annotationAllowsDeclaredPermissions("property", signature) {
		a.errorf(annotation.Position, "@%s function %q must not require permissions; got %s", kind, fn.Name, signature)
		return false
	}
	if signature.Variadic {
		a.errorf(annotation.Position, "@%s function %q must not be variadic", kind, fn.Name)
		return false
	}
	// The projection of the observable state must be a single comparable i64 scalar.
	if b, ok := signature.Return.(*BuiltinType); !ok || b.Name != "i64" {
		a.errorf(annotation.Position, "@%s function %q must return i64 (the observable-state projection), got %s", kind, fn.Name, signature.Return)
		return false
	}
	// At least one fuzzed seed parameter plus the trailing step parameter.
	if len(signature.Params) < 2 {
		a.errorf(annotation.Position, "@%s function %q must take at least one generated seed parameter plus a trailing %q parameter", kind, fn.Name, LockstepStepParamName)
		return false
	}
	last := len(signature.Params) - 1
	lastName := ""
	if last < len(signature.ExplicitParamNames) {
		lastName = signature.ExplicitParamNames[last]
	}
	if lastName != LockstepStepParamName {
		a.errorf(annotation.Position, "@%s function %q final parameter must be named %q (the step index), got %q", kind, fn.Name, LockstepStepParamName, lastName)
		return false
	}
	if b, ok := signature.Params[last].(*BuiltinType); !ok || (b.Name != "int" && b.Name != "i64" && b.Name != "u32" && b.Name != "u64") {
		a.errorf(annotation.Position, "@%s function %q step parameter %q must be int/i64/u32/u64, got %s", kind, fn.Name, LockstepStepParamName, signature.Params[last])
		return false
	}
	// Every seed parameter (all but the step) must be a generatable scalar.
	for i := 0; i < last; i++ {
		if _, ok := PropertyParamTypeName(signature.Params[i]); !ok {
			name := ""
			if i < len(signature.ExplicitParamNames) {
				name = signature.ExplicitParamNames[i]
			}
			a.errorf(annotation.Position, "@%s function %q seed parameter %q has type %s, which the harness cannot generate (supported: bool, int, i8/i16/i32/i64, u8/u16/u32/u64, f32/f64)", kind, fn.Name, name, signature.Params[i])
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
	for i, param := range a.readOnlyExpandedFuncDeclParams(fn) {
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
	case "test", "bench", "property", "differential", "lockstep", "fixture", "skip", "inline", "fast_math", "norecurse", "hot", "cold", "callconv", "guard_nonnull", "guard_variant", "internal", "main_thread", "init", "async_entry", "segment_agnostic", "segment_establishing", "segment_transition", "reentrant_safe", "deprecated", "noalloc", "inbounds", "nolock", "link_name":
		return true
	case "boundary_pointer_args":
		return true
	default:
		return false
	}
}
