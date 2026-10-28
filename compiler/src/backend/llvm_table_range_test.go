//go:build cgo && (darwin || linux)

package backend

import (
	"fmt"
	"strings"
	"testing"
)

func TestConstantTableRangeSafety(t *testing.T) {
	const ir = `target datalayout = "e-p:64:64-i64:64-n32:64-S128"
@good = internal constant [4 x i64] [i64 0, i64 3, i64 9, i64 15]
@mutable = internal global [2 x i64] [i64 0, i64 1]
@external = constant [2 x i64] [i64 0, i64 1]
@external_init = internal externally_initialized constant [2 x i64] [i64 0, i64 1]
@undef = internal constant [2 x i64] [i64 0, i64 undef]
@poison = internal constant [2 x i64] [i64 0, i64 poison]
@max = internal constant [2 x i64] [i64 0, i64 -1]
@odd = internal constant [2 x i24] [i24 0, i24 1]
define i64 @good_load(i64 %i) {
 %p = getelementptr inbounds [4 x i64], ptr @good, i64 0, i64 %i
 %v = load i64, ptr %p
 ret i64 %v
}
define i64 @noninbounds(i64 %i) {
 %p = getelementptr [4 x i64], ptr @good, i64 0, i64 %i
 %v = load i64, ptr %p
 ret i64 %v
}
define i64 @partial(i64 %i) {
 %p = getelementptr inbounds [4 x i64], ptr @good, i64 0, i64 %i
 %v = load i32, ptr %p
 %w = zext i32 %v to i64
 ret i64 %w
}
define i64 @existing(i64 %i) {
 %p = getelementptr inbounds [4 x i64], ptr @good, i64 0, i64 %i
 %v = load i64, ptr %p, !range !0
 ret i64 %v
}
define i64 @volatile_load(i64 %i) {
 %p = getelementptr inbounds [4 x i64], ptr @good, i64 0, i64 %i
 %v = load volatile i64, ptr %p
 ret i64 %v
}
define i64 @atomic_load(i64 %i) {
 %p = getelementptr inbounds [4 x i64], ptr @good, i64 0, i64 %i
 %v = load atomic i64, ptr %p monotonic, align 8
 ret i64 %v
}
` + tableRangeNegativeFunctions + `
!0 = !{i64 0, i64 4}
`
	g, err := parseLLVMIRForSplitHalfTest(ir)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := inferConstantTableRanges(g.module); n != 1 {
		t.Fatalf("annotated %d loads, want 1", n)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	output := g.printModule()
	if !strings.Contains(splitHalfAddFunctionBody(output, "good_load"), "!range") || !strings.Contains(output, "i64 0, i64 16") {
		t.Fatal("missing expected table range")
	}
	for _, name := range []string{"noninbounds", "partial", "volatile_load", "atomic_load", "mutable_load", "external_load", "external_init_load", "undef_load", "poison_load", "max_load", "odd_load"} {
		if strings.Contains(splitHalfAddFunctionBody(output, name), "!range") {
			t.Errorf("%s received range metadata", name)
		}
	}
	if n := inferConstantTableRanges(g.module); n != 0 {
		t.Fatalf("repeated pass changed %d loads", n)
	}
}

const tableRangeNegativeFunctions = `
define i64 @mutable_load(i64 %i) { %p = getelementptr inbounds [2 x i64], ptr @mutable, i64 0, i64 %i
 %v = load i64, ptr %p
 ret i64 %v
}
define i64 @external_load(i64 %i) { %p = getelementptr inbounds [2 x i64], ptr @external, i64 0, i64 %i
 %v = load i64, ptr %p
 ret i64 %v
}
define i64 @external_init_load(i64 %i) { %p = getelementptr inbounds [2 x i64], ptr @external_init, i64 0, i64 %i
 %v = load i64, ptr %p
 ret i64 %v
}
define i64 @undef_load(i64 %i) { %p = getelementptr inbounds [2 x i64], ptr @undef, i64 0, i64 %i
 %v = load i64, ptr %p
 ret i64 %v
}
define i64 @poison_load(i64 %i) { %p = getelementptr inbounds [2 x i64], ptr @poison, i64 0, i64 %i
 %v = load i64, ptr %p
 ret i64 %v
}
define i64 @max_load(i64 %i) { %p = getelementptr inbounds [2 x i64], ptr @max, i64 0, i64 %i
 %v = load i64, ptr %p
 ret i64 %v
}
define i64 @odd_load(i64 %i) { %p = getelementptr inbounds [2 x i24], ptr @odd, i64 0, i64 %i
 %v = load i24, ptr %p
 %r = zext i24 %v to i64
 ret i64 %r
}
`

func TestConstantTableRangeCachePolicy(t *testing.T) {
	normal := optimizedModuleCacheKey(&llvmGenerator{}, OptimizationLevel3, "a", "b", "c")
	debug := optimizedModuleCacheKey(&llvmGenerator{emitDebugInfo: true}, OptimizationLevel3, "a", "b", "c")
	if !strings.Contains(normal, "constant-table-range-v2-late") || normal == debug {
		t.Fatal("cache key does not distinguish table-range policy")
	}
}

func TestConstantTableRangeBudgetAndMissingLayout(t *testing.T) {
	const body = `
@large = internal constant [4097 x i64] zeroinitializer
@good = internal constant [2 x i64] [i64 1, i64 2]
define i64 @large_load(i64 %i) {
 %p = getelementptr inbounds [4097 x i64], ptr @large, i64 0, i64 %i
 %v = load i64, ptr %p
 ret i64 %v
}
define i64 @good_load(i64 %i) {
 %p = getelementptr inbounds [2 x i64], ptr @good, i64 0, i64 %i
 %v = load i64, ptr %p
 ret i64 %v
}
`
	for _, tc := range []struct {
		name, layout string
		want         int
	}{
		{"missing layout", "", 0},
		{"oversized table", "target datalayout = \"e-p:64:64-i64:64-n32:64-S128\"\n", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(tc.layout + body)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			if n := inferConstantTableRanges(g.module); n != tc.want {
				t.Fatalf("annotated %d, want %d", n, tc.want)
			}
			if strings.Contains(splitHalfAddFunctionBody(g.printModule(), "large_load"), "!range") {
				t.Fatal("oversized table was analyzed")
			}
		})
	}
}

