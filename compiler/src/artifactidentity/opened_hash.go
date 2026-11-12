package artifactidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

func OpenedHash(artifact *os.File, original os.FileInfo) (string, error) {
	if artifact == nil || original == nil {
		return "", fmt.Errorf("compiler identity unavailable")
	}
	before, err := artifact.Stat()
	if err != nil {
		return "", err
	}
	unchanged := func(info os.FileInfo) bool {
		return info.Size() == original.Size() && info.ModTime().Equal(original.ModTime())
	}
	if !unchanged(before) {
		return "", fmt.Errorf("running compiler artifact changed")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(artifact, 0, before.Size())); err != nil {
		return "", err
	}
	after, err := artifact.Stat()
	if err != nil || !unchanged(after) {
		return "", fmt.Errorf("compiler changed during identity read")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
