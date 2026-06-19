package main

import (
	"bytes"
	"elisacore/src/backend"
	"elisacore/src/easm"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

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
	inheritProjectNative := true
	if definition.InheritProjectNative != nil {
		inheritProjectNative = *definition.InheritProjectNative
	}
	projectForeign := project.config.Foreign
	projectEASM := project.config.EASM
	projectLinkFlags := project.config.LinkFlags
	if !inheritProjectNative {
		projectForeign = nil
		projectEASM = nil
		projectLinkFlags = nil
	}
	foreignFiles := make([]string, 0, len(projectForeign)+len(definition.Foreign))
	for _, path := range append(append([]string{}, projectForeign...), definition.Foreign...) {
		foreignFiles = append(foreignFiles, projectRelativePath(project.root, path))
	}
	easmFiles := make([]string, 0, len(projectEASM)+len(definition.EASM))
	for _, path := range append(append([]string{}, projectEASM...), definition.EASM...) {
		easmFiles = append(easmFiles, projectRelativePath(project.root, path))
	}
	linkFlags := make([]string, 0, len(projectLinkFlags)+len(definition.LinkFlags)+len(options.linkFlags))
	linkFlags = append(linkFlags, projectLinkFlags...)
	linkFlags = append(linkFlags, definition.LinkFlags...)
	linkFlags = append(linkFlags, options.linkFlags...)
	for _, manifest := range dependencyOrder {
		includeDirs = append(includeDirs, manifest.includeDirs...)
		foreignFiles = append(foreignFiles, manifest.foreignFiles...)
		easmFiles = append(easmFiles, manifest.easmFiles...)
		linkFlags = append(linkFlags, manifest.linkFlags...)
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
	targetTriple := strings.TrimSpace(definition.TargetTriple)
	if strings.TrimSpace(options.targetTriple) != "" {
		targetTriple = strings.TrimSpace(options.targetTriple)
	}
	packedProfile := backend.DefaultPackedLoweringProfile()
	if strings.TrimSpace(definition.PackedABI) != "" {
		return nil, fmt.Errorf("project target %q uses removed packed-abi override; use canonical packed lowering or enum-level @packed_profile(...) instead", targetName)
	}
	optLevel, hasOptLevel, err := resolveProjectOptLevel(definition.Opt, options)
	if err != nil {
		return nil, fmt.Errorf("project target %q: %w", targetName, err)
	}
	warnings := projectTargetWarningsFor(definition)
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
		easmFiles:             dedupeStrings(easmFiles),
		linkFlags:             dedupeStrings(linkFlags),
		projectExec:           append([]string{}, project.config.Exec...),
		targetExec:            append([]string{}, definition.Exec...),
		optLevel:              optLevel,
		hasOptLevel:           hasOptLevel,
		targetTriple:          targetTriple,
		packedProfile:         packedProfile,
		strictPolicy:          warnings.Strict || options.strictPolicy,
		perfStrict:            warnings.Strict || warnings.Perf || options.perfStrict,
		concurrencyStrict:     warnings.Strict || warnings.Concurrency || options.concurrencyStrict,
		proofStrict:           options.proofStrict,
		enableSMT:             options.enableSMT,
	}, nil
}
func projectTargetWarningsFor(definition projectTargetDefinition) projectTargetWarnings {
	if definition.Warnings == nil {
		return projectTargetWarnings{}
	}
	return *definition.Warnings
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
		easmFiles:    make([]string, 0, len(definition.EASM)),
		linkFlags:    append([]string{}, definition.LinkFlags...),
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
	for _, path := range definition.EASM {
		resolved.easmFiles = append(resolved.easmFiles, projectRelativePath(manifestDir, path))
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
	report, modules := easm.BuildReport(target.easmFiles, target.targetTriple)
	if easm.HasErrors(report) {
		payload, _ := easm.FormatReport(report, false)
		return nil, fmt.Errorf("EASM verification failed:\n%s", payload)
	}
	return &loadedProgram{filename: target.entryPath, source: combined.Bytes(), easm: modules}, nil
}
func readSourceWithIncludesWithOptions(filename string, seen map[string]bool, options sourceExpandOptions) ([]byte, error) {
	var out bytes.Buffer
	if err := writeSourceWithIncludesWithOptionsActive(&out, filename, seen, map[string]bool{}, options); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func writeSourceWithIncludesWithOptions(out *bytes.Buffer, filename string, seen map[string]bool, options sourceExpandOptions) error {
	return writeSourceWithIncludesWithOptionsActive(out, filename, seen, map[string]bool{}, options)
}

func writeSourceWithIncludesWithOptionsActive(out *bytes.Buffer, filename string, included map[string]bool, active map[string]bool, options sourceExpandOptions) error {
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
			resolved, err := resolveExpandedIncludePath(filepath.Dir(abs), includePath, options.includeDirs)
			if err != nil {
				return err
			}
			indent := leadingWhitespaceBytes(line)
			var includeBuf bytes.Buffer
			outLenBefore := out.Len()
			if err := writeSourceWithIncludesWithOptionsActive(&includeBuf, resolved, included, active, options); err != nil {
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
func resolveExpandedIncludePath(currentDir string, includePath string, includeDirs []string) (string, error) {
	// An absolute include path is used verbatim. filepath.Join(currentDir,
	// includePath) does NOT honor an absolute second arg — it concatenates — so
	// without this branch `include "/abs/x.elisa"` would silently resolve to
	// `<currentDir>/abs/x.elisa` and fail with a confusing not-found.
	if filepath.IsAbs(includePath) {
		if _, err := os.Stat(includePath); err == nil {
			return includePath, nil
		}
		return "", fmt.Errorf("include %q not found", includePath)
	}
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
	fmt.Fprintf(stdout, "Target triple: %s\n", defaultString(selected.targetTriple, "<host-default>"))
	if selected.outputPath != "" {
		fmt.Fprintf(stdout, "Output: %s\n", selected.outputPath)
	}
	if selected.hasOptLevel {
		fmt.Fprintf(stdout, "Optimization: O%d\n", int(selected.optLevel))
	}
	fmt.Fprintf(stdout, "Warning policy: strict=%s perf=%s concurrency=%s\n", onOffString(selected.strictPolicy), enabledString(selected.perfStrict), enabledString(selected.concurrencyStrict))
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
			if len(manifest.easmFiles) != 0 {
				fmt.Fprintf(stdout, "    easm=%s\n", strings.Join(manifest.easmFiles, ", "))
			}
		}
	}
	if len(selected.foreignFiles) != 0 {
		fmt.Fprintf(stdout, "Foreign sources:\n")
		for _, foreign := range selected.foreignFiles {
			fmt.Fprintf(stdout, "  - %s\n", foreign)
		}
	}
	if len(selected.easmFiles) != 0 {
		fmt.Fprintf(stdout, "EASM sources:\n")
		for _, source := range selected.easmFiles {
			fmt.Fprintf(stdout, "  - %s\n", source)
		}
	}
	if len(selected.linkFlags) != 0 {
		fmt.Fprintf(stdout, "Link flags:\n")
		for _, flag := range selected.linkFlags {
			fmt.Fprintf(stdout, "  - %s\n", flag)
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
func runProjectABILint(project *resolvedProject, options projectCLIOptions, stdout io.Writer, stderr io.Writer) int {
	selected, err := resolveProjectTarget(project, options)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	report, err := buildNativeABILintReport(selected, nativeABILintOptions{StrictContracts: options.strictContracts})
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	payload, err := formatNativeABILintReport(report, options.jsonOutput)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	fmt.Fprint(stdout, payload)
	if nativeABILintHasErrors(report) {
		return 1
	}
	return 0
}
func runProjectEASMLint(project *resolvedProject, options projectCLIOptions, stdout io.Writer, stderr io.Writer) int {
	selected, err := resolveProjectTarget(project, options)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	report, _ := easm.BuildReport(selected.easmFiles, selected.targetTriple)
	payload, err := easm.FormatReport(report, options.jsonOutput)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	fmt.Fprint(stdout, payload)
	if easm.HasErrors(report) {
		return 1
	}
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
func enabledString(enabled bool) string {
	if enabled {
		return "error"
	}
	return "warn"
}
func onOffString(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
