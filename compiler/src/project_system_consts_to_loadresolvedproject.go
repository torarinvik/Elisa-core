package main

import (
	"elisacore/src/backend"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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
)

type projectCLIOptions struct {
	command       projectCommand
	targetName    string
	projectPath   string
	path          string
	output        string
	emitOverride  string
	filter        string
	trust         trustLevel
	optLevel      backend.OptimizationLevel
	hasOptLevel   bool
	initName      string
	packedProfile backend.PackedLoweringProfile
	jsonOutput    bool
}
type projectDefinition struct {
	Version               string                             `json:"version,omitempty"`
	DependencySearchPaths []string                           `json:"dependency-search-paths,omitempty"`
	Dependencies          []string                           `json:"dependencies,omitempty"`
	IncludeDirs           []string                           `json:"include-dirs,omitempty"`
	Foreign               []string                           `json:"foreign,omitempty"`
	Exec                  []string                           `json:"exec,omitempty"`
	Targets               map[string]projectTargetDefinition `json:"targets"`
}
type projectTargetDefinition struct {
	Entry        string   `json:"entry"`
	Emit         string   `json:"emit,omitempty"`
	RunEmit      string   `json:"run-emit,omitempty"`
	Output       string   `json:"output,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	IncludeDirs  []string `json:"include-dirs,omitempty"`
	Foreign      []string `json:"foreign,omitempty"`
	Exec         []string `json:"exec,omitempty"`
	Opt          string   `json:"opt,omitempty"`
	PackedABI    string   `json:"packed-abi,omitempty"`
}
type manifestDefinition struct {
	Provides     string   `json:"provides"`
	Entry        string   `json:"entry,omitempty"`
	Interface    string   `json:"interface,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	IncludeDirs  []string `json:"include-dirs,omitempty"`
	Foreign      []string `json:"foreign,omitempty"`
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
	projectExec           []string
	targetExec            []string
	optLevel              backend.OptimizationLevel
	hasOptLevel           bool
	packedProfile         backend.PackedLoweringProfile
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
		emit:          target.emit,
		filename:      target.entryPath,
		output:        target.outputPath,
		filter:        options.filter,
		foreignFiles:  append([]string(nil), target.foreignFiles...),
		packedProfile: target.packedProfile,
		hasOptLevel:   target.hasOptLevel,
		optLevel:      target.optLevel,
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
func parseProjectCLIArgs(args []string) (projectCLIOptions, error) {
	if len(args) == 0 {
		return projectCLIOptions{}, fmt.Errorf("missing project command")
	}
	options := projectCLIOptions{trust: trustNone, packedProfile: backend.DefaultPackedLoweringProfile()}
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
		if subcommand != "view" && subcommand != "deps" {
			return projectCLIOptions{}, fmt.Errorf("unsupported project subcommand %q", subcommand)
		}
		if subcommand == "view" {
			options.command = projectCommandView
		} else {
			options.command = projectCommandDeps
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
		case strings.HasPrefix(arg, "-emit="):
			options.emitOverride = normalizeEmitMode(strings.TrimSpace(strings.TrimPrefix(arg, "-emit=")))
		case arg == "-emit":
			i++
			if i >= len(args) {
				return projectCLIOptions{}, fmt.Errorf("missing value after -emit")
			}
			options.emitOverride = normalizeEmitMode(strings.TrimSpace(args[i]))
		case arg == "-O0" || arg == "-O2" || arg == "-O3":
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
		case arg == "-o":
			i++
			if i >= len(args) {
				return projectCLIOptions{}, fmt.Errorf("missing value after -o")
			}
			options.output = strings.TrimSpace(args[i])
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
			case projectCommandBuild, projectCommandRun, projectCommandTest, projectCommandBench, projectCommandView, projectCommandDeps:
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
func parseTrustLevel(value string) (trustLevel, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none", "":
		return trustNone, nil
	case "include":
		return trustInclude, nil
	case "full":
		return trustFull, nil
	default:
		return trustNone, fmt.Errorf("unsupported trust level %q (expected none, include, or full)", value)
	}
}
func printProjectUsage(w io.Writer) {
	fmt.Fprintln(w, "Project commands:")
	fmt.Fprintln(w, "  elisacore init <name> [--path <dir>]")
	fmt.Fprintln(w, "  elisacore init-lib <name> [--path <dir>]")
	fmt.Fprintln(w, "  elisacore build [target] [--project <dir|project.json>] [-emit <mode>] [-o <output>] [--trust <none|include|full>] [-O0|-O2|-O3]")
	fmt.Fprintln(w, "  elisacore run [target] [--project <dir|project.json>] [--trust <none|include|full>]")
	fmt.Fprintln(w, "  elisacore test [target] [--project <dir|project.json>] [-filter <substring>] [--trust <none|include|full>] [-O0|-O2|-O3]")
	fmt.Fprintln(w, "  elisacore bench [target] [--project <dir|project.json>] [-filter <substring>] [--trust <none|include|full>]")
	fmt.Fprintln(w, "  elisacore project view [target] [--project <dir|project.json>]")
	fmt.Fprintln(w, "  elisacore project deps [target] [--project <dir|project.json>] [--json]")
}
func scaffoldProject(options projectCLIOptions) error {
	basePath := options.path
	if strings.TrimSpace(basePath) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		basePath = cwd
	}
	root := filepath.Join(basePath, options.initName)
	if _, err := os.Stat(root); err == nil {
		return fmt.Errorf("path %s already exists", root)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		return err
	}
	for _, dir := range []string{"build", "lib", "native", "test"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			return err
		}
	}
	project := projectDefinition{
		Version:               "0.1.0",
		DependencySearchPaths: []string{"lib"},
		Dependencies:          []string{},
		IncludeDirs:           []string{"src"},
		Foreign:               []string{},
		Exec:                  []string{},
		Targets: map[string]projectTargetDefinition{
			"app": {
				Entry:        "src/main.elisa",
				Emit:         emitLLVM,
				RunEmit:      emitInterpret,
				Output:       filepath.ToSlash(filepath.Join("build", "app.ll")),
				Dependencies: []string{},
				IncludeDirs:  []string{},
				Foreign:      []string{},
				Exec:         []string{},
				Opt:          "O0",
			},
		},
	}
	projectData, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		return err
	}
	if err := writeOutputFile(filepath.Join(root, projectFileName), append(projectData, '\n')); err != nil {
		return err
	}
	mainSource := "def main() -> int:\n    return 0\n"
	if err := writeOutputFile(filepath.Join(root, "src", "main.elisa"), []byte(mainSource)); err != nil {
		return err
	}
	for _, placeholder := range []string{filepath.Join(root, "lib", ".gitkeep"), filepath.Join(root, "test", ".gitkeep")} {
		if err := writeOutputFile(placeholder, nil); err != nil {
			return err
		}
	}
	return nil
}
func scaffoldLibrary(options projectCLIOptions) error {
	basePath := options.path
	if strings.TrimSpace(basePath) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		basePath = cwd
	}
	root := filepath.Join(basePath, options.initName+libraryDirSuffix)
	if _, err := os.Stat(root); err == nil {
		return fmt.Errorf("path %s already exists", root)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		return err
	}
	manifest := manifestDefinition{
		Provides:     options.initName,
		Entry:        filepath.ToSlash(filepath.Join("src", options.initName+".elisa")),
		Interface:    filepath.ToSlash(filepath.Join("src", options.initName+interfaceExtension)),
		Dependencies: []string{},
		IncludeDirs:  []string{"src"},
		Foreign:      []string{},
		Exec:         []string{},
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeOutputFile(filepath.Join(root, manifestFileName), append(manifestData, '\n')); err != nil {
		return err
	}
	libFunc := sanitizeIdentifier(options.initName) + "_value"
	libSource := fmt.Sprintf("def %s() -> int:\n    return 1\n", libFunc)
	if err := writeOutputFile(filepath.Join(root, "src", options.initName+".elisa"), []byte(libSource)); err != nil {
		return err
	}
	ifaceSource := fmt.Sprintf("extern %s() -> int\n", libFunc)
	if err := writeOutputFile(filepath.Join(root, "src", options.initName+interfaceExtension), []byte(ifaceSource)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "native"), 0o755); err != nil {
		return err
	}
	if err := writeOutputFile(filepath.Join(root, "README.md"), []byte(fmt.Sprintf("# %s\n", options.initName))); err != nil {
		return err
	}
	return nil
}
func sanitizeIdentifier(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "project"
	}
	var b strings.Builder
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			if b.Len() == 0 {
				b.WriteString("p_")
			}
			b.WriteRune(r)
		default:
			if b.Len() == 0 || strings.HasSuffix(b.String(), "_") {
				continue
			}
			b.WriteRune('_')
		}
	}
	result := strings.Trim(b.String(), "_")
	if result == "" {
		return "project"
	}
	return result
}
func loadResolvedProject(path string) (*resolvedProject, error) {
	projectPath, err := findProjectFile(path)
	if err != nil {
		return nil, err
	}
	var definition projectDefinition
	if err := decodeJSONFile(projectPath, &definition); err != nil {
		return nil, err
	}
	if len(definition.Targets) == 0 {
		return nil, fmt.Errorf("%s does not define any targets", projectPath)
	}
	root := filepath.Dir(projectPath)
	return &resolvedProject{root: root, filePath: projectPath, config: definition}, nil
}
