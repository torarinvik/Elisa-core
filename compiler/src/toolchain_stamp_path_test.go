package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestToolchainStampSymlinkRetargetPreservedMetadata(t *testing.T) {
	dir := t.TempDir()
	a, b, selector := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "tool")
	for name, contents := range map[string]string{a: "tool-A", b: "tool-B"} {
		if err := os.WriteFile(name, []byte(contents), 0700); err != nil {
			t.Fatal(err)
		}
		when := time.Unix(1700000000, 0)
		if err := os.Chtimes(name, when, when); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(a, selector); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	first, err := toolchainContentStamp(selector)
	if err != nil {
		t.Fatal(err)
	}
	replacement := selector + ".new"
	if err := os.Symlink(b, replacement); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, selector); err != nil {
		t.Fatal(err)
	}
	second, err := toolchainContentStamp(selector)
	if err != nil || first == second || second != fmt.Sprintf("%x", sha256.Sum256([]byte("tool-B"))) {
		t.Fatalf("selector retarget reused wrong contents: first=%s second=%s err=%v", first, second, err)
	}
}

func TestToolchainStampPreservesSymlinkParentTraversal(t *testing.T) {
	dir := t.TempDir()
	top, actual := filepath.Join(dir, "top"), filepath.Join(dir, "actual")
	for _, path := range []string{top, filepath.Join(actual, "nested")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(top, "compiler"), []byte("wrong lexical parent"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actual, "compiler"), []byte("actual target parent"), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(top, "link")
	if err := os.Symlink(filepath.Join(actual, "nested"), link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	// filepath.Join/Clean would change this path's filesystem meaning.
	stamp, err := toolchainContentStamp(link + string(os.PathSeparator) + "../compiler")
	if err != nil || stamp != fmt.Sprintf("%x", sha256.Sum256([]byte("actual target parent"))) {
		t.Fatalf("tool path was lexically normalized across a symlink: %s %v", stamp, err)
	}
}

func TestToolchainHashPinsOpenedFileAcrossPathReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	if err := os.WriteFile(path, []byte("opened version"), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	replacement := path + ".new"
	if err := os.WriteFile(replacement, []byte("replacement version"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	stamp, err := hashOpenToolchainFile(file)
	if err != nil || stamp != fmt.Sprintf("%x", sha256.Sum256([]byte("opened version"))) {
		t.Fatalf("hash reopened the replaced pathname: %s %v", stamp, err)
	}
	selected, err := os.Stat(path)
	if err != nil || sameToolchainFileVersion(opened, selected) {
		t.Fatal("replacement was accepted as the descriptor's original version", err)
	}
}
