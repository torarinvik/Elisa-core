package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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
	fmt.Fprintln(w, "  elisacore init <name> [--path <dir>] [--strict]")
	fmt.Fprintln(w, "  elisacore init-lib <name> [--path <dir>]")
	fmt.Fprintln(w, "  elisacore build [target] [--project <dir|project.json>] [-emit <mode>] [-o <output>] [-link <flag>|-L <dir>|-l <name>] [--trust <none|include|full>] [-O0|-O1|-O2|-O3]")
	fmt.Fprintln(w, "  elisacore run [target] [--project <dir|project.json>] [-link <flag>|-L <dir>|-l <name>] [--trust <none|include|full>]")
	fmt.Fprintln(w, "  elisacore test [target] [--project <dir|project.json>] [-filter <substring>] [-link <flag>|-L <dir>|-l <name>] [--trust <none|include|full>] [-O0|-O1|-O2|-O3]")
	fmt.Fprintln(w, "  elisacore bench [target] [--project <dir|project.json>] [-filter <substring>] [--trust <none|include|full>]")
	fmt.Fprintln(w, "  elisacore project view [target] [--project <dir|project.json>]")
	fmt.Fprintln(w, "  elisacore project deps [target] [--project <dir|project.json>] [--json]")
	fmt.Fprintln(w, "  elisacore project abi-lint [target] [--project <dir|project.json>] [--json] [--strict-contracts]")
	fmt.Fprintln(w, "  elisacore project easm-lint [target] [--project <dir|project.json>] [--json]")
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
	if options.strictPolicy {
		target := project.Targets["app"]
		target.Warnings = &projectTargetWarnings{Strict: true}
		project.Targets["app"] = target
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
