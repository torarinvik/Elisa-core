package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"elisacore/src/backend"
	"elisacore/src/frontendir"
	"elisacore/src/grammar"
	"elisacore/src/interpreter"
	"elisacore/src/semantic"
	"elisacore/src/unparse"
)

// tryEarlyCachedTestRun checks the source-level test cache for an already-built
// executable matching this program + filter + build flags. On a hit it runs the
// cached binary directly and returns (exitCode, true), skipping parse + analysis.
// On any miss/uncertainty it returns (_, false) so the normal path proceeds.
func tryEarlyCachedTestRun(program *loadedProgram, options cliOptions, stdout io.Writer, stderr io.Writer) (int, bool) {
	if !earlyTestCacheEnabled() || program == nil || program.file != nil {
		return 0, false
	}
	srcStart := time.Now()
	source, err := readSourceWithIncludes(program.filename, map[string]bool{})
	if err != nil {
		return 0, false
	}
	frontendTimingLog("early-source-read", srcStart)
	keyStart := time.Now()
	key, err := earlyTestCacheKey(source, options.filter, program.easm, options.foreignFiles, options.linkFlags, effectiveOptimizationLevel(options), options.packedProfile, options.targetTriple, options.debugInfo, options.recordTrace)
	frontendTimingLog("early-key", keyStart)
	if err != nil {
		return 0, false
	}
	meta, hit := locateEarlyTestCache(key)
	if !hit {
		return 0, false
	}
	writeTestPhaseLine(stderr, "emit_test", "early_cache_hit")
	// Honor ELISA_KEEP_TEST_BINARY on the early-cache fast path too. The cached binary is
	// already persistent (it lives in the cache dir, not a temp), but without printing its
	// path there is no way to map a (project) test run back to the exact executable it ran —
	// which is what you need to profile/lldb it. The full-compile path prints this; this fast
	// path silently skipped it, so a cached run gave no reliable handle on its binary.
	if os.Getenv("ELISA_KEEP_TEST_BINARY") != "" {
		fmt.Fprintf(stderr, "[ keep     ] test binary: %s\n", meta.Executable)
	}
	passed, skipped, failed, _ := runTestExecutableCases(meta.Executable, meta.Cases, options.targetTriple, stdout, stderr)
	fmt.Fprintf(stdout, "[ SUMMARY  ] %d test(s) selected; passed=%d skipped=%d failed=%d\n", len(meta.Cases), passed, skipped, failed)
	if failed > 0 {
		return 1, true
	}
	return 0, true
}

