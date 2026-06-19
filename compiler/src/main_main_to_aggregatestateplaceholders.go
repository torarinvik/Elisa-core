package main

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/backend"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}
func formatPostfixShorthandCastTarget(typ ast.TypeExpr) (string, bool) {
	switch n := typ.(type) {
	case *ast.OptionalTypeExpr:
		if target, ok := formatPostfixShorthandCastTarget(n.Value); ok {
			return target + "?", true
		}
	case *ast.NamedType:
		switch n.Name {
		case "void", "bool", "char", "int",
			"i8", "i16", "i32", "i64", "isize",
			"u8", "u16", "u32", "u64", "usize", "uintptr",
			"f32", "f64":
			return n.Name, true
		}
		if n.Name != "" {
			first := n.Name[0]
			if first >= 'A' && first <= 'Z' {
				return n.Name, true
			}
		}
	}
	return "", false
}
func runCLI(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) != 0 && isProjectCommand(args[0]) {
		return runProjectCLI(args, stdout, stderr)
	}
	options, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		printUsage(stderr)
		return 1
	}
	return runWithOptions(options, stdout, stderr)
}
func runWithOptions(options cliOptions, stdout io.Writer, stderr io.Writer) int {
	if options.emit == emitServe {
		if err := serveCompileServer(options.addr, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	}

	program, ok := loadProgramInput(options.filename, stderr)
	if !ok {
		return 1
	}
	return runLoadedProgramWithOptions(options, program, stdout, stderr)
}

var frontendTimingEnabled = os.Getenv("ELISACORE_BUILD_TIMING") != ""

func frontendTimingLog(label string, since time.Time) {
	if frontendTimingEnabled {
		fmt.Fprintf(os.Stderr, "[front-timing] %s: %v\n", label, time.Since(since).Round(time.Millisecond))
	}
}

func parseProgram(filename string, src []byte, stderr io.Writer) (*ast.File, bool) {
	lexStart := time.Now()
	l := lexer.New(filename, src)
	tokens := l.Tokenize()
	frontendTimingLog("lex", lexStart)
	if errs := l.Errors(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(stderr, "%s\n", e)
		}
		return nil, false
	}

	parseStart := time.Now()
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	frontendTimingLog("parse", parseStart)
	if warns := p.Notices(); len(warns) > 0 {
		for _, w := range warns {
			if shouldSuppressDeprecatedWarningsForTests(w) {
				continue
			}
			fmt.Fprintf(stderr, "%s\n", w)
		}
	}
	if errs := p.Errors(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(stderr, "%s\n", e)
		}
		return nil, false
	}
	return file, true
}
func analyzeProgram(filename string, src []byte, stderr io.Writer) (*ast.File, *semantic.Result, bool) {
	return analyzeProgramWithOptions(filename, src, stderr, semantic.AnalyzeOptions{})
}

func analyzeProgramWithOptions(filename string, src []byte, stderr io.Writer, options semantic.AnalyzeOptions) (*ast.File, *semantic.Result, bool) {
	file, ok := parseProgram(filename, src, stderr)
	if !ok {
		return nil, nil, false
	}

	analyzeStart := time.Now()
	result := semantic.AnalyzeWithOptions(file, options)
	frontendTimingLog("analyze", analyzeStart)
	if warns := result.Notices(); len(warns) > 0 {
		for _, w := range warns {
			if shouldSuppressDeprecatedWarningsForTests(w) {
				continue
			}
			fmt.Fprintf(stderr, "%s\n", w)
		}
	}
	if errs := result.Errors(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(stderr, "%s\n", e)
		}
		return nil, nil, false
	}

	return file, result, true
}
func emitSemanticWarningsIfNoErrors(file *ast.File, stderr io.Writer) {
	if file == nil {
		return
	}
	result := semantic.Analyze(file)
	if len(result.Errors()) != 0 {
		return
	}
	if warns := result.Notices(); len(warns) > 0 {
		for _, w := range warns {
			if shouldSuppressDeprecatedWarningsForTests(w) {
				continue
			}
			fmt.Fprintf(stderr, "%s\n", w)
		}
	}
}
func suppressDeprecatedWarningsForTests() bool {
	return os.Getenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS") == "1"
}
func shouldSuppressDeprecatedWarningsForTests(warning string) bool {
	if !suppressDeprecatedWarningsForTests() {
		return false
	}
	return strings.Contains(warning, "deprecated")
}

