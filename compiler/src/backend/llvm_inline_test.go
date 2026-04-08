//go:build cgo

package backend

import (
	"regexp"
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/parser"
	"llcontext/src/semantic"
)

func analyzeInlineTestSource(t *testing.T, filename string, src string) *semantic.Result {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		t.Fatalf("lexer errors:\n%s", strings.Join(errs, "\n"))
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parse errors:\n%s", strings.Join(errs, "\n"))
	}
	return semantic.Analyze(file)
}

func functionAttributeGroupForTest(t *testing.T, output string, name string) string {
	t.Helper()
	defineRe := regexp.MustCompile(`define[^@]*@` + regexp.QuoteMeta(name) + `\([^)]*\)\s+#([0-9]+)`)
	match := defineRe.FindStringSubmatch(output)
	if len(match) != 2 {
		t.Fatalf("expected function %q to have an attribute group, got:\n%s", name, output)
	}
	attrsRe := regexp.MustCompile(`attributes #` + regexp.QuoteMeta(match[1]) + ` = \{([^}]*)\}`)
	attrs := attrsRe.FindStringSubmatch(output)
	if len(attrs) != 2 {
		t.Fatalf("expected attribute group #%s for function %q, got:\n%s", match[1], name, output)
	}
	return attrs[1]
}

func functionBranchWeightsForTest(t *testing.T, output string, name string) (string, string) {
	t.Helper()
	bodyRe := regexp.MustCompile(`(?s)define[^@]*@` + regexp.QuoteMeta(name) + `\([^)]*\)[^{]*\{(.*?)\n\}`)
	bodyMatch := bodyRe.FindStringSubmatch(output)
	if len(bodyMatch) != 2 {
		t.Fatalf("expected function body for %q, got:\n%s", name, output)
	}
	branchRe := regexp.MustCompile(`br i1 [^\n]*, !prof !([0-9]+)`)
	branchMatch := branchRe.FindStringSubmatch(bodyMatch[1])
	if len(branchMatch) != 2 {
		t.Fatalf("expected conditional branch metadata for %q, got body:\n%s\nIR:\n%s", name, bodyMatch[1], output)
	}
	metadataRe := regexp.MustCompile(`!` + regexp.QuoteMeta(branchMatch[1]) + ` = !\{!"branch_weights", i32 ([0-9]+), i32 ([0-9]+)\}`)
	metadataMatch := metadataRe.FindStringSubmatch(output)
	if len(metadataMatch) != 3 {
		t.Fatalf("expected branch_weights metadata !%s for %q, got:\n%s", branchMatch[1], name, output)
	}
	return metadataMatch[1], metadataMatch[2]
}

func TestAnalyzeInlineAnnotationSetsFunctionMetadata(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "inline_semantics.llcontext", `@inline(always)
def helper[T](value: T) -> T:
	return value
`)
	sym, ok := result.GlobalScope.Lookup("helper")
	if !ok {
		t.Fatal("expected helper to be defined")
	}
	fnType, ok := sym.Type.(*semantic.FuncType)
	if !ok || fnType == nil {
		t.Fatalf("expected helper to resolve to semantic.FuncType, got %#v", sym.Type)
	}
	if !fnType.HasInlineMode || fnType.InlineMode != semantic.FuncInlineModeAlways {
		t.Fatalf("expected helper inline metadata to record always, got %+v", fnType)
	}
	specialized := specializeFuncType(fnType, map[string]semantic.Type{"T": result.NamedTypes["int"]}, nil)
	if specialized == nil {
		t.Fatal("expected specializeFuncType to produce a function type")
	}
	if !specialized.HasInlineMode || specialized.InlineMode != semantic.FuncInlineModeAlways {
		t.Fatalf("expected specialized helper inline metadata to be preserved, got %+v", specialized)
	}
}

func TestAnalyzeNoRecurseAnnotationSetsFunctionMetadata(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "norecurse_semantics.llcontext", `@norecurse
def helper[T](value: T) -> T:
	return value
`)
	sym, ok := result.GlobalScope.Lookup("helper")
	if !ok {
		t.Fatal("expected helper to be defined")
	}
	fnType, ok := sym.Type.(*semantic.FuncType)
	if !ok || fnType == nil {
		t.Fatalf("expected helper to resolve to semantic.FuncType, got %#v", sym.Type)
	}
	if !fnType.HasNoRecurse {
		t.Fatalf("expected helper norecurse metadata to be recorded, got %+v", fnType)
	}
	specialized := specializeFuncType(fnType, map[string]semantic.Type{"T": result.NamedTypes["int"]}, nil)
	if specialized == nil {
		t.Fatal("expected specializeFuncType to produce a function type")
	}
	if !specialized.HasNoRecurse {
		t.Fatalf("expected specialized helper norecurse metadata to be preserved, got %+v", specialized)
	}
}

