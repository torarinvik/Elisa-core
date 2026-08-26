package semantic

import (
	"runtime"
	"testing"
)

// An explicitly-given triple must never inherit the BUILD MACHINE's os or arch.
// Before this was fixed, `-target-triple wasm32-unknown-wasi` reported the host's
// values, so a wasm module compiled with ELISA_TARGET_OS_MACOS and
// ELISA_TARGET_ARCH_ARM64 both true on an arm64 Mac -- selecting macOS code paths
// in the runtime and making ARENA_BACKEND_WASM_HEAPBASE unreachable.
func TestTripleDoesNotFallBackToHost(t *testing.T) {
	for _, tc := range []struct{ triple, os, arch string }{
		{"wasm32-unknown-wasi", "wasi", "wasm32"},
		{"wasm32-unknown-unknown", "wasi", "wasm32"},
		{"wasm32-unknown-emscripten", "wasi", "wasm32"},
		{"wasm64-unknown-wasi", "wasi", "wasm64"},
		{"x86_64-apple-darwin", "macos", "x86_64"},
		{"arm64-apple-macosx14.0", "macos", "arm64"},
		{"x86_64-pc-windows-msvc", "windows", "x86_64"},
		{"aarch64-unknown-linux-gnu", "linux", "arm64"},
		// An arch with no normalized name still comes from the triple, not the host.
		{"riscv64-unknown-linux-gnu", "linux", "riscv64"},
	} {
		if got := targetOSFromTriple(tc.triple); got != tc.os {
			t.Errorf("targetOSFromTriple(%q) = %q, want %q", tc.triple, got, tc.os)
		}
		if got := targetArchFromTriple(tc.triple); got != tc.arch {
			t.Errorf("targetArchFromTriple(%q) = %q, want %q", tc.triple, got, tc.arch)
		}
	}
}

// The host IS the right answer when nothing was specified -- that is the case the
// fallback exists for, and it must keep working.
func TestEmptyTripleUsesHost(t *testing.T) {
	wantOS := map[string]string{"darwin": "macos", "linux": "linux", "windows": "windows", "freebsd": "freebsd"}[runtime.GOOS]
	if got := targetOSFromTriple(""); got != wantOS {
		t.Errorf("targetOSFromTriple(\"\") = %q, want host %q", got, wantOS)
	}
	wantArch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[runtime.GOARCH]
	if wantArch == "" {
		wantArch = runtime.GOARCH
	}
	if got := targetArchFromTriple(""); got != wantArch {
		t.Errorf("targetArchFromTriple(\"\") = %q, want host %q", got, wantArch)
	}
}

// WASI is POSIX-ish on purpose: the runtime's whole POSIX surface is
// mmap/munmap/malloc/free/mem*/write, which wasi-libc and the browser host shim
// both supply. This keeps wasm on the mmap arena backend, which can reclaim --
// ARENA_BACKEND_WASM_HEAPBASE's free_region is a no-op.
func TestWasiCountsAsPOSIX(t *testing.T) {
	for _, os := range []string{"macos", "linux", "freebsd", "wasi"} {
		if !isPOSIXish(os) {
			t.Errorf("isPOSIXish(%q) = false, want true", os)
		}
	}
	for _, os := range []string{"windows", ""} {
		if isPOSIXish(os) {
			t.Errorf("isPOSIXish(%q) = true, want false", os)
		}
	}
}
