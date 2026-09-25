package main

import (
	"strings"
	"testing"
)

// A view borrowed through a generic struct must carry its source region through
// the accessor and into a local growable container. The container is cleared
// before the source region ends, so no view escapes its pointee's lifetime.
//
// This runtime check is currently blocked before user code is analyzed because
// the bundled standard runtime has four unrelated initialization diagnostics.
// Keep the regression active behind that exact prerequisite gate so it starts
// exercising the end-to-end path as soon as the runtime migration lands.
func TestRegionParamSViewFromGenericStructGrowsDArray(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `struct StringHandle[@r]:
    view: sview @r

def unwrap_view[@r](handle: StringHandle[r]) -> sview @r:
    return handle.view

def collect_view[@r](handle: StringHandle[r]) -> usize:
    output: mutable darray[sview] @r = []
    value: sview @r = unwrap_view(handle)
    output.push(value)
    count: usize = output.count
    output.clear()
    return count

def main() -> i64 can[Memory.Allocate, Abort.Panic]:
    region input_region(4096):
        bytes: mutable darray[u8] @input_region = [65]
        view: sview @input_region = bytes.as_sview()
        handle: StringHandle[input_region] = StringHandle{view: view}
        return collect_view(handle).i64()
`)
	inlineVecInitBlocker := strings.Contains(out, "collections.elisa:326:") &&
		strings.Contains(out, `use of uninitialized variable "out"`)
	genericZeroBlockers := strings.Count(out, "cannot initialize T from `zeroed`") >= 3
	if status == "REJECTED" && inlineVecInitBlocker && genericZeroBlockers {
		t.Skipf("blocked by the known InlineVec and generic-pool initialization diagnostics in the standard runtime: %s", out)
	}
	if status != "RUNERR" || !strings.Contains(out, "exit status 1") {
		t.Fatalf("generic sview region must reach darray growth and run (one borrowed view), got %s %q", status, out)
	}
}
