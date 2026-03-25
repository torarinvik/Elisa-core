//go:build cgo

package backend

import (
	"regexp"
	"strings"
	"testing"

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
	specialized := specializeFuncType(fnType, map[string]semantic.Type{"T": result.NamedTypes["int"]})
	if specialized == nil {
		t.Fatal("expected specializeFuncType to produce a function type")
	}
	if !specialized.HasInlineMode || specialized.InlineMode != semantic.FuncInlineModeAlways {
		t.Fatalf("expected specialized helper inline metadata to be preserved, got %+v", specialized)
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
