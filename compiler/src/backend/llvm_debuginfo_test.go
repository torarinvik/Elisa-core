//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// With debug info enabled the module must carry a compile unit, a DISubprogram per
// defined function, and !dbg line locations -- the DWARF foundation an attach-model
// native debugger needs to symbolize backtraces and set source-line breakpoints.
func TestGenerateLLVMIREmitsDwarfDebugInfoWhenEnabled(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "debug_info.elisa", `
struct Point:
	x: mutable i32
	y: mutable i32

def compute(a: i32, b: i32) -> i32:
	s: i32 = a + b
	return s * 2

def main() -> i32:
	p: Point = Point(3, 4)
	return compute(p.x, p.y)
`)

	g, err := compileLLVMModuleWithTargetAndDebug(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "", true)
	if err != nil {
		t.Fatalf("compile with debug info returned error: %v", err)
	}
	defer g.dispose()
	ir := g.printModule()

	for _, want := range []string{
		"!llvm.dbg.cu",
		"DICompileUnit",
		"DISubprogram",
		"DIFile",
		"!DILocation",
		`!"Debug Info Version"`,
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("debug IR missing %q; got:\n%s", want, ir)
		}
	}
	// The defined functions must each carry a subprogram name.
	if !strings.Contains(ir, `name: "compute"`) || !strings.Contains(ir, `name: "main"`) {
		t.Fatalf("expected DISubprogram entries for compute and main, got:\n%s", ir)
	}

	// Phase 2: parameters and locals must be described with base types so a debugger
	// can read them by name.
	for _, want := range []string{
		"DILocalVariable",
		"DIBasicType",
		`name: "a"`, // parameter
		`name: "s"`, // local
		"arg: 1",    // first parameter marker
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("debug IR missing local/param metadata %q; got:\n%s", want, ir)
		}
	}
	// And a declare record must tie storage to the variable (DbgRecord or intrinsic form).
	if !strings.Contains(ir, "dbg_declare") && !strings.Contains(ir, "llvm.dbg.declare") {
		t.Fatalf("expected a dbg declare tying storage to variables; got:\n%s", ir)
	}

	// Struct types must be described as composite types with named members at offsets,
	// so aggregates (and e.g. a guest-context struct) show real fields, not blobs.
	for _, want := range []string{
		"DICompositeType",
		"DW_TAG_structure_type",
		`name: "Point"`,
		`name: "x"`, // member
		"DW_TAG_member",
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("debug IR missing struct composite metadata %q; got:\n%s", want, ir)
		}
	}
}

// Without the debug flag the module must contain no debug metadata (default path
// unchanged), so normal builds stay lean.
func TestGenerateLLVMIROmitsDwarfByDefault(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "no_debug_info.elisa", `
def main() -> i32:
	return 0
`)

	g, err := compileLLVMModuleWithTarget(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "")
	if err != nil {
		t.Fatalf("compile returned error: %v", err)
	}
	defer g.dispose()
	ir := g.printModule()
	if strings.Contains(ir, "DICompileUnit") || strings.Contains(ir, "DISubprogram") {
		t.Fatalf("expected no debug metadata without -g, got:\n%s", ir)
	}
}

// A darray must be described as a composite with items/count/capacity members so a
// debugger (and the elisa lldb pretty-printer) can show its length and elements.
func TestGenerateLLVMIRDescribesDArrayHeader(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "debug_darray.elisa", `
def use(xs: darray[i32]&) -> usize:
	return xs.count
`)
	g, err := compileLLVMModuleWithTargetAndDebug(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "", true)
	if err != nil {
		t.Fatalf("compile with debug info returned error: %v", err)
	}
	defer g.dispose()
	ir := g.printModule()
	for _, want := range []string{`name: "darray[i32]"`, `name: "items"`, `name: "count"`, `name: "capacity"`} {
		if !strings.Contains(ir, want) {
			t.Fatalf("darray debug type missing %q; got:\n%s", want, ir)
		}
	}
}
