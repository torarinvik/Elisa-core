package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkBuildCacheTranscriptSelfHost(b *testing.B) {
	dir := b.TempDir()
	object := filepath.Join(dir, "object")
	if err := os.WriteFile(object, []byte("object"), 0600); err != nil {
		b.Fatal(err)
	}
	artifact := buildCacheArtifact{key: "benchmark", dir: filepath.Join(dir, "entry"), object: filepath.Join(dir, "entry", "out.o")}
	transcript := buildCacheTranscript{Stderr: bytes.Repeat([]byte("diagnostic warning text\n"), (9<<20)/24)}
	if err := publishCachedBuildObject(artifact, object, transcript); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := readBuildCacheTranscript(artifact); !ok {
			b.Fatal("transcript rejected")
		}
	}
}
