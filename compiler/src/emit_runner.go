package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"elisacore/src/backend"
	"elisacore/src/frontendir"
	"elisacore/src/grammar"
	"elisacore/src/interpreter"
	"elisacore/src/semantic"
	"elisacore/src/unparse"
)

func runLoadedProgramWithOptions(options cliOptions, program *loadedProgram, stdout io.Writer, stderr io.Writer) int {
	if options.filter != "" && !emitSupportsFilter(options.emit) {
		fmt.Fprintf(stderr, "error: -filter is only supported for -emit %s\n", supportedFilterEmitModes())
		return 1
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
		file, _, ok := analyzeLoadedProgram(program, stderr)
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
		result := semantic.AnalyzeWithOptions(file, semantic.AnalyzeOptions{EnforceUnsafePermissions: true})
		if errs := result.Errors(); len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintf(stderr, "%s\n", e)
			}
			return 1
		}
		report := generateUnsafeReport(result)
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
		result := semantic.AnalyzeWithOptions(file, semantic.AnalyzeOptions{EnforceProgressSafety: true})
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

	_, result, ok := analyzeLoadedProgram(program, stderr)
	if !ok {
		return 1
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
		return executeSelectedTests(program.filename, result, options.filter, options.foreignFiles, options.linkFlags, effectiveOptimizationLevel(options), options.packedProfile, stdout, stderr)
	case emitInterpret:
		if options.output != "" {
			fmt.Fprintf(stderr, "error: -o is not supported for -emit %s\n", emitInterpret)
			return 1
		}
		execResult, err := interpreter.Execute(result, interpreter.Options{Stdout: stdout})
		if err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		if !execResult.Return.IsVoid() {
			if execResult.Stdout != "" && !strings.HasSuffix(execResult.Stdout, "\n") {
				fmt.Fprintln(stdout)
			}
			fmt.Fprintf(stdout, "[ result   ] %s\n", execResult.Return.String())
		}
		return 0
	case emitLLVM:
		output, err := backend.GenerateLLVMIRWithOptAndPackedLoweringProfile(result, effectiveOptimizationLevel(options), options.packedProfile)
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
			exePath, cleanup, err := buildNativeExecutable(result, options.foreignFiles, options.linkFlags, exeOutputPath, effectiveOptimizationLevel(options), options.packedProfile, stderr)
			if err != nil {
				cleanup()
				fmt.Fprintf(stderr, "error: %s\n", err)
				return 1
			}
			defer cleanup()
			if options.runNative {
				if err := runNativeExecutable(exePath, stdout, stderr); err != nil {
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

func writeOutputFile(path string, data []byte) error {
	if err := ensureOutputParentExists(path); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func ensureOutputParentExists(path string) error {
	parent := filepath.Dir(path)
	if parent == "." || parent == "" {
		return nil
	}
	return os.MkdirAll(parent, 0o755)
}
