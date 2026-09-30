//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// A returned view of a local's backing (`d.as_sview()`) or a returned element popped out of a local
// container shares the local's storage, so the local must be allocated in the caller-visible
// (adopted) region rather than in the function's scratch arena that is freed at return.
func TestAdoptedEscapeKeepsReceiverOfReturnedViewOrElementOutOfScratch(t *testing.T) {
	cases := map[string]string{
		"as_sview_return": `def mk() -> sview:
    can Memory.Allocate:
        d: mutable dstr = [65, 66]
        return d.as_sview()
`,
		"as_sview_via_local": `def mk() -> sview:
    can Memory.Allocate:
        d: mutable dstr = [65, 66]
        s: sview = d.as_sview()
        return s
`,
		"pop_element_return": `def mk() -> darray[u8]:
    can Memory.Allocate:
        xs: mutable darray[darray[u8]] = [[65, 66]]
        inner: darray[u8] = xs.pop()
        return inner
`,
	}
	for name, src := range cases {
		result := parseAndAnalyzeBackendTest(t, "backend_adopted_receiver_"+name+".elisa", src)
		output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
		if err != nil {
			t.Fatalf("%s: GenerateLLVMIRWithOpt returned error: %v", name, err)
		}
		if strings.Contains(output, "darray.literal.alloc = call ptr @arena_alloc(ptr %adopted.scratch") {
			t.Errorf("%s: literal backing of a returned view/element was allocated in adopted.scratch", name)
		}
	}
}
