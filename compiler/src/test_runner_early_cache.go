package main

// Early (source-level) test-runner cache.
//
// The inner cache (test_runner_cache.go) is keyed on the *generated runner source*
// (program + test-dispatch), which can only be produced after a full parse+analyze
// (the dispatch needs each test's analyzed permission refs). That means even a warm,
// fully-cached run still pays ~2.5s to parse+analyze the whole program just to reach
// the cache.
//
// The runner source is, however, fully determined by (program source, filter) plus
// the same build inputs. So this early cache keys on those directly and stores the
// already-built executable path plus the selected test list. On a hit we skip the
// front-end entirely and run the cached binary. debug/trace ARE part of the key, so
// a `-g` debugger build gets its own cached entry (debugger re-runs are snappy too).

import (
	"crypto/sha256"
	"elisacore/src/backend"
	"elisacore/src/easm"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type cachedTestCase struct {
	Name       string `json:"name"`
	SkipReason string `json:"skip,omitempty"`
}

type earlyTestCacheMeta struct {
	Executable string           `json:"exe"`
	Cases      []cachedTestCase `json:"cases"`
}

func earlyTestCacheEnabled() bool {
	// Shares the master switch with the inner cache.
	return testRunnerCacheEnabled()
}

// earlyTestCacheKey hashes everything that determines the built test executable for a
// given entry: the same build inputs the inner cache uses, but with the *source* and
// *filter* (which determine the generated runner) plus the debug/trace flags.
func earlyTestCacheKey(source []byte, filter string, easmModules []*easm.Module, foreignFiles []string, linkFlags []string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool) (string, error) {
	hash := sha256.New()
	if err := writeCommonTestRunnerCacheInputs(hash, easmModules, foreignFiles, linkFlags, optLevel, packedProfile, targetTriple); err != nil {
		return "", err
	}
	testRunnerCacheWriteBytes(hash, "source", source)
	testRunnerCacheWriteString(hash, "filter="+strings.TrimSpace(filter))
	testRunnerCacheWriteString(hash, fmt.Sprintf("debug=%t", debugInfo))
	testRunnerCacheWriteString(hash, fmt.Sprintf("trace=%t", traceInfo))
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func earlyTestCacheMetaPath(key string) (string, error) {
	root, err := testRunnerCacheRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(root), "test_runner_early", key+".json"), nil
}

// locateEarlyTestCache returns the cached metadata if the key is present AND the
// referenced executable still exists on disk.
func locateEarlyTestCache(key string) (earlyTestCacheMeta, bool) {
	metaPath, err := earlyTestCacheMetaPath(key)
	if err != nil {
		return earlyTestCacheMeta{}, false
	}
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return earlyTestCacheMeta{}, false
	}
	var meta earlyTestCacheMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return earlyTestCacheMeta{}, false
	}
	if strings.TrimSpace(meta.Executable) == "" {
		return earlyTestCacheMeta{}, false
	}
	if _, err := os.Stat(meta.Executable); err != nil {
		return earlyTestCacheMeta{}, false
	}
	return meta, true
}

func publishEarlyTestCache(key string, executable string, cases []cachedTestCase) {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(executable) == "" {
		return
	}
	metaPath, err := earlyTestCacheMetaPath(key)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		return
	}
	payload, err := json.Marshal(earlyTestCacheMeta{Executable: executable, Cases: cases})
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(metaPath), ".early-*.json")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return
	}
	_ = os.Rename(tmpName, metaPath)
}
