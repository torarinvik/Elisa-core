//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestModuleAliasPayloadConstructorAndPatterns(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "module_alias_enum.elisa", `
module Wrong:
    enum Message:
        Some(left: i64, right: i64)
        Empty
module Right:
    enum Message:
        Empty
        Some(value: i64)
using Right as R

def score(message: R::Message) -> i64:
    match message:
        R::Message.Some(value):
            return value
        R::Message.Empty:
            return 0
    return -1

def main() -> i64:
    return score(R::Message.Some(42))
`)
	if result.ModuleAliases["R"] != "Right" {
		t.Fatalf("missing canonical module alias: %v", result.ModuleAliases)
	}
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "define i64 @main") {
		t.Fatal("missing lowered main")
	}
}
