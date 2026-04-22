package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"llcontext/src/backend"
)

const (
	projectFileName  = "project.json"
	manifestFileName = "manifest.json"
	libraryDirSuffix = ".llctxlib"
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
	fmt.Fprintln(w, "  llcontext init <name> [--path <dir>]")
	fmt.Fprintln(w, "  llcontext init-lib <name> [--path <dir>]")
	fmt.Fprintln(w, "  llcontext build [target] [--project <dir|project.json>] [-emit <mode>] [-o <output>] [--trust <none|include|full>] [-O0|-O2|-O3]")
	fmt.Fprintln(w, "  llcontext run [target] [--project <dir|project.json>] [--trust <none|include|full>]")
	fmt.Fprintln(w, "  llcontext test [target] [--project <dir|project.json>] [-filter <substring>] [--trust <none|include|full>] [-O0|-O2|-O3]")
	fmt.Fprintln(w, "  llcontext bench [target] [--project <dir|project.json>] [-filter <substring>] [--trust <none|include|full>]")
	fmt.Fprintln(w, "  llcontext project view [target] [--project <dir|project.json>]")
	fmt.Fprintln(w, "  llcontext project deps [target] [--project <dir|project.json>] [--json]")
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
				Entry:        "src/main.llcontext",
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
	if err := writeOutputFile(filepath.Join(root, "src", "main.llcontext"), []byte(mainSource)); err != nil {
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
		Entry:        filepath.ToSlash(filepath.Join("src", options.initName+".llcontext")),
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
	if err := writeOutputFile(filepath.Join(root, "src", options.initName+".llcontext"), []byte(libSource)); err != nil {
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

func findProjectFile(path string) (string, error) {
	start := strings.TrimSpace(path)
	if start == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		start = cwd
	}
	info, err := os.Stat(start)
	if err == nil && !info.IsDir() {
		if filepath.Base(start) != projectFileName {
			return "", fmt.Errorf("expected %s or a directory, got %s", projectFileName, start)
		}
		return filepath.Abs(start)
	}
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	dir := start
	if info != nil && !info.IsDir() {
		dir = filepath.Dir(start)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(absDir, projectFileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(absDir)
		if parent == absDir {
			break
		}
		absDir = parent
	}
	return "", fmt.Errorf("could not find %s from %s", projectFileName, start)
}

func decodeJSONFile(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid json in %s: %w", path, err)
	}
	if decoder.More() {
		return fmt.Errorf("invalid json in %s: trailing data", path)
	}
	return nil
}

func resolveProjectTarget(project *resolvedProject, options projectCLIOptions) (*resolvedProjectTarget, error) {
	if project == nil {
		return nil, fmt.Errorf("project is nil")
	}
	targetName := strings.TrimSpace(options.targetName)
	if targetName == "" {
		targetName = defaultProjectTargetName(project.config.Targets)
	}
	definition, ok := project.config.Targets[targetName]
	if !ok {
		return nil, fmt.Errorf("project target %q was not found in %s", targetName, project.filePath)
	}
	if strings.TrimSpace(definition.Entry) == "" {
		return nil, fmt.Errorf("project target %q is missing an entry", targetName)
	}
	entryPath := projectRelativePath(project.root, definition.Entry)
	if !isSurfaceSourcePath(entryPath) {
		return nil, fmt.Errorf("project target %q entry %s must be a %s or %s source file in the current MVP", targetName, entryPath, sourceExtension, interfaceExtension)
	}
	dependencySearchPaths := project.config.DependencySearchPaths
	if len(dependencySearchPaths) == 0 {
		dependencySearchPaths = []string{"lib"}
	}
	resolvedSearchPaths := make([]string, 0, len(dependencySearchPaths))
	for _, path := range dependencySearchPaths {
		resolvedSearchPaths = append(resolvedSearchPaths, projectRelativePath(project.root, path))
	}
	resolver := &projectResolver{searchPaths: dedupeStrings(resolvedSearchPaths), cache: map[string]*resolvedManifest{}, resolving: map[string]bool{}}
	dependencyNames := append([]string{}, project.config.Dependencies...)
	dependencyNames = append(dependencyNames, definition.Dependencies...)
	dependencyOrder, err := resolver.resolveDependencyOrder(dependencyNames)
	if err != nil {
		return nil, err
	}
	includeDirs := make([]string, 0, len(project.config.IncludeDirs)+len(definition.IncludeDirs))
	for _, dir := range append(append([]string{}, project.config.IncludeDirs...), definition.IncludeDirs...) {
		includeDirs = append(includeDirs, projectRelativePath(project.root, dir))
	}
	foreignFiles := make([]string, 0, len(project.config.Foreign)+len(definition.Foreign))
	for _, path := range append(append([]string{}, project.config.Foreign...), definition.Foreign...) {
		foreignFiles = append(foreignFiles, projectRelativePath(project.root, path))
	}
	for _, manifest := range dependencyOrder {
		includeDirs = append(includeDirs, manifest.includeDirs...)
		foreignFiles = append(foreignFiles, manifest.foreignFiles...)
	}
	emitMode := emitLLVM
	if strings.TrimSpace(definition.Emit) != "" {
		emitMode = normalizeEmitMode(definition.Emit)
	}
	if strings.TrimSpace(options.emitOverride) != "" {
		emitMode = normalizeEmitMode(options.emitOverride)
	}
	if emitMode == "" {
		return nil, fmt.Errorf("project target %q requested an unsupported emit mode", targetName)
	}
	runEmit := emitInterpret
	if strings.TrimSpace(definition.RunEmit) != "" {
		runEmit = normalizeEmitMode(definition.RunEmit)
	}
	if runEmit == "" {
		return nil, fmt.Errorf("project target %q requested an unsupported run-emit mode", targetName)
	}
	outputPath := strings.TrimSpace(options.output)
	if outputPath == "" {
		outputPath = strings.TrimSpace(definition.Output)
	}
	if outputPath != "" {
		outputPath = projectRelativePath(project.root, outputPath)
	}
	packedProfile := backend.DefaultPackedLoweringProfile()
	if strings.TrimSpace(definition.PackedABI) != "" {
		return nil, fmt.Errorf("project target %q uses removed packed-abi override; use canonical packed lowering or enum-level @packed_profile(...) instead", targetName)
	}
	optLevel, hasOptLevel, err := resolveProjectOptLevel(definition.Opt, options)
	if err != nil {
		return nil, fmt.Errorf("project target %q: %w", targetName, err)
	}
	return &resolvedProjectTarget{
		project:               project,
		name:                  targetName,
		entryPath:             entryPath,
		emit:                  emitMode,
		runEmit:               runEmit,
		outputPath:            outputPath,
		includeDirs:           dedupeStrings(includeDirs),
		dependencySearchPaths: resolver.searchPaths,
		dependencyOrder:       dependencyOrder,
		foreignFiles:          dedupeStrings(foreignFiles),
		projectExec:           append([]string{}, project.config.Exec...),
		targetExec:            append([]string{}, definition.Exec...),
		optLevel:              optLevel,
		hasOptLevel:           hasOptLevel,
		packedProfile:         packedProfile,
	}, nil
}

func resolveProjectOptLevel(targetValue string, options projectCLIOptions) (backend.OptimizationLevel, bool, error) {
	if options.hasOptLevel {
		return options.optLevel, true, nil
	}
	if strings.TrimSpace(targetValue) == "" {
		return backend.OptimizationLevel0, false, nil
	}
	level, err := parseOptimizationArg(targetValue)
	if err != nil {
		return 0, false, err
	}
	return level, true, nil
}

func defaultProjectTargetName(targets map[string]projectTargetDefinition) string {
	if _, ok := targets["default"]; ok {
		return "default"
	}
	if len(targets) == 1 {
		for name := range targets {
			return name
		}
	}
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func projectRelativePath(root string, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, filepath.FromSlash(path))
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		result = append(result, trimmed)
	}
	return result
}

func (r *projectResolver) resolveDependencyOrder(names []string) ([]*resolvedManifest, error) {
	ordered := make([]*resolvedManifest, 0, len(names))
	seen := map[string]bool{}
	var visit func(*resolvedManifest)
	visit = func(manifest *resolvedManifest) {
		if manifest == nil || seen[manifest.name] {
			return
		}
		seen[manifest.name] = true
		for _, dep := range manifest.dependencies {
			visit(dep)
		}
		ordered = append(ordered, manifest)
	}
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		manifest, err := r.resolveManifest(strings.TrimSpace(name))
		if err != nil {
			return nil, err
		}
		visit(manifest)
	}
	return ordered, nil
}

