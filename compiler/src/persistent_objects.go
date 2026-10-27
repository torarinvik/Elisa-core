package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"elisacore/src/backend"
)

// The same partition layout and disk store are used by standalone invocations
// and retained servers. Only exact current bitcode can authorize partition reuse.
func newPersistentObjectSession() *backend.IncrementalObjectSession {
	session := backend.NewIncrementalObjectSession(backend.NativeObjectPartitions(), 4<<30)
	if analysisDiskCacheEnabled() {
		session.UseDiskIncrementalDefaults()
	}
	if !buildCacheEnabled() || os.Getenv("ELISACORE_PARTITION_CACHE") == "0" {
		return session
	}
	root, err := buildCacheRoot()
	if err != nil {
		return session
	}
	stamp, err := compilerSourceStamp()
	if err != nil {
		return session
	}
	library, err := backend.LLVMLibraryIdentity()
	if err != nil {
		return session
	}
	hash := sha256.New()
	testRunnerCacheWriteString(hash, "partition-compiler-and-llvm-v1")
	testRunnerCacheWriteString(hash, stamp)
	testRunnerCacheWriteString(hash, library)
	session.EnableDiskCache(filepath.Join(root, "partitions"), hex.EncodeToString(hash.Sum(nil)), 2<<30)
	return session
}
