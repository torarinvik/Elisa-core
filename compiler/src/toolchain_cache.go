package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	toolchainStampMu.Lock()
	defer toolchainStampMu.Unlock()

	for attempt := 0; attempt < 3; attempt++ {
		before, err := os.Stat(canonical)
		if err != nil {
			return "", err
		}
		if !before.Mode().IsRegular() {
			return "", fmt.Errorf("toolchain %q is not a regular file", canonical)
		}
		fingerprint, memoizable := toolchainFileFingerprint(before)
		if memoizable {
			if cached, ok := toolchainStampCache[canonical]; ok && cached.fingerprint == fingerprint {
				return cached.stamp, nil
			}
		}

		stamp, err := hashToolchainFile(canonical)
		if err != nil {
			return "", err
		}
		after, err := os.Stat(canonical)
		if err != nil {
			return "", err
		}
		if !after.Mode().IsRegular() || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			continue
		}
		afterFingerprint, afterMemoizable := toolchainFileFingerprint(after)
		if memoizable && afterMemoizable && fingerprint != afterFingerprint {
			// A package manager or user replaced the file while it was being hashed.
			// Retry against one stable file version instead of publishing a mixed stamp.
			continue
		}
		if afterMemoizable {
			toolchainStampCache[canonical] = toolchainStampCacheEntry{fingerprint: afterFingerprint, stamp: stamp}
		} else {
			delete(toolchainStampCache, canonical)
		}
		return stamp, nil
	}
	return "", fmt.Errorf("toolchain %q changed repeatedly while hashing", canonical)
}

func hashToolchainFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
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
