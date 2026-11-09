package main

import (
	"bytes"
	"elisacore/src/backend"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	linkFlags = append(linkFlags, platformLinkFlagsFor(definition.Platforms)...)
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
	lto, ok := backend.ParseLTOMode(definition.LTO)
	if !ok {
		return nil, fmt.Errorf("project target %q: unsupported lto %q (expected \"full\", \"thin\" or \"off\")", targetName, definition.LTO)
	}
	if options.lto != backend.LTOOff {
		lto = options.lto
	}
	targetCPU := backend.NormalizeTargetCPU(definition.TargetCPU)
	if options.targetCPU != "" {
		targetCPU = options.targetCPU
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
		flowLintMode:          options.flowLintMode,
		concurrencyStrict:     warnings.Strict || warnings.Concurrency || options.concurrencyStrict,
		progressStrict:        warnings.Strict || warnings.Progress || options.progressStrict,
		proofStrict:           options.proofStrict,
		enableSMT:             options.enableSMT,
		lto:                   lto,
		targetCPU:             targetCPU,
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
