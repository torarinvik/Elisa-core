package semantic

import (
	"strings"
	"testing"
)

func TestReturnedFunctionKeepsConcreteUnsafeAuthority(t *testing.T) {
	base := "def forge(raw: uintptr) -> heap u8&:\n    return raw.cast[heap u8&] can Unsafe.PointerCast\n"
	factories := []string{
		"def factory() -> fn(uintptr) -> heap u8& can[Unsafe.Alias]:\n    return forge\n",
		"def inner() -> fn(uintptr) -> heap u8& can[Unsafe.Alias]:\n    return forge\ndef factory() -> fn(uintptr) -> heap u8& can[Unsafe.Alias]:\n    return inner()\n",
		"def factory() -> fn(uintptr) -> heap u8& can[Unsafe.Alias]:\n    trusted Unsafe.PointerCast:\n        return forge\n",
		"module Box:\n    def inner() -> fn(uintptr) -> heap u8& can[Unsafe.Alias]:\n        return forge\ndef factory() -> fn(uintptr) -> heap u8& can[Unsafe.Alias]:\n    return Box::inner()\n",
	}
	for _, factory := range factories {
		src := base + factory + "def main() -> i32:\n    f = factory()\n    value = f(1) can Unsafe.Alias\n    return 0\n"
		for _, strict := range []bool{false, true} {
			result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "unsafe_factory.elisa", src, AnalyzeOptions{EnforceUnsafePermissions: strict})
			errors := strings.Join(result.Errors(), "\n")
			if strict && (!strings.Contains(errors, "PointerCast") || !strings.Contains(errors, "call to")) {
				t.Fatalf("strict callback must retain PointerCast: %s", errors)
			}
			if !strict && errors != "" {
				t.Fatalf("default stays advisory: %s", errors)
			}
			if strings.Contains(errors, "call to \"factory\"") || strings.Contains(errors, "call to \"inner\"") {
				t.Fatalf("construction must be pure: %s", errors)
			}
			positive := strings.ReplaceAll(src, "f(1) can Unsafe.Alias", "f(1) can Unsafe{Alias,PointerCast}")
			result = analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "unsafe_both.elisa", positive, AnalyzeOptions{EnforceUnsafePermissions: strict})
			if errors = strings.Join(result.Errors(), "\n"); errors != "" {
				t.Fatalf("grouped grant must pass: %s", errors)
			}
		}
	}
}
