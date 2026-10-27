package main

import (
	"elisacore/src/backend"
	"hash"
)

func writeNativeCacheTarget(hash hash.Hash, triple string) error {
	identity, err := backend.ResolveTargetIdentity(triple)
	if err != nil {
		return err
	}
	writeResolvedNativeCacheTarget(hash, identity)
	library, err := backend.LLVMLibraryIdentity()
	if err != nil {
		return err
	}
	testRunnerCacheWriteString(hash, "llvm-library-sha256="+library)
	return nil
}

func writeResolvedNativeCacheTarget(hash hash.Hash, identity backend.TargetIdentity) {
	testRunnerCacheWriteString(hash, "resolved-target-v1")
	testRunnerCacheWriteString(hash, identity.Triple)
	testRunnerCacheWriteString(hash, identity.CPU)
	testRunnerCacheWriteString(hash, identity.Features)
}