const (
	emitAST        = "ast"
	emitLowered    = "lowered"
	emitSemantic   = "semantic"
	emitFacts      = "facts"
	emitUnsafe     = "unsafe"
	emitProgress   = "progress"
	emitFmt        = "fmt"
	emitDoc        = "doc"
	emitInterface  = "iface"
	emitDeps       = "deps"
	emitDepsJSON   = "deps-json"
	emitIR         = "ir"
	emitInterpret  = "interpret"
	emitServe      = "serve"
	emitTests      = "tests"
	emitBenches    = "benches"
	emitFixtures   = "fixtures"
	emitTest       = "test"
	emitTestRunner = "test-runner"
	emitLLVM       = "llvm"
	emitPacked     = "packed"
	emitCBindCheck = "c-bind-check"
	emitCBindJSON  = "c-bind-check-json"
	emitHeader     = "header"
	emitBitcode    = "bc"
	emitObject     = "obj"
	emitCArchive   = "c-archive"
)

type cliOptions struct {
	emit              string
	filename          string
	output            string
	addr              string
	filter            string
	unsafeBudget      string
	foreignFiles      []string
	linkFlags         []string
	linkNative        bool
	runNative         bool
	targetTriple      string
	debugInfo         bool
	recordTrace       bool
	debug             bool
	debugBreak        string
	debugBreakRaise   bool
	debugFormat       string
	debugTraceLimit   int
	debugFullTrace    bool
	debugContext      int
	debugRepl         bool
	debugSaveTrace    string
	packedProfile     backend.PackedLoweringProfile
	optLevel          backend.OptimizationLevel
	hasOptLevel       bool
	strictPolicy      bool
	perfStrict        bool
	proofStrict       bool
	explainProofs     bool
	enableSMT         bool
	concurrencyStrict bool
}