func TestAnalyzeNoRecurseAnnotationRejectsArguments(t *testing.T) {
	result := analyzeInlineTestSource(t, "norecurse_invalid_args.llcontext", `@norecurse(always)
def helper() -> int:
	return 1
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, `@norecurse on function "helper" does not take arguments`) {
		t.Fatalf("expected invalid norecurse argument diagnostic, got:\n%s", errText)
	}
}

func TestAnalyzeInlineAnnotationRejectsUnsupportedMode(t *testing.T) {
	result := analyzeInlineTestSource(t, "inline_invalid_mode.llcontext", `@inline(sometimes)
def helper() -> int:
	return 1
`)
	errs := result.Errors()
	if len(errs) == 0 {
		t.Fatal("expected semantic error for unsupported inline mode")
	}
	if !strings.Contains(strings.Join(errs, "\n"), `@inline on function "helper" uses unsupported mode "sometimes"`) {
		t.Fatalf("expected unsupported inline mode diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestGenerateLLVMIRAppliesAlwaysInlineAttributeFromAnnotation(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_inline_always.llcontext", `@inline(always)
def helper(value: int) -> int:
	return value + 1
`)
	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	attrs := functionAttributeGroupForTest(t, output, "helper")
	if !strings.Contains(attrs, "alwaysinline") {
		t.Fatalf("expected helper to carry alwaysinline, got attributes {%s}\nIR:\n%s", attrs, output)
	}
	if strings.Contains(attrs, "noinline") {
		t.Fatalf("expected helper not to carry noinline, got attributes {%s}\nIR:\n%s", attrs, output)
	}
}

func TestGenerateLLVMIRAppliesNoRecurseAttributeFromAnnotation(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_norecurse.llcontext", `@norecurse
def helper(value: int) -> int:
	return value + 1
`)
	g, err := compileLLVMModule(result, OptimizationLevel2, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	attrs := functionAttributeGroupForTest(t, output, "helper")
	if !strings.Contains(attrs, "norecurse") {
		t.Fatalf("expected helper to carry norecurse, got attributes {%s}\nIR:\n%s", attrs, output)
	}
}

func TestGenerateLLVMIRAppliesNoInlineAttributeFromAnnotation(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_inline_never.llcontext", `def entry() -> int:
	return helper()

@inline(never)
def helper() -> int:
	return 1
`)
	g, err := compileLLVMModule(result, OptimizationLevel2, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	attrs := functionAttributeGroupForTest(t, output, "helper")
	if !strings.Contains(attrs, "noinline") {
		t.Fatalf("expected helper to carry noinline, got attributes {%s}\nIR:\n%s", attrs, output)
	}
	if strings.Contains(attrs, "alwaysinline") {
		t.Fatalf("expected explicit @inline(never) to suppress alwaysinline, got attributes {%s}\nIR:\n%s", attrs, output)
	}
}

func TestAnalyzeHotAnnotationSetsFunctionMetadata(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "hot_semantics.llcontext", `@hot
def helper[T](value: T) -> T:
	return value
`)
	sym, ok := result.GlobalScope.Lookup("helper")
	if !ok {
		t.Fatal("expected helper to be defined")
	}
	fnType, ok := sym.Type.(*semantic.FuncType)
	if !ok || fnType == nil {
		t.Fatalf("expected helper to resolve to semantic.FuncType, got %#v", sym.Type)
	}
	if !fnType.HasTemperatureMode || fnType.TemperatureMode != semantic.FuncTemperatureModeHot {
		t.Fatalf("expected helper temperature metadata to record hot, got %+v", fnType)
	}
	specialized := specializeFuncType(fnType, map[string]semantic.Type{"T": result.NamedTypes["int"]}, nil)
	if specialized == nil {
		t.Fatal("expected specializeFuncType to produce a function type")
	}
	if !specialized.HasTemperatureMode || specialized.TemperatureMode != semantic.FuncTemperatureModeHot {
		t.Fatalf("expected specialized helper temperature metadata to be preserved, got %+v", specialized)
	}
}

func TestInferTypeBindingsFromFuncTypesPrefersSpecializedCalleeSignature(t *testing.T) {
	sharedGate := &semantic.StructType{Name: "SharedGate"}
	i64Type := &semantic.BuiltinType{Name: "i64"}
	voidType := &semantic.BuiltinType{Name: "void"}
	base := &semantic.FuncType{
		Name: "ctx_concurrency_work1_new",
		GenericParams: []ast.GenericParam{
			{Kind: ast.GenericParamType, Name: "A"},
			{Kind: ast.GenericParamType, Name: "R"},
		},
		Params: []semantic.Type{
			&semantic.FuncType{
				Name:   "func",
				Params: []semantic.Type{&semantic.TypeParamType{Name: "A"}},
				Return: &semantic.TypeParamType{Name: "R"},
			},
			&semantic.TypeParamType{Name: "A"},
		},
		Return: voidType,
	}
	actual := &semantic.FuncType{
		Name: "ctx_concurrency_work1_new",
		Params: []semantic.Type{
			&semantic.FuncType{
				Name:   "runtime_carrier_worker",
				Params: []semantic.Type{sharedGate},
				Return: i64Type,
			},
			sharedGate,
		},
		Return: voidType,
	}
	bindings := inferTypeBindingsFromFuncTypes(base, actual)
	if got := bindings["A"]; got == nil || !semantic.SameType(got, sharedGate) {
		t.Fatalf("expected A to bind to SharedGate, got %#v", got)
	}
	if got := bindings["R"]; got == nil || !semantic.SameType(got, i64Type) {
		t.Fatalf("expected R to bind to i64, got %#v", got)
	}
}

func TestAnalyzeFunctionTemperatureRejectsArguments(t *testing.T) {
	result := analyzeInlineTestSource(t, "cold_invalid_args.llcontext", `@cold(always)
def helper() -> int:
	return 1
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, `@cold on function "helper" does not take arguments`) {
		t.Fatalf("expected invalid cold argument diagnostic, got:\n%s", errText)
	}
}

