//go:build cgo && (darwin || linux)

package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

func TestLLVMLibraryIdentityStable(t *testing.T) {
	want, err := LLVMLibraryIdentity()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(want)
	if err != nil || len(raw) != 32 {
		t.Fatalf("invalid LLVM digest %q: %v", want, err)
	}
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			got, err := LLVMLibraryIdentity()
			if err != nil || got != want {
				t.Errorf("LLVM identity changed: %q %v", got, err)
			}
		}()
	}
	group.Wait()
}

func TestPinnedLLVMLibraryRejectsMutationAndWrongImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "llvm")
	if err := os.WriteFile(path, []byte("original LLVM"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if _, err := pinLLVMLibraryFile(path, uint64(stat.Dev), stat.Ino+1); err == nil {
		t.Fatal("accepted different mapped inode")
	}
	pin, err := pinLLVMLibraryFile(path, uint64(stat.Dev), stat.Ino)
	if err != nil {
		t.Fatal(err)
	}
	defer pin.file.Close()
	before, err := pin.stamp()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("modified LLVM"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got, err := pin.stamp(); err == nil {
		t.Fatalf("accepted modified loaded LLVM: before=%s after=%s", before, got)
	}
}

func TestPinnedLLVMLibraryNeverAdoptsReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "llvm")
	if err := os.WriteFile(path, []byte("original LLVM"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	pin, err := pinLLVMLibraryFile(path, uint64(stat.Dev), stat.Ino)
	if err != nil {
		t.Fatal(err)
	}
	defer pin.file.Close()
	replacement := path + ".new"
	if err := os.WriteFile(replacement, []byte("replacement LLVM"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	got, err := pin.stamp()
	expected := sha256.Sum256([]byte("original LLVM"))
	if err == nil && got != hex.EncodeToString(expected[:]) {
		t.Fatal("adopted replacement instead of mapped image")
	}
	if _, err := pinLLVMLibraryFile(path, uint64(stat.Dev), stat.Ino); err == nil {
		t.Fatal("accepted replacement as mapped image")
	}
}
