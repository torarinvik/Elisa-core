//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRWideEnumMatchReusesScrutineeTemp(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_wide_enum_match.elisa", `
enum Wide:
    First(items: array[i64, 256])
    Second(items: array[i64, 256])
    Third(items: array[i64, 256])
    Empty

def inspect(value: Wide) -> i64:
    return match value:
        Wide.First(items): items[0]
        Wide.Second(items): items[1]
        Wide.Third(items): items[2]
        Wide.Empty: 0
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "define i64 @inspect(ptr byval(%Wide)") {
		t.Fatalf("expected wide enum parameter to use the aggregate byval ABI:\n%s", output)
	}
	if got := strings.Count(output, "match.payload.tmp = alloca %Wide"); got != 1 {
		t.Fatalf("expected one pre-dispatch wide-enum match temp, got %d:\n%s", got, output)
	}
	if got := strings.Count(output, "match.payload.tmp.memcpy"); got != 1 {
		t.Fatalf("expected one dominating wide-enum scrutinee copy, got %d:\n%s", got, output)
	}
}
