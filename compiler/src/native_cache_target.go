package main

import (
	"hash"
	"os"

	"elisacore/src/backend"
)

var nativeToolEnvironmentInputs = []string{
	"CPATH",
	"C_INCLUDE_PATH",
	"CPLUS_INCLUDE_PATH",
	"OBJC_INCLUDE_PATH",
	"LIBRARY_PATH",
	"SDKROOT",
	"DEVELOPER_DIR",
	"TOOLCHAINS",
	"MACOSX_DEPLOYMENT_TARGET",
	"IPHONEOS_DEPLOYMENT_TARGET",
	"TVOS_DEPLOYMENT_TARGET",
	"WATCHOS_DEPLOYMENT_TARGET",
	"XROS_DEPLOYMENT_TARGET",
	"CCC_OVERRIDE_OPTIONS",
	"COMPILER_PATH",
	"GCC_EXEC_PREFIX",
	"LDFLAGS",
	"CFLAGS",
	"CPPFLAGS",
	"LD_LIBRARY_PATH",
	"DYLD_LIBRARY_PATH",
	"DYLD_FRAMEWORK_PATH",
	"LD_PRELOAD",
	"DYLD_INSERT_LIBRARIES",
}

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
	if err := writeLLCCacheInputs(hash); err != nil {
		return err
	}
	for _, name := range nativeToolEnvironmentInputs {
		testRunnerCacheWriteBytes(hash, "native-tool-env:"+name, []byte(os.Getenv(name)))
	}
	return nil
}

func writeLLCCacheInputs(hash hash.Hash) error {
	path, err := findLLC()
	if err != nil {
		testRunnerCacheWriteString(hash, "llc=unavailable")
		return nil
	}
	stamp, err := toolchainContentStamp(path)
	if err != nil {
		return err
	}
	testRunnerCacheWriteString(hash, "llc="+path)
	testRunnerCacheWriteString(hash, "llc-content="+stamp)
	return nil
}

func writeResolvedNativeCacheTarget(hash hash.Hash, identity backend.TargetIdentity) {
	testRunnerCacheWriteString(hash, "resolved-target-v1")
	testRunnerCacheWriteString(hash, identity.Triple)
	testRunnerCacheWriteString(hash, identity.CPU)
	testRunnerCacheWriteString(hash, identity.Features)
}