func (r *projectResolver) resolveManifest(name string) (*resolvedManifest, error) {
	if manifest, ok := r.cache[name]; ok {
		return manifest, nil
	}
	if r.resolving[name] {
		return nil, fmt.Errorf("cyclic dependency detected while resolving %q", name)
	}
	manifestPath, manifestDir, err := r.findManifestPath(name)
	if err != nil {
		return nil, err
	}
	r.resolving[name] = true
	defer delete(r.resolving, name)
	var definition manifestDefinition
	if err := decodeJSONFile(manifestPath, &definition); err != nil {
		return nil, err
	}
	if definition.Provides != name {
		return nil, fmt.Errorf("manifest %s provides %q but was requested as %q", manifestPath, definition.Provides, name)
	}
	resolved := &resolvedManifest{
		name:         name,
		dir:          manifestDir,
		manifestPath: manifestPath,
		includeDirs:  make([]string, 0, len(definition.IncludeDirs)),
		foreignFiles: make([]string, 0, len(definition.Foreign)),
		exec:         append([]string{}, definition.Exec...),
	}
	if strings.TrimSpace(definition.Entry) != "" {
		resolved.entryPath = projectRelativePath(manifestDir, definition.Entry)
		if !isSurfaceSourcePath(resolved.entryPath) {
			return nil, fmt.Errorf("manifest %s entry %s must be a %s or %s source file in the current MVP", manifestPath, resolved.entryPath, sourceExtension, interfaceExtension)
		}
	}
	if strings.TrimSpace(definition.Interface) != "" {
		resolved.interfacePath = projectRelativePath(manifestDir, definition.Interface)
		if !isSurfaceSourcePath(resolved.interfacePath) {
			return nil, fmt.Errorf("manifest %s interface %s must be a %s or %s source file in the current MVP", manifestPath, resolved.interfacePath, sourceExtension, interfaceExtension)
		}
	}
	for _, dir := range definition.IncludeDirs {
		resolved.includeDirs = append(resolved.includeDirs, projectRelativePath(manifestDir, dir))
	}
	for _, path := range definition.Foreign {
		resolved.foreignFiles = append(resolved.foreignFiles, projectRelativePath(manifestDir, path))
	}
	r.cache[name] = resolved
	for _, depName := range definition.Dependencies {
		depName = strings.TrimSpace(depName)
		if depName == "" {
			continue
		}
		dep, err := r.resolveManifest(depName)
		if err != nil {
			return nil, err
		}
		resolved.dependencies = append(resolved.dependencies, dep)
	}
	return resolved, nil
}