func runLoadedProgramWithOptions(options cliOptions, program *loadedProgram, stdout io.Writer, stderr io.Writer) int {
	if options.filter != "" && !emitSupportsFilter(options.emit) {
		fmt.Fprintf(stderr, "error: -filter is only supported for -emit %s\n", supportedFilterEmitModes())
		return 1
	}

	// Fast path: if an unchanged source already has a built test executable in the
	// early (source-level) cache, run it directly and skip the whole front-end
	// (parse + whole-program analysis), which otherwise re-runs on every invocation.
	if options.emit == emitTest {
		if code, handled := tryEarlyCachedTestRun(program, options, stdout, stderr); handled {
			return code
		}
	}

	switch options.emit {
	case emitAST:
		file, ok := parseLoadedProgram(program, stderr)
		if !ok {
			return 1
		}
		emitSemanticWarningsIfNoErrors(file, stderr)
		if options.output != "" {
			fmt.Fprintf(stderr, "error: -o is not supported for -emit %s\n", emitAST)
			return 1
		}
		printFile(stdout, file)
		return 0
	case emitTokens:
		if options.output != "" {
			fmt.Fprintf(stderr, "error: -o is not supported for -emit %s\n", emitTokens)
			return 1
		}
		return runEmitTokens(program, stdout, stderr)
	case emitLowered:
		file, ok := parseLoadedProgram(program, stderr)
		if !ok {
			return 1
		}
		lowered := unparse.FormatFile(grammar.LowerFileStandalone(file))
		outputPath := outputPathForEmit(program.filename, options.output, loweredExtension)
		if err := writeOutputFile(outputPath, []byte(lowered)); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	case emitFmt:
		file, ok := parseLoadedProgram(program, stderr)
		if !ok {
			return 1
		}
		emitSemanticWarningsIfNoErrors(file, stderr)
		formatted := unparse.FormatFile(file)
		if options.output != "" {
			if err := writeOutputFile(options.output, []byte(formatted)); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, formatted)
		}
		return 0
	case emitDoc:
		file, ok := parseLoadedProgram(program, stderr)
		if !ok {
			return 1
		}
		emitSemanticWarningsIfNoErrors(file, stderr)
		documentation := generateReferenceDoc(program.filename, file)
		if options.output != "" {
			if err := writeOutputFile(options.output, []byte(documentation)); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, documentation)
		}
		return 0
	case emitDeps, emitDepsJSON:
		report, err := buildSourceDependencyReport(program.filename, sourceExpandOptions{})
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		payload, err := formatSourceDependencyReport(report, options.emit == emitDepsJSON)
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		if options.output != "" {
			if err := writeOutputFile(options.output, []byte(payload)); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, payload)
		}
		return 0
	case emitIR:
		file, _, ok := analyzeLoadedProgramWithOptions(program, stderr, semanticOptionsForCLI(options))
		if !ok {
			return 1
		}
		encoded, err := frontendir.Encode(buildFrontendIRBundle(program, file))
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		outputPath := outputPathForEmit(program.filename, options.output, frontendIRExtension)
		if err := writeOutputFile(outputPath, encoded); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	case emitUnsafe:
		file, ok := parseLoadedProgram(program, stderr)
		if !ok {
			return 1
		}
		result := semantic.AnalyzeWithOptions(file, semantic.AnalyzeOptions{
			EnforceUnsafePermissions: true,
			TargetTriple:             options.targetTriple,
			TargetDebug:              effectiveOptimizationLevel(options) == backend.OptimizationLevel0,
		})
		if errs := result.Errors(); len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintf(stderr, "%s\n", e)
			}
			return 1
		}
		summary := collectUnsafeSummary(result)
		if err := checkUnsafeBudget(summary, options.unsafeBudget); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		report := generateUnsafeReportFromSummary(summary)
		if options.output != "" {
			if err := writeOutputFile(options.output, []byte(report)); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, report)
		}
		return 0
	case emitProgress:
		file, ok := parseLoadedProgram(program, stderr)
		if !ok {
			return 1
		}
		result := semantic.AnalyzeWithOptions(file, semantic.AnalyzeOptions{
			EnforceProgressSafety: true,
			TargetTriple:          options.targetTriple,
			TargetDebug:           effectiveOptimizationLevel(options) == backend.OptimizationLevel0,
		})
		if errs := result.Errors(); len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintf(stderr, "%s\n", e)
			}
			return 1
		}
		report := generateProgressReport(result)
		if options.output != "" {
			if err := writeOutputFile(options.output, []byte(report)); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, report)
		}
		return 0
	}

	_, result, ok := analyzeLoadedProgramWithOptions(program, stderr, semanticOptionsForCLI(options))
	if !ok {
		return 1
	}
	if options.explainProofs {
		printProofReport(stderr, result.ProofReport, options.explainHole)
		printElisionSummary(stderr, result.ElisionSummary)
		if line := result.SMTProfile.String(); line != "" {
			fmt.Fprintf(stderr, "  %s\n", line)
		}
	}
	if options.requiresReport {
		printRequiresReport(stderr, result.RequiresReport)
	}

	switch options.emit {
	case emitSemantic:
		report := generateSemanticReport(result)
		if options.output != "" {
			if err := writeOutputFile(options.output, []byte(report)); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, report)
		}
		return 0
	case emitFacts:
		report, err := generateFactTraceReport(result, options.filter)
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		if options.output != "" {
			if err := writeOutputFile(options.output, []byte(report)); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, report)
		}
		return 0
	case emitInterface:
		interfaceSource := generateModuleInterface(result.File)
		if options.output != "" {
			if err := writeOutputFile(options.output, []byte(interfaceSource)); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, interfaceSource)
		}
		return 0
	case emitTests, emitBenches, emitFixtures:
		if options.output != "" {
			fmt.Fprintf(stderr, "error: -o is not supported for -emit %s\n", options.emit)
			return 1
		}
		printAnnotatedFunctions(stdout, result, options.emit, options.filter)
		return 0
	case emitTestRunner:
		runnerSource, err := generateTestRunnerSource(program.filename, result, options.filter)
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		if options.output != "" {
			if err := writeOutputFile(options.output, []byte(runnerSource)); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, runnerSource)
		}
		return 0
	case emitTest:
		if options.output != "" {
			fmt.Fprintf(stderr, "error: -o is not supported for -emit %s\n", emitTest)
			return 1
		}
		writeTestPhaseLine(stderr, "emit_test", "selected_test_execution")
		return executeSelectedTests(program.filename, result, options.filter, options.foreignFiles, options.linkFlags, effectiveOptimizationLevel(options), options.packedProfile, options.targetTriple, options.debugInfo, options.recordTrace, stdout, stderr)
	case emitInterpret:
		if options.output != "" {
			fmt.Fprintf(stderr, "error: -o is not supported for -emit %s\n", emitInterpret)
			return 1
		}
		var debugger *interpreter.Debugger
		if options.debug {
			conditions := []interpreter.DebugCondition{}
			if strings.TrimSpace(options.debugBreak) != "" {
				conditions = append(conditions, interpreter.BreakWhenExpr(options.debugBreak))
			}
			session := interpreter.NewDebugSession(interpreter.DebuggerConfig{
				TraceLimit:   options.debugTraceLimit,
				FullTrace:    options.debugFullTrace,
				Context:      options.debugContext,
				BreakOnRaise: options.debugBreakRaise,
			}, conditions...)
			session.Run()
			debugger = session.Debugger
		}
		execResult, err := interpreter.Execute(result, interpreter.Options{Stdout: stdout, Debugger: debugger})
		if err != nil {
			if debugger != nil {
				var halt *interpreter.DebugHaltError
				if errors.As(err, &halt) {
					if options.debugRepl {
						if replErr := interpreter.RunDebugREPL(debugger.Session, os.Stdin, stdout, options.debugContext); replErr != nil {
							fmt.Fprintf(stderr, "error: %s\n", replErr)
						}
					}
					if saveErr := saveDebugTraceIfRequested(options, debugger); saveErr != nil {
						fmt.Fprintf(stderr, "error: %s\n", saveErr)
					}
					switch options.debugFormat {
					case "jsonl":
						if formatErr := interpreter.WriteDebugTraceJSONL(stdout, debugger); formatErr != nil {
							fmt.Fprintf(stderr, "error: %s\n", formatErr)
						}
					case "llm":
						fmt.Fprint(stdout, interpreter.FormatDebugContextForLLM(debugger, options.debugContext))
					default:
						fmt.Fprint(stderr, interpreter.FormatDebugHaltHuman(debugger))
					}
					return 1
				}
				if debugger.Session != nil {
					debugger.Session.MarkFailed(err, interpreter.DebugStopRuntimeError)
					if saveErr := saveDebugTraceIfRequested(options, debugger); saveErr != nil {
						fmt.Fprintf(stderr, "error: %s\n", saveErr)
					}
				}
			}
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		if debugger != nil && debugger.Session != nil {
			if options.debugRepl {
				for index, snapshot := range debugger.Trace {
					if snapshot.Event == interpreter.DebugAfterStmt {
						debugger.Seek(index)
						break
					}
				}
				if replErr := interpreter.RunDebugREPL(debugger.Session, os.Stdin, stdout, options.debugContext); replErr != nil {
					fmt.Fprintf(stderr, "error: %s\n", replErr)
					return 1
				}
			}
			debugger.Session.MarkCompleted()
			if saveErr := saveDebugTraceIfRequested(options, debugger); saveErr != nil {
				fmt.Fprintf(stderr, "error: %s\n", saveErr)
				return 1
			}
			if options.debugFormat == "jsonl" {
				if err := interpreter.WriteDebugTraceJSONL(stdout, debugger); err != nil {
					fmt.Fprintf(stderr, "error: %s\n", err)
					return 1
				}
			} else if options.debugFormat == "llm" {
				fmt.Fprint(stdout, interpreter.FormatDebugContextForLLM(debugger, options.debugContext))
			}
		}
		if !execResult.Return.IsVoid() {
			if execResult.Stdout != "" && !strings.HasSuffix(execResult.Stdout, "\n") {
				fmt.Fprintln(stdout)
			}
			fmt.Fprintf(stdout, "[ result   ] %s\n", execResult.Return.String())
		}
		return 0
	case emitLLVM:
		output, perfWarnings, err := backend.GenerateLLVMIRWithWarnings(result, effectiveOptimizationLevel(options), options.packedProfile, "", false, false)
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		printPerfWarnings(stderr, perfWarnings)
		// Under -Wperf the autovec verifier's findings are hard errors (the messages themselves
		// carry the "error" severity): a loop that was expected to vectorize stayed scalar and no
		// `can Scalar` grant acknowledged it.
		if result.EnforcePerfLints && len(perfWarnings) > 0 {
			return 1
		}
		if options.output != "" {
			if err := writeOutputFile(options.output, []byte(output)); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, output)
		}
		return 0
	case emitPacked:
		output, err := backend.DescribePackedLowering(result, options.packedProfile)
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		if options.output != "" {
			if err := writeOutputFile(options.output, []byte(output)); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, output)
		}
		return 0
	case emitCBindCheck:
		if options.output != "" {
			fmt.Fprintf(stderr, "error: -o is not supported for -emit %s\n", emitCBindCheck)
			return 1
		}
		if err := runCBindLayoutCheck(result, options.targetTriple, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	case emitCBindJSON:
		if err := runCBindLayoutCheckJSON(result, options.targetTriple, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	case emitHeader:
		output, err := backend.GenerateCHeader(result)
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		if options.output != "" {
			if err := writeOutputFile(options.output, []byte(output)); err != nil {
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
		} else {
			fmt.Fprint(stdout, output)
		}
		return 0
	case emitBitcode:
		if err := ensureOutputParentExists(outputPathForEmit(program.filename, options.output, ".bc")); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		if err := backend.WriteLLVMBitcodeFileWithOptAndPackedLoweringProfile(result, outputPathForEmit(program.filename, options.output, ".bc"), effectiveOptimizationLevel(options), options.packedProfile); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	case emitObject:
		if options.linkNative || options.runNative {
			exeOutputPath := options.output
			if options.runNative {
				exeOutputPath = ""
			}
			exePath, cleanup, err := buildNativeExecutable(result, options.foreignFiles, options.linkFlags, exeOutputPath, effectiveOptimizationLevel(options), options.packedProfile, options.targetTriple, options.debugInfo, options.recordTrace, stderr)
			if err != nil {
				cleanup()
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
			defer cleanup()
			if options.runNative {
				if err := runNativeExecutable(exePath, options.targetTriple, stdout, stderr); err != nil {
					fmt.Fprintf(stderr, "error: %s\n", err)
					return 1
				}
			}
			return 0
		}
		if err := ensureOutputParentExists(outputPathForEmit(program.filename, options.output, ".o")); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		if err := backend.WriteLLVMObjectFileWithOptions(result, outputPathForEmit(program.filename, options.output, ".o"), backend.LLVMObjectEmitOptions{
			OptLevel:      effectiveOptimizationLevel(options),
			PackedProfile: options.packedProfile,
			TargetTriple:  options.targetTriple,
			DebugInfo:     options.debugInfo,
			Trace:         options.recordTrace,
		}); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	case emitCArchive:
		if err := buildCArchive(result, program.filename, options.output, effectiveOptimizationLevel(options), options.packedProfile, options.targetTriple, stderr); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "error: unsupported emit mode %q\n", options.emit)
		printUsage(stderr)
		return 1
	}
}

func semanticOptionsForCLI(options cliOptions) semantic.AnalyzeOptions {
	return semantic.AnalyzeOptions{
		TargetTriple:             options.targetTriple,
		TargetDebug:              effectiveOptimizationLevel(options) == backend.OptimizationLevel0,
		EnforceUnsafePermissions: options.strictPolicy,
		EnforceProgressSafety:    options.progressStrict || options.strictPolicy,
		EnforcePerfLints:         options.perfStrict,
		FlowLintMode:             options.flowLintMode,
		EnforceStrictConcurrency: options.concurrencyStrict,
		EnforceStrictProofs:      options.proofStrict,
		WarnDiscardedValues:      options.warnUnused,
		RequireExternContracts:   options.strictExterns,
		EnableSMT:                options.enableSMT,
		EmitProofHoleHints:       options.explainHole,
		RequiresReport:           options.requiresReport,
	}
}

func saveDebugTraceIfRequested(options cliOptions, debugger *interpreter.Debugger) error {
	if strings.TrimSpace(options.debugSaveTrace) == "" || debugger == nil {
		return nil
	}
	payload, err := interpreter.ExportDebugTrace(debugger)
	if err != nil {
		return err
	}
	return os.WriteFile(options.debugSaveTrace, payload, 0o644)
}

func writeOutputFile(path string, data []byte) error {
	if err := ensureOutputParentExists(path); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// printProofReport renders the refinement-discharge report under --explain (docs/85 observability):
// one line per obligation showing where it is, the subject and law, and how it was discharged
// (proven statically, refuted, or deferred to a runtime check). A trailing summary makes the
// static-vs-runtime split scannable.
func printProofReport(stderr io.Writer, report []semantic.ProofFact, explainHole bool) {
	fmt.Fprintln(stderr, "── refinement proof report (--explain) ──")
	if len(report) == 0 {
		fmt.Fprintln(stderr, "  (no refinement obligations)")
		return
	}
	var proven, assumed, refuted, runtime, measured int
	for _, f := range report {
		class := f.Class
		if class == "" {
			class = proofReportClass(f.Outcome)
		}
		fmt.Fprintf(stderr, "  %s: %s is %s — %s (%s)\n", f.Pos, f.Subject, f.Predicate, f.Outcome, class)
		if explainHole {
			fmt.Fprintf(stderr, "      goal: %s is %s\n", f.Subject, f.Predicate)
			fmt.Fprintln(stderr, "      known facts:")
			if len(f.KnownFacts) == 0 {
				fmt.Fprintln(stderr, "        - (none)")
			} else {
				for _, fact := range f.KnownFacts {
					fmt.Fprintf(stderr, "        - %s\n", fact)
				}
			}
		}
		if len(f.ClosedWorldFacts) > 0 {
			fmt.Fprintln(stderr, "      closed world:")
			for _, fact := range f.ClosedWorldFacts {
				fmt.Fprintf(stderr, "        - %s\n", fact)
			}
		}
		if f.Missing != "" {
			fmt.Fprintf(stderr, "      missing: %s\n", f.Missing)
		}
		switch f.Outcome {
		case semantic.ProofProvenFlow, semantic.ProofProvenLinear, semantic.ProofProvenConst, semantic.ProofProvenContract, semantic.ProofProvenSMT:
			proven++
		case semantic.ProofAssumedExtern:
			assumed++
		case semantic.ProofRefuted:
			refuted++
		case semantic.ProofRuntime:
			runtime++
		case semantic.ProofMeasured:
			measured++
		}
	}
	fmt.Fprintf(stderr, "  %d proven statically, %d assumed, %d runtime-checked, %d measured, %d refuted\n", proven, assumed, runtime, measured, refuted)
}

// printElisionSummary renders the per-category proof-elision telemetry under --explain (docs/85
// §elision-summary). One line per category shows how many checks were ELIDED by static proof vs
// how many fall back to a runtime guard, making the dogfooding payoff immediately scannable.
func printElisionSummary(stderr io.Writer, s semantic.ProofElisionSummary) {
	fmt.Fprintln(stderr, "── proof-elision summary (--explain) ──")
	printElisionLine(stderr, "return refinements ", s.ReturnRefinements)
	printElisionLine(stderr, "call-arg refinements", s.CallArgRefinements)
	printElisionLine(stderr, "array bounds        ", s.ArrayBounds)
	printElisionLine(stderr, "contract ensures    ", s.ContractEnsures)
}

func printElisionLine(stderr io.Writer, label string, c semantic.ProofElisionCounts) {
	total := c.Elided + c.Runtime
	if total == 0 {
		fmt.Fprintf(stderr, "  %s  —  (none)\n", label)
		return
	}
	pct := 0
	if total > 0 {
		pct = (c.Elided * 100) / total
	}
	fmt.Fprintf(stderr, "  %s  %d/%d elided (%d%% static)\n", label, c.Elided, total, pct)
}

// printRequiresReport renders the -requires-report blast-radius report (docs c3): one entry per
// (requires-bearing function, clause) showing how many direct call sites statically discharge the
// precondition vs fall back to a runtime check, listing the unprovable sites' locations. This
// surfaces, before committing, the new runtime-check obligations that adding a `requires` to a hot
// function would create.
func printRequiresReport(stderr io.Writer, report []semantic.RequiresReportEntry) {
	if len(report) == 0 {
		fmt.Fprintln(stderr, "requires-report: (no requires-bearing call sites)")
		return
	}
	for _, entry := range report {
		total := entry.Provable + entry.Unprovable
		fmt.Fprintf(stderr, "requires-report: %s  `%s`  — %d/%d call sites provable\n", entry.DeclName, entry.ClauseText, entry.Provable, total)
		if entry.Unprovable > 0 {
			fmt.Fprint(stderr, "  unprovable:")
			for _, pos := range entry.UnprovableSites {
				fmt.Fprintf(stderr, " %s", pos)
			}
			fmt.Fprintf(stderr, "  (%d)\n", entry.Unprovable)
		}
	}
}

func proofReportClass(outcome semantic.ProofOutcome) semantic.ProofDischargeClass {
	switch outcome {
	case semantic.ProofProvenFlow:
		return semantic.ProofClassFlow
	case semantic.ProofProvenLinear:
		return semantic.ProofClassLinear
	case semantic.ProofProvenConst:
		return semantic.ProofClassConst
	case semantic.ProofProvenSMT:
		return semantic.ProofClassSMT
	case semantic.ProofProvenContract:
		return semantic.ProofClassContract
	case semantic.ProofAssumedExtern:
		return semantic.ProofClassBoundary
	case semantic.ProofMeasured:
		return semantic.ProofClassMeasured
	default:
		return semantic.ProofClassRuntime
	}
}

// printPerfWarnings surfaces the backend's post-optimization performance-friction warnings (the
// auto-vectorization verifier) to the user, one per line on stderr.
func printPerfWarnings(stderr io.Writer, warnings []string) {
	for _, w := range warnings {
		fmt.Fprintln(stderr, w)
	}
}

func ensureOutputParentExists(path string) error {
	parent := filepath.Dir(path)
	if parent == "." || parent == "" {
		return nil
	}
	return os.MkdirAll(parent, 0o755)
}