func parseArgs(args []string) (cliOptions, error) {
	// docs/90: the SMT discharge tier is ON by default (cheap — a single lazy z3 spawn,
	// only for obligations the bounded-linear prover declines; sound — only `unsat`
	// concludes; graceful — a missing solver latches off and falls back to the runtime
	// check). `-nosmt` opts out.
	options := cliOptions{emit: emitAST, enableSMT: true, packedProfile: backend.DefaultPackedLoweringProfile()}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-O0" || arg == "-O2" || arg == "-O3":
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
		case arg == "-fbounds-check":
			// Force the debug index-bounds watchdog on regardless of optimization level.
			// The watchdog gate reads this env var at codegen time (build is in-process).
			_ = os.Setenv("ELISACORE_FORCE_BOUNDS_CHECK", "1")
		case arg == "-ffast-math":
			// Opt the whole program into full fast-math FP (reassociation etc.), unlocking the
			// reassociated tree reduction for FP folds. Read at codegen time (in-process build).
			_ = os.Setenv("ELISACORE_FAST_MATH", "1")
		case arg == "-fnoalias":
			// Stamp LLVM `noalias` on the eligible `mutable T&` param subset (scalar/darray
			// pointee). Sound (the alias checker makes a `mutable T&` the unique mutator of its
			// pointee) but OFF by default: a noalias miscompile is silent. Read at codegen time
			// (in-process build), same env the backend generator already consults.
			_ = os.Setenv("ELISACORE_NOALIAS_MUTABLE_REFS", "1")
		case arg == "-Wperf":
			// Graduated strictness (docs/70): promote the performance-friction lints
			// (pointer-graph, allocation churn) from warnings to hard errors for shipped code.
			options.perfStrict = true
		case arg == "-Wconcurrency":
			// Graduated strictness (docs/09): promote legacy raw-concurrency migration
			// diagnostics to hard errors for strict-mode code.
			options.concurrencyStrict = true
		case arg == "-strict":
			// docs/85: promote refinement-proof fallbacks from warnings to hard errors — a
			// refinement that cannot be discharged statically fails the build (Dafny-like).
			options.proofStrict = true
		case arg == "--explain" || arg == "-explain":
			// docs/85 observability: after analysis, print every refinement-discharge decision
			// (proven / refuted / runtime) so the user can audit what is statically guaranteed.
			options.explainProofs = true
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
			options.perfStrict = true
			options.concurrencyStrict = true
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
	if options.filename == "" && options.emit != emitServe {
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
func printUsage(w io.Writer) {
	emitModes := []string{emitAST, emitLowered, emitSemantic, emitFacts, emitUnsafe, emitProgress, emitFmt, emitDoc, emitInterface, emitDeps, emitDepsJSON, emitIR, emitInterpret, emitServe, emitTests, emitBenches, emitFixtures, emitTest, emitTestRunner, emitLLVM, emitPacked, emitCBindCheck, emitCBindJSON, emitHeader, emitBitcode, emitObject, emitCArchive}
	fmt.Fprintf(w, "Usage: elisacore [-emit %s] [-addr <host:port>] [-filter <substring>] [-target-triple <llvm-triple>] [-O0|-O2|-O3] [-o <output>] [-link <flag>|-L <dir>|-l <name>] <file%s|file%s|file%s>\n", strings.Join(emitModes, "|"), sourceExtension, interfaceExtension, frontendIRExtension)
	fmt.Fprintln(w, "       elisacore init <name> [--path <dir>] [--strict]")
	fmt.Fprintln(w, "       elisacore init-lib <name> [--path <dir>]")
	fmt.Fprintln(w, "       elisacore build|run|test|bench [target] [--project <dir|project.json>]")
	fmt.Fprintln(w, "       elisacore project view|deps [target] [--project <dir|project.json>] [--json]")
	fmt.Fprintf(w, "Packed enums lower canonically as handle-based %s in compiler mode; use @packed_profile(canonical|retained_reads|build_heavy) for supported enum-level tuning.\n", backend.PackedEnumABIVariantSparse)
}
func parseOptimizationArg(value string) (backend.OptimizationLevel, error) {
	switch strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(value), "O")) {
	case "0":
		return backend.OptimizationLevel0, nil
	case "2":
		return backend.OptimizationLevel2, nil
	case "3":
		return backend.OptimizationLevel3, nil
	default:
		return 0, fmt.Errorf("unsupported optimization level %q (expected O0, O2, or O3)", value)
	}
}
func effectiveOptimizationLevel(options cliOptions) backend.OptimizationLevel {
	if options.hasOptLevel {
		return options.optLevel
	}
	switch options.emit {
	case emitBitcode, emitObject, emitCArchive:
		return backend.OptimizationLevel3
	default:
		return backend.OptimizationLevel0
	}
}
func normalizeEmitMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case emitAST:
		return emitAST
	case emitLowered, "lower", "lowering", "grammar-lowered":
		return emitLowered
	case emitSemantic, "sema", "semantics", "lowered-semantic", "typed-report":
		return emitSemantic
	case emitFacts, "fact", "fact-trace", "trace-facts":
		return emitFacts
	case emitUnsafe, "unsafe-summary", "unsafe-report":
		return emitUnsafe
	case emitProgress, "progress-summary", "progress-report":
		return emitProgress
	case emitFmt, "format", "formatter":
		return emitFmt
	case emitDoc, "docs", "reference":
		return emitDoc
	case emitInterface, "interface", "api":
		return emitInterface
	case emitDeps, "dep", "dependencies":
		return emitDeps
	case emitDepsJSON, "depsjson", "dependencies-json":
		return emitDepsJSON
	case emitIR, "frontend-ir", "bundle":
		return emitIR
	case emitInterpret, "run", "interp":
		return emitInterpret
	case emitServe, "server":
		return emitServe
	case emitTests, "test-list":
		return emitTests
	case emitBenches, "bench-list":
		return emitBenches
	case emitFixtures, "fixture-list":
		return emitFixtures
	case emitTest, "run-test", "run-tests":
		return emitTest
	case emitTestRunner, "runner":
		return emitTestRunner
	case emitLLVM:
		return emitLLVM
	case emitPacked, "packed-info", "packedinfo":
		return emitPacked
	case emitCBindCheck, "cbind-check", "c-bind", "cbind":
		return emitCBindCheck
	case emitCBindJSON, "cbind-check-json", "c-bind-json", "cbind-json", "ffi-manifest", "abi-manifest":
		return emitCBindJSON
	case emitHeader:
		return emitHeader
	case emitBitcode, "bitcode":
		return emitBitcode
	case emitObject, "object":
		return emitObject
	case emitCArchive, "carchive", "archive", "static-archive", "staticlib", "static-lib":
		return emitCArchive
	default:
		return strings.TrimSpace(value)
	}
}
func emitSupportsFilter(emit string) bool {
	switch emit {
	case emitFacts, emitTests, emitBenches, emitFixtures, emitTest, emitTestRunner:
		return true
	default:
		return false
	}
}
func supportedFilterEmitModes() string {
	return fmt.Sprintf("%s, %s, %s, %s, %s, or %s", emitFacts, emitTests, emitBenches, emitFixtures, emitTestRunner, emitTest)
}
func outputPathForEmit(inputPath string, explicit string, ext string) string {
	if explicit != "" {
		return explicit
	}
	base := strings.TrimSuffix(inputPath, filepath.Ext(inputPath))
	if base == "" {
		return inputPath + ext
	}
	return base + ext
}
func readSourceWithIncludes(filename string, seen map[string]bool) ([]byte, error) {
	var out bytes.Buffer
	if err := writeSourceWithIncludesActive(&out, filename, seen, map[string]bool{}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func writeSourceWithIncludes(out *bytes.Buffer, filename string, seen map[string]bool) error {
	return writeSourceWithIncludesActive(out, filename, seen, map[string]bool{})
}

func writeSourceWithIncludesActive(out *bytes.Buffer, filename string, included map[string]bool, active map[string]bool) error {
	abs, err := filepath.Abs(filename)
	if err != nil {
		return err
	}
	if active[abs] {
		return fmt.Errorf("cyclic include detected for %s", abs)
	}
	if included[abs] {
		return nil
	}
	active[abs] = true
	defer delete(active, abs)

	raw, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	included[abs] = true

	out.Grow(len(raw))
	writeLineDirective(out, 1, abs)
	start := 0
	curLine := 1
	for start <= len(raw) {
		end := bytes.IndexByte(raw[start:], '\n')
		hasNewline := end >= 0
		if hasNewline {
			end += start
		} else {
			end = len(raw)
		}
		line := raw[start:end]
		if includePath, ok := parseIncludeDirectiveBytes(bytes.TrimSpace(line)); ok {
			indent := leadingWhitespaceBytes(line)
			var includeBuf bytes.Buffer
			outLenBefore := out.Len()
			// An absolute include path is used verbatim; filepath.Join would
			// concatenate it onto the current dir rather than honoring it.
			resolvedInclude := includePath
			if !filepath.IsAbs(resolvedInclude) {
				resolvedInclude = filepath.Join(filepath.Dir(abs), includePath)
			}
			if err := writeSourceWithIncludesActive(&includeBuf, resolvedInclude, included, active); err != nil {
				return err
			}
			writeIndentedInclude(out, includeBuf.Bytes(), indent)
			if out.Len() == outLenBefore || out.Bytes()[out.Len()-1] != '\n' {
				out.WriteByte('\n')
			}
			// Resume attribution to the parent file after the spliced include.
			writeLineDirective(out, curLine+1, abs)
		} else {
			out.Write(line)
			if hasNewline {
				out.WriteByte('\n')
			}
		}
		if !hasNewline {
			break
		}
		start = end + 1
		curLine++
	}
	return nil
}

// writeLineDirective emits a `#line <num> <path>` source-attribution marker at
// column 0. The lexer consumes these to retarget token positions to the real
// file/line after `include` flattens the tree into one buffer.
func writeLineDirective(out *bytes.Buffer, line int, file string) {
	fmt.Fprintf(out, "#line %d %s\n", line, file)
}

func leadingWhitespaceBytes(line []byte) []byte {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i]
}

func writeIndentedInclude(out *bytes.Buffer, data []byte, indent []byte) {
	if len(indent) == 0 || len(data) == 0 {
		out.Write(data)
		return
	}
	start := 0
	for start <= len(data) {
		end := bytes.IndexByte(data[start:], '\n')
		hasNewline := end >= 0
		if hasNewline {
			end += start
		} else {
			end = len(data)
		}
		if end > start {
			// `#line` source-attribution directives must stay at column 0 so the
			// lexer consumes them before indentation handling.
			if !bytes.HasPrefix(data[start:end], []byte("#line ")) {
				out.Write(indent)
			}
			out.Write(data[start:end])
		}
		if hasNewline {
			out.WriteByte('\n')
		}
		if !hasNewline {
			break
		}
		start = end + 1
	}
}
func parseIncludeDirective(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
	}
	for _, prefix := range []string{"include "} {
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
			return "", false
		}
		return rest[1 : len(rest)-1], true
	}
	if len(line) >= 4 && strings.HasPrefix(line, "{$") && strings.HasSuffix(line, "}") {
		body := strings.TrimSpace(line[2 : len(line)-1])
		keyword := body
		arg := ""
		if cut := strings.IndexFunc(body, unicode.IsSpace); cut >= 0 {
			keyword = body[:cut]
			arg = strings.TrimSpace(body[cut:])
		}
		switch strings.ToLower(keyword) {
		case "i", "include":
			if arg == "" {
				return "", false
			}
			if len(arg) >= 2 && ((arg[0] == '\'' && arg[len(arg)-1] == '\'') || (arg[0] == '"' && arg[len(arg)-1] == '"')) {
				return arg[1 : len(arg)-1], true
			}
			if strings.IndexFunc(arg, unicode.IsSpace) >= 0 {
				return "", false
			}
			return arg, true
		}
	}
	return "", false
}
func parseIncludeDirectiveBytes(line []byte) (string, bool) {
	return parseIncludeDirective(string(line))
}
func printFile(w io.Writer, f *ast.File) {
	fmt.Fprintf(w, "File: %s (%d declarations)\n", f.Filename, len(f.Decls))
	for _, d := range f.Decls {
		printDecl(w, d, 0)
	}
}
func printAnnotatedFunctions(w io.Writer, result *semantic.Result, emitMode string, filter string) {
	annotationName := annotationNameForEmitMode(emitMode)
	for _, fn := range selectAnnotatedFunctions(result, annotationName, filter) {
		signature := "<missing-signature>"
		if fn.Signature != nil {
			signature = fn.Signature.String()
		}
		fmt.Fprintf(w, "%s\t%s\n", fn.Name, signature)
	}
}
func annotationNameForEmitMode(emitMode string) string {
	switch emitMode {
	case emitTests:
		return "test"
	case emitBenches:
		return "bench"
	case emitFixtures:
		return "fixture"
	default:
		return ""
	}
}
func hasAnnotation(fn *semantic.AnnotatedFunc, annotationName string) bool {
	if fn == nil || annotationName == "" {
		return false
	}
	for _, annotation := range fn.Annotations {
		if annotation.Name == annotationName {
			return true
		}
	}
	return false
}
func ind(level int) string {
	return strings.Repeat("  ", level)
}
func formatFuncGenericParams(genericParams []ast.GenericParam, typeParams []string, regionParams []string, permissionParams []string) string {
	parts := make([]string, 0, len(genericParams)+len(typeParams)+len(regionParams)+len(permissionParams))
	if len(genericParams) != 0 {
		seenRegion := map[string]bool{}
		seenPermission := map[string]bool{}
		for _, param := range genericParams {
			switch param.Kind {
			case ast.GenericParamRegion:
				seenRegion[param.Name] = true
				parts = append(parts, "@"+param.Name)
			case ast.GenericParamPermission:
				seenPermission[param.Name] = true
				parts = append(parts, "permission "+param.Name)
			default:
				if param.InterfaceBound != "" {
					parts = append(parts, param.Name+": "+param.InterfaceBound)
				} else {
					parts = append(parts, param.Name)
				}
			}
		}
		for _, name := range regionParams {
			if !seenRegion[name] {
				parts = append(parts, "@"+name)
			}
		}
		for _, name := range permissionParams {
			if !seenPermission[name] {
				parts = append(parts, "permission "+name)
			}
		}
	} else {
		parts = append(parts, typeParams...)
		for _, name := range regionParams {
			parts = append(parts, "@"+name)
		}
		for _, name := range permissionParams {
			parts = append(parts, "permission "+name)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
func formatPermissionRefs(refs []ast.PermissionRef) string {
	if len(refs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		parts = append(parts, formatPermissionRef(ref))
	}
	return " can[" + strings.Join(parts, ", ") + "]"
}
func formatPermissionRef(ref ast.PermissionRef) string {
	if ref.Member != "" {
		return ref.Name + "." + ref.Member
	}
	return ref.Name
}
func formatAnnotation(annotation ast.Annotation) string {
	if len(annotation.Args) == 0 {
		return "@" + annotation.Name
	}
	return "@" + annotation.Name + "(" + strings.Join(annotation.Args, ", ") + ")"
}
func aggregateStatePlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "?"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
