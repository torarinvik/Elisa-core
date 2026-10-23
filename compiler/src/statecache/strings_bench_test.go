package statecache

import (
	"reflect"
	"strings"
	"testing"
)

type stringEntry struct {
	File string
	Name string
}

func repeatedStringFixture() (*Registry, []stringEntry) {
	entries := make([]stringEntry, 10000)
	for i := range entries {
		entries[i] = stringEntry{File: "/compiler/semantic/" + strings.Repeat("long_source_directory/", 6) + "expression_analysis.elisa", Name: "parameter"}
	}
	return New([]reflect.Type{reflect.TypeFor[[]stringEntry]()}, nil, nil), entries
}

func BenchmarkRepeatedStateStrings(b *testing.B) {
	r, root := repeatedStringFixture()
	data, err := r.Encode(root)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("encode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := r.Encode(root); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("decode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := r.Decode(data); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Logf("snapshot bytes=%d", len(data))
}
