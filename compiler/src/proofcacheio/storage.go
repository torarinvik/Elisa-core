package proofcacheio

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

func Key(identity, query string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(identity))
	_, _ = hash.Write([]byte{0})
	if len(query) <= 4096 {
		_, _ = hash.Write([]byte(query))
	} else {
		// The digest's interface makes []byte(query) escape. Bound scratch
		// instead of allocating another complete copy of every large query.
		var block [4096]byte
		for len(query) != 0 {
			n := copy(block[:], query)
			_, _ = hash.Write(block[:n])
			query = query[n:]
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func WriteRecord(path, pattern string, payload []byte) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, pattern)
	if os.IsNotExist(err) {
		if os.MkdirAll(dir, 0700) != nil {
			return
		}
		f, err = os.CreateTemp(dir, pattern)
	}
	if err != nil {
		return
	}
	temporary := f.Name()
	_, writeErr := f.Write(payload)
	closeErr := f.Close()
	if writeErr == nil && closeErr == nil && os.Rename(temporary, path) == nil {
		return
	}
	_ = os.Remove(temporary)
}
