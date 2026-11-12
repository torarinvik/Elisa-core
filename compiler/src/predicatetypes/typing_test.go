package predicatetypes

import (
	"strings"
	"testing"
)

func TestTypeTagsAndTypingValidation(t *testing.T) {
	for _, name := range []string{"int", "i64", "bool", "char", "i8", "i16", "i32", "u8", "u16", "u32", "u64", "isize", "usize", "uintptr"} {
		if tag := TypeTag(name); tag == 0 || TypeName(tag) != name {
			t.Fatalf("type %q did not round trip", name)
		}
	}
	if TypeTag("unknown") != 0 || TypeName('?') != "" {
		t.Fatal("unknown types must not be accepted")
	}
	for _, tc := range []struct {
		value          string
		simple, ranges bool
	}{
		{"bnni", true, false}, {"bnRnn", false, true}, {"", false, false}, {"nR", false, false}, {"b?", false, false}, {"b" + strings.Repeat("n", 4096), false, false},
	} {
		if ValidTyping(tc.value) != tc.simple || ValidRangeTyping(tc.value) != tc.ranges {
			t.Fatalf("incorrect validation for %q", tc.value[:min(len(tc.value), 20)])
		}
	}
}
