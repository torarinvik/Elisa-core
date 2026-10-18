package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	valid := transcript.Key == artifact.key && len(transcript.Stdout) <= buildCacheStreamLimit && len(transcript.Stderr) <= buildCacheStreamLimit && transcript.StdoutSHA256 == buildCacheStreamHash(transcript.Stdout) && transcript.StderrSHA256 == buildCacheStreamHash(transcript.Stderr)
	return transcript, valid
}
