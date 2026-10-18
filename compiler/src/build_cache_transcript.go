package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const buildCacheStreamLimit = 1 << 20

type buildCacheCapture struct {
	buffer   bytes.Buffer
	overflow bool
}

func (c *buildCacheCapture) Len() int      { return c.buffer.Len() }
func (c *buildCacheCapture) Bytes() []byte { return c.buffer.Bytes() }

func (c *buildCacheCapture) Write(p []byte) (int, error) {
	if c.overflow || len(p) > buildCacheStreamLimit-c.Len() {
		c.overflow = true
		return len(p), nil
	}
	return c.buffer.Write(p)
}

type buildCacheTranscript struct {
	ObjectSHA256               string
	Key                        string
	Stdout, Stderr             []byte
	StdoutSHA256, StderrSHA256 string
}

func buildCacheStreamHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func readBuildCacheTranscript(artifact buildCacheArtifact) (buildCacheTranscript, bool) {
	var transcript buildCacheTranscript
	f, err := os.Open(filepath.Join(artifact.dir, "transcript.json"))
	if err != nil {
		return transcript, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 3*buildCacheStreamLimit+1))
	if err != nil || len(data) > 3*buildCacheStreamLimit || json.Unmarshal(data, &transcript) != nil {
		return transcript, false
	}
	valid := len(transcript.ObjectSHA256) == 64 && transcript.Key == artifact.key && len(transcript.Stdout) <= buildCacheStreamLimit && len(transcript.Stderr) <= buildCacheStreamLimit && transcript.StdoutSHA256 == buildCacheStreamHash(transcript.Stdout) && transcript.StderrSHA256 == buildCacheStreamHash(transcript.Stderr)
	return transcript, valid
}

func buildCacheObjectHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func buildCacheObjectMatches(artifact buildCacheArtifact, transcript buildCacheTranscript) bool {
	if !usableCachedFile(artifact.object, false) {
		return false
	}
	digest, err := buildCacheObjectHash(artifact.object)
	return err == nil && digest == transcript.ObjectSHA256
}

// Validate the bytes while copying, then publish the output atomically. Neither
// corrupt cache bytes nor their diagnostic transcript reach the caller on miss.
func restoreBuildCacheObject(artifact buildCacheArtifact, output string, transcript buildCacheTranscript) error {
	in, err := os.Open(artifact.object)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(output), ".elisa-cache-restore-*")
	if err != nil {
		return err
	}
	defer func() { _ = out.Close(); _ = os.Remove(out.Name()) }()
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, hash), in); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != transcript.ObjectSHA256 {
		return fmt.Errorf("cached object checksum mismatch")
	}
	if err := out.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(out.Name(), output)
}
