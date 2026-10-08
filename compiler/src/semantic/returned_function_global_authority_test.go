package semantic

import (
	"strings"
	"testing"
)

func TestReturnedFunctionKeepsConcreteGlobalAuthority(t *testing.T) {
	base := `global mutable hot: i32 = 0
def writer() -> i32:
    can Global.Write:
        hot <- 3
    return 0
def reader() -> i32:
    return hot can Global.Read
`
	cases := []struct{ name, factory, invoke, missing string }{
		{"factory_alias", "def factory() -> fn() -> i32 can[Global.Read]:\n    return writer\n", "make = factory\n    f = make()\n    return f() can Global.Read", "Global.Write"},
		{"type_alias", "type Action = fn() -> i32 can[Global.Read]\ndef factory() -> Action:\n    return writer\n", "f = factory()\n    return f() can Global.Read", "Global.Write"},
		{"trusted_creation", "def factory() -> fn() -> i32 can[Global.Read]:\n    trusted Global.Write:\n        return writer\n", "f = factory()\n    return f() can Global.Read", "Global.Write"},
		{"direct", "def factory() -> fn() -> i32 can[Global.Read]:\n    return writer\n", "f = factory()\n    return f() can Global.Read", "Global.Write"},
		{"nested", "def factory() -> fn() -> i32 can[Global.Read]:\n    return writer\ndef relay() -> fn() -> i32 can[Global.Read]:\n    return factory()\n", "f = relay()\n    return f() can Global.Read", "Global.Write"},
		{"branch", "def factory(flag: bool) -> fn() -> i32 can[Global.Read]:\n    if flag:\n        return writer\n    return reader\n", "f = factory(true)\n    return f() can Global.Read", "Global.Write"},
		{"readonly_wrong_axis", "def factory() -> fn() -> i32 can[Global.Write]:\n    return reader\n", "f = factory()\n    return f() can Global.Write", "Global.Read"},
		{"qualified", "module Box:\n    def factory() -> fn() -> i32 can[Global.Read]:\n        return writer\n", "f = Box::factory()\n    return f() can Global.Read", "Global.Write"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := base + tc.factory + "def main() -> i32:\n    " + tc.invoke + "\n"
			result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, tc.name+".elisa", src, AnalyzeOptions{})
			errors := strings.Join(result.Errors(), "\n")
			if !strings.Contains(errors, "Global{Read,Write}") || !strings.Contains(errors, "call to") {
				t.Fatalf("returned concrete member must remain mandatory: %s", errors)
			}
			if strings.Contains(errors, "call to \"factory\"") || strings.Contains(errors, "call to \"relay\"") {
				t.Fatalf("callback construction must remain pure: %s", errors)
			}
			positive := strings.ReplaceAll(src, "return f() can Global.Read", "return f() can Global{Read,Write}")
			positive = strings.ReplaceAll(positive, "return f() can Global.Write", "return f() can Global{Read,Write}")
			result = analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, tc.name+"_both.elisa", positive, AnalyzeOptions{})
			if errors = strings.Join(result.Errors(), "\n"); errors != "" {
				t.Fatalf("Both invocation must pass with pure construction: %s", errors)
			}
		})
	}
}
