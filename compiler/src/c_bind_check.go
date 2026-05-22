package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"elisacore/src/semantic"
)

type cBindFieldCheck struct {
	Name   string
	Offset int
}

type cBindStructCheck struct {
	ElisaName string
	CName     string
	Header    string
	Size      int
	Align     int
	Prefix    bool
	Fields    []cBindFieldCheck
}

type cBindCLayout struct {
	Size    int
	Align   int
	Offsets map[string]int
}

func runCBindLayoutCheck(result *semantic.Result, targetTriple string, stdout io.Writer) error {
	checks, err := collectCBindChecks(result)
	if err != nil {
		return err
	}
	if len(checks) == 0 {
		fmt.Fprintln(stdout, "c-bind-check: no @c_bind structs")
		return nil
	}
	cLayouts, err := probeCBindLayouts(checks, targetTriple)
	if err != nil {
		return err
	}
	var mismatches []string
	for _, check := range checks {
		cLayout, ok := cLayouts[check.ElisaName]
		if !ok {
			mismatches = append(mismatches, fmt.Sprintf("%s: missing C probe result", check.ElisaName))
			continue
		}
		if !check.Prefix && check.Size != cLayout.Size {
			mismatches = append(mismatches, fmt.Sprintf("%s: size mismatch Elisa=%d C=%d", check.ElisaName, check.Size, cLayout.Size))
		}
		if !check.Prefix && check.Align != cLayout.Align {
			mismatches = append(mismatches, fmt.Sprintf("%s: align mismatch Elisa=%d C=%d", check.ElisaName, check.Align, cLayout.Align))
		}
		for _, field := range check.Fields {
			cOffset, ok := cLayout.Offsets[field.Name]
			if !ok {
				mismatches = append(mismatches, fmt.Sprintf("%s.%s: missing C offsetof result", check.ElisaName, field.Name))
				continue
			}
			if field.Offset != cOffset {
				mismatches = append(mismatches, fmt.Sprintf("%s.%s: offset mismatch Elisa=%d C=%d", check.ElisaName, field.Name, field.Offset, cOffset))
			}
		}
	}
	if len(mismatches) != 0 {
		return fmt.Errorf("C binding layout check failed:\n%s", strings.Join(mismatches, "\n"))
	}
	for _, check := range checks {
		if check.Prefix {
			fmt.Fprintf(stdout, "c-bind-check: %s prefix matches %s from %s (fields=%d)\n", check.ElisaName, check.CName, check.Header, len(check.Fields))
		} else {
			fmt.Fprintf(stdout, "c-bind-check: %s matches %s from %s (size=%d align=%d fields=%d)\n", check.ElisaName, check.CName, check.Header, check.Size, check.Align, len(check.Fields))
		}
	}
	return nil
}

func collectCBindChecks(result *semantic.Result) ([]cBindStructCheck, error) {
	if result == nil {
		return nil, fmt.Errorf("missing semantic result")
	}
	names := make([]string, 0, len(result.NamedTypes))
	for name := range result.NamedTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	checks := make([]cBindStructCheck, 0)
	for _, name := range names {
		st, ok := result.NamedTypes[name].(*semantic.StructType)
		if !ok || st.CBindHeader == "" || st.CBindName == "" {
			continue
		}
		layout, ok := result.HostABIStructLayout(st)
		if !ok {
			return nil, fmt.Errorf("@c_bind struct %q has no computable host ABI layout", name)
		}
		fields := make([]cBindFieldCheck, 0, len(layout.Fields))
		for _, field := range layout.Fields {
			fields = append(fields, cBindFieldCheck{Name: field.Name, Offset: field.Offset})
		}
		checks = append(checks, cBindStructCheck{
			ElisaName: name,
			CName:     st.CBindName,
			Header:    st.CBindHeader,
			Size:      layout.Size,
			Align:     layout.Align,
			Prefix:    st.CBindPrefix,
			Fields:    fields,
		})
	}
	return checks, nil
}

