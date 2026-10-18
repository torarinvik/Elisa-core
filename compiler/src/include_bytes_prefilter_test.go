package main

import (
	"math/rand"
	"testing"
)

func TestIncludeBytesPrefilterMatchesCanonicalParser(t *testing.T) {
	cases := []string{"", "def main() -> i64:", "    return 0", "include \"a.elisa\"", " # include \"a b.elisa\" \r", "\u2003include \"unicode.elisa\"\u2003", "include\t\"no.elisa\"", "{$I foo.elisa}", "{$INCLUDE 'a b.elisa'}", " {$I foo.elisa}", "{$include\u2003\"a.elisa\"}", "#{$I foo.elisa}", "# include \"\"", "##include \"x\"", "include \"unterminated", "{$I}"}
	random := rand.New(rand.NewSource(20261018))
	for i := 0; i < 2000; i++ {
		buf := make([]byte, random.Intn(96))
		for j := range buf {
			buf[j] = byte(random.Intn(256))
		}
		cases = append(cases, string(buf))
	}
	for _, source := range cases {
		want, wantOK := parseIncludeDirective(source)
		got, gotOK := parseIncludeDirectiveBytes([]byte(source))
		if got != want || gotOK != wantOK {
			t.Fatalf("source=%q bytes=%q/%v canonical=%q/%v", source, got, gotOK, want, wantOK)
		}
	}
	line := []byte("    return a_long_ordinary_expression + another_value")
	if allocations := testing.AllocsPerRun(100, func() {
		if _, ok := parseIncludeDirectiveBytes(line); ok {
			panic("ordinary line parsed as include")
		}
	}); allocations != 0 {
		t.Fatalf("ordinary source line allocated %g times", allocations)
	}
}
