//go:build cgo

package backend

import (
	"regexp"
	"strings"
	"testing"
)

func functionBodyForDeferTest(t *testing.T, output string, name string) string {
	t.Helper()
	bodyRe := regexp.MustCompile(`(?s)define[^@]*@` + regexp.QuoteMeta(name) + `\([^)]*\)[^{]*\{(.*?)\n\}`)
	match := bodyRe.FindStringSubmatch(output)
	if len(match) != 2 {
		t.Fatalf("expected function body for %q, got:\n%s", name, output)
	}
	return match[1]
}

func previousNonEmptyLine(lines []string, index int) string {
	for i := index - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

func TestGenerateLLVMIRFunctionDeferUsesCapturedBindingAcrossShadowing(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_defer_function_capture.llcontext", `extern sink(value: int) -> void

def keep(flag: bool) -> int:
    value: int = 10
    defer function:
        sink(value)
    if flag:
        value: int = 20
        return value
    return 0
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	body := functionBodyForDeferTest(t, output, "keep")
	if count := len(regexp.MustCompile(`alloca i64`).FindAllStringIndex(body, -1)); count < 2 {
		t.Fatalf("expected shadowed inner binding to produce a second integer alloca, got body:\n%s", body)
	}
	lines := strings.Split(body, "\n")
	foundSink := false
	for i, line := range lines {
		if !strings.Contains(line, "call void @sink(i64 ") {
			continue
		}
		prev := previousNonEmptyLine(lines, i)
		if !strings.Contains(prev, "load i64, ptr %value,") {
			t.Fatalf("expected function defer to load the outer captured binding before sink, got previous line %q in body:\n%s", prev, body)
		}
		foundSink = true
		break
	}
	if !foundSink {
		t.Fatalf("expected sink call in deferred cleanup, got body:\n%s", body)
	}
	if strings.Index(body, "call void @sink") > strings.LastIndex(body, "ret i64") {
		t.Fatalf("expected deferred sink call to appear before the return, got body:\n%s", body)
	}
}

func TestGenerateLLVMIRBlockDeferRunsBeforePoolShutdown(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_defer_block_pool_order.llcontext", parallelForConcurrencyPrelude+`
extern observe(pool: stack ThreadPool&) -> void

def keep() -> void:
	pool workers(1):
		defer block:
			observe(&workers)
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	body := functionBodyForDeferTest(t, output, "keep")
	observeIndex := strings.Index(body, "call void @observe")
	shutdownIndex := strings.Index(body, "call void @pool_shutdown")
	if observeIndex < 0 {
		t.Fatalf("expected observe call from block defer, got body:\n%s", body)
	}
	if shutdownIndex < 0 {
		t.Fatalf("expected pool shutdown cleanup call, got body:\n%s", body)
	}
	if observeIndex > shutdownIndex {
		t.Fatalf("expected block defer to run before pool shutdown, got body:\n%s", body)
	}
}
