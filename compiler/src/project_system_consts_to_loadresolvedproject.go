package main

import (
	"elisacore/src/backend"
	"elisacore/src/semantic"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// platformLinkFlagsFor returns the link flags for the current host platform from a
// per-target "platforms" map. The host GOOS is normalized to the project.json platform
// keys ("darwin" -> "macos"). Returns nil when no entry matches.
func platformLinkFlagsFor(platforms map[string]projectPlatformOverride) []string {
	if len(platforms) == 0 {
		return nil
	}
	key := runtime.GOOS
	if key == "darwin" {
		key = "macos"
	}
	if override, ok := platforms[key]; ok {
		return override.LinkFlags
	}
	return nil
}

const (
	projectFileName  = "project.json"
	manifestFileName = "manifest.json"
	libraryDirSuffix = ".elisalib"
)

type trustLevel int

const (
	trustNone trustLevel = iota
	trustInclude
	trustFull
)

type projectCommand int

const (
	projectCommandUnknown projectCommand = iota
	projectCommandInit
	projectCommandInitLib
	projectCommandBuild
	projectCommandRun
	projectCommandTest
	projectCommandBench
	projectCommandView
	projectCommandDeps
	projectCommandABILint
	projectCommandEASMLint
)

type projectCLIOptions struct {
	command           projectCommand
	targetName        string
	projectPath       string
	path              string
	output            string
	emitOverride      string
	filter            string
	linkFlags         []string
	trust             trustLevel
	targetTriple      string
	optLevel          backend.OptimizationLevel
	hasOptLevel       bool
	initName          string
	packedProfile     backend.PackedLoweringProfile
	jsonOutput        bool
	strictContracts   bool
	debugInfo         bool
	recordTrace       bool
	strictPolicy      bool
	perfStrict        bool
	flowLintMode      semantic.FlowLintMode
	concurrencyStrict bool
	progressStrict    bool
	proofStrict       bool
	enableSMT         bool
	lto               backend.LTOMode
	targetCPU         string
}
type projectDefinition struct {
	Version               string                             `json:"version,omitempty"`
	DependencySearchPaths []string                           `json:"dependency-search-paths,omitempty"`
	Dependencies          []string                           `json:"dependencies,omitempty"`
	IncludeDirs           []string                           `json:"include-dirs,omitempty"`
	Foreign               []string                           `json:"foreign,omitempty"`
	EASM                  []string                           `json:"easm,omitempty"`
	LinkFlags             []string                           `json:"link-flags,omitempty"`
	Exec                  []string                           `json:"exec,omitempty"`
	Targets               map[string]projectTargetDefinition `json:"targets"`
}
type projectTargetDefinition struct {
	Entry                string                             `json:"entry"`
	Emit                 string                             `json:"emit,omitempty"`
	RunEmit              string                             `json:"run-emit,omitempty"`
	Output               string                             `json:"output,omitempty"`
	Dependencies         []string                           `json:"dependencies,omitempty"`
	IncludeDirs          []string                           `json:"include-dirs,omitempty"`
	Foreign              []string                           `json:"foreign,omitempty"`
	EASM                 []string                           `json:"easm,omitempty"`
	LinkFlags            []string                           `json:"link-flags,omitempty"`
	InheritProjectNative *bool                              `json:"inherit-project-native,omitempty"`
	Exec                 []string                           `json:"exec,omitempty"`
	Opt                  string                             `json:"opt,omitempty"`
	TargetTriple         string                             `json:"target-triple,omitempty"`
	PackedABI            string                             `json:"packed-abi,omitempty"`
	LTO                  string                             `json:"lto,omitempty"`
	TargetCPU            string                             `json:"target-cpu,omitempty"`
	Warnings             *projectTargetWarnings             `json:"warnings,omitempty"`
	Platforms            map[string]projectPlatformOverride `json:"platforms,omitempty"`
}

// projectPlatformOverride carries host-platform-specific build settings selected at
// resolve time by the host GOOS (e.g. "macos"/"linux"/"windows"). Only the matching
// platform's settings are applied; others are ignored.
type projectPlatformOverride struct {
	LinkFlags []string `json:"link-flags,omitempty"`
}
type projectTargetWarnings struct {
	Strict      bool `json:"strict,omitempty"`
	Perf        bool `json:"perf,omitempty"`
	Concurrency bool `json:"concurrency,omitempty"`
	Progress    bool `json:"progress,omitempty"`
}
type manifestDefinition struct {
	Provides     string   `json:"provides"`
	Entry        string   `json:"entry,omitempty"`
	Interface    string   `json:"interface,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	IncludeDirs  []string `json:"include-dirs,omitempty"`
	Foreign      []string `json:"foreign,omitempty"`
	EASM         []string `json:"easm,omitempty"`
	LinkFlags    []string `json:"link-flags,omitempty"`
	Exec         []string `json:"exec,omitempty"`
}
type resolvedProject struct {
	root     string
	filePath string
	config   projectDefinition
}
type resolvedManifest struct {
	name          string
	dir           string
	manifestPath  string
	entryPath     string
	interfacePath string
	includeDirs   []string
	foreignFiles  []string
	easmFiles     []string
	linkFlags     []string
	dependencies  []*resolvedManifest
	exec          []string
}
type resolvedProjectTarget struct {
	project               *resolvedProject
	name                  string
	entryPath             string
	emit                  string
	runEmit               string
	outputPath            string
	includeDirs           []string
	dependencySearchPaths []string
	dependencyOrder       []*resolvedManifest
	foreignFiles          []string
	easmFiles             []string
	linkFlags             []string
	projectExec           []string
	targetExec            []string
	optLevel              backend.OptimizationLevel
	hasOptLevel           bool
	targetTriple          string
	packedProfile         backend.PackedLoweringProfile
	strictPolicy          bool
	perfStrict            bool
	flowLintMode          semantic.FlowLintMode
	concurrencyStrict     bool
	progressStrict        bool
	proofStrict           bool
	enableSMT             bool
	lto                   backend.LTOMode
	targetCPU             string
}
type projectResolver struct {
	searchPaths []string
	cache       map[string]*resolvedManifest
	resolving   map[string]bool
}
type sourceExpandOptions struct {
	includeDirs []string
}

func isProjectCommand(name string) bool {
	switch strings.TrimSpace(name) {
	case "init", "init-lib", "build", "run", "test", "bench", "project":
		return true
	default:
		return false
	}
}
func runProjectCLI(args []string, stdout io.Writer, stderr io.Writer) int {
	options, err := parseProjectCLIArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		printProjectUsage(stderr)
		return 1
	}

	switch options.command {
	case projectCommandInit:
		if err := scaffoldProject(options); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	case projectCommandInitLib:
		if err := scaffoldLibrary(options); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		return 0
	}

	project, err := loadResolvedProject(options.projectPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	if options.command == projectCommandView {
		return runProjectView(project, options, stdout, stderr)
	}
	if options.command == projectCommandDeps {
		return runProjectDeps(project, options, stdout, stderr)
	}
	if options.command == projectCommandABILint {
		return runProjectABILint(project, options, stdout, stderr)
	}
	if options.command == projectCommandEASMLint {
		return runProjectEASMLint(project, options, stdout, stderr)
	}

	target, err := resolveProjectTarget(project, options)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	if err := runResolvedProjectHooks(target, options.trust, stderr); err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	program, err := buildProjectLoadedProgram(target)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}

	cli := cliOptions{
		emit:              target.emit,
		filename:          target.entryPath,
		output:            target.outputPath,
		filter:            options.filter,
		foreignFiles:      append([]string(nil), target.foreignFiles...),
		linkFlags:         append([]string(nil), target.linkFlags...),
		targetTriple:      target.targetTriple,
		packedProfile:     target.packedProfile,
		hasOptLevel:       target.hasOptLevel,
		optLevel:          target.optLevel,
		debugInfo:         options.debugInfo,
		recordTrace:       options.recordTrace,
		strictPolicy:      target.strictPolicy,
		perfStrict:        target.perfStrict,
		flowLintMode:      target.flowLintMode,
		concurrencyStrict: target.concurrencyStrict,
		progressStrict:    target.progressStrict,
		proofStrict:       target.proofStrict,
		enableSMT:         target.enableSMT,
		lto:               target.lto,
		targetCPU:         target.targetCPU,
	}
	if target.targetCPU != "" {
		_ = os.Setenv(backend.TargetCPUEnv, target.targetCPU)
	}

	switch options.command {
	case projectCommandRun:
		cli.emit = target.runEmit
		cli.output = ""
		if cli.emit == emitObject {
			cli.runNative = true
		}
	case projectCommandTest:
		cli.emit = emitTest
		cli.output = ""
	case projectCommandBench:
		cli.emit = emitBenches
		cli.output = ""
	case projectCommandBuild:
		if cli.emit == emitObject && shouldLinkNativeProjectBuild(cli.output) {
			cli.linkNative = true
		}
	default:
		fmt.Fprintf(stderr, "error: unsupported project command\n")
		return 1
	}
	return runLoadedProgramWithOptions(cli, program, stdout, stderr)
}
func shouldLinkNativeProjectBuild(outputPath string) bool {
	trimmed := strings.TrimSpace(outputPath)
	if trimmed == "" {
		return false
	}
	return strings.ToLower(filepath.Ext(trimmed)) != ".o"
}
