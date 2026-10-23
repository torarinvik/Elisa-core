package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkAnalysisDiskArtifactRead(b *testing.B) {
	dir := b.TempDir()
	artifact := analysisDiskArtifact{key: "benchmark", path: filepath.Join(dir, "state")}
	data := bytes.Repeat([]byte{'x'}, 16<<20)
	if err := artifact.publish(data); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := artifact.read()
		if err != nil || len(got) != len(data) || got[0] != 'x' || got[len(got)-1] != 'x' {
			b.Fatal("snapshot read changed bytes", err)
		}
	}
}

func TestAnalysisDiskArtifactReadRejectsInvalidEnvelope(t *testing.T) {
	artifact := analysisDiskArtifact{key: "expected", path: filepath.Join(t.TempDir(), "state")}
	for _, data := range [][]byte{nil, artifact.header(), []byte("ELISACSTATE1\nwrong\nbody")} {
		if err := os.WriteFile(artifact.path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := artifact.read(); err == nil {
			t.Fatal("invalid snapshot admitted")
		}
	}
	if err := artifact.publish([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	got, err := artifact.read()
	if err != nil || string(got) != "payload" {
		t.Fatal("valid snapshot rejected", err)
	}
}
