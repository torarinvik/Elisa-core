package semantic

import (
	"strings"
	"testing"
)

// A C API hands opaque handles back as raw `void*` out-cells. `.cast` moves a raw reference
// to and from the handle type under Unsafe.PointerCast; it is not a way to turn one handle
// type into another.
const opaqueHandleCastSource = `extern LLVMModuleRef
extern LLVMContextRef

def from_raw(raw: mutable void&) -> LLVMModuleRef:
    can Unsafe.PointerCast:
        return raw.cast[LLVMModuleRef]

def to_raw(module: LLVMModuleRef) -> void&:
    can Unsafe.PointerCast:
        return module.cast[void&]
`

func TestOpaqueHandleCastFromRawReference(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "opaque_handle_cast.elisa", opaqueHandleCastSource)
	for _, e := range result.Errors() {
		t.Errorf("granted handle cast rejected: %s", e)
	}
}

func TestOpaqueHandleCastNeedsPointerCastGrant(t *testing.T) {
	source := strings.ReplaceAll(opaqueHandleCastSource, "    can Unsafe.PointerCast:\n        return", "    return")
	result := analyzeTreeTestSourceWithSemanticErrors(t, "opaque_handle_cast_ungranted.elisa", source)
	diagnostics := strings.Join(append(result.Errors(), result.Warnings()...), "\n")
	if got := strings.Count(diagnostics, "pointer cast requires"); got != 2 {
		t.Errorf("want both ungranted handle casts reported, got:\n%s", diagnostics)
	}
}

func TestOpaqueHandleCastBetweenHandlesRejected(t *testing.T) {
	source := opaqueHandleCastSource + `
def retag(context: LLVMContextRef) -> LLVMModuleRef:
    can Unsafe.PointerCast:
        return context.cast[LLVMModuleRef]
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "opaque_handle_retag.elisa", source)
	if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, "invalid cast from LLVMContextRef to LLVMModuleRef") {
		t.Errorf("handle-to-handle cast accepted, got:\n%s", got)
	}
}
