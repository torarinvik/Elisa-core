package semantic

import (
	"runtime"
	"strings"
)

func (a *Analyzer) populateTargetConstValues(targetTriple string) {
	osName := targetOSFromTriple(targetTriple)
	archName := targetArchFromTriple(targetTriple)
	compilerName := targetCompilerFromTriple(targetTriple)
	a.setTargetConstBool("ELISA_TARGET_OS_MACOS", osName == "macos")
	a.setTargetConstBool("ELISA_TARGET_OS_LINUX", osName == "linux")
	a.setTargetConstBool("ELISA_TARGET_OS_WINDOWS", osName == "windows")
	a.setTargetConstBool("ELISA_TARGET_OS_FREEBSD", osName == "freebsd")
	a.setTargetConstBool("ELISA_TARGET_OS_POSIX", osName == "macos" || osName == "linux" || osName == "freebsd")
	a.setTargetConstBool("ELISA_TARGET_ARCH_X86_64", archName == "x86_64")
	a.setTargetConstBool("ELISA_TARGET_ARCH_ARM64", archName == "arm64" || archName == "aarch64")
	a.setTargetConstAliasBool("PLATFORM_WINDOWS", osName == "windows")
	a.setTargetConstAliasBool("PLATFORM_APPLE", osName == "macos")
	a.setTargetConstAliasBool("PLATFORM_DARWIN", osName == "macos")
	a.setTargetConstAliasBool("PLATFORM_LINUX", osName == "linux")
	a.setTargetConstAliasBool("PLATFORM_FREEBSD", osName == "freebsd")
	a.setTargetConstAliasBool("ARCH_X86_64", archName == "x86_64")
	a.setTargetConstAliasBool("ARCH_ARM64", archName == "arm64" || archName == "aarch64")
	a.setTargetConstString("target.os", osName)
	a.setTargetConstString("target.arch", archName)
	a.setTargetConstString("target.compiler", compilerName)
	a.setTargetConstBool("target.features.avx2", targetFeatureFromTriple(targetTriple, "avx2"))
	a.setTargetConstBool("target.features.u128", compilerName != "msvc")
	a.setTargetConstBool("target.features.posix", osName == "macos" || osName == "linux" || osName == "freebsd")
	a.setTargetConstBool("target.libc.gnu_strerror_r", osName == "linux" && compilerName != "msvc")
	a.setTargetConstNamespace("target", ConstValue{Kind: ConstRecord, Fields: map[string]ConstValue{
		"os":       {Kind: ConstString, String: osName},
		"arch":     {Kind: ConstString, String: archName},
		"compiler": {Kind: ConstString, String: compilerName},
		"features": {Kind: ConstRecord, Fields: map[string]ConstValue{
			"avx2":  {Kind: ConstBool, Bool: targetFeatureFromTriple(targetTriple, "avx2")},
			"u128":  {Kind: ConstBool, Bool: compilerName != "msvc"},
			"posix": {Kind: ConstBool, Bool: osName == "macos" || osName == "linux" || osName == "freebsd"},
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