func (r *projectResolver) findManifestPath(name string) (string, string, error) {
	for _, searchPath := range r.searchPaths {
		for _, candidateDir := range []string{
			filepath.Join(searchPath, name+libraryDirSuffix),
			filepath.Join(searchPath, name),
		} {
			manifestPath := filepath.Join(candidateDir, manifestFileName)
			if _, err := os.Stat(manifestPath); err == nil {
				return manifestPath, candidateDir, nil
			}
		}
	}
	return "", "", fmt.Errorf("dependency %q could not be found in %s", name, strings.Join(r.searchPaths, ", "))
}

func runResolvedProjectHooks(target *resolvedProjectTarget, trust trustLevel, stderr io.Writer) error {
	if target == nil {
		return fmt.Errorf("resolved project target is nil")
	}
	if err := runHookList("project", target.project.root, target.projectExec, trust, stderr); err != nil {
		return err
	}
	if err := runHookList("target "+target.name, target.project.root, target.targetExec, trust, stderr); err != nil {
		return err
	}
	for _, manifest := range target.dependencyOrder {
		if err := runHookList("dependency "+manifest.name, manifest.dir, manifest.exec, trust, stderr); err != nil {
			return err
		}
	}
	return nil
}

func runHookList(scope string, dir string, hooks []string, trust trustLevel, stderr io.Writer) error {
	for _, hook := range hooks {
		hook = strings.TrimSpace(hook)
		if hook == "" {
			continue
		}
		if trust < trustFull {
			return fmt.Errorf("%s defines exec hooks; rerun with --trust=full to allow %q", scope, hook)
		}
		if stderr != nil {
			fmt.Fprintf(stderr, "[ hook     ] %s: %s\n", scope, hook)
		}
		cmd := shellCommand(hook)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if len(output) != 0 && stderr != nil {
			_, _ = stderr.Write(output)
			if output[len(output)-1] != '\n' {
				_, _ = stderr.Write([]byte("\n"))
			}
		}
		if err != nil {
			return fmt.Errorf("hook %q in %s failed: %w", hook, dir, err)
		}
	}
	return nil
}

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("/bin/sh", "-c", command)
}

