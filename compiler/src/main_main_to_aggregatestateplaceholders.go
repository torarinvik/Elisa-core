package main

import (
	"crypto/sha256"
	"elisacore/src/ast"
	"elisacore/src/backend"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func init() {
	semantic.SetSMTProofIdentityResolvers(analysisSourceIdentity, toolchainNativeContentStamp)
	semantic.SetSMTProofSolverIdentityResolver(smtSourceIdentity)
}

func main() {
	// A parallel -emit obj re-runs this executable as a partition worker; it exits here.
	backend.RunPartitionWorkerIfRequested()
	applyDefaultGCPercent()
	stop := startCPUProfile()
	stopMem := startMemProfile()
	code, forwarded, preloaded := tryBuildObjectCacheHitWithProgram(os.Args[1:], os.Stdout, os.Stderr)
	if !forwarded {
		code, forwarded = forwardToRetainedServer(os.Args[1:], os.Stdout, os.Stderr)
	}
	if !forwarded {
		code = runCLIWithProgram(os.Args[1:], os.Stdout, os.Stderr, preloaded)
	}
	stopMem()
	stop()
	writeCacheInvocationCertificate(os.Args[1:], os.Getenv("ELISACORE_CACHE_CERTIFICATE"), semantic.CacheInvocationCertifiable())
	os.Exit(code)
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
	return runCLIWithProgram(args, stdout, stderr, nil)
}

func runCLIWithProgram(args []string, stdout io.Writer, stderr io.Writer, preloaded *loadedProgram) int {
	if len(args) != 0 && isProjectCommand(args[0]) {
		return runProjectCLI(args, stdout, stderr)
	}
	options, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		printUsage(stderr)
		return 1
	}
	defaultSMTProofCacheDir()
	return runWithProgramOptions(options, stdout, stderr, preloaded)
}
func runWithOptions(options cliOptions, stdout io.Writer, stderr io.Writer) int {
	return runWithProgramOptions(options, stdout, stderr, nil)
}

func runWithProgramOptions(options cliOptions, stdout io.Writer, stderr io.Writer, preloaded *loadedProgram) (code int) {
	status := &analysisCommandStatus{Writer: stderr}
	stderr = status
	defer func() {
		if status.incomplete {
			code = analysisIncompleteExitCode
		}
	}()
	if err := configureCLIProfile(options); err != nil {
		fmt.Fprintf(stderr, "error: cannot configure optimization profile: %s\n", err)
		return 1
	}
	if options.emit == emitRetainedServe {
		if err := serveRetained(options.addr, stderr); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	}
	if options.emit == emitServe {
		if err := serveCompileServer(options.addr, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	}

	// Content-addressed build cache (docs/117): serve an identical `-emit obj`
	// result without reparsing/recompiling. On a miss it builds and publishes,
	// so it owns the whole build for that path.
	load := loadedProgramLoader(preloaded, loadProgramInput)
	if code, handled := tryBuildObjectCacheWith(options, stdout, stderr, load, runLoadedProgramWithOptions); handled {
		return code
	}

	program, ok := load(options.filename, stderr)
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
	return parseProgramWithTokenCache(filename, src, stderr, nil)
}

func parseProgramWithTokenCache(filename string, src []byte, stderr io.Writer, cache *sourceTokenCache) (*ast.File, bool) {
	var key sourceTokenKey
	wantTrace := false
	if cache != nil {
		replayStart := time.Now()
		key = sourceTokenKey{filename, sha256.Sum256(src)}
		file, ok, trace := cache.parsedFileWithKey(key, src)
		wantTrace = trace
		if ok {
			frontendTimingLog("parse", replayStart)
			return file, true
		}
	}
	var previous sourceTokenEntry
	if cache != nil {
		previous = cache.prefixCandidate(filename)
	}
	lexStart := time.Now()
	var tokens []lexer.Token
	var errs []string
	if cache == nil {
		l := lexer.New(filename, src)
		tokens = l.Tokenize()
		errs = l.Errors()
	} else {
		tokens, errs = cache.lexBorrowedWithKey(key, src)
	}
	frontendTimingLog("lex", lexStart)
	if len(errs) > 0 {
		lexer.DropParitySource(filename)
		for _, e := range errs {
			fmt.Fprintf(stderr, "%s\n", e)
		}
		return nil, false
	}

	parseStart := time.Now()
	var p *parser.Parser
	if cache == nil {
		p = parser.New(tokens)
	} else if wantTrace {
		p = parser.NewWithBorrowedTokensAndSyntaxTrace(tokens)
	} else {
		p = parser.NewWithBorrowedTokens(tokens)
	}
	var file *ast.File
	if cache != nil {
		cache.configureSuffixReplay(p, previous, tokens)
		if prefix, checkpoint, ok := cache.parsePrefix(previous, tokens); ok {
			file = p.ParseFileFromPrefix(filename, prefix, previous.trace, checkpoint)
		}
	}
	if file == nil {
		file = p.ParseFile(filename)
	}
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
		lexer.DropParitySource(filename)
		for _, e := range errs {
			fmt.Fprintf(stderr, "%s\n", e)
		}
		return nil, false
	}
	if cache != nil {
		cache.rememberParsedWithKey(key, file, p.SyntaxTrace(), len(p.Notices()) == 0)
	}
	return file, true
}
func analyzeProgram(filename string, src []byte, stderr io.Writer) (*ast.File, *semantic.Result, bool) {
	return analyzeProgramWithOptions(filename, src, stderr, semantic.AnalyzeOptions{})
}