func probeCBindLayouts(checks []cBindStructCheck, targetTriple string) (map[string]cBindCLayout, error) {
	tmpDir, err := os.MkdirTemp("", "elisa-c-bind-check-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	sourcePath := filepath.Join(tmpDir, "probe.c")
	exePath := filepath.Join(tmpDir, "probe")
	if err := os.WriteFile(sourcePath, []byte(cBindProbeSource(checks)), 0o644); err != nil {
		return nil, err
	}
	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}
	args := append([]string{"-std=c11"}, targetClangArgs(targetTriple)...)
	args = append(args, shellFields(os.Getenv("CPPFLAGS"))...)
	args = append(args, shellFields(os.Getenv("CFLAGS"))...)
	args = append(args, sourcePath, "-o", exePath)
	compile := exec.Command(cc, args...)
	var compileErr bytes.Buffer
	compile.Stderr = &compileErr
	if err := compile.Run(); err != nil {
		return nil, fmt.Errorf("failed to compile C binding probe with %s: %v\n%s", cc, err, strings.TrimSpace(compileErr.String()))
	}
	run := nativeExecCommand(exePath, targetTriple)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	run.Stdout = &stdout
	run.Stderr = &stderr
	if err := run.Run(); err != nil {
		return nil, fmt.Errorf("failed to run C binding probe: %v\n%s", err, strings.TrimSpace(stderr.String()))
	}
	return parseCBindProbeOutput(stdout.String())
}

func cBindProbeSource(checks []cBindStructCheck) string {
	var b strings.Builder
	b.WriteString("#include <stddef.h>\n#include <stdio.h>\n")
	included := map[string]bool{}
	for _, check := range checks {
		if included[check.Header] {
			continue
		}
		included[check.Header] = true
		b.WriteString("#include ")
		b.WriteString(formatCInclude(check.Header))
		b.WriteByte('\n')
	}
	b.WriteString("int main(void) {\n")
	for _, check := range checks {
		fmt.Fprintf(&b, "  printf(\"STRUCT %%s %%zu %%zu\\n\", %q, sizeof(%s), _Alignof(%s));\n", check.ElisaName, check.CName, check.CName)
		for _, field := range check.Fields {
			fmt.Fprintf(&b, "  printf(\"FIELD %%s %%s %%zu\\n\", %q, %q, offsetof(%s, %s));\n", check.ElisaName, field.Name, check.CName, field.Name)
		}
	}
	b.WriteString("  return 0;\n}\n")
	return b.String()
}

func formatCInclude(header string) string {
	header = strings.TrimSpace(header)
	if strings.HasPrefix(header, "<") || strings.HasPrefix(header, "\"") {
		return header
	}
	if filepath.IsAbs(header) {
		return strconv.Quote(header)
	}
	return "<" + header + ">"
}

func shellFields(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	fields := make([]string, 0)
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		fields = append(fields, current.String())
		current.Reset()
	}
	for _, ch := range value {
		if escaped {
			current.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else {
				current.WriteRune(ch)
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			flush()
			continue
		}
		current.WriteRune(ch)
	}
	if escaped {
		current.WriteRune('\\')
	}
	flush()
	return fields
}

func parseCBindProbeOutput(output string) (map[string]cBindCLayout, error) {
	layouts := map[string]cBindCLayout{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		switch {
		case len(parts) == 4 && parts[0] == "STRUCT":
			size, err := strconv.Atoi(parts[2])
			if err != nil {
				return nil, fmt.Errorf("invalid C probe size in %q", line)
			}
			align, err := strconv.Atoi(parts[3])
			if err != nil {
				return nil, fmt.Errorf("invalid C probe align in %q", line)
			}
			layout := layouts[parts[1]]
			layout.Size = size
			layout.Align = align
			if layout.Offsets == nil {
				layout.Offsets = map[string]int{}
			}
			layouts[parts[1]] = layout
		case len(parts) == 4 && parts[0] == "FIELD":
			offset, err := strconv.Atoi(parts[3])
			if err != nil {
				return nil, fmt.Errorf("invalid C probe offset in %q", line)
			}
			layout := layouts[parts[1]]
			if layout.Offsets == nil {
				layout.Offsets = map[string]int{}
			}
			layout.Offsets[parts[2]] = offset
			layouts[parts[1]] = layout
		default:
			return nil, fmt.Errorf("invalid C probe output line %q", line)
		}
	}
	return layouts, nil
}
