package main

import (
	"elisacore/src/backend"
	"elisacore/src/semantic"
	"fmt"
	"os"
	"strings"
)

func parseProjectCLIArgs(args []string) (projectCLIOptions, error) {
	if len(args) == 0 {
		return projectCLIOptions{}, fmt.Errorf("missing project command")
	}
	// docs/90: SMT discharge tier ON by default (see the main CLI note); `-nosmt` opts out.
	// docs/85: prove-it-or-fail (`-strict`) is also the default; `-permissive` opts out.
	options := projectCLIOptions{trust: trustNone, enableSMT: true, proofStrict: true, packedProfile: backend.DefaultPackedLoweringProfile()}
	switch args[0] {
	case "init":
		options.command = projectCommandInit
	case "init-lib":
		options.command = projectCommandInitLib
	case "build":
		options.command = projectCommandBuild
	case "run":
		options.command = projectCommandRun
	case "test":
		options.command = projectCommandTest
	case "bench":
		options.command = projectCommandBench
	case "project":
		if len(args) < 2 {
			return projectCLIOptions{}, fmt.Errorf("missing project subcommand")
		}
		subcommand := strings.TrimSpace(args[1])
		if subcommand != "view" && subcommand != "deps" && subcommand != "abi-lint" && subcommand != "easm-lint" {
			return projectCLIOptions{}, fmt.Errorf("unsupported project subcommand %q", subcommand)
		}
		if subcommand == "view" {
			options.command = projectCommandView
		} else if subcommand == "deps" {
			options.command = projectCommandDeps
		} else if subcommand == "abi-lint" {
			options.command = projectCommandABILint
		} else {
			options.command = projectCommandEASMLint
		}
		args = append([]string{args[0]}, args[2:]...)
	default:
		return projectCLIOptions{}, fmt.Errorf("unsupported project command %q", args[0])
	}

	start := 1
	if options.command == projectCommandView {
		start = 1
	}
	for i := start; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "":
			continue
		case strings.HasPrefix(arg, "--project="):
			options.projectPath = strings.TrimSpace(strings.TrimPrefix(arg, "--project="))
		case arg == "--project" || arg == "-p":
			i++
			if i >= len(args) {
				return projectCLIOptions{}, fmt.Errorf("missing value after %s", arg)
			}
			options.projectPath = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "--path="):
			options.path = strings.TrimSpace(strings.TrimPrefix(arg, "--path="))
		case arg == "--path":
			i++
			if i >= len(args) {
				return projectCLIOptions{}, fmt.Errorf("missing value after --path")
			}
			options.path = strings.TrimSpace(args[i])
		case arg == "--strict":
			if options.command != projectCommandInit {
				return projectCLIOptions{}, fmt.Errorf("--strict is only supported by init; use -Wstrict for build, run, test, and bench")
			}
			options.strictPolicy = true
		case strings.HasPrefix(arg, "--trust="):
			trust, err := parseTrustLevel(strings.TrimSpace(strings.TrimPrefix(arg, "--trust=")))
			if err != nil {
				return projectCLIOptions{}, err
			}
			options.trust = trust
		case arg == "--trust":
			i++
			if i >= len(args) {
				return projectCLIOptions{}, fmt.Errorf("missing value after --trust")
			}
			trust, err := parseTrustLevel(strings.TrimSpace(args[i]))
			if err != nil {
				return projectCLIOptions{}, err
			}
			options.trust = trust
		case strings.HasPrefix(arg, "-filter="):
			options.filter = strings.TrimSpace(strings.TrimPrefix(arg, "-filter="))
		case arg == "-filter":
			i++
			if i >= len(args) {
				return projectCLIOptions{}, fmt.Errorf("missing value after -filter")
			}
			options.filter = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-target-triple="):
			options.targetTriple = strings.TrimSpace(strings.TrimPrefix(arg, "-target-triple="))
		case arg == "-target-triple":
			i++
			if i >= len(args) {
				return projectCLIOptions{}, fmt.Errorf("missing value after -target-triple")
			}
			options.targetTriple = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-emit="):
			options.emitOverride = normalizeEmitMode(strings.TrimSpace(strings.TrimPrefix(arg, "-emit=")))
		case arg == "-emit":
			i++
			if i >= len(args) {
				return projectCLIOptions{}, fmt.Errorf("missing value after -emit")
			}
			options.emitOverride = normalizeEmitMode(strings.TrimSpace(args[i]))
		case arg == "-O0" || arg == "-O1" || arg == "-O2" || arg == "-O3":
			level, err := parseOptimizationArg(strings.TrimPrefix(arg, "-O"))
			if err != nil {
				return projectCLIOptions{}, err
			}
			options.optLevel = level
			options.hasOptLevel = true
		case strings.HasPrefix(arg, "-o="):
			options.output = strings.TrimSpace(strings.TrimPrefix(arg, "-o="))
		case arg == "--json":
			options.jsonOutput = true
		case arg == "--strict-contracts":
			options.strictContracts = true
		case arg == "-g" || arg == "-debug-info":
			options.debugInfo = true
		case arg == "-ftrace" || arg == "-record-trace":
			options.recordTrace = true
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
				return projectCLIOptions{}, fmt.Errorf("unsupported %s (expected -flto, -flto=full or -flto=thin)", arg)
			}
			options.lto = mode
		case strings.HasPrefix(arg, "-mcpu=") || arg == "-mcpu":
			// Target CPU for code generation: `native` (the default for a host build),
			// `generic` (portable), or an LLVM CPU name. Read at codegen time (in-process).
			value := strings.TrimPrefix(arg, "-mcpu=")
			if arg == "-mcpu" {
				i++
				if i >= len(args) {
					return projectCLIOptions{}, fmt.Errorf("missing value after -mcpu")
				}
				value = args[i]
			}
			value = backend.NormalizeTargetCPU(value)
			if value == "" {
				return projectCLIOptions{}, fmt.Errorf("missing value after -mcpu")
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
			// excluded. Keep the opt-in OFF by default because a noalias miscompile is silent.
			// Read at codegen time, same env the backend generator consults.
			_ = os.Setenv("ELISACORE_NOALIAS_MUTABLE_REFS", "1")
		case arg == "-Wperf":
			options.perfStrict = true
		case arg == "-Wflow":
			// docs/121: flow-checked-loop lints, warn tier (Phase A). See the single-file CLI parser
			// for the full note. The declarative `[warnings] flow` manifest field lands in Phase C.
			options.flowLintMode = semantic.FlowLintWarn
		case arg == "-Wflow-strict":
			options.flowLintMode = semantic.FlowLintStrict
		case arg == "-Wconcurrency":
			options.concurrencyStrict = true
		case arg == "-Wprogress":
			// docs/102: activate the (already-built but dormant) progress-safety obligation
			// checker on its own dial — every `while` loop / recursive cycle must show progress
			// evidence (`decreases`, `Progress.*`) or declare intentional non-progress.
			options.progressStrict = true
		case arg == "-smt":
			// docs/90: SMT discharge tier — ON by default; explicit opt-in kept for back-compat.
			options.enableSMT = true
		case arg == "-nosmt":
			// Opt out of the default-on SMT discharge tier.
			options.enableSMT = false
		case arg == "-strict":
			// docs/85: prove-it-or-fail — an obligation that cannot be discharged statically
			// fails the build. ON by default now; explicit opt-in / harmless no-op for back-compat.
			options.proofStrict = true
		case arg == "-permissive":
			// Opt out of default-on strict proofs: an unprovable refinement/contract degrades to
			// a warning + debug runtime check instead of failing the build.
			options.proofStrict = false
		case arg == "-Wstrict":
			options.strictPolicy = true
			options.perfStrict = true
			options.concurrencyStrict = true
			options.progressStrict = true
			options.proofStrict = true
		case arg == "-o":
			i++
			if i >= len(args) {
				return projectCLIOptions{}, fmt.Errorf("missing value after -o")
			}
			options.output = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-link="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "-link="))
			if value == "" {
				return projectCLIOptions{}, fmt.Errorf("missing value after -link")
			}
			options.linkFlags = append(options.linkFlags, value)
		case arg == "-link":
			i++
			if i >= len(args) {
				return projectCLIOptions{}, fmt.Errorf("missing value after -link")
			}
			value := strings.TrimSpace(args[i])
			if value == "" {
				return projectCLIOptions{}, fmt.Errorf("missing value after -link")
			}
			options.linkFlags = append(options.linkFlags, value)
		case strings.HasPrefix(arg, "-L="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "-L="))
			if value == "" {
				return projectCLIOptions{}, fmt.Errorf("missing value after -L")
			}
			options.linkFlags = append(options.linkFlags, "-L"+value)
		case arg == "-L":
			i++
			if i >= len(args) {
				return projectCLIOptions{}, fmt.Errorf("missing value after -L")
			}
			value := strings.TrimSpace(args[i])
			if value == "" {
				return projectCLIOptions{}, fmt.Errorf("missing value after -L")
			}
			options.linkFlags = append(options.linkFlags, "-L"+value)
		case strings.HasPrefix(arg, "-l") && len(arg) > len("-l"):
			options.linkFlags = append(options.linkFlags, arg)
		case arg == "-l":
			i++
			if i >= len(args) {
				return projectCLIOptions{}, fmt.Errorf("missing value after -l")
			}
			value := strings.TrimSpace(args[i])
			if value == "" {
				return projectCLIOptions{}, fmt.Errorf("missing value after -l")
			}
			options.linkFlags = append(options.linkFlags, "-l"+value)
		case arg == "-h" || arg == "--help":
			return projectCLIOptions{}, fmt.Errorf("help requested")
		case strings.HasPrefix(arg, "-"):
			return projectCLIOptions{}, fmt.Errorf("unknown option %q", arg)
		default:
			switch options.command {
			case projectCommandInit, projectCommandInitLib:
				if options.initName != "" {
					return projectCLIOptions{}, fmt.Errorf("expected a single project or library name, got %q", arg)
				}
				options.initName = arg
			case projectCommandBuild, projectCommandRun, projectCommandTest, projectCommandBench, projectCommandView, projectCommandDeps, projectCommandABILint, projectCommandEASMLint:
				if options.targetName != "" {
					return projectCLIOptions{}, fmt.Errorf("expected a single target name, got %q", arg)
				}
				options.targetName = arg
			default:
				return projectCLIOptions{}, fmt.Errorf("unexpected argument %q", arg)
			}
		}
	}

	switch options.command {
	case projectCommandInit, projectCommandInitLib:
		if strings.TrimSpace(options.initName) == "" {
			return projectCLIOptions{}, fmt.Errorf("missing project or library name")
		}
	}
	return options, nil
}
