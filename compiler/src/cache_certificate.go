package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"elisacore/src/backend"
	"elisacore/src/smt"
)

type cacheCertificate struct {
	Version      int                          `json:"version"`
	PID          int                          `json:"pid"`
	Dependencies []cacheCertificateDependency `json:"dependencies"`
}

type cacheCertificateDependency struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Selector string `json:"selector,omitempty"`
}

// writeCacheInvocationCertificate is best-effort: inability to prove a complete
// tool closure only disables the outer cache. It never changes the compiler's
// result or diagnostics. O_EXCL also prevents following a pre-existing path.
func writeCacheInvocationCertificate(args []string, path string, certifiable bool) bool {
	if path == "" || !certifiable {
		return false
	}
	dependencies := make([]cacheCertificateDependency, 0, 5)
	add := func(selected, selector string, resolveSymlinks bool) bool {
		if selected == "" {
			return false
		}
		absolute, err := filepath.Abs(selected)
		if err != nil {
			return false
		}
		if resolveSymlinks {
			absolute, err = filepath.EvalSymlinks(absolute)
			if err != nil {
				return false
			}
		}
		stamp, err := toolchainContentStamp(absolute)
		if err != nil {
			return false
		}
		dependencies = append(dependencies, cacheCertificateDependency{
			Path: absolute, SHA256: stamp, Selector: selector,
		})
		return true
	}

	executable, err := os.Executable()
	if err != nil || !add(executable, "", false) {
		return false
	}
	llvmPath, llvmDigest, err := backend.LLVMLibraryCacheDependency()
	if err != nil {
		return false
	}
	dependencies = append(dependencies, cacheCertificateDependency{Path: llvmPath, SHA256: llvmDigest})

	// Include the selected solver even when this source did not happen to need a
	// proof. That makes appearance, replacement, and PATH reselection invalidate
	// any response whose proof behavior could depend on the solver.
	solver := smt.ResolveBinary("")
	if strings.ContainsRune(solver, filepath.Separator) && !filepath.IsAbs(solver) {
		// The outer wrapper may be called from a different working directory;
		// a relative explicit solver path cannot be safely re-resolved there.
		return false
	}
	selectedSolver, err := exec.LookPath(solver)
	if err != nil || !add(selectedSolver, solver, false) {
		// A missing solver can turn a transient proof failure into a cached
		// rejection. Until the certificate format can represent absent tools,
		// refuse the whole response instead of leaving that dependency implicit.
		return false
	}

	if cacheEmitMode(args) == emitObject {
		// The in-process emitter can fall back to llc, so identify llc regardless
		// of which path this invocation ultimately used.
		llc, llcErr := findLLC()
		if llcErr != nil || !add(llc, "@llc", false) {
			return false
		}
		// Partition/native-fragment modes merge objects using the selected linker.
		// Conservatively include it for every object request; an unused linker
		// only causes a safe cache miss if it changes.
		merger, ok := cacheMergerPath()
		if !ok || !add(merger, "@merger", true) {
			return false
		}
	}

	data, err := json.Marshal(cacheCertificate{Version: 3, PID: os.Getpid(), Dependencies: dependencies})
	if err != nil {
		return false
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return false
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(path)
		return false
	}
	return true
}

func cacheEmitMode(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-emit=") {
			return normalizeEmitMode(strings.TrimSpace(strings.TrimPrefix(arg, "-emit=")))
		}
		if arg == "-emit" && i+1 < len(args) {
			return normalizeEmitMode(strings.TrimSpace(args[i+1]))
		}
	}
	return ""
}

func cacheMergerPath() (string, bool) {
	fields := strings.Fields(strings.TrimSpace(os.Getenv("ELISA_LD")))
	name := "ld"
	if len(fields) != 0 {
		name = fields[0]
	}
	selected, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	if len(fields) == 0 && runtime.GOOS == "darwin" && selected == "/usr/bin/ld" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, "/usr/bin/xcrun", "--find", "ld").Output()
		candidate := strings.TrimSpace(string(output))
		if err == nil && filepath.IsAbs(candidate) {
			if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
				selected = candidate
			}
		}
	}
	resolved, err := filepath.EvalSymlinks(selected)
	if err != nil {
		return "", false
	}
	return resolved, true
}
