//go:build cgo

package backend

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The darray "hdr"/"elt" alias-scope pair (see aliasSafeElementPtrs) covers the bounds-guard COUNT
// load as well as the data-pointer load, bool elements, and fields of plain structs whose fields
// are all scalars. A struct carrying a darray field must stay untagged: its element buffer holds
// darray headers.
const darrayAliasScopeExtSrc = `struct Body:
    x: mutable f64
    v: mutable f64
    alive: mutable bool

struct Holder:
    n: mutable i64
    items: mutable darray[i64]

@inline(never)
def flip(flags: mutable darray[bool]&, i: usize) -> void:
    flags[i] <- not flags[i]

@inline(never)
def nudge(bodies: mutable darray[Body]&, i: usize) -> void:
    bodies[i].v <- bodies[i].v + bodies[i].x

@inline(never)
def bump(hs: mutable darray[Holder]&, i: usize) -> void:
    hs[i].n <- hs[i].n + 1

def driver() -> void:
    flags: mutable darray[bool] = []
    flags.push(true)
    flip(&flags, 0)
    bodies: mutable darray[Body] = []
    bodies.push(Body{x: 1.0, v: 2.0, alive: true})
    nudge(&bodies, 0)
    hs: mutable darray[Holder] = []
    hs.push(Holder{n: 1, items: []})
    bump(&hs, 0)
`

func functionBody(t *testing.T, ir, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?s)define [^\n]*@` + regexp.QuoteMeta(name) + `\(.*?\n}\n`)
	body := re.FindString(ir)
	if body == "" {
		t.Fatalf("function %s not found in IR:\n%s", name, ir)
	}
	return body
}

func TestDarrayAliasScopeCoversCountBoolAndScalarStructs(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "darray_alias_scope_ext.elisa", darrayAliasScopeExtSrc)
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt error: %v", err)
	}
	countLoad := regexp.MustCompile(`load i64, ptr %wd\.count\.ptr[^\n]*!alias\.scope`)
	flip := functionBody(t, ir, "flip")
	if !countLoad.MatchString(flip) {
		t.Fatalf("bounds-guard count load must carry the hdr alias scope:\n%s", flip)
	}
	if !regexp.MustCompile(`(load|store) i1[^\n]*!alias\.scope`).MatchString(flip) {
		t.Fatalf("bool element access must carry the elt alias scope:\n%s", flip)
	}
	nudge := functionBody(t, ir, "nudge")
	if !regexp.MustCompile(`store double[^\n]*!alias\.scope[^\n]*!noalias`).MatchString(nudge) {
		t.Fatalf("scalar-struct field store must carry elt scope + hdr noalias:\n%s", nudge)
	}
	bump := functionBody(t, ir, "bump")
	for _, line := range strings.Split(bump, "\n") {
		if strings.Contains(line, "store i64") && strings.Contains(line, "!alias.scope") {
			t.Fatalf("a struct holding a darray must not be tagged elt (its buffer holds headers):\n%s", line)
		}
	}
}

func TestOptimizationAAPipelineIncludesScevAAInBothPaths(t *testing.T) {
	if !strings.Contains(optimizationAAPipeline, "scev-aa") || !strings.Contains(optimizationAAPipeline, "basic-aa") {
		t.Fatalf("optimizationAAPipeline lost a required analysis: %q", optimizationAAPipeline)
	}
	src, err := os.ReadFile("parallel_emit.c")
	if err != nil {
		t.Fatalf("read parallel_emit.c: %v", err)
	}
	if !strings.Contains(string(src), `LLVMPassBuilderOptionsSetAAPipeline(options, "`+optimizationAAPipeline+`")`) {
		t.Fatalf("parallel_emit.c part_optimize must use the same AA pipeline %q", optimizationAAPipeline)
	}
}
