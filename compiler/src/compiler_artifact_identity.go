package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
)

// Pin the executable before main starts. Reopening os.Executable lazily can
// identify a newly installed compiler instead of the code running this process.
// Linux exposes the running image directly; other supported Unix hosts pin the
// executable path during initialization. Failure disables native artifact reuse.
var nativeCompilerArtifact, nativeCompilerArtifactErr = pinRunningCompilerArtifact()

type pinnedCompilerArtifact struct {
	file        *os.File
	fingerprint string
	mu          sync.Mutex
	digest      string
}

func pinRunningCompilerArtifact() (*pinnedCompilerArtifact, error) {
	path, err := os.Executable()
	if runtime.GOOS == "linux" {
		path, err = "/proc/self/exe", nil
	}
	if err != nil {
		return nil, err
	}
	return pinCompilerArtifact(path)
}

func pinCompilerArtifact(path string) (*pinnedCompilerArtifact, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	fingerprint, ok := toolchainFileFingerprint(info)
	if !ok || !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("cannot establish stable compiler artifact identity")
	}
	return &pinnedCompilerArtifact{file: file, fingerprint: fingerprint}, nil
}

func (artifact *pinnedCompilerArtifact) stamp() (string, error) {
	artifact.mu.Lock()
	defer artifact.mu.Unlock()
	unchanged := func() error {
		info, err := artifact.file.Stat()
		if err != nil {
			return err
		}
		fingerprint, ok := toolchainFileFingerprint(info)
		if !ok || fingerprint != artifact.fingerprint {
			return fmt.Errorf("pinned compiler artifact changed; native cache disabled")
		}
		return nil
	}
	if err := unchanged(); err != nil {
		return "", err
	}
	if artifact.digest != "" {
		return artifact.digest, nil
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(artifact.file, 0, 1<<63-1)); err != nil {
		return "", err
	}
	if err := unchanged(); err != nil {
		return "", err
	}
	artifact.digest = hex.EncodeToString(hash.Sum(nil))
	return artifact.digest, nil
}

func compilerSourceStamp() (string, error) {
	if nativeCompilerArtifactErr != nil {
		return "", nativeCompilerArtifactErr
	}
	if nativeCompilerArtifact == nil {
		return "", fmt.Errorf("running compiler identity unavailable")
	}
	return nativeCompilerArtifact.stamp()
}
