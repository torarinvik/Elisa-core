package main

import (
	"bytes"
	"elisacore/src/backend"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLICompilesJSONParserWithEnumDenseFixedOverrideByDefault(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser.elisa")

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
		"%JsonParseNodeResult = type { i32, i64 }",
		"define %JsonParseNodeResult @json_parse_value_node(ptr",
		"define %JsonParseNodeResult @json_parse_array_node(ptr",
		"define %JsonParseNodeResult @json_parse_object_node(ptr",
		"call void @ctx_packed_store_reserve(",
		"call %PackedStoreIndexAllocResult @ctx_packed_store_alloc_fixed_tagged_index_result(",
		"call i32 @ctx_packed_store_read_index_tag(ptr %packed.tag.store.state, i32 ",
		"call i64 @ctx_packed_store_read_index_word(",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{
		"call %PackedStoreIndexAllocResult @ctx_packed_store_alloc_fixed_tagged_variant_sparse_result(ptr %packed.alloc.store.arena, ptr %packed.alloc.store.state, i32 ",
		"call %PackedStoreIndexAllocResult @ctx_packed_store_alloc_tagged_variant_sparse_result(ptr %packed.alloc.store.arena, i64 ",
		"call i32 @ctx_packed_store_read_variant_sparse_tag(ptr %packed.tag.store.state, i32 ",
		"call i64 @ctx_packed_store_read_variant_sparse_word(i32 %node2, ptr %packed.payload.word.state, i64 ",
		"call i64 @ctx_packed_store_read_word(ptr %packed.payload.word.arena",
		"call i64 @ctx_packed_store_read_word(ptr %packed.common.store.arena",
		"call ptr @ctx_packed_store_decode(ptr %packed.decode.store.arena, i64",
		"call ptr @ctx_packed_store_decode_index(ptr %packed.decode.store.arena, i32",
		"call %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_tagged_result(ptr %packed.alloc.store.arena, ptr %packed.alloc.store.state, i32 ",
		"call %PackedStoreAllocResult @ctx_packed_store_alloc_result(ptr %packed.alloc.store.arena, i64 %packed.alloc.bytes, ptr %packed.alloc.store.state)",
	} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected enum-level dense-fixed lowering default to avoid %q, got:\n%s", bad, output)
		}
	}
}
func TestRunCLIPrintsPackedLoweringSummary(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "packed_info.elisa")
	src := "@packed_profile(build_heavy)\npacked enum Expr:\n    common:\n        @storage(side_table)\n        span: i64\n        @storage(inline)\n        kind: u32\n    Lit(value: i64)\n    End\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write packed info fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "packed", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"packed lowering",
		"contract: canonical-compiler-graph",
		"Expr",
		"effective abi: index-soa",
		"profile: build-heavy",
		"declared abi override: dense-fixed",
		"declared prefix override: common-only",
		"side-table common words: 1",
		"- span: i64 side_table word_offset=0 words=1",
		"- kind: u32 inline row_field=1",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected packed summary to contain %q, got:\n%s", check, output)
		}
	}
}

// The packed report heading names a type, so a nested module's enum is spelled with
// `::` as source spells it -- never the internal dotted key (`Outer.Right.Message`).
func TestRunCLIPackedReportSpellsModulePathWithColons(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "packed_module_path.elisa")
	src := "module Right:\n    packed enum Message:\n        Some(left: i64, right: i64)\nmodule Outer:\n    module Right:\n        packed enum Message:\n            Empty(other: i64)\n            Some(value: i64)\ndef main() -> i64:\n    return 0\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "packed", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"\nRight::Message\n", "\nOuter::Right::Message\n"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected packed report heading %q, got:\n%s", strings.TrimSpace(want), output)
		}
	}
	if strings.Contains(output, "Outer.Right") || strings.Contains(output, "\nRight.Message") {
		t.Fatalf("packed report printed a dotted module path:\n%s", output)
	}
}

func TestRunCLIRejectsRemovedPackedABIFlag(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "json_parser.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", "-packed-abi", "word-handle", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatal("expected removed packed ABI flag to fail")
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout output, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "-packed-abi has been removed") {
		t.Fatalf("expected removed packed ABI diagnostic on stderr, got:\n%s", stderr.String())
	}
}
func TestEffectiveOptimizationLevelDefaultsByEmitMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		emit     string
		explicit bool
		level    int
		expect   int
	}{
		{name: "llvm default raw", emit: emitLLVM, expect: 0},
		{name: "bitcode default optimized", emit: emitBitcode, expect: 3},
		{name: "object default optimized", emit: emitObject, expect: 3},
		{name: "explicit overrides default", emit: emitObject, explicit: true, level: 2, expect: 2},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			options := cliOptions{emit: test.emit}
			if test.explicit {
				options.hasOptLevel = true
				options.optLevel = backend.OptimizationLevel(test.level)
			}
			if got := int(effectiveOptimizationLevel(options)); got != test.expect {
				t.Fatalf("expected effective opt level O%d, got O%d", test.expect, got)
			}
		})
	}
}
func TestRunCLIGeneratedHeaderInteropHarness(t *testing.T) {
	t.Parallel()
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "export_vec2i.elisa")
	harnessPath := filepath.Join(repoRoot, "Code", "test_programs", "export_vec2i_generated_harness.c")
	outputDir := t.TempDir()
	headerPath := filepath.Join(outputDir, "export_vec2i.h")
	objectPath := filepath.Join(outputDir, "export_vec2i.o")
	exePath := filepath.Join(outputDir, "export_vec2i_generated_harness")

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
