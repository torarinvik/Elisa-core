package semantic

import (
	"strings"
	"testing"
	"unicode"
)

func TestStoreFlowVariableNameRuneEquivalence(t *testing.T) {
	old := func(name string) bool {
		if name == "" || strings.Contains(name, "::") || strings.Contains(name, ".") {
			return false
		}
		return !unicode.IsUpper([]rune(name)[0])
	}
	check := func(name string) {
		t.Helper()
		if got, want := storeFlowVariableName(name), old(name); got != want {
			t.Fatalf("%q: got %v want %v", name, got, want)
		}
	}
	for _, name := range []string{"", ".x", "x::y", "\xff", "\xc0\x80", "\x80name", "A", "a"} {
		check(name)
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue
		}
		check(string(r) + "tail")
	}
}
