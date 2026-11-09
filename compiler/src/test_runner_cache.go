package main

import (
	"elisacore/src/smt"
	"hash"
	"os"
	"os/exec"
	"strings"
)

type testRunnerCacheArtifact struct {
	key        string
	dir        string
	executable string
}

func testRunnerCacheEnabled() bool {
	return strings.TrimSpace(os.Getenv("ELISACORE_TEST_CACHE")) != "0" && !testRunnerCacheObserverEnabled()
}

// Test-runner cache hits skip some or all work from the current request. Keep
// them out of requests that asked to observe or validate that work.
func testRunnerCacheObserverEnabled() bool {
	if buildCacheRequestObserverEnabled() || testTimingEnabled() || testPhaseDebugEnabled() {
		return true
	}
	return os.Getenv("ELISACORE_KEEP_TEST_TEMP") != ""
}

func writeTestRunnerSMTCacheInputs(hash hash.Hash, enableSMT bool) error {
	if !enableSMT {
		return nil
	}
	solver := smt.ResolveBinary("")
	testRunnerCacheWriteString(hash, "smt-selector="+solver)
	if selected, err := exec.LookPath(solver); err == nil {
		stamp, stampErr := toolchainNativeContentStamp(selected)
		if stampErr != nil {
			return stampErr
		}
		testRunnerCacheWriteString(hash, "smt-executable="+selected)
		testRunnerCacheWriteString(hash, "smt-content="+stamp)
	} else {
		testRunnerCacheWriteString(hash, "smt-executable=unavailable")
	}
	return nil
}

func writeTestRunnerLinkAuditInputs(hash hash.Hash, targetTriple string) error {
	if !isMachOTriple(targetTriple) {
		return nil
	}
	nmPath, err := exec.LookPath("nm")
	if err != nil {
		testRunnerCacheWriteString(hash, "macho-nm=unavailable")
		return nil
	}
	stamp, err := toolchainNativeContentStamp(nmPath)
	if err != nil {
		return err
	}
	testRunnerCacheWriteString(hash, "macho-nm="+nmPath)
	testRunnerCacheWriteString(hash, "macho-nm-content="+stamp)
	return nil
}
