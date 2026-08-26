package semantic

import (
	"runtime"
	"strings"
)

func (a *Analyzer) populateTargetConstValues(targetTriple string, targetDebug bool) {
	osName := targetOSFromTriple(targetTriple)
	archName := targetArchFromTriple(targetTriple)
	compilerName := targetCompilerFromTriple(targetTriple)
	a.setTargetConstBool("ELISA_TARGET_OS_MACOS", osName == "macos")
	a.setTargetConstBool("ELISA_TARGET_OS_LINUX", osName == "linux")
	a.setTargetConstBool("ELISA_TARGET_OS_WINDOWS", osName == "windows")
	a.setTargetConstBool("ELISA_TARGET_OS_FREEBSD", osName == "freebsd")
	a.setTargetConstBool("ELISA_TARGET_OS_POSIX", isPOSIXish(osName))
	a.setTargetConstBool("ELISA_TARGET_OS_WASI", osName == "wasi")
	// The same target under the name stage1 uses. Both compilers must know BOTH names, or a
	// `static if ELISA_TARGET_OS_WASM:` folds under one and not the other -- which is what the
	// emit_ast / emit_header / emit_unsafe parity gates caught on the wasm_minimal fixture:
	// stage1 saw 16 declarations where stage0 saw 15, because the name was unknown here.
	//
	// An ALIAS, not a defined symbol, and that distinction is load-bearing. elisacore_std's
	// arena.elisa declares `const ELISA_TARGET_OS_WASM: bool = false` itself, as bootstrap
	// compatibility for stage0 binaries that predate this line. setTargetConstBool would
	// define a global symbol and that declaration would then be a duplicate, breaking every
	// build of the std. The alias supplies the fold value without the symbol, so the std's
	// own const stays legal and the real target value still wins where they differ.
	//
	// Keyed on the ARCH rather than osName so it holds for a wasm triple whose OS component
	// is absent or "unknown" as well as for "wasi".
	a.setTargetConstAliasBool("ELISA_TARGET_OS_WASM", archName == "wasm32" || archName == "wasm64")
	a.setTargetConstBool("ELISA_TARGET_ARCH_X86_64", archName == "x86_64")
	a.setTargetConstBool("ELISA_TARGET_ARCH_ARM64", archName == "arm64" || archName == "aarch64")
	a.setTargetConstBool("ELISA_TARGET_ARCH_WASM32", archName == "wasm32")
	a.setTargetConstBool("ELISA_TARGET_ARCH_WASM64", archName == "wasm64")
	a.setTargetConstBool("ELISA_TARGET_ARCH_WASM", archName == "wasm32" || archName == "wasm64")
	a.setTargetConstAliasBool("PLATFORM_WINDOWS", osName == "windows")
	a.setTargetConstAliasBool("PLATFORM_APPLE", osName == "macos")
	a.setTargetConstAliasBool("PLATFORM_DARWIN", osName == "macos")
	a.setTargetConstAliasBool("PLATFORM_LINUX", osName == "linux")
	a.setTargetConstAliasBool("PLATFORM_FREEBSD", osName == "freebsd")
	a.setTargetConstAliasBool("ARCH_X86_64", archName == "x86_64")
	a.setTargetConstAliasBool("ARCH_ARM64", archName == "arm64" || archName == "aarch64")
	a.setTargetConstAliasBool("DEBUG", targetDebug)
	a.setTargetConstAliasBool("RELEASE", !targetDebug)
	a.setTargetConstString("target.os", osName)
	a.setTargetConstString("target.arch", archName)
	a.setTargetConstString("target.compiler", compilerName)
	a.setTargetConstBool("target.debug", targetDebug)
	a.setTargetConstBool("target.release", !targetDebug)
	a.setTargetConstBool("target.features.avx2", targetFeatureFromTriple(targetTriple, "avx2"))
	a.setTargetConstBool("target.features.u128", compilerName != "msvc")
	a.setTargetConstBool("target.features.posix", isPOSIXish(osName))
	a.setTargetConstBool("target.libc.gnu_strerror_r", osName == "linux" && compilerName != "msvc")
	a.setTargetConstNamespace("target", ConstValue{Kind: ConstRecord, Fields: map[string]ConstValue{
		"os":       {Kind: ConstString, String: osName},
		"arch":     {Kind: ConstString, String: archName},
		"compiler": {Kind: ConstString, String: compilerName},
		"debug":    {Kind: ConstBool, Bool: targetDebug},
		"release":  {Kind: ConstBool, Bool: !targetDebug},
		"features": {Kind: ConstRecord, Fields: map[string]ConstValue{
			"avx2":  {Kind: ConstBool, Bool: targetFeatureFromTriple(targetTriple, "avx2")},
			"u128":  {Kind: ConstBool, Bool: compilerName != "msvc"},
			"posix": {Kind: ConstBool, Bool: isPOSIXish(osName)},
		}},
		"libc": {Kind: ConstRecord, Fields: map[string]ConstValue{
			"gnu_strerror_r": {Kind: ConstBool, Bool: osName == "linux" && compilerName != "msvc"},
		}},
	}})
}

// WASI counts as POSIX-ish because the only POSIX surface the runtime uses is
// mmap/munmap/malloc/free/mem*/write, and both wasi-libc and the browser host
// shim (shells/web/elisa-wasm-runtime.js) supply exactly that subset.
func isPOSIXish(osName string) bool {
	switch osName {
	case "macos", "linux", "freebsd", "wasi":
		return true
	}
	return false
}

func (a *Analyzer) setTargetConstBool(name string, value bool) {
	a.constValues[name] = ConstValue{Kind: ConstBool, Bool: value}
	if a.globalScope == nil {
		return
	}
	if _, exists := a.globalScope.Lookup(name); exists {
		return
	}
	a.globalScope.Define(&Symbol{
		Name:    name,
		Kind:    SymbolConst,
		Type:    a.namedTypes["bool"],
		Mutable: false,
	})
}

func (a *Analyzer) setTargetConstAliasBool(name string, value bool) {
	a.constValues[name] = ConstValue{Kind: ConstBool, Bool: value}
}

func (a *Analyzer) setTargetConstString(name string, value string) {
	a.constValues[name] = ConstValue{Kind: ConstString, String: value}
}

func (a *Analyzer) setTargetConstNamespace(name string, value ConstValue) {
	a.constValues[name] = value
	if a.globalScope == nil {
		return
	}
	if _, exists := a.globalScope.Lookup(name); exists {
		return
	}
	a.globalScope.Define(&Symbol{
		Name:    name,
		Kind:    SymbolConst,
		Type:    ConstValueStaticType(a.namedTypes, value),
		Mutable: false,
	})
}

// The host is a default only when NO triple was given. When one IS given, its
// components are the truth and an unrecognized component is UNKNOWN -- never
// "whatever machine the compiler happens to be running on". Inheriting the host
// silently made `-target-triple wasm32-unknown-wasi` compile as macOS/arm64:
// ELISA_TARGET_OS_MACOS and ELISA_TARGET_ARCH_ARM64 both came out true, the
// runtime picked macOS code paths, and ARENA_BACKEND_WASM_HEAPBASE became dead
// code that no triple could ever select.
func targetOSFromTriple(targetTriple string) string {
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

func targetArchFromTriple(targetTriple string) string {
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

func targetCompilerFromTriple(targetTriple string) string {
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

func targetFeatureFromTriple(targetTriple string, feature string) bool {
	triple := strings.ToLower(strings.TrimSpace(targetTriple))
	return strings.Contains(triple, "+"+feature) || strings.Contains(triple, "-"+feature) || strings.Contains(triple, feature)
}
