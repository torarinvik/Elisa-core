package semantic

import (
	"strings"
	"testing"
)

func TestRetainedLocalGensymNameMatchesRegexp(t *testing.T) {
	inputs := []string{
		"__auto_0",
		"__auto_00", // Leading zeroes are accepted by the regexp.
		"__quantifier_123",
		"__auto_" + strings.Repeat("9", 4096), // No numeric conversion or overflow limit.
		"",
		"__auto_",
		"__quantifier_",
		"__auto_-1",
		"__auto_+1",
		"__auto_١",   // Unicode decimal digit, not ASCII [0-9].
		"__auto_1\n", // `$` is end-of-text without multiline mode.
		"__auto_1\r",
		"x__auto_1",
		"__auto_1x",
		"__quantifier_1_0",
		"__autoX_1",
		"__AUTO_1",
		"__machine_1",
		"__auto_1\xff",
		"\xff__auto_1",
	}
	for _, input := range inputs {
		want := retainedLocalGensym.MatchString(input)
		if got := retainedLocalGensymName(input); got != want {
			t.Errorf("retainedLocalGensymName(%q) = %v, regexp oracle = %v", input, got, want)
		}
	}
}

func FuzzRetainedLocalGensymNameMatchesRegexp(f *testing.F) {
	for _, seed := range []string{
		"__auto_0",
		"__auto_00",
		"__quantifier_123",
		"__auto_",
		"__auto_1\n",
		"__auto_١",
		"x__quantifier_5",
		"__auto_1\xff",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		want := retainedLocalGensym.MatchString(input)
		if got := retainedLocalGensymName(input); got != want {
			t.Fatalf("retainedLocalGensymName(%q) = %v, regexp oracle = %v", input, got, want)
		}
	})
}

var retainedLocalGensymBenchmarkSink bool

func BenchmarkRetainedLocalGensymMatcher(b *testing.B) {
	inputs := []string{
		"/private/tmp/project/src/file.elisa",
		"ordinaryIdentifier",
		"field__auto_14",
		"__MachineMode_913_hash_82",
		"__auto_",
		"__auto_x",
		"__auto_1024",
		"__quantifier_98765",
		"__auto_١",
	}
	for _, candidate := range []struct {
		name  string
		match func(string) bool
	}{
		{name: "regexp", match: retainedLocalGensym.MatchString},
		{name: "exact", match: retainedLocalGensymName},
	} {
		b.Run(candidate.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				retainedLocalGensymBenchmarkSink = candidate.match(inputs[i%len(inputs)])
			}
		})
	}
}