func TestAnalyzeFunctionTemperatureRejectsConflictingModes(t *testing.T) {
	result := analyzeInlineTestSource(t, "temperature_conflict.llcontext", `@hot
@cold
def helper() -> int:
	return 1
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, `@cold on function "helper" conflicts with existing @hot annotation`) {
		t.Fatalf("expected conflicting temperature diagnostic, got:\n%s", errText)
	}
}

func TestGenerateLLVMIRAppliesHotAttributeFromAnnotation(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_hot.llcontext", `@hot
def helper(value: int) -> int:
	return value + 1
`)
	g, err := compileLLVMModule(result, OptimizationLevel2, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	attrs := functionAttributeGroupForTest(t, output, "helper")
	if !strings.Contains(attrs, "hot") {
		t.Fatalf("expected helper to carry hot, got attributes {%s}\nIR:\n%s", attrs, output)
	}
	if strings.Contains(attrs, "cold") {
		t.Fatalf("expected helper not to carry cold, got attributes {%s}\nIR:\n%s", attrs, output)
	}
}

func TestGenerateLLVMIRAppliesColdAttributeFromAnnotation(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_cold.llcontext", `@cold
def helper(value: int) -> int:
	return value + 1
`)
	g, err := compileLLVMModule(result, OptimizationLevel2, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	attrs := functionAttributeGroupForTest(t, output, "helper")
	if !strings.Contains(attrs, "cold") {
		t.Fatalf("expected helper to carry cold, got attributes {%s}\nIR:\n%s", attrs, output)
	}
	if strings.Contains(attrs, "hot") {
		t.Fatalf("expected helper not to carry hot, got attributes {%s}\nIR:\n%s", attrs, output)
	}
}

func TestGenerateLLVMIRPropagatesHotAttributeToExportWrapper(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_hot_export.llcontext", `@hot
def helper(value: int) -> int:
	return value + 1

export func public_helper(value: int) -> int = helper
`)
	g, err := compileLLVMModule(result, OptimizationLevel2, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	attrs := functionAttributeGroupForTest(t, output, "public_helper")
	if !strings.Contains(attrs, "hot") {
		t.Fatalf("expected export wrapper to carry hot, got attributes {%s}\nIR:\n%s", attrs, output)
	}
}

func TestGenerateLLVMIRPropagatesNoRecurseAttributeToExportWrapper(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_norecurse_export.llcontext", `@norecurse
def helper(value: int) -> int:
	return value + 1

export func public_helper(value: int) -> int = helper
`)
	g, err := compileLLVMModule(result, OptimizationLevel2, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	attrs := functionAttributeGroupForTest(t, output, "public_helper")
	if !strings.Contains(attrs, "norecurse") {
		t.Fatalf("expected export wrapper to carry norecurse, got attributes {%s}\nIR:\n%s", attrs, output)
	}
}

func TestGenerateLLVMIRAppliesLikelyBranchWeightsToIf(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_if_likely.llcontext", `def helper(value: bool) -> int:
	if likely value:
		return 1
	return 0
`)
	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	trueWeight, falseWeight := functionBranchWeightsForTest(t, output, "helper")
	if trueWeight != "2000" || falseWeight != "1" {
		t.Fatalf("expected likely branch weights 2000/1, got %s/%s\nIR:\n%s", trueWeight, falseWeight, output)
	}
}

func TestGenerateLLVMIRAppliesUnlikelyBranchWeightsToWhile(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_while_unlikely.llcontext", `def helper(value: bool) -> int:
	total: mutable int = 0
	while unlikely value:
		total <- total + 1
		return total
	return total
`)
	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	trueWeight, falseWeight := functionBranchWeightsForTest(t, output, "helper")
	if trueWeight != "1" || falseWeight != "2000" {
		t.Fatalf("expected unlikely branch weights 1/2000, got %s/%s\nIR:\n%s", trueWeight, falseWeight, output)
	}
}
