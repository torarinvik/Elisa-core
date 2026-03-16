package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRootFromMainTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine test file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

func TestRunCLICompilesFixtureProgramsToLLVM(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixtures := []struct {
		name   string
		path   string
		checks []string
	}{
		{
			name: "pointer_alloc",
			path: filepath.Join(repoRoot, "Code", "test_programs", "pointer_alloc.llcontext"),
			checks: []string{
				"%Node = type { i64, ptr }",
				"declare ptr @alloc_node()",
				"declare ptr @sfree_node(ptr)",
				"define i64 @make_node_value()",
				"define void @release_node(ptr",
			},
		},
		{
			name: "shape_ops",
			path: filepath.Join(repoRoot, "Code", "test_programs", "shape_ops.llcontext"),
			checks: []string{
				"%DynArray__i32 = type { ptr, i64, i64 }",
				"declare %DynArray__i32 @resize(%DynArray__i32, i64)",
				"declare %DynArray__i32 @push(%DynArray__i32, i32)",
				"define %DynArray__i32 @grow_once(%DynArray__i32",
				"define ptr @merge_strings(ptr",
			},
		},
		{
			name: "variadic_stdio",
			path: filepath.Join(repoRoot, "Code", "test_programs", "variadic_stdio.llcontext"),
			checks: []string{
				"declare i64 @snprintf(ptr, i64, ptr, ...)",
				"define i64 @format_len(ptr",
				"define i64 @write_into(ptr %0, i64 %1, ptr %2)",
				"call i64 (ptr, i64, ptr, ...) @snprintf(",
			},
		},
		{
			name: "runtime_bridges",
			path: filepath.Join(repoRoot, "Code", "test_programs", "runtime_bridges.llcontext"),
			checks: []string{
				"declare i64 @ctx_stage0_list_len(ptr)",
				"define i64 @raw_list_len(ptr",
				"call i64 @ctx_stage0_list_len(ptr",
			},
		},
		{
			name: "pointer_casts",
			path: filepath.Join(repoRoot, "Code", "test_programs", "pointer_casts.llcontext"),
			checks: []string{
				"define i64 @ptr_bits(ptr",
				"ptrtoint ptr",
				"define ptr @bits_ptr(i64",
				"inttoptr i64",
				"define ptr @advance_raw(ptr %0, i64 %1)",
				"getelementptr i8, ptr",
			},
		},
		{
			name: "nested_access",
			path: filepath.Join(repoRoot, "Code", "test_programs", "nested_access.llcontext"),
			checks: []string{
				"declare %DynArray__i32 @make_array()",
				"declare %DynArrayView @make_array_view()",
				"declare %CtxListView @make_list_view()",
				"call %DynArray__i32 @make_array()",
				"call %DynArrayView @make_array_view()",
				"call %CtxListView @make_list_view()",
				"call %DynArrayView @arena_da_view(ptr",
				"alloca %DynArray__i32",
				"alloca %DynArrayView",
				"alloca %CtxListView",
			},
		},
		{
			name: "typed_list_views",
			path: filepath.Join(repoRoot, "Code", "test_programs", "typed_list_views.llcontext"),
			checks: []string{
				"define i32 @head_of_middle(%DynArray__i32",
				"declare %DynArrayView @arena_da_view(ptr, i64, i64)",
				"call %DynArrayView @arena_da_view(ptr",
				"define i64 @inferred_literal_head()",
				"alloca [4 x i64]",
				"getelementptr [4 x i64], ptr",
				"insertvalue %DynArrayView",
			},
		},
		{
			name: "string_view_ops",
			path: filepath.Join(repoRoot, "Code", "test_programs", "string_view_ops.llcontext"),
			checks: []string{
				"%CtxStringView = type { ptr, i64 }",
				"declare %CtxStringView @ctx_stage1rt_string_view(ptr, i64, i64)",
				"call %CtxStringView @ctx_stage1rt_string_view(ptr",
				"declare i64 @ctx_stage1rt_string_view_index(%CtxStringView, i64)",
				"declare i64 @ctx_stage1rt_strlen(ptr)",
				"declare i64 @ctx_stage1rt_string_view_eq(%CtxStringView, ptr)",
				"declare i64 @ctx_stage1rt_string_views_eq(%CtxStringView, %CtxStringView)",
			},
		},
		{
			name: "export_vec2i",
			path: filepath.Join(repoRoot, "Code", "test_programs", "export_vec2i.llcontext"),
			checks: []string{
				"define %Vec__i32 @vec_add_i32(%Vec__i32",
				"define %Vec__i32 @keep_left__Vec_i32(%Vec__i32",
				"define i64 @vec2i_add(i64",
				"define i64 @vec2i_keep_left(i64",
			},
		},
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI([]string{"-emit", "llvm", fixture.path}, &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
			}
			output := stdout.String()
			for _, check := range fixture.checks {
				if !strings.Contains(output, check) {
					t.Fatalf("expected output to contain %q, got:\n%s", check, output)
				}
			}
		})
	}
}

