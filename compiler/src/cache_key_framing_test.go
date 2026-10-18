package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestCacheKeyFramingSeparatesEmbeddedFieldBoundaries(t *testing.T) {
	var left, right bytes.Buffer
	testRunnerCacheWriteBytes(&left, "runner", []byte("x\x00shim\x00y"))
	testRunnerCacheWriteBytes(&left, "shim", []byte("z"))
	testRunnerCacheWriteBytes(&right, "runner", []byte("x"))
	testRunnerCacheWriteBytes(&right, "shim", []byte("y\x00shim\x00z"))
	if bytes.Equal(left.Bytes(), right.Bytes()) {
		t.Fatal("distinct source fields have identical cache-key encoding")
	}
	left.Reset()
	right.Reset()
	testRunnerCacheWriteString(&left, "a\x00b")
	testRunnerCacheWriteString(&right, "a")
	testRunnerCacheWriteString(&right, "b")
	if bytes.Equal(left.Bytes(), right.Bytes()) {
		t.Fatal("one string is indistinguishable from two strings")
	}
}

func TestCacheKeyFramingMatchesStandardLengthEncoding(t *testing.T) {
	for _, length := range []uint64{0, 1, 127, 128, 255, 16383, 16384, 1 << 32, ^uint64(0)} {
		var got bytes.Buffer
		testRunnerCacheWriteLength(&got, length)
		if want := binary.AppendUvarint(nil, length); !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("length%d: got%x want%x", length, got.Bytes(), want)
		}
	}
	for _, value := range [][]byte{nil, {}, []byte("a\x00b"), bytes.Repeat([]byte{255}, 16384)} {
		var got bytes.Buffer
		label := "label\x00content"
		testRunnerCacheWriteBytes(&got, label, value)
		want := []byte{2}
		want = binary.AppendUvarint(want, uint64(len(label)))
		want = append(want, label...)
		want = binary.AppendUvarint(want, uint64(len(value)))
		want = append(want, value...)
		if !bytes.Equal(got.Bytes(), want) {
			t.Fatal("byte record differs from standard length framing")
		}
	}
	var left, right bytes.Buffer
	testRunnerCacheWriteBytes(&left, "label", []byte("value"))
	testRunnerCacheWriteString(&right, "label")
	testRunnerCacheWriteString(&right, "value")
	if bytes.Equal(left.Bytes(), right.Bytes()) {
		t.Fatal("different record kinds have identical framing")
	}
}
