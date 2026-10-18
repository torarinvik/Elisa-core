package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
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
		want.Write([]byte{1})
		want.Write(binary.AppendUvarint(nil, uint64(len(value))))
		want.Write([]byte(value))
	}
	if !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		t.Fatal("string key encoding changed")
	}
	var output bytes.Buffer
	for _, value := range values {
		testRunnerCacheWriteString(&output, value)
	}
	var reference bytes.Buffer
	for _, value := range values {
		reference.WriteByte(1)
		reference.Write(binary.AppendUvarint(nil, uint64(len(value))))
		reference.WriteString(value)
	}
	if !bytes.Equal(output.Bytes(), reference.Bytes()) {
		t.Fatal("string framing differs from independent reference")
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