func buildProjectLoadedProgram(target *resolvedProjectTarget) (*loadedProgram, error) {
	if target == nil {
		return nil, fmt.Errorf("resolved project target is nil")
	}
	entries := make([]string, 0, len(target.dependencyOrder)+1)
	for _, manifest := range target.dependencyOrder {
		if strings.TrimSpace(manifest.entryPath) != "" {
			entries = append(entries, manifest.entryPath)
			continue
		}
		if strings.TrimSpace(manifest.interfacePath) != "" {
			entries = append(entries, manifest.interfacePath)
		}
	}
	entries = append(entries, target.entryPath)
	options := sourceExpandOptions{includeDirs: target.includeDirs}
	var combined bytes.Buffer
	for _, entry := range entries {
		source, err := readSourceWithIncludesWithOptions(entry, map[string]bool{}, options)
		if err != nil {
			return nil, err
		}
		if combined.Len() != 0 && combined.Bytes()[combined.Len()-1] != '\n' {
			combined.WriteByte('\n')
		}
		combined.Write(source)
		if len(source) == 0 || source[len(source)-1] != '\n' {
			combined.WriteByte('\n')
		}
	}
	return &loadedProgram{filename: target.entryPath, source: combined.Bytes()}, nil
}

func readSourceWithIncludesWithOptions(filename string, seen map[string]bool, options sourceExpandOptions) ([]byte, error) {
	var out bytes.Buffer
	if err := writeSourceWithIncludesWithOptions(&out, filename, seen, options); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeSourceWithIncludesWithOptions(out *bytes.Buffer, filename string, seen map[string]bool, options sourceExpandOptions) error {
	abs, err := filepath.Abs(filename)
	if err != nil {
		return err
	}
	if seen[abs] {
		return fmt.Errorf("cyclic include detected for %s", abs)
	}
	seen[abs] = true
	defer delete(seen, abs)

	raw, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	out.Grow(len(raw))
	start := 0
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
			resolved, err := resolveExpandedIncludePath(filepath.Dir(abs), includePath, options.includeDirs)
			if err != nil {
				return err
			}
			outLenBefore := out.Len()
			if err := writeSourceWithIncludesWithOptions(out, resolved, seen, options); err != nil {
				return err
			}
			if out.Len() == outLenBefore || out.Bytes()[out.Len()-1] != '\n' {
				out.WriteByte('\n')
			}
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
	}
	return nil
}

