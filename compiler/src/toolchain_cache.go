package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Tool binaries are immutable for the lifetime of a compiler process. Cache their content
// digests so every native cache key can detect an in-place toolchain replacement without
// rereading clang/llc for every test runner. The path remains part of each caller's key too,
// so two different tools with identical bytes do not accidentally collide.
var toolchainStampCache sync.Map // canonical path -> content digest

func toolchainContentStamp(path string) (string, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if cached, ok := toolchainStampCache.Load(canonical); ok {
		return cached.(string), nil
	}
	file, err := os.Open(canonical)
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
	stamp := hex.EncodeToString(h.Sum(nil))
	actual, _ := toolchainStampCache.LoadOrStore(canonical, stamp)
	return actual.(string), nil
}
