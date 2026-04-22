package semantic

import (
	"os"
	"path/filepath"
	"strings"

	"llcontext/src/lexer"
)

func runtimeCarrierSurfaceReplacement(typeName string) (string, bool) {
	switch typeName {
	case "StringView":
		return "sview[...]", true
	case "DynArrayView":
		return "dview[T]", true
	case "DynArray":
		return "darray[T, shape]", true
	case "DynDict":
		return "dict[K, V]", true
	default:
		return "", false
	}
}

func (a *Analyzer) maybeRejectRuntimeCarrierTypeUse(pos lexer.Pos, typeName string) {
	replacement, ok := runtimeCarrierSurfaceReplacement(typeName)
	if !ok || !a.shouldRejectRuntimeCarrierTypeUse() {
		return
	}
	a.errorf(pos, "internal runtime carrier type %q is not supported in user-facing code; use %q instead", typeName, replacement)
}

func (a *Analyzer) shouldRejectRuntimeCarrierTypeUse() bool {
	if a == nil || a.file == nil || a.file.Filename == "" {
		return false
	}
	path := filepath.ToSlash(a.file.Filename)
	if !runtimeCarrierCarrierPathHasRealSourcePath(path) {
		return false
	}
	return !runtimeCarrierCarrierPathIsInternal(path)
}

func runtimeCarrierCarrierPathHasRealSourcePath(path string) bool {
	if path == "" {
		return false
	}
	if strings.Contains(path, "/") {
		return true
	}
	_, err := os.Stat(path)
	return err == nil
}

func runtimeCarrierCarrierPathIsInternal(path string) bool {
	normalized := filepath.ToSlash(path)
	base := filepath.Base(normalized)
	switch base {
	case "generated_runner.llcontext", "execute_pool_tests_fixture.llcontext":
		return true
	}
	internalRoots := []string{
		"Code/llcontext_std/",
		"Code/frontend_llcontext/",
		"Code/llcontext_lua/",
		"Code/test_programs/",
		"Code/benchmarks/",
		"Code/lua/",
		"Code/zimdjson/",
	}
	for _, root := range internalRoots {
		if strings.HasPrefix(normalized, root) || strings.Contains(normalized, "/"+root) {
			return true
		}
	}
	return false
}