func resolveExpandedIncludePath(currentDir string, includePath string, includeDirs []string) (string, error) {
	candidates := make([]string, 0, len(includeDirs)+1)
	candidates = append(candidates, filepath.Join(currentDir, includePath))
	for _, dir := range includeDirs {
		candidates = append(candidates, filepath.Join(dir, includePath))
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("include %q not found from %s or configured include directories", includePath, currentDir)
}

func runProjectView(project *resolvedProject, options projectCLIOptions, stdout io.Writer, stderr io.Writer) int {
	if project == nil {
		fmt.Fprintf(stderr, "error: project is nil\n")
		return 1
	}
	fmt.Fprintf(stdout, "Project: %s\n", project.filePath)
	names := make([]string, 0, len(project.config.Targets))
	for name := range project.config.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprintf(stdout, "Targets (%d):\n", len(names))
	for _, name := range names {
		definition := project.config.Targets[name]
		fmt.Fprintf(stdout, "- %s entry=%s emit=%s run=%s\n", name, definition.Entry, defaultString(definition.Emit, emitLLVM), defaultString(definition.RunEmit, emitInterpret))
	}
	selected, err := resolveProjectTarget(project, options)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nSelected target: %s\n", selected.name)
	fmt.Fprintf(stdout, "Entry: %s\n", selected.entryPath)
	fmt.Fprintf(stdout, "Build emit: %s\n", selected.emit)
	fmt.Fprintf(stdout, "Run emit: %s\n", selected.runEmit)
	if selected.outputPath != "" {
		fmt.Fprintf(stdout, "Output: %s\n", selected.outputPath)
	}
	if selected.hasOptLevel {
		fmt.Fprintf(stdout, "Optimization: O%d\n", int(selected.optLevel))
	}
	fmt.Fprintf(stdout, "Include dirs:\n")
	for _, dir := range selected.includeDirs {
		fmt.Fprintf(stdout, "  - %s\n", dir)
	}
	fmt.Fprintf(stdout, "Dependency search paths:\n")
	for _, dir := range selected.dependencySearchPaths {
		fmt.Fprintf(stdout, "  - %s\n", dir)
	}
	fmt.Fprintf(stdout, "Resolved dependencies:\n")
	if len(selected.dependencyOrder) == 0 {
		fmt.Fprintln(stdout, "  - <none>")
	} else {
		for _, manifest := range selected.dependencyOrder {
			fmt.Fprintf(stdout, "  - %s (%s)\n", manifest.name, manifest.dir)
			if manifest.interfacePath != "" {
				fmt.Fprintf(stdout, "    interface=%s\n", manifest.interfacePath)
			}
			if len(manifest.foreignFiles) != 0 {
				fmt.Fprintf(stdout, "    foreign=%s\n", strings.Join(manifest.foreignFiles, ", "))
			}
		}
	}
	if len(selected.foreignFiles) != 0 {
		fmt.Fprintf(stdout, "Foreign sources:\n")
		for _, foreign := range selected.foreignFiles {
			fmt.Fprintf(stdout, "  - %s\n", foreign)
		}
	}
	fmt.Fprintf(stdout, "Exec hooks: project=%d target=%d dependencies=%d\n", len(selected.projectExec), len(selected.targetExec), countDependencyHooks(selected.dependencyOrder))
	return 0
}

func runProjectDeps(project *resolvedProject, options projectCLIOptions, stdout io.Writer, stderr io.Writer) int {
	selected, err := resolveProjectTarget(project, options)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	report, err := buildProjectDependencyReport(selected)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	payload, err := formatProjectDependencyReport(report, options.jsonOutput)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	fmt.Fprint(stdout, payload)
	return 0
}

func countDependencyHooks(manifests []*resolvedManifest) int {
	total := 0
	for _, manifest := range manifests {
		total += len(manifest.exec)
	}
	return total
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
