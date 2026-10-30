package backend

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// A disk entry contains exact input bitcode and target configuration as well as
// its output bytes (native object or optimized bitcode, in separate key domains).
// Digests locate entries and detect corruption; they never replace input checks.
// The caller supplies a compiler-build namespace so different native emitters
// cannot reuse one another's output. Disk failures are ordinary cache misses.
type incrementalObjectDisk struct {
	root     string
	maxBytes int64
	mu       sync.Mutex
}

// EnableDiskCache must be called before using the session. An empty identity or
// directory disables persistence. Entries are private to the user's cache dir.
func (s *IncrementalObjectSession) EnableDiskCache(root, compilerIdentity string, maxBytes int64) {
	s.disk = nil
	if root == "" || compilerIdentity == "" || maxBytes <= 0 {
		return
	}
	namespace := sha256.Sum256([]byte("object-partitions-v1\x00" + compilerIdentity))
	s.disk = &incrementalObjectDisk{root: filepath.Join(root, hex.EncodeToString(namespace[:])), maxBytes: maxBytes}
}

func (d *incrementalObjectDisk) path(bitcode []byte, target string) string {
	key := incrementalObjectKeyBytes(bitcode, target)
	return d.pathForKey(key)
}

func (d *incrementalObjectDisk) pathForKey(key [32]byte) string {
	return filepath.Join(d.root, hex.EncodeToString(key[:])+".part")
}

// Header: magic, three lengths, then the object's SHA256. Bitcode and target
// are compared byte-for-byte, including user data and all target settings.
func (d *incrementalObjectDisk) load(bitcode []byte, target string) []byte {
	if d == nil {
		return nil
	}
	return d.loadKey(bitcode, target, incrementalObjectKeyBytes(bitcode, target))
}

func (d *incrementalObjectDisk) loadKey(bitcode []byte, target string, key [32]byte) []byte {
	if d == nil {
		return nil
	}
	path := d.pathForKey(key)
	f, err := openRegularCacheFile(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var header [64]byte
	if _, err := io.ReadFull(f, header[:]); err != nil || string(header[:8]) != "ELIOBJ01" {
		return nil
	}
	b, t, o := binary.LittleEndian.Uint64(header[8:16]), binary.LittleEndian.Uint64(header[16:24]), binary.LittleEndian.Uint64(header[24:32])
	info, err := f.Stat()
	if err != nil || b != uint64(len(bitcode)) || t != uint64(len(target)) || o == 0 || o > uint64(d.maxBytes) || b > uint64(d.maxBytes) || t > uint64(d.maxBytes) || b+t+o+64 > uint64(d.maxBytes) || uint64(info.Size()) != b+t+o+64 {
		return nil
	}
	// Compare in bounded chunks rather than allocate another whole IR buffer.
	chunk := make([]byte, min(32*1024, max(len(bitcode), len(target))))
	if !diskInputEquals(f, bitcode, chunk) || !diskInputEquals(f, []byte(target), chunk) {
		return nil
	}
	object := make([]byte, int(o))
	if _, err := io.ReadFull(f, object); err != nil {
		return nil
	}
	digest := sha256.Sum256(object)
	if !bytes.Equal(digest[:], header[32:]) {
		return nil
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return object
}

func diskInputEquals(r io.Reader, expected, chunk []byte) bool {
	if len(chunk) == 0 {
		return len(expected) == 0
	}
	for len(expected) > 0 {
		n := len(expected)
		if n > len(chunk) {
			n = len(chunk)
		}
		if _, err := io.ReadFull(r, chunk[:n]); err != nil || !bytes.Equal(chunk[:n], expected[:n]) {
			return false
		}
		expected = expected[n:]
	}
	return true
}

func (d *incrementalObjectDisk) store(bitcode, target string, object []byte) {
	if d == nil || len(object) == 0 || int64(len(bitcode))+int64(len(target))+int64(len(object))+64 > d.maxBytes {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if os.MkdirAll(d.root, 0700) != nil {
		return
	}
	f, err := os.CreateTemp(d.root, ".publish-*")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	var header [64]byte
	copy(header[:8], "ELIOBJ01")
	binary.LittleEndian.PutUint64(header[8:16], uint64(len(bitcode)))
	binary.LittleEndian.PutUint64(header[16:24], uint64(len(target)))
	binary.LittleEndian.PutUint64(header[24:32], uint64(len(object)))
	digest := sha256.Sum256(object)
	copy(header[32:], digest[:])
	_, err = f.Write(header[:])
	if err == nil {
		_, err = io.WriteString(f, bitcode)
	}
	if err == nil {
		_, err = io.WriteString(f, target)
	}
	if err == nil {
		_, err = f.Write(object)
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return
	}
	if os.Rename(f.Name(), d.path([]byte(bitcode), target)) != nil {
		return
	}
	d.prune()
}

// Eviction may race another process's lookup: an open file stays valid and a
// missing entry merely recompiles. Temporary publications are never considered.
func (d *incrementalObjectDisk) prune() {
	entries, err := os.ReadDir(d.root)
	if err != nil {
		return
	}
	type item struct {
		name string
		size int64
		used time.Time
	}
	var files []item
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || (filepath.Ext(entry.Name()) != ".part" && filepath.Ext(entry.Name()) != ".schedule") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, item{entry.Name(), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= d.maxBytes {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].used.Before(files[j].used) })
	for _, file := range files {
		if total <= d.maxBytes {
			break
		}
		if os.Remove(filepath.Join(d.root, file.name)) == nil {
			total -= file.size
		}
	}
}
