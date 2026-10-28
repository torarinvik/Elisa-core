//go:build cgo && (darwin || linux)

package backend

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

func tableUnrollFixture(trips int, qualifier, metadata string) string {
	return fmt.Sprintf(`
@table = internal constant [16 x i64] [i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11, i64 12, i64 13, i64 14, i64 15]
define i64 @test() {
entry: br label %%loop
loop:
 %%i = phi i64 [0, %%entry], [%%next, %%loop]
 %%p = getelementptr [16 x i64], ptr @table, i64 0, i64 %%i
 %%a = load %si64, ptr %%p%s
 %%b = load i64, ptr %%p%s
 %%c = load i64, ptr %%p%s
 %%d = load i64, ptr %%p%s
 %%ab = add i64 %%a, %%b
 %%cd = add i64 %%c, %%d
 %%sum = add i64 %%ab, %%cd
 %%next = add nuw nsw i64 %%i, 1
 %%done = icmp eq i64 %%next, %d
 br i1 %%done, label %%exit, label %%loop
exit: ret i64 %%sum
}
!0 = !{i64 0, i64 16}
`, qualifier, metadata, metadata, metadata, metadata, trips)
}
func TestConstantTableUnrollSafety(t *testing.T) {
	for _, tc := range []struct {
		name     string
		trips    int
		qual, md string
		want     int
	}{
		{"range", 12, "", ", !range !0", 1}, {"plain", 2, "", "", 1},
		{"overtrip", 17, "", "", 0}, {"single", 1, "", "", 0}, {"volatile", 12, "volatile ", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(tableUnrollFixture(tc.trips, tc.qual, tc.md))
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			before := g.printModule()
			got := unrollConstantTableLoops(g.module, nil)
			want := tc.want
			if !tableUnrollSupported() {
				want = 0
			}
			if got != want {
				t.Fatalf("got %d want %d", got, want)
			}
			if err := g.verify(); err != nil {
				t.Fatal(err)
			}
			after := g.printModule()
			if want == 0 && before != after {
				t.Fatal("declined module changed")
			}
			if want == 1 && strings.Contains(after, "br i1 %done") {
				t.Fatal("loop survived")
			}
		})
	}
}

func TestConstantTableUnrollAdversarial(t *testing.T) {
	data, err := os.ReadFile("testdata/table_unroll.ll")
	if err != nil {
		t.Fatal(err)
	}
	// Specialized debug metadata anywhere in the module is intentionally a
	// whole-module decline, to bound cloning of non-candidate functions.
	debug, err := parseLLVMIRForSplitHalfTest(string(data))
	if err != nil {
		t.Fatal(err)
	}
	debugBefore := debug.printModule()
	if got := unrollConstantTableLoops(debug.module, nil); got != 0 {
		t.Fatalf("debug module accepted: %d", got)
	}
	if debug.printModule() != debugBefore {
		t.Fatal("debug decline modified module")
	}
	debug.dispose()
	ir := regexp.MustCompile(`(?ms)^define i64 @debug\(\).*?^}\n`).ReplaceAllString(string(data), "")
	ir = regexp.MustCompile(`(?m)^!llvm\.(dbg\.cu|module\.flags) = .*\n`).ReplaceAllString(ir, "")
	g, err := parseLLVMIRForSplitHalfTest(ir)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	before := g.printModule()
	want := 1
	if !tableUnrollSupported() {
		want = 0
	}
	if got := unrollConstantTableLoops(g.module, nil); got != want {
		t.Fatalf("got %d want changed", got)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	after := g.printModule()
	for _, name := range []string{"positive", "trip2", "trip16"} {
		if want == 0 {
			if splitHalfAddFunctionBody(before, name) != splitHalfAddFunctionBody(after, name) {
				t.Errorf("%s changed on unsupported LLVM", name)
			}
			continue
		}
		if strings.Contains(splitHalfAddFunctionBody(after, name), "br i1 %done") {
			t.Errorf("%s loop survived", name)
		}
	}
	for _, name := range []string{"dynamic", "counter_independent", "volatile", "atomic", "extra_volatile", "extra_atomic", "side_effect", "poison_bound", "optnone", "multiple_loops", "trip17", "small_trip", "mutable", "loop_metadata", "large"} {
		if splitHalfAddFunctionBody(before, name) != splitHalfAddFunctionBody(after, name) {
			t.Errorf("%s modified despite decline", name)
		}
	}
}
func TestConstantTableUnrollDeclinesAdversarialModules(t *testing.T) {
	for _, name := range []string{"table_unroll_attempts.ll", "table_unroll_graph_budget.ll"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + name)
			if err != nil {
				t.Fatal(err)
			}
			g, err := parseLLVMIRForSplitHalfTest(string(data))
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			before := g.printModule()
			if got := unrollConstantTableLoops(g.module, nil); got != 0 {
				t.Fatalf("unexpected status %d", got)
			}
			if got := g.printModule(); got != before {
				t.Fatal("declined module changed")
			}
		})
	}
}
func TestConstantTableUnrollPolicyKey(t *testing.T) {
	key := optimizedModuleCacheKey(&llvmGenerator{}, OptimizationLevel3, "a", "b", "c")
	if !strings.Contains(key, "constant-table-unroll-v1") {
		t.Fatal("missing optimization policy in cache identity")
	}
}

