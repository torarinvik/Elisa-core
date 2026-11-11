package main

import (
	"bytes"
	"elisacore/src/backend"
	"elisacore/src/easm"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

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
	var out sourceSplice
	if err := writeSourceWithIncludesLabels(&out, filename, seen, map[string]bool{}, &options, nil, sourceLabelResolver{}); err != nil {
		return nil, err
	}
	return out.bytes(), nil
}
func writeSourceWithIncludesWithOptions(out *bytes.Buffer, filename string, seen map[string]bool, options sourceExpandOptions) error {
	return writeSourceWithIncludesWithOptionsActive(out, filename, seen, map[string]bool{}, options)
}

func writeSourceWithIncludesWithOptionsActive(out *bytes.Buffer, filename string, included map[string]bool, active map[string]bool, options sourceExpandOptions) error {
	return writeSourceWithIncludesPrefixed(out, filename, included, active, &options, nil)
}

// writeSourceWithIncludesPrefixed streams included files into one output
// buffer. Prefixes reproduce the indentation that each recursive include splice adds
// to non-#line lines, without materializing and copying a temporary expanded buffer at
// every include depth.
func writeSourceWithIncludesPrefixed(out *bytes.Buffer, filename string, included map[string]bool, active map[string]bool, options *sourceExpandOptions, prefixes [][]byte) error {
	return writeSourceWithIncludesLabels(&sourceSplice{buffer: out}, filename, included, active, options, prefixes, sourceLabelResolver{})
}

func writeSourceWithIncludesLabels(out *sourceSplice, filename string, included map[string]bool, active map[string]bool, options *sourceExpandOptions, prefixes [][]byte, labels sourceLabelResolver) error {
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
	sourceLabel := labels.resolve(abs)
	out.lineDirective(1, sourceLabel)
	// An unindented splice can write consecutive ordinary lines at once.
	// Flush the run before an include or a rewritten source-map marker.
	written := 0
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
		next := end
		if hasNewline {
			next++
		}
		if includePath, ok := parseIncludeDirectiveBytes(bytes.TrimSpace(line)); ok {
			if len(prefixes) == 0 {
				out.write(raw[written:start])
				written = next
			}
			resolved := includePath
			if options == nil {
				if !filepath.IsAbs(resolved) {
					resolved = filepath.Join(filepath.Dir(abs), resolved)
				}
			} else {
				resolved, err = resolveExpandedIncludePath(filepath.Dir(abs), includePath, options.includeDirs)
				if err != nil {
					return err
				}
			}
			indent := leadingWhitespaceBytes(line)
			outLenBefore := out.len()
			childPrefixes := prefixes
			if len(indent) != 0 {
				out.useBuffer()
				childPrefixes = append(prefixes, indent)
			}
			if err := writeSourceWithIncludesLabels(out, resolved, included, active, options, childPrefixes, labels); err != nil {
				return err
			}
			if out.len() == outLenBefore || !out.endsInNewline() {
				out.write(sourceSpliceNewline)
			}
			// Resume attribution to the parent file after the spliced include.
			out.lineDirective(curLine+1, sourceLabel)
		} else {
			// #line is an internal source-map directive. Keep source-authored
			// spellings as ordinary comments so they cannot forge declaration
			// provenance for trusted-runtime compiler proofs.
			trimmed := bytes.TrimLeft(line, " \t")
			if bytes.HasPrefix(trimmed, []byte("#line ")) || bytes.HasPrefix(trimmed, []byte("#line\t")) {
				if len(prefixes) == 0 {
					out.write(raw[written:start])
					written = next
				}
				for _, prefix := range prefixes {
					out.write(prefix)
				}
				indent := len(line) - len(trimmed)
				out.write(line[:indent])
				out.write([]byte("# line"))
				out.write(trimmed[len("#line"):])
				if hasNewline {
					out.write(sourceSpliceNewline)
				}
			} else if len(prefixes) != 0 {
				if len(line) > 0 {
					for _, prefix := range prefixes {
						out.write(prefix)
					}
				}
				out.write(raw[start:next])
			}
		}
		if !hasNewline {
			break
		}
		start = next
		curLine++
	}
	if len(prefixes) == 0 {
		out.write(raw[written:])
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
	if selected.lto != backend.LTOOff {
		fmt.Fprintf(stdout, "LTO: %s\n", selected.lto)
	}
	if selected.targetCPU != "" {
		fmt.Fprintf(stdout, "Target CPU: %s\n", selected.targetCPU)
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
