//go:build cgo

package backend

import (
	"regexp"
	"testing"
)

// A local copied into several caller-owned aggregates must be allocated in ONE deterministic
// caller arena. callerStorageArenasForBody used to range over a map in its fixed point, so the
// arena handed to `mk` changed between runs of the same compile (seen on the stage1 driver:
// `ProjectSystem.absolute_path` in `expand_includes`). The first aggregate mutated in source
// order (`active`, hidden arena %5) now wins every time.
func TestCallerStorageArenaChoiceIsDeterministic(t *testing.T) {
	src := `def mk(path: darray[u8]&) -> darray[u8]:
    out: mutable darray[u8] = []
    for i in 0..<path.count |out, path|:
        out.push(path[i])
    return out

def seen_in(xs: darray[u8]&, p: darray[u8]&) -> bool:
    return xs.count == p.count

def expand(path: darray[u8]&, seen: mutable darray[u8]&, active: mutable darray[u8]&, extra: mutable darray[u8]&) -> bool:
    absolute: darray[u8] = mk(path)
    if seen_in(active, absolute):
        return false
    for index in 0..<absolute.count |active, absolute|:
        active.push(absolute[index])
    for index in 0..<absolute.count |seen, absolute|:
        seen.push(absolute[index])
    for index in 0..<absolute.count |extra, absolute|:
        extra.push(absolute[index])
    return true

def main() -> i32:
    p: mutable darray[u8] = [1, 2]
    s: mutable darray[u8] = []
    a: mutable darray[u8] = []
    e: mutable darray[u8] = []
    expand(p, s, a, e)
    return s.count.i32()
`
	callRe := regexp.MustCompile(`call %DynArray__u8 @mk\([^\n]*\)`)
	for run := 0; run < 24; run++ {
		result := analyzeInlineTestSource(t, "caller_storage_order.elisa", src)
		if errs := result.Errors(); len(errs) > 0 {
			t.Fatalf("unexpected semantic errors: %v", errs)
		}
		ir, err := GenerateLLVMIR(result)
		if err != nil {
			t.Fatalf("GenerateLLVMIR: %v", err)
		}
		call := callRe.FindString(ir)
		if call != "call %DynArray__u8 @mk(ptr %path1, ptr %5)" {
			t.Fatalf("run %d: mk must receive the first-mutated aggregate's arena (%%5 = active), got %q", run, call)
		}
	}
}
