package main

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

type isolatedTestTiming struct {
	RunnerSource time.Duration
	Compile      time.Duration
	Run          time.Duration
	Native       nativeBuildTiming
	Analyze      time.Duration
	ShimWrite    time.Duration
	Total        time.Duration
}
type timingField struct {
	textKey   string
	textValue string
	jsonKey   string
	jsonValue any
}

func testTimingMode() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("ELISACORE_TEST_TIMING")))
}
func testTimingEnabled() bool {
	mode := testTimingMode()
	return mode != "" && mode != "0" && mode != "false" && mode != "off"
}
func testTimingJSONEnabled() bool {
	mode := testTimingMode()
	return mode == "json" || mode == "jsonl"
}
func testPhaseDebugEnabled() bool {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("ELISACORE_TEST_PHASE_DEBUG")))
	return mode != "" && mode != "0" && mode != "false" && mode != "off"
}
func writeTestPhaseLine(w io.Writer, phase string, detail string) {
	if w == nil || !testPhaseDebugEnabled() {
		return
	}
	phase = strings.TrimSpace(phase)
	detail = strings.TrimSpace(detail)
	if detail == "" {
		fmt.Fprintf(w, "[ phase    ] %s\n", phase)
		return
	}
	fmt.Fprintf(w, "[ phase    ] %s %s\n", phase, detail)
}
func durationTimingField(key string, value time.Duration) timingField {
	return timingField{
		textKey:   key,
		textValue: value.Round(time.Millisecond).String(),
		jsonKey:   key + "_ms",
		jsonValue: float64(value) / float64(time.Millisecond),
	}
}
func boolTimingField(key string, value bool) timingField {
	return timingField{
		textKey:   key,
		textValue: fmt.Sprintf("%t", value),
		jsonKey:   key,
		jsonValue: value,
	}
}
func writeTestTimingLine(w io.Writer, label string, fields ...timingField) {
	if w == nil || !testTimingEnabled() {
		return
	}
	if testTimingJSONEnabled() {
		event := map[string]any{"label": label}
		for _, field := range fields {
			if field.jsonKey == "" {
				continue
			}
			event[field.jsonKey] = field.jsonValue
		}
		_ = json.NewEncoder(w).Encode(event)
		return
	}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		if field.textKey == "" {
			continue
		}
		parts = append(parts, field.textKey+"="+field.textValue)
	}
	if len(parts) == 0 {
		fmt.Fprintf(w, "[ timing   ] %s\n", label)
		return
	}
	fmt.Fprintf(w, "[ timing   ] %s %s\n", label, strings.Join(parts, " "))
}
func selectedTestPermissionRefs(tests []*semantic.AnnotatedFunc) []ast.PermissionRef {
	refs := []ast.PermissionRef{{Name: "Console", Member: "Write"}}
	seen := map[string]bool{"Console.Write": true}
	for _, testFn := range tests {
		if testFn == nil || testFn.Signature == nil {
			continue
		}
		fnRefs := testFn.Signature.PermissionRefs
		if len(fnRefs) == 0 {
			for _, family := range testFn.Signature.Permissions {
				fnRefs = append(fnRefs, ast.PermissionRef{Name: family})
			}
		}
		for _, ref := range fnRefs {
			key := semantic.PermissionRefString(ref)
			if seen[key] {
				continue
			}
			seen[key] = true
			refs = append(refs, ref)
		}
	}
	return refs
}
func permissionGrantString(refs []ast.PermissionRef) string {
	if len(refs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		parts = append(parts, semantic.PermissionRefString(ref))
	}
	return strings.Join(parts, ", ")
}

type selectedTestCase struct {
	Func       *semantic.AnnotatedFunc
	SkipReason string
}

func (tc selectedTestCase) skipped() bool {
	return strings.TrimSpace(tc.SkipReason) != ""
}
func selectTestCases(result *semantic.Result, filter string) []selectedTestCase {
	selected := selectAnnotatedFunctions(result, "test", filter)
	if len(selected) == 0 {
		return nil
	}
	cases := make([]selectedTestCase, 0, len(selected))
	for _, fn := range selected {
		skipReason, _ := skipReasonForAnnotatedFunc(fn)
		cases = append(cases, selectedTestCase{Func: fn, SkipReason: skipReason})
	}
	return cases
}
func skipReasonForAnnotatedFunc(fn *semantic.AnnotatedFunc) (string, bool) {
	if fn == nil {
		return "", false
	}
	for _, annotation := range fn.Annotations {
		switch strings.ToLower(strings.TrimSpace(annotation.Name)) {
		case "skip", "ignore":
			reason := strings.TrimSpace(strings.Join(annotation.Args, ", "))
			if reason == "" {
				reason = "annotation-requested"
			}
			return reason, true
		}
	}
	return "", false
}
func matchesNameFilter(name string, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	name = strings.ToLower(strings.TrimSpace(name))
	patterns := strings.Split(strings.ToLower(filter), ",")
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if strings.ContainsAny(pattern, "*?[") {
			matched, err := path.Match(pattern, name)
			if err == nil && matched {
				return true
			}
			continue
		}
		if strings.Contains(name, pattern) {
			return true
		}
	}
	return false
}
func selectAnnotatedFunctions(result *semantic.Result, annotationName string, filter string) []*semantic.AnnotatedFunc {
	if result == nil || annotationName == "" {
		return nil
	}
	selected := make([]*semantic.AnnotatedFunc, 0)
	for _, fn := range result.AnnotatedFuncs {
		if fn == nil || !hasAnnotation(fn, annotationName) {
			continue
		}
		if !matchesNameFilter(fn.Name, filter) {
			continue
		}
		selected = append(selected, fn)
	}
	return selected
}
func generateTestRunnerSource(program *loadedProgram, result *semantic.Result, filter string) (string, error) {
	if program == nil {
		return "", fmt.Errorf("missing loaded test program")
	}
	source := append([]byte(nil), program.source...)
	source = appendLawFuzzHarnessSource(source, result)
	driverSource, propertyCases := buildPropertyDrivers(result, filter)
	if driverSource != "" {
		if len(source) != 0 && source[len(source)-1] != '\n' {
			source = append(source, '\n')
		}
		source = append(source, []byte(driverSource)...)
	}
	cases := append(selectTestCases(result, filter), propertyCases...)
	return buildTestRunnerSource(source, cases, filter), nil
}
