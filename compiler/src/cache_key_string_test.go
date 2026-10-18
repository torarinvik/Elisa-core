package main

import (
	"bytes"
	"crypto/sha256"
	"math/rand"
	"strings"
	"testing"
)

func TestCacheKeyStringEncodingMatchesCopyingReference(t *testing.T) {
	values := []string{"", "ordinary", "embedded\x00separator", "\xff\xfe", "Unicode: æ界", strings.Repeat("long value", 1024)}
	random := rand.New(rand.NewSource(20261018))
	for i := 0; i < 256; i++ {
		data := make([]byte, random.Intn(128))
		random.Read(data)
		values = append(values, string(data))
	}
	got, want := sha256.New(), sha256.New()
	for _, value := range values {
		testRunnerCacheWriteString(got, value)
		want.Write([]byte(value))
		want.Write([]byte{0})
	}
	if !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		t.Fatal("string key encoding changed")
	}
	var output bytes.Buffer
	for _, value := range values {
		testRunnerCacheWriteString(&output, value)
	}
	if output.String() != strings.Join(values, "\x00")+"\x00" {
		t.Fatal("string writer changed literal key bytes")
	}
}

func BenchmarkCacheKeyStringWrite(b *testing.B) {
	hash := sha256.New()
	value := strings.Repeat("cache identity", 16)
	b.ReportAllocs()
	b.SetBytes(int64(len(value)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		testRunnerCacheWriteString(hash, value)
		hash.Reset()
	}
}
