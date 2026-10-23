package main

// Implicit retained server. A compile of a large program with ELISACORE_RETAINED_SERVER
// unset finds the server that holds that program's analysis, or starts one, so running
// the same command again re-checks only what changed. Each server belongs to one
// compiler build and one entry file and exits after ELISACORE_RETAINED_IDLE without a
// request (30 minutes by default). ELISACORE_RETAINED_SERVER= (set, empty) always
// compiles locally; ELISACORE_RETAINED_SERVER=<socket> uses that server only.

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// retainedAutoMinBytes is the smallest program (with its includes) worth a resident
// server: below it a local compile is already quick and a server only holds memory.
const retainedAutoMinBytes = 1 << 20

const retainedAutoIdle = 30 * time.Minute

// autoRetainedSocket names the implicit server for this invocation, or "" when it
// should compile locally.
func autoRetainedSocket(args []string) string {
	if _, set := os.LookupEnv("ELISACORE_RETAINED_SERVER"); set {
		return ""
	}
	entry := ""
	for i, arg := range args {
		switch arg {
		case "-h", "-help", "--help", "-version", "--version":
			return ""
		case "-emit":
			if i+1 < len(args) {
				switch args[i+1] {
				case emitRetainedServe, emitServe, "fmt", "tokens":
					return ""
				}
			}
		}
		if strings.HasSuffix(arg, ".elisa") && (i == 0 || args[i-1] != "-o") {
			entry = arg
		}
	}
	if entry == "" || isProjectCommand(args[0]) {
		return ""
	}
	abs, err := filepath.Abs(entry)
	if err != nil {
		return ""
	}
	min := retainedAutoMinBytes
	if v, err := strconv.Atoi(os.Getenv("ELISACORE_RETAINED_AUTO_MIN")); err == nil {
		min = v
	}
	if src, err := readSourceWithIncludes(abs, map[string]bool{}); err != nil || len(src) < min {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(retainedBinaryIdentity() + "\x00" + abs))
	// Unix socket paths are short (104 bytes on macOS): a hash names the server.
	return filepath.Join(home, ".elisac", "servers", fmt.Sprintf("%x.sock", sum[:8]))
}

// ensureRetainedServer starts the server for socket unless one answers there, and
// waits until it accepts connections.
func ensureRetainedServer(socket string, entry string, stderr io.Writer) bool {
	if dialRetained(socket) {
		return true
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
		return false
	}
	// One starter per socket: a concurrent invocation waits for the server it starts.
	lock := strings.TrimSuffix(socket, ".sock") + ".starting"
	if f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
		f.Close()
		defer os.Remove(lock)
	} else if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) < time.Minute {
		return waitForRetained(socket)
	} else {
		_ = os.Remove(lock) // left by a starter that died
	}
	logFile, err := os.OpenFile(strings.TrimSuffix(socket, ".sock")+".log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return false
	}
	defer logFile.Close()
	cmd := exec.Command(exe, "-emit", emitRetainedServe, "-addr", socket)
	cmd.Env = os.Environ()
	if os.Getenv("ELISACORE_RETAINED_IDLE") == "" {
		cmd.Env = append(cmd.Env, "ELISACORE_RETAINED_IDLE="+retainedAutoIdle.String())
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // outlives this invocation and its terminal
	if err := cmd.Start(); err != nil {
		return false
	}
	_ = cmd.Process.Release()
	fmt.Fprintf(stderr, "[retained] started a compile server for %s; this first build analyzes everything, later ones re-check what changed (log: %s)\n", entry, logFile.Name())
	return waitForRetained(socket)
}

func waitForRetained(socket string) bool {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if dialRetained(socket) {
			return true
		}
	}
	return false
}

func dialRetained(socket string) bool {
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// retainedIdleTimeout is how long a server waits for a request before exiting; 0 keeps
// it running.
func retainedIdleTimeout() time.Duration {
	d, err := time.ParseDuration(os.Getenv("ELISACORE_RETAINED_IDLE"))
	if err != nil || d < 0 {
		return 0
	}
	return d
}
