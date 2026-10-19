package main

// Retained-analysis server (-emit retained-serve -addr <unix socket>).
//
// It keeps the last whole-program analysis alive. Any invocation of this
// binary with ELISACORE_RETAINED_SERVER=<socket> forwards its arguments,
// working directory and environment to the server and exits with its status,
// so an unchanged build script gets the retained analysis for free.
//
// An edit that only changes function bodies is applied in place
// (semantic.RetainedAnalysis.Update) and its diagnostics are returned. When
// that result has no errors, the server analyzes the program fresh instead, so
// code generation never consumes spliced state. Anything else (signature or
// declaration edits, a different program or options) runs a fresh analysis.
// When the server is unreachable the client runs locally.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

type retainedRequest struct {
	Binary string   `json:"binary"`
	Args   []string `json:"args"`
	Cwd    string   `json:"cwd"`
	Env    []string `json:"env"`
}

type retainedResponse struct {
	Code   int    `json:"code"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// activeRetained is non-nil only inside the retained server process.
var activeRetained *retainedRuntime

type retainedRuntime struct {
	log io.Writer

	// The program the current request parsed (noteSource), if any.
	filename string
	src      []byte
	file     *ast.File

	key   string
	state *semantic.RetainedAnalysis
}

func (rt *retainedRuntime) noteSource(filename string, src []byte, file *ast.File) {
	if rt == nil {
		return
	}
	rt.filename, rt.src, rt.file = filename, src, file
}

func (rt *retainedRuntime) parse() (*ast.File, bool) {
	return parseProgramWithTokenCache(rt.filename, rt.src, io.Discard, nil)
}

func (rt *retainedRuntime) analyze(file *ast.File, options semantic.AnalyzeOptions) *semantic.Result {
	if file == nil || file != rt.file {
		return semantic.AnalyzeWithOptions(file, options)
	}
	key := rt.filename + "\x00" + fmt.Sprintf("%#v", options)
	if rt.state != nil && rt.key == key {
		// Splice into a private parse: the driver's file must stay untouched in
		// case the fresh path below has to analyze it for code generation.
		spliced, ok1 := rt.parse()
		pristine, ok2 := rt.parse()
		if ok1 && ok2 {
			result, err := rt.state.Update(spliced, pristine, rt.src)
			st := rt.state.LastStats
			if err == nil {
				fmt.Fprintf(rt.log, "[retained] update: %d changed, %d shifted; diff %v, bodies %v, total %v\n", st.Changed, st.Shifted, st.Diff.Round(time.Millisecond), st.Bodies.Round(time.Millisecond), st.Total.Round(time.Millisecond))
				if len(result.Errors()) != 0 {
					return result
				}
				fmt.Fprintf(rt.log, "[retained] no errors; analyzing fresh for code generation\n")
			} else {
				fmt.Fprintf(rt.log, "[retained] full analysis: %v\n", err)
			}
		}
	}
	rt.state = nil // release the old analysis before building the next one
	pristine, ok := rt.parse()
	if !ok {
		return semantic.AnalyzeWithOptions(file, options)
	}
	start := time.Now()
	result, state := semantic.AnalyzeRetained(file, pristine, rt.src, options)
	rt.state, rt.key = state, key
	fmt.Fprintf(rt.log, "[retained] full analysis %v\n", time.Since(start).Round(time.Millisecond))
	return result
}

func serveRetained(socket string, log io.Writer) error {
	if socket == "" || strings.Contains(socket, ":") && !strings.HasPrefix(socket, "/") {
		return fmt.Errorf("retained-serve needs -addr <unix socket path>")
	}
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer os.Remove(socket)
	activeRetained = &retainedRuntime{log: log}
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/run", func(w http.ResponseWriter, r *http.Request) {
		var req retainedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Binary != retainedBinaryIdentity() {
			// A server left running from another compiler build must not answer.
			http.Error(w, "compiler binary differs from the server's", http.StatusConflict)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		resp := runRetainedRequest(req)
		_ = json.NewEncoder(w).Encode(resp)
	})
	fmt.Fprintf(log, "[retained] serving on %s\n", socket)
	return http.Serve(listener, mux)
}

func runRetainedRequest(req retainedRequest) retainedResponse {
	var stdout, stderr bytes.Buffer
	saved := os.Environ()
	savedDir, _ := os.Getwd()
	setEnvironment(req.Env)
	defer func() {
		setEnvironment(saved)
		_ = os.Chdir(savedDir)
	}()
	if err := os.Chdir(req.Cwd); err != nil {
		return retainedResponse{Code: 1, Stderr: fmt.Sprintf("error: %s\n", err)}
	}
	start := time.Now()
	code := runCLI(req.Args, &stdout, &stderr)
	fmt.Fprintf(activeRetained.log, "[retained] %s -> %d in %v\n", strings.Join(req.Args, " "), code, time.Since(start).Round(time.Millisecond))
	return retainedResponse{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

func setEnvironment(env []string) {
	os.Clearenv()
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			_ = os.Setenv(k, v)
		}
	}
}

// forwardToRetainedServer runs this invocation on the retained server named by
// ELISACORE_RETAINED_SERVER. It reports false when there is no server to use,
// so the caller runs locally.
func forwardToRetainedServer(args []string, stdout, stderr io.Writer) (int, bool) {
	socket := os.Getenv("ELISACORE_RETAINED_SERVER")
	if socket == "" {
		return 0, false
	}
	for _, arg := range args {
		if arg == emitRetainedServe || arg == emitServe {
			return 0, false
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return 0, false
	}
	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ELISACORE_RETAINED_SERVER=") {
			env = append(env, kv)
		}
	}
	body, _ := json.Marshal(retainedRequest{Binary: retainedBinaryIdentity(), Args: args, Cwd: cwd, Env: env})
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	resp, err := client.Post("http://retained/run", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(stderr, "[retained] server unavailable (%v); compiling locally\n", err)
		return 0, false
	}
	defer resp.Body.Close()
	var out retainedResponse
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil {
		fmt.Fprintf(stderr, "[retained] bad server response (%s); compiling locally\n", resp.Status)
		return 0, false
	}
	io.WriteString(stdout, out.Stdout)
	io.WriteString(stderr, out.Stderr)
	return out.Code, true
}

// retainedBinaryIdentity names this executable's exact build: path, size and
// modification time, so a server built from older sources is never used.
func retainedBinaryIdentity() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil {
		return path
	}
	return fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano())
}
