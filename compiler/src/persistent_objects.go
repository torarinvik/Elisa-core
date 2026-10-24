package main

import (
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
	session.EnableDiskCache(filepath.Join(root, "partitions"), stamp, 2<<30)
	return session
}
