package semantic

import "elisacore/src/targetinfo"

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

// Keep the semantic API local while target normalization is shared and independently cached.
func isPOSIXish(name string) bool                   { return targetinfo.IsPOSIXish(name) }
func targetOSFromTriple(triple string) string       { return targetinfo.OSFromTriple(triple) }
func targetArchFromTriple(triple string) string     { return targetinfo.ArchFromTriple(triple) }
func targetCompilerFromTriple(triple string) string { return targetinfo.CompilerFromTriple(triple) }
func targetFeatureFromTriple(triple, feature string) bool {
	return targetinfo.FeatureFromTriple(triple, feature)
}
