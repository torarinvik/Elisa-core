package main

import (
	"elisacore/src/backend"
	"elisacore/src/semantic"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	emitAST       = "ast"
	emitLowered   = "lowered"
	emitSemantic  = "semantic"
	emitFacts     = "facts"
	emitUnsafe    = "unsafe"
	emitProgress  = "progress"
	emitFmt       = "fmt"
	emitDoc       = "doc"
	emitInterface = "iface"
	emitDeps      = "deps"
	emitDepsJSON  = "deps-json"
	emitIR        = "ir"
	emitInterpret = "interpret"
	emitServe     = "serve"
	// emitRetainedServe keeps one whole-program analysis alive behind a unix socket (retained_server.go).
	emitRetainedServe = "retained-serve"
	emitTests         = "tests"
	emitBenches       = "benches"
	emitFixtures      = "fixtures"
	emitTest          = "test"
	emitTestRunner    = "test-runner"
	emitLLVM          = "llvm"
	emitPacked        = "packed"
	emitCBindCheck    = "c-bind-check"
	emitCBindJSON     = "c-bind-check-json"
	emitHeader        = "header"
	emitBitcode       = "bc"
	emitObject        = "obj"
	emitCArchive      = "c-archive"
	emitTokens        = "tokens"
)

type cliOptions struct {
	emit                string
	filename            string
	output              string
	addr                string
	filter              string
	unsafeBudget        string
	foreignFiles        []string
	linkFlags           []string
	linkNative          bool
	runNative           bool
	targetTriple        string
	debugInfo           bool
	recordTrace         bool
	debug               bool
	debugBreak          string
	debugBreakRaise     bool
	debugFormat         string
	debugTraceLimit     int
	debugFullTrace      bool
	debugContext        int
	debugRepl           bool
	debugSaveTrace      string
	profileUse          bool
	profileHotFunctions map[string]bool
	packedProfile       backend.PackedLoweringProfile
	optLevel            backend.OptimizationLevel
	hasOptLevel         bool
	strictPolicy        bool
	globalsStrict       bool
	perfStrict          bool
	proofStrict         bool
	warnUnused          bool
	strictExterns       bool
	explainProofs       bool
	explainHole         bool
	enableSMT           bool
	concurrencyStrict   bool
	progressStrict      bool
	requiresReport      bool
	flowLintMode        semantic.FlowLintMode
	lto                 backend.LTOMode
	targetCPU           string
}

// loadOptimizationProfile reads the intentionally small, compiler-facing profile contract.
// It is advisory input only: a malformed profile is rejected, while a valid but stale profile
// can at worst change optimization choices. The profile producer writes readable names because
// they remain useful across generic specialization; the backend also matches a specialization's
// base name before applying a hot attribute.
//
// Format:
//
//	ELISA_PGO_V1
//	hot <qualified-function-name>
//
// Blank lines are accepted. Unknown records are rejected so a truncated or accidentally mixed
// artifact can never be presented as a valid optimization profile.
func loadOptimizationProfile(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read optimization profile %q: %w", path, err)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "ELISA_PGO_V1" {
		return nil, fmt.Errorf("invalid optimization profile %q: expected ELISA_PGO_V1 header", path)
	}
	result := make(map[string]bool)
	for lineIndex, raw := range lines[1:] {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "hot" || fields[1] == "" {
			return nil, fmt.Errorf("invalid optimization profile %q at line %d: expected 'hot FUNCTION'", path, lineIndex+2)
		}
		result[fields[1]] = true
	}
	return result, nil
}

