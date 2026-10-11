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
	if !strings.Contains(output, "call void @append_row") {
		t.Fatalf("missing mutating helper call:\n%s", output)
	}
}
