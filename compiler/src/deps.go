package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type sourceDependencyReport struct {
	Root  string   `json:"root"`
	Files []string `json:"files"`
}

type manifestDependencyReport struct {
	Name        string   `json:"name"`
	Manifest    string   `json:"manifest"`
	Entry       string   `json:"entry,omitempty"`
	Interface   string   `json:"interface,omitempty"`
	IncludeDirs []string `json:"includeDirs,omitempty"`
	Foreign     []string `json:"foreign,omitempty"`
	Sources     []string `json:"sources,omitempty"`
}

type projectDependencyReport struct {
	Project               string                     `json:"project"`
	Target                string                     `json:"target"`
	Entry                 string                     `json:"entry"`
	IncludeDirs           []string                   `json:"includeDirs,omitempty"`
	DependencySearchPaths []string                   `json:"dependencySearchPaths,omitempty"`
	Sources               []string                   `json:"sources"`
	Foreign               []string                   `json:"foreign,omitempty"`
	Dependencies          []manifestDependencyReport `json:"dependencies,omitempty"`
}

func buildSourceDependencyReport(filename string, options sourceExpandOptions) (*sourceDependencyReport, error) {
	files, err := collectSourceDependencies(filename, options)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return &sourceDependencyReport{}, nil
	}
	return &sourceDependencyReport{Root: files[0], Files: files}, nil
}

func formatSourceDependencyReport(report *sourceDependencyReport, jsonOutput bool) (string, error) {
	if report == nil {
		return "", nil
	}
	if jsonOutput {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data) + "\n", nil
	}
	if len(report.Files) == 0 {
		return "", nil
	}
	return strings.Join(report.Files, "\n") + "\n", nil
}

func collectSourceDependencies(filename string, options sourceExpandOptions) ([]string, error) {
	files := make([]string, 0, 8)
	seen := map[string]bool{}
	active := map[string]bool{}
	if err := appendSourceDependencies(&files, filename, seen, active, options); err != nil {
		return nil, err
	}
	return files, nil
}

func appendSourceDependencies(out *[]string, filename string, seen map[string]bool, active map[string]bool, options sourceExpandOptions) error {
	abs, err := filepath.Abs(filename)
	if err != nil {
		return err
	}
	if active[abs] {
		return fmt.Errorf("cyclic include detected for %s", abs)
	}
	if seen[abs] {
		return nil
	}
	active[abs] = true
	defer delete(active, abs)
	seen[abs] = true
	*out = append(*out, abs)

	raw, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
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
			if err := appendSourceDependencies(out, resolved, seen, active, options); err != nil {
				return err
			}
		}
		if !hasNewline {
			break
		}
		start = end + 1
	}
	return nil
}

func orderedProjectDependencyEntries(target *resolvedProjectTarget) []string {
	entries := make([]string, 0, len(target.dependencyOrder)+1)
	for _, manifest := range target.dependencyOrder {
		if manifest.entryPath != "" {
			entries = append(entries, manifest.entryPath)
			continue
		}
		if manifest.interfacePath != "" {
			entries = append(entries, manifest.interfacePath)
		}
	}
	entries = append(entries, target.entryPath)
	return entries
}

func buildProjectDependencyReport(target *resolvedProjectTarget) (*projectDependencyReport, error) {
	if target == nil {
		return nil, fmt.Errorf("resolved project target is nil")
	}
	report := &projectDependencyReport{
		Project:               target.project.filePath,
		Target:                target.name,
		Entry:                 target.entryPath,
		IncludeDirs:           append([]string(nil), target.includeDirs...),
		DependencySearchPaths: append([]string(nil), target.dependencySearchPaths...),
		Foreign:               append([]string(nil), target.foreignFiles...),
	}
	seen := map[string]bool{}
	for _, entry := range orderedProjectDependencyEntries(target) {
		files, err := collectSourceDependencies(entry, sourceExpandOptions{includeDirs: target.includeDirs})
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if seen[file] {
				continue
			}
			seen[file] = true
			report.Sources = append(report.Sources, file)
		}
	}
	for _, manifest := range target.dependencyOrder {
		depReport := manifestDependencyReport{
			Name:        manifest.name,
			Manifest:    manifest.manifestPath,
			Entry:       manifest.entryPath,
			Interface:   manifest.interfacePath,
			IncludeDirs: append([]string(nil), manifest.includeDirs...),
			Foreign:     append([]string(nil), manifest.foreignFiles...),
		}
		if manifest.entryPath != "" {
			files, err := collectSourceDependencies(manifest.entryPath, sourceExpandOptions{includeDirs: target.includeDirs})
			if err != nil {
				return nil, err
			}
			depReport.Sources = files
		} else if manifest.interfacePath != "" {
			files, err := collectSourceDependencies(manifest.interfacePath, sourceExpandOptions{includeDirs: target.includeDirs})
			if err != nil {
				return nil, err
			}
			depReport.Sources = files
		}
		report.Dependencies = append(report.Dependencies, depReport)
	}
	return report, nil
}

func formatProjectDependencyReport(report *projectDependencyReport, jsonOutput bool) (string, error) {
	if report == nil {
		return "", nil
	}
	if jsonOutput {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data) + "\n", nil
	}
	var out strings.Builder
	out.WriteString("Project: ")
	out.WriteString(report.Project)
	out.WriteString("\nTarget: ")
	out.WriteString(report.Target)
	out.WriteString("\nEntry: ")
	out.WriteString(report.Entry)
	out.WriteString("\nSources:\n")
	for _, source := range report.Sources {
		out.WriteString("  - ")
		out.WriteString(source)
		out.WriteByte('\n')
	}
	if len(report.Foreign) != 0 {
		out.WriteString("Foreign sources:\n")
		for _, foreign := range report.Foreign {
			out.WriteString("  - ")
			out.WriteString(foreign)
			out.WriteByte('\n')
		}
	}
	if len(report.Dependencies) != 0 {
		out.WriteString("Dependencies:\n")
		for _, dep := range report.Dependencies {
			out.WriteString("  - ")
			out.WriteString(dep.Name)
			out.WriteByte('\n')
			out.WriteString("    manifest: ")
			out.WriteString(dep.Manifest)
			out.WriteByte('\n')
			if dep.Entry != "" {
				out.WriteString("    entry: ")
				out.WriteString(dep.Entry)
				out.WriteByte('\n')
			}
			if dep.Interface != "" {
				out.WriteString("    interface: ")
				out.WriteString(dep.Interface)
				out.WriteByte('\n')
			}
			if len(dep.Foreign) != 0 {
				out.WriteString("    foreign:\n")
				for _, foreign := range dep.Foreign {
					out.WriteString("      - ")
					out.WriteString(foreign)
					out.WriteByte('\n')
				}
			}
			if len(dep.Sources) != 0 {
				out.WriteString("    sources:\n")
				for _, source := range dep.Sources {
					out.WriteString("      - ")
					out.WriteString(source)
					out.WriteByte('\n')
				}
			}
		}
	}
	return out.String(), nil
}
