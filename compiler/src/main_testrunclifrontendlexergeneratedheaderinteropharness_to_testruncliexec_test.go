package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestRunCLIJSONParserGeneratedHeaderInteropBuildSmoke(t *testing.T) {
	t.Parallel()
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser.elisa")
	harnessPath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser_generated_harness.c")
	shimPath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c")
	outputDir := t.TempDir()
	headerPath := filepath.Join(outputDir, "json_parser.h")
	objectPath := filepath.Join(outputDir, "json_parser.o")
	exePath := filepath.Join(outputDir, "json_parser_generated_harness")

	for _, args := range [][]string{
		{"-emit", "header", "-o", headerPath, fixturePath},
		// This test validates generated-header ABI wiring, not optimized code quality.
		{"-emit", "obj", "-O0", "-o", objectPath, fixturePath},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exitCode := runCLI(args, &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("runCLI(%v) returned %d\nstderr:\n%s", args, exitCode, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("expected no stdout for %v, got:\n%s", args, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("expected no stderr for %v, got:\n%s", args, stderr.String())
		}
	}

	compileArgs := []string{"-pthread", "-I", outputDir, harnessPath, shimPath, objectPath, "-o", exePath}
	if runtime.GOOS == "darwin" {
		compileArgs = append([]string{"-Wl,-undefined,dynamic_lookup"}, compileArgs...)
	}
	compileCmd := exec.Command(clangPath, compileArgs...)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}
}
func TestRunCLIJSONParserParallelBenchBuildSmoke(t *testing.T) {
	t.Parallel()
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser.elisa")
	benchPath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_parallel_bench.c")
	shimPath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c")
	outputDir := t.TempDir()
	headerPath := filepath.Join(outputDir, "json_parser.h")
	objectPath := filepath.Join(outputDir, "json_parser.o")
	exePath := filepath.Join(outputDir, "json_parser_parallel_bench")

	for _, args := range [][]string{
		{"-emit", "header", "-o", headerPath, fixturePath},
		// This test validates benchmark wiring and smoke behavior, not optimized code quality.
		{"-emit", "obj", "-O0", "-o", objectPath, fixturePath},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exitCode := runCLI(args, &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("runCLI(%v) returned %d\nstderr:\n%s", args, exitCode, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("expected no stdout for %v, got:\n%s", args, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("expected no stderr for %v, got:\n%s", args, stderr.String())
		}
	}

	compileArgs := []string{"-pthread", "-I", outputDir, benchPath, shimPath, objectPath, "-o", exePath}
	if runtime.GOOS == "darwin" {
		compileArgs = append([]string{"-Wl,-undefined,dynamic_lookup"}, compileArgs...)
	}
	compileCmd := exec.Command(clangPath, compileArgs...)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}
}
func runPackedMLASTBenchSmoke(t *testing.T, exePath string) {
	t.Helper()

	for _, tc := range []struct {
		name     string
		args     []string
		contains []string
	}{
		{name: "scalar", args: []string{"scalar", "3"}, contains: []string{"mode=scalar", "iterations=3", "workers=1", "checksum=", "total_checksum=", "seconds="}},
		{name: "parallel", args: []string{"parallel", "4", "2"}, contains: []string{"mode=parallel", "iterations=4", "workers=2", "checksum=", "total_checksum=", "seconds="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runCmd := exec.Command(exePath, tc.args...)
			runOutput, err := runCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("packed ML AST benchmark failed for %s: %v\n%s", tc.name, err, string(runOutput))
			}
			output := string(runOutput)
			for _, check := range tc.contains {
				if !strings.Contains(output, check) {
					t.Fatalf("expected packed ML AST benchmark output to contain %q, got:\n%s", check, output)
				}
			}
		})
	}
}
func TestRunCLIPackedMLASTBenchSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	exePath := buildPackedMLASTMediumNativeExecutable(t, repoRoot, "-O3")
	runPackedMLASTBenchSmoke(t, exePath)
}
func TestRunCLIPackedMLASTMegaBenchSmoke(t *testing.T) {
	requireSlowNativeMLAST(t)

	repoRoot := repoRootFromMainTest(t)
	exePath := buildPackedMLASTNativeExecutable(t, repoRoot, "-O3")
	runPackedMLASTBenchSmoke(t, exePath)
}
func TestRunCLIPackedMLASTUltraBenchSmoke(t *testing.T) {
	requireSlowNativeMLAST(t)

	repoRoot := repoRootFromMainTest(t)
	exePath := buildPackedMLASTUltraNativeExecutable(t, repoRoot, "-O3")
	runPackedMLASTBenchSmoke(t, exePath)
}
func TestRunCLIPackedMLExprReproSmoke(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	exePath := buildPackedMLExprReproExecutable(t, repoRoot, "-O0")

	runCmd := exec.Command(exePath)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("packed ML expr repro failed: %v\n%s", err, string(runOutput))
	}
	output := strings.TrimSpace(string(runOutput))
	if output == "" {
		t.Fatal("expected packed ML expr repro to print a checksum")
	}
	if _, err := strconv.ParseInt(output, 10, 64); err != nil {
		t.Fatalf("expected packed ML expr repro to print an integer checksum, got %q", output)
	}
}
func TestRunCLIJSONParserDOMBenchSmoke(t *testing.T) {
	t.Parallel()
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser.elisa")
	benchPath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_dom_bench.c")
	shimPath := filepath.Join(repoRoot, "Code", "benchmarks", "json_parser_runtime_shims.c")
	outputDir := t.TempDir()
	headerPath := filepath.Join(outputDir, "json_parser.h")
	objectPath := filepath.Join(outputDir, "json_parser.o")
	exePath := filepath.Join(outputDir, "json_parser_dom_bench")
	jsonPath := filepath.Join(outputDir, "sample.json")

	for _, args := range [][]string{
		{"-emit", "header", "-o", headerPath, fixturePath},
		// This test validates benchmark wiring and smoke behavior, not optimized code quality.
		{"-emit", "obj", "-O0", "-o", objectPath, fixturePath},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exitCode := runCLI(args, &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("runCLI(%v) returned %d\nstderr:\n%s", args, exitCode, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("expected no stdout for %v, got:\n%s", args, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("expected no stderr for %v, got:\n%s", args, stderr.String())
		}
	}

	compileArgs := []string{"-pthread", "-I", outputDir, benchPath, shimPath, objectPath, "-o", exePath}
	if runtime.GOOS == "darwin" {
		compileArgs = append([]string{"-Wl,-undefined,dynamic_lookup"}, compileArgs...)
	}
	compileCmd := exec.Command(clangPath, compileArgs...)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}

	if err := os.WriteFile(jsonPath, []byte("{\"items\":[1,2,3,{\"ok\":true}],\"meta\":{\"name\":\"Ada\",\"pi\":3.14},\"none\":null}\n"), 0o644); err != nil {
		t.Fatalf("failed to write sample json: %v", err)
	}

	for _, tc := range []struct {
		name     string
		mode     string
		contains []string
	}{
		{name: "default-parse", mode: "", contains: []string{"mode=dom-parse", "iterations=4", "parses=4", "MiB/s="}},
		{name: "build", mode: "build", contains: []string{"mode=dom-build", "iterations=4", "parses=4", "MiB/s="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{jsonPath, "4"}
			if tc.mode != "" {
				args = append(args, tc.mode)
			}
			runCmd := exec.Command(exePath, args...)
			runOutput, err := runCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("dom json benchmark failed: %v\n%s", err, string(runOutput))
			}
			output := string(runOutput)
			for _, check := range tc.contains {
				if !strings.Contains(output, check) {
					t.Fatalf("expected dom benchmark output to contain %q, got:\n%s", check, output)
				}
			}
		})
	}
}
func TestRunCLIExecutesJSONParserSelfHostedTests(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser_tests.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to execute json parser tests successfully, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] checksum_suite_matches_expected_values",
		"[ RUN      ] ast_checksum_matches_expected_values",
		"[ RUN      ] ast_and_checksum_paths_agree_on_nested_inputs",
		"[ RUN      ] invalid_inputs_are_rejected",
		"[ RUN      ] ast_raw_dom_helpers_expose_source_spans_and_structure",
		"[ RUN      ] ast_string_helpers_decode_escapes_and_match_unescaped_keys",
		"[ RUN      ] ast_number_helpers_materialize_integral_values_and_classify_edges",
		"[ RUN      ] ast_number_helpers_materialize_float_values_across_fractional_and_large_inputs",
		"[ RUN      ] ast_iterator_helpers_walk_object_fields_and_array_items",
		"[ SUMMARY  ] 9 test(s) selected",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected json parser self-hosted test output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLICompilesStage1RuntimeToLLVM(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	checks := []string{
		"define ptr @int_to_string(i64",
		"define ptr @rt_concat2(ptr",
		"%StringView = type { ptr, i64 }",
		"%FixedBufferAllocator = type { ptr, i64, i64 }",
		"define i64 @ctx_string_view_len(%StringView",
		"define %FixedBufferAllocator @fixed_buffer_allocator_init(",
		"define ptr @fixed_buffer_alloc(",
		"define void @fixed_buffer_reset(",
		"define %PackedStoreAllocResult @ctx_packed_store_alloc_result(ptr",
		"define void @ctx_packed_store_alloc_result_slow(ptr",
		"define %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_result(ptr",
		"define %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_tagged_result(ptr",
		"define void @ctx_packed_store_alloc_fixed_result_slow(ptr",
		"define void @ctx_packed_store_reserve(ptr",
		"define %PackedStoreIndexAllocResult @ctx_packed_store_alloc_fixed_tagged_index_result(ptr",
		"%DynDict__cstr_key_shape__i64 = type { ptr, i64, i64, i64, ptr }",
		"define %DynDict__cstr_key_shape__i64 @arena_dict_new__i64(",
		"define i32 @arena_dict_reserve__i64(",
		"define ptr @arena_dict_get__i64(",
		"define i32 @arena_dict_put__i64(",
		"define i1 @arena_dict_contains__i64(",
		"define i1 @arena_dict_remove__i64(",
		"define void @arena_dict_clear__i64(",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, check := range []string{
		"@rt_string_view_len = alias ",
		"@rt_string_from_view = alias ",
		"define i64 @rt_string_view_len(",
		"define ptr @rt_string_from_view(",
	} {
		if strings.Contains(output, check) {
			t.Fatalf("expected output to omit legacy string helper symbol %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLIRejectsInvalidStringEscape(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "invalid_escape.elisa")
	if err := os.WriteFile(fixturePath, []byte("def bad() -> u8&:\n    return \"oops\\q\".cast[u8&]\n"), 0o644); err != nil {
		t.Fatalf("failed to write invalid fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "invalid escape sequence \\\\q in string literal") {
		t.Fatalf("expected invalid escape diagnostic, got:\n%s", stderr.String())
	}
}
func TestRunCLIRejectsInvalidCharLiteral(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "invalid_char.elisa")
	if err := os.WriteFile(fixturePath, []byte("def bad() -> i64:\n    return '\\u0080'.i64()\n"), 0o644); err != nil {
		t.Fatalf("failed to write invalid char fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "char literal must decode to exactly one code unit") {
		t.Fatalf("expected invalid char diagnostic, got:\n%s", stderr.String())
	}
}
func TestRunCLIRejectsGenericKeyRuntimeBackedDictSugar(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "generic_key_dict_runtime_reject.elisa")
	// Integral keys are now supported; a float key (no safe value equality) is still rejected.
	src := "def arena_dict_get[K, T](m: dict[K, T]&, key: K) -> mutable T&?:\n    return null\n\ndef bad(values: dict[f64, i64], key: f64) -> mutable i64&?:\n    return values.get(key)\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write generic-key dict runtime rejection fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "runtime-backed dict keys must be cstr, an integer type, bool, or a const enum") {
		t.Fatalf("expected float-key runtime-backed dict diagnostic, got:\n%s", stderr.String())
	}
}
func TestRunCLIExecutesCharLiteralSmokeProgram(t *testing.T) {
	t.Parallel()
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "char_literals.elisa")
	outputDir := t.TempDir()
	objectPath := filepath.Join(outputDir, "char_literals.o")
	exePath := filepath.Join(outputDir, "char_literals")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "obj", "-O0", "-o", objectPath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected char literal smoke fixture to compile, stderr:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout while compiling char literal smoke fixture, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr while compiling char literal smoke fixture, got:\n%s", stderr.String())
	}

	compileArgs := []string{objectPath, "-o", exePath}
	if runtime.GOOS == "darwin" {
		compileArgs = append([]string{"-Wl,-undefined,dynamic_lookup"}, compileArgs...)
	}
	compileCmd := exec.Command(clangPath, compileArgs...)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}

	runCmd := exec.Command(exePath)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("char literal smoke program failed: %v\n%s", err, string(runOutput))
	}
	if len(runOutput) != 0 {
		t.Fatalf("expected char literal smoke program to produce no output, got:\n%s", string(runOutput))
	}
}
func TestRunCLIExecutesAllocatorPortSmokeProgram(t *testing.T) {
	t.Parallel()
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "allocator_ports.elisa")
	outputDir := t.TempDir()
	objectPath := filepath.Join(outputDir, "allocator_ports.o")
	exePath := filepath.Join(outputDir, "allocator_ports")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "obj", "-O0", "-o", objectPath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected allocator port smoke fixture to compile, stderr:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout while compiling allocator port smoke fixture, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr while compiling allocator port smoke fixture, got:\n%s", stderr.String())
	}

	compileArgs := []string{objectPath, "-o", exePath}
	if runtime.GOOS == "darwin" {
		compileArgs = append([]string{"-Wl,-undefined,dynamic_lookup"}, compileArgs...)
	}
	compileCmd := exec.Command(clangPath, compileArgs...)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}

	runCmd := exec.Command(exePath)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("allocator port smoke program failed: %v\n%s", err, string(runOutput))
	}
	if len(runOutput) != 0 {
		t.Fatalf("expected allocator port smoke program to produce no output, got:\n%s", string(runOutput))
	}
}
func TestRunCLIExecutesDequePortSmokeProgram(t *testing.T) {
	t.Parallel()
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "deque_ports.elisa")
	outputDir := t.TempDir()
	objectPath := filepath.Join(outputDir, "deque_ports.o")
	exePath := filepath.Join(outputDir, "deque_ports")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "obj", "-O0", "-o", objectPath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected deque port smoke fixture to compile, stderr:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout while compiling deque port smoke fixture, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr while compiling deque port smoke fixture, got:\n%s", stderr.String())
	}

	compileArgs := []string{objectPath, "-o", exePath}
	if runtime.GOOS == "darwin" {
		compileArgs = append([]string{"-Wl,-undefined,dynamic_lookup"}, compileArgs...)
	}
	compileCmd := exec.Command(clangPath, compileArgs...)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}

	runCmd := exec.Command(exePath)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("deque port smoke program failed: %v\n%s", err, string(runOutput))
	}
	if len(runOutput) != 0 {
		t.Fatalf("expected deque port smoke program to produce no output, got:\n%s", string(runOutput))
	}
}