func analyzeProgramWithOptions(filename string, src []byte, stderr io.Writer, options semantic.AnalyzeOptions) (*ast.File, *semantic.Result, bool) {
	return analyzeProgramWithAnalyzer(filename, src, stderr, options, semantic.AnalyzeWithOptions)
}

func analyzeProgramWithAnalyzer(filename string, src []byte, stderr io.Writer, options semantic.AnalyzeOptions, analyze func(*ast.File, semantic.AnalyzeOptions) *semantic.Result) (*ast.File, *semantic.Result, bool) {
	return analyzeProgramWithAnalyzerAndTokens(filename, src, stderr, options, nil, nil, analyze)
}

func analyzeProgramWithAnalyzerAndTokens(filename string, src []byte, stderr io.Writer, options semantic.AnalyzeOptions, cache *sourceTokenCache, retained *retainedRuntime, analyze func(*ast.File, semantic.AnalyzeOptions) *semantic.Result) (*ast.File, *semantic.Result, bool) {
	if retained != nil {
		retained.prefetchSource(filename, src)
		// A non-retained analyzer may not consume the prefetch. The retained
		// analyzer normally clears it in parseScratch; this deferred join also
		// covers parse errors, direct analyzers, and early returns.
		defer retained.dropPrefetch()
	}
	file, ok := parseProgramWithTokenCache(filename, src, stderr, cache)
	if !ok {
		return nil, nil, false
	}
	if retained != nil {
		retained.noteSource(filename, src, file)
	}

	analyzeStart := time.Now()
	result := analyze(file, options)
	if result == nil {
		return nil, nil, false
	}
	frontendTimingLog("analyze", analyzeStart)
	frontendSMTTimingLog(stderr, result)
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
	if result.File != nil {
		// The retained server may have analyzed its own parse of the same source.
		file = result.File
	}

	return file, result, true
}
func emitSemanticWarningsIfNoErrors(file *ast.File, stderr io.Writer) {
	emitSemanticWarningsIfNoErrorsWithSession(file, stderr, nil)
}

func emitSemanticWarningsIfNoErrorsWithSession(file *ast.File, stderr io.Writer, session *semantic.IncrementalAnalysisSession) {
	if file == nil {
		return
	}
	// The SMT discharge tier is ON by default (matching real builds), so this inspection/default pass
	// reports refinement provability the SAME way a build does. Without it, a refinement the build proves
	// (e.g. a nonlinear `a*b is Bounded[...]`) would surface a spurious "could not be proven statically"
	// warning here. Kept NON-strict: provability stays a warning (the AST/format still prints) rather than
	// a hard error. z3 is opened lazily — a file with no hard refinements never spawns it, so formatting
	// contract-free code costs nothing.
	result := analyzeSemanticWithSession(file, semantic.AnalyzeOptions{EnableSMT: true}, session)
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
