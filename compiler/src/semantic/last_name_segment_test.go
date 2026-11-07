package semantic

import (
	"math/rand"
	"strings"
	"testing"
)

func TestLastNameSegmentMatchesUnicodeReference(t *testing.T) {
	random := rand.New(rand.NewSource(83))
	for i := 0; i < 4000; i++ {
		data := make([]byte, random.Intn(100))
		for j := range data {
			data[j] = byte(random.Intn(256))
		}
		name := string(data)
		want := name
		if at := strings.LastIndexAny(name, ".:"); at >= 0 {
			want = name[at+1:]
		}
		if got := fillMayAdoptLastSegment(name); got != want || lastNameSegment(name) != want {
			t.Fatalf("%q: got %q, want %q", name, got, want)
		}
	}
}

var lastSegmentSink string

func BenchmarkLastNameSegment(b *testing.B) {
	name := "compiler::semantic::check_call_argument_region_store_escape"
	b.Run("unicode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			at := strings.LastIndexAny(name, ".:")
			lastSegmentSink = name[at+1:]
		}
	})
	b.Run("bytes", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			lastSegmentSink = fillMayAdoptLastSegment(name)
		}
	})
}
