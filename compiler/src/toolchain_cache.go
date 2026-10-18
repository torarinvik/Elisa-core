package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
)

type toolchainStampCacheEntry struct {
	fingerprint string
	stamp       string
}

// Cache content digests while the file identity and metadata stay unchanged. Hashing a
// multi-megabyte clang binary on every test-runner lookup is expensive, but caching by path
// forever can serve an object built by a replaced tool. Unix stat data includes device,
// inode and ctime, so atomic replacements and in-place writes invalidate the memo even when
// size and mtime are deliberately preserved. On platforms without that identity data we
// hash every time rather than trusting a weaker fingerprint.
var (
	toolchainStampMu    sync.Mutex
	toolchainStampCache = make(map[string]toolchainStampCacheEntry)
)

func toolchainContentStamp(path string) (string, error) {
	toolchainStampMu.Lock()
	defer toolchainStampMu.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		// Stat follows the selector in one filesystem lookup. Do not lexically
		// normalize paths: a symlink followed by /.. has filesystem semantics.
		before, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if !before.Mode().IsRegular() {
			return "", fmt.Errorf("toolchain %q is not a regular file", path)
		}
		fingerprint, memoizable := toolchainFileFingerprint(before)
		if memoizable {
			if cached, ok := toolchainStampCache[path]; ok && cached.fingerprint == fingerprint {
				return cached.stamp, nil
			}
		}
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		opened, err := file.Stat()
		if err != nil {
			file.Close()
			return "", err
		}
		if !sameToolchainFileVersion(before, opened) {
			file.Close()
			continue
		}
		// Hash the descriptor whose identity was checked, not a newly opened path.
		stamp, hashErr := hashOpenToolchainFile(file)
		after, statErr := file.Stat()
		closeErr := file.Close()
		if hashErr != nil {
			return "", hashErr
		}
		if statErr != nil {
			return "", statErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		selected, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if !sameToolchainFileVersion(opened, after) || !sameToolchainFileVersion(after, selected) {
			continue
		}
		afterFingerprint, afterMemoizable := toolchainFileFingerprint(after)
		if afterMemoizable {
			toolchainStampCache[path] = toolchainStampCacheEntry{fingerprint: afterFingerprint, stamp: stamp}
		} else {
			delete(toolchainStampCache, path)
		}
		return stamp, nil
	}
	return "", fmt.Errorf("toolchain %q changed repeatedly while hashing", path)
}

func sameToolchainFileVersion(a, b os.FileInfo) bool {
	if a == nil || b == nil || !a.Mode().IsRegular() || !b.Mode().IsRegular() || !os.SameFile(a, b) || a.Size() != b.Size() || !a.ModTime().Equal(b.ModTime()) {
		return false
	}
	first, firstStable := toolchainFileFingerprint(a)
	second, secondStable := toolchainFileFingerprint(b)
	return !firstStable || !secondStable || first == second
}

func hashOpenToolchainFile(file *os.File) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func toolchainFileFingerprint(info os.FileInfo) (string, bool) {
	if info == nil || (runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "freebsd") {
		return "", false
	}
	stat := reflect.ValueOf(info.Sys())
	if stat.Kind() == reflect.Pointer {
		stat = stat.Elem()
	}
	if !stat.IsValid() || stat.Kind() != reflect.Struct {
		return "", false
	}
	var fields []string
	hasDevice, hasInode, hasChangeTime := false, false, false
	for index := 0; index < stat.NumField(); index++ {
		fieldInfo := stat.Type().Field(index)
		name := fieldInfo.Name
		lower := strings.ToLower(name)
		// Access time can change simply because another process executed the tool. It is
		// irrelevant to the file contents and would defeat memoization on atime-enabled FSs.
		if strings.Contains(lower, "atim") || strings.Contains(lower, "accesstime") {
			continue
		}
		field := stat.Field(index)
		if !field.CanInterface() {
			continue
		}
		fields = append(fields, name+"="+fmt.Sprint(field.Interface()))
		switch name {
		case "Dev", "Device":
			hasDevice = true
		case "Ino", "Inode":
			hasInode = true
		}
		if strings.Contains(lower, "ctim") || strings.Contains(lower, "changetime") {
			hasChangeTime = true
		}
	}
	if !hasDevice || !hasInode || !hasChangeTime {
		return "", false
	}
	return fmt.Sprintf("mode=%s|size=%d|mtime=%d|%s", info.Mode(), info.Size(), info.ModTime().UnixNano(), strings.Join(fields, "|")), true
}
