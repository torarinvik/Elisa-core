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
	"sync/atomic"
	"syscall"
	"time"

	"elisacore/src/ast"
	"elisacore/src/backend"
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

	// codegen is whether the current request generates code (set per request).
	codegen bool

	tokensFor []byte
	tokens    []lexer.Token

	// prefetch is a scratch parse of the request's source started before the
	// driver's own parse, so the two run concurrently; parse consumes it once.
	prefetch *retainedPrefetch
	// options are the analysis options of the retained state; a prefetch runs the
	// scratch declaration phase with these options when a retained state exists.
	options semantic.AnalyzeOptions

	// objects retains native object partitions across requests, so an edit
	// recompiles only the partitions whose bitcode changed.
	objects *backend.IncrementalObjectSession
}

func (rt *retainedRuntime) noteSource(filename string, src []byte, file *ast.File) {
	if rt == nil {
		return
	}
	rt.filename, rt.src, rt.file = filename, src, file
}

type retainedPrefetch struct {
	filename string
	src      []byte
	done     chan struct{}
	tokens   []lexer.Token
	file     *ast.File
	ok       bool
	// prepared is set when file was analyzed as an update's scratch; such a
	// file is handed only to the update, never as an unanalyzed parse.
	prepared *semantic.RetainedScratch
	key      string
}

// prefetchSource starts a private scratch parse of src. Only the request
// goroutine touches rt; the worker writes its own prefetch record and closes
// done before parse reads it.
func (rt *retainedRuntime) prefetchSource(filename string, src []byte) {
	if rt == nil {
		return
	}
	pf := &retainedPrefetch{filename: filename, src: src, done: make(chan struct{})}
	rt.prefetch = pf
	prepare := rt.state != nil && rt.filename == filename && os.Getenv("ELISACORE_RETAINED_PREPARE_SCRATCH") != "0"
	options, key := rt.options, rt.key
	go func() {
		defer close(pf.done)
		l := lexer.New(filename, src)
		pf.tokens = l.Tokenize()
		if len(l.Errors()) != 0 {
			return
		}
		p := parser.New(pf.tokens)
		pf.file = p.ParseFile(filename)
		pf.ok = len(p.Errors()) == 0
		if prepare && pf.ok {
			pf.prepared, pf.key = semantic.PrepareRetainedScratch(pf.file, options), key
		}
	}()
}

// dropPrefetch waits for and discards an unconsumed prefetch.
func (rt *retainedRuntime) dropPrefetch() {
	if rt == nil || rt.prefetch == nil {
		return
	}
	<-rt.prefetch.done
	rt.prefetch = nil
}

// parse returns a private parse of the current source. The tokens are lexed
// once per source; parser.New copies them, so parses never share state.
func (rt *retainedRuntime) parse() (*ast.File, bool) {
	file, ok, _ := rt.parseScratch("")
	return file, ok
}

// parseScratch is parse for an update's scratch: with the retained key it may
// return the prefetched parse with its declaration phase already run.
func (rt *retainedRuntime) parseScratch(key string) (*ast.File, bool, *semantic.RetainedScratch) {
	if pf := rt.prefetch; pf != nil {
		rt.prefetch = nil
		<-pf.done
		if pf.filename == rt.filename && bytes.Equal(pf.src, rt.src) && pf.file != nil {
			rt.tokens, rt.tokensFor = pf.tokens, rt.src
			if pf.prepared == nil {
				return pf.file, pf.ok, nil
			}
			if key != "" && pf.key == key {
				return pf.file, pf.ok, pf.prepared
			}
			// Analyzed as a scratch: not usable as a plain parse.
		}
	}
	if rt.tokensFor == nil || !bytes.Equal(rt.tokensFor, rt.src) {
		l := lexer.New(rt.filename, rt.src)
		rt.tokens = l.Tokenize()
		if len(l.Errors()) != 0 {
			rt.tokensFor = nil
			return nil, false, nil
		}
		rt.tokensFor = rt.src
	}
	p := parser.New(rt.tokens)
	file := p.ParseFile(rt.filename)
	return file, len(p.Errors()) == 0, nil
}

