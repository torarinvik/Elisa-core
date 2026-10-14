//go:build cgo

package backend

import (
	"regexp"
	"strings"
	"testing"
)

const indexCheckMergeSrc = `extern ext_touch(x: u32) -> u32

@inline(never)
def pack3(a: darray[u8]&, i: usize) -> u32:
    return (a[i].u32() << 16) | (a[i + 1].u32() << 8) | a[i + 2].u32()

@inline(never)
def impure(a: darray[u8]&, i: usize) -> u32:
    return a[i].u32() + ext_touch(a[i + 1].u32())

@inline(never)
def single(a: darray[u8]&, i: usize) -> u32:
    return a[i].u32() + 1

def main() -> int can[Memory.Allocate, Abort.Panic]:
    a: mutable darray[u8] = []
    a.push(1)
    a.push(2)
    a.push(3)
    return (pack3(&a, 0) + impure(&a, 0) + single(&a, 0)).int()
`

var wdCheck = regexp.MustCompile(`%wd\.in_bounds\d* = icmp ult i64 %([\w.]+), `)

func blockBody(fn, label string) string {
	start := strings.Index(fn, "\n"+label+":")
	if start < 0 {
		return ""
	}
	rest := fn[start+1:]
	if end := regexp.MustCompile(`\n\n[\w.]+:`).FindStringIndex(rest); end != nil {
		return rest[:end[0]]
	}
	return rest
}

// a[i], a[i + 1], a[i + 2] in one pure expression: one merged guard (offset 2) chooses a fast path
// with no per-access checks; the slow path is the original expression with all three checks.
func TestIndexCheckMergeGuardsPureConsecutiveReads(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "index_check_merge.elisa", indexCheckMergeSrc)
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt: %v", err)
	}
	pack3 := functionBody(t, ir, "pack3")
	if !strings.Contains(pack3, "%merge.room = icmp ugt i64 %merge.count, 2") || !strings.Contains(pack3, "%merge.in_range = icmp ult i64") {
		t.Fatalf("pack3 must carry one overflow-safe merged guard for offsets 0..2:\n%s", pack3)
	}
	if !regexp.MustCompile(`br i1 %merge\.ok, label %merge\.fast, label %merge\.slow, !prof`).MatchString(pack3) {
		t.Fatalf("merged guard must branch to fast/slow with branch weights:\n%s", pack3)
	}
	if got := len(wdCheck.FindAllString(pack3, -1)); got != 3 {
		t.Fatalf("pack3 must keep exactly the original three per-access checks (slow path), got %d:\n%s", got, pack3)
	}
	if fast := blockBody(pack3, "merge.fast"); fast == "" || strings.Contains(fast, "wd.in_bounds") || strings.Contains(fast, "llvm.trap") {
		t.Fatalf("fast path must have no per-access checks:\n%s", fast)
	}
	if slow := blockBody(pack3, "merge.slow"); !strings.Contains(slow, "wd.in_bounds") {
		t.Fatalf("slow path must start with the first original check:\n%s", slow)
	}
	for _, name := range []string{"impure", "single"} {
		if body := functionBody(t, ir, name); strings.Contains(body, "merge.ok") {
			t.Fatalf("%s must not be merged (a call / a single access):\n%s", name, body)
		}
	}
}
