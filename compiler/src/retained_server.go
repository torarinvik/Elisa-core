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
	"os/signal"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strings"
	"sync"
	"syscall"
	"time"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
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

// retainedGCPercent is the server's GC percent; see serveRetained.
const retainedGCPercent = 200

// activeRetained is non-nil only inside the retained server process.
// retainedRequests numbers requests for ELISACORE_RETAINED_PROFILE_DIR profiles.
var retainedRequests int

var activeRetained *retainedRuntime

type retainedRuntime struct {
	log io.Writer

	// The program the current request parsed (noteSource), if any.
	filename string
	src      []byte
	file     *ast.File

	key   string
	state *semantic.RetainedAnalysis

	tokensFor []byte
	tokens    []lexer.Token
}

func (rt *retainedRuntime) noteSource(filename string, src []byte, file *ast.File) {
	if rt == nil {
		return
	}
	rt.filename, rt.src, rt.file = filename, src, file
}

// parse returns a private parse of the current source. The tokens are lexed
// once per source; parser.New copies them, so parses never share state.
func (rt *retainedRuntime) parse() (*ast.File, bool) {
	if rt.tokensFor == nil || !bytes.Equal(rt.tokensFor, rt.src) {
		l := lexer.New(rt.filename, rt.src)
		rt.tokens = l.Tokenize()
		if len(l.Errors()) != 0 {
			rt.tokensFor = nil
			return nil, false
		}
		rt.tokensFor = rt.src
	}
	p := parser.New(rt.tokens)
	file := p.ParseFile(rt.filename)
	return file, len(p.Errors()) == 0
}

func (rt *retainedRuntime) analyze(file *ast.File, options semantic.AnalyzeOptions) *semantic.Result {
	if file == nil || file != rt.file {
		return semantic.AnalyzeWithOptions(file, options)
	}
	key := rt.filename + "\x00" + fmt.Sprintf("%#v", options)
	if rt.state != nil && rt.key == key {
		scratch, ok := rt.parse()
		var result *semantic.Result
		err := fmt.Errorf("scratch parse failed")
		if ok {
			result, err = rt.state.Update(file, scratch, rt.src)
		}
		st := rt.state.LastStats
		if err != nil {
			fmt.Fprintf(rt.log, "[retained] full analysis: %v\n", err)
		} else {
			fmt.Fprintf(rt.log, "[retained] update: %d changed, %d shifted, %d late-invalidated; pairing %v, scratch %v, compare %v, diff %v, bodies %v, total %v\n", st.Changed, st.Shifted, st.Invalidated, st.Pairing.Round(time.Millisecond), st.Scratch.Round(time.Millisecond), st.Compare.Round(time.Millisecond), st.Diff.Round(time.Millisecond), st.Bodies.Round(time.Millisecond), st.Total.Round(time.Millisecond))
			if len(result.Errors()) != 0 {
				return result
			}
			// No errors: code generation follows, and it must see a fresh
			// analysis. Update consumed file, so analyze a new parse; the driver
			// takes the analyzed file from Result.File.
			fmt.Fprintf(rt.log, "[retained] no errors; analyzing fresh for code generation\n")
			fresh, ok := rt.parse()
			if !ok {
				return result
			}
			file = fresh
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
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		_ = os.Remove(socket)  // a stale socket would make clients try, fail and print a notice
		pprof.StopCPUProfile() // flush $ELISACORE_CPUPROFILE; a no-op when not profiling
		os.Exit(0)
	}()
	if _, set := os.LookupEnv("GOGC"); !set {
		// A one-shot compile trades memory for speed with a generous GC percent
		// (defaultGCPercent). The server keeps ~2.5 GB live across requests, so the
		// same percent would grow it past the soft memory limit, and the scavenger
		// would hand pages back that the next request faults in again. Collecting
		// at 3x live keeps the heap resident and warm.
		debug.SetGCPercent(retainedGCPercent)
	}
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
		restoreGC := suspendRetainedGC()
		resp := runRetainedRequest(req)
		restoreGC()
		_ = json.NewEncoder(w).Encode(resp)
		// Collect while the user edits: the next request then allocates from
		// memory this process already touched instead of faulting in fresh pages
		// (the default GC percent lets a retained heap grow for a long time).
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		fmt.Fprintf(log, "[retained] heap live %d MB, idle %d MB, released %d MB, sys %d MB\n", ms.HeapAlloc>>20, ms.HeapIdle>>20, ms.HeapReleased>>20, ms.Sys>>20)
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
	if dir := os.Getenv("ELISACORE_RETAINED_PROFILE_DIR"); dir != "" {
		retainedRequests++
		if f, err := os.Create(fmt.Sprintf("%s/retained-%d.pprof", dir, retainedRequests)); err == nil {
			if pprof.StartCPUProfile(f) == nil {
				defer func() { pprof.StopCPUProfile(); f.Close() }()
			}
		}
	}
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

// retainedRequestHeadroom is how far a request may grow the heap past what the
// server keeps live before the collector runs at all. An incremental update
// allocates a few GB on top of a ~2.5 GB live heap; marking that heap mid-request
// cost more wall time than the update's own analysis.
const retainedRequestHeadroom = 8 << 30

// suspendRetainedGC turns proportional collection off for one request and leaves a
// memory limit as the only trigger, so a request that allocates far more than an
// update (the first full analysis) still collects instead of exhausting memory.
// The handler collects after responding.
func suspendRetainedGC() func() {
	if _, set := os.LookupEnv("GOGC"); set {
		return func() {}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	prevLimit := debug.SetMemoryLimit(int64(ms.HeapAlloc) + retainedRequestHeadroom)
	prevPercent := debug.SetGCPercent(-1)
	return func() {
		debug.SetGCPercent(prevPercent)
		debug.SetMemoryLimit(prevLimit)
	}
}