func parseArgs(args []string) (cliOptions, error) {
	// docs/90: the SMT discharge tier is ON by default (cheap — a single lazy z3 spawn,
	// only for obligations the bounded-linear prover declines; sound — only `unsat`
	// concludes; graceful — a missing solver latches off and falls back to the runtime
	// check). `-nosmt` opts out.
	// docs/85/90: prove-it-or-fail (`-strict`) is the DEFAULT — an obligation that cannot be
	// discharged statically (param refinement, `requires`, contract `ensure`) fails the build
	// rather than degrading to a debug runtime check. `-permissive` opts back out to the
	// warning+runtime-check behavior. SMT (above) makes this default tolerable.
	options := cliOptions{emit: emitAST, enableSMT: true, proofStrict: true, packedProfile: backend.DefaultPackedLoweringProfile()}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-O0" || arg == "-O1" || arg == "-O2" || arg == "-O3":
			level, err := parseOptimizationArg(strings.TrimPrefix(arg, "-O"))
			if err != nil {
				return cliOptions{}, err
			}
			options.optLevel = level
			options.hasOptLevel = true
		case strings.HasPrefix(arg, "-O="):
			level, err := parseOptimizationArg(strings.TrimSpace(strings.TrimPrefix(arg, "-O=")))
			if err != nil {
				return cliOptions{}, err
			}
			options.optLevel = level
			options.hasOptLevel = true
		case arg == "-O":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -O")
			}
			level, err := parseOptimizationArg(strings.TrimSpace(args[i]))
			if err != nil {
				return cliOptions{}, err
			}
			options.optLevel = level
			options.hasOptLevel = true
		case strings.HasPrefix(arg, "-emit="):
			options.emit = strings.TrimSpace(strings.TrimPrefix(arg, "-emit="))
		case arg == "-emit":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -emit")
			}
			options.emit = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-addr="):
			options.addr = strings.TrimSpace(strings.TrimPrefix(arg, "-addr="))
		case arg == "-addr":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -addr")
			}
			options.addr = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-filter="):
			options.filter = strings.TrimSpace(strings.TrimPrefix(arg, "-filter="))
		case arg == "-filter":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -filter")
			}
			options.filter = strings.TrimSpace(args[i])
		case arg == "-g" || arg == "-debug-info":
			options.debugInfo = true
		case arg == "-ftrace" || arg == "-record-trace":
			options.recordTrace = true
		case arg == "-fprofile-use":
			if options.profileUse {
				return cliOptions{}, fmt.Errorf("-fprofile-use may be specified only once")
			}
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				return cliOptions{}, fmt.Errorf("missing path after -fprofile-use")
			}
			hotFunctions, profileErr := loadOptimizationProfile(strings.TrimSpace(args[i]))
			if profileErr != nil {
				return cliOptions{}, profileErr
			}
			options.profileUse = true
			options.profileHotFunctions = hotFunctions
		case arg == "-fbounds-check":
			// Force the debug index-bounds watchdog on regardless of optimization level.
			// The watchdog gate reads this env var at codegen time (build is in-process).
			_ = os.Setenv("ELISACORE_FORCE_BOUNDS_CHECK", "1")
		case arg == "-flto" || strings.HasPrefix(arg, "-flto=") || arg == "-fno-lto":
			// Link-time optimization. `-flto` = `-flto=full` (whole-program, one codegen
			// unit at link: Rust's `lto = "fat"`); `-flto=thin` uses ThinLTO. Applies to
			// linked native executables and makes `-emit obj` write an LTO bitcode object.
			mode, ok := backend.LTOOff, true
			if arg == "-flto" {
				mode = backend.LTOFull
			} else if arg != "-fno-lto" {
				mode, ok = backend.ParseLTOMode(strings.TrimPrefix(arg, "-flto="))
			}
			if !ok {
				return cliOptions{}, fmt.Errorf("unsupported %s (expected -flto, -flto=full or -flto=thin)", arg)
			}
			options.lto = mode
		case strings.HasPrefix(arg, "-mcpu=") || arg == "-mcpu":
			// Target CPU for code generation: `native` (the default for a host build),
			// `generic` (portable), or an LLVM CPU name. Read at codegen time (in-process).
			value := strings.TrimPrefix(arg, "-mcpu=")
			if arg == "-mcpu" {
				i++
				if i >= len(args) {
					return cliOptions{}, fmt.Errorf("missing value after -mcpu")
				}
				value = args[i]
			}
			value = backend.NormalizeTargetCPU(value)
			if value == "" {
				return cliOptions{}, fmt.Errorf("missing value after -mcpu")
			}
			options.targetCPU = value
			_ = os.Setenv(backend.TargetCPUEnv, value)
		case arg == "-ffast-math":
			// Opt the whole program into full fast-math FP (reassociation etc.), unlocking the
			// reassociated tree reduction for FP folds. Read at codegen time (in-process build).
			_ = os.Setenv("ELISACORE_FAST_MATH", "1")
		case arg == "-fstrict-fp":
			// Opt the whole program OUT of relaxed FP: no contraction, no reciprocal, no
			// fast-math anywhere. What `a + b * c` says is what it does, which is what a
			// program reproducing another implementation's FP results bit-for-bit needs.
			_ = os.Setenv("ELISACORE_STRICT_FP", "1")
		case arg == "-fnoalias":
			// Stamp LLVM `noalias` only on the eligible numeric-scalar `mutable T&` subset.
			// Mutable darray references are forwarded through nested calls and are deliberately
			// excluded. The opt-in stays OFF by default because a noalias miscompile is silent.
			// Read at codegen time (in-process build), same env the backend generator consults.
			_ = os.Setenv("ELISACORE_NOALIAS_MUTABLE_REFS", "1")
		case arg == "-Wperf":
			// Graduated strictness (docs/70): promote the performance-friction lints
			// (pointer-graph, allocation churn) from warnings to hard errors for shipped code.
			options.perfStrict = true
		case arg == "-Wflow":
			// docs/121: the flow-checked-loop lints — messy scanner/parser loops that hide an
			// untyped state machine, scatter cursor advancement, or pyramid their branches. Phase A
			// is warn-only behind this dial; the eventual default is strict (downgraded to warn by
			// -permissive). The `can ComplexFlow:` grant silences a covered loop at any level.
			options.flowLintMode = semantic.FlowLintWarn
		case arg == "-Wflow-strict":
			// Explicit strict flow lints (hard errors) — the Phase C default, available now for
			// projects that want to ban the anti-patterns outright before the default flips.
			options.flowLintMode = semantic.FlowLintStrict
		case arg == "-Wunused":
			// docs/119 W1: warn when a statement-position expression's non-void value is
			// silently discarded. Opt-in — the corpus has many side-effecting calls that
			// also return a value, so this is a tidiness channel, not a default lint.
			options.warnUnused = true
		case arg == "-requires-report":
			// docs c3: per requires-bearing function, report how many direct call sites statically
			// discharge the precondition vs fall back to a runtime check (with the unprovable sites).
			// Surfaces the blast radius of adding a `requires` to a hot function before committing.
			// Pure observability — does NOT change any error/lint.
			options.requiresReport = true
		case arg == "-Wconcurrency":
			// Graduated strictness (docs/09): promote legacy raw-concurrency migration
			// diagnostics to hard errors for strict-mode code.
			options.concurrencyStrict = true
		case arg == "-Wglobals":
			// Global state as a first-class effect: reading a `global` requires
			// can[Global.Read], writing one requires can[Global.Write], and both
			// propagate to callers exactly as Memory.Allocate does. The refs are
			// already inferred at every access; without this dial an INFERRED one is
			// dropped rather than demanded, so only a hand-declared `can[Global...]`
			// row is ever enforced. Reading a `const` stays pure — it is not a
			// `global` and carries no effect. Off by default: turning it on names
			// every function that touches process-wide state, which is the point and
			// is also a large diff for an existing codebase.
			options.globalsStrict = true
		case arg == "-Wprogress":
			// docs/102: activate the (already-built but dormant) progress-safety obligation
			// checker on its own dial — every `while` loop / recursive cycle must show progress
			// evidence (`decreases`, `Progress.*`) or declare intentional non-progress.
			options.progressStrict = true
		case arg == "-strict":
			// docs/85: prove-it-or-fail — a refinement/contract that cannot be discharged
			// statically fails the build (Dafny-like). ON by default now; kept as the explicit
			// opt-in / harmless no-op for back-compat.
			options.proofStrict = true
		case arg == "-permissive":
			// Opt out of default-on strict proofs: an unprovable refinement/contract degrades to
			// a warning + debug runtime check instead of failing the build.
			options.proofStrict = false
		case arg == "-strict-externs":
			// Opt-in extern contract discipline: every extern must declare a `requires`/`ensure`
			// boundary contract or be `@trusted("reason")`. NOT implied by -strict (see RequireExternContracts).
			options.strictExterns = true
		case arg == "--explain" || arg == "-explain":
			// docs/85 observability: after analysis, print every refinement-discharge decision
			// (proven / refuted / runtime) so the user can audit what is statically guaranteed.
			options.explainProofs = true
		case arg == "--explain-hole":
			// docs/98: opt into constructive proof-hole hints for plain asserts.
			options.explainHole = true
		case arg == "-smt":
			// docs/90: the SMT discharge tier (for obligations the bounded-linear prover declines
			// — non-linear products, richer boolean bodies). ON by default now; this flag is the
			// explicit opt-in and is kept as a harmless no-op for back-compat.
			options.enableSMT = true
		case arg == "-nosmt":
			// Opt out of the default-on SMT discharge tier (e.g. to avoid the z3 dependency or
			// pin a build to the bounded-linear prover only).
			options.enableSMT = false
		case arg == "-Wstrict":
			// Unified strictness: turn on the shipped safety/performance proof levers together.
			options.strictPolicy = true
			options.globalsStrict = true
			options.perfStrict = true
			options.concurrencyStrict = true
			options.progressStrict = true
			options.proofStrict = true
		case arg == "-debug":
			options.debug = true
		case arg == "-debug-break-raise":
			options.debug = true
			options.debugBreakRaise = true
		case strings.HasPrefix(arg, "-debug-break="):
			options.debug = true
			options.debugBreak = strings.TrimSpace(strings.TrimPrefix(arg, "-debug-break="))
		case arg == "-debug-break":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -debug-break")
			}
			options.debug = true
			options.debugBreak = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-debug-format="):
			options.debug = true
			options.debugFormat = strings.TrimSpace(strings.TrimPrefix(arg, "-debug-format="))
		case arg == "-debug-format":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -debug-format")
			}
			options.debug = true
			options.debugFormat = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-debug-trace-limit="):
			options.debug = true
			value := strings.TrimSpace(strings.TrimPrefix(arg, "-debug-trace-limit="))
			limit, err := strconv.Atoi(value)
			if err != nil || limit < 0 {
				return cliOptions{}, fmt.Errorf("invalid -debug-trace-limit %q", value)
			}
			options.debugTraceLimit = limit
		case arg == "-debug-trace-limit":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -debug-trace-limit")
			}
			options.debug = true
			limit, err := strconv.Atoi(strings.TrimSpace(args[i]))
			if err != nil || limit < 0 {
				return cliOptions{}, fmt.Errorf("invalid -debug-trace-limit %q", args[i])
			}
			options.debugTraceLimit = limit
		case arg == "-debug-full-trace":
			options.debug = true
			options.debugFullTrace = true
		case arg == "-debug-repl":
			options.debug = true
			options.debugRepl = true
		case strings.HasPrefix(arg, "-debug-context="):
			options.debug = true
			value := strings.TrimSpace(strings.TrimPrefix(arg, "-debug-context="))
			context, err := strconv.Atoi(value)
			if err != nil || context < 0 {
				return cliOptions{}, fmt.Errorf("invalid -debug-context %q", value)
			}
			options.debugContext = context
		case arg == "-debug-context":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -debug-context")
			}
			options.debug = true
			context, err := strconv.Atoi(strings.TrimSpace(args[i]))
			if err != nil || context < 0 {
				return cliOptions{}, fmt.Errorf("invalid -debug-context %q", args[i])
			}
			options.debugContext = context
		case strings.HasPrefix(arg, "-debug-save-trace="):
			options.debug = true
			options.debugSaveTrace = strings.TrimSpace(strings.TrimPrefix(arg, "-debug-save-trace="))
			if options.debugSaveTrace == "" {
				return cliOptions{}, fmt.Errorf("missing value after -debug-save-trace")
			}
		case arg == "-debug-save-trace":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -debug-save-trace")
			}
			options.debug = true
			options.debugSaveTrace = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-unsafe-budget="):
			options.unsafeBudget = strings.TrimSpace(strings.TrimPrefix(arg, "-unsafe-budget="))
		case arg == "-unsafe-budget":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -unsafe-budget")
			}
			options.unsafeBudget = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-target-triple="):
			options.targetTriple = strings.TrimSpace(strings.TrimPrefix(arg, "-target-triple="))
		case arg == "-target-triple":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -target-triple")
			}
			options.targetTriple = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-packed-abi="):
			return cliOptions{}, fmt.Errorf("-packed-abi has been removed; use canonical packed lowering or enum-level @packed_profile(...) instead")
		case arg == "-packed-abi":
			return cliOptions{}, fmt.Errorf("-packed-abi has been removed; use canonical packed lowering or enum-level @packed_profile(...) instead")
		case strings.HasPrefix(arg, "-o="):
			options.output = strings.TrimSpace(strings.TrimPrefix(arg, "-o="))
		case arg == "-o":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -o")
			}
			options.output = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-link="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "-link="))
			if value == "" {
				return cliOptions{}, fmt.Errorf("missing value after -link")
			}
			options.linkFlags = append(options.linkFlags, value)
		case arg == "-link":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -link")
			}
			value := strings.TrimSpace(args[i])
			if value == "" {
				return cliOptions{}, fmt.Errorf("missing value after -link")
			}
			options.linkFlags = append(options.linkFlags, value)
		case strings.HasPrefix(arg, "-L="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "-L="))
			if value == "" {
				return cliOptions{}, fmt.Errorf("missing value after -L")
			}
			options.linkFlags = append(options.linkFlags, "-L"+value)
		case arg == "-L":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -L")
			}
			value := strings.TrimSpace(args[i])
			if value == "" {
				return cliOptions{}, fmt.Errorf("missing value after -L")
			}
			options.linkFlags = append(options.linkFlags, "-L"+value)
		case strings.HasPrefix(arg, "-l") && len(arg) > len("-l"):
			options.linkFlags = append(options.linkFlags, arg)
		case arg == "-l":
			i++
			if i >= len(args) {
				return cliOptions{}, fmt.Errorf("missing value after -l")
			}
			value := strings.TrimSpace(args[i])
			if value == "" {
				return cliOptions{}, fmt.Errorf("missing value after -l")
			}
			options.linkFlags = append(options.linkFlags, "-l"+value)
		case strings.HasPrefix(arg, "-"):
			return cliOptions{}, fmt.Errorf("unknown option %q", arg)
		default:
			if options.filename != "" {
				return cliOptions{}, fmt.Errorf("expected a single input file, got %q", arg)
			}
			options.filename = arg
		}
	}
	options.emit = normalizeEmitMode(options.emit)
	if options.emit == "" {
		return cliOptions{}, fmt.Errorf("emit mode cannot be empty")
	}
	if options.filename == "" && options.emit != emitServe && options.emit != emitRetainedServe {
		return cliOptions{}, fmt.Errorf("missing input file")
	}
	if options.addr == "" {
		options.addr = "127.0.0.1:8080"
	}
	if options.debugFormat == "" {
		options.debugFormat = "human"
	}
	switch strings.ToLower(options.debugFormat) {
	case "human", "jsonl", "llm":
		options.debugFormat = strings.ToLower(options.debugFormat)
	default:
		return cliOptions{}, fmt.Errorf("unsupported -debug-format %q (expected human, jsonl, or llm)", options.debugFormat)
	}
	if options.debugContext == 0 {
		options.debugContext = 1
	}
	return options, nil
}
