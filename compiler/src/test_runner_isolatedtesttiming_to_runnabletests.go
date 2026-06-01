package main

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/backend"
	"elisacore/src/semantic"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
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
func generateTestRunnerSource(inputFile string, result *semantic.Result, filter string) (string, error) {
	source, err := readSourceWithIncludes(inputFile, map[string]bool{})
	if err != nil {
		return "", err
	}
	return buildTestRunnerSource(source, selectTestCases(result, filter), filter), nil
}
func buildTestRunnerSource(source []byte, cases []selectedTestCase, filter string) string {
	runnable := runnableTests(cases)
	skipped := countSkippedTests(cases)
	permissionRefs := selectedTestPermissionRefs(runnable)
	grant := permissionGrantString(permissionRefs)
	bodyIndent := "\t"

	var out strings.Builder
	out.Write(source)
	if len(source) == 0 || source[len(source)-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	if !strings.Contains(string(source), "extern puts(") {
		out.WriteString("extern puts(text: u8&) -> int can[Console.Write]\n\n")
	}
	out.WriteString("def ctx_test_main() -> int")
	out.WriteString(semantic.PermissionRefsString(permissionRefs))
	out.WriteString(":\n")
	if grant != "" && len(cases) != 0 {
		out.WriteString("\tcan ")
		out.WriteString(grant)
		out.WriteString(":\n")
		bodyIndent = "\t\t"
	}

	if len(cases) == 0 {
		message := elisacoreStringLiteral(fmt.Sprintf("[ NO TESTS ] no @test functions matched filter %q", strings.TrimSpace(filter)))
		out.WriteString(bodyIndent)
		out.WriteString("puts(")
		out.WriteString(message)
		out.WriteString(".cast[u8&]) can Console.Write\n")
		out.WriteString(bodyIndent)
		out.WriteString("return 1\n\n")
		out.WriteString("export func main() -> int = ctx_test_main\n")
		return out.String()
	}

	for _, testCase := range cases {
		if testCase.Func == nil {
			continue
		}
		if testCase.skipped() {
			skippedLine := elisacoreStringLiteral(formatTestLine("SKIPPED", testCase.Func.Name, fmt.Sprintf(" (%s)", testCase.SkipReason)))
			out.WriteString("\tputs(")
			out.WriteString(skippedLine)
			out.WriteString(".cast[u8&])\n")
			continue
		}
		runLine := elisacoreStringLiteral(formatTestLine("RUN", testCase.Func.Name, ""))
		okLine := elisacoreStringLiteral(formatTestLine("OK", testCase.Func.Name, ""))
		out.WriteString(bodyIndent)
		out.WriteString("puts(")
		out.WriteString(runLine)
		out.WriteString(".cast[u8&])\n")
		out.WriteString(bodyIndent)
		out.WriteString(testCase.Func.Name)
		out.WriteString("()\n")
		out.WriteString(bodyIndent)
		out.WriteString("puts(")
		out.WriteString(okLine)
		out.WriteString(".cast[u8&])\n")
	}

	summaryLine := elisacoreStringLiteral(fmt.Sprintf("[ SUMMARY  ] %d test(s) selected; runnable=%d skipped=%d failed=0", len(cases), len(runnable), skipped))
	out.WriteString(bodyIndent)
	out.WriteString("puts(")
	out.WriteString(summaryLine)
	out.WriteString(".cast[u8&])\n")
	out.WriteString(bodyIndent)
	out.WriteString("return 0\n\n")
	out.WriteString("export func main() -> int = ctx_test_main\n")
	return out.String()
}
func buildIsolatedTestRunnerSource(source []byte, testCase selectedTestCase) string {
	permissionRefs := testCasePermissionRefs(testCase.Func)
	grant := permissionGrantString(permissionRefs)

	var out strings.Builder
	out.Write(source)
	if len(source) == 0 || source[len(source)-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	out.WriteString("def ctx_test_main() -> int")
	out.WriteString(semantic.PermissionRefsString(permissionRefs))
	out.WriteString(":\n")
	if testCase.Func != nil {
		out.WriteString("\t")
		out.WriteString(testCase.Func.Name)
		out.WriteString("()")
		if grant != "" {
			out.WriteString(" can ")
			out.WriteString(grant)
		}
		out.WriteString("\n")
	}
	out.WriteString("\t")
	out.WriteString("return 0\n\n")
	out.WriteString("export func main() -> int = ctx_test_main\n")
	return out.String()
}
func testCaseExportName(index int) string {
	return fmt.Sprintf("ctx_test_case_%d", index)
}
func testCaseInternalName(index int) string {
	return fmt.Sprintf("ctx_test_case_impl_%d", index)
}
func testCasePermissionRefs(testFn *semantic.AnnotatedFunc) []ast.PermissionRef {
	if testFn == nil || testFn.Signature == nil {
		return nil
	}
	if len(testFn.Signature.PermissionRefs) != 0 {
		return testFn.Signature.PermissionRefs
	}
	refs := make([]ast.PermissionRef, 0, len(testFn.Signature.Permissions))
	for _, family := range testFn.Signature.Permissions {
		refs = append(refs, ast.PermissionRef{Name: family})
	}
	return refs
}
func buildDispatchTestRunnerSource(source []byte, cases []selectedTestCase) string {
	var out strings.Builder
	out.Write(source)
	if len(source) == 0 || source[len(source)-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	for index, testCase := range cases {
		if testCase.Func == nil || testCase.skipped() {
			continue
		}
		exportName := testCaseExportName(index)
		internalName := testCaseInternalName(index)
		permissionRefs := testCasePermissionRefs(testCase.Func)
		grant := permissionGrantString(permissionRefs)
		out.WriteString("def ")
		out.WriteString(internalName)
		out.WriteString("() -> int")
		out.WriteString(semantic.PermissionRefsString(permissionRefs))
		out.WriteString(":\n")
		out.WriteString("\t")
		out.WriteString(testCase.Func.Name)
		out.WriteString("()")
		if grant != "" {
			out.WriteString(" can ")
			out.WriteString(grant)
		}
		out.WriteString("\n")
		out.WriteString("\t")
		out.WriteString("return 0\n\n")
		out.WriteString("export func ")
		out.WriteString(exportName)
		out.WriteString("() -> int = ")
		out.WriteString(internalName)
		out.WriteString("\n\n")
	}
	return out.String()
}
func testRunnerDispatchShimSource(cases []selectedTestCase) string {
	var out strings.Builder
	out.WriteString(testRunnerRuntimeShimSource())
	out.WriteString("\n#include <stdio.h>\n\nextern int strcmp(const char* lhs, const char* rhs);\n\n")
	for index, testCase := range cases {
		if testCase.Func == nil || testCase.skipped() {
			continue
		}
		out.WriteString("long long ")
		out.WriteString(testCaseExportName(index))
		out.WriteString("(void);\n")
	}
	out.WriteString("\nint main(int argc, char **argv) {\n")
	out.WriteString("    if (argc < 2) {\n")
	out.WriteString("        return 2;\n")
	out.WriteString("    }\n")
	out.WriteString("    const char *test_name = argv[1];\n")
	out.WriteString("    fprintf(stderr, \"[ ACTIVE   ] %s\\n\", test_name);\n")
	out.WriteString("    fflush(stderr);\n")
	for index, testCase := range cases {
		if testCase.Func == nil || testCase.skipped() {
			continue
		}
		out.WriteString("    if (strcmp(test_name, ")
		out.WriteString(strconv.Quote(testCase.Func.Name))
		out.WriteString(") == 0) {\n")
		out.WriteString("        return (int)")
		out.WriteString(testCaseExportName(index))
		out.WriteString("();\n")
		out.WriteString("    }\n")
	}
	out.WriteString("    return 2;\n")
	out.WriteString("}\n")
	return out.String()
}
func runnableTestCases(cases []selectedTestCase) []selectedTestCase {
	out := make([]selectedTestCase, 0, len(cases))
	for _, testCase := range cases {
		if testCase.Func == nil || testCase.skipped() {
			continue
		}
		out = append(out, testCase)
	}
	return out
}
func executeSelectedTests(inputFile string, result *semantic.Result, filter string, foreignFiles []string, linkFlags []string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool, stdout io.Writer, stderr io.Writer) int {
	suiteStart := time.Now()
	writeTestPhaseLine(stderr, "selected_tests", "read_source")
	source, err := readSourceWithIncludes(inputFile, map[string]bool{})
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	writeTestPhaseLine(stderr, "selected_tests", "select_cases")
	testCases := selectTestCases(result, filter)
	if len(testCases) == 0 {
		fmt.Fprintf(stdout, "[ NO TESTS ] no @test functions matched filter %q\n", strings.TrimSpace(filter))
		return 1
	}
	runnableCases := runnableTestCases(testCases)

	clangPath, err := exec.LookPath("clang")
	if err != nil {
		fmt.Fprintf(stderr, "error: clang is required to execute tests: %s\n", err)
		return 1
	}

	passed := 0
	skipped := 0
	failed := 0
	var compileTotal time.Duration
	var runTotal time.Duration
	var analyzeTotal time.Duration
	var nativeObjectTotal time.Duration
	var nativeHeaderGenTotal time.Duration
	var nativeHeaderWriteTotal time.Duration
	var nativeLinkTotal time.Duration
	var cacheLookupTotal time.Duration
	var cachePublishTotal time.Duration
	cacheHit := false
	compileStarted := false
	var exePath string
	cleanup := func() {}
	if len(runnableCases) > 0 {
		compileStarted = true
		writeTestPhaseLine(stderr, "selected_tests", "compile_dispatch")
		runnerSourceStart := time.Now()
		runnerSource := buildDispatchTestRunnerSource(source, runnableCases)
		runnerSourceElapsed := time.Since(runnerSourceStart)
		compileStart := time.Now()
		dispatchShim := testRunnerDispatchShimSource(runnableCases)
		var nativeTiming nativeBuildTiming
		var analyzeTime time.Duration
		var shimWriteTime time.Duration
		exePath, cleanup, nativeTiming, analyzeTime, shimWriteTime, err = compileTestRunnerExecutableWithShim(clangPath, runnerSource, dispatchShim, result.EASMModules, foreignFiles, linkFlags, optLevel, packedProfile, targetTriple, debugInfo, traceInfo, stderr)
		compileTotal = time.Since(compileStart)
		analyzeTotal = analyzeTime
		nativeObjectTotal = nativeTiming.ObjectWrite
		nativeHeaderGenTotal = nativeTiming.HeaderGen
		nativeHeaderWriteTotal = nativeTiming.HeaderWrite
		nativeLinkTotal = nativeTiming.Link
		cacheLookupTotal = nativeTiming.CacheLookup
		cachePublishTotal = nativeTiming.CachePublish
		cacheHit = nativeTiming.CacheHit
		if err != nil {
			cleanup()
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		writeTestTimingLine(stderr, "compile",
			durationTimingField("build_runner", runnerSourceElapsed),
			durationTimingField("compile", compileTotal),
			durationTimingField("analyze", analyzeTime),
			durationTimingField("shim", shimWriteTime),
			durationTimingField("obj", nativeTiming.ObjectWrite),
			durationTimingField("header_gen", nativeTiming.HeaderGen),
			durationTimingField("header_write", nativeTiming.HeaderWrite),
			durationTimingField("link", nativeTiming.Link),
			durationTimingField("cache_lookup", nativeTiming.CacheLookup),
			durationTimingField("cache_publish", nativeTiming.CachePublish),
			boolTimingField("cache_hit", nativeTiming.CacheHit),
		)
		defer cleanup()
	}
	writeTestPhaseLine(stderr, "selected_tests", "run_cases")
	for _, testCase := range testCases {
		if testCase.Func == nil {
			continue
		}
		if testCase.skipped() {
			skipped++
			fmt.Fprintln(stdout, formatTestLine("SKIPPED", testCase.Func.Name, fmt.Sprintf(" (%s)", testCase.SkipReason)))
			continue
		}

		fmt.Fprintln(stdout, formatTestLine("RUN", testCase.Func.Name, ""))
		testStart := time.Now()
		timing := isolatedTestTiming{}

		var testStdout bytes.Buffer
		var testStderr bytes.Buffer
		runCmd := nativeExecCommand(exePath, targetTriple, testCase.Func.Name)
		runCmd.Stdout = &testStdout
		runCmd.Stderr = &testStderr
		runStart := time.Now()
		runErr := runCmd.Run()
		timing.Run = time.Since(runStart)
		timing.Total = time.Since(testStart)
		runTotal += timing.Run
		writeTestTimingLine(stderr, testCase.Func.Name,
			durationTimingField("total", timing.Total),
			durationTimingField("run", timing.Run),
		)

		if runErr == nil {
			passed++
			fmt.Fprintln(stdout, formatTestLine("OK", testCase.Func.Name, ""))
			continue
		}

		failed++
		status, detail := classifyTestExecutionError(runErr)
		fmt.Fprintln(stdout, formatTestLine(status, testCase.Func.Name, detail))
		writeCapturedTestOutput(stdout, testCase.Func.Name, testStdout.String(), testStderr.String())
	}
	fmt.Fprintf(stdout, "[ SUMMARY  ] %d test(s) selected; passed=%d skipped=%d failed=%d\n", len(testCases), passed, skipped, failed)
	writeTestTimingLine(stderr, "suite",
		durationTimingField("total", time.Since(suiteStart)),
		durationTimingField("compile", compileTotal),
		durationTimingField("run", runTotal),
		durationTimingField("analyze", analyzeTotal),
		durationTimingField("obj", nativeObjectTotal),
		durationTimingField("header_gen", nativeHeaderGenTotal),
		durationTimingField("header_write", nativeHeaderWriteTotal),
		durationTimingField("link", nativeLinkTotal),
		durationTimingField("cache_lookup", cacheLookupTotal),
		durationTimingField("cache_publish", cachePublishTotal),
		boolTimingField("cache_hit", cacheHit),
		boolTimingField("compiled", compileStarted),
	)
	if failed > 0 {
		return 1
	}
	return 0
}
func runnableTests(cases []selectedTestCase) []*semantic.AnnotatedFunc {
	runnable := make([]*semantic.AnnotatedFunc, 0, len(cases))
	for _, testCase := range cases {
		if testCase.Func == nil || testCase.skipped() {
			continue
		}
		runnable = append(runnable, testCase.Func)
	}
	return runnable
}
