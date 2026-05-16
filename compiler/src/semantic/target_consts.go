package semantic

import (
	"runtime"
	"strings"
)

func (a *Analyzer) populateTargetConstValues(targetTriple string) {
	osName := targetOSFromTriple(targetTriple)
	a.setTargetConstBool("ELISA_TARGET_OS_MACOS", osName == "macos")
	a.setTargetConstBool("ELISA_TARGET_OS_LINUX", osName == "linux")
	a.setTargetConstBool("ELISA_TARGET_OS_WINDOWS", osName == "windows")
	a.setTargetConstBool("ELISA_TARGET_OS_FREEBSD", osName == "freebsd")
	a.setTargetConstBool("ELISA_TARGET_OS_POSIX", osName == "macos" || osName == "linux" || osName == "freebsd")
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