func (rt *retainedRuntime) analyze(file *ast.File, options semantic.AnalyzeOptions) *semantic.Result {
	if file == nil || file != rt.file {
		return semantic.AnalyzeWithOptions(file, options)
	}
	key := rt.filename + "\x00" + fmt.Sprintf("%#v", options)
	if rt.state != nil && rt.key == key {
		scratch, ok, prepared := rt.parseScratch(key)
		var result *semantic.Result
		err := fmt.Errorf("scratch parse failed")
		if ok {
			result, err = rt.state.UpdatePrepared(file, scratch, rt.src, prepared)
		}
		st := rt.state.LastStats
		if err != nil {
			fmt.Fprintf(rt.log, "[retained] full analysis: %v\n", err)
		} else {
			fmt.Fprintf(rt.log, "[retained] update: %d changed, %d shifted, %d late-invalidated; pairing %v, scratch %v (ahead %v), compare %v, diff %v, bodies %v, post %v, total %v\n", st.Changed, st.Shifted, st.Invalidated, st.Pairing.Round(time.Millisecond), st.Scratch.Round(time.Millisecond), st.ScratchPrepared, st.Compare.Round(time.Millisecond), st.Diff.Round(time.Millisecond), st.Bodies.Round(time.Millisecond), st.Post.Round(time.Millisecond), st.Total.Round(time.Millisecond))
			// Code generation consumes the updated analysis directly: on the stage1 seed
			// its LLVM IR matches a fresh analysis's for every edit kind the sweep covers
			// (TestRetainedUpdateGeneratesFreshIR is the unit form).
			// ELISACORE_RETAINED_SPLICED_CODEGEN=0 analyzes fresh before code generation.
			if len(result.Errors()) != 0 || !rt.codegen || os.Getenv("ELISACORE_RETAINED_SPLICED_CODEGEN") != "0" {
				return result
			}
			// No errors: code generation follows, and by default it must see a
			// fresh analysis. Update consumed file, so analyze a new parse; the driver
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
	rt.state, rt.key, rt.options = state, key, options
	fmt.Fprintf(rt.log, "[retained] full analysis %v\n", time.Since(start).Round(time.Millisecond))
	return result
}

// lastRequest is when the server last started answering a request (Unix nanoseconds).
var lastRequest atomic.Int64

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
	activeRetained = &retainedRuntime{log: log, objects: newPersistentObjectSession()}
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
		lastRequest.Store(time.Now().UnixNano())
		restoreGC := suspendRetainedGC()
		resp := runRetainedRequest(req)
		// Answer before collection resumes: re-enabling the collector on a heap far
		// past its goal starts marking at once, and the response encoding (megabytes
		// of diagnostics) would pay mark assists for it.
		_ = json.NewEncoder(w).Encode(resp)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		restoreGC()
		// Collect while the user edits, and hand the freed pages back. Keeping them
		// resident looked cheaper (no fresh page faults next request), but on a machine
		// under memory pressure the compressor swaps those idle gigabytes out and the
		// next request swaps them back in: an AST variant edit on the compiler tree took
		// 10-12.6 s keeping them and 7.7-9.5 s releasing them (24 GB, swap in use).
		debug.FreeOSMemory()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		fmt.Fprintf(log, "[retained] heap live %d MB, idle %d MB, released %d MB, sys %d MB\n", ms.HeapAlloc>>20, ms.HeapIdle>>20, ms.HeapReleased>>20, ms.Sys>>20)
		// The live heap between requests, for finding what a retained analysis keeps.
		for _, kv := range req.Env {
			if path, ok := strings.CutPrefix(kv, "ELISACORE_RETAINED_HEAPPROFILE="); ok && path != "" {
				if f, err := os.Create(path); err == nil {
					_ = pprof.Lookup("heap").WriteTo(f, 0)
					_ = f.Close()
				}
			}
		}
	})
	fmt.Fprintf(log, "[retained] serving on %s\n", socket)
	lastRequest.Store(time.Now().UnixNano())
	if idle := retainedIdleTimeout(); idle > 0 {
		go func() {
			for range time.Tick(time.Minute) {
				// A request in progress holds mu; the stamp is taken when it starts.
				if !mu.TryLock() {
					continue
				}
				expired := time.Since(time.Unix(0, lastRequest.Load())) > idle
				mu.Unlock()
				if expired {
					fmt.Fprintf(log, "[retained] idle for %v; exiting\n", idle)
					_ = os.Remove(socket)
					os.Exit(0)
				}
			}
		}()
	}
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
	activeRetained.codegen = requestGeneratesCode(req.Args)
	code := runCLI(req.Args, &stdout, &stderr)
	fmt.Fprintf(activeRetained.log, "[retained] %s -> %d in %v\n", strings.Join(req.Args, " "), code, time.Since(start).Round(time.Millisecond))
	if objects := activeRetained.objects; objects != nil {
		st := objects.Stats()
		fmt.Fprintf(activeRetained.log, "[retained] objects: %d emitted, %d reused; ir %v, owners %v, clone %v, bitcode %v, native %v, merge %v\n", st.PartitionsEmitted, st.PartitionsReused, st.IRTime.Round(time.Millisecond), st.OwnerMapTime.Round(time.Millisecond), st.CloneTime.Round(time.Millisecond), st.BitcodeTime.Round(time.Millisecond), st.NativeTime.Round(time.Millisecond), st.MergeTime.Round(time.Millisecond))
	}
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
	if analysisDiskCacheEnabled() {
		return 0, false
	}
	socket := os.Getenv("ELISACORE_RETAINED_SERVER")
	for _, arg := range args {
		if arg == emitRetainedServe || arg == emitServe {
			return 0, false
		}
	}
	if socket == "" {
		if socket = autoRetainedSocket(args); socket == "" || !ensureRetainedServer(socket, args[len(args)-1], stderr) {
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

// requestGeneratesCode reports whether args run code generation: an executable or
// an IR/object emit. Analysis-only emits can use the updated analysis directly.
func requestGeneratesCode(args []string) bool {
	for i, arg := range args {
		if arg == "-emit" && i+1 < len(args) {
			switch args[i+1] {
			case emitInterface, emitSemantic, emitFacts, emitUnsafe, emitProgress, emitDoc, emitDeps, emitDepsJSON, emitAST, emitLowered, emitHeader, emitCBindCheck, emitCBindJSON:
				return false
			}
			return true
		}
	}
	return true
}