func TestRunCLIEmitsBitcodeAndObjectForFixtureProgram(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "pointer_alloc.llcontext")
	outputDir := t.TempDir()
	bitcodePath := filepath.Join(outputDir, "pointer_alloc.bc")
	objectPath := filepath.Join(outputDir, "pointer_alloc.o")

	tests := []struct {
		name       string
		args       []string
		outputPath string
		check      func(*testing.T, []byte)
	}{
		{
			name:       "bitcode",
			args:       []string{"-emit", "bc", "-o", bitcodePath, fixturePath},
			outputPath: bitcodePath,
			check: func(t *testing.T, data []byte) {
				t.Helper()
				if len(data) < 2 || !bytes.HasPrefix(data, []byte{'B', 'C'}) {
					t.Fatalf("expected bitcode magic prefix, got % x", data[:min(len(data), 4)])
				}
			},
		},
		{
			name:       "object",
			args:       []string{"-emit", "obj", "-o", objectPath, fixturePath},
			outputPath: objectPath,
			check: func(t *testing.T, data []byte) {
				t.Helper()
				if !looksLikeObjectFile(data) {
					t.Fatalf("expected native object file magic, got % x", data[:min(len(data), 4)])
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI(test.args, &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected binary emit mode not to write stdout, got:\n%s", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
			}
			data, err := os.ReadFile(test.outputPath)
			if err != nil {
				t.Fatalf("expected output file %s to exist: %v", test.outputPath, err)
			}
			if len(data) < 4 {
				t.Fatalf("expected non-empty output file, got %d bytes", len(data))
			}
			test.check(t, data)
		})
	}
}

func TestRunCLIEmitsHeaderForExportFixture(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "export_vec2i.llcontext")
	outputPath := filepath.Join(t.TempDir(), "export_vec2i.h")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "header", "-o", outputPath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected header emit with -o not to write stdout, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("expected header output file %s to exist: %v", outputPath, err)
	}
	header := string(data)
	checks := []string{
		"typedef struct Vec2i Vec2i;",
		"struct Vec2i {",
		"int32_t x;",
		"int32_t y;",
		"extern int32_t ctx_seed;",
		"Vec2i vec2i_add(Vec2i arg0, Vec2i arg1);",
	}
	for _, check := range checks {
		if !strings.Contains(header, check) {
			t.Fatalf("expected header to contain %q, got:\n%s", check, header)
		}
	}
}

func TestRunCLIGeneratedHeaderInteropHarness(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "export_vec2i.llcontext")
	harnessPath := filepath.Join(repoRoot, "Code", "test_programs", "export_vec2i_generated_harness.c")
	outputDir := t.TempDir()
	headerPath := filepath.Join(outputDir, "export_vec2i.h")
	objectPath := filepath.Join(outputDir, "export_vec2i.o")
	exePath := filepath.Join(outputDir, "export_vec2i_generated_harness")

	for _, args := range [][]string{
		{"-emit", "header", "-o", headerPath, fixturePath},
		{"-emit", "obj", "-o", objectPath, fixturePath},
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

	compileCmd := exec.Command(clangPath, "-I", outputDir, harnessPath, objectPath, "-o", exePath)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}
	runCmd := exec.Command(exePath)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated-header interop harness failed: %v\n%s", err, string(runOutput))
	}
}

func TestRunCLICompilesStage1RuntimeToLLVM(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "contextlang_runtime.llcontext")

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
		"define ptr @ctx_stage0_int_to_string(i64",
		"define ptr @ctx_stage1rt_concat2(ptr",
		"define ptr @ctx_stage1rt_string_builder_new(ptr",
		"define i64 @ctx_stage1rt_list_len(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func looksLikeObjectFile(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	magics := [][]byte{
		{0x7f, 'E', 'L', 'F'},
		{0xcf, 0xfa, 0xed, 0xfe},
		{0xce, 0xfa, 0xed, 0xfe},
		{0xfe, 0xed, 0xfa, 0xcf},
		{0xfe, 0xed, 0xfa, 0xce},
	}
	for _, magic := range magics {
		if bytes.Equal(data[:4], magic) {
			return true
		}
	}
	return false
}

func min(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
