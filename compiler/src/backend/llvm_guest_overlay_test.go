package backend

import (
	"strings"
	"testing"
)

// docs/107 regression: a guest-overlay carrier (`GuestVAddr[L]` where L is an in-source `layout`)
// used as a LOCAL-VARIABLE type annotation must lower through the backend. The overlay layout name is
// not in the backend's NamedTypes (it lives in the semantic overlay registry), so resolving the
// carrier's type-arg fails — the backend must fall back to the uintptr pointee (the pointee is
// ABI-irrelevant; the carrier is always a raw guest address). Before the fix this produced
// `unknown type "L"` at codegen even though semantic analysis accepted it. The accessor itself must
// still lower to the byte-identical MemoryManager_ReadU64(mem, base + offset) call.
func TestGuestOverlayLocalCarrierLowers(t *testing.T) {
	src := `extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

struct MemParamOverlay layout(guest, size: 48):
    size: u64 at 0
    ext2: u64 at 40

def read_ext2(raw: uintptr, memory: uintptr) -> u64:
    mem_param: GuestVAddr[MemParamOverlay] = raw.cast[GuestVAddr[MemParamOverlay]]
    return mem_param.ext2[memory]
`
	result := parseAndAnalyzeBackendTest(t, "overlay_local.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("codegen failed (the local-carrier resolution regression): %v", err)
	}
	if !strings.Contains(output, "@MemoryManager_ReadU64") {
		t.Fatalf("expected the overlay read to lower to a MemoryManager_ReadU64 call, got:\n%s", output)
	}
	// The carrier lowers to its uintptr storage (i64), so the function takes two i64s.
	if !strings.Contains(output, "define i64 @read_ext2(i64") {
		t.Fatalf("expected read_ext2 to lower with i64 carrier params, got:\n%s", output)
	}
	// ext2 sits at offset 40 — the address arithmetic must add 40.
	if !strings.Contains(output, ", 40") {
		t.Fatalf("expected the +40 field offset in the address computation, got:\n%s", output)
	}
}
