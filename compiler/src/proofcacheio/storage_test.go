package proofcacheio

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeyMatchesDigest(t *testing.T) {
	for _, query := range []string{"", "small", strings.Repeat("q", 10000)} {
		sum := sha256.Sum256([]byte("identity\x00" + query))
		if got := Key("identity", query); got != hex.EncodeToString(sum[:]) {
			t.Fatalf("wrong key for size %d", len(query))
		}
	}
}
func TestWriteRecordPublishesAndReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "record")
	WriteRecord(path, ".tmp-*", []byte("first"))
	WriteRecord(path, ".tmp-*", []byte("second"))
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "second" {
		t.Fatalf("read: %q %v", data, err)
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 1 {
		t.Fatalf("staging files remain: %v %v", files, err)
	}
}