func TestConstantTableRangeByteGEPAndLoopMetadata(t *testing.T) {
	const ir = `target datalayout = "e-p:64:64-i64:64-n32:64-S128"
@table = internal constant [4 x i64] [i64 0, i64 3, i64 9, i64 15]
define i64 @byte_gep(i64 %i) {
 %p = getelementptr inbounds [8 x i8], ptr @table, i64 %i
 %q = getelementptr inbounds i8, ptr %p, i64 8
 %v = load i64, ptr %q
 ret i64 %v
}
define i64 @misaligned_gep(i64 %i) {
 %p = getelementptr inbounds [8 x i8], ptr @table, i64 %i
 %q = getelementptr inbounds i8, ptr %p, i64 1
 %v = load i64, ptr %q
 ret i64 %v
}
define i64 @dynamic_byte_gep(i64 %i) {
 %p = getelementptr inbounds i8, ptr @table, i64 %i
 %v = load i64, ptr %p
 ret i64 %v
}
define i64 @marked_loop(i64 %i) {
entry:
 %p = getelementptr inbounds [8 x i8], ptr @table, i64 %i
 %v = load i64, ptr %p
 br label %loop
loop:
 br i1 false, label %loop, label %exit, !llvm.loop !0
exit:
 ret i64 %v
}
!0 = distinct !{!0, !1}
!1 = !{!"elisa.autovec.expected"}
`
	g, err := parseLLVMIRForSplitHalfTest(ir)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := inferConstantTableRanges(g.module); n != 1 {
		t.Fatalf("annotated %d loads, want 1", n)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	result := g.printModule()
	if !strings.Contains(splitHalfAddFunctionBody(result, "byte_gep"), "!range") {
		t.Fatal("aligned GEP was not annotated")
	}
	for _, name := range []string{"misaligned_gep", "dynamic_byte_gep", "marked_loop"} {
		if strings.Contains(splitHalfAddFunctionBody(result, name), "!range") {
			t.Errorf("%s unexpectedly annotated", name)
		}
	}
	if !strings.Contains(splitHalfAddFunctionBody(result, "marked_loop"), "!llvm.loop") {
		t.Fatal("loop metadata was lost")
	}
}

func TestConstantTableRangeDiscoveryBudgetFailsClosed(t *testing.T) {
	var ir strings.Builder
	ir.WriteString(`target datalayout = "e-p:64:64-i64:64-n32:64-S128"
@table = internal constant [2 x i64] [i64 0, i64 1]
define i64 @large(i64 %i) {
 %p = getelementptr inbounds [8 x i8], ptr @table, i64 %i
 %v = load i64, ptr %p
`)
	for i := 0; i < 32768; i++ {
		fmt.Fprintf(&ir, " %%x%d = add i64 %%i, %d\n", i, i)
	}
	ir.WriteString(" ret i64 %v\n}\n")
	g, err := parseLLVMIRForSplitHalfTest(ir.String())
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if n := inferConstantTableRanges(g.module); n != 0 {
		t.Fatalf("partially scanned function annotated %d loads", n)
	}
	if strings.Contains(splitHalfAddFunctionBody(g.printModule(), "large"), "!range") {
		t.Fatal("budget exhaustion did not fail closed")
	}
}
