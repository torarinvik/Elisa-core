package semantic

import (
	"os"
	"path/filepath"
	"strings"

	"elisacore/src/lexer"
)

// runtimeCarrierSurfaceReplacement returns the prose "use X instead" hint for the carrier
// WARNING/ERROR message. Arena/ArenaMark map to a prose phrase (not a type name) because the
// migration target is a language construct (a region scope), not a drop-in type.
func runtimeCarrierSurfaceReplacement(typeName string) (string, bool) {
	if display, ok := runtimeCarrierTypeDisplayReplacement(typeName); ok {
		return display, true
	}
	switch typeName {
	case "Arena":
		return "region scopes and inferred container regions", true
	case "ArenaMark":
		return "region scopes/checkpoints", true
	default:
		return "", false
	}
}

// runtimeCarrierTypeDisplayReplacement returns the clean *type name* a raw carrier type should
// render as inside type-mismatch diagnostics. Only carriers whose replacement is itself a valid
// type spelling belong here — notably NOT Arena/ArenaMark, whose prose hint ("region scopes …")
// would read as nonsense in "expects mutable <X>&, got <X>". Those render under their plain name.
func runtimeCarrierTypeDisplayReplacement(typeName string) (string, bool) {
	switch typeName {
	case "StringView":
		return "sview[...]", true
	case "DynArrayView":
		return "view[T]", true
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
	if !ok || !a.shouldRejectRuntimeCarrierTypeUse(pos) {
		return
	}
	if typeName == "Arena" || typeName == "ArenaMark" {
		if runtimeCarrierPathIsTempFixture(pos.File) {
			return
		}
		a.warnf(pos, "internal runtime carrier type %q is not supported in user-facing code; use %q instead", typeName, replacement)
		return
	}
	a.errorf(pos, "internal runtime carrier type %q is not supported in user-facing code; use %q instead", typeName, replacement)
}

func (a *Analyzer) shouldRejectRuntimeCarrierTypeUse(pos lexer.Pos) bool {
	path := filepath.ToSlash(pos.File)
	if path == "" || path == "<unknown>" {
		return false
	}
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
	case "generated_runner.elisa", "execute_pool_tests_fixture.elisa":
		return true
	}
	internalRoots := []string{
		"compiler/runtime/elisacore_std/",
		"elisac.elisalib/vendor/elisacore_std/",
		"vendor/elisacore_std/",
		"Code/frontend_elisacore/",
		"Code/elisacore_lua/",
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

func runtimeCarrierPathIsTempFixture(path string) bool {
	if path == "" {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	tempDir := os.TempDir()
	absTemp, err := filepath.Abs(tempDir)
	if err != nil {
		absTemp = tempDir
	}
	absPath = filepath.ToSlash(absPath)
	absTemp = strings.TrimRight(filepath.ToSlash(absTemp), "/")
	return absPath == absTemp || strings.HasPrefix(absPath, absTemp+"/")
}
