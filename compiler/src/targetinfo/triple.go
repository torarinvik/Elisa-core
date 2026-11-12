package targetinfo

import (
	"runtime"
	"strings"
)

// WASI counts as POSIX-ish because the only POSIX surface the runtime uses is
// mmap/munmap/malloc/free/mem*/write, and both wasi-libc and the browser host
// shim (shells/web/elisa-wasm-runtime.js) supply exactly that subset.
func IsPOSIXish(osName string) bool {
	switch osName {
	case "macos", "linux", "freebsd", "wasi":
		return true
	}
	return false
}

// The host is a default only when NO triple was given. When one IS given, its
// components are the truth and an unrecognized component is UNKNOWN -- never
// "whatever machine the compiler happens to be running on". Inheriting the host
// silently made `-target-triple wasm32-unknown-wasi` compile as macOS/arm64:
// ELISA_TARGET_OS_MACOS and ELISA_TARGET_ARCH_ARM64 both came out true, the
// runtime picked macOS code paths, and ARENA_BACKEND_WASM_HEAPBASE became dead
// code that no triple could ever select.
func OSFromTriple(targetTriple string) string {
	triple := strings.ToLower(strings.TrimSpace(targetTriple))
	switch {
	case strings.Contains(triple, "darwin") || strings.Contains(triple, "macos"):
		return "macos"
	case strings.Contains(triple, "linux"):
		return "linux"
	case strings.Contains(triple, "windows") || strings.Contains(triple, "win32") || strings.Contains(triple, "mingw"):
		return "windows"
	case strings.Contains(triple, "freebsd"):
		return "freebsd"
	case strings.Contains(triple, "wasi") || strings.Contains(triple, "emscripten"):
		return "wasi"
	}
	if triple != "" {
		// A wasm arch with no OS component ("wasm32-unknown-unknown") is a
		// freestanding wasm target, not the host.
		if strings.HasPrefix(triple, "wasm") {
			return "wasi"
		}
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "linux":
		return "linux"
	case "windows":
		return "windows"
	case "freebsd":
		return "freebsd"
	default:
		return ""
	}
}

func ArchFromTriple(targetTriple string) string {
	triple := strings.ToLower(strings.TrimSpace(targetTriple))
	switch {
	case strings.Contains(triple, "x86_64") || strings.Contains(triple, "amd64"):
		return "x86_64"
	case strings.Contains(triple, "aarch64") || strings.Contains(triple, "arm64"):
		return "arm64"
	case strings.Contains(triple, "wasm64"):
		return "wasm64"
	case strings.Contains(triple, "wasm32"):
		return "wasm32"
	}
	if triple != "" {
		// Standard triple layout is <arch>-<vendor>-<os>-<abi>, so an arch we
		// do not have a normalized name for is still the first component. That
		// is a far better answer than the host's arch.
		return strings.SplitN(triple, "-", 2)[0]
	}
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

func CompilerFromTriple(targetTriple string) string {
	triple := strings.ToLower(strings.TrimSpace(targetTriple))
	switch {
	case strings.Contains(triple, "msvc"):
		return "msvc"
	case strings.Contains(triple, "clang"):
		return "clang"
	case strings.Contains(triple, "gnu") || strings.Contains(triple, "gcc"):
		return "gnu"
	default:
		return ""
	}
}

func FeatureFromTriple(targetTriple string, feature string) bool {
	triple := strings.ToLower(strings.TrimSpace(targetTriple))
	return strings.Contains(triple, "+"+feature) || strings.Contains(triple, "-"+feature) || strings.Contains(triple, feature)
}