func TestConstantTableUnrollRejectsOversizedGlobals(t *testing.T) {
	ir := tableUnrollFixture(12, "", "") + "\n@large = internal constant [2097153 x i8] c\"" + strings.Repeat("x", 2097153) + "\"\n"
	g, err := parseLLVMIRForSplitHalfTest(ir)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	before := g.printModule()
	if got := unrollConstantTableLoops(g.module, nil); got != 0 {
		t.Fatalf("oversized globals accepted: %d", got)
	}
	if got := g.printModule(); got != before {
		t.Fatal("oversized module changed")
	}
}

func TestConstantTableUnrollRejectsOversizedStructure(t *testing.T) {
	base := tableUnrollFixture(12, "", "")
	cases := map[string]string{
		"function_count": base + strings.Repeat("declare void @d()\n", 0) + func() string {
			var b strings.Builder
			for i := 0; i < 1025; i++ {
				fmt.Fprintf(&b, "declare void @d%d()\n", i)
			}
			return b.String()
		}(),
		"wide_alloca":    strings.Replace(base, "entry: br label %loop", "entry: %p2 = alloca {"+strings.Repeat("i8,", 256)+"i8}\n br label %loop", 1),
		"wide_gep":       strings.Replace(base, "entry: br label %loop", "entry: %p2 = getelementptr {"+strings.Repeat("i8,", 256)+"i8}, ptr null, i64 0, i32 0\n br label %loop", 1),
		"wide_byval":     base + "\ndeclare void @byval_func(ptr byval({" + strings.Repeat("i8,", 256) + "i8}))\n",
		"wide_const_gep": base + "\n@bytes = external global i8\n@offset = external global i8\n@pg = internal global ptr getelementptr ({" + strings.Repeat("i8,", 256) + "i8}, ptr @bytes, i64 ptrtoint (ptr @offset to i64), i32 0)\n",
		"oversized_shuffle_masks": base + "\ndefine void @shuffle(<65536 x i8> %a) {\nentry:\n" + func() string {
			var b strings.Builder
			for i := 0; i < 9; i++ {
				fmt.Fprintf(&b, " %%s%d = shufflevector <65536 x i8> %%a, <65536 x i8> %%a, <65536 x i32> zeroinitializer\n", i)
			}
			return b.String()
		}() + " ret void\n}\n",
		"wide_type":       base + "\n@wide = internal global {" + strings.Repeat("i8,", 256) + "i8} zeroinitializer\n",
		"large_triple":    "target triple = \"" + strings.Repeat("x", 2097153) + "\"\n" + base,
		"large_attribute": strings.Replace(base, "define i64 @test() {", "define i64 @test() #0 {", 1) + "\nattributes #0 = { \"cost\"=\"" + strings.Repeat("x", 2097153) + "\" }\n",
		"global_section":  base + "\n@sectioned = internal global i32 1, section \"custom\"\n",
		"large_metadata":  base + "\n!large = !{!1}\n!1 = !{!\"" + strings.Repeat("x", 2097153) + "\"}\n",
	}
	for name, ir := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := parseLLVMIRForSplitHalfTest(ir)
			if err != nil {
				t.Fatal(err)
			}
			defer g.dispose()
			before := g.printModule()
			if name == "wide_const_gep" && !strings.Contains(before, "getelementptr ({ i8, i8") {
				t.Fatal("constant GEP folded before test")
			}
			if got := unrollConstantTableLoops(g.module, nil); got != 0 {
				t.Fatalf("oversized structure accepted: %d", got)
			}
			if g.printModule() != before {
				t.Fatal("declined module changed")
			}
		})
	}
}

func TestConstantTableUnrollRejectsLargeFunctionMetadata(t *testing.T) {
	base := tableUnrollFixture(12, "", "")
	ir := base + "\ndefine void @other() !custom !2 { ret void }\n!2 = !{!\"" + strings.Repeat("x", 2097153) + "\"}\n"
	g, err := parseLLVMIRForSplitHalfTest(ir)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	before := g.printModule()
	if got := unrollConstantTableLoops(g.module, nil); got != 0 {
		t.Fatalf("large function metadata accepted: %d", got)
	}
	if g.printModule() != before {
		t.Fatal("declined module changed")
	}
}

func TestConstantTableUnrollWithinShuffleBudget(t *testing.T) {
	ir := tableUnrollFixture(12, "", "") + `
define void @shuffle(<65536 x i8> %a) {
entry:
 %s = shufflevector <65536 x i8> %a, <65536 x i8> %a, <65536 x i32> zeroinitializer
 ret void
}
`
	g, err := parseLLVMIRForSplitHalfTest(ir)
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	before := g.printModule()
	want := 0
	if tableUnrollSupported() {
		want = 1
	}
	if got := unrollConstantTableLoops(g.module, nil); got != want {
		t.Fatalf("within-budget shuffle status %d, want %d", got, want)
	}
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	after := g.printModule()
	if splitHalfAddFunctionBody(after, "shuffle") != splitHalfAddFunctionBody(before, "shuffle") {
		t.Fatal("unrelated shuffle function changed")
	}
	if want == 1 && strings.Contains(splitHalfAddFunctionBody(after, "test"), "br i1 %done") {
		t.Fatal("eligible loop did not unroll")
	}
	if want == 0 && before != after {
		t.Fatal("unsupported LLVM changed module")
	}
}
