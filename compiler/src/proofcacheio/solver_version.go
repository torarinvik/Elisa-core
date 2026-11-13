package proofcacheio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

type solverVersionEntry struct {
	info    os.FileInfo
	version string
}

var solverVersions = struct {
	sync.Mutex
	entries map[string]solverVersionEntry
}{entries: make(map[string]solverVersionEntry)}

// SolverVersion caches the bounded version probe until the solver file changes.
func SolverVersion(solver string) (string, bool) {
	info, err := os.Stat(solver)
	if err != nil {
		return "", false
	}
	solverVersions.Lock()
	defer solverVersions.Unlock()
	if entry, ok := solverVersions.entries[solver]; ok && os.SameFile(entry.info, info) && entry.info.Size() == info.Size() && entry.info.ModTime().Equal(info.ModTime()) {
		return entry.version, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var version boundedVersionOutput
	command := exec.CommandContext(ctx, solver, "--version")
	command.WaitDelay = 500 * time.Millisecond
	command.Stdout = &version
	command.Stderr = io.Discard
	if command.Run() != nil {
		return "", false
	}
	text := version.String()
	solverVersions.entries[solver] = solverVersionEntry{info: info, version: text}
	return text, true
}

type boundedVersionOutput struct{ bytes.Buffer }

func (b *boundedVersionOutput) Write(payload []byte) (int, error) {
	if b.Len()+len(payload) > 4096 {
		return 0, fmt.Errorf("solver version output exceeds limit")
	}
	return b.Buffer.Write(payload)
}
