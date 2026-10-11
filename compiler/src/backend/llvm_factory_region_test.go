//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRFactoryDeclaredRegion(t *testing.T) {
	src := `struct Rows:
    values: mutable darray[i64] = []
struct Table:
    rows: mutable Rows = Rows{}
def make_table[@r]() -> Table @r:
    return Table{}
def append_row(table: mutable Table&) -> void:
    can Memory.Allocate:
        table.rows.values.push(1)
def main() -> i64:
    region r(4096):
        table: mutable Table = make_table()
        append_row(&table) can Memory.Allocate
        return 0
`
	result := parseAndAnalyzeBackendTest(t, "factory_declared_region.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("factory-owned region lowering failed: %v", err)
	}
	if strings.Contains(output, "<null operand!") {
		t.Fatalf("factory region produced invalid LLVM operand:\n%s", output)
	}
	// Both calls must use the scoped owner, and growth must consume the helper's
	// hidden arena argument rather than an ambient/global fallback.
	for _, operand := range []string{
		"call %Table @make_table(ptr %r)",
		"call void @append_row(ptr %table, ptr %r)",
		"call ptr @arena_alloc(ptr %1,",
		"call ptr @arena_realloc(ptr %1,",
	} {
		if !strings.Contains(output, operand) {
			t.Fatalf("missing caller-owner arena routing %q:\n%s", operand, output)
		}
	}
	if !strings.Contains(output, "call void @append_row") {
		t.Fatalf("missing mutating helper call:\n%s", output)
	}
}
