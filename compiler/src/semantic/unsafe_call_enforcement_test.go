package semantic

import (
	"strings"
	"testing"
)

func TestStrictUnsafeCallAuthority(t *testing.T) {
	cases := []struct {
		name, prefix, grant, body string
		reject                    bool
	}{
		{"direct", "", "Unsafe.PointerCast", "return forge(raw)", true},
		{"mixed", "", "Unsafe.PointerCast, Console.Write", "return forge(raw)", true},
		{"transitive", "def relay(raw: uintptr) -> heap u8&:\n    return forge(raw) can Unsafe.PointerCast\n", "Unsafe.PointerCast", "return relay(raw)", true},
		{"function_value", "", "Unsafe.PointerCast", "f = forge\n    return f(raw)", true},
		{"alias_wrong_axis", "alias Wrong = Unsafe.Alias\n", "Unsafe.PointerCast", "return forge(raw) can Wrong", true},
		{"wrong_axis", "", "Unsafe.PointerCast", "return forge(raw) can Unsafe.Alias", true},
		{"granted", "", "Unsafe.PointerCast", "return forge(raw) can Unsafe.PointerCast", false},
		{"alias_granted", "alias Ptr = Unsafe.PointerCast\n", "Unsafe.PointerCast", "return forge(raw) can Ptr", false},
		{"trusted_exact", "", "Unsafe.PointerCast", "trusted Unsafe.PointerCast:\n        return forge(raw)", false},
		{"trusted_wrong_axis", "", "Unsafe.PointerCast", "trusted Unsafe.Alias:\n        return forge(raw)", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.prefix + "def forge(raw: uintptr) -> heap u8&:\n    can " + tc.grant + ":\n        return raw.cast[heap u8&]\ndef caller(raw: uintptr) -> heap u8&:\n    " + tc.body + "\n"
			for _, strict := range []bool{false, true} {
				result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, tc.name+".elisa", src, AnalyzeOptions{EnforceUnsafePermissions: strict})
				errors := strings.Join(result.Errors(), "\n")
				if strict && tc.reject {
					if !strings.Contains(errors, "call to") || !strings.Contains(errors, "Unsafe") {
						t.Fatalf("strict call must reject missing Unsafe authority: %s", errors)
					}
				} else if errors != "" {
					t.Fatalf("strict=%v unexpected errors: %s", strict, errors)
				}
			}
		})
	}
}
